// Package canaryleak is the security check family for sensitive data reaching a
// response it should not: a secret rendered into a page or a bundle, and
// personal data surfaced where it does not belong.
//
// It is a READER family. It drives nothing: the run already rendered the pages
// and captured the responses against the sanitized twin, and this family reads
// that evidence back through the security Input and looks for two things in it.
//
//   - A planted canary. When the golden carries canaries, a canary is a unique
//     value planted in the twin that must never appear in output. An exact
//     substring match is a leak with no false positive at all, because the value
//     is one nobody could have typed by chance. The canary's kind says whether
//     the leak is a secret or personal data. The golden carries no canaries yet,
//     which the family reads as "nothing planted", never as "found none"; the
//     path is wired and tested and becomes live the day a seeder plants them.
//
//   - A secret or a piece of personal data by SHAPE. This is the path that
//     catches an UNSEEDED leak, the one the canary could not have been planted
//     for: a Stripe secret key compiled into a client bundle, a PEM private key
//     in a response, a social security number rendered into a page. It reuses
//     the verify package's detectors, the same lexicon that reads a masked
//     database back, so "a secret" and "personal data" mean the same thing here
//     as they do there. This path is effective today, against the evidence the
//     run already captured.
//
// The iron rule of the verify package is inherited whole: a finding NEVER
// carries the value. It names the stream it was seen in and the detector or
// canary kind that recognised it, and stops. The DOM and the response bodies
// stay inside the engine, against a copy of production, and reach no finding, no
// pull request comment and no log.
//
// It imports nothing under ee. Sensitive-data leak detection is in every
// edition, so Licensed is the empty string.
package canaryleak

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// Name is the family id and the namespace of every key and finding rule it
// owns: the keys are "security.canary_leak.<rule>".
const Name = "canary_leak"

const (
	// KeySecret is a secret reaching a response: a credential, a private key,
	// or a third party object identifier that points at a real account.
	KeySecret report.PolicyKey = "security.canary_leak.secret_in_response"
	// KeyPII is personal data reaching a response: a national id or a bank
	// account number, by shape, or a planted personal-data canary.
	KeyPII report.PolicyKey = "security.canary_leak.pii_in_response"
)

// Kind labels a planted canary so a match routes to the right key. A seeder
// plants a canary with one of these kinds; the family reads it back and reports
// a secret leak or a personal-data leak accordingly.
const (
	KindSecret = "secret"
	KindPII    = "pii"
)

// family is the security.Family implementation. It holds no state: the detector
// set is a constant of the verify package and the canaries arrive on the Input.
type family struct{}

// New returns the sensitive-data leak family.
func New() security.Family { return family{} }

// Name identifies the family.
func (family) Name() string { return Name }

// Surfaces are the change surfaces that can start a leak. A code change can
// begin returning a column it withheld, a schema change can add or expose one,
// and a masking-rule change can stop masking a value that then reaches a
// response. The router runs the family when the diff touches one of these.
func (family) Surfaces() []change.Surface {
	return []change.Surface{change.SurfaceCode, change.SurfaceSchema, change.SurfaceMasking}
}

// Checks is the canary-leak check the family contributes to the plan and report.
func (family) Checks() []change.Check { return []change.Check{change.CheckCanaryLeak} }

// Licensed is empty: leak detection is in every edition.
func (family) Licensed() string { return "" }

// Keys declares the two policy keys the family can write. A secret in a
// response defaults to fail: a private key or a live provider credential
// rendered where a client can read it is a real escape, and a project that has
// a reason to allow one says so in its manifest. Personal data by shape
// defaults to warn: a shape is evidence rather than proof, and a base twin to
// prove the value is NEW in this change is not built yet, so the finding is
// reported without stopping the merge until a project raises it.
//
// The exit each key produces is read from the spine's ExitFor rather than
// chosen here, so a family cannot declare an exit the gate would not honour.
// Both are verification failures (exit 7): this family proves a leak by reading
// the output the run produced, rather than refusing a configuration.
func (family) Keys() []security.KeySpec {
	return []security.KeySpec{
		{
			Key:     KeySecret,
			Default: report.LevelFail,
			Title:   "A secret reached a response the run rendered against the twin.",
			Docs:    "concepts/security",
			Exit:    security.ExitFor(KeySecret),
		},
		{
			Key:     KeyPII,
			Default: report.LevelWarn,
			Title:   "Personal data reached a response the run rendered against the twin.",
			Docs:    "concepts/security",
			Exit:    security.ExitFor(KeyPII),
		},
	}
}

// stream is one named evidence stream the family scans, so a finding can say
// where a value was seen without carrying the value.
type stream struct {
	name string
	text string
}

// secretDetectors and piiDetectors are the verify detectors each key scans for.
// They are named rather than taken wholesale because the two keys mean
// different things and because the noisier shapes, an email or a phone number
// that a page legitimately shows its own user, are deliberately left to the
// canary path where an exact planted value carries no false positive. What is
// scanned by shape is only what is almost never legitimate in a response: a
// credential, a private key, a live provider object id, a national id, a bank
// account number.
var secretDetectors = map[string]bool{
	"private-key": true, "credential": true, "provider-identifier": true,
}

var piiDetectors = map[string]bool{
	"national-id": true, "iban": true,
}

// Probe reads the run's evidence and reports a leak it finds in it. It drives
// nothing and returns no error for absent evidence: a run that rendered no
// pages hands the family empty streams, which is "nothing to scan", not a
// blocked probe. The level of every finding comes from the manifest through
// in.Policy, never from this family.
func (f family) Probe(_ context.Context, in security.Input) ([]report.Finding, error) {
	streams := evidenceStreams(in.Evidence())
	if len(streams) == 0 {
		return nil, nil
	}

	secretLevel := in.Policy.Level(KeySecret)
	piiLevel := in.Policy.Level(KeyPII)

	// Deduplicate: one finding per (key, stream, cause), because the same
	// detector firing on twenty pages of one bundle is one leak to fix, and a
	// finding per page would bury it. The cause is the detector name or the
	// canary kind, never the value.
	seen := map[string]bool{}
	var out []report.Finding
	emit := func(key report.PolicyKey, level report.Level, streamName, cause, detail, fix string) {
		if level == report.LevelIgnore {
			return
		}
		dedupe := string(key) + "\x00" + streamName + "\x00" + cause
		if seen[dedupe] {
			return
		}
		seen[dedupe] = true
		out = append(out, report.Finding{
			Rule: string(key), Level: level, Where: streamName,
			Title: titleFor(key), Detail: detail, Fix: fix,
		})
	}

	canaries := in.Golden.Canaries()
	for _, s := range streams {
		// The canary path first: an exact planted value is a definite leak and
		// says which kind without a detector. It is wired for the day a seeder
		// plants canaries; today the golden carries none and this loop does
		// nothing, which is the honest absent state and not a finding.
		for _, c := range canaries {
			if c.Value == "" || !strings.Contains(s.text, c.Value) {
				continue
			}
			switch c.Kind {
			case KindSecret:
				emit(KeySecret, secretLevel, s.name, "canary:secret",
					"A value planted in the twin as a secret was found in "+s.name+
						", so this response returns a secret it should never carry.",
					"Find where "+s.name+" renders the value and stop it reaching the client. "+
						"The value itself is not printed here; it lives in the copy of production.")
			case KindPII:
				emit(KeyPII, piiLevel, s.name, "canary:pii",
					"A value planted in the twin as personal data was found in "+s.name+".",
					"Find where "+s.name+" renders the value and remove it or mask it before it reaches the client.")
			}
		}

		// The shape path: what the canary could not have been planted for. Each
		// selected detector is asked whether the stream contains its shape. The
		// verify detectors for these kinds search within the text, so a whole
		// rendered page or bundle can be handed to them directly.
		for _, d := range verify.Detectors() {
			if !d.Match(s.text) {
				continue
			}
			switch {
			case secretDetectors[d.Name]:
				emit(KeySecret, secretLevel, s.name, "shape:"+d.Name,
					"A value shaped like "+d.Describe+" was found in "+s.name+
						", rendered against the sanitized twin. A secret that reaches a "+
						"response is readable by whoever reads the response.",
					"Find where "+s.name+" emits the value and stop it. If it is a build "+
						"time secret compiled into a bundle, move it to the server. The value "+
						"is not printed here.")
			case piiDetectors[d.Name]:
				emit(KeyPII, piiLevel, s.name, "shape:"+d.Name,
					"A value shaped like "+d.Describe+" was found in "+s.name+
						", rendered against the sanitized twin.",
					"Confirm this response is meant to carry it. If not, remove or mask the "+
						"value before it reaches the client. The value is not printed here.")
			}
		}
	}
	return out, nil
}

// evidenceStreams names the DOM and response streams so a finding can locate a
// leak. The values are read here and never leave: only the names travel.
func evidenceStreams(e security.Evidence) []stream {
	var streams []stream
	for i, dom := range e.DOM {
		if dom == "" {
			continue
		}
		streams = append(streams, stream{name: fmt.Sprintf("a rendered page (#%d)", i+1), text: dom})
	}
	for i, body := range e.Responses {
		if body == "" {
			continue
		}
		streams = append(streams, stream{name: fmt.Sprintf("a response body (#%d)", i+1), text: body})
	}
	return streams
}

func titleFor(key report.PolicyKey) string {
	if key == KeySecret {
		return "A secret reached a response the run rendered against the twin."
	}
	return "Personal data reached a response the run rendered against the twin."
}

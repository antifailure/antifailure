// Package auditsink forwards the engine's audit entries somewhere a security
// team already looks.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// This package is the second half of a hole. `extension.AuditSink`,
// `Registry.AddAuditSink` and `Registry.Audit` were written, documented and
// tested, and on the night this was measured `Registry.Audit` had exactly two
// call sites in the entire repository and both were in its own test file. There
// were no implementations of the interface anywhere. So `audit_stream` was a
// feature a licence granted, a document described, and nothing in any build
// could deliver: the socket existed and nothing was ever plugged into it. The
// engine now calls the socket, and this is what answers.
//
// Three shapes, because those are the three an enterprise already has somewhere
// to receive:
//
//   - syslog over TLS, which is what every SIEM ingests natively and what a
//     compliance auditor expects to be told;
//   - an HTTPS webhook, which is what a modern SIEM and every incident tool
//     take, with retry and a dead letter file so an outage loses nothing;
//   - a drop into an object store, matching the s3 and azure_blob goldens
//     stores this product already speaks, which is what a data lake ingests and
//     what a seven year retention requirement is usually satisfied by.
//
// # The contract, kept exactly
//
// The interface it implements says a sink observes and cannot alter, and that
// an error from one is recorded without stopping the lifecycle. Both halves are
// load bearing rather than stylistic:
//
//   - Nothing here returns a value the engine reads as a decision. A sink
//     cannot refuse an environment, cannot change an entry, and cannot see the
//     entries another sink received.
//   - A failure is returned as an error and the engine reports it and carries
//     on. That matters most on teardown: a SIEM outage that stopped an
//     environment being destroyed would turn a logging problem into a resource
//     leak, which is strictly worse than the problem it came from. There is a
//     test for exactly that, against a sink that is unreachable.
//
// # The licence gate is per call, not per registration
//
// Every Write begins by asking whether audit_stream is enabled, for the same
// reason policyenforce.Hook.Check does: a licence can expire while the process
// is running, and a sink gated at registration would keep forwarding an
// organization's audit stream to a destination they have stopped paying for,
// with no way to stop it short of a restart.
//
// # What is deliberately not here
//
// No buffering across process lifetimes beyond the webhook's dead letter file,
// and no background flusher. The engine is a command that runs and exits, often
// inside a CI job that is torn down the instant it returns, so a queue drained
// by a goroutine is a queue that is discarded on exit. Writing synchronously
// and bounding the wait is the honest shape for this process model, and the
// bound is why a slow SIEM cannot hold an `af down` open.
package auditsink

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

func init() {
	// Recorded so that a feature which is sold and never checked shows up as
	// such. Before this file, audit_stream was exactly that: feature.Sites for
	// it was empty, because there was no implementation of the interface
	// anywhere to check anything.
	//
	// One site rather than one per sink, and it names the file holding the
	// check rather than the three Write methods that consult it. That is the
	// honest answer to "where is this enforced": all three sinks are gated by
	// the same line, and three names pointing at one line would read as three
	// independent controls.
	feature.Declare(license.FeatureAuditStream, AuditStreamSite)
}

// AuditStreamSite is where audit_stream is enforced, in the form the licence
// catalogue and the feature registry both name a site: the path from ee/engine
// to the file holding the check, then the symbol that makes it.
//
// A constant rather than two string literals, because the whole purpose of the
// registry is that the page a customer reads and the code that enforces cannot
// name two different places, and two literals is exactly how they would come
// to differ.
const AuditStreamSite = "auditsink/auditsink.go:auditsink.permitted"

// permitted reports whether this installation may forward, right now.
//
// One function rather than the same line in three Write methods, so that the
// reasoning lives in one place and a fourth sink cannot be written without it.
// It is deliberately not an error: an unlicensed installation that configured a
// sink is not broken, it is unlicensed, and the entry is still in the engine's
// own output. Turning that into a failure on every action would make an expired
// licence look like a defect in whatever the customer was doing at the time.
func permitted(ctx context.Context) bool {
	return feature.Enabled(ctx, license.FeatureAuditStream)
}

// record is one entry as it is written, in a fixed field order.
//
// A struct rather than the map the entry carries, because JSON from a map has
// no defined key order and an audit stream whose fields move between lines is
// one that neither diffs nor compresses and that a hand written SIEM parser
// gets wrong exactly once. Everything a reader needs to correlate is at the
// front and the free form detail is last.
type record struct {
	// OccurredAt is when the action happened, from the engine's clock. Absent
	// when the producer did not say, rather than filled in from this process's
	// clock: see extension.AuditEntry.OccurredAt for why a guessed timestamp in
	// an audit log is worse than a missing one.
	OccurredAt string `json:"occurred_at,omitempty"`
	// ForwardedAt is when this sink wrote it, which is a different instant and
	// is the one that answers "how far behind is my SIEM".
	ForwardedAt string         `json:"forwarded_at"`
	Org         string         `json:"org,omitempty"`
	Actor       string         `json:"actor,omitempty"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type,omitempty"`
	TargetID    string         `json:"target_id,omitempty"`
	Origin      string         `json:"origin,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// encode renders one entry as a single line of JSON with no trailing newline.
//
// One line, because every destination here is line oriented: syslog frames one
// message, a webhook posts one document, and an object store drop is read back
// by tools that expect JSON lines. A pretty printed entry would be four of
// those things in one and correct for none.
//
// now is injected so that a test can assert on the whole line rather than on
// the parts of it that do not move.
func encode(entry extension.AuditEntry, now time.Time) ([]byte, error) {
	r := record{
		ForwardedAt: now.UTC().Format(time.RFC3339Nano),
		Org:         entry.Org,
		Actor:       entry.Actor,
		Action:      entry.Action,
		TargetType:  entry.TargetType,
		TargetID:    entry.TargetID,
		Origin:      entry.Origin,
		Detail:      entry.Detail,
	}
	if !entry.OccurredAt.IsZero() {
		r.OccurredAt = entry.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(r)
	if err != nil {
		// Reachable: Detail is map[string]any and a caller can put a channel or
		// a function in it. Named rather than swallowed, because an entry that
		// cannot be encoded is an entry that is not going to reach anybody and
		// the engine reports the problem through progress.
		return nil, fmt.Errorf("the audit entry for %s cannot be encoded: %w", entry.Action, err)
	}
	return body, nil
}

// clockOf returns the clock a sink should use, defaulting to the wall clock.
//
// Every sink takes an injectable clock for the same reason: the forwarded_at
// field and the object key are both derived from it, and a test that could not
// fix them would have to match them with a regular expression, which is a test
// that passes when the field is missing.
func clockOf(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}
	return now
}

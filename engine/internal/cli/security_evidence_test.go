package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/canaryleak"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// A live Stripe secret key shape: the prefix the verify credential detector
// knows, followed by enough entropy for it to read as a credential rather than a
// word. Split so this test file does not itself carry a string a secret scanner
// matches, the same care canaryleak_test takes.
const runnerSecret = "sk" + "_live_" + "0123456789abcdefghijABCDEF"

// scanEvidence runs the real canary_leak family over the evidence a run
// captured, with a policy that makes a secret reportable, the way the collector
// hands it. It returns the finding rules so a test asserts the seam fired.
func scanEvidence(t *testing.T, run *report.Run) []report.Finding {
	t.Helper()
	fam := canaryleak.New()
	in := security.Input{
		Policy: report.Policy{Security: map[report.PolicyKey]report.Level{
			canaryleak.KeySecret: report.LevelFail,
			canaryleak.KeyPII:    report.LevelWarn,
		}},
		Golden: security.NewGoldenView(nil),
	}.WithRunArtifacts(security.RunArtifacts{Evidence: explorationEvidence(run.Exploration)})
	findings, err := fam.Probe(context.Background(), in)
	require.NoError(t, err)
	return findings
}

// TestExplorationEvidence_NilAndEmpty proves the boundary of the fold: nothing
// to fold is empty evidence, which the family reads as "not handed evidence"
// rather than "found none", so a run that explored nothing never reads as a
// clean secret scan by accident.
func TestExplorationEvidence_NilAndEmpty(t *testing.T) {
	require.Equal(t, security.Evidence{}, explorationEvidence(nil), "no exploration folds to empty")
	ev := explorationEvidence(&report.Exploration{})
	require.Empty(t, ev.DOM, "an exploration that captured nothing has no DOM")
	require.Empty(t, ev.Responses, "an exploration that captured nothing has no responses")
}

// TestExplorationEvidence_CanaryLeakFiresOnACapturedResponse is the end-to-end
// proof of the capture seam: a response body the runner recorded against the
// twin, carrying a secret by shape, is folded into the evidence canary_leak
// scans, and the family fires. This is the producer (runner evidence.responses)
// -> explorationEvidence -> consumer (canary_leak shape) path, dead before the
// runner emitted a body, since the stream it scans was always empty.
func TestExplorationEvidence_CanaryLeakFiresOnACapturedResponse(t *testing.T) {
	var x explore.Exploration
	// The shape the runner now emits: a same-origin response body with a secret
	// compiled into it, exactly as a client bundle leaks a build-time key.
	x.Evidence.Responses = []string{"window.__STRIPE_KEY='" + runnerSecret + "';"}
	run := &report.Run{Exploration: &report.Exploration{Results: []explore.Exploration{x}}}

	findings := scanEvidence(t, run)
	require.NotEmpty(t, findings, "the captured response fed canary_leak's shape detector and it fired")
	require.Equal(t, string(canaryleak.KeySecret), findings[0].Rule, "the leak is reported as a secret")
	require.Equal(t, report.LevelFail, findings[0].Level)
	// The value stays inside the engine: no field of the finding carries it.
	for _, f := range findings {
		require.NotContains(t, f.Detail+f.Title+f.Where+f.Fix, runnerSecret,
			"the captured secret never crosses into a finding")
	}
}

// TestExplorationEvidence_CanaryLeakFiresOnACapturedDOM proves the DOM stream is
// folded too: a private key rendered into a page the runner captured is caught.
func TestExplorationEvidence_CanaryLeakFiresOnACapturedDOM(t *testing.T) {
	var x explore.Exploration
	x.Evidence.DOM = []string{"-----BEGIN RSA PRIVATE KEY-----\nMIIfake\n-----END RSA PRIVATE KEY-----"}
	run := &report.Run{Exploration: &report.Exploration{Results: []explore.Exploration{x}}}

	findings := scanEvidence(t, run)
	require.NotEmpty(t, findings, "the captured DOM fed canary_leak's shape detector and it fired")
	require.Equal(t, string(canaryleak.KeySecret), findings[0].Rule)
}

// TestExplorationEvidence_OrdinaryCaptureIsClean is the liveness arm: the same
// wiring over a captured body and DOM with nothing sensitive produces no
// finding, so the seam can say no and is not a check an operator would turn off.
func TestExplorationEvidence_OrdinaryCaptureIsClean(t *testing.T) {
	var x explore.Exploration
	x.Evidence.Responses = []string{`{"status":"ok","count":3}`}
	x.Evidence.DOM = []string{"<html><body><h1>Orders</h1><p>Your order shipped.</p></body></html>"}
	run := &report.Run{Exploration: &report.Exploration{Results: []explore.Exploration{x}}}

	require.Empty(t, scanEvidence(t, run), "an ordinary captured page proves nothing")
}

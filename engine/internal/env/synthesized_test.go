package env

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// The promise: a workflow that touches a response a model invented reports
// unverified rather than passed. It was made in five places and kept in none.
//
// The sidecar has always written `synthesized` on the decision and set an
// X-Antifailure-Synthesized header. local.Decision had no field for it, so
// json.Unmarshal dropped it, and the runner's mapping from
// `synthesized-response` to unverified had its only producer firing on a
// completely different condition: a page nobody could read. A billing workflow
// whose Stripe call was invented by a model reported PASSED.
//
// Watched. Before the field existed, the decode test below printed
// `synthesized` gone from a line that carried it, and the attribution tests
// did not compile because nothing on this side knew the word.

// The line the sidecar writes for a synthesized response, unedited in shape.
const synthLine = `{"event":"decision","at":"2026-09-01T09:00:30Z","method":"POST",` +
	`"host":"api.stripe.com","port":443,"path":"/v1/charges","tls":true,"mode":"synth",` +
	`"rule":"stripe","allowed":true,"status":200,"bytes":312,"duration":"120ms","seq":7,` +
	`"synthesized":true}`

// The boundary the fact used to die at. A decode that silently drops a field
// is invisible from both sides: the proxy is right, the consumer is right, and
// the feature does not exist.
func TestDecision_CarriesSynthesizedAcrossTheDecode(t *testing.T) {
	t.Parallel()
	var d local.Decision
	require.NoError(t, json.Unmarshal([]byte(synthLine), &d))
	require.True(t, d.Synthesized,
		"the sidecar wrote synthesized:true and the engine has to be able to read it")
	require.Equal(t, "api.stripe.com", d.Host)
	require.Equal(t, "synth", d.Mode)
}

// Pack and Fixture were dropped the same way, against the sidecar's own
// comment that a mock which cannot name its fixture is a mock nobody can
// debug.
func TestDecision_CarriesTheFixtureThatAnsweredAMock(t *testing.T) {
	t.Parallel()
	var d local.Decision
	require.NoError(t, json.Unmarshal([]byte(
		`{"event":"decision","host":"api.stripe.com","mode":"mock","allowed":true,`+
			`"pack":"stripe","fixture":"POST /v1/charges"}`), &d))
	require.Equal(t, "stripe", d.Pack)
	require.Equal(t, "POST /v1/charges", d.Fixture)
}

func result(name, verdict, from, to string) WorkflowResult {
	var r WorkflowResult
	r.Workflow = name
	r.Outcome.Verdict = verdict
	r.Outcome.Cause = "succeeded"
	r.Outcome.Detail = "Every expectation is visible on the page."
	r.Outcome.Reproduction = []string{"a step somebody could follow"}
	r.StartedAt, r.FinishedAt = from, to
	return r
}

func decision(host, at string, synthesized bool) local.Decision {
	return local.Decision{
		Event: "decision", AtRaw: at, Host: host, Mode: "synth",
		Allowed: true, Status: 200, Synthesized: synthesized,
	}
}

// The headline case, and the one the product is sold on.
func TestAttributeSynthesized_APassingWorkflowBecomesUnverified(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Passed: 1,
		Results: []WorkflowResult{
			result("billing", "pass", "2026-09-01T09:00:00Z", "2026-09-01T09:01:00Z"),
		},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.stripe.com", "2026-09-01T09:00:30Z", true),
	})

	require.Equal(t, "unverified", report.Results[0].Outcome.Verdict)
	require.Equal(t, "synthesized-response", report.Results[0].Outcome.Cause)
	require.Contains(t, report.Results[0].Outcome.Detail, "api.stripe.com")
	require.Equal(t, 0, report.Passed, "the counts have to agree with the verdicts")
	require.Equal(t, 1, report.Unverified)
	require.Empty(t, report.Results[0].Outcome.Reproduction,
		"there is nothing to reproduce; the workflow did what it was asked")
}

// Attribution is by window, so a workflow that ran before or after the
// invented call keeps its verdict. Without this the feature would be "any
// synth call anywhere makes the whole run unverified", which is a different
// and much worse product.
func TestAttributeSynthesized_OnlyTheWorkflowThatTouchedIt(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Passed: 2,
		Results: []WorkflowResult{
			result("signup", "pass", "2026-09-01T09:00:00Z", "2026-09-01T09:00:20Z"),
			result("billing", "pass", "2026-09-01T09:00:20Z", "2026-09-01T09:01:00Z"),
		},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.stripe.com", "2026-09-01T09:00:45Z", true),
	})

	require.Equal(t, "pass", report.Results[0].Outcome.Verdict, "signup never touched it")
	require.Equal(t, "unverified", report.Results[1].Outcome.Verdict)
	require.Equal(t, 1, report.Passed)
	require.Equal(t, 1, report.Unverified)
}

// A decision that is not synthesized changes nothing, so the test above is not
// passing because every decision downgrades a workflow.
func TestAttributeSynthesized_AnOrdinaryCallChangesNothing(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Passed: 1,
		Results: []WorkflowResult{
			result("billing", "pass", "2026-09-01T09:00:00Z", "2026-09-01T09:01:00Z"),
		},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.stripe.com", "2026-09-01T09:00:30Z", false),
	})
	require.Equal(t, "pass", report.Results[0].Outcome.Verdict)
	require.Equal(t, 1, report.Passed)
	require.Empty(t, report.Notes)
}

// A failure stays a failure. The application did the wrong thing with an
// invented answer, and calling that unverified would hide a real defect behind
// our own escape hatch.
func TestAttributeSynthesized_AFailureIsNotDowngraded(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Failed: 1,
		Results: []WorkflowResult{
			result("billing", "fail", "2026-09-01T09:00:00Z", "2026-09-01T09:01:00Z"),
		},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.stripe.com", "2026-09-01T09:00:30Z", true),
	})
	require.Equal(t, "fail", report.Results[0].Outcome.Verdict)
	require.Equal(t, 1, report.Failed)
	require.Zero(t, report.Unverified)
}

// A synthesized call inside no workflow's window is said out loud rather than
// pinned on whichever workflow was nearest. A confident wrong attribution is
// worse than an honest note.
func TestAttributeSynthesized_SaysWhatItCouldNotAttribute(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Passed: 1,
		Results: []WorkflowResult{
			result("signup", "pass", "2026-09-01T09:00:00Z", "2026-09-01T09:00:20Z"),
		},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.sendgrid.com", "2026-09-01T09:05:00Z", true),
	})
	require.Equal(t, "pass", report.Results[0].Outcome.Verdict)
	require.Len(t, report.Notes, 1)
	require.Contains(t, report.Notes[0], "api.sendgrid.com")
	require.Contains(t, report.Notes[0], "outside any workflow's run")
}

// A runner that sends no window gets nothing attributed to it, rather than
// everything. Somebody driving the runner by hand, or an older one, must not
// have every synthesized call in the log charged to every workflow.
func TestAttributeSynthesized_AWorkflowWithNoWindowIsSkipped(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Passed:  1,
		Results: []WorkflowResult{result("billing", "pass", "", "")},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.stripe.com", "2026-09-01T09:00:30Z", true),
	})
	require.Equal(t, "pass", report.Results[0].Outcome.Verdict)
	require.Len(t, report.Notes, 1, "and it is reported rather than silently dropped")
}

// Several hosts in one workflow are named, both of them, because "a response
// was invented" without saying by whom sends somebody to read the whole log.
func TestAttributeSynthesized_NamesEveryHost(t *testing.T) {
	t.Parallel()
	report := &TestReport{
		Passed: 1,
		Results: []WorkflowResult{
			result("checkout", "pass", "2026-09-01T09:00:00Z", "2026-09-01T09:01:00Z"),
		},
	}
	attributeSynthesized(report, []local.Decision{
		decision("api.stripe.com", "2026-09-01T09:00:10Z", true),
		decision("api.twilio.com", "2026-09-01T09:00:20Z", true),
		decision("api.stripe.com", "2026-09-01T09:00:30Z", true),
	})
	detail := report.Results[0].Outcome.Detail
	require.Contains(t, detail, "api.stripe.com")
	require.Contains(t, detail, "api.twilio.com")
	require.Contains(t, detail, "those hosts")
	require.Equal(t, 1, report.Unverified, "one workflow, however many calls")
}

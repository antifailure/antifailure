package report_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// The report is the surface a person reads, and until now it could say "4
// allowed" without being able to say whether those four carried a sandbox
// credential or a live one. These assert the rendered Markdown rather than the
// struct, because a field on Egress that no renderer prints is a capability
// that exists and does nothing, which is the shape this project keeps finding.

func TestMarkdown_NamesAnUnsubstitutedSandboxCall(t *testing.T) {
	t.Parallel()
	run := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Egress: &report.Egress{
			Allowed: 4, Sandbox: 4, Substituted: 2, Unsubstituted: 2,
			UnsubstitutedHosts: []string{"api.stripe.com", "api.twilio.com"},
		},
	}

	out := run.Markdown()
	require.Contains(t, out, "WITHOUT the credential being replaced",
		"the report must say plainly that containment did not happen")
	require.Contains(t, out, "api.stripe.com")
	require.Contains(t, out, "api.twilio.com")
	require.Contains(t, out, "2 requests")
	require.Contains(t, out, "2 of 4 calls had the credential replaced")
}

func TestMarkdown_SaysSoWhenEverySandboxCallWasSubstituted(t *testing.T) {
	t.Parallel()
	// Stated either way. A line that only ever appears when something is
	// wrong teaches a reader nothing by its absence, and "the sandbox worked"
	// is the fact somebody is looking for when they read this section.
	run := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Egress:    &report.Egress{Allowed: 3, Sandbox: 3, Substituted: 3},
	}

	out := run.Markdown()
	require.Contains(t, out, "3 of 3 calls had the credential replaced")
	require.NotContains(t, out, "WITHOUT the credential being replaced")
}

func TestMarkdown_SaysNothingAboutSandboxWhenThereWereNoSandboxCalls(t *testing.T) {
	t.Parallel()
	// A project with no sandbox rules must not get a line about sandbox
	// credentials, which would read as a finding about something it does not
	// do.
	run := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Egress:    &report.Egress{Allowed: 2, Refused: 1},
	}

	out := run.Markdown()
	require.NotContains(t, out, "had the credential replaced")
	require.Contains(t, out, "Outbound: 2 allowed, 1 refused")
}

func TestMarkdown_TheSingularCaseReadsAsEnglish(t *testing.T) {
	t.Parallel()
	// One request is the case a reader is most likely to meet first, and it
	// is the half that usually has no test.
	run := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Egress: &report.Egress{
			Allowed: 1, Sandbox: 1, Unsubstituted: 1,
			UnsubstitutedHosts: []string{"api.stripe.com"},
		},
	}

	out := run.Markdown()
	require.Contains(t, out, "1 request left under a sandbox rule")
	require.NotContains(t, out, "1 requests")
	require.False(t, strings.Contains(out, "requests left"),
		"the singular must not render the plural noun")
}

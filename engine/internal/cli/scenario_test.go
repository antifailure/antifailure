package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// What a person actually sees, and what the process actually exits with.
//
// The exit code matters more than it looks. A scenario that could not run has
// found nothing wrong with the change, so it is blocked rather than failed,
// but a check that ran nothing and exited zero is a check everybody believes
// is running. Both of those have to be true at once, and only a test can hold
// them together.

func render(t *testing.T, results []load.ScenarioResult) string {
	t.Helper()
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	for _, r := range results {
		renderScenario(e, r)
	}
	return buf.String()
}

func TestRenderScenario_ShowsTheAnswerThenTheEvidence(t *testing.T) {
	t.Parallel()
	out := render(t, []load.ScenarioResult{{
		Scenario: "impatient_upgrade", Description: "A returning customer resubmits.",
		Verdict: load.VerdictFail, Detail: "GET /api/subscriptions served a p95 of 900ms, over 800ms",
		Sessions: 50, Iterations: 4, Sent: 800,
		ScheduledMs: 4000, DurationMs: 9500,
		Overall: load.Latency{P50Ms: 40, P95Ms: 900, P99Ms: 1400},
		Steps: []load.RouteResult{
			{Route: "GET /api/subscriptions", Sent: 400, Latency: load.Latency{P95Ms: 900}, Errors: 3},
			{Route: "GET /settings/billing", Sent: 400, Latency: load.Latency{P95Ms: 40}},
		},
		Assertions: []load.AssertionResult{
			{Name: "billing_stayed_fast", Verdict: load.VerdictFail,
				Detail: "GET /api/subscriptions served a p95 of 900ms, over 800ms"},
			{Name: "every_request_answered", Verdict: load.VerdictPass, Detail: "all 800 requests answered below 400"},
		},
	}})

	require.Contains(t, out, "impatient_upgrade")
	require.Contains(t, out, "failed")
	require.Contains(t, out, "A returning customer resubmits.")
	require.Contains(t, out, "50 sessions x 4 iterations, 800 requests")
	require.Contains(t, out, "p95   900ms", "the step table lines up so the slow one is obvious")
	// The schedule against the clock. A run that took much longer than the
	// plan asked for is the finding a load test exists to produce, and a
	// latency percentile on its own does not say it.
	require.Contains(t, out, "the schedule asked for 4.0s and the run took 9.5s")
	require.Contains(t, out, "GET /settings/billing")
	require.Contains(t, out, "billing_stayed_fast")
	require.Contains(t, out, "every_request_answered")

	// The failing assertion comes with what it measured, not just its name.
	require.Contains(t, out, "over 800ms")
}

func TestRenderScenario_ABlockedScenarioNamesTheRoutesNobodyAllowed(t *testing.T) {
	t.Parallel()
	out := render(t, []load.ScenarioResult{{
		Scenario: "upgrade", Verdict: load.VerdictBlocked,
		Detail:  "it did not run: 1 request is not named in safe_routes",
		Refused: []string{"POST /billing/upgrade"},
		Assertions: []load.AssertionResult{
			{Name: "it_worked", Verdict: load.VerdictBlocked, Detail: "it did not run"},
		},
	}})
	require.Contains(t, out, "blocked")
	require.Contains(t, out, "not named in safe_routes: POST /billing/upgrade")
	require.NotContains(t, out, "sessions x", "nothing was sent, so there is nothing to count")
}

func TestScenarioVerdict_TakesThePrecedenceARunAlreadyUses(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		verdicts []string
		want     string
	}{
		"a failure outranks everything": {[]string{"pass", "blocked", "fail", "unverified"}, "fail"},
		"blocked outranks unverified":   {[]string{"pass", "unverified", "blocked"}, "blocked"},
		"unverified outranks pass":      {[]string{"pass", "unverified"}, "unverified"},
		"all pass":                      {[]string{"pass", "pass"}, "pass"},
		"nothing ran":                   {nil, "blocked"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			results := make([]load.ScenarioResult, 0, len(tc.verdicts))
			for _, v := range tc.verdicts {
				results = append(results, load.ScenarioResult{Verdict: v})
			}
			require.Equal(t, tc.want, scenarioVerdict(results))
		})
	}
}

func TestScenarioExit_AFailureAndABlockAreDifferentCodes(t *testing.T) {
	t.Parallel()
	// A failure is the application. A block is the manifest not saying a
	// route may be called, which is a configuration problem fixed in a
	// different file by a different person.
	failed := scenarioExit([]load.ScenarioResult{{
		Scenario: "a", Verdict: load.VerdictFail,
		Assertions: []load.AssertionResult{
			{Name: "one", Verdict: load.VerdictFail}, {Name: "two", Verdict: load.VerdictFail},
		},
	}})
	require.Equal(t, aferrors.AFLOD014, codeOfErr(t, failed))
	require.Contains(t, failed.Error(), "2 scenario assertions")

	blocked := scenarioExit([]load.ScenarioResult{{
		Scenario: "b", Verdict: load.VerdictBlocked, Detail: "POST /pay is not in safe_routes",
	}})
	require.Equal(t, aferrors.AFLOD015, codeOfErr(t, blocked))
	require.Contains(t, blocked.Error(), "POST /pay is not in safe_routes",
		"the code prints under the detail rather than instead of it")

	// An assertion nothing could measure is a typo in a step name, and a typo
	// that exits zero is a check that reports green forever.
	unmeasured := scenarioExit([]load.ScenarioResult{{
		Scenario: "c", Verdict: load.VerdictUnverified,
		Assertions: []load.AssertionResult{
			{Name: "about_nothing", Verdict: load.VerdictUnverified, Detail: "nothing to measure"},
		},
	}})
	require.Equal(t, aferrors.AFLOD015, codeOfErr(t, unmeasured))
	require.Contains(t, unmeasured.Error(), "about_nothing")

	// A scenario that declares no assertions asked for traffic and nothing
	// more. Its author chose that, and it is not an error.
	require.NoError(t, scenarioExit([]load.ScenarioResult{{
		Scenario: "d", Verdict: load.VerdictUnverified,
	}}))
	require.NoError(t, scenarioExit([]load.ScenarioResult{{Scenario: "e", Verdict: load.VerdictPass}}))
}

func codeOfErr(t *testing.T, err error) aferrors.Code {
	t.Helper()
	require.Error(t, err)
	var e *aferrors.Error
	require.True(t, aferrors.As(err, &e), "the error carries a catalog code: %v", err)
	return e.Code()
}

func TestLoadScenarioCommand_IsReachableFromTheLoadCommand(t *testing.T) {
	t.Parallel()
	// A command nobody can reach is the shape this repository keeps shipping.
	cmd := newLoadCommand(&Env{Out: NewOutput(&bytes.Buffer{}, &bytes.Buffer{})})
	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Name() == "scenario" {
			found = true
			require.True(t, strings.Contains(sub.Long, "safe_routes"),
				"the help says the safe list applies here too")
		}
	}
	require.True(t, found, "'af load scenario' is registered")
}

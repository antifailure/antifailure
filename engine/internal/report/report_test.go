package report_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

func run(workflows ...report.Workflow) report.Run {
	return report.Run{
		Environment: "shop-feature-x-9f0e", URL: "http://127.0.0.1:46000",
		Branch: "feature/x", Commit: "9f0edc1234567", Workflows: workflows,
	}
}

func TestVerdict_AFailureOutranksEverything(t *testing.T) {
	t.Parallel()
	r := run(
		report.Workflow{Name: "a", Verdict: "pass"},
		report.Workflow{Name: "b", Verdict: "blocked"},
		report.Workflow{Name: "c", Verdict: "fail"},
	)
	require.Equal(t, "fail", r.Verdict())
}

func TestVerdict_FlakyOutranksBlocked(t *testing.T) {
	t.Parallel()
	// A flaky workflow is a real signal about the application; a blocked one
	// is a signal about us, and burying the first under the second is how the
	// comment stops being useful.
	r := run(
		report.Workflow{Name: "a", Verdict: "blocked"},
		report.Workflow{Name: "b", Verdict: "flaky"},
	)
	require.Equal(t, "flaky", r.Verdict())
}

func TestVerdict_NothingRanIsBlocked(t *testing.T) {
	t.Parallel()
	require.Equal(t, "blocked", run().Verdict())
	require.Equal(t, "Nothing ran.", run().Headline())
}

func TestMarkdown_FailuresComeFirst(t *testing.T) {
	t.Parallel()
	// Somebody scrolling to find the failure is the same as somebody not
	// seeing it.
	body := run(
		report.Workflow{Name: "passing", Verdict: "pass"},
		report.Workflow{Name: "blocked-one", Verdict: "blocked", Detail: "no fixture"},
		report.Workflow{Name: "broken", Verdict: "fail", Detail: "still the free plan"},
	).Markdown()

	require.Less(t, strings.Index(body, "`broken`"), strings.Index(body, "`blocked-one`"))
	require.Less(t, strings.Index(body, "`blocked-one`"), strings.Index(body, "`passing`"))
	require.Contains(t, body, "1 workflow failed.")
}

func TestMarkdown_ABlockedRunSaysItDoesNotCount(t *testing.T) {
	t.Parallel()
	// A comment that reads as a wall of red on a pull request that is fine is
	// a comment people mute, and a muted comment is a check everybody believes
	// is running.
	body := run(report.Workflow{Name: "a", Verdict: "blocked", Detail: "the browser closed"}).Markdown()
	require.Contains(t, body, "Nothing here counts against the change.")
	require.Contains(t, body, "not counted against this change")
}

func TestMarkdown_StepsAreFoldedAndOnlyForFailures(t *testing.T) {
	t.Parallel()
	body := run(
		report.Workflow{Name: "passing", Verdict: "pass", Steps: []string{"1. do a thing"}},
		report.Workflow{Name: "broken", Verdict: "fail", Steps: []string{"1. open /", "2. press Buy"},
			Trace: "/tmp/broken.trace.zip"},
	).Markdown()

	require.Contains(t, body, "<details><summary>How to see <code>broken</code> yourself")
	require.Contains(t, body, "2. press Buy")
	require.Contains(t, body, "/tmp/broken.trace.zip")
	require.NotContains(t, body, "<code>passing</code> yourself",
		"a passing workflow needs no reproduction steps")
}

func TestMarkdown_ATableCellStaysACell(t *testing.T) {
	t.Parallel()
	// A detail with a newline or a pipe in it breaks the table, and a broken
	// table is unreadable rather than merely ugly.
	body := run(report.Workflow{
		Name: "broken", Verdict: "fail",
		Detail: "line one\nline two | with a pipe",
	}).Markdown()

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "| `broken`") {
			require.Equal(t, 4, strings.Count(line, "|")-strings.Count(line, "\\|"),
				"the row has exactly the cells it should: %s", line)
			require.Contains(t, line, "line one line two")
		}
	}
}

func TestMarkdown_ReportsUnverifiedMasking(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "a", Verdict: "pass"})
	r.Verification = &report.Verification{
		Clean: false, Findings: []string{"public.customers.email holds email."},
	}
	body := r.Markdown()
	require.Contains(t, body, "**Masking did not verify.**")
	require.Contains(t, body, "customers.email")
}

func TestMarkdown_NamesRefusedHostsNothingMentions(t *testing.T) {
	t.Parallel()
	// Usually a dependency somebody added without noticing, which is the one
	// line in the whole comment that teaches somebody something.
	r := run(report.Workflow{Name: "a", Verdict: "pass"})
	r.Egress = &report.Egress{Allowed: 12, Refused: 3, Surprises: []string{"api.newthing.com"}}
	body := r.Markdown()
	require.Contains(t, body, "api.newthing.com")
	require.Contains(t, body, "add a rule")
}

func TestComment_CarriesAMarkerSoAnUpdateReplacesIt(t *testing.T) {
	t.Parallel()
	// A check that adds a comment per push turns a pull request with twelve
	// pushes into one with twelve comments, and the twelfth is the only one
	// that is true.
	body := run(report.Workflow{Name: "a", Verdict: "pass"}).Comment()
	require.True(t, strings.HasPrefix(body, report.Marker))
}

func TestMarkdown_DocsLinkCanPointAtASelfHostedCopy(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "a", Verdict: "blocked"})
	r.DocsBase = "https://docs.internal.example"
	require.Contains(t, r.Markdown(), "https://docs.internal.example/concepts/verdicts")
	require.NotContains(t, r.Markdown(), "antifailure.dev")
}

func TestMarkdown_FooterCarriesWhatWasTested(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "a", Verdict: "pass"})
	r.Duration, r.Golden = "2m14s", "gv_20260826"
	body := r.Markdown()
	require.Contains(t, body, "feature/x")
	require.Contains(t, body, "`9f0edc1`", "the commit is short, the way everybody writes it")
	require.Contains(t, body, "2m14s")
	require.Contains(t, body, "gv_20260826")
}

// The masking section renders when something fills it in.
//
// It could not, before. Run.Verification was rendered here and assigned by
// nothing outside a test, so the section that tells a reviewer the data was
// proved masked was unreachable in a real comment. That is the product's
// central promise, and it was a field nobody filled in.
func TestMarkdown_ReportsVerifiedMasking(t *testing.T) {
	r := run(report.Workflow{Name: "sign in", Verdict: "pass"})
	r.Verification = &report.Verification{Clean: true, Columns: 84, RowsSampled: 2620}
	md := r.Markdown()
	require.Contains(t, md, "Masking verified")
	require.Contains(t, md, "84 columns")
	require.Contains(t, md, "2620 rows")
}

// The insights section says what the database noticed.
func TestMarkdown_ReportsWhatTheDatabaseNoticed(t *testing.T) {
	r := run(report.Workflow{Name: "sign in", Verdict: "pass"})
	r.Insights = &report.Insights{
		Sequential: []report.Scan{{Table: "audit_entries", Scans: 12, Rows: 41000}},
		Slowest:    "select * from environments", SlowestMs: 412,
		Unused: []string{"events.events_env_idx"},
	}
	md := r.Markdown()
	require.Contains(t, md, "audit_entries")
	require.Contains(t, md, "41000 rows")
	require.Contains(t, md, "412ms")
	require.Contains(t, md, "1 indexes nothing read")
}

// Looked and found nothing is not the same as could not look.
//
// Both are four words on a pull request and only one of them is evidence. A
// section that reports a clean bill of health because the extension was not
// installed is the worst possible answer, because it is the one somebody
// trusts.
func TestMarkdown_SaysWhenInsightsCouldNotLook(t *testing.T) {
	r := run(report.Workflow{Name: "sign in", Verdict: "pass"})
	r.Insights = &report.Insights{Missing: []string{"pg_stat_statements is not installed"}}
	md := r.Markdown()
	require.Contains(t, md, "could not look")
	require.Contains(t, md, "pg_stat_statements")
	require.NotContains(t, md, "no table read end to end")

	clean := run(report.Workflow{Name: "sign in", Verdict: "pass"})
	clean.Insights = &report.Insights{}
	require.Contains(t, clean.Markdown(), "no table read end to end")
}

// The comment is the only part of this most people see, so what it says when
// the workflows pass and the data is broken is the thing to get right.
func TestACleanRunOfWorkflowsWithBrokenDataDoesNotReadAsAPass(t *testing.T) {
	run := report.Run{
		Environment: "pr-42",
		Workflows: []report.Workflow{
			{Name: "checkout", Verdict: "pass"},
			{Name: "signup", Verdict: "pass"},
		},
		Invariants: []report.Invariant{
			{
				Name:        "no-orphan-orders",
				Description: "Every order belongs to a user that exists.",
				Columns:     []string{"order_id", "user_id"},
				Rows:        [][]string{{"900", "4242"}, {"901", "4243"}},
			},
		},
	}

	require.Equal(t, "fail", run.Verdict(),
		"a violated invariant fails the run even when every workflow passed")
	require.Equal(t, "Every workflow passed and 1 invariant did not hold.", run.Headline())

	md := run.Markdown()
	require.Contains(t, md, "**Invariant `no-orphan-orders` does not hold.**")
	require.Contains(t, md, "Every order belongs to a user that exists.")
	// The rows are the diagnosis, so they are in the comment rather than
	// behind an instruction to go and run the query.
	require.Contains(t, md, "| order_id | user_id |")
	require.Contains(t, md, "| 900 | 4242 |")
	require.Contains(t, md, "| 901 | 4243 |")
	require.NotContains(t, md, "All 2 workflows passed")
}

func TestAnInvariantThatHeldCostsTheReaderOneLine(t *testing.T) {
	run := report.Run{
		Workflows:  []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Invariants: []report.Invariant{{Name: "no-orphan-orders", Held: true}},
	}
	require.Equal(t, "pass", run.Verdict())
	require.Equal(t, "All 1 workflows passed, and 1 invariant held.", run.Headline())
	require.Contains(t, run.Markdown(), "Invariants: 1 invariant held.")
}

// An invariant that could not be asked is ours, not the change's, and the
// comment has to say so or an incomplete environment reads as broken data.
func TestAnInvariantThatCouldNotBeCheckedIsNotHeldAgainstTheChange(t *testing.T) {
	run := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Invariants: []report.Invariant{
			{Name: "no-orphan-orders", Error: "AF-AGT-010 Invariant no-orphan-orders did not finish within 30s."},
		},
	}
	require.Equal(t, "pass", run.Verdict(), "a blocked invariant does not fail the run")
	require.Zero(t, run.InvariantsViolated())

	md := run.Markdown()
	require.Contains(t, md, "could not be checked")
	require.Contains(t, md, "Nothing here counts against the change.")
	require.NotContains(t, md, "does not hold")
}

func TestMoreRowsThanKeptIsSaidRatherThanImplied(t *testing.T) {
	run := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: "pass"}},
		Invariants: []report.Invariant{{
			Name:    "no-orphan-orders",
			Columns: []string{"order_id"},
			Rows:    [][]string{{"1"}, {"2"}},
			More:    true,
		}},
	}
	require.Contains(t, run.Markdown(), "More rows than these.")
}

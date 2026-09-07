package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
)

// The line and the exit code are one decision, so they are tested as one.
//
// Asserting on the exact string rather than on a flag is deliberate. The
// failure was not a wrong boolean, it was the word "ok" printed over a check
// that did not run, and a test that only asserted "returns an error" would
// have passed while the word was still there.

func TestInsightsSummary_ABlockedCheckNeverSaysOk(t *testing.T) {
	t.Parallel()
	const reason = "the migrations were not rehearsed: no migration tool was recognised " +
		"in this repository"
	line, err := insightsSummary(insights.Full{Blocked: []string{reason}})

	require.Empty(t, line, "a blocked run must print no summary of its own")
	require.NotContains(t, line, "ok")
	require.Error(t, err)

	// Exit 7, told apart from both a pass and a proven break. This is the
	// whole point: CI can branch on it.
	require.Equal(t, aferrors.ExitVerification, aferrors.ExitCodeOf(err))
	require.NotEqual(t, aferrors.ExitSuccess, aferrors.ExitCodeOf(err))

	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFDB033, coded.Entry.Code)
	// The reason travels out to the exit, because "a check did not run" with
	// no because sends somebody to read the code rather than the manifest.
	require.Contains(t, coded.Fields["detail"], "no migration tool was recognised")
}

func TestInsightsSummary_ARunWithNothingToReportStillSaysSo(t *testing.T) {
	t.Parallel()
	line, err := insightsSummary(insights.Full{})
	require.NoError(t, err)
	require.Equal(t, "nothing to report", line)
}

func TestInsightsSummary_ADeclinedRehearsalIsAPass(t *testing.T) {
	t.Parallel()
	// --no-rehearsal puts its reason in Missing and not in Blocked, so the
	// run ends clean. A flag whose only use is to get a clean run without a
	// rehearsal has to actually produce one.
	line, err := insightsSummary(insights.Full{
		Missing: []string{"the migrations were not rehearsed, because --no-rehearsal was given"},
	})
	require.NoError(t, err)
	require.Equal(t, "nothing to report", line)
}

func TestInsightsSummary_AFindingPrintsNoSummaryAndDoesNotFailHere(t *testing.T) {
	t.Parallel()
	// A run that found something has already printed it, and the failure exit
	// codes for a proven break are decided before this function is reached.
	line, err := insightsSummary(insights.Full{
		PlanFindings: []insights.PlanFinding{{Statement: "SELECT 1"}},
	})
	require.NoError(t, err)
	require.Empty(t, line)
}

func TestInsightsSummary_ABreakAndABlockAreDifferentExitCodes(t *testing.T) {
	t.Parallel()
	// The reason there are three codes rather than two. A blocked check that
	// exited like a break trains people to ignore it; one that exited like a
	// pass is the bug being fixed. Both failures come from collapsing these.
	_, blocked := insightsSummary(insights.Full{Blocked: []string{"nothing was rehearsed"}})
	migrationBroke := aferrors.Coded(aferrors.AFDB030, "detail", "syntax error")
	rollingBroke := aferrors.Coded(aferrors.AFDB032, "detail", "checkout fails")

	require.NotEqual(t, aferrors.ExitCodeOf(blocked), aferrors.ExitCodeOf(migrationBroke))
	require.NotEqual(t, aferrors.ExitCodeOf(blocked), aferrors.ExitCodeOf(rollingBroke))
	require.NotEqual(t, aferrors.ExitSuccess, aferrors.ExitCodeOf(blocked))
}

// The rendered output, not the returned string.
//
// The plan asked for this in these words: a repository where discovery
// genuinely fails produces a summary that does not contain `ok`, proved by
// asserting on the exact output. A test on the return value proves the
// decision; only a test on the buffer proves the character that reached the
// terminal, and the character is what the developer read and believed.

func TestPrintInsightsSummary_ABlockedRunPrintsNothingAtAll(t *testing.T) {
	t.Parallel()
	var out, errW bytes.Buffer
	e := &Env{Out: NewOutput(&out, &errW)}

	err := printInsightsSummary(e, insights.Full{
		Blocked: []string{"no migration tool was recognised anywhere in this repository"},
	})

	require.Error(t, err)
	require.Equal(t, aferrors.ExitVerification, aferrors.ExitCodeOf(err))
	require.Empty(t, out.String(), "a blocked run printed a summary line")
	require.NotContains(t, out.String(), "ok")
	require.NotContains(t, out.String(), SymbolOK)
	require.NotContains(t, out.String(), "nothing to report")
}

func TestPrintInsightsSummary_ACleanRunStillPrintsItsLine(t *testing.T) {
	t.Parallel()
	var out, errW bytes.Buffer
	e := &Env{Out: NewOutput(&out, &errW)}

	require.NoError(t, printInsightsSummary(e, insights.Full{}))
	require.Contains(t, out.String(), "nothing to report")
	require.Contains(t, out.String(), "from the checks that ran")
}

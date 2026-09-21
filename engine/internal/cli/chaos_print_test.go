package cli_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The words pgcrash records when bt_index_check returned without raising.
// Spelled out rather than imported, because the test is about what a person
// reads and the constant is unexported for a reason.
const amcheckPassed = "the index verified, with every heap tuple present in it"

const pagesPass = "the writers' table read back in full with data checksums on, and no page of it " +
	"failed its checksum. No other table was read."

// crashRun is one crash fault that recovered, with the page integrity facts
// the proof hands the printer set by the caller.
func crashRun(stateAfter string, checksumsOn bool, amcheck string, findings ...report.Finding) *env.ChaosRun {
	return &env.ChaosRun{
		Report: report.Chaos{Faults: []report.ChaosFault{{
			Name: "crash-postgres-under-load", Kind: "process_kill", Target: "database",
			Evidence: "sent SIGKILL to pid 27 (postgres: checkpointer)",
			Injected: true, Undone: true,
			Recovery: &report.ChaosRecovery{
				Crashed: true, Signal: 9, Replayed: true,
				RedoStart: "0/83C39E8", RedoEnd: "0/83E5E80",
				StateBefore: "in production", StateAfter: stateAfter,
				Acknowledged: 601, Present: 602, InFlightLanded: 1,
				HeapRows: 602, IndexRows: 602,
				Amcheck: amcheck, ChecksumsOn: checksumsOn,
				DowntimeMs: 3220, Verified: true,
			},
		}}},
		Findings: findings,
	}
}

// printed renders a run and joins every wrapped value back onto its label's
// line, so an assertion is about the sentence and not about where the
// terminal width happened to break it.
func printed(t *testing.T, run *env.ChaosRun) string {
	t.Helper()
	out := cli.PrintChaosForTest(run)
	t.Log("\n" + out)
	require.NotContains(t, out, "\u2014", "an em dash reached the terminal")
	return strings.ReplaceAll(out, "\n"+strings.Repeat(" ", len("      pages          ")), " ")
}

// TestPrintChaos_SaysWhatWasEstablishedAboutPages is the defect: with data
// checksums on, the terminal printed nothing whatsoever about page integrity,
// so a check that ran and passed looked exactly like one that never ran.
func TestPrintChaos_SaysWhatWasEstablishedAboutPages(t *testing.T) {
	out := printed(t, crashRun("in production", true, amcheckPassed))
	require.Contains(t, out, "      pages          "+pagesPass+"\n",
		"a run that read the table back with checksums on did not say so")
	require.Contains(t, out, "      amcheck        "+amcheckPassed+"\n",
		"a run whose index amcheck verified did not say so")
}

// TestPrintChaos_ChecksumsOffSaysPagesWereNotChecked is the case that used to
// be the only one with a line, and it still has to say NOT checked beside an
// amcheck pass rather than borrow that pass for the pages.
func TestPrintChaos_ChecksumsOffSaysPagesWereNotChecked(t *testing.T) {
	out := printed(t, crashRun("in production", false, amcheckPassed, report.Finding{
		Rule: pgcrash.RuleChecksumsOff, Level: report.LevelWarn,
		Title: "Data page checksums are off on this cluster",
	}))
	require.Contains(t, out, "      pages          not checked, because data checksums are off on this cluster "+
		"and a torn page would read back as data\n")
	require.NotContains(t, out, pagesPass, "pages were claimed checked on a cluster with checksums off")
	require.Contains(t, out, pgcrash.RuleChecksumsOff, "the checksums warning is no longer printed")
}

// TestPrintChaos_AmcheckThatCouldNotRunIsNotAPass holds the two ways amcheck
// can fail to vouch for the index. The table was still read back in full when
// the extension was merely missing, so the pages line stands on its own; when
// the read back itself did not finish, neither line may claim anything.
func TestPrintChaos_AmcheckThatCouldNotRunIsNotAPass(t *testing.T) {
	const missing = "the amcheck extension is not available in this database: ERROR: extension \"amcheck\" is not available"
	out := printed(t, crashRun("in production", true, missing))
	require.Contains(t, out, "      amcheck        "+missing+"\n",
		"amcheck's reason for not verifying the index was not printed")
	require.NotContains(t, out, amcheckPassed, "an index amcheck never verified was printed as verified")
	require.Contains(t, out, "      pages          "+pagesPass+"\n",
		"amcheck runs only after the heap was counted, so its absence says nothing against the pages")

	out = printed(t, crashRun("in production", true, ""))
	require.Contains(t, out, "      amcheck        did not run, because reading the writers' table back after the fault did not finish\n",
		"an amcheck that never ran was printed as a blank, which reads as nothing wrong")
	require.Contains(t, out, "      pages          not checked, because reading the writers' table back after the fault did not finish\n",
		"a read back that did not finish was printed as a page check")
	require.NotContains(t, out, pagesPass)
}

// TestPrintChaos_AnUnreadControlFileIsNotChecksumsOff is the case a zero value
// would get wrong: a checksum version that was never read is false in the
// report, and false is not "off".
func TestPrintChaos_AnUnreadControlFileIsNotChecksumsOff(t *testing.T) {
	out := printed(t, crashRun("", false, amcheckPassed))
	require.Contains(t, out, "      pages          not checked, because the control file could not be read after the fault, "+
		"so whether data checksums are on is unknown\n")
	require.NotContains(t, out, "data checksums are off", "an unread control file was reported as checksums off")
}

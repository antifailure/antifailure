package report_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// held is a run with a fault that landed and a recovery that was correct.
func heldChaos() *report.Chaos {
	return &report.Chaos{Faults: []report.ChaosFault{{
		Name: "postgres-crash", Kind: "process_kill", Target: "database",
		Evidence: "sent SIGKILL to pid 56 (postgres: checkpointer)",
		Injected: true, Undone: true,
		Recovery: &report.ChaosRecovery{
			Crashed: true, Signal: 9, Replayed: true,
			RedoStart: "0/1950478", RedoEnd: "0/197AD88",
			StateBefore: "in production", StateAfter: "in production",
			Acknowledged: 897, Present: 902, Lost: 0, Phantom: 0, InFlightLanded: 5,
			HeapRows: 902, IndexRows: 902, Amcheck: pgcrash.AmcheckPassed,
			ChecksumsOn: false, DowntimeMs: 9833, Verified: true,
		},
	}}}
}

func TestChaosSection_SaysWhatWasMeasuredAndNotOnlyThatItPassed(t *testing.T) {
	// The numbers are the section's reason for existing. "Nothing was lost" out
	// of four commits is almost nothing, and a reader can only tell the
	// difference if the denominator is on the page.
	out := report.Run{Chaos: heldChaos()}.Markdown()
	for _, want := range []string{
		"broken on purpose",
		"`postgres-crash`",
		"sent SIGKILL to pid 56",
		"a server process was killed by signal 9",
		"from 0/1950478 to 0/197AD88",
		"| Commits the client was told were committed | 897 |",
		"| Of those, missing after recovery | 0 |",
		"| Rows present that no client wrote | 0 |",
		"| Commits in flight at the crash that landed | 5 |",
		"| Heap and index agree | yes, 902 rows both ways, and amcheck found every row in the index |",
		"in production, then in production",
	} {
		require.Contains(t, out, want, "the chaos section must carry %q", want)
	}
}

func TestChaosSection_ADatabaseThatNeverCrashedIsNotRenderedAsOneThatRecovered(t *testing.T) {
	c := heldChaos()
	c.Faults[0].Recovery.Crashed = false
	c.Faults[0].Recovery.Signal = 0
	c.Faults[0].Recovery.Replayed = false
	c.Faults[0].Recovery.RedoStart, c.Faults[0].Recovery.RedoEnd = "", ""
	c.Faults[0].Recovery.Verified = false

	out := report.Run{Chaos: c}.Markdown()
	require.Contains(t, out, "no, and the log carries no process killed by a signal")
	require.Contains(t, out, "no replay is recorded in the log")
	require.Contains(t, out, "This fault's recovery was not established.")
	require.NotContains(t, out, "killed by signal 9")
}

func TestChaosSection_ADamagedRelationIsBoldAndCarriesBothCounts(t *testing.T) {
	c := heldChaos()
	c.Faults[0].Recovery.IndexRows = 900
	out := report.Run{Chaos: c}.Markdown()
	require.Contains(t, out, "**no: the heap counted 902 and the index counted 900**")
}

func TestChaosSection_AFaultThatWouldNotGoInSaysNothingWasMeasured(t *testing.T) {
	out := report.Run{Chaos: &report.Chaos{Faults: []report.ChaosFault{{
		Name: "postgres-crash", Kind: "process_kill", Target: "database",
		Error: "AF-CHS-004: no process in the container matches",
	}}}}.Markdown()
	require.Contains(t, out, "could not be injected")
	require.Contains(t, out, "Nothing after it was measured.")
	// And no table, because there is nothing to put in one.
	require.NotContains(t, out, "| What was measured | Result |")
}

func TestChaosSection_IsAbsentWhenNoFaultWasDeclared(t *testing.T) {
	// The liveness arm for every assertion above: a run with no chaos block
	// must not gain a section, or the section would say the same thing about
	// every project whether or not it asked for one.
	out := report.Run{}.Markdown()
	require.NotContains(t, strings.ToLower(out), "broken on purpose")
}

// withIntegrity is the held run with the page integrity facts the proof hands
// the renderer replaced.
func withIntegrity(checksumsOn bool, amcheck string) string {
	c := heldChaos()
	c.Faults[0].Recovery.ChecksumsOn = checksumsOn
	c.Faults[0].Recovery.Amcheck = amcheck
	return report.Run{Chaos: c}.Markdown()
}

const pagesPassRow = "| Torn pages in the writers' table | the writers' table read back in full with data checksums on, " +
	"and no page of it failed its checksum. No other table was read. |"

// TestChaosSection_APassingRunSaysWhatAmcheckAndThePagesEstablished is the
// one case where the table may say yes, and it has to say what the yes rests
// on: both counts, amcheck's verdict and the pages it read.
func TestChaosSection_APassingRunSaysWhatAmcheckAndThePagesEstablished(t *testing.T) {
	out := withIntegrity(true, pgcrash.AmcheckPassed)
	require.Contains(t, out, "| Heap and index agree | yes, 902 rows both ways, and amcheck found every row in the index |")
	require.Contains(t, out, pagesPassRow, "a run that read the table back with checksums on did not say so")
}

// TestChaosSection_AnAmcheckFailureIsNotAYes is the defect: the cell said yes
// whenever amcheck had answered anything, so an index it reported a problem
// in read as a clean one on the page a reviewer reads before merging.
func TestChaosSection_AnAmcheckFailureIsNotAYes(t *testing.T) {
	const problem = "bt_index_check reported a problem: ERROR: item order invariant violated for index \"commits_pkey\""
	out := withIntegrity(true, problem)
	require.NotContains(t, out, "| Heap and index agree | yes", "an index amcheck reported a problem in was called agreeing")
	require.Contains(t, out, "| Heap and index agree | **not verified: both scans counted 902 rows, and amcheck said: "+
		problem+"** |", "the reason amcheck gave was not quoted")
}

// TestChaosSection_AnAmcheckThatWasUnavailableIsNotAYes is the other way
// amcheck can fail to vouch: the extension was not there. The table was still
// read back in full before amcheck was asked, so the pages row stands.
func TestChaosSection_AnAmcheckThatWasUnavailableIsNotAYes(t *testing.T) {
	const missing = "the amcheck extension is not available in this database: ERROR: extension \"amcheck\" is not available"
	out := withIntegrity(true, missing)
	require.NotContains(t, out, "| Heap and index agree | yes", "an index amcheck never looked at was called agreeing")
	require.Contains(t, out, "amcheck said: "+missing+"** |", "the reason amcheck gave was not quoted")
	require.Contains(t, out, pagesPassRow, "amcheck runs only after the heap was counted, so its absence says nothing against the pages")
}

// TestChaosSection_AReadBackThatDidNotFinishClaimsNothing is the run whose
// read back stopped, for instance at a page that failed its checksum. Neither
// the counts nor the pages may be offered as a result.
func TestChaosSection_AReadBackThatDidNotFinishClaimsNothing(t *testing.T) {
	out := withIntegrity(true, "")
	require.Contains(t, out, "| Heap and index agree | not checked, because reading the writers' table back after the fault did not finish |")
	require.NotContains(t, out, pagesPassRow, "a read back that did not finish was reported as a page check")
	require.Contains(t, out, "| Torn pages in the writers' table | not checked, because reading the writers' table back "+
		"after the fault did not finish |")
}

// TestChaosSection_ChecksumsOffSaysPagesWereNotChecked holds the row against
// borrowing amcheck's pass on a cluster that could not have seen a torn page.
func TestChaosSection_ChecksumsOffSaysPagesWereNotChecked(t *testing.T) {
	out := withIntegrity(false, pgcrash.AmcheckPassed)
	require.NotContains(t, out, pagesPassRow, "pages were claimed checked on a cluster with checksums off")
	require.Contains(t, out, "| Torn pages in the writers' table | not checked, because data checksums are off on this cluster "+
		"and a torn page would read back as data |")
	require.NotContains(t, out, "\u2014", "an em dash reached the pull request comment")
}

// partitionChaos is the fault from the report that found this: a network
// partition declared to hold five seconds, which the injector held for five.
func partitionChaos() *report.Chaos {
	return &report.Chaos{Faults: []report.ChaosFault{{
		Name: "cut-the-service-off-from-the-database", Kind: "network_partition", Target: "service ledger",
		Evidence: "detached af-svc-ledger from af-net-ledger",
		Injected: true, Undone: true,
		DurationMs: 10548, InPlaceMs: 5001, HoldDeclaredMs: 5000,
	}}}
}

// A partition's comment used to say what was detached and nothing about for
// how long, while the only number any surface carried was a duration that was
// zero for every such fault. The comment now says how long the fault was
// measured to be in place, beside what the manifest declared, so a reviewer
// never has to infer the length of an outage from a field that measured
// something else.
func TestChaosSection_SaysHowLongTheFaultWasInPlace(t *testing.T) {
	out := report.Run{Chaos: partitionChaos()}.Markdown()
	require.Contains(t, out,
		"Fault `cut-the-service-off-from-the-database` (network_partition) on service ledger: "+
			"detached af-svc-ledger from af-net-ledger. It was in place for 5.001s (declared 5s), then undone.")
}

// A measured zero is said as a sentence, never printed as "0 ms" beside a
// declared hold where it reads as a fault that did not last.
func TestInPlaceSays_AZeroIsSaidAndNotPrinted(t *testing.T) {
	f := partitionChaos().Faults[0]
	f.InPlaceMs = 0
	require.Equal(t, "in place for no measurable time (declared 5s)", f.InPlaceSays())
}

// A fault whose undo did not complete is not "then undone".
func TestInPlaceSays_AnUndoThatDidNotCompleteIsNotUndone(t *testing.T) {
	f := partitionChaos().Faults[0]
	f.Undone = false
	require.Equal(t, "in place for 5.001s (declared 5s), and its undo did not complete", f.InPlaceSays())
}

// A fault that never went in was never in place, and says nothing rather
// than a duration.
func TestInPlaceSays_AFaultThatNeverWentInSaysNothing(t *testing.T) {
	f := partitionChaos().Faults[0]
	f.Injected, f.Undone, f.InPlaceMs = false, false, 0
	require.Empty(t, f.InPlaceSays())
}

// The unreachable row used to print the time from the fault to the first
// query after the settle, which could never be less than the settle. It now
// prints what a probe beside the fault measured, with its resolution, and says
// "never" rather than printing a zero when every probe was answered.
func TestUnreachableSays_WhatTheProbeMeasuredWithItsResolution(t *testing.T) {
	rec := &report.ChaosRecovery{DowntimeMs: 1660, Unreachable: true, Recovered: true, ProbeIntervalMs: 100}
	require.Equal(t, "1.66s, probed every 100ms", rec.UnreachableSays())

	out := report.Run{Chaos: &report.Chaos{Faults: []report.ChaosFault{{
		Name: "postgres-crash", Kind: "process_kill", Target: "database", Injected: true, Undone: true,
		Recovery: rec,
	}}}}.Markdown()
	require.Contains(t, out, "| The database was unreachable for | 1.66s, probed every 100ms |")
}

func TestUnreachableSays_NeverIsAWordNotAZero(t *testing.T) {
	rec := &report.ChaosRecovery{ProbeIntervalMs: 100}
	require.Equal(t, "never: every query a probe sent every 100ms from the fault onwards was answered", rec.UnreachableSays())
}

func TestUnreachableSays_AnOutageWithNoEndIsAFloor(t *testing.T) {
	rec := &report.ChaosRecovery{DowntimeMs: 3000, Unreachable: true, ProbeIntervalMs: 100}
	require.Equal(t, "at least 3s, and it had not answered again when the probe stopped (probed every 100ms)", rec.UnreachableSays())
}

// A report from an engine that did not probe carries only the old number, and
// it is labelled as what it was rather than passed off as a measurement.
func TestUnreachableSays_AnUnprobedNumberIsLabelledAsOne(t *testing.T) {
	rec := &report.ChaosRecovery{DowntimeMs: 3100}
	require.Equal(t, "3.1s, timed from the fault to the first query after the settle, not probed", rec.UnreachableSays())
}

// withInvariants is the held run with the manifest's own invariants declared,
// in the shapes a reader has to be able to tell apart.
func withInvariants(invs ...report.ChaosInvariant) string {
	c := heldChaos()
	c.Faults[0].Invariants = invs
	return report.Run{Chaos: c}.Markdown()
}

// TestChaosSection_CarriesBothSidesOfEveryInvariant is what makes a violation
// readable.
//
// The after column alone cannot be acted on: a rule that is broken after a
// crash and was broken before it is not something the crash did, and a section
// showing only the second would put a project's own pre-existing defect on the
// fault. Both sides are on the page for exactly that reason.
func TestChaosSection_CarriesBothSidesOfEveryInvariant(t *testing.T) {
	out := withInvariants(
		report.ChaosInvariant{Name: "every-account-exists", BeforeHeld: true, AfterHeld: true},
		report.ChaosInvariant{Name: "no-negative-balance", Rows: [][]string{{"2"}}},
		report.ChaosInvariant{Name: "orders-have-a-customer", BeforeHeld: true,
			Rows: [][]string{{"7"}, {"9"}}},
		report.ChaosInvariant{Name: "asks-a-missing-table",
			BeforeError: "relation does not exist", AfterError: "relation does not exist"},
	)
	require.Contains(t, out, "| The project's own rules about its own data | Before the fault | After the recovery |")
	require.Contains(t, out, "| `every-account-exists` | held | held |")
	require.Contains(t, out, "| `no-negative-balance` | violated | violated, 1 row |")
	// The one the fault is answerable for is the one that is bold, the way a
	// damaged relation is.
	require.Contains(t, out, "| **`orders-have-a-customer`** | held | violated, 2 rows |")
	require.Contains(t, out, "| `asks-a-missing-table` | not asked: relation does not exist | not asked: relation does not exist |")
}

// TestChaosSection_AnInvariantNobodyAskedIsNotRenderedAsHeld is the pass that
// would be silent. "Not asked" and "held" are the two answers this arm exists
// to keep apart, and a reader who cannot see the difference has been told the
// run checked something it never did.
func TestChaosSection_AnInvariantNobodyAskedIsNotRenderedAsHeld(t *testing.T) {
	out := withInvariants(report.ChaosInvariant{
		Name: "no-negative-balance", BeforeHeld: true,
		AfterError: "the database did not answer a query after the fault",
	})
	require.Contains(t, out, "| `no-negative-balance` | held | not asked: the database did not answer a query after the fault |")
	require.NotContains(t, out, "| `no-negative-balance` | held | held |")
}

// TestChaosSection_SurvivesAFaultThatEndedInAnError is the ordering the arm
// exists for: the database did not come back, so there is no recovery table,
// and the one thing worth saying is that the project's rules were never asked.
func TestChaosSection_SurvivesAFaultThatEndedInAnError(t *testing.T) {
	out := report.Run{Chaos: &report.Chaos{Faults: []report.ChaosFault{{
		Name: "freeze", Kind: "container_pause", Target: "database",
		Injected: true, Undone: true,
		Error:    "AF-CHS-006: the database did not answer a query within 5s",
		Invariants: []report.ChaosInvariant{{
			Name: "no-negative-balance", BeforeHeld: true,
			AfterError: "the database did not answer a query after the fault",
		}},
	}}}}.Markdown()
	require.Contains(t, out, "| `no-negative-balance` | held | not asked: the database did not answer a query after the fault |")
}

// TestChaosSection_AddsNothingWhenNoInvariantIsDeclared is the liveness arm
// for every assertion above, and the requirement that a project which declares
// none sees no change whatsoever.
func TestChaosSection_AddsNothingWhenNoInvariantIsDeclared(t *testing.T) {
	require.NotContains(t, report.Run{Chaos: heldChaos()}.Markdown(),
		"The project's own rules about its own data")
}

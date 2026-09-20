package report_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
			HeapRows: 902, IndexRows: 902, Amcheck: "the index verified",
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
		"yes, 902 rows both ways",
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

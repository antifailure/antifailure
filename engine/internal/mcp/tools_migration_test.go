package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// nativeVerdict had no test at all, which is how the second door stayed open.
//
// A rehearsal that ran over nothing produces no findings, and no findings read
// as a pass in every counting scheme. The only thing that can tell it apart
// from a repository whose migrations are genuinely safe is whether the run
// said it was blocked, so that is what is asserted here.

func TestNativeVerdict_ARehearsalThatNeverRanIsUnverified(t *testing.T) {
	t.Parallel()
	require.Equal(t, report.VerdictUnverified,
		nativeVerdict(insights.Full{}, nil))
}

func TestNativeVerdict_ARehearsalThatRanOverNothingIsUnverified(t *testing.T) {
	t.Parallel()
	// The rehearsal is non nil, so the old nil check saw a completed run.
	full := insights.Full{
		Rehearsal: &insights.Rehearsal{Tool: insights.ToolNone},
		Blocked:   []string{"no migration tool was recognised anywhere in this repository"},
	}
	require.Equal(t, report.VerdictUnverified, nativeVerdict(full, nil))
	require.NotEqual(t, report.VerdictPass, nativeVerdict(full, nil))
}

func TestNativeVerdict_ARehearsalThatRanAndFoundNothingIsAPass(t *testing.T) {
	t.Parallel()
	// The distinction has to cut both ways or it is just a way of never
	// passing. A real tool, a real run, no findings, is a pass.
	require.Equal(t, report.VerdictPass, nativeVerdict(insights.Full{
		Rehearsal: &insights.Rehearsal{Tool: insights.ToolPrisma},
	}, nil))
}

func TestNativeVerdict_AFindingStillDecidesOverABlockedFlag(t *testing.T) {
	t.Parallel()
	// A run that is blocked on one thing and has proved a failure on another
	// reports the failure. Ordering matters: unverified is what you say when
	// you know nothing, and here something is known.
	full := insights.Full{
		Rehearsal: &insights.Rehearsal{Tool: insights.ToolPrisma},
	}
	require.Equal(t, report.VerdictFail, nativeVerdict(full,
		[]report.Finding{{Level: report.LevelFail, Rule: "lock"}}))
}

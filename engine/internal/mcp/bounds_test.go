package mcp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// The bounding tests use large but deliberately chosen sizes.
//
// The property under test has no size dependent branch: the same comparison
// against the same constant decides at forty one elements and at forty
// million. What the scale buys is confidence that nothing between the input
// and the output is quadratic or unbounded, so it is set as high as this
// machine's memory allows rather than as high as the number sounds. The exact
// figures are stated so that a reader knows what was actually run.
const (
	manyFindings = 250_000
	manyEvidence = 1_000_000
)

func TestBoundFindings_StaysBoundedAndReportsTheTrueTotal(t *testing.T) {
	t.Parallel()
	in := make([]report.Finding, 0, manyFindings)
	for i := range manyFindings {
		level := report.LevelWarn
		// A handful of failures buried deep in the list. They have to surface,
		// because a truncation that dropped the only finding that decides the
		// verdict would be worse than no output at all.
		if i%50_000 == 49_999 {
			level = report.LevelFail
		}
		in = append(in, report.Finding{
			Rule: "synthetic", Level: level,
			Title: fmt.Sprintf("finding %d", i), Where: "table_x",
		})
	}

	page := boundFindings(in)

	require.Equal(t, maxFindings, page.Shown)
	require.Len(t, page.Items, maxFindings)
	// The total is the count BEFORE truncation. A caller must never have to
	// infer how much it was not shown.
	require.Equal(t, manyFindings, page.Total)
	require.True(t, page.Truncated)
	require.Contains(t, page.Note, fmt.Sprint(manyFindings),
		"the truncation note states the real total in words as well")

	// Worst first, so the cut fell on the least important end.
	for i, f := range page.Items {
		if i < 5 {
			require.Equal(t, string(report.LevelFail), f.Level,
				"every failure must appear before any warning")
		}
	}
}

func TestBoundFindings_DoesNotClaimTruncationWhenNothingWasWithheld(t *testing.T) {
	t.Parallel()
	page := boundFindings([]report.Finding{{Rule: "a", Level: report.LevelWarn, Title: "one"}})

	require.False(t, page.Truncated)
	require.Empty(t, page.Note)
	require.Equal(t, 1, page.Total)
	require.Equal(t, 1, page.Shown)
}

func TestBoundFindings_EmptyIsAnEmptyListNotNull(t *testing.T) {
	t.Parallel()
	page := boundFindings(nil)
	require.NotNil(t, page.Items, "a null here decodes as absent rather than as none")
	require.Empty(t, page.Items)
	require.Equal(t, 0, page.Total)
}

func TestBoundEvidence_PagesThroughAHugeListWithoutLosingCount(t *testing.T) {
	t.Parallel()
	all := make([]Evidence, 0, manyEvidence)
	for range manyEvidence {
		all = append(all, Evidence{URI: "af://x", Kind: "synthetic"})
	}

	page, fault := boundEvidence("run_a", all, "")
	require.Nil(t, fault)
	require.Equal(t, maxEvidencePerPage, page.Shown)
	require.Equal(t, manyEvidence, page.Total)
	require.True(t, page.Truncated)
	require.NotEmpty(t, page.NextCursor, "a truncated page must hand back a way to continue")

	// The cursor advances rather than repeating the first page, which is the
	// bug a caller only finds after paging forever.
	second, fault := boundEvidence("run_a", all, page.NextCursor)
	require.Nil(t, fault)
	require.Equal(t, manyEvidence, second.Total)
	require.NotEqual(t, page.NextCursor, second.NextCursor)
	require.Contains(t, second.Note, "21 to 40")
}

func TestBoundEvidence_LastPageHasNoCursor(t *testing.T) {
	t.Parallel()
	all := []Evidence{{URI: "a"}, {URI: "b"}}
	page, fault := boundEvidence("run_a", all, "")

	require.Nil(t, fault)
	require.False(t, page.Truncated)
	require.Empty(t, page.NextCursor, "an empty cursor is how a caller knows to stop")
	require.Equal(t, 2, page.Total)
}

func TestBoundEvidence_RefusesACursorFromAnotherRun(t *testing.T) {
	t.Parallel()
	all := make([]Evidence, 50)
	page, _ := boundEvidence("run_a", all, "")
	require.NotEmpty(t, page.NextCursor)

	// A cursor is a position, not a capability. Presenting one against a
	// different run is refused, so it cannot become a way to walk another
	// run's artifacts.
	_, fault := boundEvidence("run_b", all, page.NextCursor)
	require.NotNil(t, fault)
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Equal(t, "evidence_cursor", fault.Field)
}

func TestBoundEvidence_RefusesAForgedOrOversizedCursor(t *testing.T) {
	t.Parallel()
	all := make([]Evidence, 50)
	for _, cursor := range []string{
		"not-base64!!", "", "eyJvZmZzZXQiOjF9",
	} {
		if cursor == "" {
			continue // the empty cursor means the first page, tested above
		}
		_, fault := boundEvidence("run_a", all, cursor)
		require.NotNil(t, fault, "cursor %q", cursor)
	}

	long := make([]byte, 300)
	for i := range long {
		long[i] = 'A'
	}
	_, fault := boundEvidence("run_a", all, string(long))
	require.NotNil(t, fault)
	require.Equal(t, FaultArgumentTooLarge, fault.Code)
}

func TestBoundEvidence_ACursorPastTheEndYieldsAnEmptyFinalPage(t *testing.T) {
	t.Parallel()
	// A caller that kept a cursor while the list shrank must get an empty page
	// rather than a panic or a negative slice.
	all := []Evidence{{URI: "a"}}
	page, fault := boundEvidence("run_a", all, encodeCursor("run_a", 9999))

	require.Nil(t, fault)
	require.Empty(t, page.Items)
	require.Equal(t, 1, page.Total)
	require.Empty(t, page.NextCursor)
}

func TestBoundMetrics_PutsBreachedMeasurementsFirst(t *testing.T) {
	t.Parallel()
	in := []Metric{
		{Name: "fine_a"}, {Name: "fine_b"},
		{Name: "breached", Breached: true},
		{Name: "fine_c"},
	}
	out := boundMetrics(in)
	require.Equal(t, "breached", out[0].Name,
		"a caller that reads one metric should read the one that decided the verdict")
}

func TestClip_MarksWhatItCutAndKeepsCharactersWhole(t *testing.T) {
	t.Parallel()
	// A silently cut string is one a reader believes is complete, and half a
	// sentence read as a whole one is how a truncation becomes a wrong
	// conclusion.
	require.Equal(t, "abc", clip("abc", 10))
	require.Contains(t, clip("abcdefghij", 4), "[truncated]")

	// Multi byte characters must not be split in half, which would produce
	// invalid UTF-8 and a result that will not encode.
	out := clip("aaaéééé", 5)
	require.True(t, isValidUTF8(out), "clipping left invalid UTF-8: %q", out)
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

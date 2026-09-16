package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/review"
)

// fakeReviewRunner returns a canned result and meta, so the tool is exercised
// without a model, a checkout or a network, the same way the security tool's
// tests build a stored row directly. The branch it was called with is recorded,
// so a test can prove the optional argument reached the runner and nothing else.
type fakeReviewRunner struct {
	res    review.Result
	meta   reviewMeta
	err    error
	branch string
}

func (f *fakeReviewRunner) run(_ context.Context, branch string) (review.Result, reviewMeta, error) {
	f.branch = branch
	return f.res, f.meta, f.err
}

func callReview(t *testing.T, run reviewRunner, args map[string]any) (map[string]any, *Fault) {
	t.Helper()
	tool := newReviewChangeTool(&Project{ID: "repo"}, run)
	out, fault := tool.Handler(context.Background(), &Call{Caller: "cli", Project: "repo"}, args)
	if fault != nil {
		return nil, fault
	}
	doc, ok := out.(map[string]any)
	require.True(t, ok, "the projection is a document")
	return doc, nil
}

func TestReviewChange_ProjectsFindingsWorstFirst(t *testing.T) {
	fake := &fakeReviewRunner{
		res: review.Result{
			Findings: []report.Finding{
				// A warn first, so a worst-first projection must reorder it below
				// the fail rather than preserve the input order.
				{Rule: "review.edge_case", Level: report.LevelWarn,
					Title: "loop excludes the last element", Detail: "i < n-1 drops index n-1",
					Fix: "use i < n", Where: "worker/pool.go:88", Count: 1},
				{Rule: "review.error_handling", Level: report.LevelFail,
					Title: "error is checked and dropped", Detail: "err is compared then ignored",
					Fix: "return err", Where: "api/handler.go:42", Count: 1},
			},
			Notes: []string{"a-generated-file.go was too large to show in full"},
		},
		meta: reviewMeta{TouchedCode: true, HadKey: true, FilesReviewed: 3},
	}

	doc, fault := callReview(t, fake.run, map[string]any{"project_id": "repo", "branch": "feature"})
	require.Nil(t, fault)

	require.Equal(t, "review_findings", doc["kind"])
	require.Equal(t, true, doc["reviewed"])
	require.Equal(t, 3, doc["files_reviewed"])
	require.Equal(t, "feature", fake.branch, "the branch argument reaches the runner")

	findings := doc["findings"].([]reviewFinding)
	require.Len(t, findings, 2)
	// Worst first: the fail leads, the warn follows.
	require.Equal(t, "review.error_handling", findings[0].Rule, "findings are worst first")
	require.Equal(t, "fail", findings[0].Level)
	require.Equal(t, "error_handling", findings[0].Category, "the category is read from the rule")
	require.Equal(t, "api/handler.go:42", findings[0].Where, "the file:line location survives")
	require.Equal(t, "review.edge_case", findings[1].Rule)
	require.Equal(t, "edge_case", findings[1].Category)

	totals := doc["totals"].(map[string]int)
	require.Equal(t, 1, totals["fail"])
	require.Equal(t, 1, totals["warn"])

	byCategory := doc["by_category"].(map[string]map[string]int)
	require.Equal(t, 1, byCategory["error_handling"]["fail"])
	require.Equal(t, 1, byCategory["edge_case"]["warn"])

	// The reviewer's own notes ride along.
	require.Contains(t, doc["notes"].([]string),
		"a-generated-file.go was too large to show in full")

	// The projection carries a boundary note and no field shaped like a captured
	// body, response or row: a review reads a diff, which has none.
	require.Contains(t, doc, "boundary_note")
	for _, forbidden := range []string{"body", "response", "responses", "row", "rows", "dom", "screenshot"} {
		require.NotContains(t, doc, forbidden, "the projection must never carry %q", forbidden)
	}
}

func TestReviewChange_DropsIgnoredFindings(t *testing.T) {
	fake := &fakeReviewRunner{
		res: review.Result{
			Findings: []report.Finding{
				{Rule: "review.correctness", Level: report.LevelFail,
					Title: "kept", Where: "a.go:1", Count: 1},
				// Silenced by the project's policy. Returning it would put back
				// exactly what the manifest turned off.
				{Rule: "review.dead_code", Level: report.LevelIgnore,
					Title: "silenced", Where: "b.go:2", Count: 1},
			},
		},
		meta: reviewMeta{TouchedCode: true, HadKey: true, FilesReviewed: 2},
	}

	doc, fault := callReview(t, fake.run, map[string]any{"project_id": "repo"})
	require.Nil(t, fault)

	findings := doc["findings"].([]reviewFinding)
	require.Len(t, findings, 1, "the ignored finding is dropped")
	require.Equal(t, "review.correctness", findings[0].Rule)
	totals := doc["totals"].(map[string]int)
	require.Equal(t, 1, totals["fail"])
	require.Equal(t, 0, totals["warn"])
}

func TestReviewChange_NoModelKeyIsSkippedNotClean(t *testing.T) {
	fake := &fakeReviewRunner{
		meta: reviewMeta{TouchedCode: true, HadKey: false, FilesReviewed: 0},
	}

	doc, fault := callReview(t, fake.run, map[string]any{"project_id": "repo"})
	require.Nil(t, fault)

	require.Equal(t, "review_findings", doc["kind"])
	require.Equal(t, false, doc["reviewed"], "a skipped review is not a clean bill")
	require.Empty(t, doc["findings"].([]reviewFinding), "no key means no findings")

	notes := doc["notes"].([]string)
	require.Contains(t, notes,
		"code review skipped: no model key configured. Set one with 'af model set' to "+
			"review the change's added lines for correctness defects.",
		"the honest no-key skip note appears")
}

func TestReviewChange_NotCodeChangeIsNothingToReview(t *testing.T) {
	fake := &fakeReviewRunner{
		meta: reviewMeta{TouchedCode: false},
	}

	doc, fault := callReview(t, fake.run, map[string]any{"project_id": "repo"})
	require.Nil(t, fault)

	require.Equal(t, "review_findings", doc["kind"])
	require.Equal(t, false, doc["reviewed"])
	require.Empty(t, doc["findings"].([]reviewFinding))
	require.Equal(t, 0, doc["files_reviewed"])
	require.Contains(t, doc["summary"].(string), "no code surfaces",
		"a docs-only change is reported as nothing to review, not a clean pass")
}

func TestReviewChange_RequiresTheProjectAssertion(t *testing.T) {
	fake := &fakeReviewRunner{}

	// A missing project_id is refused before the runner is ever called.
	_, fault := callReview(t, fake.run, map[string]any{})
	require.NotNil(t, fault, "project_id is required on every tool")
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Equal(t, "", fake.branch, "the runner is not called when the assertion fails")

	// A project_id naming a different repository is refused too.
	_, fault = callReview(t, fake.run, map[string]any{"project_id": "not-this-repo"})
	require.NotNil(t, fault)
	require.Equal(t, FaultProjectMismatch, fault.Code)
}

func TestReviewChange_UnreadableDiffIsRefusedNotEmpty(t *testing.T) {
	fake := &fakeReviewRunner{err: context.DeadlineExceeded}

	_, fault := callReview(t, fake.run, map[string]any{"project_id": "repo"})
	require.NotNil(t, fault, "a diff that could not be read is a refusal, not an empty review")
	require.Equal(t, FaultSafetyUnavailable, fault.Code)
}

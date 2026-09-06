package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The values a test plants when it wants to prove they never come back. They
// are deliberately distinctive, so that a NotContains over the encoded result
// is an assertion about the whole document rather than about one field.
const (
	realBefore = "SENTINEL-real-customer@example.invalid"
	realAfter  = "SENTINEL-masked-9f2a@example.invalid"
)

func maskTool(t *testing.T, readers maskingReaders) *Tool {
	t.Helper()
	return newInspectMaskingTool(evidenceProject(nil), readers)
}

func callMask(t *testing.T, readers maskingReaders, in map[string]any) (any, *Fault) {
	t.Helper()
	in["project_id"] = "test-project"
	return maskTool(t, readers).Handler(context.Background(), &Call{}, in)
}

// -----------------------------------------------------------------------
// the plan
// -----------------------------------------------------------------------

func samplePlan() *env.PlanResult {
	customers := masking.Table{
		Schema: "public", Name: "customers", Rows: 12000,
		PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"}, {Name: "email", Type: "text"},
		},
	}
	return &env.PlanResult{
		RulesHash: "abc123", Source: "this environment's branch",
		Plan: masking.Plan{
			RulesHash: "abc123",
			Tables: []masking.TablePlan{{
				Table:     customers,
				ChunkSize: masking.DefaultChunkSize,
				OrderBy:   []string{"id"},
				Columns: []masking.Assignment{{
					Table: customers, Column: customers.Columns[1],
					Transform: "email", Why: "the column is called email",
				}},
			}},
		},
	}
}

func planReaders(res *env.PlanResult, err error) maskingReaders {
	return maskingReaders{
		Plan: func(context.Context) (*env.PlanResult, error) { return res, err },
	}
}

func TestMaskPlan_ReportsWhichColumnGetsWhichTransform(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, planReaders(samplePlan(), nil), args("question", "plan"))
	require.Nil(t, fault)

	doc := out.(*maskingPlanDoc)
	require.Len(t, doc.Tables, 1)
	require.Equal(t, "email", doc.Tables[0].Columns[0].Transform)
}

func TestMaskPlan_NamesEveryColumnNoRuleCovers(t *testing.T) {
	t.Parallel()
	// Left alone, for a column called customer_notes, means the notes ship.
	// This list is the question somebody has to answer.
	res := samplePlan()
	res.Plan.Unclassified = []masking.Assignment{{
		Table:     res.Plan.Tables[0].Table,
		Column:    masking.ColumnInfo{Name: "customer_notes", Type: "text"},
		Unmatched: true, Why: "no rule named it",
	}}

	out, fault := callMask(t, planReaders(res, nil), args("question", "plan"))
	require.Nil(t, fault)

	doc := out.(*maskingPlanDoc)
	require.Len(t, doc.Unclassified, 1)
	require.Equal(t, "customer_notes", doc.Unclassified[0].Column)
}

func TestMaskPlan_APlanThatWillNotRunIsInconclusiveAndNotAPass(t *testing.T) {
	t.Parallel()
	// A plan with unresolved problems is refused rather than partly applied,
	// so reporting it as a pass would be the opposite of what happens next.
	res := samplePlan()
	res.Plan.Problems = []masking.Assignment{{
		Table:   res.Plan.Tables[0].Table,
		Column:  masking.ColumnInfo{Name: "email", Unique: true},
		Problem: "the transform does not preserve uniqueness",
	}}

	out, fault := callMask(t, planReaders(res, nil), args("question", "plan"))
	require.Nil(t, fault)

	doc := out.(*maskingPlanDoc)
	require.Equal(t, VerdictInconclusive, doc.Verdict)
}

func TestMaskPlan_SaysWhichDatabaseTheSchemaCameFrom(t *testing.T) {
	t.Parallel()
	// A plan is about a schema, and which schema is not obvious when there are
	// two it could have been.
	out, fault := callMask(t, planReaders(samplePlan(), nil), args("question", "plan"))
	require.Nil(t, fault)

	doc := out.(*maskingPlanDoc)
	require.Equal(t, "this environment's branch", doc.Source)
}

func TestMaskPlan_ReportsATableWithNoPrimaryKeyAsUnchunked(t *testing.T) {
	t.Parallel()
	// Without a key there is no stable order, so there is no way to resume and
	// no way to be sure every row was covered exactly once. The plan says so
	// rather than pretending it was chunked.
	res := samplePlan()
	res.Plan.Tables[0].ChunkSize = 0

	out, fault := callMask(t, planReaders(res, nil), args("question", "plan"))
	require.Nil(t, fault)

	doc := out.(*maskingPlanDoc)
	require.False(t, doc.Tables[0].Chunked)
}

func TestMaskPlan_AFailureToBuildThePlanIsRefusedRatherThanReportedAsNothingToMask(t *testing.T) {
	t.Parallel()
	_, fault := callMask(t, planReaders(nil, errors.New("no branch and no source")),
		args("question", "plan"))

	require.NotNil(t, fault)
	require.Equal(t, FaultSafetyUnavailable, fault.Code)
}

// -----------------------------------------------------------------------
// the sample
// -----------------------------------------------------------------------

func sampleReaders(rows [][]env.PreviewRow, err error) maskingReaders {
	return maskingReaders{
		Sample: func(context.Context, string, int) ([][]env.PreviewRow, error) {
			return rows, err
		},
	}
}

func TestMaskSample_NeverReturnsTheValueItWasDecidingAbout(t *testing.T) {
	t.Parallel()
	// The privacy boundary this whole file exists for. A preview that showed
	// the values would leak exactly the data masking removes, to a model,
	// into a transcript.
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "email", Before: realBefore, After: realAfter},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	require.NotContains(t, jsonOf(t, out), realBefore)
}

func TestMaskSample_DoesNotReturnTheMaskedValueEither(t *testing.T) {
	t.Parallel()
	// The after value is not safe by construction: a column no rule covers is
	// passed through, so the after value is sometimes the before value.
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "email", Before: realBefore, After: realAfter},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	require.NotContains(t, jsonOf(t, out), realAfter)
}

func TestMaskSample_AColumnARuleDidNotChangeFails(t *testing.T) {
	t.Parallel()
	// The failure this question exists to find. A rule that names a column and
	// then leaves it exactly as it was is invisible in a plan, which says what
	// was assigned rather than what happened.
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "email", Before: realBefore, After: realBefore},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.Equal(t, VerdictFail, doc.Verdict)
}

func TestMaskSample_AColumnThatChangedPasses(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "email", Before: realBefore, After: realAfter},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.Equal(t, VerdictPass, doc.Verdict)
}

func TestMaskSample_ARowThatWasAlreadyEmptyIsNotCountedAsUnchanged(t *testing.T) {
	t.Parallel()
	// There was nothing to mask, so an unchanged value there is not evidence
	// of a rule failing, and counting it as one would cry wolf on every
	// nullable column.
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "middle_name", Before: "", After: ""},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.Equal(t, 0, doc.Columns[0].Unchanged)
}

func TestMaskSample_AValueEmptiedFromSomethingIsReportedSeparately(t *testing.T) {
	t.Parallel()
	// What a transform failing on a column's type looks like.
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "email", Before: realBefore, After: ""},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.Equal(t, 1, doc.Columns[0].EmptiedFromNonEmpty)
}

func TestMaskSample_NoRowsIsInconclusiveAndNotAPass(t *testing.T) {
	t.Parallel()
	// Nothing was transformed, so nothing was shown to work.
	out, fault := callMask(t, sampleReaders(nil, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.Equal(t, VerdictInconclusive, doc.Verdict)
}

func TestMaskSample_AcrossRowsTheCountsAccumulatePerColumn(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{
		{{Column: "email", Before: realBefore, After: realAfter}},
		{{Column: "email", Before: "second@example.invalid", After: "second@example.invalid"}},
	}, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.Len(t, doc.Columns, 1)
	require.Equal(t, 1, doc.Columns[0].Unchanged,
		"a transform that fires on one row and not another is exactly what to surface")
}

func TestMaskSample_StatesThatValuesWereWithheld(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, sampleReaders([][]env.PreviewRow{{
		{Column: "email", Before: realBefore, After: realAfter},
	}}, nil), args("question", "sample"))
	require.Nil(t, fault)

	doc := out.(*maskingSampleDoc)
	require.True(t, doc.ValuesWithheld)
}

func TestMaskSample_TheRowsArgumentIsBounded(t *testing.T) {
	t.Parallel()
	// More rows do not show more values, because no value is returned. The
	// bound is there so a caller cannot ask the database for a table.
	tool := maskTool(t, maskingReaders{})
	body, err := json.Marshal(map[string]any{
		"project_id": "test-project", "question": "sample", "rows": 10_000,
	})
	require.NoError(t, err)

	_, fault := validateArguments(tool.Input, body)
	require.NotNil(t, fault)
	require.Equal(t, FaultArgumentTooLarge, fault.Code)
}

// -----------------------------------------------------------------------
// the verification
// -----------------------------------------------------------------------

func verifyReaders(rep verify.Report, err error) maskingReaders {
	return maskingReaders{
		Verify: func(context.Context) (verify.Report, error) { return rep, err },
	}
}

func TestMaskVerify_AColumnThatStillLooksRealFails(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, verifyReaders(verify.Report{
		Tables: 4, Columns: 40, SampleSize: 2000, RowsSampled: 80000,
		Findings: []verify.Finding{{
			Schema: "public", Table: "customers", Column: "email",
			Detector: "email address", Rows: 7, Example: realBefore,
		}},
	}, nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Equal(t, VerdictFail, doc.Verdict)
}

func TestMaskVerify_TheRedactedExcerptIsWithheldToo(t *testing.T) {
	t.Parallel()
	// The scanner keeps an excerpt of each value it recognised, and an
	// excerpt of real data is real data. The CLI prints it; this does not.
	out, fault := callMask(t, verifyReaders(verify.Report{
		Findings: []verify.Finding{{
			Schema: "public", Table: "customers", Column: "email",
			Detector: "email address", Rows: 7, Example: realBefore,
		}},
	}, nil), args("question", "verify"))
	require.Nil(t, fault)

	require.NotContains(t, jsonOf(t, out), realBefore)
}

func TestMaskVerify_TheColumnAndDetectorSurviveSoTheFindingCanBeActedOn(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, verifyReaders(verify.Report{
		Findings: []verify.Finding{{
			Schema: "public", Table: "customers", Column: "email",
			Detector: "email address", Rows: 7, Example: realBefore,
		}},
	}, nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Len(t, doc.Findings, 1)
	require.Equal(t, "email address", doc.Findings[0].Detector)
}

func TestMaskVerify_AColumnNobodyCouldReadIsNotAColumnThatPassed(t *testing.T) {
	t.Parallel()
	// A scan that cannot read a column is a scan whose answer is that it does
	// not know, and not knowing fails rather than passes.
	out, fault := callMask(t, verifyReaders(verify.Report{
		Tables: 4, Columns: 40, SampleSize: 2000,
		Skipped: []string{"public.blobs.payload could not be read"},
	}, nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Equal(t, VerdictInconclusive, doc.Verdict)
}

func TestMaskVerify_ACleanScanPasses(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, verifyReaders(verify.Report{
		Tables: 4, Columns: 40, SampleSize: 2000, RowsSampled: 80000,
	}, nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Equal(t, VerdictPass, doc.Verdict)
}

func TestMaskVerify_ReportsTheSampleSizeSoCleanHasAScope(t *testing.T) {
	t.Parallel()
	// Clean covers what was sampled and nothing more, and a reader who does
	// not know the sample size does not know what clean covered.
	out, fault := callMask(t, verifyReaders(verify.Report{
		Tables: 4, Columns: 40, SampleSize: 2000,
	}, nil), args("question", "verify"))
	require.Nil(t, fault)

	doc := out.(*maskingVerifyDoc)
	require.Equal(t, 2000, doc.SampleSize)
}

func TestMaskVerify_AScanThatCouldNotRunIsRefusedRatherThanReportedClean(t *testing.T) {
	t.Parallel()
	_, fault := callMask(t, verifyReaders(verify.Report{}, errors.New("no environment")),
		args("question", "verify"))

	require.NotNil(t, fault)
	require.Equal(t, FaultSafetyUnavailable, fault.Code)
}

// -----------------------------------------------------------------------
// the read only tool as a whole
// -----------------------------------------------------------------------

func TestInspectMasking_IsMarkedReadOnly(t *testing.T) {
	t.Parallel()
	require.True(t, maskTool(t, maskingReaders{}).ReadOnly)
}

func TestInspectMasking_RefusesAQuestionItDoesNotHave(t *testing.T) {
	t.Parallel()
	// Notably including anything that sounds like applying.
	tool := maskTool(t, maskingReaders{})
	for _, q := range []string{"apply", "run", "mask", "delete", ""} {
		body, err := json.Marshal(map[string]any{
			"project_id": "test-project", "question": q,
		})
		require.NoError(t, err)
		_, fault := validateArguments(tool.Input, body)
		require.NotNil(t, fault, "the question %q must be refused", q)
	}
}

// -----------------------------------------------------------------------
// apply_data_masking
// -----------------------------------------------------------------------

func applyTool(t *testing.T, apply maskingApplier) (*Tool, *Project) {
	t.Helper()
	p := evidenceProject(nil)
	store, _ := newStore(t)
	eng := NewEngine(context.Background(), p, store, nil)
	t.Cleanup(eng.Wait)
	return newApplyMaskingTool(p, eng, apply), p
}

func TestApplyMasking_IsNotMarkedReadOnly(t *testing.T) {
	t.Parallel()
	// It rewrites every masked column of every masked table in place. A client
	// deciding what to prompt for reads this field.
	tool, _ := applyTool(t, nil)

	require.False(t, tool.ReadOnly)
}

func TestApplyMasking_SaysItIsIrreversibleBeforeAnythingElse(t *testing.T) {
	t.Parallel()
	// The description is what a model reads when it is choosing. The word has
	// to be the first thing in it, not a caveat at the end.
	tool, _ := applyTool(t, nil)

	require.True(t, len(tool.Description) > 12)
	require.Equal(t, "IRREVERSIBLE", tool.Description[:12])
}

func TestApplyMasking_PointsAtTheReadOnlyToolByName(t *testing.T) {
	t.Parallel()
	// A caller that meant to preview has to be told what to call instead, in
	// the place it is already reading.
	tool, _ := applyTool(t, nil)

	require.Contains(t, tool.Description, "inspect_data_masking")
}

func TestApplyMasking_WithoutTheAcknowledgementNothingIsApplied(t *testing.T) {
	t.Parallel()
	// The assertion that matters is not the refusal, it is that the applier
	// was never reached.
	called := false
	tool, p := applyTool(t, func(context.Context) (masking.Result, error) {
		called = true
		return masking.Result{}, nil
	})

	_, fault := tool.Handler(context.Background(), &Call{Caller: "test"},
		args("project_id", p.ID, "acknowledge_irreversible", "yes"))
	require.NotNil(t, fault)
	require.False(t, called, "the data must not be rewritten by a call that was refused")
}

func TestApplyMasking_TheAcknowledgementHasExactlyOneAcceptedValue(t *testing.T) {
	t.Parallel()
	// An enum of one makes reaching this tool a deliberate act. A model that
	// meant to preview cannot satisfy it by accident.
	tool, _ := applyTool(t, nil)

	require.Equal(t,
		[]string{applyAcknowledgement},
		tool.Input.Properties["acknowledge_irreversible"].Enum)
}

func TestApplyMasking_TheAcknowledgementIsRequiredBySchemaAsWellAsByCode(t *testing.T) {
	t.Parallel()
	// Two mechanisms on purpose. A required irreversible action is the wrong
	// place to depend on exactly one.
	tool, _ := applyTool(t, nil)
	body, err := json.Marshal(map[string]any{"project_id": "test-project"})
	require.NoError(t, err)

	_, fault := validateArguments(tool.Input, body)
	require.NotNil(t, fault)
	require.Equal(t, "acknowledge_irreversible", fault.Field)
}

func TestApplyMasking_WithTheAcknowledgementItSubmitsARun(t *testing.T) {
	t.Parallel()
	tool, p := applyTool(t, func(context.Context) (masking.Result, error) {
		return masking.Result{Tables: 3, Rows: 900}, nil
	})

	out, fault := tool.Handler(context.Background(), &Call{Caller: "test"},
		args("project_id", p.ID, "acknowledge_irreversible", applyAcknowledgement))
	require.Nil(t, fault)

	ack, ok := out.(submission)
	require.True(t, ok, "an irreversible rewrite takes minutes and must be polled, not awaited")
	require.NotEmpty(t, ack.RunID)
}

// runMaskApplyFor drives the experiment body against a real run row.
func runMaskApplyFor(
	t *testing.T, res masking.Result, err error,
) (string, *ResultBody, *Fault) {
	t.Helper()
	p := evidenceProject(nil)
	store, _ := newStore(t)
	eng := NewEngine(context.Background(), p, store, nil)
	run, _, fault := store.Submit(context.Background(), "test", p.ID,
		"apply_data_masking", "", args())
	require.Nil(t, fault)

	return runMaskApply(context.Background(), eng,
		func(context.Context) (masking.Result, error) { return res, err }, run.ID)
}

func TestApplyMasking_TheResultTellsTheCallerToVerifyAfterwards(t *testing.T) {
	t.Parallel()
	// Masking that is not checked is masking somebody believes in, and this
	// result is the last moment anybody is looking.
	_, body, fault := runMaskApplyFor(t, masking.Result{
		Tables: 3, Rows: 900, Duration: 2 * time.Second,
	}, nil)
	require.Nil(t, fault)

	require.Contains(t, body.Summary, "not proof that the data is now safe")
}

func TestApplyMasking_TheResultReportsCountsAndNoValues(t *testing.T) {
	t.Parallel()
	_, body, fault := runMaskApplyFor(t, masking.Result{
		Tables: 3, Rows: 900, Duration: 2 * time.Second,
	}, nil)
	require.Nil(t, fault)

	doc, ok := body.Detail.(*maskApplyDoc)
	require.True(t, ok)
	require.Equal(t, int64(900), doc.Rows)
}

func TestApplyMasking_ARefusedPlanIsReportedAsNothingWritten(t *testing.T) {
	t.Parallel()
	// The executor refuses a plan with problems before it writes anything, so
	// this is not a half applied table and the caller has to be told so.
	_, _, fault := runMaskApplyFor(t, masking.Result{},
		errors.New("the plan has 2 problems and will not be run"))

	require.NotNil(t, fault)
	require.Contains(t, fault.Detail, "before anything is written")
}

func TestApplyMasking_AResumedRunSaysItsCountsAreNotTheWholeTable(t *testing.T) {
	t.Parallel()
	_, body, fault := runMaskApplyFor(t, masking.Result{
		Tables: 1, Rows: 100, Resumed: true, Duration: time.Second,
	}, nil)
	require.Nil(t, fault)

	require.Contains(t, body.Summary, "resumed from a checkpoint")
}

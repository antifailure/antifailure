package pgcrash_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The invariant arm is judged from values here, the way the rest of this
// package's judgement is, because the three answers it has to keep apart do
// not all arrive from a healthy machine. A database that never came back and a
// statement the server refused are both ordinary in the field and neither
// happens on a laptop with a working Postgres, so the branches that read them
// would otherwise never run.

// held and violated are the two sides an invariant can come back with, and
// unasked is the third answer that is neither.
func held() pgcrash.InvariantSide { return pgcrash.InvariantSide{Held: true} }

func violated(rows int, more bool) pgcrash.InvariantSide {
	out := pgcrash.InvariantSide{Columns: []string{"id"}, More: more}
	for i := 0; i < rows; i++ {
		out.Rows = append(out.Rows, []string{"row"})
	}
	return out
}

func unasked(why string) pgcrash.InvariantSide { return pgcrash.InvariantSide{Error: why} }

// judged runs the production judgement over one invariant and returns the
// rules it raised, split the way a caller reads them.
func judged(t *testing.T, c pgcrash.InvariantCheck) (problems, unverified []string) {
	t.Helper()
	r := &pgcrash.Result{Invariants: []pgcrash.InvariantCheck{c}}
	pgcrash.JudgeForTest(r, nil, nil)
	return invariantRulesOf(r.Problems), invariantRulesOf(r.Unverified)
}

// invariantRulesOf keeps only this arm's rules, because judging a zero valued
// result also raises the checksums, amcheck and ledger entries that every
// other arm raises when it was handed nothing.
func invariantRulesOf(ps []pgcrash.Problem) []string {
	var out []string
	for _, p := range ps {
		if strings.HasPrefix(p.Rule, "chaos.invariant.") {
			out = append(out, p.Rule)
		}
	}
	return out
}

// detailOf is the one detail this arm wrote, for the assertions about what a
// reader is told.
func detailOf(t *testing.T, c pgcrash.InvariantCheck) string {
	t.Helper()
	r := &pgcrash.Result{Invariants: []pgcrash.InvariantCheck{c}}
	pgcrash.JudgeForTest(r, nil, nil)
	for _, p := range append(append([]pgcrash.Problem{}, r.Problems...), r.Unverified...) {
		if strings.HasPrefix(p.Rule, "chaos.invariant.") {
			return p.Detail
		}
	}
	return ""
}

// TestJudgeInvariants_TheThreeAnswersAreKeptApart is the whole point of asking
// an invariant twice.
//
// An invariant that was already broken before anything was touched is not
// something the fault did, and a check that only looked afterwards would
// report a project's own pre-existing defect as a durability failure caused by
// the crash. So each ordering gets its own row, and the one that is a FAILURE
// is the one and only ordering the run can attribute to the fault.
func TestJudgeInvariants_TheThreeAnswersAreKeptApart(t *testing.T) {
	for name, tc := range map[string]struct {
		check          pgcrash.InvariantCheck
		wantProblem    []string
		wantUnverified []string
	}{
		"held before and violated after is the fault breaking it": {
			check:       pgcrash.InvariantCheck{Name: "balances", Before: held(), After: violated(3, false)},
			wantProblem: []string{pgcrash.RuleInvariantBroken},
		},
		"violated before and after is not attributable to the fault": {
			check:          pgcrash.InvariantCheck{Name: "balances", Before: violated(2, false), After: violated(2, false)},
			wantUnverified: []string{pgcrash.RuleInvariantAlreadyViolated},
		},
		"violated before and held after is still not attributable": {
			check:          pgcrash.InvariantCheck{Name: "balances", Before: violated(2, false), After: held()},
			wantUnverified: []string{pgcrash.RuleInvariantAlreadyViolated},
		},
		"held on both sides is nothing to report": {
			check: pgcrash.InvariantCheck{Name: "balances", Before: held(), After: held()},
		},
		"not asked before the fault is unverified": {
			check:          pgcrash.InvariantCheck{Name: "balances", Before: unasked("the statement timed out"), After: held()},
			wantUnverified: []string{pgcrash.RuleInvariantUnevaluated},
		},
		"not asked after the recovery is unverified": {
			check:          pgcrash.InvariantCheck{Name: "balances", Before: held(), After: unasked("the database did not answer")},
			wantUnverified: []string{pgcrash.RuleInvariantUnevaluated},
		},
		"not asked after the recovery beats a violation before it": {
			// The before side found rows and the after side never ran. The
			// answer is "I could not look", not "it was already broken":
			// ordering the branches the other way would report a rule as
			// inherited when nobody established that it still is.
			check:          pgcrash.InvariantCheck{Name: "balances", Before: violated(1, false), After: unasked("the database did not answer")},
			wantUnverified: []string{pgcrash.RuleInvariantUnevaluated},
		},
		"not asked on either side is unverified once": {
			check: pgcrash.InvariantCheck{Name: "balances",
				Before: unasked("connecting to the database to ask it: refused"),
				After:  unasked("connecting to the database to ask it: refused")},
			wantUnverified: []string{pgcrash.RuleInvariantUnevaluated},
		},
	} {
		t.Run(name, func(t *testing.T) {
			problems, unverified := judged(t, tc.check)
			require.Equal(t, tc.wantProblem, problems, "the failing side of the judgement")
			require.Equal(t, tc.wantUnverified, unverified, "the could not look side of the judgement")
		})
	}
}

// TestJudgeInvariants_OnlyTheAttributableOneFailsTheRun holds the level, which
// is the fact a merge is stopped by.
//
// A rule the run inherited broken must not stop a merge, because the change
// under test did not break it and a gate that fails on it teaches a project to
// switch the whole arm off. Result.Held is what a caller reads for that.
func TestJudgeInvariants_OnlyTheAttributableOneFailsTheRun(t *testing.T) {
	broke := &pgcrash.Result{Invariants: []pgcrash.InvariantCheck{
		{Name: "balances", Before: held(), After: violated(1, false)},
	}}
	pgcrash.JudgeForTest(broke, nil, nil)
	require.False(t, broke.Held(), "an invariant the fault broke did not fail the run")

	for name, c := range map[string]pgcrash.InvariantCheck{
		"already violated": {Name: "balances", Before: violated(1, false), After: violated(1, false)},
		"never asked":      {Name: "balances", Before: held(), After: unasked("the database did not answer")},
	} {
		t.Run(name, func(t *testing.T) {
			r := &pgcrash.Result{Invariants: []pgcrash.InvariantCheck{c}}
			pgcrash.JudgeForTest(r, nil, nil)
			require.True(t, r.Held(), "a finding this run cannot attribute to the fault failed the run")
			require.False(t, r.Verified(), "a run that could not look reported itself as verified")
		})
	}
}

// TestJudgeInvariants_ADeclaredNothingAddsNothing is the requirement that a
// project which declares no invariants sees no change at all.
//
// Compared against the same judgement with the arm absent rather than against
// a hardcoded list, so the assertion stays true as the other arms change.
func TestJudgeInvariants_ADeclaredNothingAddsNothing(t *testing.T) {
	bare := &pgcrash.Result{}
	pgcrash.JudgeForTest(bare, nil, nil)
	empty := &pgcrash.Result{Invariants: []pgcrash.InvariantCheck{}}
	pgcrash.JudgeForTest(empty, nil, nil)

	require.Equal(t, len(bare.Problems), len(empty.Problems))
	require.Equal(t, len(bare.Unverified), len(empty.Unverified))
	require.Empty(t, invariantRulesOf(bare.Unverified), "an invariant rule fired with no invariants declared")
	require.Empty(t, invariantRulesOf(bare.Problems), "an invariant rule fired with no invariants declared")
}

// TestJudgeInvariants_TheDetailSaysWhichSideAndHowMany is what a reader acts
// on. A finding that says an invariant is broken and neither which side could
// not be asked nor how many rows are wrong is a finding somebody has to
// reproduce by hand before they can start.
func TestJudgeInvariants_TheDetailSaysWhichSideAndHowMany(t *testing.T) {
	broke := detailOf(t, pgcrash.InvariantCheck{
		Name: "balances", Description: "no account may go negative",
		Before: held(), After: violated(3, false),
	})
	require.Contains(t, broke, "3 rows violate it", "the reader is not told how many rows are wrong")
	require.Contains(t, broke, "no account may go negative",
		"the manifest's own words for what the rule means were dropped")

	// The bound is at the server, so the kept count is a floor and never a
	// total. Printing it as a total would understate a check that matched a
	// million rows by a million.
	many := detailOf(t, pgcrash.InvariantCheck{Name: "balances", Before: held(), After: violated(5, true)})
	require.Contains(t, many, "more than 5 rows violate it", "a bounded count was printed as a total")

	// Which side could not be asked, because the two mean different things:
	// one is a rule that was never established as holding in the first place,
	// the other is a database that did not come back.
	before := detailOf(t, pgcrash.InvariantCheck{
		Name: "balances", Before: unasked("the statement timed out"), After: held()})
	require.Contains(t, before, "before the fault")
	require.Contains(t, before, "the statement timed out")

	after := detailOf(t, pgcrash.InvariantCheck{
		Name: "balances", Before: held(), After: unasked("the database did not answer a query after the fault")})
	require.Contains(t, after, "after the recovery")
	require.Contains(t, after, "the database did not answer a query after the fault")

	// A rule that was broken before and holds now is still not attributable,
	// and the sentence has to say what actually happened rather than repeat
	// the violated wording, because rows that disappeared across a crash are
	// their own thing to look at.
	gone := detailOf(t, pgcrash.InvariantCheck{Name: "balances", Before: violated(2, false), After: held()})
	require.Contains(t, gone, "no longer there")
}

// TestInvariantSide_AnErrorIsNotAViolation holds the distinction at the
// smallest place it exists. A side with no verdict leaves Held false, and a
// caller that read Held alone would report every timeout as a broken rule.
func TestInvariantSide_AnErrorIsNotAViolation(t *testing.T) {
	e := unasked("the statement timed out")
	require.False(t, e.Held)
	require.False(t, e.Evaluated())
	require.False(t, e.Violated(), "a check that could not run was reported as a violation")

	v := violated(1, false)
	require.True(t, v.Evaluated())
	require.True(t, v.Violated())

	h := held()
	require.True(t, h.Evaluated())
	require.False(t, h.Violated())
}

// TestPairInvariants_AMissingSideIsNotAMissingInvariant is the shape of a run
// that stopped partway.
//
// An invariant left out of the arm reads as an invariant with nothing to
// report, which is the same defect this whole feature exists to catch: a check
// that did not happen looking exactly like one that passed. So a short side is
// filled with "not asked" and the invariant stays in the list.
func TestPairInvariants_AMissingSideIsNotAMissingInvariant(t *testing.T) {
	invs := []schema.Invariant{
		{Name: "balances", SQL: "SELECT 1"},
		{Name: "orders", SQL: "SELECT 1"},
	}
	checks := pgcrash.PairInvariantsForTest(invs, []pgcrash.InvariantSide{held()}, nil)
	require.Len(t, checks, 2, "an invariant went missing from the arm")
	require.Equal(t, "orders", checks[1].Name)
	require.False(t, checks[0].After.Evaluated(), "a side nobody produced was read as a verdict")
	require.False(t, checks[1].Before.Evaluated(), "a side nobody produced was read as a verdict")

	r := &pgcrash.Result{Invariants: checks}
	pgcrash.JudgeForTest(r, nil, nil)
	require.Equal(t,
		[]string{pgcrash.RuleInvariantUnevaluated, pgcrash.RuleInvariantUnevaluated},
		invariantRulesOf(r.Unverified))

	// And nothing at all when nothing was declared, which is the path most
	// manifests take.
	require.Nil(t, pgcrash.PairInvariantsForTest(nil, nil, nil))
}

package conformance_test

import (
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
)

// The third verdict, proved in every direction it can go, and two of those
// directions are the ones that would otherwise ship silently.
//
// A verdict with three answers needs proof in all of them, and the risk of
// adding an answer to an instrument is that the answer becomes the one it
// always gives. A suite that could no longer refuse a false copy on write claim
// would be worse than the one before this lane, because it would look like the
// one after it. So the cells that must still decide are here beside the ones
// that must not.
//
//	asserts a real service   declares   verdict
//	no                       true       UNPROVEN
//	no                       false      UNPROVEN, and NOT the comfortable pass
//	nothing set at all       either     UNPROVEN, because absence is the input
//	yes, a real Postgres     true       decided: REFUTED when it really copies
//	yes, a real Postgres     false      decided: PROVED when it really copies
//
// The last two are what stops this being a switch that turns the instrument
// off. The two credential free provider suites in the repository sit on
// opposite sides of the assertion, docker declaring true against a real daemon
// and pgurl declaring false against a real Postgres, and both are asserted real
// in their own files so neither moves to unproven. They are checked by CI
// rather than restated here.
//
// WHAT THESE RUN AT, and why the first answer to that was wrong.
//
// The children that still take a measurement run at the SHIPPED sizes and set
// no override. They were first run at the behaviour's floor, 64 MiB against
// 4 MiB, to save time; it passed on the machine it was written on and CI
// refused it, because CI's storage copied the 60 MiB of extra data in 163ms,
// inside the 250ms the allowance gives to noise. Nothing was distinguishable.
// cow.go says this in advance about the size rather than about these tests, and
// db_selftest_test.go already warns beside the fault table that a self test run
// at sizes the suite does not ship proves the faults are catchable at SOME
// configuration. The economy was buying exactly that weaker sentence.
//
// The unproven children take no measurement at all, so they cost nothing and
// the size is not a question for them.

const cowBehavior = "CopyOnWrite_BranchTimeMatchesTheDeclaration"

// The lines a reader has to see, checked as literals because each is a separate
// way the verdict could go invisible, and invisible is the failure mode that
// matters.
const (
	unprovenSubtestLine = "UNPROVEN. " + cowBehavior + " is not decided by this run"
	unprovenReportLine  = "NOT PROVED BY THIS RUN. This is not a pass."
)

func requireCopyOnWritePostgres(t *testing.T) {
	t.Helper()
	if !postgresReachable(t) {
		t.Skipf("skipped: this is a claim about moving bytes and needs a Postgres at %s",
			postgresURL())
	}
}

// requireUnproven insists a child reached the third verdict and said so in
// every place a reader looks.
func requireUnproven(t *testing.T, passed bool, out string) {
	t.Helper()
	if !passed {
		t.Fatalf("the child FAILED where it should have reported unproven. A run that "+
			"cannot decide a claim must not report it as refuted either.\n%s", out)
	}
	for _, want := range []string{unprovenSubtestLine, unprovenReportLine, "UNPROVEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run reached the third verdict and its output does not contain %q, "+
				"so a reader cannot tell it from a pass.\n%s", want, out)
		}
	}
	// The word that must never appear for this, because the existing skip line
	// reads "skipped: <provider> does not declare <capability>" and the two are
	// different claims about different things.
	if strings.Contains(out, "skipped: "+cowBehavior) ||
		strings.Contains(out, "--- SKIP: TestDatabaseSuiteChild/"+cowBehavior) {
		t.Errorf("the third verdict was reported as a SKIP. A skip says this provider makes "+
			"no such claim; an unproven says it makes the claim and this run could not "+
			"reach it, and a reader who confuses them reads an unmeasured commercial claim "+
			"as a supported one.\n%s", out)
	}
	// It cost nothing, which is the other half of "the measurement was not
	// taken". A run that built two goldens and then discarded the readings
	// would be paying half a gibibyte for an answer it already had.
	if strings.Contains(out, "copy on write, measured") {
		t.Errorf("the unproven run took the measurement anyway. It cannot change the "+
			"verdict, and publishing what a simulator timed invites somebody to quote it "+
			"as though it were about the product.\n%s", out)
	}
}

// TestATrueDeclarationOnASimulatorIsUnproven is the first direction, and the one
// the ruling was written from.
func TestATrueDeclarationOnASimulatorIsUnproven(t *testing.T) {
	requireCopyOnWritePostgres(t)
	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior,
		fault: fakes.CopyOnWriteThatCopies, asSimulator: true,
	})
	requireUnproven(t, passed, out)
	t.Logf("the report the run printed, which is the thing a reader sees:\n%s",
		unprovenBlock(out))
}

// TestAFalseDeclarationOnASimulatorIsUnprovenAndNotAPass is the direction the
// first version of this design missed, and it is the dangerous one.
//
// The assertion is two sided and false requires the branch time to GROW with the
// data, which against a harness that copies every branch holds comfortably. So
// every snapshot restore provider in the wave would have collected a GREEN copy
// on write gate for a reason that has nothing to do with the service it ships
// against, and nobody would ever have reread it. A verdict raised only on a red
// fixes the visible half of the problem and leaves this one in place.
func TestAFalseDeclarationOnASimulatorIsUnprovenAndNotAPass(t *testing.T) {
	requireCopyOnWritePostgres(t)
	// The plain Postgres backed provider declares CopyOnWrite false, honestly,
	// because it copies. On a real service that is a decidable claim and the
	// control below decides it; here nothing asserts a real service.
	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior, asSimulator: true,
	})
	requireUnproven(t, passed, out)
	// The comfortable pass, named by what it would have LOOKED like rather than
	// by the subtest's own result, and the difference matters enough to say.
	//
	// Go has two states. The subtest reports PASS here and must, because the
	// alternative is FAIL and a run that could not decide a claim has not
	// refuted it either. So "--- PASS" is not the thing to assert on: it is
	// present for an unproven verdict and for a real one alike, which is
	// exactly why the third answer had to be carried somewhere Go's result
	// cannot reach. That somewhere is the output, the end of run report, the
	// ledger and the published cell, and requireUnproven has just checked all
	// four.
	//
	// What separates the two is whether a MEASUREMENT decided it. A false
	// declaration that collected the comfortable green did so by measuring a
	// copying fake and finding the growth it wanted; the assertion below is
	// that no such reading exists. This was first written as a check on
	// "--- PASS" and that check could never have passed, which is its own small
	// lesson: an assertion nobody has watched succeed is as unproved as one
	// nobody has watched fail.
	for _, forbidden := range []string{"marginal cost", "this run could refuse", "min "} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a false declaration on a simulator produced %q, so a measurement "+
				"decided it after all. That reading is about the fake and the cell it "+
				"would certify is about somebody's product.\n%s", forbidden, out)
		}
	}
}

// TestAHarnessThatAssertsNothingIsUnproven is the third direction, and it is
// about the DEFAULT rather than about a declaration.
//
// It is nearly the same run as the two above and it is a separate test on
// purpose. Those two set a field to withhold the assertion, which is an act.
// This one is the case where nobody ever heard of the field, which is what the
// next lane writing a control plane will actually do, and the whole inversion
// exists so that forgetting produces the safe answer. If this ever passes, the
// mechanism has the defect it was built to remove.
func TestAHarnessThatAssertsNothingIsUnproven(t *testing.T) {
	requireCopyOnWritePostgres(t)
	passed, out := runChild(t, child{
		backend: inMemory, behavior: cowBehavior, asSimulator: true,
	})
	requireUnproven(t, passed, out)
}

// TestARealServiceStillDecidesARefutation is the first control.
//
// A provider that declares copy on write and copies every byte, on a run that
// asserts its storage is real, is still REFUTED. If this direction breaks, the
// third verdict has disarmed the two sided assertion and every provider in the
// wave could declare copy on write untested.
func TestARealServiceStillDecidesARefutation(t *testing.T) {
	requireCopyOnWritePostgres(t)
	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior, fault: fakes.CopyOnWriteThatCopies,
	})
	if passed {
		t.Fatalf("the suite PASSED a provider that declares CopyOnWrite and copies every "+
			"byte, on a run asserting a real service. The instrument is disarmed.\n%s", out)
	}
	requireFailedIn(t, out, cowBehavior)
	if strings.Contains(out, "UNPROVEN") {
		t.Fatalf("the run asserted a real service and reached the third verdict anyway, so "+
			"the verdict is not gated on the assertion at all.\n%s", out)
	}
}

// TestARealServiceStillDecidesAPass is the second control, on the other side.
//
// The same provider with the honest false declaration, on a run asserting a real
// service, still PASSES. Without this the red above would be attributable to the
// assertion rather than to the fault.
func TestARealServiceStillDecidesAPass(t *testing.T) {
	requireCopyOnWritePostgres(t)
	passed, out := runChild(t, child{backend: onPG, behavior: cowBehavior})
	if !passed {
		t.Fatalf("the suite FAILED a provider that copies and honestly declares CopyOnWrite "+
			"false, on a run asserting a real service.\n%s", out)
	}
	if strings.Contains(out, "UNPROVEN") || strings.Contains(out, unprovenReportLine) {
		t.Fatalf("a run asserting a real service reached the third verdict.\n%s", out)
	}
}

// TestCopyOnWriteUnderstatedIsStillDetectable is the sixth proof, and it is
// about the fault catalogue rather than about this mechanism.
//
// The catalogue names both mistakes available here, CopyOnWriteThatCopies and
// CopyOnWriteUnderstated, and both are marked proved able to fail. So flipping a
// declaration to false to get past the gate is not a way around the instrument,
// it is walking into the second named fault. Whatever the third verdict does, it
// must leave that one catchable, or the cheapest way out of an unproven cell
// becomes a lie the suite can no longer see.
func TestCopyOnWriteUnderstatedIsStillDetectable(t *testing.T) {
	requireCopyOnWritePostgres(t)
	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior,
		flatBranch: true, fault: fakes.CopyOnWriteUnderstated,
	})
	if passed {
		t.Fatalf("the suite PASSED a provider that branches in constant time and denies it. "+
			"An understated capability puts the wrong row in the table a buyer chooses "+
			"from, and it is the cheapest way out of an unproven cell.\n%s", out)
	}
	requireFailedIn(t, out, cowBehavior)
}

// requireFailedIn insists the child failed in the named behaviour rather than
// anywhere at all, which is what makes a red mean one thing.
func requireFailedIn(t *testing.T, out, behavior string) {
	t.Helper()
	marker := "--- FAIL: TestDatabaseSuiteChild/" + behavior
	if !strings.Contains(out, marker) {
		t.Fatalf("the child failed, and not in %s, so this proves nothing about that "+
			"behaviour.\n%s", behavior, out)
	}
}

// unprovenBlock pulls the report out of a child's output for the log.
func unprovenBlock(out string) string {
	i := strings.Index(out, unprovenReportLine)
	if i < 0 {
		return "(the run printed no report)"
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j > 0 {
		return rest[:j]
	}
	return rest
}

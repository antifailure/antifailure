package conformance_test

import (
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
)

// The third verdict, proved in every direction it can go.
//
// A verdict with three answers needs three proofs and one of them is new. The
// two old ones are not assumed to have survived: the whole risk of adding an
// answer to an instrument is that the answer becomes the one it always gives,
// and a suite that could no longer refuse a false copy on write claim would be
// worse than the one that existed before this lane, because it would look like
// the one that existed after it.
//
// So all four cells are here, side by side, against the same provider on the
// same harness, differing only in the two bits that decide the verdict: whether
// the storage under the run copies, and whether the fixture said so.
//
//	harness copies   fixture declared it   verdict
//	yes              no                    REFUTED, and it must stay refuted
//	yes              yes                   UNPROVEN, which is the new one
//	no               no                    PROVED
//	no               yes                   REFUTED, for the DECLARATION
//
// The third row is not repeated here. db_selftest_test.go already carries it as
// TestTheFlatBranchAffordanceIsHonestWhenItDeclaresCopyOnWrite, against the same
// provider on the same harness, and a second child of half a gibibyte proving
// the same sentence is cost with no evidence in it.
//
// The fourth row is the lock. A fixture whose storage turns out to share after
// all has said something false about itself, and the suite fails it for that
// rather than passing it on the reading, which is what stops the declaration
// being an annotation somebody sets on every run in the wave.
//
// WHAT THESE RUN AT, and why the first answer to that was wrong.
//
// Each child builds two goldens and branches each of them several times, and
// the shipped large size is half a gibibyte, so the obvious economy is to run
// these at the floor the behaviour will accept, 64 MiB against 4 MiB. That was
// done, it passed on the machine it was written on, and CI refused it.
//
// CI's storage copied the 60 MiB of extra data in 163ms, inside the 250ms the
// allowance gives to noise. So nothing was distinguishable: the copying
// provider was not refused, because its copy was invisible, and the fixture
// that declared its storage copies was refused, because on that hardware at
// that size the storage did not measurably copy. Both reds were the instrument
// working. cow.go says this in advance, about the size rather than about these
// tests: smaller lets a real copy pass as shared storage on a fast disk, and
// the default is the smallest size at which a copy running at the speed of the
// fastest storage that exists is still refused.
//
// So these run at the SHIPPED sizes and set no override. That is the same
// argument db_selftest_test.go already makes for the fault table beside it: a
// self test run at sizes the suite does not ship proves the faults are
// catchable at SOME configuration, which is a weaker sentence than anybody
// reading a green would assume it to be. The economy was buying exactly that
// weaker sentence, and paying for it with a proof that did not hold on the one
// machine whose verdict blocks a merge.
//
// It costs three children of half a gibibyte. That is the price of the claim.

// theHarnessLimit is the sentence a Wave 2 fixture would write.
//
// It is the real one rather than a placeholder, because the reason is published
// beside the verdict and a test that proved the machinery with the word "x"
// would prove nothing about whether a reader can act on the output.
const theHarnessLimit = "this fixture is a fake cloud control plane over one local Postgres, " +
	"and the only way it can hand back a branch carrying the golden's data is " +
	"CREATE DATABASE ... TEMPLATE, which copies files"

const cowBehavior = "CopyOnWrite_BranchTimeMatchesTheDeclaration"

func requireCopyOnWritePostgres(t *testing.T) {
	t.Helper()
	if !postgresReachable(t) {
		t.Skipf("skipped: the third verdict is a claim about moving bytes and needs a "+
			"Postgres at %s", postgresURL())
	}
}

// TestACopyingProviderIsStillRefusedWhenNothingDeclaredTheHarness is the first
// direction, and it is the one that must not have been disarmed.
//
// A provider that declares CopyOnWrite and copies every byte still fails, on a
// harness that copies, when the fixture makes no claim about its storage. If
// this direction broke, the third verdict would have turned the instrument the
// whole database wave was held for into one that cannot say no.
func TestACopyingProviderIsStillRefusedWhenNothingDeclaredTheHarness(t *testing.T) {
	requireCopyOnWritePostgres(t)

	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior, fault: copyOnWriteThatCopies(),
	})
	if passed {
		t.Fatalf("the suite PASSED a provider that declares CopyOnWrite and copies every "+
			"byte, with no declaration from the fixture. The third verdict has disarmed "+
			"the two sided assertion, and every provider in the wave could now declare "+
			"copy on write untested.\n%s", out)
	}
	requireFailedIn(t, out, cowBehavior)
	if strings.Contains(out, "UNPROVEN") {
		t.Fatalf("the child failed and reached the third verdict on the way, so the red is "+
			"not attributable to the assertion. UNPROVEN must be unreachable when the "+
			"fixture declared nothing.\n%s", out)
	}
}

// TestTheTemplateCopyHarnessReportsUnproven is the third direction, and it is
// the one this lane exists for.
//
// A provider declaring CopyOnWrite truthfully, on a harness whose storage
// copies, with the fixture declaring that limit before the run. The child must
// not fail, must not read as a plain pass, and must print the verdict where
// somebody sees it.
func TestTheTemplateCopyHarnessReportsUnproven(t *testing.T) {
	requireCopyOnWritePostgres(t)

	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior,
		fault:         copyOnWriteThatCopies(),
		harnessCopies: theHarnessLimit,
	})
	if !passed {
		t.Fatalf("the suite FAILED a truthful CopyOnWrite declaration on a harness that "+
			"declared it cannot exhibit copy on write. That is the case the ruling exists "+
			"to answer, and failing it is what blocks Aurora, Cloud SQL and AlloyDB.\n%s", out)
	}
	// Every one of these is a separate way the verdict could be invisible, and
	// invisible is the failure mode that matters: a skip that reads as a pass
	// is what this whole design is against.
	for _, want := range []string{
		"UNPROVEN",
		"NOT PROVED BY THIS RUN. This is not a pass.",
		theHarnessLimit,
		"unproven",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the run reached the third verdict and its output does not contain %q, "+
				"so a reader of this run cannot tell it from a pass.\n%s", want, out)
		}
	}
	// The measurement ran in full. An unproven verdict that skipped the work
	// would be a skip with a longer name, and the readings are what make the
	// verdict readable rather than asserted.
	for _, want := range []string{"copy on write, measured", "marginal cost", "extra branch time"} {
		if !strings.Contains(out, want) {
			t.Errorf("the unproven run did not print %q, so it did not take the measurement "+
				"and the verdict is a skip wearing a longer word.\n%s", want, out)
		}
	}
	// The subtest itself did not fail, which is what "not a pass" has to be
	// reconciled with: Go has two states and the third one lives in the output.
	if !strings.Contains(out, "--- PASS: TestDatabaseSuiteChild/"+cowBehavior) &&
		!strings.Contains(out, "PASS") {
		t.Errorf("the child neither failed nor passed the behaviour, so something other "+
			"than the verdict decided this run.\n%s", out)
	}
	t.Logf("the report the run printed, which is the thing a reader sees:\n%s",
		unprovenBlock(out))
}

// TestAFixtureThatDeclaresALimitItDoesNotHaveIsRefused is the fourth cell, and
// it is the lock that stops the declaration being a free annotation.
//
// The fixture says its storage copies every branch. The provider branches in
// constant time and the stopwatch says so. The suite fails, for the
// DECLARATION rather than for the provider, because a fixture allowed to claim
// a limit it does not have could set the field on every run in the wave and
// never be contradicted.
func TestAFixtureThatDeclaresALimitItDoesNotHaveIsRefused(t *testing.T) {
	requireCopyOnWritePostgres(t)

	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior,
		flatBranch: true, harnessCopies: theHarnessLimit,
	})
	if passed {
		t.Fatalf("a fixture declared that its storage copies every branch, the storage "+
			"shared instead, and the suite said nothing. The declaration is then an "+
			"annotation rather than a claim about the harness, and anybody could set it "+
			"on every run in the wave.\n%s", out)
	}
	requireFailedIn(t, out, cowBehavior)
	if !strings.Contains(out, "The declaration is what is wrong here") {
		t.Errorf("the child failed and did not name the declaration as the thing that is "+
			"wrong, so a reader would go looking at the provider.\n%s", out)
	}
}

// TestTheHarnessLimitIsRefusedOnAProviderThatDeclaresCopyOnWriteFalse is the
// first of the three refusals, checked on its own.
//
// On the false side a copying harness exhibits exactly what is being asserted,
// so there is nothing the harness cannot reach and the declaration has no
// subject. Refusing it is what tells a lane copying a fixture across the wave
// from a provider that clones to one that restores from a snapshot.
func TestTheHarnessLimitIsRefusedOnAProviderThatDeclaresCopyOnWriteFalse(t *testing.T) {
	requireCopyOnWritePostgres(t)

	// The plain Postgres backed provider declares CopyOnWrite false, honestly,
	// because it copies.
	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior, harnessCopies: theHarnessLimit,
	})
	if passed {
		t.Fatalf("a fixture declared a harness limit against a provider that declares "+
			"CopyOnWrite FALSE and the suite accepted it. The false side is settleable on "+
			"a copying harness, so the declaration there is a misconfiguration that would "+
			"otherwise sit unnoticed in a lane's fixture.\n%s", out)
	}
	requireFailedIn(t, out, cowBehavior)
	if !strings.Contains(out, "there is nothing here it cannot reach") {
		t.Errorf("the refusal did not say why the declaration has no subject on the false "+
			"side.\n%s", out)
	}
	// Refused BEFORE the measurement, which is the difference between a
	// configuration error found in a second and one found after half an hour
	// of building goldens.
	if strings.Contains(out, "copy on write, measured") {
		t.Errorf("the misconfiguration was refused only after the measurement ran. A "+
			"fixture whose declaration cannot apply should learn that before it builds "+
			"two goldens.\n%s", out)
	}
}

// TestAShortHarnessReasonIsRefused is the second refusal.
func TestAShortHarnessReasonIsRefused(t *testing.T) {
	requireCopyOnWritePostgres(t)

	passed, out := runChild(t, child{
		backend: onPG, behavior: cowBehavior,
		fault: copyOnWriteThatCopies(), harnessCopies: "it copies",
	})
	if passed {
		t.Fatalf("a fixture reached the third verdict with %q as its whole reason. The "+
			"reason is the only thing a reader has to judge the limit by, and one that "+
			"says nothing turns the verdict back into a skip.\n%s", "it copies", out)
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
	i := strings.Index(out, "NOT PROVED BY THIS RUN")
	if i < 0 {
		return "(the run printed no report)"
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j > 0 {
		return rest[:j]
	}
	return rest
}

// copyOnWriteThatCopies names the fault rather than importing the constant into
// four call sites, so that a rename in fakes reaches one line here.
func copyOnWriteThatCopies() fakes.Fault { return fakes.CopyOnWriteThatCopies }

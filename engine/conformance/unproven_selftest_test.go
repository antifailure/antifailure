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
// The fourth row is the lock. A fixture whose storage turns out to share after
// all has said something false about itself, and the suite fails it for that
// rather than passing it on the reading, which is what stops the declaration
// being an annotation somebody sets on every run in the wave.
//
// WHAT THESE RUN AT, said out loud because it is not the shipped configuration.
//
// Each child builds two goldens and branches each of them several times, and
// the shipped large size is half a gibibyte. Four children of that on a machine
// with a load average of twenty four is hours, so these run at the floor the
// behaviour will accept, which is 64 MiB against 4 MiB. That is a weaker
// sentence than the same proof at the defaults and the suite says so itself:
// every child prints the sizes it ran at, and prints the copy rate the
// configuration was able to refuse. What is proved here is that the four cells
// are reachable and distinguishable, which does not depend on the size.

// The sizes these children run at, and the floors they sit on.
//
// 64 MiB is exactly MinCopyOnWriteLargeBytes, and 4 MiB is exactly a sixteenth
// of it, which is MinCopyOnWriteRatio. A child configured any smaller is
// refused by copyOnWriteSettings, which is the behaviour's own answer to a run
// trying to buy a cheap pass, so these sit on the boundary rather than under it.
const (
	selftestSmallBytes = "4194304"
	selftestLargeBytes = "67108864"
)

// theHarnessLimit is the sentence a Wave 2 fixture would write.
//
// It is the real one rather than a placeholder, because the reason is published
// beside the verdict and a test that proved the machinery with the word "x"
// would prove nothing about whether a reader can act on the output.
const theHarnessLimit = "this fixture is a fake cloud control plane over one local Postgres, " +
	"and the only way it can hand back a branch carrying the golden's data is " +
	"CREATE DATABASE ... TEMPLATE, which copies files"

const cowBehavior = "CopyOnWrite_BranchTimeMatchesTheDeclaration"

// affordableCopyOnWrite points the children at the floor rather than the
// shipped sizes, and at whichever Postgres the run was given.
func affordableCopyOnWrite(t *testing.T) {
	t.Helper()
	t.Setenv("AF_CONFORMANCE_COW_SMALL_BYTES", selftestSmallBytes)
	t.Setenv("AF_CONFORMANCE_COW_LARGE_BYTES", selftestLargeBytes)
}

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
	affordableCopyOnWrite(t)

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

// TestAnHonestFlatProviderStillPasses is the second direction.
//
// The provider that really does branch in constant time, declaring so, on a
// harness that can exhibit it, still passes and does not touch the third
// verdict. Without this the red above would be attributable to the addition
// rather than to the fault.
func TestAnHonestFlatProviderStillPasses(t *testing.T) {
	requireCopyOnWritePostgres(t)
	affordableCopyOnWrite(t)

	passed, out := runChild(t, child{backend: onPG, behavior: cowBehavior, flatBranch: true})
	if !passed {
		t.Fatalf("the suite FAILED a provider whose branch time really is constant and "+
			"which declares so. The pass side of the assertion is gone.\n%s", out)
	}
	if strings.Contains(out, "UNPROVEN") || strings.Contains(out, "NOT PROVED BY THIS RUN") {
		t.Fatalf("a run with nothing declared about the harness reached the third verdict, "+
			"so the verdict is not gated on the declaration at all.\n%s", out)
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
	affordableCopyOnWrite(t)

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
	affordableCopyOnWrite(t)

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
	affordableCopyOnWrite(t)

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
	affordableCopyOnWrite(t)

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

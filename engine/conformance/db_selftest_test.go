package conformance_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A conformance suite nobody has watched fail is not evidence. It is a list of
// assertions that might all be vacuous, and the usual ways an assertion goes
// vacuous are undramatic: a helper starts skipping, a comparison compares a
// value against itself, a behaviour asserts on state an earlier behaviour
// already established. Every one of those still prints ok.
//
// So this points the suite at a provider that violates exactly one guarantee
// and requires the suite to go RED, in the named behaviour. If it stays green,
// that behaviour was not checking what it claims, and the green run could never
// have told anybody.
//
// It needs a SUBPROCESS. A suite proving it can fail has to actually fail, and
// a failure inside the process asserting on it fails that process too. The
// child below is skipped unless it is the child; the parent re-executes the
// test binary once per fault.
//
// It needs NO DATABASE, which is the part that had stopped this being done. The
// property being false is arranged in the fake, not in a real service, and a
// negative control that needs infrastructure gets skipped, which is a false
// green rather than a proof. (That correction is lane 4's; an earlier note of
// mine claimed a real database was required and it was wrong.)
const (
	childEnv = "AF_DB_SELFTEST_CHILD"
	faultEnv = "AF_DB_SELFTEST_FAULT"
)

// TestDatabaseSuiteChild is the suite under examination. It runs only when the
// parent re-executes this binary, so an ordinary `go test ./...` does not see
// it fail on purpose.
func TestDatabaseSuiteChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of TestTheDatabaseSuiteCanFail")
	}

	fault := fakes.Fault(os.Getenv(faultEnv))
	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p := provider.Database(fakes.NewInMemoryDatabase())
		if fault != "" {
			p = fakes.Break(p, fault)
		}
		return p
	}, conformance.Options{SkipSlow: true})
}

// selftestBehaviors are the faults whose behaviour can be checked without a
// real database, with the behaviour each one must break.
//
// The rest of fakes.Catches needs rows, and rows need Postgres. Naming the
// subset here rather than silently iterating everything is deliberate: a table
// that quietly skipped the ones it could not do would report a coverage it
// does not have, which is the failure this whole file exists to prevent.
func selftestBehaviors(t *testing.T) map[fakes.Fault]string {
	t.Helper()

	// Behaviours that read rows through a connection string, which an
	// in-memory provider cannot answer.
	needsRows := map[string]bool{
		"Branch_ReadsAKnownRow":              true,
		"Branch_IsIsolatedFromTheGolden":     true,
		"Branch_IsIsolatedFromOtherBranches": true,
		"Reset_ReturnsToGoldenState":         true,
		"ConnString_PooledWorksWhenDeclared": true,
	}

	out := map[fakes.Fault]string{}
	for fault, behavior := range fakes.Catches() {
		if !needsRows[behavior] {
			out[fault] = behavior
		}
	}
	if len(out) == 0 {
		t.Fatal("no fault is checkable without a database, which means this file is testing nothing")
	}
	return out
}

// runChild executes one behaviour in a subprocess and reports whether it
// passed, along with its output for the failure message.
func runChild(t *testing.T, behavior string, fault fakes.Fault) (bool, string) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run", "TestDatabaseSuiteChild/"+behavior, "-test.v")
	cmd.Env = append(os.Environ(), childEnv+"=1", faultEnv+"="+string(fault))
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// The positive control, and it is the half almost nobody writes. Without it a
// suite can "pass" by skipping itself, and every negative result below would
// then be meaningless: everything fails, including the correct provider.
func TestTheSuitePassesAgainstAProviderThatKeepsItsGuarantees(t *testing.T) {
	for fault, behavior := range selftestBehaviors(t) {
		t.Run(behavior, func(t *testing.T) {
			passed, out := runChild(t, behavior, "")
			if !passed {
				t.Fatalf("%s must pass against a correct provider, or the fault result for %s proves nothing.\n%s",
					behavior, fault, out)
			}
			if strings.Contains(out, "--- SKIP") {
				t.Fatalf("%s SKIPPED rather than ran. A suite that passes by skipping is a false green.\n%s",
					behavior, out)
			}
		})
	}
}

// knownGaps are faults the suite does NOT catch, with the reason.
//
// They are recorded rather than removed, and an entry that starts being caught
// fails this file, for the same reason a stale vulnerability suppression does:
// an exemption describing something that is no longer true reads as protection
// that is not there.
//
// Both were found by running the negative controls below for the first time.
// Neither is a fault in the fake.
var knownGaps = map[fakes.Fault]string{
	fakes.BranchAcceptsUnverified: "" +
		"The VACUITY in Branch_RefusesAnUnverifiedGolden is fixed: it used to put " +
		"its whole assertion inside `if err == nil && gv.ID != \"\"`, which a " +
		"provider that correctly refuses to PUBLISH never satisfies, so nothing " +
		"ran. It now asserts on both paths, and fakes.RefusesWithoutSayingSo " +
		"proves the refusal path is live. " +
		"What remains cannot be closed from here: a provider that refuses to " +
		"publish never produces an unverified version, so there is nothing for " +
		"the suite to hand to Branch, and the branch-side rule is unreachable " +
		"through the interface. Closing it needs a way to obtain an unverified " +
		"version, which is an interface change and a design decision rather " +
		"than a fix.",

	fakes.HealthErrorsOnDestroyed: "" +
		"Health_ReportsADestroyedBranch is DECLARED as \"Health reports a destroyed " +
		"branch as unreachable rather than erroring\" and its implementation returns " +
		"early when Health errors, with a comment saying an error is acceptable. The " +
		"suite and its own catalogue description disagree, and the description is the " +
		"one a provider author reads. One of the two has to change; the suite is not " +
		"mine to decide for.",
}

// The negative controls.
func TestEveryFaultTurnsItsBehaviorRed(t *testing.T) {
	for fault, behavior := range selftestBehaviors(t) {
		t.Run(string(fault), func(t *testing.T) {
			passed, out := runChild(t, behavior, fault)

			if reason, known := knownGaps[fault]; known {
				if !passed {
					t.Fatalf("%s is recorded as a gap the suite does not catch, and it "+
						"caught it. Remove the entry: a recorded gap that is no longer "+
						"real describes nothing.\nThe recorded reason was: %s", fault, reason)
				}
				t.Skipf("known gap, recorded rather than hidden: %s", reason)
			}

			if passed {
				t.Fatalf("the suite PASSED against a provider that %s.\n"+
					"%s is therefore not checking what it claims, and no green run could ever say so.\n%s",
					fault, behavior, out)
			}
			// It has to fail in the named behaviour rather than anywhere at
			// all. A child that failed to build, or failed in setup, would
			// otherwise count as the suite catching the fault.
			if !strings.Contains(out, "--- FAIL: TestDatabaseSuiteChild/"+behavior) {
				t.Fatalf("the child failed, but not in %s, so this proves nothing about that behaviour.\n%s",
					behavior, out)
			}
		})
	}
}

// A fault that no behaviour can be checked for without a database is still a
// fault, and this records which ones those are rather than leaving the gap to
// be discovered.
func TestTheFaultsThatStillNeedARealDatabaseAreNamed(t *testing.T) {
	checkable := selftestBehaviors(t)
	var uncovered []string
	for fault, behavior := range fakes.Catches() {
		if _, ok := checkable[fault]; !ok {
			uncovered = append(uncovered, string(fault)+" ("+behavior+")")
		}
	}
	t.Logf("%d of %d database faults are proved able to fail offline; %d still need Postgres: %s",
		len(checkable), len(fakes.Catches()), len(uncovered), strings.Join(uncovered, ", "))
}

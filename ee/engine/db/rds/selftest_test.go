// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// A conformance suite nobody has watched fail is not evidence. It is a list of
// assertions that might all be vacuous, and the usual ways an assertion goes
// vacuous are undramatic: a helper starts skipping, a comparison compares a
// value against itself, a behaviour asserts on state an earlier behaviour
// already established. Every one of those still prints ok.
//
// So this points the suite at a control plane that breaks exactly one
// guarantee and requires the suite to go RED, in the named behaviour. If it
// stays green, that behaviour was not checking what it claims about THIS
// provider, and the green run could never have told anybody.
//
// It needs a SUBPROCESS. A suite proving it can fail has to actually fail, and
// a failure inside the process asserting on it fails that process too. The
// child below is skipped unless it is the child; the parent re-executes the
// test binary once per fault.
//
// Copy on write is not in this table. The suite withholds that verdict for a
// run that asserts no real service, before the behaviour runs, so no fault this
// fake can inject could turn it red and a row for it would be a row that can
// never fail. verdict_test.go holds what can honestly be said about it here.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const (
	childEnv = "AF_RDS_SELFTEST_CHILD"
	faultEnv = "AF_RDS_SELFTEST_FAULT"
)

// TestConformanceChild is the suite under examination. It runs only when the
// parent re-executes this binary, so an ordinary `go test ./...` does not see
// it fail on purpose.
func TestConformanceChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of the self test")
	}

	server := newFake(t, conformance.DefaultSeedSQL, fakerds.Fault(os.Getenv(faultEnv)))
	opts := conformanceOptions()
	// An instance held in creating for ever would otherwise wait the whole
	// ready timeout in every behaviour to learn the same thing.
	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		o := options(t, server)
		o.ReadyTimeout = 10 * time.Second
		p, err := scopedNew(context.Background(), o)
		require.NoError(t, err)
		return p
	}, opts)
}

// child describes one re-execution.
type child struct {
	// behavior is the single subtest to run. Empty runs the whole suite.
	behavior string
	fault    fakerds.Fault
}

// runChild executes one behaviour in a subprocess and reports whether it
// passed, along with its output for the failure message.
func runChild(t *testing.T, c child) (bool, string) {
	t.Helper()

	pattern := "TestConformanceChild"
	if c.behavior != "" {
		pattern += "/^" + c.behavior + "$"
	}
	cmd := exec.Command(os.Args[0], "-test.run", pattern, "-test.v", "-test.timeout", "40m")
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		faultEnv+"="+string(c.fault),
		// The child stands up its own fake with its own random prefix, and it
		// must find the same Postgres this process did.
		"AF_RDS_TEST_DATABASE_URL="+postgresURL(),
	)
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// The faults, and the behaviour each one must turn red.
//
// Each is a thing a real control plane could plausibly do, not an arbitrary
// corruption: a restore that shares its source, a restore that is empty, a tag
// that is not recorded, a delete that does not delete, a password that is not
// rotated, an instance that never comes up.
var faultRows = []struct {
	fault    fakerds.Fault
	behavior string
	because  string
}{
	{
		fakerds.FaultRestoreSharesItsSnapshot,
		"Branch_IsIsolatedFromOtherBranches",
		"a restore that points at the snapshot's own database gives two environments one " +
			"database, and every endpoint still answers",
	},
	{
		fakerds.FaultRestoreIsEmpty,
		"Branch_ReadsAKnownRow",
		"a restore that is a fresh empty database produces a branch that exists, connects " +
			"and holds nothing",
	},
	{
		fakerds.FaultTagsAreNotRecorded,
		"Refresh_ProducesAVerifiedGolden",
		"a golden here IS a tagged snapshot, so a control plane that records no tags " +
			"publishes nothing while every call succeeds, and the refresh reads its " +
			"publication back and refuses rather than reporting a golden nobody can find",
	},
	{
		fakerds.FaultDeleteDoesNotDelete,
		"Destroy_RemovesTheBranch",
		"a delete that succeeds and keeps the instance is how a provider leaks an " +
			"environment's database",
	},
	{
		fakerds.FaultPasswordIsNotRotated,
		"Refresh_ProducesAVerifiedGolden",
		"a rotation that changed nothing leaves the derived credential opening nothing, " +
			"and the refresh connects with it to close the logins the restore inherited " +
			"before anything is masked",
	},
	{
		fakerds.FaultInstanceNeverBecomesAvailable,
		"Refresh_ProducesAVerifiedGolden",
		"an instance held in creating for ever is what a subnet with no capacity looks " +
			"like, and a provider that did not wait would publish a golden of a database " +
			"nothing could read",
	},
}

// The negative results: each fault turns its behaviour red.
func TestEachFaultTurnsItsBehaviourRed(t *testing.T) {
	requirePostgres(t)
	for _, row := range faultRows {
		t.Run(string(row.fault), func(t *testing.T) {
			passed, out := runChild(t, child{behavior: row.behavior, fault: row.fault})
			requireRed(t, row.behavior, passed, out, row.because)
		})
	}
}

// The positive control, and it is the half almost nobody writes. Without it a
// suite can "pass" by skipping itself, and every negative result above would
// then be meaningless: everything fails, including the correct provider.
func TestEveryFaultedBehaviourPassesWithNothingBroken(t *testing.T) {
	requirePostgres(t)
	seen := map[string]bool{}
	for _, row := range faultRows {
		if seen[row.behavior] {
			continue
		}
		seen[row.behavior] = true
		t.Run(row.behavior, func(t *testing.T) {
			passed, out := runChild(t, child{behavior: row.behavior})
			requireGreen(t, row.behavior, passed, out)
		})
	}
}

// requireRed is the assertion a self test needs, and it is not "the exit code
// was non zero".
//
// A child that failed to compile, or matched no test, or crashed before
// reaching the behaviour, produces exactly the same exit code as one that
// caught the fault. So the behaviour has to be seen to have RUN and to have
// FAILED by name.
func requireRed(t *testing.T, behavior string, passed bool, out, because string) {
	t.Helper()
	if !strings.Contains(out, "=== RUN   TestConformanceChild/"+behavior) {
		t.Fatalf("%s never ran in the child, so its result says nothing. A -test.run that "+
			"matches nothing exits zero and reads exactly like a pass.\n%s", behavior, out)
	}
	if strings.Contains(out, "--- SKIP: TestConformanceChild/"+behavior) {
		t.Fatalf("%s SKIPPED rather than ran, and a skip is the false green this file "+
			"exists against.\n%s", behavior, out)
	}
	if !strings.Contains(out, "--- FAIL: TestConformanceChild/"+behavior) {
		t.Fatalf("%s stayed green with the fault present, so it is not checking what it "+
			"claims: %s\n%s", behavior, because, out)
	}
	if passed {
		t.Fatalf("the child reported success while %s failed, which cannot both be "+
			"true.\n%s", behavior, out)
	}
}

// requireGreen is the same discipline in the other direction.
func requireGreen(t *testing.T, behavior string, passed bool, out string) {
	t.Helper()
	if strings.Contains(out, "--- SKIP: TestConformanceChild/"+behavior) {
		t.Fatalf("%s SKIPPED rather than ran. A suite that passes by skipping is a false "+
			"green.\n%s", behavior, out)
	}
	if !strings.Contains(out, "--- PASS: TestConformanceChild/"+behavior) {
		t.Fatalf("%s must pass against an unbroken control plane, or a red result for it "+
			"proves nothing.\n%s", behavior, out)
	}
	if !passed {
		t.Fatalf("the child failed while %s passed, so something else in the run is "+
			"broken.\n%s", behavior, out)
	}
}

// Every fault the fake can inject is in the table above.
//
// Without this, adding a fault and forgetting to point the suite at it leaves
// a fault nothing proves is catchable, which reads exactly like coverage that
// is not there.
func TestEveryFaultIsProvedBySomeRow(t *testing.T) {
	covered := map[fakerds.Fault]bool{}
	for _, row := range faultRows {
		covered[row.fault] = true
	}
	for _, fault := range fakerds.AllFaults() {
		require.True(t, covered[fault],
			"the fake can inject %q and no row requires a behaviour to go red for it, so "+
				"nothing shows that fault is catchable", fault)
	}
}

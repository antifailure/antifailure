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
// THE ROW THAT MATTERS MOST IS THE LAST ONE. Copy on write is the
// distinguishing commercial claim of this whole wave, this provider declares
// it FALSE, and until engine/conformance/cow.go landed nothing could refuse
// either answer. So the table below includes a child whose ONLY difference is
// that it declares copy on write TRUE over the same, honestly copying,
// provider, and requires the suite to refuse it. A declaration that cannot be
// refused is the defect that lane was built against, and this is the local
// proof that it is refused here.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const (
	childEnv     = "AF_RDS_SELFTEST_CHILD"
	faultEnv     = "AF_RDS_SELFTEST_FAULT"
	overstateEnv = "AF_RDS_SELFTEST_OVERSTATE_COW"
	cowSizeEnv   = "AF_RDS_SELFTEST_COW"
)

// The copy on write sizes the self test runs at, which are the ones
// conformance_test.go runs at and not the suite's own defaults.
//
// The same budget argument is written out there in full: the enterprise job
// runs this whole module under a fifteen minute timeout, this is the only
// behaviour whose cost is a size, and shrinking it can only make a provider
// declaring copy on write FALSE fail rather than pass.
//
// Identical to the main run's on purpose. The negative child below and the
// positive one beside it differ in exactly one field, the declaration, and a
// control taken at a different configuration would not be a control.
const (
	selfTestSmallBytes = 8 << 20
	selfTestLargeBytes = 256 << 20
	selfTestSamples    = 2
)

// TestConformanceChild is the suite under examination. It runs only when the
// parent re-executes this binary, so an ordinary `go test ./...` does not see
// it fail on purpose.
func TestConformanceChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of the self test")
	}

	server := newFake(t, conformance.DefaultSeedSQL, fakerds.Fault(os.Getenv(faultEnv)))
	overstate := os.Getenv(overstateEnv) == "1"

	opts := conformance.Options{
		Timeout:  6 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	}
	if os.Getenv(cowSizeEnv) == "1" {
		opts.CopyOnWriteSmallBytes = selfTestSmallBytes
		opts.CopyOnWriteLargeBytes = selfTestLargeBytes
		opts.CopyOnWriteSamples = selfTestSamples
		opts.CopyOnWriteTimeout = 20 * time.Minute
	}

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := rds.New(context.Background(), options(t, server))
		require.NoError(t, err)
		if overstate {
			return overstated{p}
		}
		return p
	}, opts)
}

// overstated is this provider with one field changed: it declares copy on
// write while copying every byte, which is precisely the claim the wave is
// sold on and precisely the claim that used to be unfalsifiable.
//
// A wrapper rather than a knob on the provider, because a production type with
// a field that makes it lie about itself is a worse thing to ship than a test
// that lies on its behalf.
type overstated struct{ provider.Database }

func (o overstated) Capabilities() provider.Caps {
	caps := o.Database.Capabilities()
	caps.CopyOnWrite = true
	return caps
}

// child describes one re-execution.
type child struct {
	// behavior is the single subtest to run. Empty runs the whole suite.
	behavior string
	fault    fakerds.Fault
	// overstateCoW declares copy on write over a provider that copies.
	overstateCoW bool
	// smallCoW runs the copy on write behaviour at this file's sizes rather
	// than the suite's.
	smallCoW bool
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
		overstateEnv+"="+boolEnv(c.overstateCoW),
		cowSizeEnv+"="+boolEnv(c.smallCoW),
		// The child stands up its own fake with its own random prefix, and it
		// must find the same Postgres this process did.
		"AF_RDS_TEST_DATABASE_URL="+postgresURL(),
	)
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func boolEnv(v bool) string {
	if v {
		return "1"
	}
	return "0"
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
		"List_ReturnsWhatWasCreated",
		"a golden here IS a tagged snapshot, so a control plane that records no tags " +
			"publishes nothing while every call succeeds",
	},
	{
		fakerds.FaultDeleteDoesNotDelete,
		"Destroy_RemovesTheBranch",
		"a delete that succeeds and keeps the instance is how a provider leaks an " +
			"environment's database",
	},
	{
		fakerds.FaultPasswordIsNotRotated,
		"Health_ReportsAReachableBranch",
		"a rotation that changed nothing leaves the derived credential opening nothing, " +
			"which is invisible until something connects",
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

// The copy on write declaration is refused when it is wrong.
//
// This provider copies every byte and declares CopyOnWrite false. The child
// here declares TRUE over the same provider and changes nothing else, so the
// only thing that can make the suite go red is the instrument reading the
// stopwatch and refusing the claim. Before engine/conformance/cow.go, this
// child would have passed.
//
// WHAT THIS ROW COVERS, AND WHAT IT DOES NOT. The bytes being copied are the
// simulator's, so the refusal proves the INSTRUMENT can say no about a
// declaration made over this harness. It is not a measurement of RDS and it
// cannot become one here. The value of the row is the direction it fires in:
// the positive control below passes at the same sizes, so the pair rules out
// "everything fails" and "everything passes", which is the most a lane with no
// cloud account can honestly claim about this capability.
func TestDeclaringCopyOnWriteOverAProviderThatCopiesIsRefused(t *testing.T) {
	requirePostgres(t)
	const behavior = "CopyOnWrite_BranchTimeMatchesTheDeclaration"
	passed, out := runChild(t, child{behavior: behavior, overstateCoW: true, smallCoW: true})
	requireRed(t, behavior, passed, out,
		"a snapshot restore copies the whole volume, so branch time grows with the data "+
			"and a declaration of copy on write over it is a claim about seconds that is "+
			"not true")
	// The refusal has to be the copy on write one rather than any other
	// failure that happened to be red at the same moment.
	require.Contains(t, out, "declares CopyOnWrite, and branching the golden with",
		"the behaviour went red for some other reason, so this proves nothing about the "+
			"declaration.\n%s", out)
}

// And the honest declaration passes at the same sizes, which is the control
// that stops the row above being "everything fails here".
func TestDeclaringCopyOnWriteFalseIsAcceptedAtTheSameSizes(t *testing.T) {
	requirePostgres(t)
	const behavior = "CopyOnWrite_BranchTimeMatchesTheDeclaration"
	passed, out := runChild(t, child{behavior: behavior, smallCoW: true})
	requireGreen(t, behavior, passed, out)
	// The power of the check, published by the suite itself, carried into this
	// test's own output so that a reader of THIS file can see what the
	// configuration was able to refuse rather than taking a constant's word.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "could refuse") || strings.Contains(line, "marginal cost") {
			t.Log(strings.TrimSpace(line))
		}
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

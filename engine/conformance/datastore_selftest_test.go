package conformance

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The claim RunDatastore's doc comment makes, checked.
//
// A conformance suite nobody has proved can fail is a list of function calls
// that always pass, and this repository has already shipped one behaviour that
// asserted nothing for every provider that behaved correctly. That was found
// by a self test of exactly this shape and by nothing else: it was green, it
// reviewed as correct, and it had never once run its own body.
//
// So every behaviour below is shown going red against a datastore built with
// one thing wrong, and both correct shapes are shown going green with nothing
// skipped that the shape could have run.

// dsNegativeControls pairs each flaw with the behaviour that has to notice it.
//
// Three rows red more than one behaviour, and they say so rather than being
// quietly assigned to the first name in the output. A flaw that reds two
// behaviours is still a control for each of them; what would not be a control
// is a row whose expected text appears for a reason other than the property it
// names, which is why every row asserts a specific subtest name.
var dsNegativeControls = []struct {
	flaw  string
	shape string
	// wants is the text that must appear in the failing run's output.
	wants string
	// also names the other behaviours this flaw reds, where it reds more than
	// one. Recorded so that a reader can see the overlap is known rather than
	// missed.
	also string
}{
	{dsFlawNoName, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Name_IsNotEmpty", ""},
	{dsFlawCopyOnWriteWithoutBranch, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Capabilities_AreSelfConsistent", ""},
	{dsFlawGenericNoGoldenError, dsShapeCache, "--- FAIL: TestDatastoreSuiteChild/Refresh_SaysErrNoGoldenWhenItHoldsNone", ""},
	{dsFlawReturnsUnverified, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Refresh_ProducesAVerifiedGolden", ""},
	{
		dsFlawSkipsMaskAndVerify, dsShapeGolden,
		"--- FAIL: TestDatastoreSuiteChild/Refresh_CallsMaskThenVerify",
		"a store that calls neither callback also publishes when verification would have failed, " +
			"so Refresh_RefusesToPublishWhenVerificationFails reds too",
	},
	{dsFlawPublishesFailedVerify, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Refresh_RefusesToPublishWhenVerificationFails", ""},
	{dsFlawBranchNotIdempotent, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Branch_IsIdempotentByEnvironment", ""},
	{
		dsFlawBranchesUnverified, dsShapeGolden,
		"--- FAIL: TestDatastoreSuiteChild/Branch_RefusesAnUnverifiedGolden",
		"an unverified golden has to exist before it can be branched, so this flaw publishes one " +
			"and Refresh_RefusesToPublishWhenVerificationFails reds too",
	},
	{
		dsFlawDestroyLeavesTheBranch, dsShapeGolden,
		"--- FAIL: TestDatastoreSuiteChild/Destroy_RemovesTheBranch",
		"a branch that survives its own Destroy is still there when the next behaviour looks, " +
			"so Health_ReportsADestroyedBranch reds too",
	},
	{dsFlawDestroyTwiceErrors, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Destroy_OfSomethingAlreadyGoneSucceeds", ""},
	{dsFlawEmptyConnString, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/ConnString_IsASecret", ""},
	{dsFlawRedactedIsThePlaintext, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/ConnString_IsASecret", ""},
	{dsFlawEmptyInventory, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Inventory_ListsLiveResources", ""},
	{dsFlawHealthNeverReachable, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Health_ReportsAReachableBranch", ""},
	{dsFlawHealthErrorsWhenGone, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Health_ReportsADestroyedBranch", ""},
	{dsFlawCancelledBranchIsHidden, dsShapeGolden, "--- FAIL: TestDatastoreSuiteChild/Cancellation_LeavesNoUntrackedResource", ""},
	// Not a behaviour. The leak check runs after every behaviour has passed,
	// and it is the one assertion a green suite can still fail. Without this
	// row a suite that leaked a resource per refresh would report a clean
	// sweep in exactly the words it uses for a correct one.
	{dsFlawLeaksOnRefresh, dsShapeGolden, "the suite left", ""},
}

const (
	dsChildEnv = "AF_DATASTORE_SELFTEST_CHILD"
	dsFlawEnv  = "AF_DATASTORE_SELFTEST_FLAW"
	dsShapeEnv = "AF_DATASTORE_SELFTEST_SHAPE"
)

// TestDatastoreSuiteChild runs the suite against the fake, and is normally
// skipped.
//
// It exists to be re-executed as a subprocess, because a suite proving it can
// fail has to actually fail, and a test that fails inside the process
// asserting on it fails that process too. The subprocess is the only way to
// watch a red run and call it a pass.
func TestDatastoreSuiteChild(t *testing.T) {
	if os.Getenv(dsChildEnv) != "1" {
		t.Skip("skipped: runs only as the subprocess of TestDatastoreSuite_*")
	}
	shape := os.Getenv(dsShapeEnv)
	flaw := os.Getenv(dsFlawEnv)

	state := newDSState()
	RunDatastore(t, func(*testing.T) provider.Datastore {
		return newFakeDatastore(state, shape, flaw)
	}, DatastoreOptions{
		// Short on purpose. Nothing here touches a container or a network, so
		// a behaviour that has not finished in five seconds is one waiting on
		// something the fake will never do.
		Timeout: 5 * time.Second,
	})
}

// TestDatastoreSuite_PassesACorrectDatastore is the positive control.
//
// Without it the negative controls prove only that the suite can fail, which a
// suite that failed on everything would also satisfy.
func TestDatastoreSuite_PassesACorrectDatastore(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{dsShapeGolden, dsShapeCache} {
		shape := shape
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			out, err := runDatastoreChild(t, shape, dsFlawNone)
			if err != nil {
				t.Fatalf("the suite failed against a correct %s datastore:\n%s", shape, out)
			}
			for _, b := range datastoreBehaviors {
				name := "TestDatastoreSuiteChild/" + b.Name
				passed := strings.Contains(out, "--- PASS: "+name)
				skipped := strings.Contains(out, "--- SKIP: "+name)
				if !passed && !skipped {
					t.Errorf("behaviour %s neither passed nor skipped against a correct %s "+
						"datastore, so it never ran at all:\n%s", b.Name, shape, dsTail(out))
				}
			}
		})
	}
}

// TestDatastoreSuite_RunsEveryBehaviourAcrossTheTwoShapes is what stops the
// positive control above from being satisfied by skipping.
//
// A skip is a legitimate answer for one shape and it is not an answer for
// every shape. A behaviour skipped by the golden store AND by the cache has
// never been run against anything correct, and the suite would report a clean
// sweep for a property nothing has ever exercised. This is the check that
// caught nothing yet and is the reason the cache shape exists at all.
func TestDatastoreSuite_RunsEveryBehaviourAcrossTheTwoShapes(t *testing.T) {
	t.Parallel()
	outs := map[string]string{}
	for _, shape := range []string{dsShapeGolden, dsShapeCache} {
		out, err := runDatastoreChild(t, shape, dsFlawNone)
		if err != nil {
			t.Fatalf("the suite failed against a correct %s datastore:\n%s", shape, out)
		}
		outs[shape] = out
	}
	for _, b := range datastoreBehaviors {
		ran := false
		for _, out := range outs {
			if strings.Contains(out, "--- PASS: TestDatastoreSuiteChild/"+b.Name) {
				ran = true
			}
		}
		if !ran {
			t.Errorf("behaviour %s was skipped by both shapes, so nothing correct has ever "+
				"run it and its assertions have never executed", b.Name)
		}
	}
}

// TestDatastoreSuite_FailsEachBrokenDatastore is the claim in the file's own
// doc comment, checked.
func TestDatastoreSuite_FailsEachBrokenDatastore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped: -short, and this runs one subprocess per flaw")
	}
	t.Parallel()
	for _, control := range dsNegativeControls {
		control := control
		t.Run(control.flaw, func(t *testing.T) {
			t.Parallel()
			out, err := runDatastoreChild(t, control.shape, control.flaw)
			if err == nil {
				t.Fatalf("a datastore whose flaw is %q passed the suite; the behaviour that "+
					"should have caught it is asserting something that cannot go red:\n%s",
					control.flaw, dsTail(out))
			}
			if !strings.Contains(out, control.wants) {
				t.Errorf("a datastore whose flaw is %q failed, but not where it should have: "+
					"expected %q in the output. A behaviour failing for the wrong reason is "+
					"not a control.\n%s", control.flaw, control.wants, dsTail(out))
			}
		})
	}
}

// TestDatastoreSuite_EveryBehaviourHasAControl is the check that keeps the
// table above honest as behaviours are added.
//
// A behaviour with no flaw pointed at it has never been shown to go red, which
// is the state every assertion is in until somebody proves otherwise. Adding a
// behaviour and no control is exactly how a suite acquires a line that always
// passes.
func TestDatastoreSuite_EveryBehaviourHasAControl(t *testing.T) {
	t.Parallel()
	controlled := map[string]bool{}
	for _, c := range dsNegativeControls {
		const prefix = "--- FAIL: TestDatastoreSuiteChild/"
		if strings.HasPrefix(c.wants, prefix) {
			controlled[strings.TrimPrefix(c.wants, prefix)] = true
		}
	}
	for _, b := range datastoreBehaviors {
		if !controlled[b.Name] {
			t.Errorf("behaviour %s has no negative control, so nothing has shown it can "+
				"fail; add a flaw to the fake and a row to dsNegativeControls", b.Name)
		}
	}
}

// runDatastoreChild re-executes this test binary running only the child test.
func runDatastoreChild(t *testing.T, shape, flaw string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestDatastoreSuiteChild$", "-test.v", "-test.timeout", "4m")
	cmd.Env = append(os.Environ(), dsChildEnv+"=1", dsShapeEnv+"="+shape, dsFlawEnv+"="+flaw)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func dsTail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

package conformance

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The claim RunEmulator's doc comment makes, checked.
//
// A conformance suite nobody has proved can fail is a list of function calls
// that always pass, and this repository has already shipped one behaviour that
// asserted nothing for every provider that behaved correctly. That was found
// by a self test of exactly this shape and by nothing else.
//
// The rule here is one break per ASSERTION rather than one per behaviour,
// because require and Fatalf both stop at the first failure: a behaviour with
// three assertions and one control has shown that its first assertion can go
// red and has shown nothing at all about the other two, which can be dead and
// look alive. So every row below names the assertion it points at, and
// TestEmulatorSuite_EveryBehaviourHasAControl counts behaviours while the
// wants text is what pins the row to a specific one.

var emuNegativeControls = []struct {
	flaw  string
	shape string
	// wants is the subtest that must fail.
	wants string
	// says is a distinctive fragment of the message that assertion produces.
	// It is what makes a row a control for ONE assertion: without it a flaw
	// that reds the behaviour for any reason at all would satisfy the row.
	says string
	// also names the other behaviours this flaw reds, where it reds more than
	// one, so that a reader can see the overlap is known rather than missed.
	also string
}{
	{
		emuFlawNoName, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Name_IsNotEmpty",
		"the emulator's name is empty", "",
	},
	{
		emuFlawNoHosts, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Hosts_AreDeclared",
		"answers for no hosts",
		"every behaviour that sends a probe reds too, because a probe with no host of its " +
			"own is sent to the first declared host and there is none",
	},
	{
		emuFlawHostIsAURL, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Hosts_AreDeclared",
		"is not a bare hostname",
		"nothing else reds: the fake still answers, because the fake is not matching on the " +
			"host. Against a real sidecar the rule would match nothing, which is exactly the " +
			"silence this assertion exists to catch before it happens",
	},
	{
		emuFlawTransportError, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Covered_IsAnswered",
		"did not reach the emulator",
		"Covered_IsNotRefused, both State behaviours and LiveCredential_IsRefused red too, " +
			"because every one of them sends a probe first",
	},
	{
		emuFlawRefusesEverything, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Covered_IsNotRefused",
		"was refused with 403",
		"both State behaviours red too, because a refused write creates nothing. That is the " +
			"overlap this flaw exists to show: an emulator that refuses everything passes " +
			"Uncovered_IsRefusedInTheProvidersErrorShape and Reach_FindsNoRouteOut",
	},
	{
		emuFlawCoveredCarriesErrorCode, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Covered_IsNotRefused",
		"was refused in a shape a status check does not see",
		"both State behaviours red too, because this answer creates nothing either. It is " +
			"the control for the body half of the behaviour, which a status check alone " +
			"would never have reached",
	},
	{
		emuFlawAnswersUncovered, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Uncovered_IsRefusedInTheProvidersErrorShape",
		"rather than refused",
		"",
	},
	{
		emuFlawWrongErrorContentType, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Uncovered_IsRefusedInTheProvidersErrorShape",
		"this provider's own errors are",
		"",
	},
	{
		emuFlawUncoveredWithoutCode, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Uncovered_IsRefusedInTheProvidersErrorShape",
		"does not carry the error code",
		"",
	},
	{
		emuFlawEmptyState, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/State_IsEnumerable",
		"with nothing new in it",
		"State_IsDiscardedByReset reds too, and it reds on its own assertion: there is " +
			"nothing for the reset to discard, which would otherwise pass",
	},
	{
		emuFlawNamelessStateItem, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/State_IsEnumerable",
		"both fields identify it",
		"",
	},
	{
		emuFlawResetKeepsState, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/State_IsDiscardedByReset",
		"survived the reset",
		"",
	},
	{
		emuFlawLiveCredentialAccepted, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/LiveCredential_IsRefused",
		"rather than refused with 403",
		"",
	},
	{
		emuFlawSilentRefusal, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/LiveCredential_IsRefused",
		"does not say it was about a live credential",
		"",
	},
	{
		emuFlawHasARouteOut, emuShapeObject,
		"--- FAIL: TestEmulatorSuiteChild/Reach_FindsNoRouteOut",
		"opened a connection to",
		"",
	},
}

const (
	emuChildEnv = "AF_EMULATOR_SELFTEST_CHILD"
	emuFlawEnv  = "AF_EMULATOR_SELFTEST_FLAW"
	emuShapeEnv = "AF_EMULATOR_SELFTEST_SHAPE"
)

// TestEmulatorSuiteChild runs the suite against the fake, and is normally
// skipped.
//
// It exists to be re-executed as a subprocess, because a suite proving it can
// fail has to actually fail, and a test that fails inside the process
// asserting on it fails that process too.
func TestEmulatorSuiteChild(t *testing.T) {
	if os.Getenv(emuChildEnv) != "1" {
		t.Skip("skipped: runs only as the subprocess of TestEmulatorSuite_*")
	}
	shape := os.Getenv(emuShapeEnv)
	flaw := os.Getenv(emuFlawEnv)

	store := newFakeEmulatorStore()
	RunEmulator(t, func(*testing.T) provider.Emulator {
		return newFakeEmulator(store, shape, flaw)
	}, EmulatorOptions{
		// Short on purpose. Nothing here touches a container or a network, so
		// a behaviour that has not finished in five seconds is one waiting on
		// something the fake will never do.
		Timeout: 5 * time.Second,
		// The fake reports no route whatever the address is, so this names an
		// address nothing will be dialled at. It is spelled out rather than
		// left as the default so that a reader does not go looking for where
		// the self test contacts 1.1.1.1, which it never does.
		ReachAddress: "203.0.113.1:9",
	})
}

// TestEmulatorSuite_PassesACorrectEmulator is the positive control.
//
// Without it the negative controls prove only that the suite can fail, which a
// suite that failed on everything would also satisfy.
func TestEmulatorSuite_PassesACorrectEmulator(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{emuShapeObject, emuShapeQueue} {
		shape := shape
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			out, err := runEmulatorChild(t, shape, emuFlawNone)
			if err != nil {
				t.Fatalf("the suite failed against a correct %s emulator:\n%s", shape, out)
			}
			for _, b := range emulatorBehaviors {
				name := "TestEmulatorSuiteChild/" + b.Name
				passed := strings.Contains(out, "--- PASS: "+name)
				skipped := strings.Contains(out, "--- SKIP: "+name)
				if !passed && !skipped {
					t.Errorf("behaviour %s neither passed nor skipped against a correct %s "+
						"emulator, so it never ran at all:\n%s", b.Name, shape, emuTail(out))
				}
			}
		})
	}
}

// TestEmulatorSuite_RunsEveryBehaviourAcrossTheTwoShapes is what stops the
// positive control above from being satisfied by skipping.
//
// A behaviour skipped by the object shape AND by the queue shape has never
// been run against anything correct, and the suite would report a clean sweep
// for a property nothing has ever exercised.
func TestEmulatorSuite_RunsEveryBehaviourAcrossTheTwoShapes(t *testing.T) {
	t.Parallel()
	outs := map[string]string{}
	for _, shape := range []string{emuShapeObject, emuShapeQueue} {
		out, err := runEmulatorChild(t, shape, emuFlawNone)
		if err != nil {
			t.Fatalf("the suite failed against a correct %s emulator:\n%s", shape, out)
		}
		outs[shape] = out
	}
	for _, b := range emulatorBehaviors {
		ran := false
		for _, out := range outs {
			if strings.Contains(out, "--- PASS: TestEmulatorSuiteChild/"+b.Name) {
				ran = true
			}
		}
		if !ran {
			t.Errorf("behaviour %s was skipped by both shapes, so nothing correct has ever "+
				"run it and its assertions have never executed", b.Name)
		}
	}
}

// TestEmulatorSuite_TheQueueShapeSkipsRatherThanFails is the assertion that
// keeps Creates false an honest answer.
//
// A read only covered probe is a legitimate emulator, and a suite that failed
// it for holding no state would push a provider author into declaring Creates
// on a probe that creates nothing, which turns State_IsEnumerable into a
// behaviour that reds for a reason nobody can act on.
func TestEmulatorSuite_TheQueueShapeSkipsRatherThanFails(t *testing.T) {
	t.Parallel()
	out, err := runEmulatorChild(t, emuShapeQueue, emuFlawNone)
	if err != nil {
		t.Fatalf("the suite failed against a correct queue emulator:\n%s", out)
	}
	for _, name := range []string{"State_IsEnumerable", "State_IsDiscardedByReset"} {
		if !strings.Contains(out, "--- SKIP: TestEmulatorSuiteChild/"+name) {
			t.Errorf("%s did not skip against a covered probe that creates nothing:\n%s",
				name, emuTail(out))
		}
	}
}

// TestEmulatorSuite_FailsEachBrokenEmulator is the claim in the file's own doc
// comment, checked.
func TestEmulatorSuite_FailsEachBrokenEmulator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped: -short, and this runs one subprocess per flaw")
	}
	t.Parallel()
	for _, control := range emuNegativeControls {
		control := control
		t.Run(control.flaw, func(t *testing.T) {
			t.Parallel()
			out, err := runEmulatorChild(t, control.shape, control.flaw)
			if err == nil {
				t.Fatalf("an emulator whose flaw is %q passed the suite; the behaviour that "+
					"should have caught it is asserting something that cannot go red:\n%s",
					control.flaw, emuTail(out))
			}
			if !strings.Contains(out, control.wants) {
				t.Errorf("an emulator whose flaw is %q failed, but not where it should have: "+
					"expected %q in the output. A behaviour failing for the wrong reason is "+
					"not a control.\n%s", control.flaw, control.wants, emuTail(out))
			}
			if !strings.Contains(out, control.says) {
				t.Errorf("an emulator whose flaw is %q failed in the right behaviour and not "+
					"on the right assertion: expected %q in the output. A behaviour has more "+
					"than one assertion and only the one that produced this message has been "+
					"shown to work.\n%s", control.flaw, control.says, emuTail(out))
			}
		})
	}
}

// TestEmulatorSuite_EveryBehaviourHasAControl is the check that keeps the
// table above honest as behaviours are added.
//
// A behaviour with no flaw pointed at it has never been shown to go red, which
// is the state every assertion is in until somebody proves otherwise.
func TestEmulatorSuite_EveryBehaviourHasAControl(t *testing.T) {
	t.Parallel()
	controlled := map[string]bool{}
	for _, c := range emuNegativeControls {
		const prefix = "--- FAIL: TestEmulatorSuiteChild/"
		if strings.HasPrefix(c.wants, prefix) {
			controlled[strings.TrimPrefix(c.wants, prefix)] = true
		}
	}
	for _, b := range emulatorBehaviors {
		if !controlled[b.Name] {
			t.Errorf("behaviour %s has no negative control, so nothing has shown it can "+
				"fail; add a flaw to the fake and a row to emuNegativeControls", b.Name)
		}
	}
}

// TestEmulatorSuite_EveryFlawIsUsed is the other half of the ledger.
//
// A flaw the fake implements and no row exercises is a break nobody has ever
// run, which reads in the diff exactly like a control that works.
func TestEmulatorSuite_EveryFlawIsUsed(t *testing.T) {
	t.Parallel()
	used := map[string]bool{emuFlawNone: true}
	for _, c := range emuNegativeControls {
		used[c.flaw] = true
	}
	all := []string{
		emuFlawNone, emuFlawNoName, emuFlawNoHosts, emuFlawHostIsAURL,
		emuFlawTransportError, emuFlawRefusesEverything, emuFlawCoveredCarriesErrorCode,
		emuFlawAnswersUncovered, emuFlawWrongErrorContentType, emuFlawUncoveredWithoutCode,
		emuFlawEmptyState, emuFlawNamelessStateItem, emuFlawResetKeepsState,
		emuFlawLiveCredentialAccepted, emuFlawSilentRefusal, emuFlawHasARouteOut,
	}
	for _, flaw := range all {
		if !used[flaw] {
			t.Errorf("the fake implements the flaw %q and no control uses it, so that break "+
				"has never been run", flaw)
		}
	}
}

// runEmulatorChild re-executes this test binary running only the child test.
func runEmulatorChild(t *testing.T, shape, flaw string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0],
		"-test.run", "^TestEmulatorSuiteChild$", "-test.v", "-test.timeout", "4m")
	cmd.Env = append(os.Environ(), emuChildEnv+"=1", emuShapeEnv+"="+shape, emuFlawEnv+"="+flaw)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func emuTail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

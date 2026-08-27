package conformance

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The flaws the fake runtime can be built with. Each is one thing a real
// runtime could plausibly get wrong, and each has exactly one behavior in the
// suite whose job is to catch it.
const (
	flawNone                       = ""
	flawNoName                     = "no-name"
	flawLiesAboutLogs              = "lies-about-logs"
	flawAcceptsEmptyEnvID          = "accepts-empty-env-id"
	flawNoProxy                    = "no-proxy"
	flawNoURL                      = "no-url"
	flawDuplicatesOnSecondUp       = "duplicates-on-second-up"
	flawIgnoresDependencies        = "ignores-dependencies"
	flawHangsOnCycle               = "hangs-on-cycle"
	flawIgnoresMissingDependency   = "ignores-missing-dependency"
	flawStartsAfterFailedMigration = "starts-after-failed-migration"
	flawRollsBackFailedService     = "rolls-back-failed-service"
	flawIgnoresJournalRefusal      = "ignores-journal-refusal"
	flawJournalsUnfindableNames    = "journals-unfindable-names"
	flawJournalsAfterCreating      = "journals-after-creating"
	flawLosesServiceKind           = "loses-service-kind"
	flawNoExitCode                 = "no-exit-code"
	flawExitCodeWhileRunning       = "exit-code-while-running"
	flawErrorsOnUnknownEnv         = "errors-on-unknown-env"
	flawDownLeavesResources        = "down-leaves-resources"
	flawDownErrorsWhenAbsent       = "down-errors-when-absent"
	flawDownNotIdempotent          = "down-not-idempotent"
	flawDownRemovesEverything      = "down-removes-everything"
	flawEmptyInventory             = "empty-inventory"
	flawInventoryWithoutEnvID      = "inventory-without-env-id"
	flawNoPolicyAllowsEverything   = "no-policy-allows-everything"
	flawBlocksAllowedHost          = "blocks-allowed-host"
	flawAllowsUnnamedHost          = "allows-unnamed-host"
	flawHonoursProxyVarsOnly       = "honours-proxy-vars-only"
	flawRawAddressEscapes          = "raw-address-escapes"
	flawMetadataReachable          = "metadata-reachable"
	flawUDPEscapes                 = "udp-escapes"
	flawNamesCrossEnvironments     = "names-cross-environments"
	flawNoLogs                     = "no-logs"
	flawLeaksOnTeardown            = "leaks-on-teardown"
)

// negativeControls pairs each flaw with the behavior that has to notice it.
//
// This table is the actual claim the package doc makes. Every row is one
// assertion in the suite being shown to go red when the property it asserts is
// untrue, which is the only thing that distinguishes a conformance suite from
// a list of function calls that always pass.
var negativeControls = []struct {
	flaw string
	// wants is the text that must appear in the failing run's output.
	wants string
}{
	{flawNoName, "--- FAIL: TestRuntimeSuiteChild/Name_IsNotEmpty"},
	{flawLiesAboutLogs, "--- FAIL: TestRuntimeSuiteChild/Capabilities_MatchWhatIsImplemented"},
	{flawAcceptsEmptyEnvID, "--- FAIL: TestRuntimeSuiteChild/Up_RefusesAnEnvironmentWithNoID"},
	{flawNoProxy, "--- FAIL: TestRuntimeSuiteChild/Up_StartsAServiceAndReportsIt"},
	{flawNoURL, "--- FAIL: TestRuntimeSuiteChild/Up_ReportsAReachableURL"},
	{flawDuplicatesOnSecondUp, "--- FAIL: TestRuntimeSuiteChild/Up_IsIdempotentForOneEnvironment"},
	{flawIgnoresDependencies, "--- FAIL: TestRuntimeSuiteChild/Up_StartsDependenciesFirst"},
	{flawHangsOnCycle, "--- FAIL: TestRuntimeSuiteChild/Up_ReportsACycleRatherThanHanging"},
	{flawIgnoresMissingDependency, "--- FAIL: TestRuntimeSuiteChild/Up_ReportsAMissingDependency"},
	{flawStartsAfterFailedMigration, "--- FAIL: TestRuntimeSuiteChild/Up_DoesNotStartAServiceWhoseMigrationFailed"},
	{flawRollsBackFailedService, "--- FAIL: TestRuntimeSuiteChild/Up_LeavesAFailedServiceFindable"},
	{flawIgnoresJournalRefusal, "--- FAIL: TestRuntimeSuiteChild/Up_CreatesNothingTheJournalRefused"},
	{flawJournalsUnfindableNames, "--- FAIL: TestRuntimeSuiteChild/Up_JournalsResourcesTeardownCanFind"},
	{flawJournalsAfterCreating, "--- FAIL: TestRuntimeSuiteChild/Up_JournalsBeforeCreating"},
	{flawLosesServiceKind, "--- FAIL: TestRuntimeSuiteChild/Status_ReportsRunningServices"},
	{flawNoExitCode, "--- FAIL: TestRuntimeSuiteChild/Status_ReportsAnExitCode"},
	{flawExitCodeWhileRunning, "--- FAIL: TestRuntimeSuiteChild/Status_ReportsAnExitCode"},
	{flawErrorsOnUnknownEnv, "--- FAIL: TestRuntimeSuiteChild/Status_OfAnUnknownEnvironmentIsEmpty"},
	{flawDownLeavesResources, "--- FAIL: TestRuntimeSuiteChild/Down_RemovesEverythingItCreated"},
	{flawDownErrorsWhenAbsent, "--- FAIL: TestRuntimeSuiteChild/Down_OfSomethingNeverUpSucceeds"},
	{flawDownNotIdempotent, "--- FAIL: TestRuntimeSuiteChild/Down_IsIdempotent"},
	{flawDownRemovesEverything, "--- FAIL: TestRuntimeSuiteChild/Down_TouchesOnlyItsOwnEnvironment"},
	{flawEmptyInventory, "--- FAIL: TestRuntimeSuiteChild/Inventory_ListsLiveResources"},
	{flawInventoryWithoutEnvID, "--- FAIL: TestRuntimeSuiteChild/Inventory_AttributesResourcesToEnvironments"},
	{flawNoPolicyAllowsEverything, "--- FAIL: TestRuntimeSuiteChild/Egress_NoPolicyMeansNothingGetsOut"},
	{flawBlocksAllowedHost, "--- FAIL: TestRuntimeSuiteChild/Egress_AllowedHostIsReached"},
	{flawAllowsUnnamedHost, "--- FAIL: TestRuntimeSuiteChild/Egress_HostWithNoRuleIsRefused"},
	{flawHonoursProxyVarsOnly, "--- FAIL: TestRuntimeSuiteChild/Egress_AppliesToAClientThatIgnoresProxyVariables"},
	{flawRawAddressEscapes, "--- FAIL: TestRuntimeSuiteChild/Egress_CannotBeBypassedByAddress"},
	{flawMetadataReachable, "--- FAIL: TestRuntimeSuiteChild/Egress_CannotReachTheMetadataEndpoint"},
	{flawUDPEscapes, "--- FAIL: TestRuntimeSuiteChild/Egress_CannotBeBypassedByUDP"},
	{flawNamesCrossEnvironments, "resolved to ANOTHER environment's service"},
	{flawNoLogs, "--- FAIL: TestRuntimeSuiteChild/Logs_ReturnWhatAServiceWrote"},
	// Not a behavior. The leak check runs after every behavior has passed, and
	// it is the one assertion a green suite can still fail.
	{flawLeaksOnTeardown, "the suite left"},
}

const (
	childEnv = "AF_CONFORMANCE_SELFTEST_CHILD"
	flawEnv  = "AF_CONFORMANCE_SELFTEST_FLAW"
)

// TestRuntimeSuiteChild runs the suite against the fake, and is normally
// skipped.
//
// It exists to be re-executed as a subprocess by the two tests below, because
// a suite proving it can fail has to actually fail, and a test that fails
// inside the process asserting on it fails that process too. The subprocess is
// the only way to watch a red run and call it a pass.
func TestRuntimeSuiteChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("skipped: runs only as the subprocess of TestRuntimeSuite_*")
	}
	flaw := os.Getenv(flawEnv)

	// A real server for the allowed host, so that the suite's own reachability
	// check passes and the egress behaviors run instead of skipping. It also
	// keeps the whole self test offline: nothing here touches a real network.
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("reachable"))
	}))
	defer allowed.Close()

	state := newFakeState()
	defer state.Shutdown()

	RunRuntime(t, func(t *testing.T) provider.Runtime {
		f := newFakeRuntime(state, flaw, strings.TrimPrefix(allowed.URL, "http://"))
		if flaw == flawLiesAboutLogs {
			return noLogsFake{inner: f}
		}
		return f
	}, RuntimeOptions{
		// Short on purpose. Nothing here touches a container or a network, so
		// a behavior that has not finished in five seconds is one waiting on
		// something the fake will never do, which is the point of most of
		// these runs.
		Timeout:     5 * time.Second,
		ShellImage:  "fake:none",
		AllowedHost: strings.TrimPrefix(allowed.URL, "http://"),
		RefusedHost: "refused.invalid",
	})
}

// TestRuntimeSuite_PassesACorrectRuntime is the positive control.
//
// Without it the negative controls below prove only that the suite can fail,
// which a suite that failed on everything would also satisfy.
func TestRuntimeSuite_PassesACorrectRuntime(t *testing.T) {
	out, err := runChild(t, flawNone)
	if err != nil {
		t.Fatalf("the suite failed against a correct runtime:\n%s", out)
	}
	// Every behavior has to have actually run. A suite that skipped half of
	// itself would pass here and mean nothing, and skipping is exactly what
	// happens when a capability is misdeclared.
	for _, b := range runtimeBehaviors {
		want := "--- PASS: TestRuntimeSuiteChild/" + b.Name
		if !strings.Contains(out, want) {
			t.Errorf("behavior %s did not pass against a correct runtime; it was "+
				"skipped or never ran:\n%s", b.Name, tail(out))
		}
	}
}

// TestRuntimeSuite_FailsEachBrokenRuntime is the claim in the package doc,
// checked.
func TestRuntimeSuite_FailsEachBrokenRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped: -short, and this runs one subprocess per flaw")
	}
	for _, control := range negativeControls {
		control := control
		t.Run(control.flaw, func(t *testing.T) {
			t.Parallel()
			out, err := runChild(t, control.flaw)
			if err == nil {
				t.Fatalf("a runtime whose flaw is %q passed the suite; the behavior "+
					"that should have caught it is asserting something that cannot "+
					"go red:\n%s", control.flaw, tail(out))
			}
			if !strings.Contains(out, control.wants) {
				t.Errorf("a runtime whose flaw is %q failed, but not where it should "+
					"have: expected %q in the output. A behavior failing for the "+
					"wrong reason is not a control.\n%s",
					control.flaw, control.wants, tail(out))
			}
		})
	}
}

// runChild re-executes this test binary running only the child test.
func runChild(t *testing.T, flaw string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestRuntimeSuiteChild$", "-test.v", "-test.timeout", "4m")
	cmd.Env = append(os.Environ(), childEnv+"=1", flawEnv+"="+flaw)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tail keeps a failure message readable when the child printed a full verbose
// run.
func tail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

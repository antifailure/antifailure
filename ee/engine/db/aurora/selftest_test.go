// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// The conformance run in this package proves nothing until somebody has
// watched it fail.
//
// A suite pointed at a fake control plane is exactly the place a green result
// goes vacuous, and it goes vacuous quietly: a fake that answers whatever the
// provider hoped for makes every behaviour pass, and the run reads identically
// to one against a control plane that told the truth. So this points the same
// suite at a control plane broken in ONE named way at a time and requires it to
// go RED in the behaviour that names that guarantee.
//
// It needs a SUBPROCESS, for the reason the engine's own database self test
// needs one: a suite proving it can fail has to actually fail, and a failure
// inside the process asserting on it fails that process too. The child below
// is skipped unless it is the child, and the parent re-executes the test binary
// once per fault.
//
// It needs a POSITIVE CONTROL, and that is the half almost nobody writes.
// Without it a red proves nothing, because a suite that fails against
// everything, including a correct provider, would produce the same table. Each
// behaviour is therefore run twice: once against the unbroken fake, which must
// pass, and once against the fake broken in the way that behaviour exists to
// catch, which must fail.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/ee/engine/db/aurora/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const (
	childEnv = "AF_AURORA_SELFTEST_CHILD"
	faultEnv = "AF_AURORA_SELFTEST_FAULT"
)

// proof is one fault and the behaviour that has to catch it.
//
// The pairing is the assertion. A fault that turned the suite red SOMEWHERE
// would prove only that the suite is fragile; a fault that turns red in the
// behaviour whose one sentence describes that guarantee is the suite doing its
// job.
type proof struct {
	fault    fakerds.Fault
	behavior string
	// because is what a reader of a failing table needs: what would be shipped
	// if this behaviour ever stopped going red.
	because string
}

func proofs() []proof {
	return []proof{
		{
			fakerds.FaultCloneSharesItsSource, "Branch_IsIsolatedFromTheGolden",
			"two environments would be writing to one database while every endpoint, " +
				"every status and every row count looked right",
		},
		{
			fakerds.FaultCloneIsEmpty, "Branch_ReadsAKnownRow",
			"a preview environment would come up with a schema and no data in it, and " +
				"the control plane would report it healthy",
		},
		{
			fakerds.FaultTagsAreNotRecorded, "List_ReturnsWhatWasCreated",
			"a refresh would report a published golden that nothing can find afterwards",
		},
		{
			fakerds.FaultDeleteDoesNotDelete, "Destroy_RemovesTheBranch",
			"every environment ever torn down would go on costing money, and the leak " +
				"detector would be reading the same list the provider is",
		},
		{
			fakerds.FaultPasswordIsNotRotated, "Health_ReportsAReachableBranch",
			"the derived credential would not open the database it was derived for, " +
				"which is the whole rotation this provider does",
		},
		{
			fakerds.FaultInstanceNeverBecomesAvailable, "Refresh_ProducesAVerifiedGolden",
			"a golden would be published from a cluster nothing had connected to, so " +
				"nothing was masked and nothing was verified",
		},
	}
}

// TestEveryFaultIsUsed is the guard against this file drifting away from the
// fake.
//
// A fault added to fakerds and never pointed at a behaviour is a fault nobody
// is proving anything with, and it would sit there looking like coverage.
func TestEveryFaultInTheFakeIsProvedToBeCaught(t *testing.T) {
	used := map[fakerds.Fault]bool{}
	for _, p := range proofs() {
		used[p.fault] = true
	}
	for _, fault := range fakerds.AllFaults() {
		require.True(t, used[fault],
			"the fake can be broken with %q and no behaviour is required to catch it", fault)
	}
}

// TestTheSuitePassesAgainstTheUnbrokenFake is the positive control.
func TestTheSuitePassesAgainstTheUnbrokenFake(t *testing.T) {
	requirePostgres(t)
	for _, p := range proofs() {
		t.Run(p.behavior, func(t *testing.T) {
			passed, out := runChild(t, "", p.behavior)
			require.True(t, passed,
				"%s fails against a control plane that is not broken, so its red below "+
					"proves nothing:\n%s", p.behavior, out)
			require.Contains(t, out, "--- PASS: TestConformanceChild/"+p.behavior,
				"%s did not run; a behaviour that skipped is a false green:\n%s", p.behavior, out)
		})
	}
}

// TestTheSuiteFailsAgainstABrokenControlPlane is the negative half.
func TestTheSuiteFailsAgainstABrokenControlPlane(t *testing.T) {
	requirePostgres(t)
	for _, p := range proofs() {
		t.Run(string(p.fault), func(t *testing.T) {
			passed, out := runChild(t, p.fault, p.behavior)
			require.False(t, passed,
				"the suite stayed green against a control plane where %s. %s no longer "+
					"checks what it claims:\n%s", p.because, p.behavior, out)
			require.Contains(t, out, "--- FAIL: TestConformanceChild/"+p.behavior,
				"the suite went red somewhere other than %s, so what failed is not the "+
					"guarantee this fault breaks:\n%s", p.behavior, out)
		})
	}
}

// TestConformanceChild is the suite under examination.
//
// It runs only when the parent re-executes this binary, so an ordinary
// go test ./... never sees it fail on purpose.
func TestConformanceChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of the self test")
	}
	fault := fakerds.Fault(os.Getenv(faultEnv))
	server := newFake(t, conformance.DefaultSeedSQL, fault)

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		opts := options(t, server)
		// Seconds rather than the thirty the other suites use. One fault holds
		// an instance in creating for ever, and every behaviour would
		// otherwise wait the full timeout to learn the same thing.
		opts.ReadyTimeout = 5 * time.Second
		p, err := aurora.New(context.Background(), opts)
		require.NoError(t, err)
		return p
	}, conformance.Options{Timeout: 90 * time.Second})
}

// runChild executes one behaviour in a subprocess and reports whether it
// passed, with its output for the failure message.
func runChild(t *testing.T, fault fakerds.Fault, behavior string) (bool, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0],
		"-test.run", "TestConformanceChild/^"+behavior+"$",
		"-test.v", "-test.timeout", "10m")
	cmd.Env = append(os.Environ(), childEnv+"=1", faultEnv+"="+string(fault))
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(text, "runs only as the child") {
		t.Fatalf("the child skipped itself, so its result says nothing:\n%s", text)
	}
	return err == nil, text
}

package chaos_test

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
)

// A roster, a tally, and a package that fails when it proved less than a
// reader would take `ok` to mean.
//
// This exists because `go test` without -v prints neither skip lines nor
// anything a test wrote, so a package whose every test skipped reports `ok`
// with a plausible duration and nothing distinguishes it from one that ran.
// That is bad everywhere and worst here: this suite exists to prove that
// failure paths work, so a suite that quietly proves nothing is the exact
// shape it was written to catch. It also happened, twice, elsewhere in this
// repository on the same night, to two different people who had both already
// written warnings about it.
//
// The print below is therefore NOT the mechanism, and it was a mistake to
// describe it as one. CI runs `go test ./... -race` with no -v, which
// discards a passing package's stdout and stderr both; the tally is visible
// only on the run that fails, which is the run that needs it. The mechanism is
// the exit code, and what it buys is that `ok github.com/.../chaos` is a
// statement with content: these ten scenarios, by name, each ran to a verdict.
//
// Three ways a green run could otherwise mean less than it says, all closed:
//   - a scenario skipped, because Docker was absent or a helper gave up. Only
//     AF_SKIP_DOCKER excuses that, and it has to be asked for.
//   - a scenario deleted, renamed, or added without registering itself. The
//     roster is compared as a set, so the suite notices its own shape changing
//     and makes somebody say so here.
//   - a -run filter, which narrows the suite silently and is the easiest of
//     the three to do by accident. A partial run is reported as partial.
var roster = []string{
	"TestACommandRunAgainstALiveControlPlaneReportsToIt",
	"TestAKilledEngineIsReconciledFromTheJournal",
	"TestAProviderThatReportsADeleteAsCompleteHasActuallyReleasedIt",
	"TestAResourceThisBuildCannotDeleteIsReportedRatherThanForgotten",
	"TestATeardownAgainstAnUnreachableProviderSaysSoAndTheNextOneFinishesIt",
	"TestABranchInterruptedPartWayThroughIsEitherGoneOrInTheInventory",
	"TestAnIncompleteTeardownCarriesTheCodeAndTheExitStatusThatSayItIsIncomplete",
	"TestEventsFromACommandRunWhileTheControlPlaneWasDownArriveWithTheNextOne",
	"TestReconcilingAResourceThatIsAlreadyGoneSucceeds",
	"TestReconcilingTwiceIsNotAnError",
}

var (
	tally   sync.Mutex
	ran     = map[string]bool{}
	skipped []string
)

// scenario registers a test with the tally. Called as the first line of each
// one, before any precondition, so a test that skips inside a helper is still
// counted as having been attempted.
func scenario(t *testing.T) {
	t.Helper()
	tally.Lock()
	ran[t.Name()] = true
	tally.Unlock()
	t.Cleanup(func() {
		if !t.Skipped() {
			return
		}
		tally.Lock()
		skipped = append(skipped, t.Name())
		tally.Unlock()
	})
}

func TestMain(m *testing.M) {
	code := m.Run()

	tally.Lock()
	defer tally.Unlock()
	fmt.Printf("chaos: %d scenarios attempted, %d skipped\n", len(ran), len(skipped))

	if len(skipped) > 0 && os.Getenv("AF_SKIP_DOCKER") == "" {
		fmt.Printf("chaos: these skipped without AF_SKIP_DOCKER being set, so this run "+
			"proved less than it appears to have: %v\n", skipped)
		code = 1
	}

	var missing, unexpected []string
	on := map[string]bool{}
	for _, name := range roster {
		on[name] = true
		if !ran[name] {
			missing = append(missing, name)
		}
	}
	for name := range ran {
		if !on[name] {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)

	if len(missing) > 0 {
		fmt.Printf("chaos: on the roster and did not run, so this run proved less than "+
			"the package claims: %v\n", missing)
		fmt.Println("chaos: a -run filter does this, and so does deleting or renaming a " +
			"scenario without saying so in the roster")
		code = 1
	}
	if len(unexpected) > 0 {
		fmt.Printf("chaos: ran and is not on the roster, so add it there: %v\n", unexpected)
		code = 1
	}
	os.Exit(code)
}

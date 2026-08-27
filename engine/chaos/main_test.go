package chaos_test

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// A tally, printed on every run, and a package that fails when a scenario
// skipped without being asked to.
//
// This exists because `go test` without -v does not print skip lines, so a
// package whose every test skipped reports `ok` with a plausible duration and
// nothing distinguishes it from one that ran. That is bad everywhere and worst
// here: this suite exists to prove that failure paths work, so a suite that
// quietly proves nothing is the exact shape it was written to catch. It also
// happened, twice, elsewhere in this repository on the same night, to two
// different people who had both already written warnings about it.
//
// The rule: AF_SKIP_DOCKER is the only acceptable reason to skip, and it has to
// be asked for. Anything else that produces a skip fails the package, with the
// count, so a green run is evidence rather than an inference.

var (
	tally   sync.Mutex
	started int
	skipped []string
)

// scenario registers a test with the tally. Called as the first line of each
// one, before any precondition, so a test that skips inside a helper is still
// counted as having been attempted.
func scenario(t *testing.T) {
	t.Helper()
	tally.Lock()
	started++
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
	fmt.Printf("chaos: %d scenarios attempted, %d skipped\n", started, len(skipped))

	if len(skipped) > 0 && os.Getenv("AF_SKIP_DOCKER") == "" {
		fmt.Printf("chaos: these skipped without AF_SKIP_DOCKER being set, so this run "+
			"proved less than it appears to have: %v\n", skipped)
		code = 1
	}
	if started == 0 {
		fmt.Println("chaos: no scenario ran at all, which is not a pass")
		code = 1
	}
	os.Exit(code)
}

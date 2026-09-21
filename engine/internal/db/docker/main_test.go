package docker_test

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
)

// asked and skipped count how many tests wanted the daemon and how many did
// not get it. TestMain reads them.
var asked, skipped atomic.Int64

// silentFullSkip reports whether a run proved nothing and said so quietly.
//
// A pure function so it can be tested, which a TestMain cannot be. The
// condition is narrow on purpose: some tests skipping is a machine being slow
// on one operation, and no tests asking is a package with nothing to skip.
// Every test asking and every one skipping is the case where ok is a lie.
func silentFullSkip(asked, skipped int64, optedOut bool) bool {
	return !optedOut && asked > 0 && skipped == asked
}

// TestMain refuses a run in which every test that needed the daemon skipped.
//
// This package holds the database conformance suite, and that suite is the
// only thing in the repository that can refuse a provider's copy on write
// declaration, its branching, or its goldens. When the daemon does not answer,
// every one of those behaviours skips, the package prints ok, and `go test`
// exits 0. Nothing anywhere then says that the reference implementation's
// proof did not run, and the next person reads the ok as the proof.
//
// It happened six times in one session on 2026-09-21. A measurement loop over
// this package recorded three consecutive runs of
// TestConformance/CopyOnWrite_BranchTimeMatchesTheDeclaration that took ten
// seconds each and exited 0, and a scaling experiment beside it lost three
// more the same way. They were only caught because somebody was reading the
// timestamps and noticed a four minute test finishing in eleven seconds. The
// exit code said nothing, and on a CI runner nobody is reading timestamps.
//
// The sibling package engine/internal/runtime/local reached this conclusion
// first and its TestMain says the same thing about containment. This is that
// guard, for the other property the product is sold on.
//
// Set AF_SKIP_DOCKER to say out loud that this machine has no daemon.
func TestMain(m *testing.M) {
	code := m.Run()

	a, s := asked.Load(), skipped.Load()
	if a > 0 {
		fmt.Fprintf(os.Stderr, "db/docker: %d of %d daemon tests ran\n", a-s, a)
	}
	if code == 0 && silentFullSkip(a, s, os.Getenv("AF_SKIP_DOCKER") != "") {
		fmt.Fprintf(os.Stderr,
			"db/docker: every one of the %d daemon tests skipped, so this package proved "+
				"nothing about the reference provider. Start Docker, or set AF_SKIP_DOCKER=1 "+
				"to accept that.\n", a)
		code = 1
	}
	os.Exit(code)
}

// The guard's own decision, checked directly. A TestMain cannot be tested, so
// the condition it acts on is a function that can be.
func TestSilentFullSkipOnlyFiresWhenNothingRan(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		asked, skipped int64
		optedOut       bool
		want           bool
	}{
		{"everything skipped", 7, 7, false, true},
		{"everything ran", 7, 0, false, false},
		{"some skipped", 7, 2, false, false},
		{"nothing asked", 0, 0, false, false},
		{"everything skipped, said out loud", 7, 7, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := silentFullSkip(c.asked, c.skipped, c.optedOut); got != c.want {
				t.Fatalf("silentFullSkip(%d, %d, %v) = %v, want %v",
					c.asked, c.skipped, c.optedOut, got, c.want)
			}
		})
	}
}

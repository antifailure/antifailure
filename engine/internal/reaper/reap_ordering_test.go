package reaper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/reaper"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// af ci stamps its environment with a SHORT ephemeral expiry, its run budget
// plus a grace, precisely so the reaper is a fast backstop for the one ordering
// the run cannot clean up itself. A run can end in four ways, and teardown has
// to reach the environment in each:
//
//	normal completion   the run's own Down removes it at once. The reaper never
//	                    sees it. Proven by env's up-then-down test, where Down
//	                    lands the env id in the runtime's teardown list.
//	cancel / interrupt  the deferred Down removes it on the way out, the same
//	                    path and the same proof; only a SIGKILL skips the defer.
//	crash mid-run       Down never runs. The short ephemeral expiry passes and
//	                    the reaper takes it. This is the ordering the short
//	                    lifetime exists for: a day-long default would leak for a
//	                    day, this leaks for an hour.
//	reaper catches leak identical to the crash: an expired, unheld environment
//	                    is destroyed on the next sweep.
//
// This test proves the reaper half of that table: a run env within its lifetime
// is NOT taken (a live run), an env past its ephemeral expiry IS taken (a
// crashed run), and an env something still holds is DEFERRED rather than pulled
// out from under the run.
func TestReaper_IsTheBackstopForARunThatCrashedBeforeTeardown(t *testing.T) {
	t.Parallel()
	const budgetPlusGrace = 90 * time.Minute
	expires := epoch.Add(budgetPlusGrace)
	env := []provider.Resource{res("web", "af-ci-1", expires)}

	// Liveness. While the run is still within the lifetime it was stamped with,
	// the environment is not expired. A reaper that took it here would be
	// destroying live work, which is the whole reason the predicate is a strict
	// "before now" and not "at or before".
	require.Empty(t, reaper.Plan(env, nil, expires.Add(-time.Minute)),
		"a run still within its ephemeral lifetime must not be reaped")

	// Crash backstop. Once the ephemeral expiry passes, the environment the
	// crashed run left up is expired, and the reaper names it.
	plan := reaper.Plan(env, nil, expires.Add(time.Minute))
	require.Len(t, plan, 1, "the leaked env is past its expiry and must be reaped")
	require.Equal(t, "af-ci-1", plan[0].EnvID)

	// And the sweep actually reaches it: teardown lands on the leaked env.
	rec := newRecorder()
	result := reaper.Sweep(context.Background(), env, nil, expires.Add(time.Minute), rec)
	require.Equal(t, []string{"af-ci-1"}, rec.seen, "the sweep destroyed the leaked env")
	require.Zero(t, result.Failed())

	// In-use deferral. A run that is still alive holds its environment, and the
	// destroyer says so. The sweep records the deferral and moves on; it never
	// force-removes an environment out from under a running command.
	held := newRecorder()
	held.err["af-ci-1"] = errors.New("something is running against this environment")
	result = reaper.Sweep(context.Background(), env, nil, expires.Add(time.Minute), held)
	require.Equal(t, []string{"af-ci-1"}, held.seen, "the sweep attempted the held env")
	require.Equal(t, 1, result.Failed(), "an in-use env is deferred, not force-removed")
}

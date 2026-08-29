package dockerutil_test

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
)

// TestAwaitExit_ReturnsTheExitCode is the ordinary path: the container exits
// before the deadline and its code comes back.
func TestAwaitExit_ReturnsTheExitCode(t *testing.T) {
	cli := requireDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	id := createRunning(t, cli, []string{"sh", "-c", "exit 7"})
	require.NoError(t, cli.ContainerStart(ctx, id, container.StartOptions{}))

	code, err := dockerutil.AwaitExit(ctx, cli, id)
	require.NoError(t, err)
	require.Equal(t, int64(7), code)
}

// TestAwaitExit_ADeadlineEndsTheWaitAndLeavesNothingParked is the regression
// test for the leak that took the goroutine check out of internal/insights.
//
// The shape that leaked: waiting on a container hands back an UNBUFFERED
// result channel and a goroutine that closes the response body only after it
// has handed its result over. Both call sites here also selected on
// ctx.Done(), so when the deadline landed at the same moment as the result --
// and select picks at random between two ready cases -- the goroutine was
// parked on the send for good, its body never closed, its connection never
// returned to the idle pool, and Client.Close could not reclaim it.
//
// AwaitExit has no ctx.Done() case for exactly that reason, so what is
// asserted is the contract that replaces it: the deadline still ends the wait
// promptly, because it cancels the request the goroutine is reading, and once
// the client is closed nothing is left running.
func TestAwaitExit_ADeadlineEndsTheWaitAndLeavesNothingParked(t *testing.T) {
	cli := requireDaemon(t)
	defer func() { _ = cli.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	before := goleak.IgnoreCurrent()

	id := createRunning(t, cli, []string{"sleep", "60"})
	require.NoError(t, cli.ContainerStart(ctx, id, container.StartOptions{}))

	short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	start := time.Now()
	_, err := dockerutil.AwaitExit(short, cli, id)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"the container outlives the deadline, so the deadline is what ends the wait")
	require.Less(t, time.Since(start), 30*time.Second,
		"the deadline has to end the wait, not be outlived by it")

	require.NoError(t, dockerutil.RemoveContainer(ctx, cli, id))
	_ = cli.Close()

	require.NoError(t, goleak.Find(before),
		"a goroutine outlived the client, so a wait was left parked and its "+
			"connection stranded")
}

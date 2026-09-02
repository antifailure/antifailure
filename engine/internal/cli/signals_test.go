//go:build unix

// syscall.Kill is not on Windows, and this test is about delivering a real
// SIGINT to this process rather than about simulating one, so there is
// nothing to run there rather than something to port.

package cli_test

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// blockingCommand is a command that ignores its context, which is the only
// command a second interrupt is for. A command that honours cancellation stops
// on the first one and never reaches this path.
func blockingCommand(started, release chan struct{}) cli.ExtraCommand {
	return cli.ExtraCommand{
		Use:   "blockforever",
		Short: "Blocks until the test releases it.",
		Run: func(_ context.Context, _ []string) int {
			close(started)
			<-release
			return 0
		},
	}
}

// TestASecondInterruptForcesTheExitOutFromUnderARunningCommand is the whole
// claim main makes about control C twice, end to end: a real signal, a real
// command that is not going to stop, and the exit code the user gets anyway.
func TestASecondInterruptForcesTheExitOutFromUnderARunningCommand(t *testing.T) {
	// Absorb interrupts for the length of the test so that one arriving
	// outside the window WithSignals is listening in cannot kill the test
	// binary with the default action. Go delivers a signal to every channel
	// registered for it, so this does not take anything away from the code
	// under test.
	guard := make(chan os.Signal, 8)
	signal.Notify(guard, os.Interrupt)
	defer signal.Stop(guard)

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	ctx, forced, stop := cli.WithSignals(context.Background())
	defer stop()

	var stdout, stderr bytes.Buffer
	codes := make(chan int, 1)
	go func() {
		codes <- cli.Run(ctx, forced, []string{"blockforever"}, cli.Options{
			Stdout:  &stdout,
			Stderr:  &stderr,
			WorkDir: t.TempDir(),
			Extra:   []cli.ExtraCommand{blockingCommand(started, release)},
		})
	}()

	<-started

	// The first interrupt cancels the root context. Waiting for that before
	// sending the second is what keeps the test honest: two kills in a row can
	// be delivered as one, and a test that raced them would pass for the wrong
	// reason.
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the first interrupt did not cancel the root context")
	}

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	select {
	case code := <-codes:
		require.Equal(t, int(aferrors.ExitInterruptedDirty), code,
			"a second interrupt has to exit 10, not wait for a command that is never coming back")
	case <-time.After(10 * time.Second):
		t.Fatal("the second interrupt did not force the exit; the command is still running and af is still waiting for it")
	}

	require.Contains(t, prose(stderr.String()), "journal",
		"a forced exit has to say what was left behind and how to remove it")
}

// TestStopEndsTheWatcher covers the other half of the same machinery: a
// command that finishes normally leaves nothing behind. goleak in TestMain is
// what fails if it does.
func TestStopEndsTheWatcher(t *testing.T) {
	ctx, forced, stop := cli.WithSignals(context.Background())
	require.NoError(t, ctx.Err())
	select {
	case <-forced:
		t.Fatal("nothing was forced and the channel is closed")
	default:
	}
	stop()
	stop()
	require.Error(t, ctx.Err(), "stop cancels the context it handed out")
}

package lock_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/lock"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".antifailure", "lock")
}

func TestAcquire_CreatesTheLockWithOwnerMetadata(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	l, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Release()) }()

	require.Equal(t, os.Getpid(), l.Owner().PID)
	require.Equal(t, "af up", l.Owner().Command)
	require.Equal(t, epoch, l.Owner().AcquiredAt)
	require.False(t, l.Reclaimed())
	require.Equal(t, path, l.Path())

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// The whole point: two af up invocations on one branch must not both proceed.
func TestAcquire_RefusesWhenALiveProcessHoldsIt(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	first, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Release()) }()

	_, err = lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFRUN003))
	// The message must name the process and when it started, so the user can
	// decide between waiting and killing it.
	require.Contains(t, err.Error(), "2026-01-01T00:00:00Z")
}

// A killed process leaves its lock file behind. Refusing forever would need a
// manual delete that no error message can make obvious.
func TestAcquire_ReclaimsALockHeldByADeadProcess(t *testing.T) {
	t.Parallel()
	path := lockPath(t)

	// A real process that has exited, so the identifier is genuinely dead
	// rather than merely unlikely to exist.
	cmd := exec.Command("sh", "-c", "exit 0")
	require.NoError(t, cmd.Run())
	deadPID := cmd.Process.Pid

	host, _ := os.Hostname()
	writeLock(t, path, lock.Owner{
		PID: deadPID, Host: host, Command: "af up",
		AcquiredAt: epoch.Add(-time.Hour),
	})

	l, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Release()) }()
	require.True(t, l.Reclaimed(), "the caller needs to know a stale lock was taken over")
	require.Equal(t, os.Getpid(), l.Owner().PID)
}

func TestAcquire_ReclaimsATruncatedLockFile(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	// A crash between create and write leaves an empty file.
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	l, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Release()) }()
	require.True(t, l.Reclaimed())
}

func TestAcquire_ReclaimsALockFileWithNoProcess(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"pid":0,"host":"x"}`), 0o600))

	l, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Release()) }()
	require.True(t, l.Reclaimed())
}

// A lock written on another machine cannot be liveness checked, so it is
// treated as live. Preferring a false "someone is working" over a false "the
// coast is clear" is the right way round.
func TestAcquire_TreatsALockFromAnotherHostAsLive(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	writeLock(t, path, lock.Owner{
		PID: 999999, Host: "some-other-machine", Command: "af up", AcquiredAt: epoch,
	})
	_, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFRUN003))
}

func TestRelease_IsIdempotentAndSurvivesAMissingFile(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	l, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)

	require.NoError(t, l.Release())
	require.NoFileExists(t, path)
	require.NoError(t, l.Release(), "releasing twice must be safe")
}

// The dangerous case: a slow process whose lock was reclaimed as stale must
// not delete the lock a newer process now holds. Otherwise a third process
// would take a lock the second one believes it owns.
func TestRelease_DoesNotRemoveALockANewerProcessHolds(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	stale, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)

	// Someone reclaimed it and took it over.
	require.NoError(t, os.Remove(path))
	newer, err := lock.Acquire(path, clock.NewFake(epoch.Add(time.Hour)), "af up")
	require.NoError(t, err)

	require.NoError(t, stale.Release())
	require.FileExists(t, path, "the newer holder's lock must survive")

	holder, ok, err := lock.Holder(path)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, epoch.Add(time.Hour), holder.AcquiredAt)
	require.NoError(t, newer.Release())
}

func TestRelease_LeavesAnUnreadableFileForTheNextAcquire(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	l, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("corrupted"), 0o600))
	require.NoError(t, l.Release(), "an unreadable lock is not ours to delete")
	require.FileExists(t, path)
}

func TestHolder_ReportsAbsenceWithoutAnError(t *testing.T) {
	t.Parallel()
	_, ok, err := lock.Holder(lockPath(t))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestHolder_ReportsAParseFailure(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	_, _, err := lock.Holder(path)
	require.Error(t, err)
}

func TestAcquire_ReportsAnUncreatableDirectory(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	_, err := lock.Acquire(filepath.Join(f, "sub", "lock"), clock.NewFake(epoch), "af up")
	require.Error(t, err)
}

func writeLock(t *testing.T, path string, o lock.Owner) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	b, err := json.Marshal(o)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

func TestAcquire_RefusalNamesTheHoldersCommand(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	held, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, held.Release()) }()

	_, err = lock.Acquire(path, clock.NewFake(epoch), "af mcp")
	require.True(t, lock.IsHeld(err))
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded))
	require.Equal(t, "af up", coded.Fields["command"],
		"a caller that cannot see a process table needs to know what is holding the branch")
	require.Equal(t, strconv.Itoa(os.Getpid()), coded.Fields["pid"])
}

func TestAcquireWithin_QueuesBehindAShortHolder(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	held, err := lock.Acquire(path, clock.NewFake(epoch), "af down")
	require.NoError(t, err)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = held.Release()
	}()

	start := time.Now()
	l, err := lock.AcquireWithin(context.Background(), path, clock.NewFake(epoch), "af mcp", 5*time.Second)
	require.NoError(t, err, "the holder released inside the wait")
	defer func() { require.NoError(t, l.Release()) }()
	require.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond,
		"it waited rather than took the lock away from a live holder")
}

func TestAcquireWithin_RefusesALongHolderNamingIt(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	held, err := lock.Acquire(path, clock.NewFake(epoch), "af explore")
	require.NoError(t, err)
	defer func() { require.NoError(t, held.Release()) }()

	start := time.Now()
	_, err = lock.AcquireWithin(context.Background(), path, clock.NewFake(epoch), "af mcp", 400*time.Millisecond)
	require.True(t, lock.IsHeld(err), "a live holder is never preempted: %v", err)
	require.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond)
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded))
	require.Equal(t, "af explore", coded.Fields["command"])
	require.Equal(t, os.Getpid(), held.Owner().PID, "the holder still holds it")
}

func TestAcquireWithin_ZeroWaitIsAcquire(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	held, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, held.Release()) }()

	start := time.Now()
	_, err = lock.AcquireWithin(context.Background(), path, clock.NewFake(epoch), "af golden list", 0)
	require.True(t, lock.IsHeld(err))
	require.Less(t, time.Since(start), 200*time.Millisecond, "a terminal caller is told at once")
}

func TestAcquireWithin_StopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	path := lockPath(t)
	held, err := lock.Acquire(path, clock.NewFake(epoch), "af up")
	require.NoError(t, err)
	defer func() { require.NoError(t, held.Release()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = lock.AcquireWithin(ctx, path, clock.NewFake(epoch), "af mcp", time.Minute)
	require.True(t, lock.IsHeld(err))
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestAlive_IsTheOneLivenessRule(t *testing.T) {
	t.Parallel()
	host, _ := os.Hostname()
	require.True(t, lock.Alive(lock.Owner{PID: os.Getpid(), Host: host}))
	cmd := exec.Command("sh", "-c", "exit 0")
	require.NoError(t, cmd.Run())
	require.False(t, lock.Alive(lock.Owner{PID: cmd.Process.Pid, Host: host}))
	require.True(t, lock.Alive(lock.Owner{PID: cmd.Process.Pid, Host: "another-host"}),
		"a process on another host cannot be checked, so it is treated as live")
}

package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// lockedFixture is an orchestrator whose branch lock another process holds,
// over a manifest whose database provider refuses before it reaches anything:
// neon with no project is AF-MAN-002 at construction, with no daemon in the
// way. A call that reaches the provider was not stopped by the lock, and a
// call that reports AF-RUN-003 was. The two codes are the instrument.
func lockedFixture(t *testing.T, wait time.Duration) (*Orchestrator, *lock.Lock) {
	t.Helper()
	root := t.TempDir()
	o, err := New(Options{
		Root:     root,
		Manifest: &schema.Manifest{Name: "app", Database: &schema.Database{Provider: schema.DBNeon}},
		Branch:   "main", Clock: clock.New(), Redactor: redact.New(),
		Progress: func(string) {}, LockWait: wait,
	})
	require.NoError(t, err)
	// This process stands in for the other one: the holder's pid is alive,
	// so the lock package will not reclaim it.
	held, err := lock.Acquire(filepath.Join(root, StateDir, o.envID+".lock"), clock.New(), "af up")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })
	return o, held
}

func isCode(err error, code aferrors.Code) bool {
	var coded *aferrors.Error
	return errors.As(err, &coded) && coded.Code() == code
}

func TestReads_TakeNoBranchLock(t *testing.T) {
	o, _ := lockedFixture(t, 0)
	ctx := context.Background()

	reads := map[string]func() error{
		"Goldens":       func() error { _, err := o.Goldens(ctx); return err },
		"Fidelity":      func() error { _, err := o.Fidelity(ctx); return err },
		"MaskVerify":    func() error { _, err := o.MaskVerify(ctx); return err },
		"RunInvariants": func() error { _, err := o.RunInvariants(ctx); return err },
	}
	o.opts.Manifest.Invariants = []schema.Invariant{{Name: "one", SQL: "select 1"}}
	for name, read := range reads {
		err := read()
		require.False(t, lock.IsHeld(err), "%s was refused by the branch lock: %v", name, err)
		require.True(t, isCode(err, aferrors.AFMAN002),
			"%s should have reached the provider and stopped there, got %v", name, err)
	}

	// The writes still take it. A read that took none is only safe because
	// it cannot write; the moment something can, it queues.
	err := o.DestroyGolden(ctx, "v1")
	require.True(t, lock.IsHeld(err), "DestroyGolden must hold the branch: %v", err)
	_, err = o.MaskApply(ctx)
	require.True(t, lock.IsHeld(err), "MaskApply must hold the branch: %v", err)
}

func TestOpen_WaitsForAShortHolderAndRefusesALongOne(t *testing.T) {
	o, held := lockedFixture(t, 2*time.Second)
	ctx := context.Background()

	// Long: the holder does not let go inside the wait.
	quick, err := New(Options{
		Root: o.opts.Root, Manifest: o.opts.Manifest, Branch: "main",
		Clock: clock.New(), Redactor: redact.New(), Progress: func(string) {},
		LockWait: 300 * time.Millisecond,
	})
	require.NoError(t, err)
	start := time.Now()
	err = quick.DestroyGolden(ctx, "v1")
	require.True(t, lock.IsHeld(err), "a live holder is never preempted: %v", err)
	require.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	holder, found, readErr := lock.Holder(held.Path())
	require.NoError(t, readErr)
	require.True(t, found)
	require.Equal(t, os.Getpid(), holder.PID, "the holder still holds it")

	// Short: the holder lets go inside the wait, and the call proceeds to
	// the provider, which is where this manifest stops it.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = held.Release()
	}()
	err = o.DestroyGolden(ctx, "v1")
	require.False(t, lock.IsHeld(err), "the caller should have queued behind the holder: %v", err)
	require.True(t, isCode(err, aferrors.AFMAN002), "got %v", err)
}

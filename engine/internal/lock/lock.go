// Package lock provides the exclusive advisory lock that keeps two engine
// processes from operating on one environment at the same time.
//
// The failure it prevents is concrete: two af up invocations on the same
// branch, each journalling its own intents, each creating containers with
// deterministic names that collide, and each tearing down the other's work.
// One lock per branch turns that into a clear message and an exit code.
//
// The lock is a file holding the owner's process identifier, host, command,
// and start time. That metadata is what makes a stale lock recoverable: a
// process that was killed leaves its file behind, and the next invocation has
// to tell "someone else is working" from "a corpse is holding the door". It
// does that by checking whether the recorded process is still alive, which is
// both cheaper and far more reliable than a timeout.
package lock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/privatefs"
)

// Owner describes who holds a lock.
type Owner struct {
	PID        int       `json:"pid"`
	Host       string    `json:"host"`
	Command    string    `json:"command"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// Lock is a held advisory lock.
type Lock struct {
	path      string
	owner     Owner
	reclaimed bool
}

// Acquire takes the lock at path, creating the parent directory if needed.
//
// If the lock is held by a live process it returns AF-RUN-003 naming that
// process and when it started. If it is held by a process that no longer
// exists, the stale file is reclaimed and Reclaimed reports it so that the
// caller can emit an event rather than reclaim silently.
func Acquire(path string, c clock.Clock, command string) (*Lock, error) {
	if err := privatefs.MkdirAll(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("lock: create %s: %w", filepath.Dir(path), err)
	}
	host, _ := os.Hostname()
	me := Owner{PID: os.Getpid(), Host: host, Command: command, AcquiredAt: c.Now().UTC()}

	reclaimed := false
	reclaims, vanished := 0, 0
	for {
		// Publishing a complete file under the lock's name is the atomic part.
		// Two processes racing here, on the same machine or on a shared
		// filesystem that honours it, cannot both win.
		err := publish(path, me)
		if err == nil {
			return &Lock{path: path, owner: me, reclaimed: reclaimed}, nil
		}
		if !os.IsExist(err) && !transient(err) {
			return nil, fmt.Errorf("lock: acquire %s: %w", path, err)
		}

		holder, readErr := read(path)
		if transient(err) || os.IsNotExist(readErr) || transient(readErr) {
			// The holder let go between this attempt and this look. That is
			// contention resolving rather than a crash, so nothing is reclaimed
			// and nothing has failed: try again. It used to fall into the
			// unreadable branch below, which counted it as a reclaim, and once
			// the attempts ran out the final look found no file and returned
			// "no such file or directory" to a caller that should have queued.
			// On Windows the same moment can also read as a refusal, while the
			// holder's remove is still in progress; see transient.
			vanished++
			if vanished <= maxVanished {
				time.Sleep(time.Millisecond)
				continue
			}
			return nil, fmt.Errorf("lock: acquire %s: the lock changed hands %d times while this process tried for it, the last look failing with: %w",
				path, maxVanished, errors.Join(err, readErr))
		}
		if readErr == nil && Alive(holder) {
			return nil, heldBy(holder)
		}
		if reclaims == 2 {
			// Two reclaim attempts both lost the race, which means another
			// process is actively taking the lock. Reporting contention is the
			// correct answer.
			if readErr != nil {
				return nil, fmt.Errorf("lock: acquire %s: %w", path, readErr)
			}
			return nil, heldBy(holder)
		}
		if readErr != nil {
			// A truncated or unreadable lock file can only come from a crash,
			// because publish never lets the name exist before its contents do.
			// Refusing forever because of one would need a manual delete that
			// no error message can make obvious, so it is treated as stale.
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) && !transient(rmErr) {
				return nil, fmt.Errorf("lock: reclaim an unreadable lock at %s: %w", path, rmErr)
			}
		} else if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) && !transient(rmErr) {
			return nil, fmt.Errorf("lock: reclaim the stale lock at %s: %w", path, rmErr)
		}
		reclaimed = true
		reclaims++
	}
}

// maxVanished bounds how many times Acquire retries a lock that was released
// between its attempt and its look. Each retry means another process made
// progress, so the bound is only there to stop a pathological churn spinning.
const maxVanished = 100

// heldBy is the refusal for a lock a live process holds.
//
// The command is carried as a field even though the catalog sentence does not
// print it, so that a caller rendering the refusal somewhere other than a
// terminal, the MCP server in particular, can name what is holding the branch
// rather than only which process number is.
func heldBy(holder Owner) error {
	return aferrors.Coded(aferrors.AFRUN003,
		"pid", fmt.Sprint(holder.PID),
		"command", holder.Command,
		"since", holder.AcquiredAt.Format(time.RFC3339))
}

// AcquireWithin is Acquire with a bounded wait for a live holder to finish.
//
// A second MCP server asking a short question while the first is between
// steps of a short operation should queue behind it rather than refuse, so
// this retries a refusal caused by a live holder until wait has elapsed or
// ctx ends. The last refusal is returned unchanged, so the caller still learns
// who holds the lock. A wait of zero or less is exactly Acquire, which is
// what every command line caller wants: a person at a terminal is told at
// once and decides for themselves.
func AcquireWithin(
	ctx context.Context, path string, c clock.Clock, command string, wait time.Duration,
) (*Lock, error) {
	// A wait of zero puts the deadline in the past, which is what makes it
	// exactly Acquire: the first refusal is returned without a pause.
	deadline := time.Now().Add(wait)
	for {
		l, err := Acquire(path, c, command)
		if err == nil || !IsHeld(err) {
			return l, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, err
		}
		pause := acquirePoll
		if pause > remaining {
			pause = remaining
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(pause):
		}
	}
}

// acquirePoll is how often AcquireWithin retries.
const acquirePoll = 250 * time.Millisecond

// IsHeld reports whether err is the refusal for a lock a live process holds.
func IsHeld(err error) bool {
	return errors.Is(err, aferrors.Coded(aferrors.AFRUN003))
}

// publish makes path name a file that already holds me, or fails with an error
// os.IsExist recognises when path is taken.
//
// The owner is written to a private file of its own first and only then linked
// to the lock's name, because a lock that is visible before it is written is
// two defects. A reader that arrives between the create and the write finds an
// empty file, which Acquire reclaims as a crash: on Unix that removed the lock
// a live process was still writing and handed the same lock to a second one,
// and on Windows the remove was refused, because the writer's handle does not
// share delete, and the waiter failed with an error instead of waiting. The
// replay store's test that retires and imports at once reached the second form
// on the required Windows check. A hard link is created exclusively on every
// platform, so it is the same O_EXCL race as before with the contents already
// in place.
//
// A filesystem that cannot link is the one case that falls back to creating
// the name directly, which is the old behaviour and its old window.
func publish(path string, me Owner) error {
	staging := fmt.Sprintf("%s.%d.%d.new", path, me.PID, stagingSeq.Add(1))
	// The name is this process's own, so anything already there was left by
	// an earlier process that had the same identifier and crashed mid stage.
	_ = os.Remove(staging)
	f, err := privatefs.CreateExclusive(staging)
	if err != nil {
		// Held in a stageError, which does not unwrap: a staging file that
		// cannot be made says nothing about the lock, and unwrapped it would
		// read as "the lock exists" or as a remove in progress to the caller
		// deciding whether to wait.
		return fmt.Errorf("lock: stage %s: %w", staging, stageError{err})
	}
	if err := writeOwner(f, staging, me); err != nil {
		return err
	}
	defer func() { _ = os.Remove(staging) }()
	linkErr := os.Link(staging, path)
	if linkErr == nil || os.IsExist(linkErr) {
		return linkErr
	}
	direct, err := privatefs.CreateExclusive(path)
	if err != nil {
		return err
	}
	return writeOwner(direct, path, me)
}

// stageError carries a failure to make the staging file without letting
// errors.Is or os.IsExist see through it.
type stageError struct{ err error }

func (e stageError) Error() string { return e.err.Error() }

// stagingSeq keeps two goroutines of one process from staging under one name.
var stagingSeq atomic.Uint64

func writeOwner(f *os.File, path string, me Owner) error {
	body, err := json.Marshal(me)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("lock: encode the owner: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("lock: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("lock: close %s: %w", path, err)
	}
	return nil
}

// Owner returns the metadata written into the lock file.
func (l *Lock) Owner() Owner { return l.owner }

// Path returns the lock file path.
func (l *Lock) Path() string { return l.path }

// Reclaimed reports whether this lock replaced a stale one left by a process
// that is no longer running. The caller emits a warning event when it is true,
// because a reclaimed lock usually means a previous run was killed and its
// resources may need a journal replay.
func (l *Lock) Reclaimed() bool { return l.reclaimed }

// Release removes the lock.
//
// It only removes a file that still records this process as the owner, so a
// slow process whose lock was reclaimed as stale cannot delete the lock a
// newer process now holds.
func (l *Lock) Release() error {
	for try := 0; ; try++ {
		retry := try < maxVanished
		holder, err := read(l.path)
		if err != nil {
			if transient(err) && retry {
				// Another process is part way through removing or reclaiming
				// it; look again rather than leave a file that may be ours.
				time.Sleep(time.Millisecond)
				continue
			}
			// Gone, or unreadable, and an unreadable file is not ours to
			// remove either: the next Acquire reclaims it.
			return nil
		}
		if holder.PID != l.owner.PID || !holder.AcquiredAt.Equal(l.owner.AcquiredAt) {
			// Someone else owns it now. Deleting would hand a third process a
			// lock the second one believes it holds.
			return nil
		}
		if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
			if transient(err) && retry {
				time.Sleep(time.Millisecond)
				continue
			}
			return fmt.Errorf("lock: release %s: %w", l.path, err)
		}
		return nil
	}
}

// Holder reports who holds the lock at path, if anyone.
func Holder(path string) (Owner, bool, error) {
	o, err := read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Owner{}, false, nil
		}
		return Owner{}, false, err
	}
	return o, true, nil
}

func read(path string) (Owner, error) {
	b, err := readFile(path)
	if err != nil {
		return Owner{}, err
	}
	var o Owner
	if err := json.Unmarshal(b, &o); err != nil {
		return Owner{}, fmt.Errorf("lock: parse %s: %w", path, err)
	}
	if o.PID <= 0 {
		return Owner{}, fmt.Errorf("lock: %s records no process", path)
	}
	return o, nil
}

// Alive reports whether the recorded process is still running.
//
// A lock from another host cannot be checked this way, so it is treated as
// live. Preferring a false "someone is working" over a false "the coast is
// clear" is the right way round: the first costs a wait, the second costs two
// processes fighting over one environment.
//
// Exported because it is the one liveness rule this engine has, and the MCP
// run store needs the same answer to the same question: a run recorded as in
// flight is interrupted only if the process that owns it is gone. A second
// rule written there would be a second answer to whether a process exists.
func Alive(o Owner) bool {
	host, _ := os.Hostname()
	if o.Host != "" && host != "" && o.Host != host {
		return true
	}
	return processExists(o.PID)
}

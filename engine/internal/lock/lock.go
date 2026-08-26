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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lock: create %s: %w", filepath.Dir(path), err)
	}
	host, _ := os.Hostname()
	me := Owner{PID: os.Getpid(), Host: host, Command: command, AcquiredAt: c.Now().UTC()}

	reclaimed := false
	for attempt := 0; attempt < 2; attempt++ {
		// O_EXCL is the atomic part. Two processes racing here, on the same
		// machine or on a shared filesystem that honours it, cannot both win.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if wErr := writeOwner(f, path, me); wErr != nil {
				return nil, wErr
			}
			return &Lock{path: path, owner: me, reclaimed: reclaimed}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("lock: acquire %s: %w", path, err)
		}

		holder, readErr := read(path)
		if readErr != nil {
			// A truncated or unreadable lock file can only come from a crash
			// between create and write. Refusing forever because of one would
			// need a manual delete that no error message can make obvious, so
			// it is treated as stale.
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				return nil, fmt.Errorf("lock: reclaim an unreadable lock at %s: %w", path, rmErr)
			}
			reclaimed = true
			continue
		}
		if alive(holder) {
			return nil, aferrors.Coded(aferrors.AFRUN003,
				"pid", fmt.Sprint(holder.PID),
				"since", holder.AcquiredAt.Format(time.RFC3339))
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return nil, fmt.Errorf("lock: reclaim the stale lock at %s: %w", path, rmErr)
		}
		reclaimed = true
	}

	// Two reclaim attempts both lost the race, which means another process is
	// actively taking the lock. Reporting contention is the correct answer.
	holder, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("lock: acquire %s: %w", path, err)
	}
	return nil, aferrors.Coded(aferrors.AFRUN003,
		"pid", fmt.Sprint(holder.PID),
		"since", holder.AcquiredAt.Format(time.RFC3339))
}

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
	holder, err := read(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// An unreadable file is not ours to remove either; the next Acquire
		// reclaims it.
		return nil
	}
	if holder.PID != l.owner.PID || !holder.AcquiredAt.Equal(l.owner.AcquiredAt) {
		// Someone else owns it now. Deleting would hand a third process a lock
		// the second one believes it holds.
		return nil
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lock: release %s: %w", l.path, err)
	}
	return nil
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
	b, err := os.ReadFile(path) //nolint:gosec // the path is inside our own state directory
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

// alive reports whether the recorded process is still running.
//
// A lock from another host cannot be checked this way, so it is treated as
// live. Preferring a false "someone is working" over a false "the coast is
// clear" is the right way round: the first costs a wait, the second costs two
// processes fighting over one environment.
func alive(o Owner) bool {
	host, _ := os.Hostname()
	if o.Host != "" && host != "" && o.Host != host {
		return true
	}
	return processExists(o.PID)
}

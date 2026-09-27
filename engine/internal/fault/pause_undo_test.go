package fault_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/fault"
)

// On 2026-09-22 TestInjectInto_RefusesAContainerAntifailureDidNotCreate went
// red in CI with "the undo ran and the container is still frozen", on a pull
// request that did not touch this package. Undo had returned nil. The pause
// undo asked whether the container was paused before thawing it and returned
// success on a no, and the helper it asked answers no for an inspect that
// fails as well as for a thawed container. These drive that undo against a
// daemon that misbehaves in exactly the ways that path could not tell apart.

// freezer is a daemon holding one container of ours that it can pause.
type freezer struct {
	fault.Docker
	paused bool
	// failInspect makes every inspect fail once the fault is in place, which
	// is the stumble that used to read as "not paused".
	failInspect bool
	// stuck makes the thaw report success and change nothing.
	stuck bool
	// lagsBy is how many inspects after an accepted thaw still report the
	// container frozen, which is what a loaded daemon does: the accept and the
	// recomputed state are different instants.
	lagsBy   int
	inspects int
	// gone makes the container disappear, as a target that died does.
	gone     bool
	unpauses int
	thawAt   int
}

const freezerEnv = "env-freezer"

// frozenNow is what an inspect reports. Once a lagging thaw has been accepted,
// thawAt is the inspect at which the daemon's own state finally agrees.
func (f *freezer) frozenNow() bool {
	if f.thawAt > 0 && f.inspects >= f.thawAt {
		f.paused = false
	}
	return f.paused
}

func (f *freezer) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	f.inspects++
	if f.gone {
		return client.ContainerInspectResult{}, fmt.Errorf("%w: no such container", cerrdefs.ErrNotFound)
	}
	if f.failInspect {
		return client.ContainerInspectResult{}, errors.New("Error response from daemon: context deadline exceeded")
	}
	return client.ContainerInspectResult{Container: container.InspectResponse{
		ID: "c1", Name: "/af-db-freezer",
		Config: &container.Config{Labels: map[string]string{
			dockerutil.LabelManaged: dockerutil.ManagedValue,
			dockerutil.LabelEnv:     freezerEnv,
			dockerutil.LabelKind:    "branch",
		}},
		State: &container.State{Running: true, Paused: f.frozenNow(), Status: "running"},
	}}, nil
}

func (f *freezer) ContainerPause(context.Context, string, client.ContainerPauseOptions) (client.ContainerPauseResult, error) {
	f.paused = true
	return client.ContainerPauseResult{}, nil
}

func (f *freezer) ContainerUnpause(context.Context, string, client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error) {
	f.unpauses++
	if f.gone {
		return client.ContainerUnpauseResult{}, fmt.Errorf("%w: no such container", cerrdefs.ErrNotFound)
	}
	if !f.paused {
		return client.ContainerUnpauseResult{}, fmt.Errorf("%w: container c1 is not paused", cerrdefs.ErrConflict)
	}
	if !f.stuck {
		// The thaw is accepted here and the state catches up lagsBy inspects
		// later, which is the ordering the undo has to tolerate.
		if f.lagsBy > 0 {
			f.thawAt = f.inspects + f.lagsBy
		} else {
			f.paused = false
		}
	}
	return client.ContainerUnpauseResult{}, nil
}

func frozen(t *testing.T, f *freezer) *fault.Injection {
	t.Helper()
	inj, err := fault.New(f, freezerEnv)
	require.NoError(t, err)
	in, err := inj.InjectInto(t.Context(), "c1", fault.Fault{
		Name: "freeze", Kind: fault.KindContainerPause, Target: fault.Target{Role: fault.RoleDatabase},
	})
	require.NoError(t, err)
	require.True(t, f.paused)
	return in
}

func TestPauseUndo_ThawsEvenWhenTheDaemonCannotSayItIsFrozen(t *testing.T) {
	fault.ThawConfirmWindowForTest(t, 100*time.Millisecond)
	f := &freezer{}
	in := frozen(t, f)
	f.failInspect = true
	err := in.Undo(t.Context())
	require.Equal(t, 1, f.unpauses, "the undo never asked for the thaw because an inspect failed")
	require.False(t, f.paused, "the container was left frozen")
	require.Error(t, err, "a thaw nobody could confirm was reported as done")
	require.Contains(t, err.Error(), "could not be confirmed")
}

func TestPauseUndo_ReportsAThawThatChangedNothing(t *testing.T) {
	fault.ThawConfirmWindowForTest(t, 100*time.Millisecond)
	f := &freezer{stuck: true}
	in := frozen(t, f)
	err := in.Undo(t.Context())
	require.Error(t, err, "the daemon accepted the thaw, the container is still frozen, and the undo said nothing")
	require.Contains(t, err.Error(), "still frozen")
}

func TestPauseUndo_OfAThawedOrVanishedContainerIsNotAnError(t *testing.T) {
	t.Run("thawed by something else first", func(t *testing.T) {
		f := &freezer{}
		in := frozen(t, f)
		f.paused = false
		require.NoError(t, in.Undo(t.Context()),
			"a container that is already thawed is the state the undo wants")
	})
	t.Run("the target died", func(t *testing.T) {
		f := &freezer{}
		in := frozen(t, f)
		f.gone = true
		require.NoError(t, in.Undo(t.Context()),
			"Undo is documented as not an error on a target that has since died")
	})
	t.Run("the ordinary thaw", func(t *testing.T) {
		f := &freezer{}
		in := frozen(t, f)
		require.NoError(t, in.Undo(t.Context()))
		require.False(t, f.paused)
	})
}

// On 2026-09-26 the `edition boundary` gate went red on a pull request whose
// whole diff was a container registry change. The underlying failure was
// TestInjectInto_RefusesAnotherEnvironmentsContainer reporting "is still frozen
// after the daemon accepted the thaw", and tools/editioncheck then ran the
// package three times, FAIL without ee, PASS with ee, PASS again without it,
// and printed COULD-NOT-LOOK rather than naming a violation.
//
// The cause was not the freezer and not the edition boundary. ContainerUnpause
// returning means the daemon ACCEPTED the thaw, and the undo read the state back
// ONCE, immediately, with no wait at all. Under load the accept and the
// recomputed state are different instants, so the read landed on the state the
// daemon still had.
//
// This drives that exact ordering. The window is deliberately left at its
// production value: a window shortened below the lag would make this pass
// because there was nothing to absorb.
func TestPauseUndo_WaitsForADaemonThatHasNotCaughtUpWithItsOwnThaw(t *testing.T) {
	f := &freezer{lagsBy: 3}
	in := frozen(t, f)
	require.NoError(t, in.Undo(t.Context()),
		"the daemon accepted the thaw and had not applied it yet, which is not a frozen container")
	require.False(t, f.paused, "the container was left frozen")
	require.Equal(t, 1, f.unpauses,
		"the thaw was asked for more than once; the retry belongs on the READ BACK, not on the request")
}

// The window expiring still says no, and says which of the two things happened.
// A wait that cannot expire would have replaced a false red with a hang, and a
// wait that expired quietly would have replaced it with the 2026-09-22 defect.
func TestPauseUndo_TheWaitExpiresAndNamesWhatItSaw(t *testing.T) {
	t.Run("still frozen names the window it waited", func(t *testing.T) {
		fault.ThawConfirmWindowForTest(t, 120*time.Millisecond)
		f := &freezer{stuck: true}
		in := frozen(t, f)
		err := in.Undo(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "still frozen")
		require.Contains(t, err.Error(), "120ms",
			"the message does not say how long it waited, so a reader cannot tell a stuck container from a tight window")
	})
	t.Run("an inspect that never answers is not a thaw", func(t *testing.T) {
		fault.ThawConfirmWindowForTest(t, 120*time.Millisecond)
		f := &freezer{}
		in := frozen(t, f)
		f.failInspect = true
		err := in.Undo(t.Context())
		require.Error(t, err, "a thaw nobody could confirm was reported as done")
		require.Contains(t, err.Error(), "could not be confirmed")
		require.Contains(t, err.Error(), "attempts",
			"the message does not say it tried more than once")
	})
}

// A cancelled context must end the wait rather than spend the whole window on a
// caller that has already given up.
func TestPauseUndo_ACancelledContextEndsTheWait(t *testing.T) {
	fault.ThawConfirmWindowForTest(t, time.Hour)
	f := &freezer{stuck: true}
	in := frozen(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	require.Error(t, in.Undo(ctx))
	require.Less(t, time.Since(started), 5*time.Second,
		"the undo spent the window on a context that was already cancelled")
}

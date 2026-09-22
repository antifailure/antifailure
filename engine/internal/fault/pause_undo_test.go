package fault_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
	// gone makes the container disappear, as a target that died does.
	gone     bool
	unpauses int
}

const freezerEnv = "env-freezer"

func (f *freezer) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
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
		State: &container.State{Running: true, Paused: f.paused, Status: "running"},
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
		f.paused = false
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

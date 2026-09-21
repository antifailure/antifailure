package fault_test

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/fault"
)

func TestValidate_RefusesAParameterThatBelongsToAnotherKind(t *testing.T) {
	db := fault.Target{Role: fault.RoleDatabase}
	cases := map[string]struct {
		in   fault.Fault
		want string
	}{
		"a kind that does not exist": {
			in:   fault.Fault{Name: "x", Kind: "reboot_the_planet", Target: db},
			want: "AF-CHS-002",
		},
		"a process kill with no process": {
			in:   fault.Fault{Name: "x", Kind: fault.KindProcessKill, Target: db},
			want: "killing whichever one was listed first",
		},
		"a process named on a kind that kills none": {
			in:   fault.Fault{Name: "x", Kind: fault.KindContainerPause, Target: db, Process: "postgres"},
			want: "a kind that kills no process",
		},
		"a service target with no service": {
			in:   fault.Fault{Name: "x", Kind: fault.KindContainerPause, Target: fault.Target{Role: fault.RoleService}},
			want: "names no service",
		},
		"a service name on a database target": {
			in:   fault.Fault{Name: "x", Kind: fault.KindContainerPause, Target: fault.Target{Role: fault.RoleDatabase, Service: "api"}},
			want: "for a target that is not a service",
		},
		"a role that does not exist": {
			in:   fault.Fault{Name: "x", Kind: fault.KindContainerPause, Target: fault.Target{Role: "the-whole-cluster"}},
			want: "AF-CHS-001",
		},
		"a disk fill with no headroom": {
			in:   fault.Fault{Name: "x", Kind: fault.KindDiskFill, Target: db, MaxFillBytes: 1 << 20},
			want: "no room to write the file that empties it",
		},
		"a disk fill with no cap": {
			in:   fault.Fault{Name: "x", Kind: fault.KindDiskFill, Target: db, HeadroomBytes: 1 << 20},
			want: "bounded only by the machine",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := fault.Validate(tc.in)
			require.Error(t, err, "the fault was accepted")
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidate_AcceptsEveryKindWrittenCorrectly(t *testing.T) {
	// The liveness arm for the table above. Without it, a Validate that
	// refused everything would pass every case there.
	db := fault.Target{Role: fault.RoleDatabase}
	good := map[fault.Kind]fault.Fault{
		fault.KindProcessKill:      {Name: "a", Kind: fault.KindProcessKill, Target: db, Process: "postgres: checkpointer"},
		fault.KindContainerKill:    {Name: "b", Kind: fault.KindContainerKill, Target: db},
		fault.KindContainerStop:    {Name: "c", Kind: fault.KindContainerStop, Target: db},
		fault.KindContainerPause:   {Name: "d", Kind: fault.KindContainerPause, Target: db},
		fault.KindNetworkPartition: {Name: "e", Kind: fault.KindNetworkPartition, Target: fault.Target{Role: fault.RoleService, Service: "api"}},
		fault.KindReadOnlyData:     {Name: "f", Kind: fault.KindReadOnlyData, Target: db, Path: "/data"},
		fault.KindDiskFill:         {Name: "g", Kind: fault.KindDiskFill, Target: db, Path: "/data", HeadroomBytes: 1 << 20, MaxFillBytes: 1 << 30},
	}
	for _, k := range fault.Kinds() {
		f, ok := good[k]
		require.Truef(t, ok, "the kind %q is declared and this test has no valid example of it, "+
			"so nothing here proves it can be written correctly", k)
		require.NoErrorf(t, fault.Validate(f), "a correctly written %s was refused", k)
	}
}

func TestExpectsCrash_IsTrueOnlyForTheKindsThatStopThePostmasterUncleanly(t *testing.T) {
	// The list decides whether a recovery is expected, and a kind added to it
	// wrongly turns "this fault crashes nothing" into a silent pass. A clean
	// stop is the one that catches it: it takes the database away and brings
	// it back with no replay at all.
	want := map[fault.Kind]bool{
		fault.KindProcessKill:      true,
		fault.KindContainerKill:    true,
		fault.KindContainerStop:    false,
		fault.KindContainerPause:   false,
		fault.KindNetworkPartition: false,
		fault.KindReadOnlyData:     false,
		fault.KindDiskFill:         false,
	}
	for _, k := range fault.Kinds() {
		expected, ok := want[k]
		require.Truef(t, ok, "the kind %q is declared and this test says nothing about whether it crashes", k)
		require.Equalf(t, expected, k.ExpectsCrash(), "%s", k)
	}
}

func TestNew_RefusesAnInjectorWithNothingToScopeItTo(t *testing.T) {
	// An injector with no environment would resolve every container on the
	// machine, which is the one thing this package exists to prevent.
	_, err := fault.New(nil, "env")
	require.Error(t, err)
	_, err = fault.New(stubDocker{}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing to scope a fault to")
	_, err = fault.New(stubDocker{}, "   ")
	require.Error(t, err, "an environment id of spaces was accepted")
}

func TestTarget_NamesItselfTheWayAFindingDoes(t *testing.T) {
	require.Equal(t, "database", fault.Target{Role: fault.RoleDatabase}.String())
	require.Equal(t, "service api", fault.Target{Role: fault.RoleService, Service: "api"}.String())
}

func TestUndo_IsIdempotentAndRunsOnce(t *testing.T) {
	// The caller's deferred undo runs after the one the run already made, so
	// a second call must do nothing rather than report a problem it created.
	var calls int
	in := fault.InjectionForTest(func() error { calls++; return nil })
	require.False(t, in.Undone())
	require.NoError(t, in.Undo(t.Context()))
	require.True(t, in.Undone())
	require.NoError(t, in.Undo(t.Context()))
	require.Equal(t, 1, calls, "the undo ran twice")

	// And a nil injection, which is what a refused fault leaves behind.
	var none *fault.Injection
	require.NoError(t, none.Undo(t.Context()))
}

// stubDocker satisfies the daemon interface and answers nothing. It exists so
// that the constructor's refusals can be driven without a daemon, and every
// method on it panics rather than returning a zero value: a test that reached
// one of them would be testing a fake instead of the product.
type stubDocker struct{ fault.Docker }

// fakeDaemon returns a fixed listing whatever it is asked for.
//
// It stands in for the one case the real daemon cannot produce: a listing that
// carries a container the label filter should have excluded. That is what the
// predicate in Containers is for, and with a real daemon the predicate is
// unreachable, because dockerutil.Filter already adds the managed label as a
// presence test. A guard nothing can reach is a guard nobody can show works,
// so this is the door it is reached through.
type fakeDaemon struct {
	fault.Docker
	items []container.Summary
}

func (f fakeDaemon) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: f.items}, nil
}

func TestContainers_DropsAContainerTheDaemonShouldNotHaveReturned(t *testing.T) {
	const mine = "env-mine"
	inj, err := fault.New(fakeDaemon{items: []container.Summary{
		{ID: "a", Names: []string{"/ours"}, Labels: map[string]string{
			dockerutil.LabelManaged: dockerutil.ManagedValue,
			dockerutil.LabelEnv:     mine,
			dockerutil.LabelKind:    "branch",
		}},
		{ID: "b", Names: []string{"/another-environment"}, Labels: map[string]string{
			dockerutil.LabelManaged: dockerutil.ManagedValue,
			dockerutil.LabelEnv:     "env-theirs",
			dockerutil.LabelKind:    "branch",
		}},
		{ID: "c", Names: []string{"/not-ours-at-all"}, Labels: map[string]string{
			dockerutil.LabelEnv: mine,
		}},
	}}, mine)
	require.NoError(t, err)

	got, err := inj.Containers(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 1,
		"the listing kept a container this environment does not own; the daemon filter is not the only thing that decides")
	require.Equal(t, "a", got[0].ID)
}

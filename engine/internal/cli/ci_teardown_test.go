package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// af ci is the one command that tears down without anybody watching a terminal,
// and it was the one that said nothing when teardown failed.
//
// The deferred teardown read `if td, downErr := o.Down(c); downErr == nil`, so a
// teardown that could not reach the daemon printed nothing at all, and one that
// left containers behind printed only how many it removed. Both answered exit 0
// on a green pull request with an environment still running on the runner. That
// is the leak this product exists to prevent, reported as a success, in the
// place where a human is least likely to notice.
//
// af down has said both of these things since it was written. These cases are
// the same sentences, from the command that actually runs in continuous
// integration.

func teardownOutput(t *testing.T, td *env.Teardown, err error) string {
	t.Helper()
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	reportTeardown(e, td, err)
	return buf.String()
}

func TestTeardownThatRemovedEverythingReadsAsItAlwaysDid(t *testing.T) {
	got := teardownOutput(t, &env.Teardown{EnvID: "af-1", Removed: 4}, nil)
	require.Contains(t, got, "torn down, 4 resources removed")
}

func TestTeardownThatLeftSomethingBehindNamesEachThing(t *testing.T) {
	// The daemon refused two removals. Before this, the only line printed was
	// "torn down, 3 resources removed", which is true and is the half that does
	// not matter.
	got := teardownOutput(t, &env.Teardown{
		EnvID:   "af-1",
		Removed: 3,
		Pending: []provider.PendingResource{
			{Kind: "container", ID: "a1b2c3d4", Reason: "device or resource busy"},
			{Kind: "network", ID: "af-1-inner", Reason: "has active endpoints"},
		},
	}, nil)

	require.Contains(t, got, "torn down, 3 resources removed")
	require.Contains(t, got, "2 resources are still there")
	require.Contains(t, got, "container/a1b2c3d4: device or resource busy")
	require.Contains(t, got, "network/af-1-inner: has active endpoints")
}

func TestTeardownThatCouldNotRunAtAllSaysSo(t *testing.T) {
	// The worst case and the quietest one: Down returned an error, so td is nil
	// and nothing was attempted. The old code printed nothing whatsoever, which
	// reads exactly like a run configured with --keep.
	got := teardownOutput(t, nil, errors.New("cannot reach the Docker daemon at unix:///var/run/docker.sock"))

	require.Contains(t, got, "teardown did not run")
	require.Contains(t, got, "cannot reach the Docker daemon")
	require.Contains(t, got, "af down")
}

package reaper_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/reaper"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// Nothing in this file reads a real clock. The subject is a predicate about
// time whose whole risk is at the boundary, and `now` is a parameter for
// exactly that reason.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// res builds one inventory resource with a stated expiry.
func res(id, envID string, expires time.Time) provider.Resource {
	return provider.Resource{
		Kind: "container", ID: id, EnvID: envID,
		Labels: map[string]string{
			"expires": strconv.FormatInt(expires.UTC().Unix(), 10),
		},
	}
}

// unstamped builds one created by a release that knew nothing about expiry.
func unstamped(id, envID string) provider.Resource {
	return provider.Resource{Kind: "container", ID: id, EnvID: envID, Labels: map[string]string{}}
}

// recorder is a Destroyer that records rather than destroys.
type recorder struct {
	seen []string
	err  map[string]error
	n    map[string]int
}

func newRecorder() *recorder {
	return &recorder{err: map[string]error{}, n: map[string]int{}}
}

func (r *recorder) Destroy(_ context.Context, envID string) (int, error) {
	r.seen = append(r.seen, envID)
	if err := r.err[envID]; err != nil {
		return 0, err
	}
	if n, ok := r.n[envID]; ok {
		return n, nil
	}
	return 1, nil
}

// ---------------------------------------------------------------------------
// What it destroys
// ---------------------------------------------------------------------------

func TestPlan_NamesAnEnvironmentPastItsExpiry(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("c1", "af-old", epoch.Add(-time.Hour)),
	}, nil, epoch)

	require.Len(t, got, 1)
	require.Equal(t, "af-old", got[0].EnvID)
	require.Equal(t, time.Hour, got[0].Overdue)
	require.Equal(t, 1, got[0].Resources)
	require.False(t, got[0].Extended)
}

// ---------------------------------------------------------------------------
// What it must not destroy. A reaper with a wrong predicate is worse than
// none, so each of these is a separate named case rather than a table.
// ---------------------------------------------------------------------------

func TestPlan_LeavesAnEnvironmentInsideItsLifetime(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("c1", "af-live", epoch.Add(time.Hour)),
	}, nil, epoch)
	require.Empty(t, got)
}

// The boundary itself. An environment expiring exactly now is not yet past its
// lifetime, and this is the case a > against a >= gets wrong.
func TestPlan_LeavesAnEnvironmentExpiringExactlyNow(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("c1", "af-edge", epoch),
	}, nil, epoch)
	require.Empty(t, got)
}

func TestPlan_NeverDestroysWhatStatesNoLifetime(t *testing.T) {
	t.Parallel()
	// Everything an older release created. Reading "no lifetime stated" as
	// "lifetime already over" would turn an upgrade into a machine wipe.
	got := reaper.Plan([]provider.Resource{
		unstamped("c1", "af-legacy"),
		unstamped("c2", "af-legacy"),
	}, nil, epoch.Add(365*day))
	require.Empty(t, got)
}

func TestPlan_NeverDestroysWhatStatesAnUnreadableLifetime(t *testing.T) {
	t.Parallel()
	// A garbled label is "this resource states no lifetime", not "expired".
	// The other direction destroys an environment because a label was corrupt.
	got := reaper.Plan([]provider.Resource{
		{Kind: "container", ID: "c1", EnvID: "af-x", Labels: map[string]string{"expires": "not-a-time"}},
		{Kind: "container", ID: "c2", EnvID: "af-y", Labels: map[string]string{"expires": ""}},
	}, nil, epoch.Add(365*day))
	require.Empty(t, got)
}

func TestPlan_NeverDestroysAMachineScopedResource(t *testing.T) {
	t.Parallel()
	// The sidecar and forwarder images, shared by every environment on this
	// daemon. No environment's lifetime may take them.
	got := reaper.Plan([]provider.Resource{
		{Kind: "image", ID: "img", EnvID: "", Labels: map[string]string{
			"expires": strconv.FormatInt(epoch.Add(-day).Unix(), 10),
		}},
	}, nil, epoch)
	require.Empty(t, got)
}

// The case that makes this safe to run on a shared machine: another project's
// environment states its own lifetime, and the sweep reads that rather than
// the manifest of whoever happens to be running.
func TestPlan_LeavesAnotherProjectsEnvironmentAlone(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("c1", "af-mine", epoch.Add(-time.Hour)),
		res("c2", "af-theirs", epoch.Add(6*day)),
	}, nil, epoch)

	require.Len(t, got, 1)
	require.Equal(t, "af-mine", got[0].EnvID)
}

// An environment is a set of resources created over a span of time. Taking the
// oldest would destroy the containers somebody started ten minutes ago along
// with the network from this morning.
func TestPlan_TakesTheLatestExpiryWithinOneEnvironment(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("net", "af-a", epoch.Add(-6*time.Hour)),
		res("c1", "af-a", epoch.Add(time.Hour)),
	}, nil, epoch)
	require.Empty(t, got)
}

// ---------------------------------------------------------------------------
// Leases
// ---------------------------------------------------------------------------

func TestPlan_ALeaseKeepsAnExpiredEnvironmentAlive(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("c1", "af-a", epoch.Add(-time.Hour)),
	}, map[string]time.Time{"af-a": epoch.Add(day)}, epoch)
	require.Empty(t, got)
}

// A lease wins in both directions. Somebody who says "I am done with this at
// noon" is making the more recent deliberate statement about the environment,
// and the point of taking one out is to be believed.
func TestPlan_ALeaseCanAlsoShortenALifetime(t *testing.T) {
	t.Parallel()
	got := reaper.Plan([]provider.Resource{
		res("c1", "af-a", epoch.Add(6*day)),
	}, map[string]time.Time{"af-a": epoch.Add(-time.Hour)}, epoch)

	require.Len(t, got, 1)
	require.True(t, got[0].Extended)
}

func TestPlan_ALeaseForSomethingGoneIsNotAnEnvironment(t *testing.T) {
	t.Parallel()
	// A stale record for an environment already destroyed. Planning a teardown
	// for it would report removing something that does not exist.
	got := reaper.Plan(nil, map[string]time.Time{"af-ghost": epoch.Add(-day)}, epoch)
	require.Empty(t, got)
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

func TestPlan_IsOrderedByEnvironmentRegardlessOfInventoryOrder(t *testing.T) {
	t.Parallel()
	forward := []provider.Resource{
		res("c1", "af-c", epoch.Add(-day)),
		res("c2", "af-a", epoch.Add(-day)),
		res("c3", "af-b", epoch.Add(-day)),
	}
	backward := []provider.Resource{forward[2], forward[0], forward[1]}

	want := []string{"af-a", "af-b", "af-c"}
	for _, in := range [][]provider.Resource{forward, backward} {
		var got []string
		for _, e := range reaper.Plan(in, nil, epoch) {
			got = append(got, e.EnvID)
		}
		require.Equal(t, want, got)
	}
}

// ---------------------------------------------------------------------------
// Sweep
// ---------------------------------------------------------------------------

func TestSweep_DestroysEveryExpiredEnvironmentAndCountsWhatWent(t *testing.T) {
	t.Parallel()
	d := newRecorder()
	d.n["af-a"] = 4
	d.n["af-b"] = 3

	result := reaper.Sweep(context.Background(), []provider.Resource{
		res("c1", "af-a", epoch.Add(-day)),
		res("c2", "af-b", epoch.Add(-day)),
		res("c3", "af-live", epoch.Add(day)),
	}, nil, epoch, d)

	require.Equal(t, []string{"af-a", "af-b"}, d.seen)
	require.Equal(t, 3, result.Scanned)
	require.Equal(t, 7, result.Removed())
	require.Equal(t, 0, result.Failed())
}

func TestSweep_DestroysNothingWhenNothingHasExpired(t *testing.T) {
	t.Parallel()
	d := newRecorder()
	result := reaper.Sweep(context.Background(), []provider.Resource{
		res("c1", "af-live", epoch.Add(day)),
	}, nil, epoch, d)

	require.Empty(t, d.seen)
	require.Equal(t, 1, result.Scanned)
	require.Equal(t, 0, result.Removed())
}

// One unreachable provider must not strand the other environments, for the
// same reason teardown does not stop at the first failure.
func TestSweep_CarriesOnPastAFailureAndReportsItAgainstItsEnvironment(t *testing.T) {
	t.Parallel()
	d := newRecorder()
	d.err["af-a"] = errors.New("the daemon is not listening")

	result := reaper.Sweep(context.Background(), []provider.Resource{
		res("c1", "af-a", epoch.Add(-day)),
		res("c2", "af-b", epoch.Add(-day)),
	}, nil, epoch, d)

	require.Equal(t, []string{"af-a", "af-b"}, d.seen)
	require.Equal(t, 1, result.Failed())
	require.Error(t, result.Outcomes[0].Err)
	require.NoError(t, result.Outcomes[1].Err)
	require.Equal(t, 1, result.Removed())
}

// An interrupt leaves environments either untouched or fully removed and never
// half of each, so the check is between environments rather than inside one.
func TestSweep_StopsBetweenEnvironmentsWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := newRecorder()
	result := reaper.Sweep(ctx, []provider.Resource{
		res("c1", "af-a", epoch.Add(-day)),
		res("c2", "af-b", epoch.Add(-day)),
	}, nil, epoch, d)

	require.Empty(t, d.seen, "a cancelled sweep destroyed something")
	require.Equal(t, 1, result.Failed())
}

package traffic_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/traffic"
)

// The committed artifact, and the refusal that makes it worth having.
//
// A profile is committed because the machine that most needs the denominator
// is a pull request check that can reach production not at all. A committed
// number goes out of date silently, so a stale one is refused rather than
// quoted, and every refusal here says which input was stale rather than
// leaving the report to substitute a number for it.

func written(t *testing.T, p traffic.Profile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traffic.json")
	require.NoError(t, traffic.Write(path, p))
	return path
}

func full() traffic.Profile {
	return traffic.Profile{
		CollectedAt:     time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC),
		Source:          "otel export telemetry/traces.json",
		From:            time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC),
		To:              time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC),
		Requests:        145_152_200,
		PeakConcurrency: 90,
		Routes: []traffic.Route{
			{Method: "POST", Path: "/capture", Requests: 92_000_000, P95Ms: 12.4},
			{Method: "GET", Path: "/health", Requests: 41_000, P95Ms: 2},
		},
		Missing: []string{"1 span not counted: not a server span"},
	}
}

func TestLoad_RefusesAProfileOlderThanItsMaxAge(t *testing.T) {
	t.Parallel()
	path := written(t, full())
	now := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	p, why := traffic.Load(path, traffic.DefaultMaxAge, now)
	require.Empty(t, p.Routes, "a profile thirty days old was quoted")
	require.Contains(t, why, "was collected 30 days ago, past the 14 days it is allowed to be")
	require.Contains(t, why, "af traffic record")
}

func TestLoad_AcceptsAProfileInsideItsMaxAge(t *testing.T) {
	t.Parallel()
	path := written(t, full())
	now := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)
	p, why := traffic.Load(path, traffic.DefaultMaxAge, now)
	require.Empty(t, why)
	require.Len(t, p.Routes, 2)
}

func TestLoad_RefusesAProfileWithNoCollectionTime(t *testing.T) {
	t.Parallel()
	p := full()
	p.CollectedAt = time.Time{}
	_, why := traffic.Load(written(t, p), traffic.DefaultMaxAge, time.Now())
	require.Contains(t, why, "does not say when it was collected",
		"an unknown age is the most stale a profile can be and it was treated as fresh")
}

func TestLoad_RefusesAProfileNamingNoRoute(t *testing.T) {
	t.Parallel()
	p := full()
	p.Routes = nil
	_, why := traffic.Load(written(t, p), traffic.DefaultMaxAge, p.CollectedAt)
	require.Contains(t, why, "names no route",
		"a profile of nothing is a denominator of zero, which is how a report claims a run "+
			"covers everything")
}

func TestLoad_SaysWhereItLookedWhenThereIsNoProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "traffic.json")
	_, why := traffic.Load(path, traffic.DefaultMaxAge, time.Now())
	require.Contains(t, why, path)
	require.Contains(t, why, "af traffic record")
}

func TestLoad_SaysSoWhenTheFileIsNotAProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "traffic.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	_, why := traffic.Load(path, traffic.DefaultMaxAge, time.Now())
	require.Contains(t, why, "could not be read")
	require.Contains(t, why, "is not a traffic profile")
}

func TestStale_ZeroMaxAgeNeverRefuses(t *testing.T) {
	t.Parallel()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.False(t, traffic.Stale(old, 0, time.Now()),
		"a project that configured no maximum age was interrupted by a refresh it did not ask for")
	require.True(t, traffic.Stale(time.Time{}, traffic.DefaultMaxAge, time.Now()))
}

func TestWrite_RoundTripsEverythingAReportQuotes(t *testing.T) {
	t.Parallel()
	want := full()
	got, err := traffic.Read(written(t, want))
	require.NoError(t, err)
	require.Equal(t, want, got)

	// And the numbers a report quotes survive the trip, because a field that
	// round trips as zero is a report that says production serves nothing.
	require.EqualValues(t, 145_152_200, got.Requests)
	require.Equal(t, 90, got.PeakConcurrency)
	require.Equal(t, 12.4, got.Routes[0].P95Ms)
	rate, ok := got.Rate()
	require.True(t, ok)
	require.InDelta(t, 240.0, rate, 0.01)
}

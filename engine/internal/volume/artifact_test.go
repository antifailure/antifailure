package volume_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/volume"
)

// A stale profile is refused, the way golden.max_age already refuses a stale
// golden.
//
// The whole point of committing the artifact is that a machine which cannot
// reach production still has a denominator. A committed number goes out of
// date silently, and a fidelity report quoting a six month old row count is
// quoting a figure production has moved on from with nothing saying so. That
// is a worse report than one with no denominator at all, because the first is
// wrong and the second is honest.

func writeProfile(t *testing.T, p volume.Profile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "volume.json")
	require.NoError(t, volume.Write(path, p))
	return path
}

func TestLoad_RefusesAProfileOlderThanItsMaxAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	path := writeProfile(t, volume.Profile{
		CollectedAt: now.Add(-90 * 24 * time.Hour),
		Tables:      []volume.Table{table("public.events", 4_200_000_000)},
	})

	p, why := volume.Load(path, 30*24*time.Hour, now)
	require.Empty(t, p.Tables, "a stale profile was handed back as a denominator")
	require.Contains(t, why, "90 days ago")
	require.Contains(t, why, "past the 30 days it is allowed to be")
	require.Contains(t, why, "af volume record")
}

func TestLoad_AcceptsAProfileInsideItsMaxAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	path := writeProfile(t, volume.Profile{
		CollectedAt: now.Add(-29 * 24 * time.Hour),
		Tables:      []volume.Table{table("public.events", 4_200_000_000)},
	})

	p, why := volume.Load(path, 30*24*time.Hour, now)
	require.Empty(t, why)
	require.Len(t, p.Tables, 1)
	require.EqualValues(t, 4_200_000_000, p.Rows())
}

// A profile that does not say when it was taken is the one case where the age
// cannot be checked at all, and an unknown age is the most stale a profile can
// be.
func TestLoad_RefusesAProfileWithNoCollectionTime(t *testing.T) {
	t.Parallel()
	path := writeProfile(t, volume.Profile{
		Tables: []volume.Table{table("public.events", 12)},
	})
	p, why := volume.Load(path, 30*24*time.Hour, time.Now())
	require.Empty(t, p.Tables)
	require.Contains(t, why, "does not say when it was collected")
}

func TestLoad_SaysWhereItLookedWhenThereIsNoProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nothing.json")
	p, why := volume.Load(path, 30*24*time.Hour, time.Now())
	require.Empty(t, p.Tables)
	require.Contains(t, why, path)
	require.Contains(t, why, "af volume record")
}

func TestLoad_SaysSoWhenTheFileIsNotAProfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "volume.json")
	require.NoError(t, os.WriteFile(path, []byte("{{{"), 0o600))
	_, why := volume.Load(path, 30*24*time.Hour, time.Now())
	require.Contains(t, why, "could not be read")
}

// A maximum age of zero is not a maximum age, which is exactly golden.Stale's
// rule: a project that has not configured one is never interrupted.
func TestStale_ZeroMaxAgeNeverRefuses(t *testing.T) {
	t.Parallel()
	require.False(t, volume.Stale(time.Time{}, 0, time.Now()))
	require.True(t, volume.Stale(time.Time{}, time.Hour, time.Now()),
		"a profile with no collection time was treated as fresh")
}

func TestWrite_RoundTripsEverythingAReportQuotes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	want := volume.Profile{
		CollectedAt: now, Source: "the database named by PRODUCTION_DATABASE_URL",
		ServerVersion: "PostgreSQL 17.2",
		Tables: []volume.Table{{
			Name: "public.events", Rows: 4_200_000_000, Analyzed: true,
			TableBytes: 900 << 30, IndexBytes: 120 << 30,
			Partitions: 24, LargestPartitionRows: 800_000_000,
			Keys: []volume.Key{{Column: "id", Distinct: 4_200_000_000}},
		}},
		Missing: []string{"public.staging has never been analyzed"},
	}
	path := writeProfile(t, want)

	got, err := volume.Read(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// age is what the refusal message is written in, and it is the difference
// between "collected 187 days ago" and a duration in nanoseconds.
func TestLoad_TheRefusalSaysTheAgeInTheUnitSomebodyReadsIt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{90 * 24 * time.Hour, "90 days ago"},
		{6 * time.Hour, "6 hours ago"},
		{20 * time.Minute, "20 minutes ago"},
		{30 * time.Second, "30s ago"},
	} {
		path := writeProfile(t, volume.Profile{
			CollectedAt: now.Add(-c.ago),
			Tables:      []volume.Table{table("public.events", 12)},
		})
		// A maximum age of a second, so every one of these is stale and the
		// only thing under test is how the age is rendered.
		_, why := volume.Load(path, time.Second, now)
		require.Contains(t, why, c.want)
	}
}

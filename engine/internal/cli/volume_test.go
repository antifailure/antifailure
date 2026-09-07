package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// af volume, end to end through the command rather than through the package.
//
// The reading half of this lane has no credential and reaches nothing: the
// manifest names a file, and every machine afterwards, including a pull
// request check that can reach nothing at all, reads the file. That whole path
// is exercised here, which is what makes it a check on the feature rather than
// on the arithmetic underneath it. The recording half needs a database to
// record from, so its refusals are here and its success is in
// engine/internal/env's volume_postgres_test.go, against a real Postgres.

// volumeProject writes a manifest declaring a profile, and the profile.
//
// The clock the CLI runs on is fixed at 2026-01-01, so a profile dated
// relative to that is what decides whether it is refused. collected is written
// into the artifact exactly as given.
func volumeProject(t *testing.T, collected string, maxAge string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: docker
  volume:
    profile: .antifailure/volume.json
`
	if maxAge != "" {
		manifest += "    max_age: " + maxAge + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(manifest), 0o600))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".antifailure"), 0o750))
	profile := `{
  "collected_at": "` + collected + `",
  "source": "the database named by PRODUCTION_DATABASE_URL",
  "tables": [
    {"name": "public.events", "rows": 4200000000, "analyzed": true,
     "table_bytes": 966367641600, "index_bytes": 128849018880,
     "partitions": 24, "largest_partition_rows": 4000000000,
     "keys": [{"column": "id", "distinct": 4200000000}]},
    {"name": "public.users", "rows": 8400000, "analyzed": true}
  ]
}`
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".antifailure", "volume.json"), []byte(profile), 0o600))
	return dir
}

func TestVolumeShow_PrintsWhatProductionHolds(t *testing.T) {
	t.Parallel()
	dir := volumeProject(t, "2025-12-20T02:00:00Z", "")

	got := runCLI(t, dir, nil, "volume", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "4,200,000,000")
	require.Contains(t, got.stdout, "public.users")
	require.Contains(t, prose(got.stdout), "24, largest holds 95 percent",
		"a partitioned table with nearly every row in one partition did not say so")
	require.Contains(t, prose(got.stdout), "public.events.id 4,200,000,000",
		"the cardinality of the join column is missing")
}

// The refusal. A profile past its max_age is not printed with a warning beside
// it, because a stale denominator is an unknown rather than a smaller number.
func TestVolumeShow_RefusesAProfilePastItsMaxAge(t *testing.T) {
	t.Parallel()
	// The clock is 2026-01-01 and the profile is from 2025-06-01, which is
	// more than the thirty days the default allows.
	dir := volumeProject(t, "2025-06-01T02:00:00Z", "")

	got := runCLI(t, dir, nil, "volume", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, prose(got.stdout), "213 days ago")
	require.Contains(t, prose(got.stdout), "past the 30 days it is allowed to be")
	require.NotContains(t, got.stdout, "4,200,000,000",
		"the refused profile's numbers were printed anyway, which is the whole failure")
}

// A longer max_age is honoured, so the refusal is the manifest's decision
// rather than a fixed rule. A check that always refuses is not a check.
func TestVolumeShow_HonoursALongerMaxAge(t *testing.T) {
	t.Parallel()
	dir := volumeProject(t, "2025-06-01T02:00:00Z", "8760h")

	got := runCLI(t, dir, nil, "volume", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "4,200,000,000")
}

func TestVolumeShow_SaysWhereItLookedWhenThereIsNoProfile(t *testing.T) {
	t.Parallel()
	dir := volumeProject(t, "2025-12-20T02:00:00Z", "")
	require.NoError(t, os.Remove(filepath.Join(dir, ".antifailure", "volume.json")))

	got := runCLI(t, dir, nil, "volume", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, prose(got.stdout), "no volume profile has been recorded")
	require.Contains(t, prose(got.stdout), "af volume record")
}

func TestVolumeShow_JSONCarriesTheAgeAndTheLimitBesideTheNumbers(t *testing.T) {
	t.Parallel()
	dir := volumeProject(t, "2025-12-20T02:00:00Z", "")

	got := runCLI(t, dir, nil, "volume", "show", "-o", "json")
	require.Zero(t, got.code, got.stderr)

	var doc struct {
		CollectedAt string  `json:"collected_at"`
		AgeHours    float64 `json:"age_hours"`
		MaxAgeHours float64 `json:"max_age_hours"`
		Stale       bool    `json:"stale"`
		Rows        int64   `json:"rows"`
		Tables      []struct {
			Name                  string   `json:"name"`
			Rows                  int64    `json:"rows"`
			LargestPartitionShare *float64 `json:"largest_partition_share"`
		} `json:"tables"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &doc))
	require.Equal(t, "2025-12-20T02:00:00Z", doc.CollectedAt)
	require.EqualValues(t, 4_208_400_000, doc.Rows)
	require.False(t, doc.Stale)
	require.InDelta(t, 720, doc.MaxAgeHours, 0.01,
		"the limit the number was judged against is not beside it")
	require.Greater(t, doc.AgeHours, 200.0)
	require.Len(t, doc.Tables, 2)
	require.NotNil(t, doc.Tables[0].LargestPartitionShare,
		"a partitioned table reported no skew")
	require.Nil(t, doc.Tables[1].LargestPartitionShare,
		"an unpartitioned table reported a skew of zero, which is not the same as none")
}

// A manifest that declares no volume block says so rather than failing, and
// names the key to add.
func TestVolumeRecord_RefusesWhenTheManifestNamesNowhereToWriteIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(`
version: 1
name: shop
services:
  - name: web
    port: 3000
`), 0o600))

	got := runCLI(t, dir, nil, "volume", "record")
	require.NotZero(t, got.code)
	require.Contains(t, prose(got.stderr), "declares no database.volume.profile")
}

// And one that declares nowhere to read production from is refused by name,
// rather than handed an empty profile. A profile of nothing is a denominator
// of zero.
func TestVolumeRecord_RefusesWhenNothingNamesAProductionDatabase(t *testing.T) {
	t.Parallel()
	dir := volumeProject(t, "2025-12-20T02:00:00Z", "")

	got := runCLI(t, dir, nil, "volume", "record")
	require.NotZero(t, got.code)
	require.Contains(t, prose(got.stderr), "database.source_url_env names no variable")
}

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// af traffic, end to end through the command rather than through the package.
//
// Neither half of this lane opens a socket. The manifest names a file, and
// every machine afterwards, including a pull request check that can reach
// nothing at all, reads the file; recording reads a second file a collector or
// a reverse proxy already wrote. That whole path is exercised here, which is
// what makes it a check on the feature rather than on the arithmetic
// underneath it.

// trafficProject writes a manifest declaring a profile, and the profile.
//
// The clock the CLI runs on is fixed at 2026-01-01, so a profile dated
// relative to that is what decides whether it is refused. The four safe_routes
// are this repository's own shape: a hand written list that covers a fraction
// of what production serves.
func trafficProject(t *testing.T, collected, maxAge string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `
version: 1
name: shop
services:
  - name: web
    port: 3000
load:
  enabled: true
  source: none
  safe_routes:
    - GET /health
    - GET /api/organizations/1/members
  traffic:
    profile: .antifailure/traffic.json
`
	if maxAge != "" {
		manifest += "    max_age: " + maxAge + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(manifest), 0o600))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".antifailure"), 0o750))
	profile := `{
  "collected_at": "` + collected + `",
  "source": "otel export telemetry/traces.json",
  "from": "2025-12-13T02:00:00Z",
  "to": "2025-12-20T02:00:00Z",
  "requests": 130041000,
  "peak_concurrency": 90,
  "routes": [
    {"method": "POST", "path": "/capture", "requests": 92000000, "p95_ms": 12.4},
    {"method": "GET", "path": "/decide", "requests": 38000000, "p95_ms": 8.1},
    {"method": "GET", "path": "/api/organizations/{id}/members", "requests": 32000, "p95_ms": 44},
    {"method": "GET", "path": "/health", "requests": 9000, "p95_ms": 2}
  ]
}`
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".antifailure", "traffic.json"), []byte(profile), 0o600))
	return dir
}

func TestTrafficShow_PrintsWhatProductionServesAndWhatTheRunSends(t *testing.T) {
	t.Parallel()
	dir := trafficProject(t, "2025-12-20T02:00:00Z", "")

	got := runCLI(t, dir, nil, "traffic", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "POST /capture")
	require.Contains(t, got.stdout, "92,000,000")
	require.Contains(t, prose(got.stdout), "215 requests a second, 90 in flight at once at its peak")

	// The finding: the two routes the run reaches carry a fraction of a
	// percent, and the one it never sends carries two thirds.
	require.Contains(t, prose(got.stdout),
		"this run sends 2 of the 4 routes production served, carrying 0.031 percent of its requests")
	require.Contains(t, prose(got.stdout), "The heaviest it never sends is POST /capture")

	// The other half of what a run reproduces, which a coverage percentage
	// hides completely. Scale is the manifest default of 0.05 against the
	// smoke shape's five requests a second.
	require.Contains(t, prose(got.stdout),
		"How fast it sends: this run sends 0.25 requests a second against production's "+
			"215 requests a second, which is 0.11 percent of it")

	// And the lines somebody would paste, printed rather than written.
	require.Contains(t, prose(got.stdout), "- POST /capture")
	require.Contains(t, prose(got.stdout), "Read them before pasting them")

	// The manifest is not touched. Proposing is not adjusting.
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(body), "/capture",
		"the command wrote the route into the manifest, which is the one thing it must not do")
}

// The refusal. A profile past its max_age is not printed with a warning beside
// it, because a stale denominator is an unknown rather than a smaller number.
func TestTrafficShow_RefusesAProfilePastItsMaxAge(t *testing.T) {
	t.Parallel()
	dir := trafficProject(t, "2025-06-01T02:00:00Z", "")

	got := runCLI(t, dir, nil, "traffic", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, prose(got.stdout), "213 days ago")
	require.Contains(t, prose(got.stdout), "past the 14 days it is allowed to be")
	require.NotContains(t, got.stdout, "92,000,000",
		"the refused profile's numbers were printed anyway, which is the whole failure")
}

// A longer max_age is honoured, so the refusal is the manifest's decision
// rather than a fixed rule. A check that always refuses is not a check.
func TestTrafficShow_HonoursALongerMaxAge(t *testing.T) {
	t.Parallel()
	dir := trafficProject(t, "2025-06-01T02:00:00Z", "8760h")

	got := runCLI(t, dir, nil, "traffic", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "92,000,000")
}

func TestTrafficShow_SaysWhereItLookedWhenThereIsNoProfile(t *testing.T) {
	t.Parallel()
	dir := trafficProject(t, "2025-12-20T02:00:00Z", "")
	require.NoError(t, os.Remove(filepath.Join(dir, ".antifailure", "traffic.json")))

	got := runCLI(t, dir, nil, "traffic", "show")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, prose(got.stdout), "no traffic profile has been recorded")
	require.Contains(t, prose(got.stdout), "af traffic record")
}

func TestTrafficShow_JSONCarriesTheCoverageBesideTheRoutes(t *testing.T) {
	t.Parallel()
	dir := trafficProject(t, "2025-12-20T02:00:00Z", "")

	got := runCLI(t, dir, nil, "traffic", "show", "-o", "json")
	require.Zero(t, got.code, got.stderr)

	var doc struct {
		CollectedAt       string  `json:"collected_at"`
		AgeHours          float64 `json:"age_hours"`
		MaxAgeHours       float64 `json:"max_age_hours"`
		Stale             bool    `json:"stale"`
		Requests          int64   `json:"requests"`
		RequestsPerSecond float64 `json:"requests_per_second"`
		PeakConcurrency   int     `json:"peak_concurrency"`
		Routes            []struct {
			Path  string  `json:"path"`
			Share float64 `json:"share"`
			Sent  bool    `json:"sent"`
		} `json:"routes"`
		Coverage *struct {
			Share      float64  `json:"share"`
			Covers     bool     `json:"covers"`
			SentRoutes int      `json:"sent_routes"`
			Uncovered  []string `json:"uncovered"`
		} `json:"coverage"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &doc))
	require.Equal(t, "2025-12-20T02:00:00Z", doc.CollectedAt)
	require.EqualValues(t, 130_041_000, doc.Requests)
	require.False(t, doc.Stale)
	require.InDelta(t, 336, doc.MaxAgeHours, 0.01,
		"the limit the number was judged against is not beside it")
	require.InDelta(t, 215.0, doc.RequestsPerSecond, 1.0)
	require.Equal(t, 90, doc.PeakConcurrency)
	require.Len(t, doc.Routes, 4)
	require.Equal(t, "/capture", doc.Routes[0].Path)
	require.False(t, doc.Routes[0].Sent)

	require.NotNil(t, doc.Coverage)
	require.False(t, doc.Coverage.Covers)
	require.Equal(t, 2, doc.Coverage.SentRoutes)
	require.Contains(t, doc.Coverage.Uncovered, "POST /capture")
}

// A manifest that declares no traffic block says so rather than failing, and
// names the key to add.
func TestTrafficRecord_RefusesWhenTheManifestNamesNowhereToWriteIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(`
version: 1
name: shop
services:
  - name: web
    port: 3000
`), 0o600))

	got := runCLI(t, dir, nil, "traffic", "record")
	require.NotZero(t, got.code)
	require.Contains(t, prose(got.stderr), "declares no load.traffic.profile")
}

// Recording writes the profile the manifest names, from the file the command
// is given, and nothing in the path opens a socket.
func TestTrafficRecord_WritesTheProfileFromAFileOnDisk(t *testing.T) {
	t.Parallel()
	dir := trafficProject(t, "2025-12-20T02:00:00Z", "")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "access.log"), []byte(
		`1.2.3.4 - - [20/Dec/2025:02:00:00 +0000] "GET /health HTTP/1.1" 200 12`+"\n"+
			`1.2.3.4 - - [20/Dec/2025:02:00:10 +0000] "POST /capture HTTP/1.1" 204 0`+"\n"), 0o600))

	got := runCLI(t, dir, nil, "traffic", "record", "--from", "access.log")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, prose(got.stdout), "2 routes, 2 requests")
	require.Contains(t, prose(got.stdout), "carries no request duration")

	body, err := os.ReadFile(filepath.Join(dir, ".antifailure", "traffic.json"))
	require.NoError(t, err)
	require.Contains(t, string(body), `"path": "/capture"`)
	require.Contains(t, string(body), `"source": "access log access.log"`,
		"the recorded source names an absolute path, which would put somebody's home "+
			"directory in a committed file")
}

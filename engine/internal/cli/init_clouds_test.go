package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A finding nothing prints is the same silence in a new place.
//
// Detection can propose a stance for every store in a compose file and name
// every cloud service with no rule, and if af init never renders any of it the
// developer's experience is unchanged: a manifest that mentions one store, and
// a refusal in an environment three days later that says no rule matches.
// These tests run the real command over a real directory and read its real
// output, so a proposal with no reader fails here rather than shipping.

// cloudsFixture is an analytics product: Postgres for records, ClickHouse for
// events, Redis for cache, Kafka for the bus, LocalStack standing in for AWS,
// and one AWS SDK the catalog has no host for.
func cloudsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"name":"insight","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0","@aws-sdk/client-textract":"3.0.0"}}`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  db:
    image: postgres:16
  events:
    image: clickhouse/clickhouse-server:24.3
  cache:
    image: redis:7
  broker:
    image: confluentinc/cp-kafka:7.6.0
  localstack:
    image: localstack/localstack:3.4
    environment:
      SERVICES: s3,sqs
`,
	} {
		filename := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
		require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
	}
	return dir
}

func initCloudsJSON(t *testing.T, dir string) struct {
	Datastores []struct {
		Name     string `json:"name"`
		Engine   string `json:"engine"`
		Stance   string `json:"stance"`
		From     string `json:"from"`
		Declared bool   `json:"declared"`
		Evidence string `json:"evidence"`
	} `json:"datastores"`
	Emulators []struct {
		Product  string   `json:"Product"`
		Cloud    string   `json:"Cloud"`
		Service  string   `json:"Service"`
		Services []string `json:"Services"`
		Hosts    []string `json:"Hosts"`
		Unnamed  []string `json:"Unnamed"`
	} `json:"emulators"`
	CloudGaps []string `json:"cloud_gaps"`
} {
	t.Helper()
	var report struct {
		Datastores []struct {
			Name     string `json:"name"`
			Engine   string `json:"engine"`
			Stance   string `json:"stance"`
			From     string `json:"from"`
			Declared bool   `json:"declared"`
			Evidence string `json:"evidence"`
		} `json:"datastores"`
		Emulators []struct {
			Product  string   `json:"Product"`
			Cloud    string   `json:"Cloud"`
			Service  string   `json:"Service"`
			Services []string `json:"Services"`
			Hosts    []string `json:"Hosts"`
			Unnamed  []string `json:"Unnamed"`
		} `json:"emulators"`
		CloudGaps []string `json:"cloud_gaps"`
	}
	got := runCLI(t, dir, nil, "init", "--non-interactive", "--output", "json")
	require.Equal(t, 0, got.code, got.stderr)
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))
	return report
}

func TestInitClouds_TheReportNamesEveryStoreIncludingTheOnesLeftOut(t *testing.T) {
	report := initCloudsJSON(t, cloudsFixture(t))

	declared := map[string]bool{}
	for _, d := range report.Datastores {
		declared[d.Name] = d.Declared
	}
	require.Equal(t, map[string]bool{"events": true, "cache": false, "broker": false}, declared,
		"a store this build cannot provide must be reported rather than dropped or declared")
}

// datastoreRow returns the one row of the Datastores table whose STORE column
// is name, matched on the row's own leading field.
//
// It is a leading field rather than a substring because a substring cannot
// tell the STORE column from the rest of the row, and every word this test
// wants is in the rest of the row. Each store's reason is rendered in its own
// row's WHY column, and each of these reasons happens to contain the compose
// service's name: ClickHouse's reason opens "the events are here", Redis's
// says "a cache is rebuilt from the primary", Kafka's says "a broker wants its
// topics". So a row printing the ENGINE in the STORE column still contains
// every word, on that same row, and only the position separates the two.
//
// Exactly one row must match. Two would mean the search found the table and
// something else, and returning the first would be a check reading whichever
// one came out on top.
func datastoreRow(t *testing.T, stdout, name string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if field, _, ok := strings.Cut(trimmed, " "); ok && field == name {
			found = append(found, trimmed)
		}
	}
	require.Len(t, found, 1,
		"want exactly one summary row whose first column is %q, got %d: %v", name, len(found), found)
	return found[0]
}

func TestInitClouds_TheSummaryPrintsTheStoresAndTheirStances(t *testing.T) {
	dir := cloudsFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)

	// The table, by the developer's own names for the stores, read a row at a
	// time and anchored on the STORE column.
	//
	// This asserted Contains over the whole summary until a mutation proved it
	// could not fail: printing the ENGINE in the STORE column, which deletes
	// the developer's own name for every store, left all eight words on the
	// page and the test green. "clickhouse", "redis" and "kafka" came back
	// from the ENGINE column, "events" from ClickHouse's reason, and "cache"
	// and "broker" from the sentences about the two stores left out. The
	// richer the rendered page, the weaker a Contains against it, and this
	// page is deliberately rich.
	for _, want := range []struct{ store, engine, stance, where string }{
		{"events", "clickhouse", "golden", "manifest"},
		{"cache", "redis", "empty", "not declared"},
		{"broker", "kafka", "topics_only", "not declared"},
	} {
		row := datastoreRow(t, got.stdout, want.store)
		require.Contains(t, row, want.engine,
			"the row for %q does not name the engine it runs", want.store)
		require.Contains(t, row, want.stance,
			"the row for %q does not carry the stance, which is the decision", want.store)
		require.Contains(t, row, want.where,
			"the row for %q does not say whether it reached the manifest", want.store)
	}
	// And the sentence for the two it did not declare, each with the stance it
	// would have taken, because the stance is the decision.
	require.Contains(t, got.stdout, "It is not in the manifest",
		"a store left out of the manifest must be named in the summary")
}

func TestInitClouds_OnlyTheProvidableStoreReachesTheManifestOnDisk(t *testing.T) {
	dir := cloudsFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	text := string(body)
	require.Contains(t, text, "clickhouse")
	require.NotContains(t, text, "engine: redis",
		"a datastore this build has no provider for makes a manifest af up refuses")
	require.NotContains(t, text, "engine: kafka")
}

func TestInitClouds_TheEmulatorAndItsRulesAreReported(t *testing.T) {
	report := initCloudsJSON(t, cloudsFixture(t))
	require.Len(t, report.Emulators, 1)
	em := report.Emulators[0]
	require.Equal(t, "LocalStack", em.Product)
	require.Equal(t, "aws", em.Cloud)
	require.Equal(t, []string{"s3", "sqs"}, em.Services)
	require.Contains(t, em.Hosts, "s3.amazonaws.com")
	require.Contains(t, em.Hosts, "sqs.*.amazonaws.com")
	require.Empty(t, em.Unnamed)
}

func TestInitClouds_TheSummaryPrintsTheEmulator(t *testing.T) {
	dir := cloudsFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	require.Contains(t, got.stdout, "Cloud emulators")
	require.Contains(t, got.stdout, "LocalStack")
}

func TestInitClouds_TheServiceWithNoRuleIsNamedInTheReportAndTheSummary(t *testing.T) {
	report := initCloudsJSON(t, cloudsFixture(t))
	require.Len(t, report.CloudGaps, 1)
	require.Contains(t, report.CloudGaps[0], "@aws-sdk/client-textract")

	// A second directory, because af init refuses to replace a manifest it
	// already wrote and the two runs must not share one.
	got := runCLI(t, cloudsFixture(t), nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	require.Contains(t, got.stdout, "Cloud services with no rule")
	require.Contains(t, got.stdout, "textract",
		"a service that will be refused with no rule matches must be named while it is cheap to fix")
}

func TestInitClouds_ARepositoryWithNoneOfThisPrintsNoneOfIt(t *testing.T) {
	// A section that appears on every run is a section nobody reads. This is
	// the direction that makes the other six worth having.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"plain","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	for _, section := range []string{"Datastores", "Cloud emulators", "Cloud services with no rule"} {
		require.NotContains(t, got.stdout, section,
			"%q appeared for a repository that has none", section)
	}
	require.NotContains(t, strings.ToLower(got.stdout), "it is not in the manifest")
}

package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// The lane's number, measured on the path a user actually takes.
//
// Not on a rig. Every step below is the one `af mask crossstore` performs: a
// manifest a person would write, parsed and normalized by the real loader, an
// orchestrator built the way the command builds one, the two connection
// strings resolved through the secret chain from the variables the manifest
// NAMES, both catalogs read by the readers that ship, and the number taken off
// the orchestrator method the command calls. The only thing not exercised here
// is cobra parsing the flags.
//
// It shares its production stores with the live test beside it, which is the
// point rather than a saving: productionPostgres and productionClickHouse
// build the same analytics shaped pair that `af up` refreshes, masks and
// branches, so the schema this measures against is the schema of a twin the
// product can actually produce.

// crossStoreManifest is the analytics stack with both stores naming where
// their schema is read from.
//
// The same shape as the up manifest beside it, minus the services, because the
// cross store check reads schemas and starts nothing. That difference is worth
// keeping: a customer can run this before `af up` has ever succeeded, which is
// exactly when a masking rule is being written.
const crossStoreManifest = `
version: 1
name: crossstoretwin
services:
  - name: charts
    kind: worker
    build:
      strategy: image
      image: alpine:3.20
    command: sleep 3600
database:
  provider: docker
  version: 17
  source_url_env: PRODUCTION_DATABASE_URL
  masking_rules: ./masking.yaml
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: PRODUCTION_CLICKHOUSE_URL
egress:
  default: block
`

// crossStoreRules is one rules file for both stores, and it agrees.
const crossStoreRules = `
rules:
  - column: distinct_id
    transform: email
    link: person
    why: "this product's distinct id is the address a real person reads"

  - column: email
    transform: email
    link: person
    why: "the address itself"

  - column: properties
    transform: empty_json
    why: "per person fields, jsonb in Postgres and a String in ClickHouse"

  - column: event
    transform: preserve
    why: "an event name is not a person and a chart of nothing is not a twin"
`

// crossStoreDivergentRules is a break a person could actually write.
//
// Somebody hashes the address in the analytics store because it is only ever a
// key there, and leaves the primary masking it as an address. Every row still
// looks masked, both stores verify clean, and one person is now two people:
// every join across the two returns nobody or somebody else, and every report
// built on it is plausible.
const crossStoreDivergentRules = `
rules:
  - table: "*.events"
    column: distinct_id
    transform: hash_hex
    why: "in the analytics store this is only ever a key, so hash it"

  - column: distinct_id
    transform: email
    link: person
    why: "this product's distinct id is the address a real person reads"

  - column: email
    transform: email
    link: person
    why: "the address itself"

  - column: properties
    transform: empty_json
    why: "per person fields, jsonb in Postgres and a String in ClickHouse"

  - column: event
    transform: preserve
    why: "an event name is not a person and a chart of nothing is not a twin"
`

// crossStoreProject writes the manifest and the rules and builds the
// orchestrator, exactly as the command does.
func crossStoreProject(t *testing.T, rules, pgSource, chSource string) *Orchestrator {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"),
		[]byte(strings.TrimSpace(crossStoreManifest)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "masking.yaml"),
		[]byte(strings.TrimSpace(rules)+"\n"), 0o644))

	m, err := manifest.Load(filepath.Join(root, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := New(Options{
		Root: root, Manifest: m, Branch: "l82-crossstore",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(line string) { t.Log(line) },
		Getenv: func(k string) string {
			switch k {
			case MaskingKeyEnv:
				return "a-project-key-long-enough-to-be-accepted"
			case "PRODUCTION_DATABASE_URL":
				return pgSource
			case "PRODUCTION_CLICKHOUSE_URL":
				return chSource
			}
			return ""
		},
	})
	require.NoError(t, err)
	return o
}

// TestCrossStoreLive_TheNumberOnTheUsersOwnStores is the lane's measurement.
func TestCrossStoreLive_TheNumberOnTheUsersOwnStores(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and this needs a ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgSource := productionPostgres(t)
	chSource, _ := productionClickHouse(t, ctx)

	t.Run("the number", func(t *testing.T) {
		o := crossStoreProject(t, crossStoreRules, pgSource, chSource.Reveal())
		res, err := o.CrossStoreCheck(ctx)
		require.NoError(t, err)
		t.Log("\n" + res.Report.Summary())

		require.Empty(t, res.Report.Unread,
			"a store the command could not read: %v", res.Report.Unread)
		require.Equal(t, []string{"primary", "events"}, res.Report.Read)
		require.Empty(t, res.WithoutSource,
			"both stores name a source, so nothing may be reported as never read")
		require.True(t, res.Report.OK(), res.Report.Summary())
		require.Positive(t, res.Report.Cross.Checked,
			"the two stores hold one product's people and share at least one identifier; "+
				"a run that found none has read a catalog wrong rather than proved anything")
		require.Equal(t, 100.0, res.Report.Cross.Percent())
		require.Positive(t, res.Report.Tables)
		require.Positive(t, res.Report.Columns)
	})

	// The other half of the row's acceptance, on the same two stores.
	t.Run("a divergent rules file says no", func(t *testing.T) {
		o := crossStoreProject(t, crossStoreDivergentRules, pgSource, chSource.Reveal())
		res, err := o.CrossStoreCheck(ctx)
		require.NoError(t, err,
			"a disagreement is a finding the report carries, not a failure to report")
		t.Log("\n" + res.Report.Summary())

		require.False(t, res.Report.OK(),
			"one identifier masked two different ways in the two stores was called a pass")
		require.NotEmpty(t, res.Report.Cross.Mismatches())
		require.Contains(t, res.Report.Summary(), "distinct_id")
		require.Positive(t, res.Report.Cross.Checked,
			"a report that says no because it compared nothing is not this test's subject")
	})

	// The variable is unset, which is the commonest way a run reaches one
	// store. It must report that it proved nothing rather than a hundred
	// percent of the store that answered.
	t.Run("an unset variable is not a pass", func(t *testing.T) {
		o := crossStoreProject(t, crossStoreRules, pgSource, "")
		res, err := o.CrossStoreCheck(ctx)
		require.NoError(t, err)
		t.Log("\n" + res.Report.Summary())

		require.False(t, res.Report.OK())
		require.Equal(t, []string{"primary"}, res.Report.Read)
		require.Len(t, res.Report.Unread, 1)
		require.Equal(t, "events", res.Report.Unread[0].Store)
		require.Contains(t, res.Report.Unread[0].Why, "PRODUCTION_CLICKHOUSE_URL",
			"the message names the variable to set, and never its value")
		require.Contains(t, res.Report.Summary(), "nothing is proved")
	})
}

// The check reads schemas and NO ROWS, which is what makes it safe to point at
// production. Asserted against the servers rather than by reading the code:
// the source stores are read while their own query logs are watched, and the
// only statements that reach them list tables and columns.
func TestCrossStoreLive_ItReadsNoRows(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and this needs a ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgSource := productionPostgres(t)
	chSource, server := productionClickHouse(t, ctx)

	o := crossStoreProject(t, crossStoreRules, pgSource, chSource.Reveal())
	before := time.Now().UTC().Add(-2 * time.Second)
	res, err := o.CrossStoreCheck(ctx)
	require.NoError(t, err)
	require.True(t, res.Report.OK(), res.Report.Summary())

	chExec(t, server.URL, "SYSTEM FLUSH LOGS")
	// Every statement the check sent, out of the server's own log. A SELECT
	// naming a table of the source database would be a row read, and the whole
	// safety claim rests on there being none.
	out := chQuery(t, server.URL, `
SELECT query FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= toDateTime(`+quoteTime(before)+`)
  AND query NOT LIKE '%system.query_log%'
  AND query LIKE '%system.%'
FORMAT TSV`)
	require.NotEmpty(t, strings.TrimSpace(out),
		"the check read no catalog at all, so this proves nothing about what it did not read")

	rows := chQuery(t, server.URL, `
SELECT count() FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= toDateTime(`+quoteTime(before)+`)
  AND positionCaseInsensitive(query, 'FROM events') > 0
FORMAT TSV`)
	require.Equal(t, "0", strings.TrimSpace(rows),
		"the cross store check selected from a data table, so it is not safe to point "+
			"at production and the report's rows_read of zero is a lie")
}

// quoteTime renders a time as a ClickHouse literal.
func quoteTime(at time.Time) string {
	return "'" + at.Format("2006-01-02 15:04:05") + "'"
}

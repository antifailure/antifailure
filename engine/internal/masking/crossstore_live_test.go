package masking_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The lane's acceptance, run against two real servers.
//
// Everything else about cross store determinism can be argued from the
// construction: the subkey comes from the column identity, the identity does
// not name the store, so the same identity gives the same output. This is the
// argument being checked rather than repeated. Two databases, two engines, one
// key, one rules file, and the address that identifies one person read back out
// of both of them afterwards.

// personTable is the Postgres side of the pair, added beside the fixture the
// rest of this package's live tests use.
const personTable = `
CREATE TABLE person (
  id          bigserial PRIMARY KEY,
  distinct_id text NOT NULL UNIQUE,
  email       text NOT NULL,
  properties  jsonb
);
INSERT INTO person (distinct_id, email, properties) VALUES
  ('ada@lovelace-analytics.co.uk',  'ada@lovelace-analytics.co.uk',  '{"plan":"team"}'),
  ('grace@hopper-systems.io', 'grace@hopper-systems.io', '{"plan":"free"}'),
  ('alan@turing-labs.net',  'alan@turing-labs.net',  '{"plan":"team"}');
`

// eventsForThosePeople is the ClickHouse side, holding the same three people.
const eventsForThosePeople = `
CREATE TABLE events (
  uuid        UUID,
  event       String,
  distinct_id String,
  email       Nullable(String),
  properties  String,
  ts          DateTime64(6)
) ENGINE = MergeTree ORDER BY uuid`

const insertThosePeople = `INSERT INTO events VALUES
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a01', 'pageview', 'ada@lovelace-analytics.co.uk',
   'ada@lovelace-analytics.co.uk', '{"plan":"team"}', '2026-09-07 10:00:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a02', 'pageview', 'grace@hopper-systems.io',
   'grace@hopper-systems.io', '{"plan":"free"}', '2026-09-07 10:01:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a03', 'pageview', 'alan@turing-labs.net',
   'alan@turing-labs.net', '{"plan":"team"}', '2026-09-07 10:02:00')`

// TestCrossStoreLive_OneIdentityMasksToOnePersonInBothStores is the lane's
// number, measured rather than argued.
func TestCrossStoreLive_OneIdentityMasksToOnePersonInBothStores(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ch := requireClickHouse(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, personTable)
	require.NoError(t, err)
	ch.exec(t, eventsForThosePeople, nil)
	ch.exec(t, insertThosePeople, nil)

	// One rules file, both stores. The properties rule is the one a person has
	// to write: the same blob is jsonb on one side and a String on the other,
	// and without it the classifier empties one as JSON and the other as text.
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Column: "distinct_id", Transform: "email", Link: "email",
			Why: "This product's distinct id is the person's address."},
		{Column: "properties", Type: "text", Transform: "empty_json",
			Why: "ClickHouse holds this JSON in a String."},
	})
	require.NoError(t, err)

	pgTables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	require.NotEmpty(t, pgTables)
	for _, tb := range pgTables {
		require.Equal(t, "postgres", tb.Engine,
			"a table read from Postgres does not say so, so the empty compatibility case "+
				"in DialectFor covers something a catalog reader produced")
	}
	chTables := ch.catalog(t)
	require.NotEmpty(t, chTables)

	// The number.
	key := testKey(t)
	report, err := masking.CrossStoreCheck(key, []masking.StoreAssignments{
		{Store: "primary", Assignments: rules.Assign(pgTables)},
		{Store: "events", Assignments: rules.Assign(chTables)},
	}, nil)
	require.NoError(t, err)
	t.Log("\n" + report.Summary())
	require.True(t, report.OK(), "the two stores do not agree:\n%s", report.Summary())
	require.Equal(t, 100.0, report.Percent())

	// And then the same claim one level down, from the rows themselves. The
	// report compares what the transforms WOULD do; this masks both stores and
	// reads the values back out of two different servers.
	pgPlan := masking.BuildPlan(pgTables, rules.Assign(pgTables), "live")
	require.True(t, pgPlan.Runnable(), masking.DescribeProblems(pgPlan.Problems))
	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: key})
	require.NoError(t, err)
	_, err = exec.Apply(ctx, conn, pgPlan)
	require.NoError(t, err)

	chPlan := masking.BuildPlan(chTables, rules.Assign(chTables), "live")
	require.True(t, chPlan.Runnable(), masking.DescribeProblems(chPlan.Problems))
	maskClickHouse(t, ch, chPlan, key)

	pgEmails := postgresEmails(t, conn)
	chEmails := clickhouseEmails(t, ch)
	require.Len(t, pgEmails, 3)
	require.Equal(t, pgEmails, chEmails,
		"the same three people masked into six; a join between the two stores now "+
			"returns nothing and every report built on it is confidently wrong")

	for _, address := range []string{
		"ada@lovelace-analytics.co.uk", "grace@hopper-systems.io", "alan@turing-labs.net",
	} {
		require.NotContains(t, pgEmails, address, "an address survived masking")
	}

	// Both stores read back with the same detectors, and both clean. The
	// guarantee does not move: a golden that fails here is never published.
	pgReport, err := verify.Scan(ctx, conn, verify.Options{SampleSize: 200})
	require.NoError(t, err)
	require.Empty(t, pgReport.Findings, "%v", pgReport.Findings)
	chReport, err := verify.ScanSource(ctx, ch.source(), verify.Options{SampleSize: 200})
	require.NoError(t, err)
	require.Empty(t, chReport.Findings, "%v", chReport.Findings)
	require.Equal(t, "postgres", pgReport.Engine)
	require.Equal(t, "clickhouse", chReport.Engine)
}

// maskClickHouse runs the plan the dialect compiled, one row at a time, exactly
// as the executor does against Postgres: read a chunk, compute in Go, write the
// values back as parameters.
func maskClickHouse(t *testing.T, ch *chServer, plan masking.Plan, key *masking.Key) {
	t.Helper()
	d, err := masking.DialectFor("clickhouse")
	require.NoError(t, err)
	for _, tp := range plan.Tables {
		read := d.SelectChunk(tp, "")
		rows, rowsErr := ch.rows(context.Background(), read.SQL)
		require.NoError(t, rowsErr, read.SQL)
		stmt := tp.Compile()
		for _, row := range rows {
			params := map[string]string{"p1": string(row[0])}
			for i, c := range tp.Columns {
				transform, ok := masking.Lookup(c.Transform)
				require.True(t, ok, c.Transform)
				in := string(row[1+i])
				out, applyErr := transform.Apply(key, masking.Column{
					Schema: tp.Table.Schema, Table: tp.Table.Name,
					Name: c.Column.Name, Link: c.Link,
				}, &in)
				require.NoError(t, applyErr)
				require.NotNil(t, out, "this fixture holds no nulls")
				params[fmt.Sprintf("p%d", i+2)] = *out
			}
			ch.exec(t, stmt.SQL, params)
		}
	}
}

// postgresEmails reads the masked addresses back out of Postgres.
//
// Ordered by distinct_id, which after masking is the same value as email: both
// carry the link email, so both derive the same subkey and one address masks to
// one address whichever column it was in. That is the within store half of the
// same guarantee, and it is what makes the two lists comparable row by row.
func postgresEmails(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		"SELECT email FROM person ORDER BY distinct_id")
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var email string
		require.NoError(t, rows.Scan(&email))
		out = append(out, email)
	}
	require.NoError(t, rows.Err())
	return out
}

// clickhouseEmails reads the same addresses out of the other server.
func clickhouseEmails(t *testing.T, ch *chServer) []string {
	t.Helper()
	rows, err := ch.rows(context.Background(),
		"SELECT assumeNotNull(email) FROM events ORDER BY distinct_id")
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		out = append(out, string(r[0]))
	}
	return out
}

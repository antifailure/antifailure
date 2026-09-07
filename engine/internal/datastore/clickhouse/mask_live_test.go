package clickhouse_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// TestMaskLive_TheEventsAreMaskedAndTheScanIsClean is the claim this lane
// exists to make, on a real server.
//
// A twin of an analytics product held a masked Postgres and zero events. This
// is the other half arriving: the events are here, every address in them has
// been replaced by a deterministic fake, and the same scanner that publishes
// the Postgres golden reads this one back and finds nothing.
func TestMaskLive_TheEventsAreMaskedAndTheScanIsClean(t *testing.T) {
	server := requireServer(t)
	s := newScratch(t, server, "mask")
	s.exec(eventsSchema)
	s.exec(insertEvents)
	ctx := context.Background()

	before := s.query("SELECT toString(count()), toString(uniqExact(_partition_id)) FROM events")
	require.Equal(t, [][]string{{"3", "2"}}, before)

	res, err := clickhouse.Mask(ctx, s.url, clickhouse.MaskOptions{
		Key: testKey(t), Rules: liveRules(t), RulesHash: "live",
		Progress: func(line string) { t.Log(line) },
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Tables)
	require.Equal(t, int64(3), res.Rows)

	// Nothing was lost and nothing was multiplied, and the partitions the
	// table was written into are still the partitions it has. A rewrite that
	// collapsed a partitioned table into one part would still hold every row
	// and would no longer be the table production has.
	after := s.query("SELECT toString(count()), toString(uniqExact(_partition_id)) FROM events")
	require.Equal(t, before, after)

	// Every address is gone, in both the column that holds one and the column
	// this product uses as an identifier.
	for _, address := range []string{
		"ada@lovelace-analytics.co.uk", "grace@hopper-systems.io", "alan@turing-labs.net",
	} {
		found := s.column(fmt.Sprintf(
			"SELECT toString(count()) FROM events WHERE distinct_id = '%s' OR email = '%s'",
			address, address))
		require.Equal(t, []string{"0"}, found, "%s survived masking", address)
	}

	// The values were REPLACED rather than emptied, and three people are still
	// three people. Asserting only that the real address is gone would pass
	// against a rewrite that wiped the column, which is a thing this shape can
	// do: a value map whose join key does not match any row writes an empty
	// string over every value. checkEveryValueMatched is what refuses that,
	// and this is the assertion that would notice if it ever stopped.
	masked := s.column("SELECT distinct_id FROM events ORDER BY uuid")
	require.Len(t, masked, 3)
	for _, address := range masked {
		require.Contains(t, address, "@",
			"a masked identifier is not an address, so the column was emptied rather than "+
				"masked and every query that joins on it now matches everything")
	}
	require.Len(t, uniqueOf(masked), 3,
		"three people masked into fewer, so the twin has lost the shape production has")

	// One address masks to one address wherever it appears. distinct_id and
	// email carry the same link, so the row that held the same value in both
	// still holds one value in both, which is what keeps a join across the two
	// stores returning one person.
	pairs := s.query("SELECT distinct_id, ifNull(email, 'NULL') FROM events ORDER BY uuid")
	require.Len(t, pairs, 3)
	require.Equal(t, pairs[0][0], pairs[0][1],
		"one identity masked into two people inside a single row")
	require.Equal(t, pairs[1][0], pairs[1][1])

	// A null is still a null. The transform never saw it, because a value that
	// is absent is not a value to mask, and a rewrite that turned it into an
	// empty string would have invented a fact about a person.
	require.Equal(t, "NULL", pairs[2][1],
		"an absent address came back present, so the twin holds a fact production does not")
	require.Equal(t, []string{"1"},
		s.column("SELECT toString(count()) FROM events WHERE ip IS NULL"))

	// The LowCardinality column is untouched and still LowCardinality. An
	// events table with no event names in it is not a twin of anything, and
	// the type is what a chart groups by.
	require.Equal(t, []string{"click", "pageview"},
		s.column("SELECT DISTINCT event FROM events ORDER BY event"))
	require.Equal(t, []string{"LowCardinality(String)"}, s.column(
		"SELECT type FROM system.columns WHERE database = currentDatabase() "+
			"AND table = 'events' AND name = 'event'"))

	// And the scan the golden is published on, reading the same store back
	// with the same detectors.
	report, err := clickhouse.Scan(ctx, s.url, clickhouse.ScanOptions{
		SampleSize: 200, Unruled: res.CopiedUnchanged,
	})
	require.NoError(t, err)
	require.Empty(t, report.Findings, "%v", report.Findings)
	require.Empty(t, report.Skipped, "%v", report.Skipped)
	require.Equal(t, "clickhouse", report.Engine)
	require.Positive(t, report.Columns)
	t.Logf("events in the twin: %d rows, %d distinct values masked, scan clean over %d columns",
		res.Rows, res.Values, report.Columns)
}

// uniqueOf returns the distinct values of a slice.
func uniqueOf(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// TestMaskLive_TheScanReadsOnlyTheDatabaseItWasGiven is the reason this
// package has a scoped dialect at all.
//
// The scanner's own ClickHouse statement lists every database on the server
// that is not the server's, because until this lane nothing here opened a
// ClickHouse connection and there was no reason for it to be narrower. A
// golden lives on a server beside other goldens, other branches and whatever
// else is on the machine, so an unscoped scan would read somebody else's data,
// attest to it, and refuse to publish a perfectly good golden over a finding
// in a store it does not own.
func TestMaskLive_TheScanReadsOnlyTheDatabaseItWasGiven(t *testing.T) {
	server := requireServer(t)
	mine := newScratch(t, server, "scoped")
	mine.exec("CREATE TABLE clean (id UInt64) ENGINE = MergeTree ORDER BY id")
	mine.exec("INSERT INTO clean VALUES (1)")

	// A second database on the same server, holding exactly what a scan is
	// supposed to refuse to publish over.
	theirs := newScratch(t, server, "elsewhere")
	theirs.exec("CREATE TABLE customers (id UInt64, email String) ENGINE = MergeTree ORDER BY id")
	theirs.exec("INSERT INTO customers VALUES (1, 'ada@lovelace-analytics.co.uk')")

	report, err := clickhouse.Scan(context.Background(), mine.url, clickhouse.ScanOptions{
		SampleSize: 200,
	})
	require.NoError(t, err)
	require.Empty(t, report.Findings,
		"the scan of one database found something in another one: %v", report.Findings)

	// And the control: the same scan pointed at the other database does find
	// it, so the empty result above is scoping rather than a scanner that
	// never looks.
	other, err := clickhouse.Scan(context.Background(), theirs.url, clickhouse.ScanOptions{
		SampleSize: 200,
	})
	require.NoError(t, err)
	require.NotEmpty(t, other.Findings,
		"the scanner found nothing in a table holding a real address, so the test above "+
			"proves nothing about scoping")
}

// TestMaskLive_TheBulkShapeAgreesWithTheStatementsTheDialectEmits is the check
// on the optimisation.
//
// The provider does not mask with the dialect's per row UPDATE, because that
// statement is a ClickHouse MUTATION and one per row is quadratic: 1.81 rows a
// second, measured on this machine against a ten thousand row table. What it
// does instead is a value map joined into a shadow table, which is correct
// only if masking is a pure function of the value. It is, and this is that
// argument being checked rather than repeated: the same fixture masked both
// ways, row for row identical.
//
// Two things about the fixture are load bearing and both are limits of the per
// row shape rather than of this one.
//
// The sorting key is a uuid, which is unique. A ClickHouse sorting key usually
// is not: an events table ordered by (team_id, toDate(timestamp), event) has
// millions of rows per value of its first column, and the dialect's statement
// addresses rows by exactly that. On such a table the two shapes would not
// agree, and the per row one would be the wrong one, because it would write
// one row's masked value over every row that shares its key.
//
// And it holds no nulls, because a ClickHouse query parameter cannot express
// one, so the per row statement cannot write a null back at all.
func TestMaskLive_TheBulkShapeAgreesWithTheStatementsTheDialectEmits(t *testing.T) {
	server := requireServer(t)
	ctx := context.Background()

	bulk := newScratch(t, server, "bulk")
	bulk.exec(eventsSchema)
	bulk.exec(insertEventsWithoutNulls)
	perRow := newScratch(t, server, "perrow")
	perRow.exec(eventsSchema)
	perRow.exec(insertEventsWithoutNulls)

	key := testKey(t)
	rules := liveRules(t)
	_, err := clickhouse.Mask(ctx, bulk.url, clickhouse.MaskOptions{
		Key: key, Rules: rules, RulesHash: "live",
	})
	require.NoError(t, err)

	maskOneRowAtATime(t, perRow, key, rules)

	const readBack = "SELECT toString(uuid), event, distinct_id, ifNull(email, 'NULL'), " +
		"properties, ifNull(ip, 'NULL') FROM events ORDER BY uuid"
	require.Equal(t, perRow.query(readBack), bulk.query(readBack),
		"the shape the provider uses and the shape the dialect describes disagree, so one "+
			"of them is masking something differently from the other store")
}

// maskOneRowAtATime runs the plan the dialect compiled, exactly as the
// Postgres executor does: read a chunk, compute in Go, write the values back
// as parameters.
func maskOneRowAtATime(t *testing.T, s *scratch, key *masking.Key, rules *masking.RuleSet) {
	t.Helper()
	d, err := masking.DialectFor("clickhouse")
	require.NoError(t, err)

	tables := catalogFor(t, s)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "live")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	require.NotEmpty(t, plan.Tables)

	for _, tp := range plan.Tables {
		read := d.SelectChunk(tp, "")
		rows := s.query(read.SQL)
		stmt := tp.Compile()
		for _, row := range rows {
			params := map[string]string{"p1": row[0]}
			for i, c := range tp.Columns {
				transform, ok := masking.Lookup(c.Transform)
				require.True(t, ok, c.Transform)
				in := row[1+i]
				out, applyErr := transform.Apply(key, masking.Column{
					Schema: tp.Table.Schema, Table: tp.Table.Name,
					Name: c.Column.Name, Link: c.Link,
				}, &in)
				require.NoError(t, applyErr)
				require.NotNil(t, out, "this fixture holds no nulls in a masked column")
				params[fmt.Sprintf("p%d", i+2)] = *out
			}
			settings := map[string]string{}
			for k, v := range params {
				settings["param_"+k] = v
			}
			_, err := request(context.Background(), s.url, stmt.SQL, settings)
			require.NoError(t, err, stmt.SQL)
		}
	}
}

// catalogFor reads the tables of a scratch database the way the package does,
// through its own catalog rather than the package's, so the comparison above
// is between two independently assembled plans.
func catalogFor(t *testing.T, s *scratch) []masking.Table {
	t.Helper()
	keys := map[string][]string{}
	for _, r := range s.query(
		"SELECT name, sorting_key FROM system.tables WHERE database = currentDatabase()") {
		var cols []string
		for _, part := range strings.Split(r[1], ",") {
			if part = strings.TrimSpace(part); part != "" {
				cols = append(cols, part)
			}
		}
		keys[r[0]] = cols
	}
	byName := map[string]*masking.Table{}
	var order []string
	for _, r := range s.query(
		"SELECT table, name, type FROM system.columns WHERE database = currentDatabase() " +
			"ORDER BY table, position") {
		if _, ok := keys[r[0]]; !ok {
			continue
		}
		if _, ok := byName[r[0]]; !ok {
			byName[r[0]] = &masking.Table{
				Engine: "clickhouse", Schema: s.name, Name: r[0], PrimaryKey: keys[r[0]],
			}
			order = append(order, r[0])
		}
		byName[r[0]].Columns = append(byName[r[0]].Columns, masking.ColumnInfo{
			Name: r[1], Type: r[2], Nullable: strings.Contains(r[2], "Nullable("),
		})
	}
	out := make([]masking.Table, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

// TestMaskLive_RefusesAColumnWithMoreDistinctValuesThanItWillHold is the
// negative control for the one bound this shape has.
//
// The map of a column's distinct values is held in this process, so a column
// with a hundred million distinct values is a column this cannot mask. What it
// must not do is fill memory until the machine kills it, because an out of
// memory kill says nothing about which column caused it.
func TestMaskLive_RefusesAColumnWithMoreDistinctValuesThanItWillHold(t *testing.T) {
	server := requireServer(t)
	s := newScratch(t, server, "toomany")
	s.exec(eventsSchema)
	s.exec(insertEvents)

	_, err := clickhouse.Mask(context.Background(), s.url, clickhouse.MaskOptions{
		Key: testKey(t), Rules: liveRules(t), RulesHash: "live", MaxDistinct: 1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "distinct values")

	// And the table is exactly as it was. A refusal that had already rewritten
	// half a table would be worse than no refusal.
	require.Equal(t, []string{"1"}, s.column(
		"SELECT toString(count()) FROM events WHERE distinct_id = 'ada@lovelace-analytics.co.uk'"))
}

// TestMaskLive_RefusesToEmptyAColumnThatCannotHoldNothing is the other
// refusal, and it is the one the Postgres path gets from the server.
//
// A nullify rule on a column the schema declares NOT NULL cannot be carried
// out. Postgres answers that with a not null violation halfway through the
// update; ClickHouse would put an empty string in and call it masked, so the
// refusal has to happen here, before anything is written.
func TestMaskLive_RefusesToEmptyAColumnThatCannotHoldNothing(t *testing.T) {
	server := requireServer(t)
	s := newScratch(t, server, "notnull")
	s.exec(eventsSchema)
	s.exec(insertEvents)

	rules, err := masking.NewRuleSet([]masking.Rule{
		{Column: "distinct_id", Transform: "nullify", Why: "the test asks for the impossible"},
		{Column: "properties", Type: "text", Transform: "empty_json", Why: "as elsewhere"},
		{Column: "event", Transform: "preserve", Why: "as elsewhere"},
	})
	require.NoError(t, err)

	_, err = clickhouse.Mask(context.Background(), s.url, clickhouse.MaskOptions{
		Key: testKey(t), Rules: rules, RulesHash: "live",
	})
	// Refused by the PLANNER, before this package reads a single value, which
	// is the earliest of the three places that could have caught it and the
	// only one that catches it for both stores at once.
	require.Error(t, err)
	require.Contains(t, err.Error(), "not nullable")
	require.Contains(t, err.Error(), "AF-MSK-010")
	require.Equal(t, []string{"1"}, s.column(
		"SELECT toString(count()) FROM events WHERE distinct_id = 'ada@lovelace-analytics.co.uk'"))
}

// TestEngineNameIsTheSameInAllThreePlaces is the check that keeps a dialect
// from being registered under a name nothing looks up.
//
// The masking dialect, the verification dialect and this provider each name
// the engine, and they are in three packages that do not import each other's
// constants on purpose. A dialect registered under a name the provider does
// not use is one that never runs, and the column it would have classified is
// one nobody decided about.
func TestEngineNameIsTheSameInAllThreePlaces(t *testing.T) {
	t.Parallel()
	p, err := clickhouse.New(clickhouse.Options{
		ServerURL: mustURL("http://user:pass@127.0.0.1:8123/default"),
	})
	require.NoError(t, err)
	engine := p.Capabilities().Engine

	_, err = masking.DialectFor(engine)
	require.NoError(t, err, "the masking package has no dialect for %q", engine)
	require.Equal(t, engine, verify.ClickHouse.Engine())
}

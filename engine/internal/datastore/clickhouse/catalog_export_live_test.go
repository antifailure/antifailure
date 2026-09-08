package clickhouse_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Catalog, the exported reader, which exists so that something outside this
// package can ask what a store's schema WOULD be masked into without opening a
// connection of its own.
//
// The cross store check is that caller. It compares what two stores' rules do
// to the same identifier, which needs both catalogs and no rows, and before
// this it would have had to carry a second ClickHouse client and a second
// opinion about what a type name means. Two readers kept in step by nobody is
// how a check ends up comparing a plan that will never run.

// TestCatalogReadsTheDatabaseTheURLNames is the scoping, and it is the
// assertion that matters most.
//
// A ClickHouse server holds the goldens of other refreshes and the branches of
// other environments beside the one being asked about. A reader that walked the
// server would fold those into the answer, and the failure would not look like
// a failure: it would be a bigger number computed over the wrong stores.
func TestCatalogReadsTheDatabaseTheURLNames(t *testing.T) {
	server := requireServer(t)
	mine := newScratch(t, server, "catalog_mine")
	theirs := newScratch(t, server, "catalog_theirs")

	mine.exec(`CREATE TABLE person (
  id UInt64, email String, properties String
) ENGINE = MergeTree ORDER BY id`)
	theirs.exec(`CREATE TABLE somebody_elses_branch (
  id UInt64, email String
) ENGINE = MergeTree ORDER BY id`)

	tables, skipped, err := clickhouse.Catalog(context.Background(), mine.url)
	require.NoError(t, err)
	require.Empty(t, skipped)

	names := make([]string, 0, len(tables))
	for _, tb := range tables {
		names = append(names, tb.Name)
		require.Equal(t, mine.name, tb.Schema,
			"a table came back under a database this URL does not name")
	}
	require.Equal(t, []string{"person"}, names,
		"the reader walked the server, so the number a caller computes from this "+
			"covers somebody else's branch as well as the store it was asked about")
}

// The shape the masking planner needs, read from a real server rather than
// asserted from the type names.
func TestCatalogReadsTheShapeTheMaskingPlannerUses(t *testing.T) {
	server := requireServer(t)
	s := newScratch(t, server, "catalog_shape")
	s.exec(`CREATE TABLE events (
  uuid UUID,
  distinct_id String,
  email Nullable(String),
  country LowCardinality(Nullable(String)),
  day Date MATERIALIZED toDate(ts),
  ts DateTime64(6)
) ENGINE = MergeTree ORDER BY (uuid, toDate(ts))`)

	tables, _, err := clickhouse.Catalog(context.Background(), s.url)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	tb := tables[0]

	require.Equal(t, "clickhouse", tb.Engine,
		"a table that does not say which engine it came from is classified against the "+
			"Postgres vocabulary, so String matches no rule and the column is copied unchanged")
	require.True(t, tb.ColumnNamed("email").Nullable)
	require.True(t, tb.ColumnNamed("country").Nullable,
		"nullability under a LowCardinality is still nullability")
	require.False(t, tb.ColumnNamed("distinct_id").Nullable)
	require.Equal(t, "", tb.ColumnNamed("day").Name,
		"the server computes a MATERIALIZED column, so no statement may write it and it "+
			"is not a column masking decides about")
	require.Equal(t, []string{"uuid"}, tb.PrimaryKey,
		"toDate(ts) is an expression rather than a column, so a statement addressing a "+
			"row through it would name something that is not there")

	// And the whole point of reading it: the planner can plan against it.
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	for _, a := range rules.Assign(tables) {
		require.Empty(t, a.Problem, "%s.%s: %s", a.Table, a.Column.Name, a.Problem)
	}
}

// A table the reader leaves out is NAMED with the reason.
//
// A view has no rows of its own, so it is correctly not part of a golden and
// correctly not a store whose masking anybody compares. Dropping it silently
// would make every number computed from this a true answer to a smaller
// question, with nothing saying which question.
func TestCatalogNamesWhatItDidNotReturn(t *testing.T) {
	server := requireServer(t)
	s := newScratch(t, server, "catalog_skips")
	s.exec(`CREATE TABLE person (id UInt64, email String) ENGINE = MergeTree ORDER BY id`)
	s.exec(`CREATE VIEW person_emails AS SELECT email FROM person`)

	tables, skipped, err := clickhouse.Catalog(context.Background(), s.url)
	require.NoError(t, err)

	names := make([]string, 0, len(tables))
	for _, tb := range tables {
		names = append(names, tb.Name)
	}
	require.Equal(t, []string{"person"}, names)
	require.Len(t, skipped, 1, "the view was dropped without a word")
	require.Contains(t, skipped[0], "person_emails")
}

// An empty connection string is refused rather than dialled, and no message
// anywhere quotes the URL, because the URL carries the password.
func TestCatalogRefusesAnEmptyConnectionString(t *testing.T) {
	t.Parallel()
	_, _, err := clickhouse.Catalog(context.Background(), secrets.Value{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

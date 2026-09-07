package masking_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

// The lane's number, produced by a harness in this repository so that anybody
// can run it against their own two stores and get their own.
//
// It is the one number in the table that can only be quoted when it is perfect,
// which is what makes quoting it worth something: a join key that masks to two
// different values is a broken join and a leak, so anything below 100 percent
// is a bug rather than a score.
//
// The BEFORE figure is not a simulation. The classifier decides on a canonical
// type, and before this existed it compared a rule's `type:` against whatever
// name the catalog reported. Labelling the ClickHouse catalog as Postgres is
// exactly that: no translation happens, the raw ClickHouse names are matched
// against Postgres type names, and the result is the code that used to run.

// The two schemas the number is measured on.
//
// Shaped like the product this wave exists for, and carrying the three ways a
// join key diverges when the classifier speaks one engine's vocabulary. `name`
// is matched by a built in rule that names a TYPE, `session_note` is matched by
// no rule at all and is classified from its type, and `properties` is the same
// blob under two type names. Nothing is inserted: this measures what the
// classifier decides, which is a property of the schema.
const benchmarkPerson = `
CREATE TABLE bench_person (
  id           bigserial PRIMARY KEY,
  distinct_id  text NOT NULL UNIQUE,
  email        text NOT NULL,
  name         text NOT NULL,
  session_note text,
  properties   jsonb
);`

const benchmarkEvents = `
CREATE TABLE bench_events (
  uuid         UUID,
  distinct_id  String,
  email        Nullable(String),
  name         String,
  session_note Nullable(String),
  properties   String,
  ts           DateTime64(6)
) ENGINE = MergeTree ORDER BY uuid`

func TestBenchmarkCrossStoreJoinKeys(t *testing.T) {
	if os.Getenv("AF_BENCHMARK") == "" {
		t.Skip("skipped: set AF_BENCHMARK=1, or run just benchmark")
	}
	conn, done := requireDatabase(t)
	defer done()
	ch := requireClickHouse(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, benchmarkPerson)
	require.NoError(t, err)
	ch.exec(t, benchmarkEvents, nil)

	rules, err := masking.NewRuleSet([]masking.Rule{
		{Column: "distinct_id", Transform: "email", Link: "email",
			Why: "This product's distinct id is the person's address."},
		{Column: "properties", Type: "text", Transform: "empty_json",
			Why: "ClickHouse holds this JSON in a String."},
	})
	require.NoError(t, err)

	pgTables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	chTables := ch.catalog(t)

	after, err := masking.CrossStoreCheck(testKey(t), []masking.StoreAssignments{
		{Store: "primary", Assignments: rules.Assign(pgTables)},
		{Store: "events", Assignments: rules.Assign(chTables)},
	}, nil)
	require.NoError(t, err)

	// The same catalogs, with the second store's engine unset, which is the
	// code that ran before this change: every type name compared as though it
	// were already a Postgres one.
	untranslated := make([]masking.Table, 0, len(chTables))
	for _, tb := range chTables {
		tb.Engine = "postgres"
		untranslated = append(untranslated, tb)
	}
	before, err := masking.CrossStoreCheck(testKey(t), []masking.StoreAssignments{
		{Store: "primary", Assignments: rules.Assign(pgTables)},
		{Store: "events", Assignments: rules.Assign(untranslated)},
	}, nil)
	require.NoError(t, err)

	report := crossStoreReportFile(t, ch, before, after)
	t.Log("\n" + report)

	out := os.Getenv("AF_BENCHMARK_OUT")
	if out == "" {
		out = filepath.Join("..", "..", "..", "benchmarks",
			time.Now().UTC().Format("2006-01-02-1504")+"-crossstore.md")
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, os.WriteFile(out, []byte(report), 0o644))
	t.Logf("wrote %s", out)

	// The run is also the check. A benchmark that recorded a number nobody
	// looked at would be a report, and this one refuses.
	require.True(t, after.OK(), "the number this run produced is a bug:\n%s", after.Summary())
	require.Less(t, before.Percent(), after.Percent(),
		"the before figure is not worse than the after figure, so either the fixture no "+
			"longer reaches the defect or the defect was never there")
}

func crossStoreReportFile(
	t *testing.T, ch *chServer, before, after masking.CrossStoreReport,
) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# Cross store join keys, before and after the dialect boundary\n\n")
	fmt.Fprintf(&b, "Run on %s.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Machine: %s %s, %d cores.\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Fprintf(&b, "- Stores: a Postgres holding people and a %s holding the events about them.\n",
		serverVersion(t, ch))
	fmt.Fprintf(&b, "- Harness: `just benchmark`, which is "+
		"`engine/internal/masking/benchmark_test.go` in this repository.\n")
	fmt.Fprintf(&b, "- Rules: one `masking.yaml` for both stores, two rules and the built in "+
		"defaults under them.\n\n")

	fmt.Fprintf(&b, "| | Candidate join keys | Verified identical | Share |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| Before | %d | %d | %.1f%% |\n",
		before.Checked, before.Identical, before.Percent())
	fmt.Fprintf(&b, "| After | %d | %d | %.1f%% |\n\n",
		after.Checked, after.Identical, after.Percent())

	fmt.Fprintf(&b, "A candidate join key is a column name that appears in both stores, or two "+
		"columns a rule gave the same link, where at least one side is masked. Anything below "+
		"100 percent is a bug rather than a score: a join key that masks to two different "+
		"values is a broken join, and a join key masked in one store and copied in the other "+
		"is also a leak.\n\n")

	if mismatches := before.Mismatches(); len(mismatches) > 0 {
		fmt.Fprintf(&b, "What the before figure was, column by column:\n\n")
		for _, p := range mismatches {
			fmt.Fprintf(&b, "- `%s` and `%s`: %s\n", p.A, p.B, p.Reason)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "The before figure is not a simulation. The classifier decides on a "+
		"canonical type name, and before the dialect boundary it compared a rule's `type:` "+
		"against whatever the catalog reported. This run reproduces that by labelling the "+
		"ClickHouse catalog as Postgres, so no translation happens and the raw ClickHouse "+
		"type names are matched against Postgres ones, which is the code that used to run.\n\n")

	fmt.Fprintf(&b, "The after figure is the one to read as a pass, and only at 100 percent. "+
		"A report that found nothing to compare is not a pass either: this run requires the "+
		"candidate count to be positive.\n")
	return b.String()
}

// serverVersion names the second store, so a report says which build it is about.
func serverVersion(t *testing.T, ch *chServer) string {
	t.Helper()
	rows, err := ch.rows(context.Background(), "SELECT version()")
	if err != nil || len(rows) == 0 {
		return "ClickHouse"
	}
	return "ClickHouse " + string(rows[0][0])
}

// The load average is deliberately NOT recorded here, where the database
// provider benchmark next door does record it.
//
// That one measures times, and a time measured on a laptop under load is partly
// about the laptop. This measures a count of columns that agree, which does not
// move with load at all, and printing a figure that cannot affect the result
// would invite somebody to explain a mismatch with it.

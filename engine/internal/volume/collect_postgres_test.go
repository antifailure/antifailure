package volume_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/volume"
)

// The collector runs against a real Postgres or it does not run.
//
// Everything it produces is SQL against the catalogs, and a fake would agree
// with whatever the SQL said, including a query that counts a partition twice,
// misses a table in a second schema, or reads pg_stats.n_distinct as a count
// when the catalog stored it as a negative fraction. Those are the three
// things this file exists to catch and none of them is visible without a
// server.
//
// AF_TEST_VOLUME_DATABASE_URL overrides the address, and the default is the
// scratch server every other Postgres suite here uses. A machine with no
// server skips, which is why AF_REQUIRE_DATABASE exists: a skip prints nothing
// and the package reports ok having examined nothing.
const volumeTestDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

func volumeConn(t *testing.T) *pgx.Conn {
	t.Helper()
	url := volumeTestDatabaseURL
	if u := os.Getenv("AF_TEST_VOLUME_DATABASE_URL"); u != "" {
		url = u
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("no Postgres at %s: %v", url, err)
	}
	name := "af_volume_" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	if len(name) > 60 {
		name = name[:60]
	}
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	require.NoError(t, admin.Close(ctx))

	own := strings.Replace(url, "/antifailure", "/"+name, 1)
	conn, err := pgx.Connect(ctx, own)
	require.NoError(t, err)
	t.Cleanup(func() {
		c := context.WithoutCancel(ctx)
		_ = conn.Close(c)
		if a, err := pgx.Connect(c, url); err == nil {
			_, _ = a.Exec(c, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = a.Close(c)
		}
	})
	return conn
}

func find(t *testing.T, p volume.Profile, name string) volume.Table {
	t.Helper()
	tbl, ok := p.Find(name)
	require.Truef(t, ok, "the profile does not name %s: %+v", name, p.Tables)
	return tbl
}

// A partitioned table is reported once, holding what its partitions hold.
//
// Listing the partitions separately would report every one of them as a table
// the branch does not have on the first month production rolls over, and
// leaving them out of the parent would report a table holding four billion
// rows as holding none.
func TestCollect_APartitionedTableIsOneTableHoldingItsPartitionsRows(t *testing.T) {
	conn := volumeConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE TABLE events (id bigint, at date not null) PARTITION BY RANGE (at);
CREATE TABLE events_a PARTITION OF events FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE events_b PARTITION OF events FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');
INSERT INTO events SELECT g, DATE '2026-01-01' FROM generate_series(1, 900) g;
INSERT INTO events SELECT g, DATE '2026-02-01' FROM generate_series(1, 100) g;
ANALYZE;`)
	require.NoError(t, err)

	p, err := volume.Collect(ctx, conn, "the test database", time.Now())
	require.NoError(t, err)

	names := make([]string, 0, len(p.Tables))
	for _, tbl := range p.Tables {
		names = append(names, tbl.Name)
	}
	require.Equal(t, []string{"public.events"}, names,
		"the partitions were listed as tables of their own, or the parent was dropped")

	events := find(t, p, "public.events")
	require.EqualValues(t, 1000, events.Rows, "the parent reports what its partitions hold")
	require.Equal(t, 2, events.Partitions)
	require.EqualValues(t, 900, events.LargestPartitionRows)
	require.Greater(t, events.TableBytes, int64(0), "the parent reports its partitions' bytes")

	share, ok := events.Skew()
	require.True(t, ok)
	require.InDelta(t, 0.9, share, 0.01,
		"nine tenths of the rows are in one partition and the profile did not say so")
}

// pg_stats stores a negative n_distinct to mean a fraction of the row count,
// which is how it says "unique" without being re-estimated as the table grows.
// Minus one on a table of a thousand rows is a thousand distinct values, and
// reporting minus one would be a number somebody divides by.
func TestCollect_ANegativeNDistinctIsResolvedIntoACount(t *testing.T) {
	conn := volumeConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE TABLE users (id int primary key, org_id int);
CREATE TABLE orgs (id int primary key);
ALTER TABLE users ADD CONSTRAINT users_org FOREIGN KEY (org_id) REFERENCES orgs (id);
INSERT INTO orgs SELECT g FROM generate_series(1, 20) g;
INSERT INTO users SELECT g, 1 + (g % 20) FROM generate_series(1, 1000) g;
ANALYZE;`)
	require.NoError(t, err)

	p, err := volume.Collect(ctx, conn, "the test database", time.Now())
	require.NoError(t, err)

	users := find(t, p, "public.users")
	keys := map[string]volume.Key{}
	for _, k := range users.Keys {
		keys[k.Column] = k
	}
	require.Contains(t, keys, "id", "the primary key is not in the profile")
	require.Contains(t, keys, "org_id", "the foreign key is not in the profile")

	require.Empty(t, keys["id"].Reason)
	require.EqualValues(t, 1000, keys["id"].Distinct,
		"a unique column stored as minus one was not resolved against the row count")
	require.EqualValues(t, 20, keys["org_id"].Distinct)
}

// Two schemas may hold a table of the same name, and an unqualified profile
// would carry one of them twice and drop the other.
func TestCollect_TablesAreSchemaQualifiedAndTheCatalogsAreLeftOut(t *testing.T) {
	conn := volumeConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE SCHEMA billing;
CREATE TABLE orders (id int primary key);
CREATE TABLE billing.orders (id int primary key);
CREATE VIEW paid AS SELECT * FROM orders;
INSERT INTO orders SELECT g FROM generate_series(1, 300) g;
INSERT INTO billing.orders SELECT g FROM generate_series(1, 11) g;
ANALYZE;`)
	require.NoError(t, err)

	p, err := volume.Collect(ctx, conn, "the test database", time.Now())
	require.NoError(t, err)

	names := make([]string, 0, len(p.Tables))
	for _, tbl := range p.Tables {
		names = append(names, tbl.Name)
	}
	require.Equal(t, []string{"billing.orders", "public.orders"}, names,
		"a view was counted as a table, or a catalog leaked in, or one schema was read twice")
	require.EqualValues(t, 300, find(t, p, "public.orders").Rows)
	require.EqualValues(t, 11, find(t, p, "billing.orders").Rows)
	require.EqualValues(t, 311, p.Rows())
}

// A table nobody has analyzed has no row count, and zero is the wrong word for
// that. It is said out loud, because a profile carrying a silent zero is a
// denominator somebody divides by.
func TestCollect_ATableNobodyAnalyzedIsNamedRatherThanCountedAsEmpty(t *testing.T) {
	conn := volumeConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE TABLE fresh (id int primary key);
INSERT INTO fresh SELECT g FROM generate_series(1, 40) g;`)
	require.NoError(t, err)

	p, err := volume.Collect(ctx, conn, "the test database", time.Now())
	require.NoError(t, err)

	fresh := find(t, p, "public.fresh")
	require.False(t, fresh.Analyzed, "an unanalyzed table claimed a row count")
	require.Contains(t, strings.Join(p.Missing, "\n"), "public.fresh has never been analyzed")
}

func TestCollect_SizesAndTheServerVersionAreRecorded(t *testing.T) {
	conn := volumeConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE TABLE wide (id int primary key, body text);
INSERT INTO wide SELECT g, repeat('x', 200) FROM generate_series(1, 5000) g;
ANALYZE;`)
	require.NoError(t, err)

	p, err := volume.Collect(ctx, conn, "the test database", time.Now())
	require.NoError(t, err)
	require.Contains(t, p.ServerVersion, "PostgreSQL")
	require.Equal(t, "the test database", p.Source)

	wide := find(t, p, "public.wide")
	require.Greater(t, wide.TableBytes, int64(0))
	require.Greater(t, wide.IndexBytes, int64(0), "the primary key index was not measured")
}

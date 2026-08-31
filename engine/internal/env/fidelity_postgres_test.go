package env

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/personas"
)

// What the branch is asked, against a real Postgres.
//
// The row counts and the account lookup are the two things in the inventory
// that come from the branch rather than from a manifest or a provider, and both
// are SQL. A fake would agree with whatever the SQL said, including a query
// that counts the wrong relations or one that misses an address because it
// compares case sensitively, so these run against a server or they do not run.
//
// AF_TEST_FIDELITY_DATABASE_URL overrides the address, and the default is the
// scratch server this package's tests expect. A machine without one skips,
// which is why AF_REQUIRE_DATABASE exists: a skip prints nothing and the
// package reports ok having examined nothing.
const fidelityTestDatabaseURL = "postgres://postgres:test@127.0.0.1:55511/antifailure"

func fidelityConn(t *testing.T) *pgx.Conn {
	t.Helper()
	url := fidelityTestDatabaseURL
	if u := os.Getenv("AF_TEST_FIDELITY_DATABASE_URL"); u != "" {
		url = u
	}
	// Generous, because this runs on a machine where a dozen other containers
	// are competing for the daemon. A tight bound here fails as a missing
	// Postgres, which is the one diagnosis that sends somebody the wrong way.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("no Postgres for the branch queries: %v", err)
	}

	// Its own database, so one test's tables cannot be counted by another's.
	name := fmt.Sprintf("af_fid_%d", time.Now().UnixNano())
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

func TestBranchSize_CountsTheApplicationsTablesAndNotTheCatalogues(t *testing.T) {
	conn := fidelityConn(t)
	ctx := context.Background()

	tables, rows, atLeast, err := branchSize(ctx, conn)
	require.NoError(t, err)
	require.Zero(t, tables, "an empty database holds no application tables")
	require.Zero(t, rows)
	require.False(t, atLeast)

	// Two tables called orders, in two schemas, holding different numbers of
	// rows. An unqualified count of an unanalyzed table reads whichever the
	// search path finds, which is the same one twice.
	_, err = conn.Exec(ctx, `
CREATE TABLE customers (id int primary key, email text);
CREATE TABLE orders (id int primary key, customer_id int, total_cents int);
CREATE SCHEMA billing;
CREATE TABLE billing.orders (id int primary key);
CREATE VIEW paid AS SELECT * FROM orders;
INSERT INTO customers SELECT g, 'a' || g || '@example.test' FROM generate_series(1, 700) g;
INSERT INTO orders SELECT g, g, g * 10 FROM generate_series(1, 300) g;
INSERT INTO billing.orders SELECT g FROM generate_series(1, 11) g;
`)
	require.NoError(t, err)

	// Before ANALYZE the planner has never seen these tables and reports minus
	// one, which would render as a negative row count in a report. They are
	// counted instead, so the number is right rather than absurd.
	tables, rows, atLeast, err = branchSize(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, 3, tables, "a table in another schema counts and a view does not")
	require.EqualValues(t, 1011, rows,
		"the count read one schema's orders twice, or missed the other's")
	require.False(t, atLeast, "nothing here is near the ceiling")

	_, err = conn.Exec(ctx, "ANALYZE")
	require.NoError(t, err)
	tables, rows, atLeast, err = branchSize(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, 3, tables)
	require.EqualValues(t, 1011, rows, "the estimate and the count disagree on a table this size")
	require.False(t, atLeast)
}

// A live count that stopped at its ceiling is a floor, and the report has to
// say so rather than presenting it as a total somebody would quote.
func TestBranchSize_SaysWhenACountStoppedAtTheCeiling(t *testing.T) {
	conn := fidelityConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE wide (id int); INSERT INTO wide SELECT g FROM generate_series(1, %d) g",
		countCeiling+50))
	require.NoError(t, err)

	tables, rows, atLeast, err := branchSize(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, 1, tables)
	require.EqualValues(t, countCeiling, rows)
	require.True(t, atLeast, "the count stopped at the ceiling and did not say so")
}

func TestAccountExists_MatchesTheAddressTheAdapterWouldMatch(t *testing.T) {
	conn := fidelityConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE TABLE users (id int primary key, email text not null);
INSERT INTO users VALUES (1, 'Ada@Example.Test');
`)
	require.NoError(t, err)

	scheme := personas.Scheme{
		Name:  "generic",
		Users: personas.Table{Name: "users", ID: "id", Email: "email"},
	}

	// Case insensitively, because that is what the SQL adapter matches on when
	// it decides whether to reconcile or insert. Asking a different question
	// here would report an account missing that provisioning would have found.
	present, err := accountExists(ctx, conn, scheme, "ada@example.test")
	require.NoError(t, err)
	require.True(t, present)

	present, err = accountExists(ctx, conn, scheme, "nobody@example.test")
	require.NoError(t, err)
	require.False(t, present)
}

func TestAccountExists_ReadsAQualifiedTable(t *testing.T) {
	conn := fidelityConn(t)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE SCHEMA auth;
CREATE TABLE auth.users (id int primary key, email text not null);
INSERT INTO auth.users VALUES (1, 'ada@example.test');
CREATE TABLE users (id int primary key, email text not null);
`)
	require.NoError(t, err)

	// A schema qualified table is read from that schema and not from whatever
	// the search path found first, which here is a table holding nobody.
	scheme := personas.Scheme{
		Name:  "supabase",
		Users: personas.Table{Schema: "auth", Name: "users", ID: "id", Email: "email"},
	}
	present, err := accountExists(ctx, conn, scheme, "ada@example.test")
	require.NoError(t, err)
	require.True(t, present)
}

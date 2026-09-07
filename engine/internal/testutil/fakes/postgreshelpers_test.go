package fakes_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The same schema every conformance run works against, so a row count here
// means what a row count there means.
var seedSQL = conformance.DefaultSeedSQL

// defaultPostgresURL is the scratch server `just db` starts and the one CI
// starts, on 55432, which is where every other Postgres suite in this
// repository looks.
const defaultPostgresURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

func postgresURL() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return defaultPostgresURL
}

// The tests talk SQL to a branch through the connection string the provider
// hands out, for the reason conformance/sql.go gives: asking the provider what
// it thinks would test its own bookkeeping, and connecting tests the thing the
// application will experience.

func countUsers(t *testing.T, url string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to the branch: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM conformance_users").Scan(&n); err != nil {
		t.Fatalf("count rows in the branch: %v", err)
	}
	return n
}

func writeUser(t *testing.T, ctx context.Context, p provider.Database, b provider.Branch, email string) {
	t.Helper()
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	c, err := pgx.Connect(ctx, conn.Reveal())
	if err != nil {
		t.Fatalf("connect to the branch: %v", err)
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	if _, err := c.Exec(ctx, "INSERT INTO conformance_users (email) VALUES ($1)", email); err != nil {
		t.Fatalf("write to the branch: %v", err)
	}
}

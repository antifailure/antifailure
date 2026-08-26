package conformance

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib" //nolint:revive // registers the driver

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The suite talks SQL to a branch through the connection string the provider
// hands out.
//
// That is deliberate. Checking isolation by asking the provider what it
// thinks would test the provider's own bookkeeping; connecting and reading
// tests the thing the application will experience. It also means a provider
// cannot pass by returning plausible metadata about branches that do not work.

var _ = stdlib.GetDefaultDriver

func (h *harness) open(ctx context.Context, b provider.Branch) *sql.DB {
	h.t.Helper()
	conn, err := h.p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		h.t.Fatalf("ConnString: %v", err)
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		h.t.Fatalf("open the branch: %v", err)
	}
	db.SetMaxOpenConns(2)
	h.t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		h.t.Fatalf("connect to the branch: %v", err)
	}
	return db
}

func (h *harness) exec(ctx context.Context, b provider.Branch, query string) {
	h.t.Helper()
	if _, err := h.open(ctx, b).ExecContext(ctx, query); err != nil {
		h.t.Fatalf("exec %q: %v", query, err)
	}
}

func (h *harness) countUsers(ctx context.Context, b provider.Branch) int {
	h.t.Helper()
	var n int
	err := h.open(ctx, b).QueryRowContext(ctx, "SELECT count(*) FROM conformance_users").Scan(&n)
	if err != nil {
		h.t.Fatalf("count rows in the branch: %v", err)
	}
	return n
}

func (h *harness) countMatching(ctx context.Context, b provider.Branch, email string) int {
	h.t.Helper()
	var n int
	err := h.open(ctx, b).QueryRowContext(ctx,
		"SELECT count(*) FROM conformance_users WHERE email = $1", email).Scan(&n)
	if err != nil {
		h.t.Fatalf("count matching rows: %v", err)
	}
	return n
}

// SeedError wraps a failure to seed a golden candidate, so that a provider can
// report it with context rather than a bare driver message.
func SeedError(err error) error {
	return fmt.Errorf("conformance: seed the golden candidate: %w", err)
}

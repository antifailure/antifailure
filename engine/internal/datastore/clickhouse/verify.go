package clickhouse

import (
	"context"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The verification half, which is the same scanner with the same detectors
// reading a different store.
//
// internal/verify already carries the ClickHouse dialect: the statement that
// lists the columns, the statement that samples one, and the type table that
// decides which columns are worth reading. What it does not carry is anything
// that can open a connection, which is deliberate and is this package's half.

// ScanOptions configure a scan.
type ScanOptions struct {
	// SampleSize is rows per column, and zero uses the scanner's default.
	SampleSize int
	// Unruled names the columns masking copied unchanged because no rule
	// covered them. It is what decides whether an unreadable column with a
	// secret's name is a finding or a note.
	Unruled []string
	// Progress receives a line per table, and may be nil.
	Progress func(string)
	// Now is the time source.
	Now func() time.Time
}

// Scan reads a ClickHouse database back and reports what still looks real.
//
// Scoped to the database the URL names, which is the whole reason this is not
// three lines at the call site. A golden here is a database on a server that
// also holds the goldens of other refreshes and the branches of other
// environments, and the scanner's own statement lists every database on the
// server that is not the server's. Unscoped, a refresh would scan somebody
// else's branch, attest to it, and refuse to publish over a finding in a store
// it does not own.
func Scan(ctx context.Context, url secrets.Value, opts ScanOptions) (verify.Report, error) {
	c, err := parseURL(url)
	if err != nil {
		return verify.Report{}, err
	}
	return verify.ScanSource(ctx, source(c), verify.Options{
		SampleSize: opts.SampleSize,
		Unruled:    opts.Unruled,
		Progress:   opts.Progress,
		Now:        opts.Now,
	})
}

// source pairs the scoped dialect with this client.
func source(c *client) verify.Source {
	return verify.NewSource(scopedDialect{verify.ClickHouse},
		func(ctx context.Context, sql string, yield func(verify.Row) error) error {
			return c.rows(ctx, sql, nil, func(r [][]byte) error {
				return yield(verify.Row(r))
			})
		})
}

// scopedDialect is the ClickHouse dialect with its column listing limited to
// one database.
//
// The listing is composed from the dialect's own rather than rewritten, so
// which engines it excludes and which system databases it drops stay that
// package's answer and cannot drift from it. This adds one predicate.
type scopedDialect struct{ verify.Dialect }

func (scopedDialect) Columns() string {
	return "SELECT database, `table`, name, type FROM (" +
		verify.ClickHouse.Columns() +
		") WHERE database = currentDatabase() ORDER BY database, `table`, name"
}

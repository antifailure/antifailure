package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The verification half: the same scanner and the same detectors, reading a
// MySQL. internal/verify carries the dialect, and this supplies the
// connection.

// ScanOptions configure a scan.
type ScanOptions struct {
	// SampleSize is rows per column, and zero uses the scanner's default.
	SampleSize int
	// Unruled names the columns masking copied unchanged because no rule
	// covered them, as database.table.column under the database the scan
	// reads, which is what MaskResult.CopiedUnchanged carries. A name under any
	// other database is refused rather than ignored.
	Unruled []string
	// Progress receives a line per table, and may be nil.
	Progress func(string)
	// Now is the time source.
	Now func() time.Time
}

// Scan reads a MySQL database back and reports what still looks real.
//
// Scoped to the database the URL names, for the reason the ClickHouse scan is:
// the server also holds other goldens and other environments' branches, and
// the dialect's own listing covers every database on it.
func Scan(ctx context.Context, url secrets.Value, opts ScanOptions) (verify.Report, error) {
	s, err := parseURL(url)
	if err != nil {
		return verify.Report{}, err
	}
	if s.database == "" {
		return verify.Report{}, fmt.Errorf("datastore.mysql: the address names no database to scan")
	}
	if err := unruledUnder(s.database, opts.Unruled); err != nil {
		return verify.Report{}, err
	}
	db, err := s.open(sessionPlain)
	if err != nil {
		return verify.Report{}, err
	}
	defer func() { _ = db.Close() }()
	return verify.ScanSource(ctx, source(db), verify.Options{
		SampleSize: opts.SampleSize,
		Unruled:    opts.Unruled,
		Progress:   opts.Progress,
		Now:        opts.Now,
	})
}

// source pairs the scoped dialect with a pool.
func source(db *sql.DB) verify.Source {
	return verify.NewSource(scopedDialect{verify.MySQL},
		func(ctx context.Context, query string, yield func(verify.Row) error) error {
			rows, err := db.QueryContext(ctx, query)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			cols, err := rows.Columns()
			if err != nil {
				return err
			}
			raw := make([]sql.RawBytes, len(cols))
			dest := make([]any, len(cols))
			for i := range raw {
				dest[i] = &raw[i]
			}
			for rows.Next() {
				if err := rows.Scan(dest...); err != nil {
					return err
				}
				// RawBytes is nil for a NULL and the driver's buffer
				// otherwise, which is exactly a Row: valid until yield
				// returns.
				row := make(verify.Row, len(raw))
				for i, v := range raw {
					row[i] = v
				}
				if err := yield(row); err != nil {
					return err
				}
			}
			return rows.Err()
		})
}

// scopedDialect is the MySQL dialect with its column listing limited to the
// session's database. Composed from the dialect's own listing, so which tables
// it reads stays that package's answer.
type scopedDialect struct{ verify.Dialect }

func (scopedDialect) Columns() string {
	return "SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, COLUMN_TYPE FROM (" +
		verify.MySQL.Columns() +
		") AS listed WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, COLUMN_NAME"
}

// unruledUnder refuses an unruled column named under a database other than the
// one being scanned.
//
// The scanner decides whether a column a rule never covered is unruled by
// comparing names, and a name that matches nothing is silently a ruled column.
// That is exactly how a golden with an unread, unmasked secret in it was
// published: masking named the columns by the source database and the scan by
// the candidate. A name that cannot match is a caller that disagrees with the
// scan about what the database is called, and that is refused here, before a
// verdict built on it exists.
func unruledUnder(database string, unruled []string) error {
	for _, name := range unruled {
		rest, ok := strings.CutPrefix(name, database+".")
		if !ok || !strings.Contains(rest, ".") {
			return fmt.Errorf("datastore.mysql: the column %s, which masking copied unchanged, "+
				"is not named as database.table.column under %s, the database being scanned, "+
				"so the scan could not tell it was unruled", name, database)
		}
	}
	return nil
}

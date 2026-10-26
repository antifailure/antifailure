package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Moving a table, from production into a golden candidate and from a golden
// into a branch.
//
// The definition and the rows travel separately and over different sessions.
// The definition is SHOW CREATE TABLE, run as it was written, over an ordinary
// utf8mb4 session, because a default or a comment in it is text and a binary
// session would read a non-ASCII default meant for a latin1 column as the
// bytes of its UTF-8 spelling. The rows travel over a binary session, where
// they are the bytes each column stores, so nothing is transcoded on the way.

// maxParameters is MySQL's limit on the markers in one prepared statement.
const maxParameters = 65535

// Batch bounds. Rows per INSERT, and bytes per INSERT so that a table of
// documents does not build one statement past max_allowed_packet. Four
// megabytes is a sixteenth of MySQL 8's default packet.
const (
	batchRows  = 1000
	batchBytes = 4 << 20
)

// showCreate returns a table's own definition, unqualified, which is what lets
// it be run in another database: CREATE TABLE `t` creates t wherever the
// session is, and a foreign key to a table of the same database is written
// without the database too, so it follows.
func showCreate(ctx context.Context, q querier, name string) (string, error) {
	var ddl string
	err := each(ctx, q, "SHOW CREATE TABLE "+quoteIdent(name), nil, func(r *sql.Rows) error {
		var got string
		if err := r.Scan(&got, &ddl); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("datastore.mysql: reading the definition of %s: %w", name, err)
	}
	if !strings.HasPrefix(strings.TrimSpace(ddl), "CREATE TABLE") {
		return "", fmt.Errorf(
			"datastore.mysql: the definition of %s does not begin with CREATE TABLE, so it is "+
				"not a table this can copy", name)
	}
	return ddl, nil
}

// copyRows streams a table's rows from src into the same table in dst, which
// must already exist with the same definition.
//
// src is read with no parameters, which MySQL answers over its text protocol:
// every value arrives as the bytes the column stores or as its text form, and
// a NULL as nothing. Each is sent back as a parameter over a binary session,
// which the server assigns to the column as it would the same value written by
// hand.
func copyRows(ctx context.Context, src querier, dst *sql.DB, t table) (int64, error) {
	cols := t.writableColumns()
	names := make([]string, len(cols))
	markers := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quoteIdent(c.name)
		markers[i] = "?"
		if strings.HasPrefix(strings.ToLower(c.typ), "json") {
			// A JSON column refuses a string whose character set is binary,
			// which is every string a binary session sends. The value is the
			// UTF-8 text the server wrote it out as, so it is named as that.
			markers[i] = "CONVERT(? USING utf8mb4)"
		}
	}
	row := "(" + strings.Join(markers, ", ") + ")"
	head := fmt.Sprintf("INSERT INTO %s (%s) VALUES ", quoteIdent(t.name), strings.Join(names, ", "))

	perBatch := batchRows
	if limit := maxParameters / len(cols); limit < perBatch {
		perBatch = limit
	}

	rows, err := src.QueryContext(ctx,
		fmt.Sprintf("SELECT %s FROM %s", strings.Join(names, ", "), quoteIdent(t.name)))
	if err != nil {
		return 0, fmt.Errorf("datastore.mysql: reading %s: %w", t.name, err)
	}
	defer func() { _ = rows.Close() }()

	var (
		args    = make([]any, 0, perBatch*len(cols))
		pending int
		bytes   int
		copied  int64
	)
	flush := func() error {
		if pending == 0 {
			return nil
		}
		stmt := head + strings.TrimSuffix(strings.Repeat(row+", ", pending), ", ")
		if _, err := dst.ExecContext(ctx, stmt, args...); err != nil {
			return fmt.Errorf("datastore.mysql: writing %s: %w", t.name, err)
		}
		copied += int64(pending)
		args, pending, bytes = args[:0], 0, 0
		return nil
	}

	raw := make([]sql.RawBytes, len(cols))
	dest := make([]any, len(cols))
	for i := range raw {
		dest[i] = &raw[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return copied, fmt.Errorf("datastore.mysql: reading %s: %w", t.name, err)
		}
		for _, v := range raw {
			if v == nil {
				args = append(args, nil)
				continue
			}
			// Copied, because RawBytes is the driver's buffer and the next
			// row is read into it.
			args = append(args, append([]byte{}, v...))
			bytes += len(v)
		}
		pending++
		if pending >= perBatch || bytes >= batchBytes {
			if err := flush(); err != nil {
				return copied, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return copied, fmt.Errorf("datastore.mysql: reading %s: %w", t.name, err)
	}
	return copied, flush()
}

// copyWithin fills a table in one database from the same table in another on
// the same server, which is a branch being made from a golden. One statement,
// with the server moving the rows, over a session whose sql_mode accepts what
// the golden holds.
func copyWithin(ctx context.Context, db *sql.DB, from, to string, t table) error {
	cols := t.writableColumns()
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quoteIdent(c.name)
	}
	list := strings.Join(names, ", ")
	_, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s.%s (%s) SELECT %s FROM %s.%s",
		quoteIdent(to), quoteIdent(t.name), list, list, quoteIdent(from), quoteIdent(t.name)))
	if err != nil {
		return fmt.Errorf("datastore.mysql: filling %s from the golden: %w", t.name, err)
	}
	return nil
}

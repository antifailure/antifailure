package mysql

import (
	"context"
	"database/sql"
	"fmt"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The masking run.
//
// The Postgres executor's shape, because MySQL is the same kind of store: read
// a chunk in primary key order inside a transaction, compute each row's masked
// values in Go from the project key, and write each row back by its whole
// primary key before the transaction commits. The statements are the mysql
// dialect's, and the arguments are bound through masking.ArgsInTextOrder,
// because MySQL binds by position in the text and the executor numbers them by
// meaning.

// MaskOptions configure a run.
type MaskOptions struct {
	// Key is the project's masking key. It never reaches the server.
	Key *masking.Key
	// Rules classify the columns.
	Rules *masking.RuleSet
	// RulesHash identifies them, and is carried into the plan.
	RulesHash string
	// Schema is the name masking knows the database by, which is the source
	// database's name. Empty uses the name of the database the URL names. See
	// table.maskingTable for why the two differ.
	Schema string
	// Progress receives a line per table, and may be nil.
	Progress func(string)
}

// MaskResult is what a run did.
type MaskResult struct {
	// Rows is how many rows were rewritten.
	Rows int64
	// Tables is how many tables were rewritten.
	Tables int
	// Unclassified are the columns no rule covered.
	Unclassified []masking.Assignment
	// CopiedUnchanged names the columns that ship exactly as they were, as
	// database.table.column under the database the URL names, which is the
	// name the verification scan of that same database reports them by. NOT
	// the schema masking derived its keys from: see unruledNames.
	CopiedUnchanged []string
}

// Mask applies a masking plan to the MySQL database a URL names, which is a
// golden candidate: nothing here writes to a source. A plan with problems is
// refused rather than partly run.
func Mask(ctx context.Context, url secrets.Value, opts MaskOptions) (MaskResult, error) {
	var res MaskResult
	if opts.Key == nil {
		return res, fmt.Errorf("datastore.mysql: masking needs a key")
	}
	if opts.Rules == nil {
		return res, fmt.Errorf("datastore.mysql: masking needs a rule set")
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	s, err := parseURL(url)
	if err != nil {
		return res, err
	}
	if s.database == "" {
		return res, fmt.Errorf("datastore.mysql: the address names no database to mask")
	}
	schema := opts.Schema
	if schema == "" {
		schema = s.database
	}
	db, err := s.open(sessionMask)
	if err != nil {
		return res, err
	}
	defer func() { _ = db.Close() }()

	tables, skips, err := readCatalog(ctx, db, s.database)
	if err != nil {
		return res, err
	}
	for _, sk := range skips {
		progress(fmt.Sprintf("not masking %s: %s", sk.Name, sk.Reason))
	}
	mt := make([]masking.Table, 0, len(tables))
	for _, t := range tables {
		mt = append(mt, t.maskingTable(schema, s.database))
	}
	plan := masking.BuildPlan(mt, opts.Rules.Assign(mt), opts.RulesHash)
	res.Unclassified = plan.Unclassified
	res.CopiedUnchanged = unruledNames(plan, s.database)
	if !plan.Runnable() {
		return res, aferrors.Coded(aferrors.AFMSK010, "detail", masking.DescribeProblems(plan.Problems))
	}

	d, err := masking.DialectFor(engineName)
	if err != nil {
		return res, err
	}
	for _, tp := range plan.Tables {
		n, err := maskTable(ctx, db, d, s.database, tp, opts.Key, progress)
		res.Rows += n
		if err != nil {
			return res, err
		}
		res.Tables++
		progress(fmt.Sprintf("masked %s: %d rows", tp.Table.Name, n))
	}
	return res, nil
}

// unruledNames names the columns a plan copies unchanged under the database
// they physically sit in.
//
// The plan knows every table by the source's name, because that is what the
// keys are derived from, so plan.CopiedUnchangedNames says shop.users.api_key.
// The scan that reads the candidate back lists its columns by the database it
// is reading, af_cand_<hash>.users.api_key, and matches the two by string. They
// never matched, so the scan counted every column as one a rule had decided,
// and a secret it could not read, bytes that are not UTF-8 in a column named
// like a key, became a note rather than a refusal and the golden published.
// One name for one column is the fix, and it is the scan's, because the scan
// is the one reading the rows.
func unruledNames(plan masking.Plan, physical string) []string {
	cols := plan.CopiedUnchanged()
	out := make([]string, 0, len(cols))
	for _, a := range cols {
		out = append(out, physical+"."+a.Table.Name+"."+a.Column.Name)
	}
	return out
}

// rewritesKey reports whether the plan rewrites a column of the primary key.
func rewritesKey(tp masking.TablePlan) bool {
	key := map[string]bool{}
	for _, k := range tp.OrderBy {
		key[k] = true
	}
	for _, c := range tp.Columns {
		if key[c.Column.Name] {
			return true
		}
	}
	return false
}

// maskTable rewrites one table, chunk by chunk.
//
// tp is the table as masking knows it, under the source's schema name, and is
// what every column's key is derived from. The statements are compiled from a
// copy addressed to the database the rows are actually in.
//
// A table whose primary key is itself rewritten is read in one pass. The chunks
// resume after the last key masked, and uuid_remap, the transform that exists
// for keys, scatters them: a key masked in the first chunk lands anywhere in
// the order, including after the resume point, where the next chunk would read
// it again and remap the remapped value. Its foreign keys, masked once, would
// then point at nothing. Read whole, every row is read before any is written,
// at the cost of holding the table's masked columns in memory, which is the
// cost the Postgres executor already pays for a table with no key at all.
func maskTable(
	ctx context.Context, db *sql.DB, d masking.Dialect, physical string,
	tp masking.TablePlan, key *masking.Key, progress func(string),
) (int64, error) {
	if len(tp.Columns) == 0 {
		return 0, nil
	}
	if rewritesKey(tp) {
		tp.ChunkSize = 0
		progress(fmt.Sprintf("%s rewrites its own primary key, so it is read in one pass rather "+
			"than resumed by a key it is changing", tp.Table.Name))
	}
	addressed := tp
	addressed.Table.Schema = physical
	stmt := d.Update(addressed)

	var total int64
	var after []string
	for {
		n, last, err := maskChunk(ctx, db, d, tp, addressed, stmt.SQL, after, key)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 || addressed.ChunkSize == 0 {
			return total, nil
		}
		after = last
	}
}

// maskChunk reads one chunk and writes it back inside one transaction.
func maskChunk(
	ctx context.Context, db *sql.DB, d masking.Dialect, identity, addressed masking.TablePlan,
	update string, after []string, key *masking.Key,
) (int64, []string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("datastore.mysql: starting a transaction for %s: %w",
			identity.Table.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	q := d.SelectChunk(addressed, after)
	args, err := masking.ArgsInTextOrder(q.SQL, q.Args)
	if err != nil {
		return 0, nil, err
	}
	rows, err := tx.QueryContext(ctx, q.SQL, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("datastore.mysql: reading %s: %w", identity.Table.Name, err)
	}
	width := addressed.AddressWidth()
	type record struct {
		key    []string
		values []*string
	}
	var batch []record
	scanned := make([]sql.NullString, width+len(addressed.Columns))
	dest := make([]any, len(scanned))
	for i := range scanned {
		dest[i] = &scanned[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("datastore.mysql: reading %s: %w", identity.Table.Name, err)
		}
		r := record{key: make([]string, width), values: make([]*string, len(addressed.Columns))}
		for i := 0; i < width; i++ {
			r.key[i] = scanned[i].String
		}
		for i := range addressed.Columns {
			if v := scanned[width+i]; v.Valid {
				s := v.String
				r.values[i] = &s
			}
		}
		batch = append(batch, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("datastore.mysql: reading %s: %w", identity.Table.Name, err)
	}
	if len(batch) == 0 {
		return 0, after, tx.Commit()
	}

	prepared, err := tx.PrepareContext(ctx, update)
	if err != nil {
		return 0, nil, fmt.Errorf("datastore.mysql: preparing the rewrite of %s: %w",
			identity.Table.Name, err)
	}
	defer func() { _ = prepared.Close() }()

	last := after
	for _, r := range batch {
		params := make([]any, 0, width+len(identity.Columns))
		for _, k := range r.key {
			params = append(params, k)
		}
		for i, c := range identity.Columns {
			transform, ok := masking.Lookup(c.Transform)
			if !ok {
				return 0, nil, fmt.Errorf("datastore.mysql: no transform called %s", c.Transform)
			}
			col := masking.Column{
				Schema: identity.Table.Schema, Table: identity.Table.Name,
				Name: c.Column.Name, Link: c.Link,
			}
			out, err := transform.Apply(key, col, r.values[i])
			if err != nil {
				return 0, nil, fmt.Errorf("datastore.mysql: %s.%s: %w",
					identity.Table.Name, c.Column.Name, err)
			}
			params = append(params, out)
		}
		bound, err := masking.ArgsInTextOrder(update, params)
		if err != nil {
			return 0, nil, err
		}
		if _, err := prepared.ExecContext(ctx, bound...); err != nil {
			return 0, nil, fmt.Errorf("datastore.mysql: writing %s: %w", identity.Table.Name, err)
		}
		last = r.key
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("datastore.mysql: committing %s: %w", identity.Table.Name, err)
	}
	return int64(len(batch)), last, nil
}

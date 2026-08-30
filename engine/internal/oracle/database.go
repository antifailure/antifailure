package oracle

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The database half of the oracle compares the two branches' CONTENTS rather
// than the statements that produced them, and that is the decision most people
// would make differently, so here is the argument.
//
// The alternatives were logical decoding and audit triggers. Logical decoding
// needs a replication slot and an output plugin installed in the database, and
// the point of this product is that the customer's application and database run
// unmodified. Audit triggers need DDL on every table in a database that is
// supposed to have production's shape, which changes the planner statistics the
// insights package is measuring three metres away. Both also answer a question
// nobody asked: which statements ran. The promise is "same state, same
// behaviour", and state is what a row holds.
//
// What content comparison costs: an insert and a delete inside one probe are
// invisible, because the net effect is nothing. That is a real limitation and
// it is written down rather than papered over. What it buys: a write is found
// however it arrived, including from a background worker, a trigger, a cascade
// or a migration, none of which a request level capture would have seen.
//
// Two snapshots per side rather than one, because "this row differs" and "this
// row differed before either version served a request" are different findings.
// The first is the application; the second is the migrations. A report that ran
// them together would send somebody to read a handler about a backfill.

// DefaultMaxRowsPerTable is how many rows a table may hold and still be
// compared.
//
// A table over the bound is NOT silently skipped: it is reported as not
// compared, with its approximate size, which is the difference between an
// oracle that is honest about its reach and one that reads as a clean bill of
// health. Ten thousand rows is enough for the tables a pull request's traffic
// touches and small enough that reading every one of them twice per side takes
// a second rather than a minute.
const DefaultMaxRowsPerTable = 10000

// maxFindingsPerTable bounds one table's contribution, for the reason
// maxFindingsPerBody gives.
const maxFindingsPerTable = 20

// captureTimeout bounds one table's read.
//
// Per table rather than for the whole capture, so one enormous table cannot
// consume the budget of every table after it and leave them all unread.
const captureTimeout = 30 * time.Second

// Conn is the part of *pgx.Conn this package uses, so a caller may pass a
// pooled connection.
type Conn interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// DatabaseOptions decide which tables are read and how much of each.
type DatabaseOptions struct {
	// Include, when not empty, restricts the comparison to tables matching one
	// of these patterns. A pattern is "schema.table", and either half may be
	// "*". A bare name matches that table in any schema.
	Include []string
	// Exclude removes tables matching any of these patterns, applied after
	// Include.
	Exclude []string
	// MaxRows is the per table bound. Zero means DefaultMaxRowsPerTable.
	MaxRows int
}

// MaxRowsOrDefault is the bound in force.
func (o DatabaseOptions) MaxRowsOrDefault() int {
	if o.MaxRows <= 0 {
		return DefaultMaxRowsPerTable
	}
	return o.MaxRows
}

// Column is one column's name and declared type.
//
// The type is carried because a migration that widens a column changes
// behaviour without changing a single row, and a comparison that only looked at
// values would report nothing at all.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (c Column) String() string { return c.Name + " " + c.Type }

// Snapshot is one branch's contents at one instant.
type Snapshot struct {
	Tables []Table `json:"tables"`
	// Notes are the tables that could not be read, and why.
	Notes []string `json:"notes,omitempty"`
}

// Table is one relation's contents.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Key is the primary key's columns, in order. Empty when the table has no
	// primary key, in which case rows are identified by their whole content
	// and an update reads as a delete and an insert.
	Key []string `json:"key,omitempty"`
	// Columns is every column with its type.
	Columns []Column `json:"columns"`
	// Rows is keyed by the rendered primary key, or by the row's canonical
	// form when there is no key. Excluded from JSON: it is the customer's data
	// and it belongs in a finding somebody chose to look at, not in every
	// machine readable report.
	Rows map[string]map[string]any `json:"-"`
	// Truncated is set when the table holds more rows than the bound, in which
	// case Rows is empty and the table is reported as not compared.
	Truncated bool `json:"truncated,omitempty"`
	// Estimate is the planner's row count, used only to say how big a table
	// that was not compared is. It is an estimate and is reported as one.
	Estimate int64 `json:"estimate,omitempty"`
	// RowCount is the number of rows read, exact when Truncated is false.
	RowCount int `json:"rows"`
}

// Qualified is the table's name as SQL writes it.
func (t Table) Qualified() string { return t.Schema + "." + t.Name }

// ColumnNames is the column list without types.
func (t Table) ColumnNames() []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// DatabaseSummary is what the report says about the database comparison as a
// whole.
type DatabaseSummary struct {
	// TablesCompared is how many tables were read on both sides.
	TablesCompared int `json:"tables_compared"`
	// RowsCompared is how many rows were read on the candidate side.
	RowsCompared int `json:"rows_compared"`
	// NotCompared names the tables that were not read, with the reason.
	NotCompared []string `json:"not_compared,omitempty"`
	// MaxRows is the bound that was in force, so a reader can raise it.
	MaxRows int `json:"max_rows"`
}

// Capture reads a branch's contents.
//
// Inside one READ ONLY REPEATABLE READ transaction, for two reasons that are
// both load bearing. Read only means Postgres refuses a write rather than this
// package promising not to make one, which is the argument the invariant
// package makes for the same construction. Repeatable read means every table is
// read at one instant, so a worker writing while the capture runs cannot
// produce a snapshot where orders has a row that order_items does not.
func Capture(ctx context.Context, conn Conn, opts DatabaseOptions) (*Snapshot, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
		IsoLevel:   pgx.RepeatableRead,
	})
	if err != nil {
		return nil, fmt.Errorf("opening a read only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tables, err := listTables(ctx, tx)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{}
	for i := range tables {
		t := tables[i]
		if !selected(t.Schema, t.Name, opts) {
			continue
		}
		if err := readTable(ctx, tx, &t, opts.MaxRowsOrDefault()); err != nil {
			snap.Notes = append(snap.Notes,
				fmt.Sprintf("%s could not be read: %s", t.Qualified(), oneLine(err.Error())))
			continue
		}
		snap.Tables = append(snap.Tables, t)
	}
	sort.Slice(snap.Tables, func(i, j int) bool {
		return snap.Tables[i].Qualified() < snap.Tables[j].Qualified()
	})
	return snap, nil
}

// listTables finds every ordinary table a user could have written to.
//
// Child partitions are left out because selecting from the partitioned parent
// already returns their rows, and listing both would report every partitioned
// row twice. Views are left out because they are derived: a view that differs
// does so because its tables differ, and the tables are already here.
func listTables(ctx context.Context, tx pgx.Tx) ([]Table, error) {
	const q = `
SELECT n.nspname, c.relname, c.reltuples::bigint
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp%'
ORDER BY n.nspname, c.relname`
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var out []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Schema, &t.Name, &t.Estimate); err != nil {
			return nil, fmt.Errorf("listing tables: %w", err)
		}
		if t.Estimate < 0 {
			// A table that has never been analysed reports minus one, which is
			// not a row count and must not be printed as one.
			t.Estimate = 0
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// readTable fills in a table's key, columns and rows.
func readTable(ctx context.Context, tx pgx.Tx, t *Table, maxRows int) error {
	ctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()

	qualified := quoteIdent(t.Schema) + "." + quoteIdent(t.Name)

	key, err := primaryKey(ctx, tx, qualified)
	if err != nil {
		return err
	}
	t.Key = key

	cols, err := columns(ctx, tx, qualified)
	if err != nil {
		return err
	}
	t.Columns = cols

	// to_jsonb rather than reading the columns individually. It gives every
	// value in one representation that the JSON comparison already
	// understands, so a timestamptz column and a timestamp in a response body
	// are normalised by the same code. Two implementations of "is this a clock
	// reading" would eventually disagree, and the report would then depend on
	// which half of the product a value came through.
	order := "to_jsonb(af_t)::text"
	if len(key) > 0 {
		quoted := make([]string, len(key))
		for i, k := range key {
			quoted[i] = quoteIdent(k)
		}
		order = strings.Join(quoted, ", ")
	}
	// One more than the bound, so "there are more than this" is known without
	// counting. Counting would be a second full scan of the table this bound
	// exists to avoid reading.
	q := fmt.Sprintf("SELECT to_jsonb(af_t) FROM %s AS af_t ORDER BY %s LIMIT %d",
		qualified, order, maxRows+1)

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()

	held := map[string]map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if len(held) >= maxRows {
			t.Truncated = true
			t.Rows = nil
			t.RowCount = 0
			// The extra row is drained by the deferred Close rather than
			// abandoned mid stream. The bound is inside the statement for the
			// same reason the invariant package puts its LIMIT there: pgx has
			// to finish the portal before the connection is usable again, so a
			// caller that stops reading pays for every remaining row anyway.
			return rows.Err()
		}
		value, err := decodeJSON(raw)
		if err != nil {
			return fmt.Errorf("a row of %s did not decode: %w", t.Qualified(), err)
		}
		row, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("a row of %s decoded as %s rather than an object",
				t.Qualified(), typeName(value))
		}
		held[rowKey(row, key)] = row
	}
	if err := rows.Err(); err != nil {
		return err
	}
	t.Rows = held
	t.RowCount = len(held)
	return nil
}

// rowKey identifies a row for matching.
//
// The primary key when there is one. When there is not, the row's whole
// canonical content, which means an updated row reads as one deleted and one
// inserted. That is the honest answer rather than a guess: without a key there
// is no fact about which row on one side corresponds to which row on the other,
// and inventing a correspondence would produce a confident and wrong "this
// column changed".
func rowKey(row map[string]any, key []string) string {
	if len(key) == 0 {
		return canonical(Config{}, row)
	}
	parts := make([]string, len(key))
	for i, k := range key {
		parts[i] = render(row[k])
	}
	return strings.Join(parts, "\x00")
}

func primaryKey(ctx context.Context, tx pgx.Tx, qualified string) ([]string, error) {
	const q = `
SELECT a.attname
FROM pg_index i
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
WHERE i.indrelid = $1::regclass AND i.indisprimary
ORDER BY array_position(i.indkey::int2[], a.attnum)`
	rows, err := tx.Query(ctx, q, qualified)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func columns(ctx context.Context, tx pgx.Tx, qualified string) ([]Column, error) {
	const q = `
SELECT a.attname, format_type(a.atttypid, a.atttypmod)
FROM pg_attribute a
WHERE a.attrelid = $1::regclass AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`
	rows, err := tx.Query(ctx, q, qualified)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// quoteIdent renders an identifier for SQL.
//
// Written here rather than passed as a bind parameter because an identifier
// cannot be one in Postgres. The names come from the catalogue rather than from
// a user, so this is defence against a table somebody named with a quotation
// mark rather than against an attacker, and it costs one function.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// selected applies the include and exclude patterns.
func selected(schema, name string, opts DatabaseOptions) bool {
	qualified := schema + "." + name
	if len(opts.Include) > 0 {
		found := false
		for _, p := range opts.Include {
			if matchTable(p, schema, name, qualified) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, p := range opts.Exclude {
		if matchTable(p, schema, name, qualified) {
			return false
		}
	}
	return true
}

// matchTable matches "schema.table", "table", "*.table" or "schema.*".
//
// A bare name matches the table in any schema, because most projects have one
// schema and asking them to write "public." in front of everything is a rule
// that buys nothing.
func matchTable(pattern, schema, name, qualified string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == qualified || pattern == name {
		return true
	}
	s, t, ok := strings.Cut(pattern, ".")
	if !ok {
		return false
	}
	return (s == "*" || s == schema) && (t == "*" || t == name)
}

// compareSnapshots diffs two branches' contents.
func compareSnapshots(
	cfg Config, opts DatabaseOptions, collect *collector, base, cand *Snapshot,
) ([]Finding, *DatabaseSummary) {
	summary := &DatabaseSummary{MaxRows: opts.MaxRowsOrDefault()}
	var findings []Finding

	byName := func(s *Snapshot) map[string]*Table {
		out := map[string]*Table{}
		for i := range s.Tables {
			out[s.Tables[i].Qualified()] = &s.Tables[i]
		}
		return out
	}
	bt, ct := byName(base), byName(cand)

	names := map[string]bool{}
	for n := range bt {
		names[n] = true
	}
	for n := range ct {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	ignore := newMatcher(cfg.IgnoreFields)

	for _, name := range sorted {
		b, inBase := bt[name]
		c, inCand := ct[name]
		switch {
		case !inCand:
			f := newFinding(KindTableMissing, Major, name, "")
			f.Detail = "the baseline has this table and the candidate does not"
			findings = append(findings, f)
			continue
		case !inBase:
			f := newFinding(KindTableExtra, Minor, name, "")
			f.Detail = "the candidate has this table and the baseline does not"
			findings = append(findings, f)
			continue
		}

		if diff := columnDifference(b.Columns, c.Columns); diff != "" {
			f := newFinding(KindColumns, Major, name, "")
			f.Baseline, f.Candidate = joinColumns(b.Columns), joinColumns(c.Columns)
			f.Detail = diff
			findings = append(findings, f)
		}

		if b.Truncated || c.Truncated {
			summary.NotCompared = append(summary.NotCompared, fmt.Sprintf(
				"%s holds about %d rows, more than the %d row bound, so its contents were not compared",
				name, maxInt64(b.Estimate, c.Estimate), summary.MaxRows))
			continue
		}

		summary.TablesCompared++
		summary.RowsCompared += c.RowCount
		findings = append(findings, compareRows(cfg, collect, ignore, name, b, c)...)
	}

	summary.NotCompared = append(summary.NotCompared, base.Notes...)
	summary.NotCompared = append(summary.NotCompared, cand.Notes...)
	return findings, summary
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func joinColumns(cols []Column) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c.String()
	}
	return strings.Join(parts, ", ")
}

// columnDifference describes how two column lists differ, or returns empty.
//
// Compared on name and type together, so a column retyped in place is a
// difference. A reordering is not: the order of columns in a table is not
// something an application can observe, and reporting it would fire on every
// migration that drops and re-adds a column.
func columnDifference(base, cand []Column) string {
	index := func(cols []Column) map[string]string {
		out := map[string]string{}
		for _, c := range cols {
			out[c.Name] = c.Type
		}
		return out
	}
	bi, ci := index(base), index(cand)

	var removed, added, retyped []string
	for _, c := range base {
		t, ok := ci[c.Name]
		switch {
		case !ok:
			removed = append(removed, c.Name)
		case t != c.Type:
			retyped = append(retyped, fmt.Sprintf("%s from %s to %s", c.Name, c.Type, t))
		}
	}
	for _, c := range cand {
		if _, ok := bi[c.Name]; !ok {
			added = append(added, c.Name)
		}
	}

	var parts []string
	if len(removed) > 0 {
		parts = append(parts, "drops "+strings.Join(removed, ", "))
	}
	if len(added) > 0 {
		parts = append(parts, "adds "+strings.Join(added, ", "))
	}
	if len(retyped) > 0 {
		parts = append(parts, "retypes "+strings.Join(retyped, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "the candidate " + strings.Join(parts, " and ")
}

// compareRows diffs one table's contents.
func compareRows(
	cfg Config, collect *collector, ignore *matcher, name string, base, cand *Table,
) []Finding {
	keys := make([]string, 0, len(base.Rows)+len(cand.Rows))
	seen := map[string]bool{}
	for k := range base.Rows {
		keys, seen[k] = append(keys, k), true
	}
	for k := range cand.Rows {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var out []Finding
	extra := 0
	add := func(f Finding) {
		if len(out) >= maxFindingsPerTable {
			extra++
			return
		}
		out = append(out, f)
	}

	for _, k := range keys {
		b, inBase := base.Rows[k]
		c, inCand := cand.Rows[k]
		switch {
		case inBase && !inCand:
			// Critical. The candidate stopped writing, or deleted, a row the
			// baseline has. Both databases branched the same golden and
			// received the same requests in the same order, so there is no
			// innocent explanation that does not involve the change.
			f := newFinding(KindRowMissing, Critical, name, describeKey(base.Key, k))
			f.Baseline = renderColumns(b, base.ColumnNames())
			f.Detail = "the baseline has this row and the candidate does not"
			add(f)
		case !inBase && inCand:
			f := newFinding(KindRowExtra, Minor, name, describeKey(cand.Key, k))
			f.Candidate = renderColumns(c, cand.ColumnNames())
			f.Detail = "the candidate has this row and the baseline does not"
			add(f)
		default:
			if changed := changedColumns(cfg, collect, ignore, name, b, c); len(changed) > 0 {
				f := newFinding(KindRowChanged, Major, name, describeKey(base.Key, k))
				f.Baseline = renderColumns(b, changed)
				f.Candidate = renderColumns(c, changed)
				f.Detail = plural(len(changed), "column differs", "columns differ") +
					": " + strings.Join(changed, ", ")
				add(f)
			}
		}
	}
	if extra > 0 {
		f := newFinding(KindRowChanged, Minor, name, "")
		f.Detail = fmt.Sprintf("%d more differing rows in this table, not listed", extra)
		out = append(out, f)
	}
	return out
}

// changedColumns names the columns whose values disagree, after normalisation.
func changedColumns(
	cfg Config, collect *collector, ignore *matcher, table string, base, cand map[string]any,
) []string {
	names := map[string]bool{}
	for k := range base {
		names[k] = true
	}
	for k := range cand {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var out []string
	for _, col := range sorted {
		// A row's path is $.<column>, so one field pattern covers a response
		// field and the column behind it. "$..created_at" is written once and
		// applies to both, which is what somebody means when they write it.
		if ignore.matches([]segment{keySegment(col)}) {
			continue
		}
		b, c := base[col], cand[col]
		if typeName(b) != typeName(c) {
			out = append(out, col)
			continue
		}
		switch b.(type) {
		case map[string]any, []any:
			// A jsonb or array column, compared as a whole rather than walked.
			// A path inside a jsonb column is not a path a manifest pattern can
			// address, and reporting "$.settings.theme" as a table finding
			// would invent a syntax nobody can use.
			if !equalValues(cfg, b, c) {
				out = append(out, col)
			}
		default:
			if !normaliseScalar(cfg, collect, table+"."+col, b, c) {
				out = append(out, col)
			}
		}
	}
	return out
}

func describeKey(key []string, rendered string) string {
	parts := strings.Split(rendered, "\x00")
	if len(key) == 0 || len(key) != len(parts) {
		// No primary key, so the identity is the whole row and printing it
		// again beside the values would be noise.
		return "(no primary key)"
	}
	pairs := make([]string, len(key))
	for i, k := range key {
		pairs[i] = k + "=" + parts[i]
	}
	return strings.Join(pairs, ", ")
}

func renderColumns(row map[string]any, cols []string) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		if _, ok := row[c]; !ok {
			continue
		}
		parts = append(parts, c+"="+render(row[c]))
	}
	joined := strings.Join(parts, " ")
	if len(joined) > maxValue {
		return joined[:maxValue-1] + "…"
	}
	return joined
}

// identity is what makes two findings from two comparisons the same finding.
//
// It deliberately leaves the values out: a row that differed before the traffic
// and differs differently after it is still the migrations' doing, and
// reclassifying it as the application's because a timestamp moved would be
// wrong.
func identity(f Finding) string {
	return string(f.Kind) + "\x00" + f.Where + "\x00" + f.Path
}

// attributePhases marks each database finding as the migrations' or the
// traffic's.
//
// Anything the pre-traffic comparison already found belongs to the migrations.
// Everything else appeared while the probes ran.
func attributePhases(before, after []Finding) []Finding {
	prior := map[string]bool{}
	for _, f := range before {
		prior[identity(f)] = true
	}
	out := make([]Finding, 0, len(after))
	for _, f := range after {
		if prior[identity(f)] {
			f.Phase = PhaseMigration
			// A difference the migrations made is not the candidate losing a
			// row it used to write; it is two schemas holding different seed
			// data. Ranking it critical would make every branch that touches a
			// migration look like a regression, so it is ranked down and the
			// phase says why.
			if f.Severity == Critical {
				f.Severity = Major
				f.SeverityName = f.Severity.String()
			}
		} else {
			f.Phase = PhaseTraffic
		}
		out = append(out, f)
	}
	return out
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= 200 {
		return s
	}
	return s[:199] + "…"
}

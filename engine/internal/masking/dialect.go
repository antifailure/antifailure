package masking

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// A dialect is the part of masking that knows one datastore engine.
//
// Everything else in this package is deliberately engine independent and was
// already: a transform is a pure function of the project key, the column
// identity and the input value, computed in Go and never in the database, and
// that property does not care which store the value came out of. Three things
// do care, and all three were Postgres without saying so.
//
//  1. The name an engine gives a type. `text` in Postgres is `String` in
//     ClickHouse, and the classifier, the rules people write and the
//     verification scanner all speak the Postgres name.
//  2. The way an identifier is quoted.
//  3. The statement that rewrites a row.
//
// The first of those is the one that leaks data. A ClickHouse column of type
// String matched no rule carrying `type: text`, so nothing in the classifier
// recognised it, so nothing decided what happened to it and it was copied
// unchanged. The same person is then a fake customer in Postgres and a real
// one in ClickHouse, and a join across the two produces two different people
// for one user. That is worse than an empty store, because an empty store is
// visible and this is not.
//
// So an engine's type names are mapped onto the POSTGRES names rather than
// onto a third vocabulary invented here. There are already two independent
// tables of Postgres type names in this repository, this package's and the
// verification scanner's, kept in step by a test rather than by sharing code,
// because verify must not import masking. A third vocabulary would mean three
// tables to keep in step instead of two, and the failure mode of a stale one
// is a column nobody classified.

// Query is a statement and the arguments it is run with.
type Query struct {
	SQL  string
	Args []any
}

// Dialect is everything masking needs to know about one datastore engine.
//
// Deliberately small, and deliberately not an execution surface. Nothing here
// opens a connection or runs a statement: the executor holds a Postgres
// connection and runs a chunk inside a transaction, which is what ctid
// addressing requires and what a ClickHouse mutation is not. Widening this
// interface until it could execute against both would weaken the contract the
// Postgres path depends on to accommodate an engine whose write path is not a
// transaction at all. The second engine's execution loop belongs with the
// second engine's provider.
type Dialect interface {
	// Engine is the name a manifest datastore declares, such as postgres or
	// clickhouse.
	Engine() string
	// Canonical maps one of this engine's own type names onto the Postgres
	// name for the same kind of value, which is the vocabulary the rules and
	// both classifiers speak. It is a projection: a name that is already
	// canonical comes back unchanged, so feeding the output back in is safe.
	// A type this dialect does not recognise comes back as it went in, which
	// is what lands it in the classifier's "nobody has decided about this"
	// branch rather than in a silently wrong one.
	Canonical(dataType string) string
	// QuoteIdent quotes an identifier so that a name carrying the quote
	// character cannot end the quoting early.
	QuoteIdent(name string) string
	// Placeholder renders the nth statement parameter, counting from one.
	//
	// The numbering is the executor's contract rather than either engine's:
	// the row key is always the first argument and the masked values follow in
	// the plan's column order. Two engines write a parameter differently and
	// neither gets to reorder them.
	Placeholder(n int) string
	// Qualify renders a table the way a statement addresses it.
	Qualify(t Table) string
	// Unaddressable says why this engine cannot rewrite one row of this table
	// at a time, and returns empty when it can. Postgres can always: a table
	// with no primary key is addressed by ctid. An engine with no physical row
	// identifier says so here, at planning time, because a masking run that
	// fails halfway leaves a table neither real nor safe.
	Unaddressable(t Table) string
	// RowKey is the expression that addresses one row.
	RowKey(tp TablePlan) string
	// SelectChunk reads one chunk of a table, resuming after a key when one is
	// given.
	SelectChunk(tp TablePlan, after string) Query
	// Update rewrites the planned columns of one row, with the values as
	// parameters rather than interpolated: they are computed in Go from a key
	// the database never sees, and a value carrying a quote must not be able
	// to change what the statement means.
	Update(tp TablePlan) Statement
}

// dialects is the registry, keyed by engine name.
var (
	dialectMu sync.RWMutex
	dialects  = map[string]Dialect{}
)

// RegisterDialect adds a dialect. Registering two under one name panics, the
// same way two transforms under one name do: it is a programming error visible
// at startup rather than a behaviour that changes with link order.
func RegisterDialect(d Dialect) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	name := d.Engine()
	if _, dup := dialects[name]; dup {
		panic("masking: two dialects are registered as " + name)
	}
	dialects[name] = d
}

// DialectFor returns the dialect for an engine.
//
// An empty engine is Postgres. That is a compatibility case and not a default
// anybody should rely on: a Table read from a database carries the engine that
// produced it, and every catalog reader sets it. The empty case exists because
// a Table value written before this field did, in a test or in a caller that
// only ever had one engine, means the one engine there was.
//
// An engine nobody has a dialect for is an ERROR rather than Postgres. Guessing
// would mean classifying a ClickHouse schema with Postgres type names, and the
// result of that is not a failure, it is a column that looks classified and
// was not.
func DialectFor(engine string) (Dialect, error) {
	if engine == "" {
		engine = enginePostgres
	}
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	d, ok := dialects[engine]
	if !ok {
		return nil, fmt.Errorf(
			"masking: there is no dialect for the engine %q, so nothing here knows what its "+
				"type names mean or how to address a row in it; there is %s",
			engine, strings.Join(dialectNamesLocked(), ", "))
	}
	return d, nil
}

// DialectNames returns every registered engine, sorted.
func DialectNames() []string {
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	return dialectNamesLocked()
}

func dialectNamesLocked() []string {
	out := make([]string, 0, len(dialects))
	for name := range dialects {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// dialect returns the table's dialect, or Postgres when the engine is unknown.
//
// Used only where an error cannot be reported and where the caller has already
// been refused: Assign records a Problem on every column of a table whose
// engine has no dialect, BuildPlan skips a table with problems, and the
// executor refuses a plan that has any. So a statement is never compiled for an
// engine nobody recognised. The fallback is here rather than a panic because a
// TablePlan built by hand in a test is a legitimate value.
func (t Table) dialect() Dialect {
	d, err := DialectFor(t.Engine)
	if err != nil {
		return postgresDialect{}
	}
	return d
}

// canonical returns the column with its type renamed into the Postgres
// vocabulary the classifier and the rules speak.
//
// The RAW name is what the plan prints and what an error message names, because
// a person reading it is looking at their own store's schema and "String" is
// what they will find there. The canonical name is what decides.
func canonical(t Table, c ColumnInfo) ColumnInfo {
	c.Type = t.dialect().Canonical(c.Type)
	return c
}

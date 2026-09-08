// Package crossstore answers one question about a twin that holds more than
// one store: is the person who is masked in your Postgres masked into the SAME
// person in your ClickHouse?
//
// The check itself has existed since the masking dialects landed, and it was
// reachable by nobody. masking.CrossStoreCheck had zero production callers:
// every caller was a test or a benchmark, "cross store" appeared nowhere in
// the command reference, and no tool mentioned it. So the guarantee was true
// in our continuous integration, on our fixtures, and for a customer it was
// still "the same person is masked identically in both, and you have our word
// for it", which is the sentence this product exists to refuse to say.
//
// What this package adds is the part that was missing: reading two of the
// customer's own catalogs, assigning their own rules to both, and handing the
// pair to the check that was already written.
//
// IT READS NO ROWS. That is the property that makes it safe to point at
// production, and it is not a happy accident of the implementation: the check
// masks values of its OWN through both stores' rules and compares the outputs,
// so what it needs from a store is the schema and nothing else. Every
// statement in this package lists columns and tables. None of them selects
// from a table anybody's data is in.
package crossstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Store is one datastore to compare, as the manifest declares it.
type Store struct {
	// Name is the datastore's name in the manifest.
	Name string
	// Engine is what it runs, such as postgres or clickhouse.
	Engine string
	// Var is the environment variable the connection string came from, which
	// is what a message names when a store could not be read. The value is
	// never named.
	Var string
	// URL is the connection string, which renders as [redacted] everywhere
	// text is produced.
	URL secret.Value
}

// Unread is a store that was declared and could not be read.
//
// Its own field rather than an error, because a run that reached one store and
// not the other has to report the number it got AND the store it did not, and
// an error would replace the first with the second. A cross store check over
// one store is not a check that passed.
type Unread struct {
	Store  string `json:"store"`
	Engine string `json:"engine"`
	// Why says what stopped it, with no credential in it.
	Why string `json:"why"`
}

// Report is what a run found.
type Report struct {
	// Read names the stores whose catalogs were read, in the order given.
	Read []string `json:"stores_read"`
	// Unread names the stores that were declared and could not be read, with
	// the reason for each.
	Unread []Unread `json:"stores_unread,omitempty"`
	// Tables and Columns are how much schema was read, so a number can be read
	// against the size of the thing it is about.
	Tables  int `json:"tables"`
	Columns int `json:"columns"`
	// SkippedTables names the tables a reader deliberately did not return,
	// with the reason for each. Empty for a run that read everything.
	SkippedTables []string `json:"skipped_tables,omitempty"`
	// Cross is the check's own report, and is the zero value when fewer than
	// two stores could be read.
	Cross masking.CrossStoreReport `json:"cross_store"`
	// RulesHash identifies the rule set both sides were assigned with. One
	// rules file for both stores is the whole premise: two files would make
	// disagreement the expected outcome rather than the finding.
	RulesHash string `json:"rules_hash,omitempty"`
}

// OK reports whether the guarantee held.
//
// THREE conditions, and the third is the one an ordinary implementation leaves
// out. Two stores read, every candidate join key verified identical, and NO
// STORE LEFT UNREAD.
//
// The third matters because the guarantee is about every store, not about the
// two that happened to answer. A manifest declaring a Postgres, a ClickHouse
// and an Elasticsearch, where the Elasticsearch refused the connection, has
// been shown nothing at all about the Elasticsearch, and a green line saying
// the identifiers are identical would be read as covering it. That is the
// defect this whole package was written against, one store further along.
//
// A store that names no source_url_env is NOT counted here. Nobody asked for
// it to be compared: a Redis declared empty holds no identity to compare, and
// requiring one would make a pass impossible for every realistic manifest. It
// is named in the report instead, so the coverage is visible without being a
// verdict.
func (r Report) OK() bool {
	return len(r.Read) >= 2 && len(r.Unread) == 0 && r.Cross.OK()
}

// Summary is the line a person quotes, and the lines under it that say why
// when there is a why.
func (r Report) Summary() string {
	var b strings.Builder
	switch {
	case len(r.Read) == 0:
		b.WriteString("no store could be read, so nothing was compared and nothing is proved\n")
	case len(r.Read) == 1:
		fmt.Fprintf(&b, "only %s could be read, so there was nothing to compare it with "+
			"and nothing is proved\n", r.Read[0])
	default:
		b.WriteString(r.Cross.Summary())
	}
	for _, u := range r.Unread {
		fmt.Fprintf(&b, "  %s (%s) could not be read, so nothing here compared it: %s\n",
			u.Store, u.Engine, u.Why)
	}
	return b.String()
}

// Catalog is a store's schema, as this package reads it.
type Catalog interface {
	// Tables lists the tables and columns masking would decide about.
	Tables(ctx context.Context) ([]masking.Table, error)
	// Skipped names the tables the reader deliberately did not return, with
	// the reason for each, and is empty when it returned everything.
	//
	// Part of the interface rather than an implementation detail, because a
	// number computed over the tables a reader felt like returning is not a
	// number anybody should quote. A ClickHouse view has no rows of its own
	// and a Distributed engine is a pointer at another server; neither is a
	// store whose masking can be compared, and both have to be SAID rather
	// than dropped.
	Skipped() []string
	// Close releases the connection.
	Close() error
}

// Opener opens a store for reading its catalog.
//
// A function rather than a registry, so a test can supply two catalogs with no
// server and the live path stays the one thing that needs one.
type Opener func(ctx context.Context, s Store) (Catalog, error)

// Request is one run of the check.
type Request struct {
	// Key is the project's masking key. The check masks its probes through
	// both sides with it, and a different key would compare two runs rather
	// than two stores.
	Key *masking.Key
	// Rules is the one rule set both stores are assigned with.
	Rules *masking.RuleSet
	// RulesHash identifies that rule set, for the report.
	RulesHash string
	// Stores are the stores to compare, in the order they should be reported.
	Stores []Store
	// Probes are the values masked through both sides, and the default set is
	// used when this is empty.
	Probes []string
	// Open opens a store, defaulting to the live one.
	Open Opener
}

// Check reads every store's catalog, assigns one rule set to all of them, and
// compares what they would do to the same identifier.
//
// A store that cannot be opened or read is recorded and the run continues,
// because the alternative is that an unreachable cache stops anybody finding
// out whether their Postgres and their ClickHouse agree. What it must never do
// is let the absence read as agreement, and that is what Report.OK is for.
func Check(ctx context.Context, req Request) (Report, error) {
	if req.Key == nil {
		return Report{}, errors.New("crossstore: the check needs the project's masking key")
	}
	if req.Rules == nil {
		return Report{}, errors.New("crossstore: the check needs one rule set for every store")
	}
	open := req.Open
	if open == nil {
		open = Open
	}

	report := Report{RulesHash: req.RulesHash}
	var assigned []masking.StoreAssignments
	for _, s := range req.Stores {
		tables, skipped, err := readOne(ctx, open, s)
		if err != nil {
			report.Unread = append(report.Unread, Unread{
				Store: s.Name, Engine: s.Engine, Why: oneLine(err),
			})
			continue
		}
		report.Read = append(report.Read, s.Name)
		report.Tables += len(tables)
		for _, t := range tables {
			report.Columns += len(t.Columns)
		}
		for _, skip := range skipped {
			report.SkippedTables = append(report.SkippedTables, s.Name+"."+skip)
		}
		assigned = append(assigned, masking.StoreAssignments{
			Store: s.Name, Assignments: req.Rules.Assign(tables),
		})
	}

	if len(assigned) < 2 {
		// Not an error. The caller has a report naming which stores were read
		// and why the others were not, OK is false, and that is a more useful
		// answer than a failure with the same sentence in it.
		return report, nil
	}
	cross, err := masking.CrossStoreCheck(req.Key, assigned, req.Probes)
	if err != nil {
		return report, err
	}
	report.Cross = cross
	return report, nil
}

// readOne opens a store, reads its catalog, and closes it.
func readOne(ctx context.Context, open Opener, s Store) ([]masking.Table, []string, error) {
	cat, err := open(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = cat.Close() }()
	tables, err := cat.Tables(ctx)
	if err != nil {
		return nil, nil, err
	}
	// Sorted, so that two stores read in whatever order their servers list
	// them produce pairs in one order and a report can be compared with
	// yesterday's.
	sort.Slice(tables, func(i, j int) bool { return tables[i].String() < tables[j].String() })
	return tables, cat.Skipped(), nil
}

// oneLine flattens a message so a report stays one line per store.
func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

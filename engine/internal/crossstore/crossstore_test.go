package crossstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/crossstore"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// The run around the check, which is where the honesty has to live.
//
// masking.CrossStoreCheck already refuses to call zero of zero a pass, and its
// own tests pin that. What is new here is everything around it: a store that
// could not be opened, a store that named no variable, a run that reached one
// of two. Each of those has an obvious wrong answer that looks like a right
// one, which is to report the number from the stores that did answer and say
// nothing about the ones that did not.

func testKey(t *testing.T) *masking.Key {
	t.Helper()
	key, err := masking.NewKey(secret.New("a-test-key-that-is-long-enough-to-use"))
	require.NoError(t, err)
	return key
}

// fakeCatalog is a store that answers from a fixed catalog.
type fakeCatalog struct {
	tables  []masking.Table
	skipped []string
	closed  *bool
}

func (f fakeCatalog) Tables(context.Context) ([]masking.Table, error) { return f.tables, nil }
func (f fakeCatalog) Skipped() []string                               { return f.skipped }
func (f fakeCatalog) Close() error {
	if f.closed != nil {
		*f.closed = true
	}
	return nil
}

// failingCatalog is a store whose schema cannot be read once it is open.
type failingCatalog struct{ err error }

func (f failingCatalog) Tables(context.Context) ([]masking.Table, error) { return nil, f.err }
func (f failingCatalog) Skipped() []string                               { return nil }
func (f failingCatalog) Close() error                                    { return nil }

func postgresPerson() masking.Table {
	return masking.Table{
		Engine: "postgres", Schema: "public", Name: "person",
		PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "email", Type: "text", Nullable: true},
		},
	}
}

func clickhouseEvents() masking.Table {
	return masking.Table{
		Engine: "clickhouse", Schema: "af", Name: "events",
		PrimaryKey: []string{"uuid"},
		Columns: []masking.ColumnInfo{
			{Name: "uuid", Type: "UUID"},
			{Name: "email", Type: "Nullable(String)", Nullable: true},
		},
	}
}

func catalogs(byStore map[string][]masking.Table) crossstore.Opener {
	return func(_ context.Context, s crossstore.Store) (crossstore.Catalog, error) {
		tables, ok := byStore[s.Name]
		if !ok {
			return nil, errors.New("connection refused")
		}
		return fakeCatalog{tables: tables}, nil
	}
}

func request(t *testing.T, open crossstore.Opener, stores ...crossstore.Store) crossstore.Request {
	t.Helper()
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	return crossstore.Request{
		Key: testKey(t), Rules: rules, RulesHash: "test", Stores: stores, Open: open,
	}
}

func store(name, engine string) crossstore.Store {
	return crossstore.Store{
		Name: name, Engine: engine, Var: "URL_" + name, URL: secret.New("x://" + name),
	}
}

// The number, from two stores that agree.
func TestCrossStore_TwoStoresThatAgreeReportTheNumber(t *testing.T) {
	t.Parallel()
	open := catalogs(map[string][]masking.Table{
		"primary": {postgresPerson()},
		"events":  {clickhouseEvents()},
	})
	report, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err)

	require.True(t, report.OK(), report.Summary())
	require.Equal(t, []string{"primary", "events"}, report.Read)
	require.Positive(t, report.Cross.Checked, "email is in both stores and both mask it")
	require.Equal(t, report.Cross.Checked, report.Cross.Identical)
	require.Equal(t, 100.0, report.Cross.Percent())
	require.Equal(t, 2, report.Tables)
	require.Equal(t, 4, report.Columns)
}

// A rules file that masks the same column two different ways in two stores is
// the failure the whole check exists for: one identity becomes two people, and
// every join across the two stores then returns the wrong one.
func TestCrossStore_DivergentRulesSayNo(t *testing.T) {
	t.Parallel()
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "public.person", Column: "email", Transform: "email",
			Why: "The address in the primary."},
		{Table: "af.events", Column: "email", Transform: "hash_hex",
			Why: "The same address, hashed, which is a different person."},
	})
	require.NoError(t, err)

	open := catalogs(map[string][]masking.Table{
		"primary": {postgresPerson()},
		"events":  {clickhouseEvents()},
	})
	report, err := crossstore.Check(context.Background(), crossstore.Request{
		Key: testKey(t), Rules: rules, Open: open,
		Stores: []crossstore.Store{store("primary", "postgres"), store("events", "clickhouse")},
	})
	require.NoError(t, err)

	require.False(t, report.OK(), "two transforms for one identifier is not a pass")
	require.Len(t, report.Cross.Mismatches(), 1)
	require.Contains(t, report.Cross.Mismatches()[0].Reason, "one identity becomes two people")
	require.Contains(t, report.Summary(), "1")
}

// One store read is not a comparison, and it must not report the number from
// the store that answered.
func TestCrossStore_OneStoreReadProvesNothing(t *testing.T) {
	t.Parallel()
	open := catalogs(map[string][]masking.Table{"primary": {postgresPerson()}})
	report, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err, "an unreachable store is a finding rather than a failure to report")

	require.False(t, report.OK())
	require.Equal(t, []string{"primary"}, report.Read)
	require.Len(t, report.Unread, 1)
	require.Equal(t, "events", report.Unread[0].Store)
	require.Contains(t, report.Unread[0].Why, "connection refused")
	require.Contains(t, report.Summary(), "nothing is proved")
	require.Zero(t, report.Cross.Checked,
		"a report over one store has no pairs, so nothing may be counted as identical")
}

// A store that opens and then cannot be read is the same fact as one that
// never opened, and both belong in Unread rather than in a returned error.
func TestCrossStore_AStoreThatOpensAndCannotBeReadIsUnread(t *testing.T) {
	t.Parallel()
	open := func(_ context.Context, s crossstore.Store) (crossstore.Catalog, error) {
		if s.Name == "events" {
			return failingCatalog{err: errors.New("permission denied on system.columns")}, nil
		}
		return fakeCatalog{tables: []masking.Table{postgresPerson()}}, nil
	}
	report, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err)
	require.Len(t, report.Unread, 1)
	require.Contains(t, report.Unread[0].Why, "permission denied")
	require.False(t, report.OK())
}

// Every store is closed, including the one whose schema could not be read. A
// catalog left open holds a connection to production.
func TestCrossStore_EveryStoreIsClosed(t *testing.T) {
	t.Parallel()
	closedPrimary, closedEvents := false, false
	open := func(_ context.Context, s crossstore.Store) (crossstore.Catalog, error) {
		if s.Name == "events" {
			return fakeCatalog{tables: []masking.Table{clickhouseEvents()}, closed: &closedEvents}, nil
		}
		return fakeCatalog{tables: []masking.Table{postgresPerson()}, closed: &closedPrimary}, nil
	}
	_, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err)
	require.True(t, closedPrimary, "the primary's connection was left open")
	require.True(t, closedEvents, "the second store's connection was left open")
}

// Two stores that share no identifier is zero of zero, and zero of zero is not
// a pass. This is the case the check itself is most careful about and the one a
// wrapper is most likely to lose.
func TestCrossStore_NothingInCommonIsNotAPass(t *testing.T) {
	t.Parallel()
	other := masking.Table{
		Engine: "clickhouse", Schema: "af", Name: "hits",
		PrimaryKey: []string{"hit_id"},
		Columns: []masking.ColumnInfo{
			{Name: "hit_id", Type: "UUID"},
			{Name: "path", Type: "String"},
		},
	}
	open := catalogs(map[string][]masking.Table{
		"primary": {postgresPerson()},
		"events":  {other},
	})
	report, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err)

	require.Equal(t, 0, report.Cross.Checked)
	require.False(t, report.OK(), "nothing compared is not everything agreeing")
	require.Contains(t, report.Summary(), "nothing is proved")
}

func TestCrossStore_RefusesWithoutAKeyOrRules(t *testing.T) {
	t.Parallel()
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)

	_, err = crossstore.Check(context.Background(), crossstore.Request{Rules: rules})
	require.ErrorContains(t, err, "masking key")

	_, err = crossstore.Check(context.Background(), crossstore.Request{Key: testKey(t)})
	require.ErrorContains(t, err, "one rule set for every store")
}

// A Report is an exported value somebody can assemble, and OK has to be right
// about one that was not produced by Check.
//
// Inside Check the two clauses of OK are belt and braces: a run that read one
// store never sets Cross, so the embedded report is the zero value and says
// false on its own. This is the case where they are not: a caller that filled
// in a cross store report by hand beside a single store. Two stores read is
// part of the answer, not an implementation detail of how it was reached.
func TestCrossStore_AReportOverOneStoreIsNotAPassHoweverItWasBuilt(t *testing.T) {
	t.Parallel()
	r := crossstore.Report{
		Read: []string{"primary"},
		Cross: masking.CrossStoreReport{
			Stores: []string{"primary"}, Checked: 4, Identical: 4,
		},
	}
	require.False(t, r.OK(),
		"four of four across one store is four of four across nothing")
	require.Contains(t, r.Summary(), "nothing is proved")
}

// The store that answered is not the store that did not.
//
// Two stores agreeing perfectly while a third refused the connection is not a
// pass. The guarantee is about every store, and a green line saying the
// identifiers are identical would be read as covering the one nothing opened.
// This is the defect the whole package exists against, one store further along.
func TestCrossStore_AStoreLeftUnreadIsNotAPassEvenWhenTheRestAgree(t *testing.T) {
	t.Parallel()
	open := catalogs(map[string][]masking.Table{
		"primary": {postgresPerson()},
		"events":  {clickhouseEvents()},
	})
	report, err := crossstore.Check(context.Background(), request(t, open,
		store("primary", "postgres"), store("events", "clickhouse"), store("search", "elasticsearch")))
	require.NoError(t, err)

	require.Equal(t, []string{"primary", "events"}, report.Read)
	require.Equal(t, report.Cross.Checked, report.Cross.Identical,
		"the two stores that answered do agree, which is what makes this the hard case")
	require.True(t, report.Cross.OK(), "the comparison itself passed")
	require.False(t, report.OK(),
		"a store nothing opened has been shown nothing about, and a pass would be read "+
			"as covering it")
	require.Contains(t, report.Summary(), "nothing here compared it")
}

// A store that names no source is NOT the same fact and does not refuse the
// run. Nobody asked for it to be compared: a Redis declared empty holds no
// identity to compare, and requiring one would make a pass impossible for
// every realistic manifest.
func TestCrossStore_AStoreNobodyAskedAboutDoesNotRefuseTheRun(t *testing.T) {
	t.Parallel()
	open := catalogs(map[string][]masking.Table{
		"primary": {postgresPerson()},
		"events":  {clickhouseEvents()},
	})
	report, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err)
	require.True(t, report.OK(),
		"the stores that were asked about agree and nothing was left unread")
}

// A table the reader did not return is named in the report, with its store.
//
// The number is a share of the join keys it could see, and a reader that
// dropped half a schema without saying so would make it a true answer to a
// smaller question. A ClickHouse view has no rows of its own and a Distributed
// engine is a pointer at another server, so both are correctly left out of a
// comparison and both have to be said.
func TestCrossStore_ATableTheReaderSkippedIsNamedWithItsStore(t *testing.T) {
	t.Parallel()
	open := func(_ context.Context, s crossstore.Store) (crossstore.Catalog, error) {
		if s.Name == "events" {
			return fakeCatalog{
				tables:  []masking.Table{clickhouseEvents()},
				skipped: []string{"events_mv: a materialized view has no rows of its own"},
			}, nil
		}
		return fakeCatalog{tables: []masking.Table{postgresPerson()}}, nil
	}
	report, err := crossstore.Check(context.Background(),
		request(t, open, store("primary", "postgres"), store("events", "clickhouse")))
	require.NoError(t, err)

	require.Equal(t, []string{"events.events_mv: a materialized view has no rows of its own"},
		report.SkippedTables,
		"the store is part of the name, because two stores can hold a table with one name")
	require.True(t, report.OK(),
		"a table correctly left out of a golden is not a store that disagreed")
}

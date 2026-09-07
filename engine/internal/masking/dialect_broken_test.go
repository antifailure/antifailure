package masking

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// The broken fakes, one per behaviour, and the self test that watches each one
// go red.
//
// Every fake wraps a correct dialect and breaks exactly one thing, which is
// what makes it a control: a fake that was broken in five ways would fail the
// suite for reasons nobody could attribute. The Postgres dialect is the one
// wrapped because it is the one with a live suite behind it.

// brokenDialect is a correct dialect with one method replaced.
type brokenDialect struct {
	Dialect
	engine      func() string
	canonical   func(string) string
	quoteIdent  func(string) string
	placeholder func(int) string
	qualify     func(Table) string
	rowKey      func(TablePlan) string
	selectChunk func(TablePlan, string) Query
	update      func(TablePlan) Statement
	addressable func(Table) string
}

func (b brokenDialect) Engine() string {
	if b.engine != nil {
		return b.engine()
	}
	return b.Dialect.Engine()
}

func (b brokenDialect) Canonical(t string) string {
	if b.canonical != nil {
		return b.canonical(t)
	}
	return b.Dialect.Canonical(t)
}

func (b brokenDialect) QuoteIdent(name string) string {
	if b.quoteIdent != nil {
		return b.quoteIdent(name)
	}
	return b.Dialect.QuoteIdent(name)
}

func (b brokenDialect) Placeholder(n int) string {
	if b.placeholder != nil {
		return b.placeholder(n)
	}
	return b.Dialect.Placeholder(n)
}

func (b brokenDialect) Qualify(t Table) string {
	if b.qualify != nil {
		return b.qualify(t)
	}
	return b.Dialect.Qualify(t)
}

func (b brokenDialect) RowKey(tp TablePlan) string {
	if b.rowKey != nil {
		return b.rowKey(tp)
	}
	return b.Dialect.RowKey(tp)
}

func (b brokenDialect) SelectChunk(tp TablePlan, after string) Query {
	if b.selectChunk != nil {
		return b.selectChunk(tp, after)
	}
	return b.Dialect.SelectChunk(tp, after)
}

func (b brokenDialect) Update(tp TablePlan) Statement {
	if b.update != nil {
		return b.update(tp)
	}
	return b.Dialect.Update(tp)
}

func (b brokenDialect) Unaddressable(t Table) string {
	if b.addressable != nil {
		return b.addressable(t)
	}
	return b.Dialect.Unaddressable(t)
}

func sound() Dialect { return postgresDialect{} }

// dialectControls pairs each injected defect with the behaviour that has to
// notice it, and with every other behaviour it reds.
//
// The `also` column is exact rather than a note. The self test requires the set
// of behaviours that went red to be precisely the named one plus these, so an
// overlap is something a reader can see rather than something the table quietly
// tolerates. Three defects red more than one behaviour because the row key
// appears in every statement built from it, and a dialect that addresses the
// wrong row writes the wrong statement everywhere.
var dialectControls = []struct {
	flaw      string
	behaviour string
	also      []string
	dialect   Dialect
}{
	{
		flaw:      "a dialect registered under a name it does not answer to",
		behaviour: "Engine_IsNamedAndRegistered",
		dialect:   brokenDialect{Dialect: sound(), engine: func() string { return "not-registered" }},
	},
	{
		flaw:      "a canonical mapping that moves again when it is applied twice",
		behaviour: "Canonical_IsAProjection",
		dialect: brokenDialect{Dialect: sound(), canonical: func(t string) string {
			switch t {
			case "text":
				return "json"
			case "json":
				return "jsonb"
			}
			return t
		}},
	},
	{
		flaw:      "a dialect that guesses text for a type it has never heard of",
		behaviour: "Canonical_LeavesATypeItDoesNotKnowAlone",
		dialect: brokenDialect{Dialect: sound(), canonical: func(t string) string {
			if sound().Canonical(t) == t {
				return "text"
			}
			return sound().Canonical(t)
		}},
	},
	{
		flaw:      "an identifier quoted without doubling the quote inside it",
		behaviour: "QuoteIdent_CannotBeEndedEarly",
		dialect: brokenDialect{Dialect: sound(), quoteIdent: func(name string) string {
			return `"` + name + `"`
		}},
	},
	{
		flaw:      "a table addressed without its schema",
		behaviour: "Qualify_NamesTheSchemaAndTheTable",
		dialect: brokenDialect{Dialect: sound(), qualify: func(t Table) string {
			return sound().QuoteIdent(t.Name)
		}},
	},
	{
		flaw:      "two argument positions that render as one placeholder",
		behaviour: "Placeholder_IsDistinctPerPosition",
		dialect: brokenDialect{Dialect: sound(), placeholder: func(n int) string {
			if n == 2 {
				return "$1"
			}
			return fmt.Sprintf("$%d", n)
		}},
	},
	{
		flaw:      "a row addressed by the second key column instead of the first",
		behaviour: "RowKey_AddressesTheFirstKeyColumn",
		also: []string{
			// The row key is what every statement compares and orders on, so a
			// dialect whose RowKey disagrees with its own statements disagrees
			// with them everywhere at once.
			"Update_SendsEveryValueAsAParameter",
			"SelectChunk_ReadsTheKeyAndEveryPlannedColumn",
			"SelectChunk_OrdersByTheRowKey",
		},
		dialect: brokenDialect{Dialect: sound(), rowKey: func(tp TablePlan) string {
			return sound().QuoteIdent(tp.OrderBy[1])
		}},
	},
	{
		flaw:      "a statement that reports fewer columns than it rewrites",
		behaviour: "Update_RewritesEveryPlannedColumn",
		dialect: brokenDialect{Dialect: sound(), update: func(tp TablePlan) Statement {
			s := sound().Update(tp)
			s.Columns = s.Columns[:len(s.Columns)-1]
			return s
		}},
	},
	{
		flaw:      "a statement that reports its columns in the wrong order",
		behaviour: "Update_RewritesEveryPlannedColumn",
		dialect: brokenDialect{Dialect: sound(), update: func(tp TablePlan) Statement {
			s := sound().Update(tp)
			for i, j := 0, len(s.Columns)-1; i < j; i, j = i+1, j-1 {
				s.Columns[i], s.Columns[j] = s.Columns[j], s.Columns[i]
			}
			return s
		}},
	},
	{
		flaw:      "a masked value built into the statement text instead of sent as an argument",
		behaviour: "Update_SendsEveryValueAsAParameter",
		dialect: brokenDialect{Dialect: sound(), update: func(tp TablePlan) Statement {
			s := sound().Update(tp)
			last := sound().Placeholder(len(tp.Columns) + 1)
			s.SQL = strings.Replace(s.SQL, last, "'a literal'", 1)
			return s
		}},
	},
	{
		flaw:      "a read with no chunk limit, which takes the whole table at once",
		behaviour: "SelectChunk_ReadsTheKeyAndEveryPlannedColumn",
		dialect: brokenDialect{Dialect: sound(), selectChunk: func(tp TablePlan, after string) Query {
			q := sound().SelectChunk(tp, after)
			q.SQL = strings.Replace(q.SQL, fmt.Sprintf(" LIMIT %d", tp.ChunkSize), "", 1)
			return q
		}},
	},
	{
		flaw:      "a resume point interpolated into the text instead of sent as an argument",
		behaviour: "SelectChunk_ResumesAfterAKey",
		dialect: brokenDialect{Dialect: sound(), selectChunk: func(tp TablePlan, after string) Query {
			q := sound().SelectChunk(tp, after)
			if after != "" {
				q.SQL = strings.Replace(q.SQL, sound().Placeholder(1), "'"+after+"'", 1)
				q.Args = nil
			}
			return q
		}},
	},
	{
		flaw:      "a read in no order, which can visit one row twice and another never",
		behaviour: "SelectChunk_OrdersByTheRowKey",
		dialect: brokenDialect{Dialect: sound(), selectChunk: func(tp TablePlan, after string) Query {
			q := sound().SelectChunk(tp, after)
			if at := strings.Index(q.SQL, " ORDER BY "); at >= 0 {
				end := strings.Index(q.SQL[at+1:], " LIMIT ")
				if end < 0 {
					q.SQL = q.SQL[:at]
				} else {
					q.SQL = q.SQL[:at] + q.SQL[at+1+end:]
				}
			}
			return q
		}},
	},
	{
		flaw:      "a statement that is different text every time it is compiled",
		behaviour: "Statements_AreTheSameTextTwice",
		dialect:   &driftingDialect{Dialect: sound()},
	},
	{
		flaw:      "a table with a key refused as though it had none",
		behaviour: "Unaddressable_AcceptsATableWithAKey",
		dialect: brokenDialect{Dialect: sound(), addressable: func(Table) string {
			return "this dialect refuses everything"
		}},
	},
}

// driftingDialect compiles a different statement every time, which is the one
// defect a stateless fake cannot express.
type driftingDialect struct {
	Dialect
	n int
}

func (d *driftingDialect) Update(tp TablePlan) Statement {
	d.n++
	s := d.Dialect.Update(tp)
	s.SQL = fmt.Sprintf("%s /* %d */", s.SQL, d.n)
	return s
}

// redBehaviours runs every behaviour against a dialect and names the ones that
// failed.
func redBehaviours(d Dialect) []string {
	var out []string
	for _, b := range dialectBehaviours() {
		if err := b.Check(d); err != nil {
			out = append(out, b.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestDialectSuite_FailsEachBrokenDialect is the claim the suite makes about
// itself, checked.
func TestDialectSuite_FailsEachBrokenDialect(t *testing.T) {
	t.Parallel()
	for _, control := range dialectControls {
		control := control
		t.Run(control.flaw, func(t *testing.T) {
			t.Parallel()
			red := redBehaviours(control.dialect)
			want := append([]string{control.behaviour}, control.also...)
			sort.Strings(want)

			if len(red) == 0 {
				t.Fatalf("%s passed every behaviour, so nothing in the suite can catch it "+
					"and %s asserts something that cannot go red",
					control.flaw, control.behaviour)
			}
			if strings.Join(red, ",") != strings.Join(want, ",") {
				t.Errorf("%s reddened %v and the table says it reddens %v; a behaviour "+
					"failing for a reason nobody wrote down is not a control",
					control.flaw, red, want)
			}
		})
	}
}

// TestDialectSuite_EveryBehaviourHasABrokenFake keeps the table honest as
// behaviours are added.
//
// A behaviour with no defect pointed at it has never been shown to go red,
// which is the state every assertion is in until somebody proves otherwise.
func TestDialectSuite_EveryBehaviourHasABrokenFake(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, control := range dialectControls {
		covered[control.behaviour] = true
	}
	for _, b := range dialectBehaviours() {
		if !covered[b.Name] {
			t.Errorf("no broken dialect is pointed at %s, so it has never been seen to "+
				"fail and there is nothing to say it can", b.Name)
		}
	}
}

// TestDialectSuite_PassesACorrectDialect is the positive control.
//
// Without it the fakes above prove only that the suite can fail, which a suite
// that failed on everything would also satisfy.
func TestDialectSuite_PassesACorrectDialect(t *testing.T) {
	t.Parallel()
	for _, name := range DialectNames() {
		d, err := DialectFor(name)
		if err != nil {
			t.Fatalf("DialectFor(%q): %v", name, err)
		}
		if red := redBehaviours(d); len(red) > 0 {
			t.Errorf("the %s dialect fails %v", name, red)
		}
	}
}

package masking

import (
	"fmt"
	"strings"
	"testing"
)

// The conformance suite for a dialect, with a broken fake per behaviour and a
// self test that watches each one go red.
//
// The rule this follows is the one L0.1 exists to enforce: every new interface
// ships with a broken fake and a self test in the same commit. The database
// conformance suite in this repository declared twenty four behaviours and had
// never been shown to fail a single one, and a behaviour that has never gone
// red is a function call, not a check.
//
// A behaviour here returns an error rather than calling t.Error, which is what
// lets the self test watch a red without a subprocess: a failure is a value
// rather than a state of the test binary. The datastore suite next door needs
// the subprocess because its behaviours use require; these are pure functions
// over a plan and do not.

// dialectBehaviour is one property every dialect must have.
type dialectBehaviour struct {
	Name  string
	Check func(d Dialect) error
}

// probeTable is the table every behaviour reasons about.
//
// Three columns so that "every column" can differ from "the first column", a
// two column key so that "the first key column" can differ from "the key", and
// a chunk size so that the limit is visible in the read.
func probeTable() TablePlan {
	t := Table{
		Schema: "app", Name: "people",
		PrimaryKey: []string{"id", "tenant"},
		Columns: []ColumnInfo{
			{Name: "id", Type: "uuid"},
			{Name: "tenant", Type: "text"},
			{Name: "email", Type: "text", Nullable: true},
			{Name: "full_name", Type: "text", Nullable: true},
			{Name: "note", Type: "text", Nullable: true},
		},
	}
	return TablePlan{
		Table: t,
		Columns: []Assignment{
			{Table: t, Column: t.ColumnNamed("email"), Transform: "email", Link: "email"},
			{Table: t, Column: t.ColumnNamed("full_name"), Transform: "name", Link: "name"},
			{Table: t, Column: t.ColumnNamed("note"), Transform: "free_text", Link: "free_text"},
		},
		ChunkSize: 20000,
		OrderBy:   []string{"id", "tenant"},
	}
}

// awkwardNames are identifiers that end the quoting early when nobody escapes
// them. A store whose table names come from a manifest is a store where these
// are reachable, which is why quoteIdent is unconditional rather than reasoned
// about per call site.
var awkwardNames = []string{`a"b`, "a`b", "a'b", `a\b`, "a b", "select"}

func dialectBehaviours() []dialectBehaviour {
	return []dialectBehaviour{
		{
			Name: "Engine_IsNamedAndRegistered",
			Check: func(d Dialect) error {
				if strings.TrimSpace(d.Engine()) == "" {
					return fmt.Errorf("the dialect has no engine name, so no manifest can " +
						"select it and no error message can say which store it was about")
				}
				found, err := DialectFor(d.Engine())
				if err != nil {
					return fmt.Errorf("the dialect calls itself %q and DialectFor does not "+
						"know that name: %w", d.Engine(), err)
				}
				if found.Engine() != d.Engine() {
					return fmt.Errorf("DialectFor(%q) returned the dialect for %q",
						d.Engine(), found.Engine())
				}
				return nil
			},
		},
		{
			Name: "Canonical_IsAProjection",
			Check: func(d Dialect) error {
				// The canonical vocabulary is the Postgres one, so every one
				// of these must come back as itself. Without that law, the
				// classifier's own type lists would be reachable only from
				// Postgres and a second engine would map onto names nothing
				// recognises.
				for _, name := range []string{
					"text", "character varying", "json", "jsonb", "uuid", "bigint",
					"boolean", "timestamp with time zone", "ARRAY", "USER-DEFINED",
					"bytea", "inet", "numeric", "date",
				} {
					once := d.Canonical(name)
					twice := d.Canonical(once)
					if once != twice {
						return fmt.Errorf("Canonical(%q) is %q and Canonical of that is %q, "+
							"so the mapping is not stable and a name that has already been "+
							"canonicalised changes meaning when it is seen again",
							name, once, twice)
					}
				}
				return nil
			},
		},
		{
			Name: "Canonical_LeavesATypeItDoesNotKnowAlone",
			Check: func(d Dialect) error {
				const unknown = "a_type_this_build_has_never_heard_of"
				if got := d.Canonical(unknown); got != unknown {
					return fmt.Errorf("Canonical(%q) is %q, and a type nobody recognises has "+
						"to come back unchanged so the classifier reports it rather than "+
						"deciding something about it", unknown, got)
				}
				return nil
			},
		},
		{
			Name: "QuoteIdent_CannotBeEndedEarly",
			Check: func(d Dialect) error {
				for _, name := range awkwardNames {
					quoted := d.QuoteIdent(name)
					if len(quoted) < len(name)+2 {
						return fmt.Errorf("QuoteIdent(%q) is %q, which is not quoted at all",
							name, quoted)
					}
					delim := quoted[0]
					if quoted[len(quoted)-1] != delim {
						return fmt.Errorf("QuoteIdent(%q) is %q, which does not open and "+
							"close with the same character", name, quoted)
					}
					body := quoted[1 : len(quoted)-1]
					for i := 0; i < len(body); i++ {
						if body[i] != delim {
							continue
						}
						if i+1 >= len(body) || body[i+1] != delim {
							return fmt.Errorf("QuoteIdent(%q) is %q, where a %c inside the "+
								"name is not doubled, so the identifier ends early and "+
								"whatever follows it in the statement is no longer a name",
								name, quoted, delim)
						}
						i++
					}
				}
				return nil
			},
		},
		{
			Name: "Qualify_NamesTheSchemaAndTheTable",
			Check: func(d Dialect) error {
				tp := probeTable()
				got := d.Qualify(tp.Table)
				for _, part := range []string{tp.Table.Schema, tp.Table.Name} {
					if !strings.Contains(got, d.QuoteIdent(part)) {
						return fmt.Errorf("Qualify is %q and does not carry the quoted %q, so "+
							"a statement built from it addresses a table in whichever "+
							"schema the session happens to be in", got, part)
					}
				}
				return nil
			},
		},
		{
			Name: "Placeholder_IsDistinctPerPosition",
			Check: func(d Dialect) error {
				seen := map[string]int{}
				for n := 1; n <= 5; n++ {
					p := d.Placeholder(n)
					if p == "" {
						return fmt.Errorf("Placeholder(%d) is empty, so a value would have to "+
							"be interpolated and a masked value carrying a quote would "+
							"change what the statement means", n)
					}
					if prev, dup := seen[p]; dup {
						return fmt.Errorf("Placeholder(%d) and Placeholder(%d) are both %q, "+
							"so two arguments address one slot and the values land in the "+
							"wrong columns", prev, n, p)
					}
					seen[p] = n
				}
				return nil
			},
		},
		{
			Name: "RowKey_AddressesTheFirstKeyColumn",
			Check: func(d Dialect) error {
				tp := probeTable()
				got := d.RowKey(tp)
				if !strings.Contains(got, d.QuoteIdent(tp.OrderBy[0])) {
					return fmt.Errorf("RowKey is %q and does not name the key column %q, so "+
						"the statement does not say which row it means",
						got, tp.OrderBy[0])
				}
				return nil
			},
		},
		{
			Name: "Update_RewritesEveryPlannedColumn",
			Check: func(d Dialect) error {
				tp := probeTable()
				stmt := d.Update(tp)
				if !strings.Contains(stmt.SQL, d.Qualify(tp.Table)) {
					return fmt.Errorf("the update does not name the table: %s", stmt.SQL)
				}
				if len(stmt.Columns) != len(tp.Columns) {
					return fmt.Errorf("the plan rewrites %d columns and the statement reports "+
						"%d, so the executor sends a different number of values than the "+
						"statement expects", len(tp.Columns), len(stmt.Columns))
				}
				for i, c := range tp.Columns {
					if stmt.Columns[i] != c.Column.Name {
						return fmt.Errorf("the statement reports column %d as %q and the plan "+
							"has %q there; the values are positional, so a different order "+
							"writes each column's value into another column",
							i, stmt.Columns[i], c.Column.Name)
					}
					if !strings.Contains(stmt.SQL, d.QuoteIdent(c.Column.Name)) {
						return fmt.Errorf("the update does not rewrite %q, so that column "+
							"keeps whatever production had in it: %s",
							c.Column.Name, stmt.SQL)
					}
				}
				if !stmt.Keyed {
					return fmt.Errorf("the plan has a key and the statement says it is not " +
						"keyed, so the executor will not checkpoint it and an interrupted " +
						"run starts over")
				}
				return nil
			},
		},
		{
			Name: "Update_SendsEveryValueAsAParameter",
			Check: func(d Dialect) error {
				tp := probeTable()
				stmt := d.Update(tp)
				for n := 1; n <= len(tp.Columns)+1; n++ {
					if !strings.Contains(stmt.SQL, d.Placeholder(n)) {
						return fmt.Errorf("the update has no %s, so argument %d has nowhere "+
							"to land: %s", d.Placeholder(n), n, stmt.SQL)
					}
				}
				if !strings.Contains(stmt.SQL, d.RowKey(tp)) {
					return fmt.Errorf("the update does not compare the row key, so it "+
						"rewrites every row of the table at once: %s", stmt.SQL)
				}
				return nil
			},
		},
		{
			Name: "SelectChunk_ReadsTheKeyAndEveryPlannedColumn",
			Check: func(d Dialect) error {
				tp := probeTable()
				q := d.SelectChunk(tp, "")
				if !strings.Contains(q.SQL, d.RowKey(tp)) {
					return fmt.Errorf("the read does not select the row key, so the update "+
						"that follows has nothing to address a row with: %s", q.SQL)
				}
				for _, c := range tp.Columns {
					if !strings.Contains(q.SQL, d.QuoteIdent(c.Column.Name)) {
						return fmt.Errorf("the read does not select %q, so its transform is "+
							"applied to nothing: %s", c.Column.Name, q.SQL)
					}
				}
				if !strings.Contains(q.SQL, fmt.Sprintf("%d", tp.ChunkSize)) {
					return fmt.Errorf("the read carries no chunk limit, so one statement "+
						"reads the whole table: %s", q.SQL)
				}
				if len(q.Args) != 0 {
					return fmt.Errorf("the first read takes %d arguments and there is no "+
						"resume point yet", len(q.Args))
				}
				return nil
			},
		},
		{
			Name: "SelectChunk_ResumesAfterAKey",
			Check: func(d Dialect) error {
				tp := probeTable()
				const after = "0189-resume-point"
				q := d.SelectChunk(tp, after)
				if len(q.Args) != 1 || q.Args[0] != after {
					return fmt.Errorf("a resumed read takes %v as arguments and it has to "+
						"take exactly the resume point, or the bound is interpolated "+
						"into the text", q.Args)
				}
				if !strings.Contains(q.SQL, d.Placeholder(1)) {
					return fmt.Errorf("a resumed read has no %s, so the resume point has "+
						"nowhere to land: %s", d.Placeholder(1), q.SQL)
				}
				if !strings.Contains(q.SQL, ">") {
					return fmt.Errorf("a resumed read does not bound the key, so it reads "+
						"the rows it has already masked again: %s", q.SQL)
				}
				return nil
			},
		},
		{
			Name: "SelectChunk_OrdersByTheRowKey",
			Check: func(d Dialect) error {
				tp := probeTable()
				q := d.SelectChunk(tp, "")
				at := strings.Index(strings.ToUpper(q.SQL), "ORDER BY")
				if at < 0 {
					return fmt.Errorf("the read has no order, so two chunks can return the "+
						"same row and skip another: %s", q.SQL)
				}
				if !strings.Contains(q.SQL[at:], d.RowKey(tp)) {
					return fmt.Errorf("the read is not ordered by the key it resumes on, so "+
						"the resume point does not mean what the next chunk assumes: %s",
						q.SQL)
				}
				return nil
			},
		},
		{
			Name: "Statements_AreTheSameTextTwice",
			Check: func(d Dialect) error {
				tp := probeTable()
				if a, b := d.Update(tp).SQL, d.Update(tp).SQL; a != b {
					return fmt.Errorf("two updates for one plan differ:\n%s\n%s", a, b)
				}
				if a, b := d.SelectChunk(tp, "x").SQL, d.SelectChunk(tp, "x").SQL; a != b {
					return fmt.Errorf("two reads for one plan differ:\n%s\n%s", a, b)
				}
				return nil
			},
		},
		{
			Name: "Unaddressable_AcceptsATableWithAKey",
			Check: func(d Dialect) error {
				if why := d.Unaddressable(probeTable().Table); why != "" {
					return fmt.Errorf("a table with a primary key is refused as "+
						"unaddressable: %s", why)
				}
				return nil
			},
		},
	}
}

// runDialect is the suite.
func runDialect(t *testing.T, d Dialect) {
	t.Helper()
	for _, b := range dialectBehaviours() {
		t.Run(b.Name, func(t *testing.T) {
			if err := b.Check(d); err != nil {
				t.Errorf("%s: %v", d.Engine(), err)
			}
		})
	}
}

// TestDialects_EveryRegisteredDialectPassesTheSuite is the positive control.
//
// Over the registry rather than over a list written here, so that a dialect
// added later is covered by everything below without anybody remembering to
// add it.
func TestDialects_EveryRegisteredDialectPassesTheSuite(t *testing.T) {
	t.Parallel()
	names := DialectNames()
	if len(names) < 2 {
		t.Fatalf("there are %d dialects registered and a boundary with one implementation "+
			"has never been shown to be a boundary at all: %v", len(names), names)
	}
	for _, name := range names {
		d, err := DialectFor(name)
		if err != nil {
			t.Fatalf("DialectFor(%q): %v", name, err)
		}
		t.Run(name, func(t *testing.T) { runDialect(t, d) })
	}
}

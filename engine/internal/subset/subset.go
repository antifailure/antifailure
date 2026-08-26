// Package subset takes a slice of a database rather than all of it.
//
// A golden the size of production is a golden nobody refreshes, and a golden
// nobody refreshes drifts until it is testing last quarter's schema. So a
// subset starts from a seed, follows the foreign keys, and takes only what the
// seed rows need.
//
// The direction matters and is the thing people get wrong. Following keys
// upward, from a row to what it references, is mandatory: an order whose
// customer is missing is a row that violates its own constraint and a database
// that will not load. Following them downward, from a row to what references
// it, is optional and bounded, because "every order of every customer" is most
// of the database again.
//
// What it cannot do is stated rather than hidden. A relationship expressed in
// application code and not in the schema is invisible here, so the manifest can
// declare one, and a table nothing reaches is reported rather than silently
// emptied.
package subset

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

// Relationship is one table depending on another.
type Relationship struct {
	// From is the table holding the key, To is the table it points at.
	FromTable  string
	FromColumn string
	ToTable    string
	ToColumn   string
	// Virtual marks a relationship the manifest declared rather than one the
	// schema enforces. They are followed identically and reported separately,
	// because a wrong virtual relationship produces a broken subset and the
	// schema cannot catch it.
	Virtual bool
}

// String renders a relationship for a plan.
func (r Relationship) String() string {
	arrow := "->"
	if r.Virtual {
		arrow = "~>"
	}
	return fmt.Sprintf("%s.%s %s %s.%s", r.FromTable, r.FromColumn, arrow, r.ToTable, r.ToColumn)
}

// Config is what the manifest asks for.
type Config struct {
	SeedTable string
	SeedWhere string
	MaxRows   int
	// FollowDependents is how many levels of "things that reference this" to
	// take. Zero takes none, which is the right default: one level from a
	// customer is every order they ever placed.
	FollowDependents int
	Virtual          []Relationship
}

// Plan is the order tables are copied in, and what is taken from each.
type Plan struct {
	// Steps are in dependency order: a table is always copied after
	// everything it references, or the copy violates a constraint.
	Steps []Step
	// Unreachable are tables nothing connects to the seed. They are reported
	// rather than silently emptied, because a table that arrives empty and
	// should not have is a bug somebody finds three days later in a test that
	// returns nothing.
	Unreachable []string
	// Relationships are what was followed, for the explanation.
	Relationships []Relationship
	// Cycles are the foreign key loops that had to be broken, and where. A
	// schema with a cycle cannot be loaded in any order, so one edge is
	// deferred, and saying which is the difference between a subset somebody
	// trusts and one they do not.
	Cycles []string
}

// Step is one table's extraction.
type Step struct {
	Table string
	// Where is the condition, in terms of what has already been copied.
	Where string
	// Reason says in one sentence why this table is included, which is what
	// makes a plan reviewable rather than merely correct.
	Reason string
	// Full marks a table taken whole, which is right for small reference
	// tables and wrong for anything with a customer in it.
	Full bool
}

// Build works out the order and the conditions.
func Build(tables []masking.Table, cfg Config) (Plan, error) {
	if cfg.SeedTable == "" {
		return Plan{}, fmt.Errorf("subset: no seed table is configured, so there is nothing to start from")
	}
	byName := map[string]masking.Table{}
	for _, t := range tables {
		byName[t.Name] = t
		byName[t.String()] = t
	}
	if _, ok := byName[cfg.SeedTable]; !ok {
		return Plan{}, fmt.Errorf(
			"subset: the seed table %q is not in this database; it has %s",
			cfg.SeedTable, describeTables(tables, 8))
	}

	rels := relationships(tables, cfg.Virtual)
	plan := Plan{Relationships: rels}

	// Upward first, and mandatory. An order whose customer is missing is a row
	// that violates its own constraint.
	needed := map[string]string{cfg.SeedTable: seedReason(cfg)}
	frontier := []string{cfg.SeedTable}
	for len(frontier) > 0 {
		var next []string
		for _, table := range frontier {
			for _, r := range rels {
				if r.FromTable != table || needed[r.ToTable] != "" {
					continue
				}
				needed[r.ToTable] = fmt.Sprintf("%s.%s references it, and a row whose reference is missing will not load",
					r.FromTable, r.FromColumn)
				next = append(next, r.ToTable)
			}
		}
		frontier = next
	}

	// Downward, bounded, and optional.
	for level := 0; level < cfg.FollowDependents; level++ {
		var added []string
		for table := range needed {
			for _, r := range rels {
				if r.ToTable != table || needed[r.FromTable] != "" {
					continue
				}
				needed[r.FromTable] = fmt.Sprintf("it references %s, and dependents are followed %s",
					r.ToTable, levels(cfg.FollowDependents))
				added = append(added, r.FromTable)
			}
		}
		if len(added) == 0 {
			break
		}
	}

	ordered, cycles := topological(needed, rels)
	plan.Cycles = cycles

	for _, table := range ordered {
		step := Step{Table: table, Reason: needed[table]}
		switch {
		case table == cfg.SeedTable:
			step.Where = cfg.SeedWhere
		default:
			step.Where = condition(table, rels, needed)
			if step.Where == "" {
				// Nothing narrows it, which is right for a small reference
				// table and is said out loud rather than assumed.
				step.Full = true
			}
		}
		plan.Steps = append(plan.Steps, step)
	}

	for _, t := range tables {
		if needed[t.Name] == "" {
			plan.Unreachable = append(plan.Unreachable, t.String())
		}
	}
	sort.Strings(plan.Unreachable)
	return plan, nil
}

func seedReason(cfg Config) string {
	if cfg.SeedWhere != "" {
		return "it is the seed, narrowed by " + cfg.SeedWhere
	}
	return "it is the seed"
}

func levels(n int) string {
	if n == 1 {
		return "one level"
	}
	return fmt.Sprintf("%d levels", n)
}

// condition narrows a table to rows whose references are already copied.
func condition(table string, rels []Relationship, needed map[string]string) string {
	var parts []string
	for _, r := range rels {
		if r.FromTable != table || needed[r.ToTable] == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s IN (SELECT %s FROM %s)",
			quote(r.FromColumn), quote(r.ToColumn), quote(r.ToTable)))
	}
	sort.Strings(parts)
	// AND rather than OR. A row is taken only when every reference it has is
	// present; taking one whose second reference is missing puts a row in that
	// cannot be loaded.
	return strings.Join(parts, " AND ")
}

// relationships reads the schema's foreign keys and adds the declared ones.
func relationships(tables []masking.Table, virtual []Relationship) []Relationship {
	var out []Relationship
	for _, t := range tables {
		for _, c := range t.Columns {
			if c.ForeignKey == "" {
				continue
			}
			parts := strings.Split(c.ForeignKey, ".")
			if len(parts) != 3 {
				continue
			}
			out = append(out, Relationship{
				FromTable: t.Name, FromColumn: c.Name,
				ToTable: parts[1], ToColumn: parts[2],
			})
		}
	}
	for _, v := range virtual {
		v.Virtual = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// topological orders tables so each comes after what it references, and
// reports the cycles it had to break.
func topological(needed map[string]string, rels []Relationship) ([]string, []string) {
	names := make([]string, 0, len(needed))
	for n := range needed {
		names = append(names, n)
	}
	// Sorted first, so the order is the same on every run and a plan can be
	// compared with the last one.
	sort.Strings(names)

	state := map[string]int{}
	var out []string
	var cycles []string

	var visit func(name string, path []string)
	visit = func(name string, path []string) {
		switch state[name] {
		case 2:
			return
		case 1:
			// A loop. Every schema with one has to break it somewhere, and
			// saying where is the difference between a subset somebody trusts
			// and one they do not.
			cycles = append(cycles, strings.Join(append(path, name), " -> "))
			return
		}
		state[name] = 1
		var deps []string
		for _, r := range rels {
			if r.FromTable == name && needed[r.ToTable] != "" && r.ToTable != name {
				deps = append(deps, r.ToTable)
			}
		}
		sort.Strings(deps)
		for _, d := range deps {
			visit(d, append(path, name))
		}
		state[name] = 2
		out = append(out, name)
	}
	for _, n := range names {
		visit(n, nil)
	}
	return out, cycles
}

// SQL renders the statement that copies one step.
func (s Step) SQL(limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SELECT * FROM %s", quote(s.Table))
	if s.Where != "" {
		fmt.Fprintf(&b, " WHERE %s", s.Where)
	}
	if limit > 0 && !s.Full {
		// The limit applies to the seed and to anything narrowed, and not to a
		// small reference table taken whole, where cutting it off would leave
		// dangling references.
		fmt.Fprintf(&b, " LIMIT %d", limit)
	}
	return b.String()
}

// Explain renders a plan for a person.
func (p Plan) Explain() string {
	var b strings.Builder
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s.Table)
		fmt.Fprintf(&b, "   %s\n", s.Reason)
		if s.Full {
			b.WriteString("   taken whole\n")
		} else if s.Where != "" {
			fmt.Fprintf(&b, "   where %s\n", s.Where)
		}
	}
	if len(p.Cycles) > 0 {
		b.WriteString("\nForeign key cycles, broken at the first repeated table:\n")
		for _, c := range p.Cycles {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	}
	if len(p.Unreachable) > 0 {
		b.WriteString("\nNothing connects these to the seed, so they arrive empty:\n")
		for _, t := range p.Unreachable {
			fmt.Fprintf(&b, "  %s\n", t)
		}
		b.WriteString("\nIf one of them should have rows, the relationship is in application code\n")
		b.WriteString("rather than in the schema. Declare it under subset.virtual_relationships.\n")
	}
	return b.String()
}

func describeTables(tables []masking.Table, limit int) string {
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

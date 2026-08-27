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
// of the database again. The two interleave rather than run once each, because
// a table pulled in downward brings its own upward requirements with it, and a
// closure that ran upward only at the start would leave them unmet.
//
// What it cannot do is stated rather than hidden. A relationship expressed in
// application code and not in the schema is invisible here, so the manifest can
// declare one, and a table nothing reaches is reported rather than silently
// emptied.
//
// The selection is computed entirely on the source, in one read-only
// transaction, as a chain of materialized common table expressions: the seed's
// rows are computed once and every descendant narrows against that result
// rather than against the source table. That is the difference between a
// subset and a filter that quietly matches everything, and it is why nothing
// here writes to the source, not even a temporary table.
package subset

import (
	"fmt"
	"sort"
	"strings"
)

// Config is what the manifest asks for.
type Config struct {
	SeedTable string
	SeedWhere string
	// MaxRows caps what is taken from any one narrowed table. Zero means no cap.
	MaxRows int
	// FollowDependents is how many levels of "things that reference this" to
	// take. Zero takes none, which is the right default: one level from a
	// customer is every order they ever placed.
	FollowDependents int
	Virtual          []ForeignKey
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
	// Relationships are every key considered, for the explanation.
	Relationships []ForeignKey
	// Deferred are the keys no copy order can satisfy: self references, and
	// one edge of every cycle. They are not conditions on the selection; they
	// are repaired after the load, and Repairs records what that cost.
	Deferred []ForeignKey
	// Cycles are the foreign key loops that had to be broken, and where. A
	// schema with a cycle cannot be loaded in any order, so one edge is
	// deferred, and saying which is the difference between a subset somebody
	// trusts and one they do not.
	Cycles []string
	// Warnings are things worth knowing that are not reasons to refuse.
	Warnings []string
	// Sequences are the sequences that have to be moved past what was copied.
	Sequences []Sequence
	// MaxRows is the budget the plan was built with, carried so a plan can be
	// executed without its config.
	MaxRows int
}

// Step is one table's extraction.
type Step struct {
	Table Table
	// Alias is this step's name as a common table expression, so that a later
	// step narrows against this step's result rather than against the whole
	// source table.
	Alias string
	// Reason says in one sentence why this table is included, which is what
	// makes a plan reviewable rather than merely correct.
	Reason string
	// Conditions are the predicates that narrow it, combined with AND. A row
	// is taken only when every reference it has is present; taking one whose
	// second reference is missing puts a row in that cannot be loaded.
	Conditions []string
	// Refs are the indexes of the steps this step's conditions name.
	Refs []int
	// Full marks a table taken whole, which is right for a small reference
	// table and wrong for anything with a customer in it.
	Full bool
	// Order is the columns that make truncation deterministic. Empty when the
	// table has no primary key, in which case no budget is applied either,
	// because a budget with no order takes different rows on every run.
	Order []string
	// Limit is the row budget for this table, zero for none.
	Limit int
	// Project is what the step's common table expression selects: the columns
	// a later step needs from it. A wide table's every column would otherwise
	// be materialized to answer a question about its key.
	Project []string
}

// Build works out the order and the conditions.
func Build(cat *Catalog, cfg Config) (Plan, error) {
	seed, err := cat.TableNamed(cfg.SeedTable)
	if err != nil {
		return Plan{}, err
	}

	keys := relationships(cat, cfg.Virtual)
	plan := Plan{Relationships: keys, MaxRows: cfg.MaxRows}

	needed := map[string]string{seed.Ref(): seedReason(cfg)}
	closeUpward(keys, needed)
	for level := 0; level < cfg.FollowDependents; level++ {
		if !addDependents(keys, needed, level+1, cfg.FollowDependents) {
			break
		}
		// A table pulled in downward has upward requirements of its own. A
		// closure that ran upward only once would take an order's line items
		// and not the product each one names.
		closeUpward(keys, needed)
	}

	ordered, cycles := topological(needed, keys)
	plan.Cycles = cycles

	index := map[string]int{}
	for i, ref := range ordered {
		index[ref] = i
	}
	// What each table's children will ask it for, so a step materializes the
	// key rather than the row.
	project := map[string]map[string]bool{}
	for _, k := range keys {
		if needed[k.From] == "" || needed[k.To] == "" {
			continue
		}
		if project[k.To] == nil {
			project[k.To] = map[string]bool{}
		}
		for _, c := range k.ToColumns {
			project[k.To][c] = true
		}
	}

	var problems []string
	for i, ref := range ordered {
		table, ok := cat.Table(ref)
		if !ok {
			continue
		}
		step := Step{
			Table:  table,
			Alias:  fmt.Sprintf("af_s%d", i),
			Reason: needed[ref],
		}
		if ref == seed.Ref() && strings.TrimSpace(cfg.SeedWhere) != "" {
			step.Conditions = append(step.Conditions, "("+cfg.SeedWhere+")")
		}
		for _, k := range keys {
			if k.From != ref || needed[k.To] == "" {
				continue
			}
			parent, isEarlier := index[k.To]
			if !isEarlier || parent >= i {
				// Nothing copied yet can answer this: it is a self reference,
				// or the edge of a cycle that had to be broken. It becomes a
				// repair rather than a condition.
				plan.Deferred = append(plan.Deferred, k)
				continue
			}
			step.Conditions = append(step.Conditions, condition(k, ordered[parent], plan.aliasAt(parent)))
			step.Refs = appendUnique(step.Refs, parent)
		}
		sort.Strings(step.Conditions)
		sort.Ints(step.Refs)

		step.Full = len(step.Conditions) == 0
		step.Order = table.PrimaryKey
		switch {
		case step.Full:
			// No budget. Cutting off a table nothing narrows leaves dangling
			// references in everything that points at it, and a reference
			// table is small precisely because it is a reference table.
			if cfg.MaxRows > 0 && table.Rows > int64(cfg.MaxRows) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf(
					"%s is taken whole because nothing narrows it, and it has about %d rows",
					ref, table.Rows))
			}
		case len(table.PrimaryKey) == 0:
			// Narrowed but with no key to order by. The selection is still
			// deterministic, because the condition is; the budget is not, so
			// there is none, and that is said rather than assumed.
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"%s has no primary key, so the row budget does not apply to it: "+
					"a budget with no order takes different rows on every run", ref))
		default:
			step.Limit = cfg.MaxRows
		}

		if step.Full && len(table.PrimaryKey) == 0 && cfg.MaxRows > 0 && table.Rows > int64(cfg.MaxRows) {
			problems = append(problems, fmt.Sprintf(
				"%s has no primary key and about %d rows, and nothing narrows it. "+
					"It cannot be taken whole within a budget of %d and it cannot be "+
					"truncated to one, because there is no order that would take the "+
					"same rows twice. Give it a primary key, or narrow it by declaring "+
					"a relationship under subset.virtual_relationships",
				ref, table.Rows, cfg.MaxRows))
		}

		for _, col := range sortedKeys(project[ref]) {
			step.Project = appendUniqueString(step.Project, col)
		}
		for _, col := range table.PrimaryKey {
			step.Project = appendUniqueString(step.Project, col)
		}
		if len(step.Project) == 0 {
			// Nothing asks this table for anything, so its result is never
			// selected from. One constant column keeps the expression legal.
			step.Project = nil
		}
		plan.Steps = append(plan.Steps, step)
	}

	for _, t := range cat.Tables {
		if needed[t.Ref()] == "" {
			plan.Unreachable = append(plan.Unreachable, t.Ref())
		}
	}
	sort.Strings(plan.Unreachable)
	plan.Sequences = sequencesFor(cat, needed)
	sort.Slice(plan.Deferred, func(i, j int) bool {
		return plan.Deferred[i].String() < plan.Deferred[j].String()
	})

	if len(problems) > 0 {
		sort.Strings(problems)
		return plan, fmt.Errorf("subset: this plan cannot be run:\n  %s",
			strings.Join(problems, "\n  "))
	}
	return plan, nil
}

func (p Plan) aliasAt(i int) string { return fmt.Sprintf("af_s%d", i) }

// closeUpward takes everything the current selection references, to a fixed
// point. It is mandatory: a row whose reference is missing will not load.
func closeUpward(keys []ForeignKey, needed map[string]string) {
	for {
		grew := false
		for _, k := range keys {
			if needed[k.From] == "" || needed[k.To] != "" {
				continue
			}
			needed[k.To] = fmt.Sprintf(
				"%s(%s) references it, and a row whose reference is missing will not load",
				k.From, strings.Join(k.FromColumns, ", "))
			grew = true
		}
		if !grew {
			return
		}
	}
}

// addDependents takes one level of things that reference the selection.
func addDependents(keys []ForeignKey, needed map[string]string, level, of int) bool {
	var added []string
	for _, k := range keys {
		if needed[k.To] == "" || needed[k.From] != "" {
			continue
		}
		added = append(added, k.From)
		needed[k.From] = fmt.Sprintf("it references %s, and dependents are followed %s",
			k.To, levels(of))
	}
	return len(added) > 0
}

func seedReason(cfg Config) string {
	if strings.TrimSpace(cfg.SeedWhere) != "" {
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

// condition narrows a table to rows whose reference is in what was already
// selected from the parent, and not to rows whose reference is in the parent
// table as a whole, which is every row and therefore no condition at all.
//
// A null reference is kept. Postgres treats NULL IN (...) as unknown rather
// than true, so the obvious form of this predicate drops every row whose
// optional reference is not set, which is a subset that silently loses the
// rows nobody thought about. A foreign key with a null column is satisfied
// under MATCH SIMPLE, which is the default and what almost every schema uses,
// so those rows belong in the subset.
//
// A reference that IS set and points outside the slice excludes the row, and
// that is a decision rather than an accident, so it is worth being able to
// disagree with knowingly. The alternative, for an optional reference, would
// be to keep the row and clear the link, which loses less. It is not done
// because the same rule has to hold for the key that pulled a table into the
// subset in the first place: following dependents from customers to orders
// takes the orders of those customers precisely because customer_id narrows
// it, and a rule that stopped narrowing on optional keys would copy the whole
// orders table and then clear most of it. Excluding the row is the version
// that cannot accidentally copy a table the subset exists to avoid copying.
// In practice the case is rare, because the closure has already brought the
// parent in, and it only bites when the parent was narrowed differently.
func condition(k ForeignKey, parentRef, alias string) string {
	var nulls []string
	for _, c := range k.FromColumns {
		nulls = append(nulls, quote(c)+" IS NULL")
	}
	from := strings.Join(quoteAll(k.FromColumns), ", ")
	to := strings.Join(quoteAll(k.ToColumns), ", ")
	if len(k.FromColumns) > 1 {
		from = "(" + from + ")"
	}
	_ = parentRef
	in := fmt.Sprintf("%s IN (SELECT %s FROM %s)", from, to, alias)
	return "(" + strings.Join(append(nulls, in), " OR ") + ")"
}

// relationships takes the schema's foreign keys and adds the declared ones.
func relationships(cat *Catalog, virtual []ForeignKey) []ForeignKey {
	out := append([]ForeignKey(nil), cat.Keys...)
	for _, v := range virtual {
		v.Virtual = true
		if v.Name == "" {
			v.Name = "virtual"
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// topological orders tables so each comes after what it references, and
// reports the cycles it had to break.
func topological(needed map[string]string, keys []ForeignKey) ([]string, []string) {
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
	seen := map[string]bool{}

	var visit func(name string, path []string)
	visit = func(name string, path []string) {
		switch state[name] {
		case 2:
			return
		case 1:
			// A loop. Every schema with one has to break it somewhere, and
			// saying where is the difference between a subset somebody trusts
			// and one they do not.
			line := strings.Join(append(append([]string(nil), path...), name), " -> ")
			if !seen[line] {
				seen[line] = true
				cycles = append(cycles, line)
			}
			return
		}
		state[name] = 1
		var deps []string
		for _, k := range keys {
			if k.From == name && needed[k.To] != "" && k.To != name {
				deps = appendUniqueString(deps, k.To)
			}
		}
		sort.Strings(deps)
		for _, d := range deps {
			visit(d, append(append([]string(nil), path...), name))
		}
		state[name] = 2
		out = append(out, name)
	}
	for _, n := range names {
		visit(n, nil)
	}
	sort.Strings(cycles)
	return out, cycles
}

// sequencesFor returns the sequences belonging to tables in the selection,
// plus every standalone sequence, which belongs to nothing and so cannot be
// left behind on the strength of its table not being copied.
func sequencesFor(cat *Catalog, needed map[string]string) []Sequence {
	var out []Sequence
	for _, s := range cat.Seqs {
		if s.Table == "" || needed[s.Table] != "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// Query renders the statement that selects one step's rows from the source,
// with every step it depends on materialized ahead of it.
//
// Materialized rather than left to the planner: an inlined common table
// expression is re-evaluated per reference and, worse, can be pushed into the
// outer query in a form the row budget does not survive. MATERIALIZED is the
// difference between the seed being chosen once and being chosen again for
// every descendant, which for a LIMIT means a different thousand rows each time.
func (p Plan) Query(i int) string {
	step := p.Steps[i]
	var b strings.Builder
	if prefix := p.with(i); prefix != "" {
		b.WriteString(prefix)
	}
	b.WriteString(selectFrom(step, quoteAll(step.Table.Writable()), step.Table.Qualified()))
	return b.String()
}

// with renders the common table expressions one step needs, transitively, in
// dependency order.
func (p Plan) with(i int) string {
	needed := map[int]bool{}
	var walk func(int)
	walk = func(n int) {
		for _, r := range p.Steps[n].Refs {
			if !needed[r] {
				needed[r] = true
				walk(r)
			}
		}
	}
	walk(i)
	if len(needed) == 0 {
		return ""
	}
	idx := sortedInts(needed)
	parts := make([]string, 0, len(idx))
	for _, n := range idx {
		s := p.Steps[n]
		parts = append(parts, fmt.Sprintf("%s AS MATERIALIZED (%s)",
			s.Alias, selectFrom(s, quoteAll(s.Project), s.Table.Qualified())))
	}
	return "WITH " + strings.Join(parts, ",\n     ") + "\n"
}

func selectFrom(s Step, columns []string, from string) string {
	cols := "*"
	if len(columns) > 0 {
		cols = strings.Join(columns, ", ")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", cols, from)
	if len(s.Conditions) > 0 {
		fmt.Fprintf(&b, " WHERE %s", strings.Join(s.Conditions, " AND "))
	}
	if s.Limit > 0 && len(s.Order) > 0 {
		fmt.Fprintf(&b, " ORDER BY %s LIMIT %d", strings.Join(quoteAll(s.Order), ", "), s.Limit)
	}
	return b.String()
}

// Explain renders a plan for a person.
func (p Plan) Explain() string {
	var b strings.Builder
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s.Table.Ref())
		fmt.Fprintf(&b, "   %s\n", s.Reason)
		switch {
		case s.Full:
			b.WriteString("   taken whole\n")
		case len(s.Conditions) > 0:
			fmt.Fprintf(&b, "   where %s\n", strings.Join(s.Conditions, " AND "))
		}
		if s.Limit > 0 {
			fmt.Fprintf(&b, "   at most %d rows, ordered by %s\n",
				s.Limit, strings.Join(s.Order, ", "))
		}
	}
	if len(p.Cycles) > 0 {
		b.WriteString("\nForeign key cycles, broken at the first repeated table:\n")
		for _, c := range p.Cycles {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	}
	if len(p.Deferred) > 0 {
		b.WriteString("\nThese keys cannot be satisfied by copy order, so they are checked\n")
		b.WriteString("after the load and repaired where they do not resolve:\n")
		for _, k := range p.Deferred {
			fmt.Fprintf(&b, "  %s\n", k)
		}
	}
	if len(p.Warnings) > 0 {
		b.WriteString("\nWorth knowing:\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "  %s\n", w)
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

func appendUnique(items []int, v int) []int {
	for _, i := range items {
		if i == v {
			return items
		}
	}
	return append(items, v)
}

func appendUniqueString(items []string, v string) []string {
	for _, i := range items {
		if i == v {
			return items
		}
	}
	return append(items, v)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedInts(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

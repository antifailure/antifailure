// Package sqlload rehearses a concurrent SQL workload against the branch.
//
// THE QUESTION THIS PACKAGE ANSWERS, AND WHY NOTHING ELSE IN THE ENGINE DID.
//
// Everything this engine sent at a preview environment before this package
// went over HTTP. The mix compiles from OTLP or an access log and sends
// requests; a scenario is an ordered journey of requests; a workflow is a
// browser. All three reach the database only through the application, so the
// number they report is the application's latency with the database somewhere
// inside it. That is the right measurement for an application change and the
// wrong one for a database change. A person changing an index, a lock, a
// storage parameter or a query wants to know what happened to transactions per
// second and to statement latency, and the HTTP path can only answer that
// through whatever the application happens to do on the route they can reach.
//
// So this package opens connections to the branch and runs statements on them.
// N clients, each on its own connection, each running whole transactions, with
// think time between them and a seed that makes two runs execute the same
// sequence.
//
// WHY THIS IS WRITTEN IN GO RATHER THAN SHELLING OUT TO pgbench.
//
// pgbench is the obvious tool and it is the wrong one here, for three reasons
// that are about this product rather than about pgbench.
//
// It may not be installed. It ships in postgresql-contrib, which a hosted
// runner image does not carry, and a workload that reports "pgbench not found"
// on the machine a customer runs it on is a feature that exists in the
// documentation only. This package owns its own execution and so cannot have
// that failure.
//
// Its output would have to be scraped. pgbench prints a human report, and a
// result assembled by parsing another program's prose is a result that changes
// shape when that program is upgraded. Every other kind of workload in this
// engine projects into one Result the control plane stores and compare.go
// differences, and a fifth kind that arrived as parsed text could not join
// them.
//
// And its script language is its own. A workload derived from
// pg_stat_statements carries parameter types the server reported, and turning
// those into a pgbench script means generating a second language, writing it
// to a temporary file, and hoping the quoting survived. Binding the values
// directly is fewer moving parts and no quoting at all.
//
// WHAT A MIX IS, AND WHY THERE ARE TWO WAYS TO GET ONE.
//
// A Mix is a weighted set of transactions. A transaction is an ordered list of
// statements that run inside one BEGIN and COMMIT, because that is the unit a
// database's throughput is measured in and because a lock held across two
// statements is the thing worth rehearsing.
//
// The declared way is a document in the repository: the author writes the
// statements and says where their parameter values come from. It is exact and
// it is the only way to rehearse a write path honestly, because the author is
// the only one who knows which values are legal.
//
// The derived way reads pg_stat_statements on the branch and takes the
// statements that actually ran, weighted by how often they ran. That is the
// difference between a benchmark and a rehearsal: the mix is the customer's
// own traffic rather than a shape somebody invented. What it CANNOT do is
// recover the parameter values, because pg_stat_statements stores the
// normalised text with every literal replaced. So the derived path asks the
// server for the parameter TYPES, by preparing each statement, and generates
// values of those types from the seed. The consequence is stated in the report
// rather than hidden: a selective predicate filled with a generated value may
// match no rows, so a derived run measures the plan, the locks and the
// storage engine faithfully and does not measure the result set size.
//
// Which is also why the derived path refuses a write by default. A generated
// value in a WHERE clause selects nothing; a generated value in a SET clause
// writes nonsense. Reads are taken, writes are refused unless the manifest
// says otherwise, and everything else is refused always. The vocabulary is
// deliberately the one load.Shape.Safe already uses for routes, because it is
// the same decision about the same kind of risk.
package sqlload

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// Source names where a mix came from. Carried into the result, so a reader can
// tell production's own statements from a document somebody wrote.
const (
	// SourceDeclared is a document in the repository.
	SourceDeclared = "declared"
	// SourceStatementStatistics is pg_stat_statements on the branch.
	SourceStatementStatistics = "statement_statistics"
)

// Mix is the weighted set of transactions a run executes.
type Mix struct {
	// Source is SourceDeclared or SourceStatementStatistics.
	Source string `json:"source"`
	// Transactions are what a client picks between.
	Transactions []Transaction `json:"transactions"`
	// Refused is every statement that was read and not taken, one entry each.
	//
	// Never null. An empty list and an unasked question are different answers:
	// a derived mix with no refusals means every statement in the database was
	// taken, and a nil one would let a report say the same thing about a mix
	// that was never filtered at all.
	Refused []Refused `json:"refused"`
	// Skipped counts statements the reader saw and did not consider, with the
	// reason. Separate from Refused because a statement this engine itself ran
	// is noise rather than a decision about the customer's traffic.
	Skipped map[string]int `json:"skipped,omitempty"`
}

// Transaction is one unit of work: an ordered list of statements that run
// inside one BEGIN and COMMIT.
type Transaction struct {
	// Name identifies it in a report and on the command line's --only.
	Name string `json:"name"`
	// Weight is how often it is picked, relative to the others. For a derived
	// mix it is the call count pg_stat_statements reported.
	Weight float64 `json:"weight"`
	// Statements run in order on one connection.
	Statements []Statement `json:"statements"`
	// BaselineMeanMs is what this transaction's statements cost on the
	// database the mix was read from, summed. Set only for a derived mix,
	// where pg_stat_statements carries it.
	//
	// HasBaseline rather than a zero test, because a statement that really
	// does take no measurable time and a statement nothing measured are
	// different facts and a threshold that cannot tell them apart reports the
	// second as a pass.
	BaselineMeanMs float64 `json:"baseline_mean_ms,omitempty"`
	HasBaseline    bool    `json:"has_baseline"`
}

// Statement is one statement and where its parameter values come from.
type Statement struct {
	// Label identifies the statement in a report. For a derived statement it
	// is built from the statement itself, so two reports of the same database
	// name the same statement the same way.
	Label string `json:"label"`
	// SQL is sent to the server unchanged. Parameters are $1, $2 and so on,
	// bound rather than interpolated, so a generated value can never become
	// syntax.
	SQL string `json:"sql"`
	// Params fill $1 upward, in order.
	Params []Param `json:"params,omitempty"`
	// Write says the statement changes data. Recorded rather than inferred at
	// execution time so a report can say what a run was allowed to do.
	Write bool `json:"write"`

	// ref is this statement's identity, shared by pointer so a client can
	// publish what it is executing for the price of one atomic store. Filled
	// once before the run starts, the way a query parameter's pool is, and
	// unexported because it is bookkeeping rather than part of the mix
	// anybody writes or reads back.
	ref *stmtRef
}

// ParamKind is where one parameter's values come from.
type ParamKind string

const (
	// ParamInt draws a whole number from a declared range.
	ParamInt ParamKind = "int"
	// ParamText picks from a declared list of strings.
	ParamText ParamKind = "text"
	// ParamQuery draws from the values a query returned when the run started.
	//
	// The one that turns a benchmark into a rehearsal: an id drawn from the
	// table is an id that exists, so the statement reads a row rather than
	// proving that an empty result is fast.
	ParamQuery ParamKind = "query"
	// ParamGenerated is the derived path's parameter: a value of the type the
	// server reported for this position, drawn from the seed.
	ParamGenerated ParamKind = "generated"
)

// Param is one bound value.
type Param struct {
	Kind ParamKind `json:"kind"`
	// Min and Max bound a ParamInt, inclusive.
	Min int64 `json:"min,omitempty"`
	Max int64 `json:"max,omitempty"`
	// Values are a ParamText's choices.
	Values []string `json:"values,omitempty"`
	// Query is a ParamQuery's source. Its first column becomes the pool.
	Query string `json:"query,omitempty"`
	// Type is the Postgres type name a ParamGenerated produces, as the server
	// spelled it.
	Type string `json:"type,omitempty"`

	// pool holds what Query returned, filled once when the run starts.
	pool []any
}

// Refused is one statement that was read and not taken.
type Refused struct {
	// Statement is the normalised text, truncated, so a person can tell which
	// one it was without the report carrying a megabyte of SQL.
	Statement string `json:"statement"`
	// Code is the stable identifier for why.
	Code string `json:"code"`
	// Reason is one sentence.
	Reason string `json:"reason"`
}

// The reasons a statement is refused. Stable strings, because a console groups
// on them and a report is read for the shape of the refusals as much as for
// their count.
const (
	// RefusedWrite is a write in a derived mix that did not ask for writes.
	RefusedWrite = "write_not_allowed"
	// RefusedNotDataChanging is DDL, a utility statement, or anything that is
	// not a query. Refused under every setting.
	RefusedNotDataChanging = "not_a_query"
	// RefusedUnpreparable is a statement the server would not prepare, which
	// means it does not parse against this branch's schema.
	RefusedUnpreparable = "will_not_prepare"
	// RefusedParameterType is a parameter whose type this package cannot
	// generate a value for.
	RefusedParameterType = "parameter_type"
)

// validate reports whether a mix can be run.
//
// Checked before a connection is opened, so a mix with a transaction that has
// no statements fails as a configuration problem rather than as a run that
// produced nothing.
//
// Unexported, and that is a decision rather than an oversight. The two ways a
// mix comes into existence, ParseScript and Derive, both run it, and Run runs
// it again on whatever it is handed. An exported third door would be one
// nothing outside this package has a reason to open, and a function with no
// caller is the shape this repository keeps finding in itself.
func (m *Mix) validate() error {
	if m == nil || len(m.Transactions) == 0 {
		return fmt.Errorf("the mix holds no transactions")
	}
	seen := map[string]bool{}
	for i, tx := range m.Transactions {
		name := strings.TrimSpace(tx.Name)
		if name == "" {
			return fmt.Errorf("transaction %d has no name", i+1)
		}
		if seen[name] {
			return fmt.Errorf("two transactions are both called %q, so a report could not tell them apart", name)
		}
		seen[name] = true
		if len(tx.Statements) == 0 {
			return fmt.Errorf("the transaction %q holds no statements", name)
		}
		if tx.Weight < 0 {
			return fmt.Errorf("the transaction %q has a negative weight", name)
		}
		for j, st := range tx.Statements {
			if strings.TrimSpace(st.SQL) == "" {
				return fmt.Errorf("statement %d of %q is empty", j+1, name)
			}
			if err := st.validateParams(name, j+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s Statement) validateParams(tx string, position int) error {
	for i, p := range s.Params {
		switch p.Kind {
		case ParamInt:
			if p.Max < p.Min {
				return fmt.Errorf("parameter %d of statement %d of %q has a max below its min",
					i+1, position, tx)
			}
		case ParamText:
			if len(p.Values) == 0 {
				return fmt.Errorf("parameter %d of statement %d of %q is a text parameter with no values",
					i+1, position, tx)
			}
		case ParamQuery:
			if strings.TrimSpace(p.Query) == "" {
				return fmt.Errorf("parameter %d of statement %d of %q is a query parameter with no query",
					i+1, position, tx)
			}
		case ParamGenerated:
			if strings.TrimSpace(p.Type) == "" {
				return fmt.Errorf("parameter %d of statement %d of %q is generated and names no type",
					i+1, position, tx)
			}
		default:
			return fmt.Errorf("parameter %d of statement %d of %q has no kind", i+1, position, tx)
		}
	}
	return nil
}

// Select narrows a mix to the named transactions.
//
// A name that matches nothing is an error rather than an empty mix. A run that
// selected a misspelled transaction and sent nothing reports as a run that
// found no problems, which is the silent green this product exists to stop.
func (m *Mix) Select(names []string) (*Mix, error) {
	if len(names) == 0 {
		return m, nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	out := &Mix{Source: m.Source, Refused: m.Refused, Skipped: m.Skipped}
	for _, tx := range m.Transactions {
		if want[tx.Name] {
			out.Transactions = append(out.Transactions, tx)
			delete(want, tx.Name)
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for n := range want {
			missing = append(missing, n)
		}
		sort.Strings(missing)
		// The names that DO exist, beside the one that does not. A selection
		// that matched nothing is usually a typo or a renamed transaction, and
		// the answer to both is the list, which the person asking does not have
		// in front of them.
		return nil, fmt.Errorf("the mix holds no transaction called %s; it holds %s",
			strings.Join(quoteAll(missing), ", "), strings.Join(quoteAll(m.Names()), ", "))
	}
	return out, nil
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return out
}

// Names is every transaction's name, in the order the mix holds them.
func (m *Mix) Names() []string {
	out := make([]string, 0, len(m.Transactions))
	for _, tx := range m.Transactions {
		out = append(out, tx.Name)
	}
	return out
}

// picker chooses transactions according to their weights.
//
// The same construction load.Picker uses for routes, and for the same reason:
// two runs of one mix with one seed must pick the same sequence, or a
// comparison between them is a comparison of two different workloads. The
// transactions are sorted by name first so that a mix assembled in a different
// order still picks the same sequence.
//
// It holds no generator of its own, and that is the difference from
// load.Picker. A client draws its transaction and its parameter values from
// ONE stream, so the two cannot be correlated with each other, which two
// generators seeded from the same number are: their first draws are the same
// uniform value. The cost of one stream is that anything taking an extra draw
// moves everything after it, which is exactly why a transaction's values are
// bound once rather than again on every retry.
type picker struct {
	transactions []Transaction
	cumulative   []float64
	total        float64
}

func newPicker(txs []Transaction) (*picker, error) {
	if len(txs) == 0 {
		return nil, fmt.Errorf("sqlload: there are no transactions to run")
	}
	sorted := append([]Transaction(nil), txs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	p := &picker{transactions: sorted}
	for _, tx := range sorted {
		w := tx.Weight
		if w <= 0 {
			// A transaction with no weight is still work, just the least of
			// it. Dropping it would silently remove something somebody
			// declared, and a mix that runs fewer transactions than it lists
			// is a mix whose report is wrong about what it measured.
			w = 0.0001
		}
		p.total += w
		p.cumulative = append(p.cumulative, p.total)
	}
	return p, nil
}

func (p *picker) next(rng *rand.Rand) Transaction {
	target := rng.Float64() * p.total
	i := sort.SearchFloat64s(p.cumulative, target)
	if i >= len(p.transactions) {
		i = len(p.transactions) - 1
	}
	return p.transactions[i]
}

// truncate shortens a statement for a report.
const maxStatementInReport = 300

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxStatementInReport {
		return s
	}
	return s[:maxStatementInReport] + "..."
}

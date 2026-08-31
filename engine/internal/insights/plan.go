package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Plan is what Postgres said it would do with one statement.
type Plan struct {
	// Statement is the normalised query the plan is for.
	Statement string `json:"statement"`
	// Cost is the planner's estimate of the total cost of the whole plan.
	Cost float64 `json:"cost"`
	// Rows is the planner's estimate of rows returned.
	Rows float64 `json:"rows"`
	// SeqScans are the tables read end to end, with the planner's row estimate
	// for each.
	SeqScans map[string]float64 `json:"seq_scans,omitempty"`
	// IndexScans are the indexes used, by the table they belong to.
	IndexScans map[string][]string `json:"index_scans,omitempty"`
	// Error is why the statement could not be explained. A query referring to
	// a table this branch does not have is normal on one side of a diff and
	// is not a failure of the diff.
	Error string `json:"error,omitempty"`
}

// PlanFinding is one query whose plan got worse.
type PlanFinding struct {
	// Kind is what changed.
	Kind PlanChange `json:"kind"`
	// Statement is the query, normalised.
	Statement string `json:"statement"`
	// Table is what the change is about, where it is about one table.
	Table string `json:"table,omitempty"`
	// Rows is how many rows that table holds, which is what separates a
	// sequential scan that is the right plan from one that is an outage.
	Rows int64 `json:"rows,omitempty"`
	// Detail says what changed, in words.
	Detail string `json:"detail"`
	// Before and After are the total cost estimate on each side.
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

// PlanChange names a way a plan can get worse.
type PlanChange string

const (
	// PlanNewSeqScan is a table now read end to end that was not before.
	PlanNewSeqScan PlanChange = "new_sequential_scan"
	// PlanLostIndex is an index the plan used before and does not now.
	PlanLostIndex PlanChange = "index_no_longer_used"
	// PlanCostUp is a cost estimate that grew by more than the factor.
	PlanCostUp PlanChange = "cost_increase"
)

// PlanChanges is every way a plan can get worse, and it is the closed set the
// prose describing plan_regression has to cover.
//
// It exists because the manifest schema's description of plan_regression named
// two of these three and cost_increase appeared nowhere a user could read. A
// promise phrase followed by a gloss of part of a set is worse than no gloss:
// a reader who sees a colon and two items reasonably concludes the two are the
// list, and then does not know why their build failed on the third.
//
// TestSchemaDescribesEveryPlanChange holds the description to this slice, so
// the prose cannot fall behind. A constant added to the type and not to this
// slice is invisible to that test, because Go cannot enumerate the constants
// of a string type at run time; tools/constcheck reads the const block above
// through go/ast and holds the count stated in the prose, which is the half
// that closes it.
var PlanChanges = []PlanChange{PlanNewSeqScan, PlanLostIndex, PlanCostUp}

// PlanTitle is how one kind of plan regression is put to a person.
//
// Exported so that the test holding the manifest schema's prose to this set
// can join the two on the same phrases a report shows, rather than on a second
// copy of the wording that would drift from the first.
func PlanTitle(k PlanChange) string { return planTitle(k) }

// CapturePlans explains each statement and reads back the structure.
//
// ANALYZE runs first. Two branches of the same golden hold identical data and
// can still plan differently, because a freshly created branch has no
// statistics until something gathers them, and a planner with no statistics
// guesses. Comparing a guess to a measurement is the single easiest way to
// produce a plan diff full of findings that mean nothing.
func CapturePlans(ctx context.Context, conn *pgx.Conn, statements []string) ([]Plan, error) {
	if _, err := conn.Exec(ctx, "ANALYZE"); err != nil {
		return nil, err
	}
	options := "FORMAT JSON, COSTS true"
	if generic, err := supportsGenericPlan(ctx, conn); err == nil && generic {
		options += ", GENERIC_PLAN true"
	}
	out := make([]Plan, 0, len(statements))
	for _, s := range statements {
		out = append(out, explainOne(ctx, conn, options, s))
	}
	return out, nil
}

// supportsGenericPlan reports whether the server understands GENERIC_PLAN.
//
// This is the option that makes a plan diff possible at all. The statements
// come from pg_stat_statements, which normalises every literal to $1, and
// before Postgres 16 there was no way to explain a statement with an unbound
// parameter: the planner refused with "there is no parameter $1". GENERIC_PLAN
// asks for the plan the server would cache for a prepared statement, which is
// also the plan production actually runs for a parameterised query, so it is
// the right thing to compare as well as the only thing available.
func supportsGenericPlan(ctx context.Context, conn *pgx.Conn) (bool, error) {
	// current_setting rather than SHOW, because SHOW answers as text and
	// scanning text into an int fails, which reported "this server is too old"
	// for a Postgres 17 and cost the plan diff every parameterised statement
	// it exists to read.
	var version int
	if err := conn.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return false, err
	}
	return version >= 160000, nil
}

// explainOne runs EXPLAIN without ANALYZE, so nothing is executed.
//
// That matters more than it looks. The statements come from what the
// application ran, which includes UPDATE and DELETE, and EXPLAIN ANALYZE would
// run them. It also means the numbers are estimates, which is the right thing
// to compare: the question is what the planner decided, not what the machine
// happened to be busy with.
func explainOne(ctx context.Context, conn *pgx.Conn, options, statement string) Plan {
	p := Plan{Statement: normalise(statement)}

	// The raw protocol call, deliberately, and this took three attempts to get
	// right. pgx's default mode prepares the statement, sees the $1 in it and
	// refuses before the server is ever asked: "expected 1 arguments, got 0".
	// Its simple protocol mode is no better, because that one interpolates the
	// arguments into the SQL itself and refuses the same way: "insufficient
	// arguments". Both are pgx protecting the caller from a mistake this is
	// not. GENERIC_PLAN is the SERVER's answer to an unbound parameter, so the
	// text has to reach the server unexamined, and PgConn().Exec is the only
	// call that sends it that way. Without this the plan diff cannot explain a
	// single statement out of pg_stat_statements, since every literal in one
	// has been normalised to $1.
	results, err := conn.PgConn().Exec(ctx, "EXPLAIN ("+options+") "+statement).ReadAll()
	if err != nil {
		p.Error = short(err)
		return p
	}
	var body []byte
	for _, r := range results {
		if r.Err != nil {
			p.Error = short(r.Err)
			return p
		}
		if len(r.Rows) > 0 && len(r.Rows[0]) > 0 {
			body = r.Rows[0][0]
		}
	}
	if len(body) == 0 {
		p.Error = "the planner returned nothing"
		return p
	}

	var explained []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(body, &explained); err != nil || len(explained) == 0 {
		p.Error = "the plan could not be read"
		return p
	}
	root := explained[0].Plan
	p.Cost = root.TotalCost
	p.Rows = root.PlanRows
	p.SeqScans = map[string]float64{}
	p.IndexScans = map[string][]string{}
	walk(&root, &p, "")
	if len(p.SeqScans) == 0 {
		p.SeqScans = nil
	}
	if len(p.IndexScans) == 0 {
		p.IndexScans = nil
	}
	return p
}

// planNode is the part of EXPLAIN's JSON this needs. Postgres emits far more
// and adds fields between versions, so only the stable ones are named.
type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	TotalCost    float64    `json:"Total Cost"`
	PlanRows     float64    `json:"Plan Rows"`
	Plans        []planNode `json:"Plans"`
}

// walk collects the scans, carrying the enclosing relation down the tree.
//
// The relation has to come from above because a Bitmap Index Scan does not
// name one: the table is on its parent Bitmap Heap Scan and only the index is
// on the node itself. Reading the index name as though it were the table put
// every bitmap plan under a key no table has, so a query that reached a table
// by index looked like a query that never touched it, and the finding this
// whole diff exists for went missing on exactly the plan shape Postgres
// chooses for a moderately selective predicate.
func walk(n *planNode, p *Plan, parent string) {
	relation := n.RelationName
	if relation == "" {
		relation = parent
	}
	switch n.NodeType {
	case "Seq Scan", "Parallel Seq Scan":
		if relation != "" {
			p.SeqScans[relation] = n.PlanRows
		}
	case "Index Scan", "Index Only Scan", "Bitmap Index Scan",
		"Parallel Index Scan", "Parallel Index Only Scan":
		if n.IndexName != "" && relation != "" {
			p.IndexScans[relation] = append(p.IndexScans[relation], n.IndexName)
		}
	}
	for i := range n.Plans {
		walk(&n.Plans[i], p, relation)
	}
}

// PlanOptions are the thresholds a plan diff uses.
type PlanOptions struct {
	// LargeTableRows is the size above which a new sequential scan is a
	// finding. Below it a sequential scan is usually the right plan and
	// reporting one is the noise that gets a check turned off.
	LargeTableRows int
	// CostFactor is how much a cost estimate has to grow to be reported.
	CostFactor float64
	// Rows is live rows per table, for putting a size against a finding.
	Rows map[string]int64
}

// DiffPlans reports the plans that got worse.
//
// Structural first, cost second. A cost estimate is a number the planner made
// up from statistics and it moves for reasons nobody changed; a sequential
// scan appearing where an index scan was is a decision, and decisions are
// what a reviewer can act on.
func DiffPlans(base, branch []Plan, opts PlanOptions) []PlanFinding {
	if opts.CostFactor <= 0 {
		opts.CostFactor = 2
	}
	if opts.LargeTableRows <= 0 {
		opts.LargeTableRows = LargeTableRows
	}
	before := map[string]Plan{}
	for _, p := range base {
		before[p.Statement] = p
	}

	var out []PlanFinding
	for _, after := range branch {
		prior, ok := before[after.Statement]
		if !ok || after.Error != "" || prior.Error != "" {
			continue
		}

		for table, rows := range after.SeqScans {
			if _, wasScanned := prior.SeqScans[table]; wasScanned {
				continue
			}
			live := opts.Rows[table]
			if live < int64(opts.LargeTableRows) {
				continue
			}
			detail := fmt.Sprintf(
				"%s is now read end to end. It was reached by index before, and it holds "+
					"about %d rows, so the planner expects to look at %.0f of them.",
				table, live, rows)
			if idx, had := prior.IndexScans[table]; had {
				detail += " The index it used to use is " + strings.Join(idx, ", ") + "."
			}
			out = append(out, PlanFinding{
				Kind: PlanNewSeqScan, Statement: after.Statement, Table: table,
				Rows: live, Detail: detail, Before: prior.Cost, After: after.Cost,
			})
		}

		for table, indexes := range prior.IndexScans {
			for _, idx := range indexes {
				if usesIndex(after.IndexScans[table], idx) {
					continue
				}
				if _, nowScanned := after.SeqScans[table]; nowScanned {
					// Already reported above as a sequential scan, which says
					// the same thing more usefully.
					continue
				}
				out = append(out, PlanFinding{
					Kind: PlanLostIndex, Statement: after.Statement, Table: table,
					Rows: opts.Rows[table],
					Detail: fmt.Sprintf(
						"%s is no longer used for %s. Either it was dropped, or the WHERE "+
							"clause changed shape so that it no longer matches the index's "+
							"leading columns.", idx, table),
					Before: prior.Cost, After: after.Cost,
				})
			}
		}

		if prior.Cost > 0 && after.Cost/prior.Cost >= opts.CostFactor {
			out = append(out, PlanFinding{
				Kind: PlanCostUp, Statement: after.Statement,
				Detail: fmt.Sprintf(
					"The planner's estimate went from %.0f to %.0f, a factor of %.1f. A cost "+
						"estimate is not a measurement, so this is worth looking at rather "+
						"than worth acting on by itself.",
					prior.Cost, after.Cost, after.Cost/prior.Cost),
				Before: prior.Cost, After: after.Cost,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		return planSeverity(out[i].Kind) < planSeverity(out[j].Kind)
	})
	return out
}

func planSeverity(k PlanChange) int {
	switch k {
	case PlanNewSeqScan:
		return 0
	case PlanLostIndex:
		return 1
	default:
		return 2
	}
}

func usesIndex(have []string, want string) bool {
	for _, h := range have {
		if h == want {
			return true
		}
	}
	return false
}

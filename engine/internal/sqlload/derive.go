package sqlload

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Deriving a mix from pg_stat_statements is what makes this a rehearsal rather
// than a benchmark, and this file is where the honesty about it lives.
//
// WHAT THE SERVER GIVES US. pg_stat_statements holds every statement the
// database ran, normalised, with the number of calls and the mean time. The
// call count is a weight that nobody had to invent, and the mean is a baseline
// to compare the rehearsal against. Both come from the customer's own traffic.
//
// WHAT IT DOES NOT GIVE US, AND WHY THAT DECIDES THE DESIGN. Normalisation
// replaces every literal with a placeholder, so the values are gone. There is
// no way to recover the id that was read or the amount that was written. Two
// consequences follow and neither is hidden:
//
// A write cannot be replayed. A generated value in a SET clause writes
// nonsense, and a generated value in a WHERE clause of a DELETE either deletes
// nothing or deletes the wrong row. So a derived write is refused unless the
// manifest asks for it, exactly as load.Shape.Safe refuses a route that
// mutates state unless it was declared.
//
// A read is replayed with a value of the right TYPE and not the right value.
// The type is not guessed: the statement is prepared on the branch and the
// server reports what it inferred. So the plan, the locks, the buffer traffic
// and the storage engine are exercised faithfully, and the result set size is
// not. A run against a selective predicate may return no rows. The report says
// so rather than letting somebody read an empty scan as a fast one.
//
// Preparing every candidate has a second use that is worth as much as the
// first: a statement that will not prepare does not parse against THIS
// branch's schema. A column the change renamed, a function it dropped, a type
// it altered, all show up here as a refusal naming the server's own message,
// before a single transaction runs.

// DeriveOptions configure reading a mix from the server.
type DeriveOptions struct {
	// MaxStatements caps how many statements the mix holds. The tail of
	// pg_stat_statements is one call apiece and adding it makes a mix that
	// takes longer to set up than to run.
	MaxStatements int
	// Writes allows statements that change data. Off by default.
	Writes bool
	// MinCalls drops statements that ran fewer times than this. One call is
	// not a shape, and a migration's own DDL is exactly the thing that ran
	// once.
	MinCalls int64
}

// Defaults for a derived mix.
const (
	defaultMaxStatements = 20
	defaultMinCalls      = 2
	// maxLabel bounds the readable name built from a statement. Long enough to
	// tell two statements on one table apart, short enough to sit in a table
	// cell and on a command line.
	maxLabel = 70
)

// Derive reads the statements the branch actually ran and builds a mix.
//
// One statement per transaction, because that is what pg_stat_statements
// records: it counts statements, and which of them shared a transaction is not
// in the view. Inventing a grouping would be inventing the part of the shape
// that matters most, so the derived mix says plainly that each statement is
// its own transaction and the declared path is where a multi statement
// transaction comes from.
func Derive(ctx context.Context, conn *pgx.Conn, opts DeriveOptions) (*Mix, error) {
	if opts.MaxStatements <= 0 {
		opts.MaxStatements = defaultMaxStatements
	}
	if opts.MinCalls <= 0 {
		opts.MinCalls = defaultMinCalls
	}

	rows, err := readStatements(ctx, conn, opts.MinCalls)
	if err != nil {
		return nil, aferrors.Coded(aferrors.AFLOD019, "detail", short(err))
	}

	mix := &Mix{Source: SourceStatementStatistics, Refused: []Refused{}, Skipped: map[string]int{}}
	used := map[string]bool{}
	for _, row := range rows {
		if len(mix.Transactions) >= opts.MaxStatements {
			mix.Skipped["beyond the statement limit"]++
			continue
		}
		if reason := skipReason(row.text); reason != "" {
			mix.Skipped[reason]++
			continue
		}

		kind := classifyStatement(row.text)
		switch {
		case kind == statementOther:
			mix.Refused = append(mix.Refused, Refused{
				Statement: truncate(row.text), Code: RefusedNotDataChanging,
				Reason: "only a query can be prepared and replayed, and this is not one",
			})
			continue
		case kind == statementWrite && !opts.Writes:
			mix.Refused = append(mix.Refused, Refused{
				Statement: truncate(row.text), Code: RefusedWrite,
				Reason: "the statement changes data and its values were normalised away, so " +
					"replaying it would write values nobody chose",
			})
			continue
		}

		types, err := parameterTypes(ctx, conn, row.text)
		if err != nil {
			mix.Refused = append(mix.Refused, Refused{
				Statement: truncate(row.text), Code: RefusedUnpreparable,
				Reason: "the server would not prepare it against this branch: " + short(err),
			})
			continue
		}

		params, bad := generatorsFor(types)
		if bad != "" {
			mix.Refused = append(mix.Refused, Refused{
				Statement: truncate(row.text), Code: RefusedParameterType,
				Reason: "no value of type " + bad + " can be generated, so this statement " +
					"cannot be replayed without the values that were normalised away",
			})
			continue
		}

		name := uniqueName(label(row.text), row.id, used)
		mix.Transactions = append(mix.Transactions, Transaction{
			Name:           name,
			Weight:         float64(row.calls),
			BaselineMeanMs: row.meanMs,
			HasBaseline:    true,
			Statements: []Statement{{
				Label: name, SQL: row.text, Params: params,
				Write: kind == statementWrite,
			}},
		})
	}

	if len(mix.Transactions) == 0 {
		return nil, aferrors.Coded(aferrors.AFLOD020,
			"detail", describeEmpty(rows, mix))
	}
	if len(mix.Skipped) == 0 {
		mix.Skipped = nil
	}
	return mix, nil
}

// statementRow is one row of pg_stat_statements.
type statementRow struct {
	id     int64
	text   string
	calls  int64
	meanMs float64
}

// readStatements pulls the busiest statements, ordered by how often they ran.
//
// By calls rather than by total time, and the difference is the whole point of
// this path. insights.collectQueries orders by total time because it is
// hunting the one slow statement. A workload is shaped by what runs OFTEN: the
// statement that ran four hundred thousand times is the load, and ordering by
// total time would put a nightly report at the top of a mix meant to reproduce
// a Tuesday afternoon.
func readStatements(ctx context.Context, conn *pgx.Conn, minCalls int64) ([]statementRow, error) {
	// The dbid predicate is load bearing and it was not obvious. The view is
	// CLUSTER wide: it returns every statement every database on the server
	// ran, with a dbid column saying which. A branch on a shared server would
	// otherwise derive its workload from its neighbours' traffic, produce
	// statements against tables it does not have, and report them as the
	// customer's own shape. Found by reading a real view on a server with
	// several databases on it, rather than from the documentation.
	const query = `
SELECT COALESCE(queryid, 0), query, calls, mean_exec_time
FROM pg_stat_statements
WHERE calls >= $1
  AND dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY calls DESC
LIMIT 500`

	rows, err := conn.Query(ctx, query, minCalls)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []statementRow
	for rows.Next() {
		var r statementRow
		if err := rows.Scan(&r.id, &r.text, &r.calls, &r.meanMs); err != nil {
			return nil, err
		}
		r.text = strings.TrimSpace(r.text)
		out = append(out, r)
	}
	return out, rows.Err()
}

// skipReason names statements that are the engine's own noise rather than the
// customer's traffic, so they are counted and left out rather than refused.
//
// A refusal is a decision about the customer's workload and a skip is not, and
// putting the engine's own catalogue reads in the refusal list would bury the
// one refusal a person needs to see under forty they can do nothing about.
func skipReason(text string) string {
	upper := strings.ToUpper(text)
	switch {
	case strings.HasPrefix(upper, "BEGIN"), strings.HasPrefix(upper, "COMMIT"),
		strings.HasPrefix(upper, "ROLLBACK"), strings.HasPrefix(upper, "DEALLOCATE"),
		strings.HasPrefix(upper, "PREPARE"), strings.HasPrefix(upper, "SET "),
		strings.HasPrefix(upper, "SHOW "), strings.HasPrefix(upper, "DISCARD"):
		return "session control rather than work"
	case strings.Contains(upper, "OPERATOR(PG_CATALOG."),
		strings.Contains(upper, "FOR KEY SHARE OF"),
		strings.Contains(upper, "FOR NO KEY UPDATE OF"):
		// The statement a foreign key trigger runs to check a referenced row
		// still exists. It is the busiest statement in most databases, by a
		// wide margin, and it is not the application's: replaying it would
		// spend the whole run measuring the integrity check rather than the
		// query that caused it.
		return "an internal referential integrity check rather than a statement the application sent"
	case strings.Contains(upper, "PG_STAT_STATEMENTS"),
		strings.Contains(upper, "PG_STAT_ACTIVITY"),
		strings.Contains(upper, "PG_CATALOG."),
		strings.Contains(upper, "INFORMATION_SCHEMA."),
		strings.Contains(upper, "PG_STAT_USER_"),
		strings.Contains(upper, "PG_PREPARED_STATEMENTS"):
		return "a read of the catalogue, which is this engine's own traffic"
	}
	return ""
}

// The three things a statement can be.
type statementKind int

const (
	statementRead statementKind = iota
	statementWrite
	statementOther
)

// dataChanging matches the verbs that write, as whole words.
//
// A word boundary rather than a substring, because a column called updated_at
// contains UPDATE and a table called deletions contains DELETE, and a
// classifier that read either as a write would refuse every read in the
// database.
var dataChanging = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE|TRUNCATE)\b`)

// classifyStatement says whether replaying a statement would change data.
func classifyStatement(text string) statementKind {
	upper := strings.ToUpper(strings.TrimSpace(text))
	switch {
	case strings.HasPrefix(upper, "SELECT"), strings.HasPrefix(upper, "TABLE "),
		strings.HasPrefix(upper, "VALUES"):
		// A plain SELECT cannot change data. A SELECT FOR UPDATE takes locks
		// and is still a read, which is exactly the statement a concurrent
		// rehearsal wants to include.
		return statementRead
	case strings.HasPrefix(upper, "WITH"):
		// A common table expression can carry a writing statement inside it,
		// and a WITH that does is a write however it ends. Nothing short of
		// parsing the statement can tell which, so the presence of a writing
		// verb anywhere in it decides, which errs towards refusing a read
		// rather than towards replaying a write.
		if dataChanging.MatchString(upper) {
			return statementWrite
		}
		return statementRead
	case strings.HasPrefix(upper, "INSERT"), strings.HasPrefix(upper, "UPDATE"),
		strings.HasPrefix(upper, "DELETE"), strings.HasPrefix(upper, "MERGE"):
		return statementWrite
	}
	return statementOther
}

// probeName is the prepared statement this package uses to ask the server what
// a statement's parameters are. One name, deallocated after each use, so a
// derivation that fails halfway leaves nothing behind on the connection.
const probeName = "af_sqlload_probe"

// parameterTypes asks the server what types a statement's parameters are.
//
// Prepared rather than guessed from the text. The alternative is to read
// "WHERE id = $1" and decide that id is an integer, which is a guess about the
// schema made by something that has the schema right there. The server infers
// the types from the catalogue, so a uuid primary key comes back as a uuid and
// a citext column comes back as citext and is then honestly refused.
func parameterTypes(ctx context.Context, conn *pgx.Conn, sql string) ([]string, error) {
	// Deallocated first rather than only afterwards: a previous probe that
	// failed between PREPARE and DEALLOCATE would otherwise make every
	// statement after it fail as a duplicate name.
	_, _ = conn.Exec(ctx, "DEALLOCATE "+probeName)
	if _, err := conn.Exec(ctx, "PREPARE "+probeName+" AS "+sql); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "DEALLOCATE "+probeName) }()

	var types []string
	err := conn.QueryRow(ctx,
		`SELECT parameter_types::text[] FROM pg_prepared_statements WHERE name = $1`,
		probeName).Scan(&types)
	if err != nil {
		return nil, err
	}
	return types, nil
}

// generatorsFor turns the server's parameter types into value generators, or
// names the first type it cannot.
func generatorsFor(types []string) ([]Param, string) {
	if len(types) == 0 {
		return nil, ""
	}
	out := make([]Param, 0, len(types))
	for _, t := range types {
		name := strings.ToLower(strings.TrimSpace(t))
		if !canGenerate(name) {
			return nil, name
		}
		out = append(out, Param{Kind: ParamGenerated, Type: name})
	}
	return out, ""
}

// label builds a readable name from a statement.
func label(text string) string {
	one := strings.Join(strings.Fields(text), " ")
	if len(one) <= maxLabel {
		return one
	}
	return one[:maxLabel] + "..."
}

// uniqueName keeps two statements with the same opening words apart.
//
// The queryid is appended rather than a counter, because a counter would
// depend on the order the rows arrived in and two reports of one database
// would then name the same statement differently.
func uniqueName(base string, id int64, used map[string]bool) string {
	name := base
	if used[name] {
		name = base + " #" + strconv.FormatInt(id, 10)
	}
	for used[name] {
		name += "+"
	}
	used[name] = true
	return name
}

// describeEmpty says why a derivation produced nothing, in terms of what it
// saw rather than as a bare refusal.
//
// The three reasons are different problems with different fixes: an empty view
// means nothing has run against the branch yet, all refused means the traffic
// is writes this run was not allowed to replay, and all skipped means the only
// statements recorded were the engine's own.
func describeEmpty(rows []statementRow, mix *Mix) string {
	skipped := 0
	for _, n := range mix.Skipped {
		skipped += n
	}
	switch {
	case len(rows) == 0:
		return "pg_stat_statements recorded no statement that ran more than once, so " +
			"nothing has exercised this branch yet"
	case len(mix.Refused) > 0:
		return fmt.Sprintf("every one of the %d statements recorded was refused: %s",
			len(rows), refusalSummary(mix.Refused))
	default:
		return fmt.Sprintf("all %d statements recorded were this engine's own catalogue reads", skipped)
	}
}

func refusalSummary(refused []Refused) string {
	counts := map[string]int{}
	for _, r := range refused {
		counts[r.Code]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return strings.Join(parts, ", ")
}

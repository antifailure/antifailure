// Package insights reads what the database noticed while the environment ran.
//
// The bugs it looks for are the ones no test catches, because the test passes:
// the endpoint that now runs four hundred queries instead of two, the index
// that stopped being used, the sequential scan on a table that grew. Each of
// those is correct and slow, and correct and slow is what takes a site down
// under load rather than in review.
//
// It is read only and it says what it could not see. pg_stat_statements is an
// extension somebody has to install, and an insight that silently reports
// nothing because the extension is missing is worse than one that says so:
// the first looks like a clean bill of health.
package insights

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Report is what the database noticed.
type Report struct {
	// Queries are the statements that ran, worst first.
	Queries []Query `json:"queries"`
	// Unused are indexes nothing read, which is disk and write cost for
	// nothing. Reported after a real workload, not after a smoke test, and
	// the caller is told which it was.
	Unused []Index `json:"unused_indexes"`
	// Scans are sequential scans on tables large enough to matter.
	Scans []Scan `json:"sequential_scans"`
	// Missing says what could not be read, and why. An insight that silently
	// reports nothing looks like a clean bill of health.
	Missing []string `json:"missing,omitempty"`
}

// Query is one statement the database ran.
type Query struct {
	// ID is Postgres's own queryid: a hash of the parsed statement, which is
	// what makes two reports comparable.
	//
	// The text cannot do that job, and this is not a theoretical worry. The
	// same statement, run four times and then four hundred, came back from
	// pg_stat_statements as "... LIMIT $2" the first time and "... LIMIT 1"
	// the second, because the stored text is whatever source was current when
	// the entry was created and a prepared statement is not re-parsed on
	// every execution. Matching on text reported the busiest query in the
	// database as a brand new one and found no N+1 at all, which is the
	// single finding the whole feature exists for. The queryid was identical
	// across both.
	ID int64 `json:"id,omitempty"`
	// Text is the normalised statement, with its parameters replaced.
	Text string `json:"text"`
	// Calls is how many times it ran.
	Calls int64 `json:"calls"`
	// TotalMs and MeanMs are what it cost.
	TotalMs float64 `json:"total_ms"`
	MeanMs  float64 `json:"mean_ms"`
	// Rows is how many rows it returned in total.
	Rows int64 `json:"rows"`
}

// Index is one index and whether anything read it.
type Index struct {
	Table string `json:"table"`
	Name  string `json:"name"`
	Scans int64  `json:"scans"`
	Size  string `json:"size"`
}

// Scan is a table being read sequentially.
type Scan struct {
	Table string `json:"table"`
	// SeqScans is how many times it was read end to end.
	SeqScans int64 `json:"seq_scans"`
	// IndexScans is how many times an index was used instead.
	IndexScans int64 `json:"index_scans"`
	// LiveRows is roughly how many rows it holds, which decides whether a
	// sequential scan is a problem: on a table of forty rows it is the right
	// plan and flagging it is noise.
	LiveRows int64 `json:"live_rows"`
}

// key is what two reports match a statement on.
//
// The queryid where there is one, because the text of the same statement is
// not stable across resets. The text where there is not, which is a server
// too old to report one or a row the extension could not attribute.
func (q Query) key() string {
	if q.ID != 0 {
		return "id:" + strconv.FormatInt(q.ID, 10)
	}
	return "text:" + q.Text
}

// Ratio is the share of reads that went end to end.
func (s Scan) Ratio() float64 {
	total := s.SeqScans + s.IndexScans
	if total == 0 {
		return 0
	}
	return float64(s.SeqScans) / float64(total)
}

// minRowsForScanConcern is the size below which a sequential scan is the right
// plan and flagging it is noise somebody learns to ignore.
const minRowsForScanConcern = 1000

// Collect reads the statistics a database has gathered.
func Collect(ctx context.Context, conn *pgx.Conn, limit int) (Report, error) {
	if limit <= 0 {
		limit = 20
	}
	var report Report

	queries, err := collectQueries(ctx, conn, limit)
	if err != nil {
		// The extension is the common reason and it is not a failure. Saying
		// so is the whole point: silence here reads as nothing to report.
		report.Missing = append(report.Missing,
			"query statistics need the pg_stat_statements extension, which is not available here: "+
				short(err))
	} else {
		report.Queries = queries
	}

	if unused, err := collectUnusedIndexes(ctx, conn); err != nil {
		report.Missing = append(report.Missing, "index usage could not be read: "+short(err))
	} else {
		report.Unused = unused
	}

	if scans, err := collectScans(ctx, conn); err != nil {
		report.Missing = append(report.Missing, "table access could not be read: "+short(err))
	} else {
		report.Scans = scans
	}
	return report, nil
}

func collectQueries(ctx context.Context, conn *pgx.Conn, limit int) ([]Query, error) {
	// Ordered by total time rather than by mean. A query taking two
	// milliseconds four hundred times is the N+1 worth finding, and ordering
	// by mean buries it under one slow report nobody runs.
	const query = `
SELECT COALESCE(queryid, 0), query, calls, total_exec_time, mean_exec_time, rows
FROM pg_stat_statements
WHERE query NOT LIKE '%pg_stat_statements%'
  AND query NOT LIKE 'COMMIT%' AND query NOT LIKE 'BEGIN%'
ORDER BY total_exec_time DESC
LIMIT $1`

	rows, err := conn.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Query
	for rows.Next() {
		var q Query
		if err := rows.Scan(&q.ID, &q.Text, &q.Calls, &q.TotalMs, &q.MeanMs, &q.Rows); err != nil {
			return nil, err
		}
		q.Text = normalise(q.Text)
		out = append(out, q)
	}
	return out, rows.Err()
}

func collectUnusedIndexes(ctx context.Context, conn *pgx.Conn) ([]Index, error) {
	// Primary keys and unique constraints are excluded. They exist to enforce
	// a rule rather than to be read, and reporting one as unused is advice
	// that would break the schema if taken.
	const query = `
SELECT s.relname, s.indexrelname, s.idx_scan,
       pg_size_pretty(pg_relation_size(s.indexrelid))
FROM pg_stat_user_indexes s
JOIN pg_index i ON i.indexrelid = s.indexrelid
WHERE s.idx_scan = 0 AND NOT i.indisunique AND NOT i.indisprimary
ORDER BY pg_relation_size(s.indexrelid) DESC
LIMIT 20`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Index
	for rows.Next() {
		var idx Index
		if err := rows.Scan(&idx.Table, &idx.Name, &idx.Scans, &idx.Size); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

func collectScans(ctx context.Context, conn *pgx.Conn) ([]Scan, error) {
	const query = `
SELECT relname, seq_scan, COALESCE(idx_scan, 0), n_live_tup
FROM pg_stat_user_tables
WHERE seq_scan > 0
ORDER BY seq_scan DESC
LIMIT 40`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Scan
	for rows.Next() {
		var s Scan
		if err := rows.Scan(&s.Table, &s.SeqScans, &s.IndexScans, &s.LiveRows); err != nil {
			return nil, err
		}
		if s.LiveRows < minRowsForScanConcern {
			// On a small table a sequential scan is the right plan, and
			// flagging it is noise somebody learns to ignore.
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ratio() > out[j].Ratio() })
	return out, rows.Err()
}

// Diff compares two reports and reports what got worse.
//
// A comparison rather than a number, because "this endpoint runs 412 queries"
// means nothing without "and it ran 4 before". The absolute figure is what a
// dashboard shows; the change is what a pull request needs.
type Diff struct {
	// NewQueries are statements this branch runs and the baseline did not.
	NewQueries []Query `json:"new_queries"`
	// Busier are statements this branch runs far more often.
	Busier []Change `json:"busier"`
	// Slower are statements whose mean time grew.
	Slower []Change `json:"slower"`
}

// Change is one statement that got worse.
type Change struct {
	Text   string  `json:"text"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Factor float64 `json:"factor"`
}

// Thresholds decide what counts as a regression.
type Thresholds struct {
	// CallGrowth is how many times more often a statement has to run.
	CallGrowth float64
	// TimeGrowth is how many times slower each call has to get.
	TimeGrowth float64
	// MinMS is the smallest absolute change worth reporting, in
	// milliseconds. It is the manifest's regression_min_ms and it exists
	// because a query going from 0.1ms to 0.3ms is three times slower and
	// means nothing. Without a floor the report is all noise, and a report
	// that is all noise is one people stop reading, which is worse than not
	// having it.
	MinMS float64
}

// CompareTo reports what got worse against a baseline.
//
// The thresholds are ratios rather than absolutes for the same reason the load
// report uses them: a query going from two milliseconds to eight is a
// quadrupling worth seeing, and six milliseconds of change is not. The
// absolute floor is the other half of the same idea, applied from the other
// end.
func (r Report) CompareTo(baseline Report, t Thresholds) Diff {
	if t.CallGrowth <= 0 {
		t.CallGrowth = 2
	}
	if t.TimeGrowth <= 0 {
		t.TimeGrowth = 1.5
	}

	before := map[string]Query{}
	for _, q := range baseline.Queries {
		before[q.key()] = q
	}

	var diff Diff
	for _, q := range r.Queries {
		prior, seen := before[q.key()]
		if !seen {
			diff.NewQueries = append(diff.NewQueries, q)
			continue
		}
		if prior.Calls > 0 && float64(q.Calls)/float64(prior.Calls) >= t.CallGrowth {
			diff.Busier = append(diff.Busier, Change{
				Text: q.Text, Before: float64(prior.Calls), After: float64(q.Calls),
				Factor: float64(q.Calls) / float64(prior.Calls),
			})
		}
		if prior.MeanMs > 0 && q.MeanMs/prior.MeanMs >= t.TimeGrowth &&
			q.MeanMs-prior.MeanMs >= t.MinMS {
			diff.Slower = append(diff.Slower, Change{
				Text: q.Text, Before: prior.MeanMs, After: q.MeanMs,
				Factor: q.MeanMs / prior.MeanMs,
			})
		}
	}
	sort.Slice(diff.Busier, func(i, j int) bool { return diff.Busier[i].Factor > diff.Busier[j].Factor })
	sort.Slice(diff.Slower, func(i, j int) bool { return diff.Slower[i].Factor > diff.Slower[j].Factor })
	return diff
}

// Empty reports whether a diff found anything.
func (d Diff) Empty() bool {
	return len(d.NewQueries) == 0 && len(d.Busier) == 0 && len(d.Slower) == 0
}

// normalise makes a statement readable in a table.
func normalise(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	const max = 160
	if len(q) > max {
		q = q[:max-1] + "…"
	}
	return q
}

func short(err error) string {
	text := err.Error()
	if i := strings.Index(text, "\n"); i > 0 {
		text = text[:i]
	}
	const max = 120
	if len(text) > max {
		text = text[:max] + "…"
	}
	return text
}

// Explain renders a report for a person.
func (r Report) Explain() string {
	var b strings.Builder
	if len(r.Queries) > 0 {
		b.WriteString("Queries, by total time:\n")
		for _, q := range r.Queries {
			fmt.Fprintf(&b, "  %6d calls  %8.1fms total  %6.2fms each  %s\n",
				q.Calls, q.TotalMs, q.MeanMs, q.Text)
		}
		b.WriteString("\n")
	}
	if len(r.Scans) > 0 {
		b.WriteString("Tables read end to end:\n")
		for _, s := range r.Scans {
			fmt.Fprintf(&b, "  %-28s %d sequential, %d by index, about %d rows\n",
				s.Table, s.SeqScans, s.IndexScans, s.LiveRows)
		}
		b.WriteString("\n")
	}
	if len(r.Unused) > 0 {
		b.WriteString("Indexes nothing read:\n")
		for _, i := range r.Unused {
			fmt.Fprintf(&b, "  %-28s %s on %s\n", i.Name, i.Size, i.Table)
		}
		b.WriteString("\nAn index nothing read costs disk and slows every write. If the workload\n")
		b.WriteString("that would use it did not run here, that is the answer rather than a finding.\n\n")
	}
	for _, m := range r.Missing {
		fmt.Fprintf(&b, "Not measured: %s\n", m)
	}
	return b.String()
}

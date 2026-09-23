package workload

import (
	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// One side of a two build comparison, when the workload is the SQL one.
//
// WHY THIS EXISTS AT ALL. `af load compare` could compare the HTTP mix and
// nothing else, so the sentence "the same workload on both builds, same data
// and concurrency, compare throughput and latency distribution" was true of
// API traffic and false of the PostgreSQL workload, which is the half a person
// changing a storage engine actually cares about. The differencing, the
// thresholds, the round against round interval and the verdict were all
// already written and all already kind agnostic. The only missing piece was a
// side of the comparison built from a SQL run.
//
// WHAT THE UNIT OF COMPARISON IS, AND WHY IT IS TWO THINGS. An HTTP comparison
// has one natural row, the route. A SQL comparison has two, and reporting
// either alone loses the finding:
//
//   - the TRANSACTION, because that is what throughput is counted in and what
//     a lock is held across. A transaction whose p99 doubled while its p50 held
//     is a lock or a checkpoint, and no statement row says so on its own.
//   - the STATEMENT, because that is the row somebody who changed an index
//     reads, and a transaction's latency is the sum of several of them.
//
// They cannot collide, and that is a property of the keys rather than a hope.
// A transaction row is (scenario "", route <name>) and a statement row is
// (scenario <transaction>, route <label>), so a transaction and a statement
// can never produce the same key however either is named. It also makes the
// scope strings read the way the units nest: "checkout" is the transaction and
// "checkout select_items" is a statement inside it.
//
// THE STATEMENT ROWS ARE NOT BUILT HERE. They come from projectSQL, the same
// function the hosted `af workload sql` run projects with, called rather than
// copied. A second projection that drifted from the first would make a
// comparison of two documents that no longer describe the same thing, which is
// the reason ProjectLoad exists in the shape it does.

// ProjectSQLLoadOptions identify one side of a two build SQL comparison.
//
// The same shape ProjectLoadOptions carries, with this workload's own two
// thresholds in place of the mix's.
type ProjectSQLLoadOptions struct {
	// EnvID and Branch name the environment the transactions ran against.
	EnvID  string
	Branch string
	// Command is the plain invocation that reproduces this one side, and
	// ManifestDigest is the manifest it was run under. Compare reads both.
	Command        string
	ManifestDigest string
	// MeanIncrease and ErrorRate are the manifest's SINGLE RUN limits, the
	// ones under load.sql.thresholds. They are carried per side, exactly as
	// ProjectLoad carries the mix's, so that a verdict TRANSITION between the
	// two sides is visible: a run whose error_rate passed on the base branch
	// and fails on this one is a finding, and it is a different fact from any
	// delta under load.comparison.thresholds, which Judge evaluates over both
	// sides at once.
	//
	// mean_increase needs a baseline from pg_stat_statements, so a declared
	// mix carries none. That is not a reason to withhold it: sqlThresholds
	// answers with an unverified row and the reason, which is the honest
	// answer, and withholding it would report a run as clean over a limit
	// nothing was ever compared against.
	MeanIncrease float64
	ErrorRate    float64
}

// ProjectSQLLoad builds a result document from one SQL workload run.
//
// A nil run is unverified and failed, never a pass and never a document full
// of zeros. Zero transactions differenced against a real run would report the
// whole of the other side as a regression, and a throughput drop of one
// hundred percent is the most confident possible way to be wrong about a run
// that simply did not happen.
func ProjectSQLLoad(out *sqlload.Result, opts ProjectSQLLoadOptions) *Result {
	res := &Result{
		Schema:      ResultSchema,
		Kind:        SQLWorkload,
		State:       StateSucceeded,
		Environment: Environment{EnvID: opts.EnvID, Branch: opts.Branch},
		Reproduce:   Reproduce{Command: opts.Command, ManifestDigest: opts.ManifestDigest},
	}
	if out == nil {
		res.State = StateFailed
		res.Verdict = VerdictUnverified
		return res
	}
	projectSQL(res, out)
	// The same three calls runSQLWorkload makes, in the same order, rather
	// than a second opinion about any of them. A run that committed nothing is
	// unverified and so is a mean_increase that was in force and measured
	// nothing, which is what stops a side reporting green over a check that
	// never ran.
	res.Thresholds = sqlThresholds(out, opts.MeanIncrease, opts.ErrorRate)
	res.Verdict = sqlVerdict(out, opts.MeanIncrease, res.Thresholds)
	res.Routes = append(sqlTransactionMetrics(out), res.Routes...)
	return res
}

// sqlTransactionMetrics is one row per transaction kind, in the order the mix
// declares them.
//
// Mix order rather than slowest first, which is the order the statement rows
// take. A statement table is read to find the slow one; a transaction table is
// read to check the mix, and a transaction that moved from the top of it
// between two runs would look like a change in the workload rather than in
// its latency.
//
// A transaction that no round picked still gets a row, with no percentiles.
// That is the whole reason the runner keeps a row for it: a mix whose rarest
// transaction never ran is a finding about the run's length, and a missing row
// hides it. The comparison then reports it as present on one side only rather
// than as a pass, which is what it is.
func sqlTransactionMetrics(out *sqlload.Result) []RouteMetric {
	rows := make([]RouteMetric, 0, len(out.PerTransaction))
	for i, tx := range out.PerTransaction {
		m := RouteMetric{
			Route:    tx.Name,
			Sent:     tx.Executed,
			Errors:   tx.Failed,
			Position: i,
		}
		// Both together or neither, never a zero standing in for "nothing
		// committed". A p95 of zero on a transaction that ran no successful
		// attempt would enter the comparison as an infinitely fast side.
		if tx.Executed > 0 {
			m.P50Ms = floatp(tx.Latency.P50Ms)
			m.P90Ms = floatp(tx.Latency.P90Ms)
			m.P95Ms = floatp(tx.Latency.P95Ms)
			m.P99Ms = floatp(tx.Latency.P99Ms)
			m.MaxMs = floatp(tx.Latency.MaxMs)
		}
		if tx.Baselines.Has {
			m.BaselineP95Ms = floatp(tx.Baselines.MeanMs)
			m.P95Increase = floatp(tx.Baselines.MeanIncrease)
		}
		rows = append(rows, m)
	}
	return rows
}

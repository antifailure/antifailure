package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// The concurrent SQL workload, which until this file existed was reachable
// from the command line and from nowhere else.
//
// THE GAP THIS CLOSES. engine/internal/cli/sqlload.go has served af load sql
// since it landed, and engine/internal/mcp referenced env.Orchestrator.SQLLoad
// nowhere at all. run_load_test is HTTP traffic: its orchestratorFactory.sendLoad
// calls Load or Scenarios, which send requests at the application, so the
// number it reports is the application's latency with the database somewhere
// inside it. That is the right measurement for an application change and the
// wrong one for a database change, which is the whole argument af load sql was
// written for and is repeated here rather than assumed, because the two tools
// look interchangeable from a tool listing and are not.
//
// So an agent driving this server could rehearse a migration, send traffic,
// drive a browser and explore, and could not ask the one question somebody
// changing an index, a lock, a storage parameter or a query is actually
// asking. The capability existed, on one surface, and nothing anywhere said so.
//
// WHAT IS DELIBERATELY NOT HERE. There is no connection string, no branch, no
// statement text and no threshold in the schema. The statements come from the
// manifest's load.sql block or from pg_stat_statements on the branch, the
// database is the one this server's checkout has running, and the thresholds
// come from the manifest. A caller says how hard to push and for how long,
// within bounds, and cannot say what is run or what it is judged against.

// The bounds the schema publishes and enforces.
//
// Here rather than inline so the description and the validator read from one
// number, which is the convention tools_load.go already sets. Each of them
// costs wall clock time or database connections when it is crossed, so the
// schema refuses the expensive mistake rather than leaving sqlload.Run to
// discover it with a hundred open connections.
const (
	// maxSQLSeconds is the same ceiling run_load_test carries, for the same
	// reason: a run longer than ten minutes finds little a minute did not and
	// holds the branch for the whole of it.
	maxSQLSeconds = 600
	// maxSQLClients is a ceiling on connections, not on goroutines. Every
	// client holds its own connection for the length of the run, so this is
	// the number that meets max_connections on the server. Above it the run
	// fails with AF-LOD-018 rather than measuring anything, which is a fact
	// about the server's configuration dressed as a result.
	maxSQLClients = 200
	// maxSQLTransactionsPerClient bounds the other way a run is asked for.
	// A transaction count runs INSTEAD of a duration, so it is the one knob
	// here with no deadline attached to it at all: a client asked for ten
	// million transactions runs until it has done them.
	maxSQLTransactionsPerClient = 100000
	// maxSQLThinkTimeMS bounds the wait between transactions. A think time
	// longer than a minute produces a run that spends its whole duration
	// idle and reports a concurrency it never had.
	maxSQLThinkTimeMS = 60000
	// maxSQLNamedTransactions bounds the selection list.
	maxSQLNamedTransactions = 20

	// The result is read by a model, so the two lists that grow with the
	// project rather than with the run are bounded and the true totals are
	// reported beside them.
	maxSQLTransactionsReported = 20
	maxSQLStatementsReported   = 30
	maxSQLRefusedReported      = 20
	// The blocking pairs are bounded the same way. The engine already caps
	// what one run keeps; this is the second cap, on what a model is handed,
	// and the total is reported beside it so a truncation is never silent.
	maxSQLLockPairsReported = 12
)

// sqlWorkloadRequest is one workload, already validated and bounded.
//
// Zero means the caller did not ask, which is the distinction
// env.ResolveSQLLoad is built around: a value nobody typed must not shadow the
// manifest's own load.sql. This is the same defect the command line had, where
// a cobra flag holding its default made load.scale unreachable, and it is why
// af load sql reads cmd.Flags().Changed rather than the variable.
type sqlWorkloadRequest struct {
	Clients      int
	Duration     time.Duration
	Transactions int
	ThinkTime    time.Duration
	Seed         int64
	// Only narrows the mix to these transaction names. Empty means all.
	Only []string
}

// sqlWorkloadOutcome is what the workload measured, with the two thresholds it
// is judged against carried beside it.
//
// The thresholds travel out of the engine rather than being read again here,
// so the result reports each measurement next to the limit it was judged by
// instead of leaving a caller to guess which manifest produced the verdict.
type sqlWorkloadOutcome struct {
	Result *sqlload.Result
	Plan   *env.SQLLoadPlan
	// MeanIncrease and ErrorRate are the manifest's load.sql thresholds.
	// Zero means the manifest sets none, which is stated in the result rather
	// than reported as a threshold of zero.
	MeanIncrease float64
	ErrorRate    float64
}

// sendSQLWorkload runs the workload against the branch's database.
//
// A function value so the tool can be built against a fake in tests. The real
// one is the orchestrator, which is the same code path af load sql takes.
type sendSQLWorkload func(ctx context.Context, req sqlWorkloadRequest) (sqlWorkloadOutcome, error)

// newRunSQLWorkloadTool builds run_sql_workload.
func newRunSQLWorkloadTool(p *Project, eng *Engine, send sendSQLWorkload) *Tool {
	return &Tool{
		Name:  "run_sql_workload",
		Title: "Run a concurrent SQL workload against the branch's database",
		Description: "Answer whether this change holds up under production shaped " +
			"concurrency at the database itself. Clients, each on its own connection, " +
			"run whole transactions directly against the branch's database rather than " +
			"through the application, and it reports transactions per second, " +
			"transaction and per statement latency percentiles, deadlocks, " +
			"serialization failures, retries and the rows the statements actually " +
			"touched. " +
			"Use this rather than run_load_test for a change to an index, a lock, a " +
			"storage parameter or a query: run_load_test sends HTTP traffic, so what it " +
			"measures is the application's latency with the database somewhere inside " +
			"it, reachable only through whatever the application does on a route the " +
			"manifest names safe. " +
			"The statements come from the manifest's load.sql block, or from " +
			"pg_stat_statements on the branch, which is the traffic that really ran " +
			"weighted by how often it ran. A derived mix cannot recover the parameter " +
			"values, because the statistics normalise them away, so it asks the server " +
			"for the parameter types and generates values of those types, and it " +
			"refuses a write unless the manifest allows one. " +
			"It reports how many of its own backends the server had inside a " +
			"transaction at one instant, read from pg_stat_activity while the run was " +
			"going: N clients are not N concurrent sessions and that number is the " +
			"evidence rather than the claim. " +
			"The same connection asks pg_blocking_pids which of those backends were " +
			"waiting for a lock and which ones were in front of them, so the result " +
			"carries the contention the run was under and names the blocked statement " +
			"and the statement that blocked it. Sampled, so the counts are floors " +
			"rather than totals, and a run nothing watched reports that rather than a " +
			"zero: no contention and not measured are different answers. " +
			"Nothing reaches production and no production credential is used. " +
			"Bring an environment up first with start_environment; without one this " +
			"reports INCONCLUSIVE rather than a clean result, and so does a project " +
			"that declares no load.sql block. " +
			"This takes as long as its duration, so it returns a run_id immediately: " +
			"poll it with get_rehearsal_run.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"concurrency": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxSQLClients,
					Description: "Optional. How many clients run at once, each holding its " +
						"own connection for the length of the run. Leave it out and the " +
						"manifest's own load.sql.concurrency decides, falling back to 8. " +
						"Raising it pushes more concurrent transactions at the database, " +
						"which is the point of this workload, and it is bounded because " +
						"every client is a connection the server has to have left.",
				},
				"duration_seconds": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxSQLSeconds,
					Description: "Optional. How long to run for, in seconds. Leave it out " +
						"and the manifest's own load.sql.duration decides, falling back to " +
						"60. Sixty seconds is enough to see a p95 and to provoke a " +
						"deadlock; a longer run costs wall clock time and finds little a " +
						"minute did not.",
				},
				"transactions_per_client": {
					Type: "integer", HasMin: true, Minimum: 1,
					HasMax: true, Maximum: maxSQLTransactionsPerClient,
					Description: "Optional. How many transactions each client runs, INSTEAD " +
						"of a duration, which is what makes two runs comparable by work " +
						"done rather than by time. Leave it out and the run is bounded by " +
						"its duration.",
				},
				"think_time_ms": {
					Type: "integer", HasMin: true, Minimum: 0, HasMax: true, Maximum: maxSQLThinkTimeMS,
					Description: "Optional. How long a client waits between transactions, in " +
						"milliseconds. Leave it out and the manifest decides, falling back " +
						"to none, which is the shape that provokes contention. Think time " +
						"models a real user and lowers the concurrency actually reached.",
				},
				"seed": {
					Type: "integer", HasMin: true, Minimum: 0, HasMax: true, Maximum: 2147483647,
					Description: "Optional. Makes two runs execute the same sequence, so a " +
						"result can be reproduced. Defaults to 1, which is what the command " +
						"line uses, so a run here and a run there match.",
				},
				"transaction_names": {
					Type: "array", MaxItems: maxSQLNamedTransactions,
					Description: "Optional. Run only these transactions, by the name each " +
						"one gives itself in the manifest's load.sql block, which is what " +
						"the command line's own selection flag takes. Leave it out to run " +
						"the whole declared or derived mix, which is the measurement that " +
						"means anything about production: one transaction run alone " +
						"proves that transaction is " +
						"fast, which nobody doubted.",
					Items: nameSchema("The transaction's declared name, such as \"checkout\"."),
				},
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect this workload to show, in your " +
						"own words. Recorded with the run so the numbers can be read " +
						"against the expectation. It is never executed and never changes " +
						"what is run or what is measured.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			req, fault := readSQLWorkloadRequest(args)
			if fault != nil {
				return nil, fault
			}
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "run_sql_workload", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runSQLWorkload(ctx, p, eng, send, runID, req, hypothesis)
				})
		},
	}
}

// readSQLWorkloadRequest turns validated arguments into a request.
func readSQLWorkloadRequest(args map[string]any) (sqlWorkloadRequest, *Fault) {
	req := sqlWorkloadRequest{Seed: 1}

	if raw, present := args["concurrency"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "concurrency",
				"This field must be a whole number.")
		}
		req.Clients = n
	}
	if raw, present := args["duration_seconds"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "duration_seconds",
				"This field must be a whole number of seconds.")
		}
		req.Duration = time.Duration(n) * time.Second
	}
	if raw, present := args["transactions_per_client"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "transactions_per_client",
				"This field must be a whole number.")
		}
		req.Transactions = n
	}
	if raw, present := args["think_time_ms"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "think_time_ms",
				"This field must be a whole number of milliseconds.")
		}
		req.ThinkTime = time.Duration(n) * time.Millisecond
	}
	if raw, present := args["seed"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "seed",
				"This field must be a whole number.")
		}
		req.Seed = int64(n)
	}
	names, fault := readNames(args, "transaction_names", maxSQLNamedTransactions)
	if fault != nil {
		return req, fault
	}
	req.Only = names
	return req, nil
}

// runSQLWorkload runs one workload and reports what it measured.
func runSQLWorkload(
	ctx context.Context, p *Project, eng *Engine, send sendSQLWorkload,
	runID string, req sqlWorkloadRequest, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}

	// A project with no load.sql block is answered here rather than by the
	// orchestrator, and the difference is the answer's shape. The engine
	// refuses it as AF-LOD-017, a configuration error, which through the fault
	// below would reach a caller as a retryable failure to reach a database.
	// Retrying it will never work: nothing is wrong except that this project
	// has not said what its transactions are. So it is a finished run carrying
	// INCONCLUSIVE, which is the same answer check_data_invariants gives a
	// project that declares no invariants, and for the same reason: an
	// experiment that examined nothing has not passed.
	if p.Manifest == nil || p.Manifest.Load == nil || p.Manifest.Load.SQL == nil {
		return report.VerdictUnverified, &ResultBody{
			Summary: "This project declares no load.sql block, so there is no workload to " +
				"run and this says nothing about the database. Add one to " +
				"antifailure.yaml: either a source of pg_stat_statements, which " +
				"derives the mix from the traffic production really ran, or a " +
				"declared document of named transactions.",
			Detail: &sqlWorkloadDoc{
				Declared: false,
				Notes: []string{
					"Nothing was run. The manifest's load.sql block is what says which " +
						"transactions a workload is made of, and this manifest has none.",
				},
				ByTransaction: []sqlTransactionDoc{},
				ByStatement:   []sqlStatementDoc{},
			},
		}, nil
	}

	eng.Phase(ctx, runID, "running the SQL workload against the branch's database")

	out, err := send(ctx, req)
	if err != nil {
		// Nothing was measured, so this says nothing about how the change
		// behaves under concurrency. Reporting a pass because no threshold was
		// crossed would be reporting an experiment that did not happen as one
		// that found nothing, which is the failure this whole server refuses.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The workload could not be run, so this says nothing about the " +
				"database under concurrency. It runs against an environment rather " +
				"than creating one, so the usual cause is that nothing is running for " +
				"this branch: bring one up with start_environment. The other cause is " +
				"a server with no connections left for the client count asked for.",
			Retryable: true,
			wrapped:   err,
		}
	}

	eng.Phase(ctx, runID, "ranking what the workload showed")

	breaches := out.Result.Breaches(out.MeanIncrease, out.ErrorRate)
	inert := out.Result.InertMeanIncrease(out.MeanIncrease)
	native := sqlWorkloadVerdict(out, breaches, inert)
	findings := sqlWorkloadFindings(out, breaches, inert)

	body := &ResultBody{
		Findings: boundFindings(findings),
		Metrics:  sqlWorkloadMetrics(out, breaches),
		Evidence: sqlWorkloadEvidence(out),
		Detail:   describeSQLWorkload(out, breaches, inert),
	}
	body.Summary = sqlWorkloadSummary(out, breaches, inert, native, hypothesis)
	return native, body, nil
}

// sqlWorkloadVerdict maps the run onto the engine's own vocabulary.
//
// The same three questions af load sql's sqlLoadExit asks, in the same order,
// and the same three engine/internal/workload's sqlVerdict asks for the hosted
// run. There are now three callers of Breaches, InertMeanIncrease and
// Unverified and they must not be able to disagree: a workload that fails at a
// terminal and passes through an agent is worse than one that fails in both
// places.
//
// It deliberately does NOT consult the project's policy. af load sql exits non
// zero on a breach whatever policy.load_regression says, because a SQL
// threshold is declared in load.sql.thresholds and is not one of the levels
// that block is about, and a tool that ranked it at a policy level would pass a
// run the command line fails. That divergence is exactly what this file exists
// to remove, so it is not introduced one line lower down.
func sqlWorkloadVerdict(out sqlWorkloadOutcome, breaches []sqlload.Breach, inert bool) string {
	if out.Result.Unverified() {
		// The run committed nothing. Every threshold in it passed over an
		// empty measurement, and a breach count of zero there is the green
		// over nothing this product exists to stop.
		return report.VerdictUnverified
	}
	if len(breaches) > 0 {
		return report.VerdictFail
	}
	if inert {
		// A mean_increase threshold was in force and no transaction carried a
		// baseline for it to be measured against. A check that ran nothing and
		// reported green is a check everybody believes is running.
		return report.VerdictUnverified
	}
	return report.VerdictPass
}

// ruleSQLWorkload is the rule name a SQL threshold breach is reported under.
//
// Its own name rather than load_regression, because the two are judged by
// different numbers out of different manifest blocks: load_regression is
// load.thresholds over HTTP routes and carries a configurable policy level,
// and this is load.sql.thresholds over transactions and does not. One name for
// two rules would send somebody to the wrong block.
const ruleSQLWorkload = "sql_workload_regression"

// sqlWorkloadFindings writes the sentence for what the run crossed.
//
// Nothing here invents a threshold or a level. Which thresholds were breached
// comes from sqlload.Result.Breaches, called with the two numbers the command
// line reads out of the same manifest.
func sqlWorkloadFindings(
	out sqlWorkloadOutcome, breaches []sqlload.Breach, inert bool,
) []report.Finding {
	var findings []report.Finding
	if len(breaches) > 0 {
		where := make([]string, 0, len(breaches))
		detail := make([]string, 0, len(breaches))
		for _, b := range breaches {
			where = append(where, b.What)
			detail = append(detail, b.Detail)
		}
		findings = append(findings, report.Finding{
			Rule: ruleSQLWorkload, Level: report.LevelFail, Count: len(breaches),
			Where: strings.Join(where, ", "),
			Title: fmt.Sprintf("The SQL workload crossed %s the manifest sets.",
				plural(len(breaches), "a threshold", "thresholds")),
			Detail: strings.Join(detail, ". "),
			Fix: "Raise the threshold in load.sql.thresholds, or fix the regression the " +
				"transactions measured.",
		})
	}
	if inert {
		findings = append(findings, report.Finding{
			Rule: ruleSQLWorkload, Level: report.LevelFail, Count: 1,
			Where: "mean_increase",
			Title: "A threshold was in force and measured nothing.",
			Detail: fmt.Sprintf(
				"load.sql.thresholds sets mean_increase %.2f and no transaction in this "+
					"run carried a baseline, so nothing was compared against it.",
				out.MeanIncrease),
			Fix: "Derive the mix from pg_stat_statements, which carries the baseline, or " +
				"remove the mean_increase threshold so it is not reported as in force.",
		})
	}
	return findings
}

func sqlWorkloadMetrics(out sqlWorkloadOutcome, breaches []sqlload.Breach) []Metric {
	res := out.Result
	metrics := []Metric{
		{Name: "transactions_committed", Value: float64(res.Transactions), Unit: "transactions"},
		{Name: "transactions_failed", Value: float64(res.TransactionsFailed), Unit: "transactions"},
		{Name: "transactions_per_second", Value: res.TPS, Unit: "transactions_per_second"},
		{Name: "transaction_p50_latency", Value: res.Overall.P50Ms, Unit: "ms"},
		{Name: "transaction_p95_latency", Value: res.Overall.P95Ms, Unit: "ms"},
		{Name: "transaction_p99_latency", Value: res.Overall.P99Ms, Unit: "ms"},
	}
	if out.ErrorRate > 0 {
		limit := out.ErrorRate
		metrics = append(metrics, Metric{
			Name: "error_rate", Value: res.ErrorRate, Unit: "ratio",
			Threshold: &limit, Breached: res.ErrorRate > limit,
		})
	} else {
		metrics = append(metrics, Metric{Name: "error_rate", Value: res.ErrorRate, Unit: "ratio"})
	}
	metrics = append(metrics,
		// Retried, deadlocked and serialization failed are reported always,
		// including as zero, because they are the two outcomes a concurrent
		// workload exists to provoke plus the count of what was retried into
		// looking like a success. A run that committed everything on the
		// second attempt is not the same run as one that committed on the
		// first, and the transaction count cannot tell them apart.
		Metric{Name: "retries", Value: float64(res.Retries), Unit: "attempts"},
		Metric{Name: "deadlocks", Value: float64(res.Deadlocks), Unit: "transactions"},
		Metric{
			Name: "serialization_failures", Value: float64(res.SerializationFailures),
			Unit: "transactions",
		},
		Metric{Name: "statements_run", Value: float64(res.Statements), Unit: "statements"},
		Metric{Name: "statements_failed", Value: float64(res.StatementsFailed), Unit: "statements"},
		// Rows touched is what says whether a derived mix's generated
		// parameters matched anything at all. Forty thousand statements that
		// touched no rows measured the cost of finding nothing, which is a
		// real measurement of an index and is not a measurement of the
		// customer's result sets. Without it the fast one looks like the good
		// one.
		Metric{Name: "rows_touched", Value: float64(res.Rows), Unit: "rows"},
		Metric{
			Name: "statements_refused_as_unsafe", Value: float64(len(res.Refused)),
			Unit: "statements",
		},
		Metric{Name: "clients_requested", Value: float64(res.Clients), Unit: "clients"},
	)
	if res.ClientsStopped > 0 {
		zero := 0.0
		metrics = append(metrics, Metric{
			Name: "clients_stopped_early", Value: float64(res.ClientsStopped), Unit: "clients",
			Threshold: &zero, Breached: true,
		})
	}
	// The observation, and only when there was one. A nil pointer means the
	// observer could not run, and reporting that as zero would turn "nobody
	// looked" into "no two transactions ever overlapped", which is the
	// strongest claim this result can make and would be made by mistake.
	if res.PeakOpenTransactions != nil {
		metrics = append(metrics, Metric{
			Name: "peak_open_transactions", Value: float64(*res.PeakOpenTransactions),
			Unit: "transactions",
		})
	}
	if res.PeakActiveBackends != nil {
		metrics = append(metrics, Metric{
			Name: "peak_executing_backends", Value: float64(*res.PeakActiveBackends),
			Unit: "backends",
		})
	}
	if res.BackendsSeen != nil {
		metrics = append(metrics, Metric{
			Name: "backends_seen", Value: float64(*res.BackendsSeen), Unit: "backends",
		})
	}
	// The contention, under the same rule and with more riding on it. A zero
	// here reads as "this build blocked nothing", which is the most
	// reassuring thing this tool can say, so it is emitted only when a
	// sample actually landed. The note beside the numbers carries what the
	// sampling could not see.
	if res.LockWaits != nil {
		metrics = append(metrics, Metric{
			Name: "lock_waits", Value: float64(*res.LockWaits), Unit: "waits",
		})
	}
	if res.LockWaitMS != nil {
		metrics = append(metrics, Metric{
			Name: "lock_wait_ms", Value: *res.LockWaitMS, Unit: "backend milliseconds",
		})
	}
	if len(breaches) > 0 {
		zero := 0.0
		metrics = append(metrics, Metric{
			Name: "thresholds_breached", Value: float64(len(breaches)), Unit: "thresholds",
			Threshold: &zero, Breached: true,
		})
	}
	return metrics
}

// sqlWorkloadEvidence points at where the full measurement lives, and never
// carries it.
func sqlWorkloadEvidence(out sqlWorkloadOutcome) []Evidence {
	evidence := []Evidence{{
		URI: "af://sqlload/" + sqlSourceName(out.Result.Source), Kind: "command",
		Note: "Run af load sql -o json for the full per statement measurement, " +
			"including the rows this result truncated.",
	}}
	if len(out.Result.Refused) > 0 {
		evidence = append(evidence, Evidence{
			URI: "af://sqlload/refused", Kind: "refused_statements",
			Note: fmt.Sprintf(
				"%d statements were read out of the mix and not run, usually because "+
					"they write and the manifest's load.sql does not allow a write. They "+
					"are listed in the detail with the reason.", len(out.Result.Refused)),
		})
	}
	return evidence
}

// sqlSourceName renders the mix's source for a reader.
//
// The same two words af load sql prints, because a derived mix and a declared
// one are different claims about the same numbers and a reader must not have
// to know the engine's constant to tell them apart.
func sqlSourceName(source string) string {
	if source == sqlload.SourceStatementStatistics {
		return "pg_stat_statements"
	}
	return "declared"
}

// sqlWorkloadDoc is the workload evidence a result carries.
type sqlWorkloadDoc struct {
	// Declared says the manifest had a load.sql block at all. False is a run
	// that did not happen, and every number below it is absent rather than
	// zero.
	Declared bool `json:"load_sql_declared"`
	// Source says where the mix came from, so a reader can tell production's
	// own statements from a document somebody wrote. A guess and a measurement
	// must never be mistaken for each other.
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`

	Clients      int    `json:"clients,omitempty"`
	Duration     string `json:"duration,omitempty"`
	ThinkTime    string `json:"think_time,omitempty"`
	Transactions int    `json:"transactions_committed"`
	Failed       int    `json:"transactions_failed"`
	Retries      int    `json:"retries"`
	Deadlocks    int    `json:"deadlocks"`
	// SerializationFailures is counted apart from the error map for the same
	// reason deadlocks are: it is one of the two a concurrent workload exists
	// to provoke and a reader should not need its SQLSTATE to find it.
	SerializationFailures int     `json:"serialization_failures"`
	Statements            int     `json:"statements_run"`
	StatementsFailed      int     `json:"statements_failed"`
	Rows                  int64   `json:"rows_touched"`
	TPS                   float64 `json:"transactions_per_second"`
	ErrorRate             float64 `json:"error_rate"`

	Overall *latencyDoc `json:"transaction_latency,omitempty"`

	TransactionsTotal int                 `json:"transactions_by_kind_total"`
	ByTransaction     []sqlTransactionDoc `json:"transactions_by_kind"`
	StatementsTotal   int                 `json:"statements_by_label_total"`
	ByStatement       []sqlStatementDoc   `json:"statements_by_label"`
	ClientsStopped    int                 `json:"clients_stopped_early"`
	StoppedBecause    map[string]int      `json:"stopped_because,omitempty"`
	Errors            map[string]int      `json:"errors_by_reason,omitempty"`
	Refused           []sqlRefusedDoc     `json:"refused_statements,omitempty"`
	RefusedTotal      int                 `json:"refused_total"`
	Observation       sqlObservationDoc   `json:"concurrency_observed"`
	Contention        sqlContentionDoc    `json:"lock_contention"`
	Thresholds        sqlThresholdsDoc    `json:"thresholds"`
	Breaches          []sqlBreachDoc      `json:"breaches,omitempty"`
	Notes             []string            `json:"notes,omitempty"`
}

// sqlTransactionDoc is one transaction kind's numbers.
type sqlTransactionDoc struct {
	Name     string      `json:"name"`
	Executed int         `json:"executed"`
	Failed   int         `json:"failed"`
	Retries  int         `json:"retries"`
	Weight   float64     `json:"weight,omitempty"`
	Latency  *latencyDoc `json:"latency,omitempty"`
	// BaselineMeanMs and MeanIncrease are absent when the source carried no
	// baseline, and HasBaseline says which. Nothing to compare against and no
	// change are different answers, and a zero would read as the second.
	BaselineMeanMs float64 `json:"baseline_mean_ms,omitempty"`
	MeanIncrease   float64 `json:"mean_increase,omitempty"`
	HasBaseline    bool    `json:"has_baseline"`
}

// sqlStatementDoc is one statement's numbers, which is the row a person
// changing an index actually reads.
type sqlStatementDoc struct {
	// Transaction and Label together are the identity. One statement can
	// appear in two transactions and their latencies cannot be merged, for
	// the same reason two scenarios' percentiles for one route cannot be
	// averaged.
	Transaction string      `json:"transaction"`
	Label       string      `json:"label"`
	Executed    int         `json:"executed"`
	Errors      int         `json:"errors"`
	Rows        int64       `json:"rows"`
	Latency     *latencyDoc `json:"latency,omitempty"`
}

type sqlRefusedDoc struct {
	Statement string `json:"statement"`
	Code      string `json:"code"`
	Reason    string `json:"reason"`
}

// sqlObservationDoc is what the server said about this run's own backends.
//
// Pointers all the way out, because the observer reports them as pointers for
// exactly this reason: N clients are not N concurrent sessions, and a run that
// spawned eight clients and never had two statements in flight is a real
// outcome that a zero here would be indistinguishable from a run nobody
// watched.
type sqlObservationDoc struct {
	Observed             bool   `json:"observed"`
	PeakOpenTransactions *int   `json:"peak_open_transactions,omitempty"`
	PeakActiveBackends   *int   `json:"peak_executing_backends,omitempty"`
	BackendsSeen         *int   `json:"backends_seen,omitempty"`
	Note                 string `json:"note,omitempty"`
}

// sqlContentionDoc is what the run was seen to wait for.
//
// Observed is a boolean beside the pointers rather than a thing to infer from
// them, because a model given a missing field will supply the friendliest
// value for it. Zero waits and an unwatched run have to be two different
// sentences here, and Note is what makes the second one impossible to read as
// the first.
type sqlContentionDoc struct {
	Observed   bool     `json:"observed"`
	LockWaits  *int     `json:"lock_waits,omitempty"`
	LockWaitMS *float64 `json:"lock_wait_ms,omitempty"`
	// PairsTotal is how many distinct blocking pairs the run kept and Pairs is
	// the worst of them, so a truncated list cannot be read as the whole.
	PairsTotal int              `json:"blocking_pairs_total"`
	Pairs      []sqlLockPairDoc `json:"blocking_pairs,omitempty"`
	Note       string           `json:"note"`
}

// sqlLockPairDoc is one blocked statement and the statement in front of it.
//
// The labels come from the mix, which is repository content read by a model,
// so they are neutralised and clipped the way every other label in this file
// is. The lock type, the mode and the relation name come from the server's own
// catalogue and are neutralised for the same reason: a relation can be named
// anything a customer's migration called it.
type sqlLockPairDoc struct {
	BlockedTransaction  string  `json:"blocked_transaction,omitempty"`
	BlockedStatement    string  `json:"blocked_statement,omitempty"`
	BlockingTransaction string  `json:"blocking_transaction,omitempty"`
	BlockingStatement   string  `json:"blocking_statement,omitempty"`
	BlockingState       string  `json:"blocking_state,omitempty"`
	BlockingInRun       bool    `json:"blocking_in_run"`
	Relation            string  `json:"relation,omitempty"`
	LockType            string  `json:"lock_type"`
	Mode                string  `json:"mode"`
	Waits               int     `json:"waits"`
	WaitedMS            float64 `json:"waited_ms"`
}

type sqlThresholdsDoc struct {
	// MeanIncrease and ErrorRate come from the manifest's load.sql.thresholds
	// and cannot be set from a tool call. Zero means the manifest sets none,
	// which is stated rather than reported as a threshold of zero.
	MeanIncrease float64 `json:"mean_increase"`
	ErrorRate    float64 `json:"error_rate"`
	Note         string  `json:"note"`
}

type sqlBreachDoc struct {
	What      string  `json:"what"`
	Detail    string  `json:"detail"`
	Threshold float64 `json:"threshold"`
	Observed  float64 `json:"observed"`
}

// describeSQLWorkload renders the measurement, bounded and neutralised.
//
// Transaction names and statement labels come from the manifest or from a
// document in the candidate branch, the refused statement text is normalised
// SQL out of the database, and the error reasons carry the server's own
// messages. All of them are repository or database content read by a model, so
// all of them are neutralised and clipped. They are not identifiers and are not
// checked against safeIdentifier, which would refuse a statement for containing
// a space.
func describeSQLWorkload(
	out sqlWorkloadOutcome, breaches []sqlload.Breach, inert bool,
) *sqlWorkloadDoc {
	res := out.Result
	doc := &sqlWorkloadDoc{
		Declared:              true,
		Source:                sqlSourceName(res.Source),
		Clients:               res.Clients,
		Duration:              res.Duration.Round(time.Millisecond).String(),
		Transactions:          res.Transactions,
		Failed:                res.TransactionsFailed,
		Retries:               res.Retries,
		Deadlocks:             res.Deadlocks,
		SerializationFailures: res.SerializationFailures,
		Statements:            res.Statements,
		StatementsFailed:      res.StatementsFailed,
		Rows:                  res.Rows,
		TPS:                   res.TPS,
		ErrorRate:             res.ErrorRate,
		Overall:               latency(res.Overall),
		TransactionsTotal:     len(res.PerTransaction),
		StatementsTotal:       len(res.PerStatement),
		ClientsStopped:        res.ClientsStopped,
		Errors:                boundedReasons(res.Errors),
		RefusedTotal:          len(res.Refused),
		Thresholds: sqlThresholdsDoc{
			MeanIncrease: out.MeanIncrease, ErrorRate: out.ErrorRate,
			Note: "These come from the manifest's load.sql.thresholds and cannot be set " +
				"from a tool call. Zero means the manifest sets none, so nothing was " +
				"judged against it.",
		},
		ByTransaction: []sqlTransactionDoc{},
		ByStatement:   []sqlStatementDoc{},
	}
	if out.Plan != nil {
		doc.Description = neutralize(out.Plan.Description, 200)
		if doc.Clients == 0 {
			doc.Clients = out.Plan.Clients
		}
		if out.Plan.ThinkTime > 0 {
			doc.ThinkTime = out.Plan.ThinkTime.String()
		}
	}
	if len(res.StoppedBecause) > 0 {
		doc.StoppedBecause = boundedReasons(res.StoppedBecause)
	}

	transactions := res.PerTransaction
	if len(transactions) > maxSQLTransactionsReported {
		transactions = transactions[:maxSQLTransactionsReported]
		doc.Notes = append(doc.Notes, fmt.Sprintf(
			"%d transaction kinds ran and the first %d are shown. Read the rest with "+
				"af load sql -o json.", doc.TransactionsTotal, maxSQLTransactionsReported))
	}
	for _, tx := range transactions {
		doc.ByTransaction = append(doc.ByTransaction, sqlTransactionDoc{
			Name: neutralize(tx.Name, 128), Executed: tx.Executed, Failed: tx.Failed,
			Retries: tx.Retries, Weight: tx.Weight, Latency: latency(tx.Latency),
			BaselineMeanMs: tx.Baselines.MeanMs, MeanIncrease: tx.Baselines.MeanIncrease,
			HasBaseline: tx.Baselines.Has,
		})
	}

	statements := res.PerStatement
	if len(statements) > maxSQLStatementsReported {
		statements = statements[:maxSQLStatementsReported]
		doc.Notes = append(doc.Notes, fmt.Sprintf(
			"%d statements ran and the first %d are shown. Read the rest with "+
				"af load sql -o json.", doc.StatementsTotal, maxSQLStatementsReported))
	}
	for _, st := range statements {
		doc.ByStatement = append(doc.ByStatement, sqlStatementDoc{
			Transaction: neutralize(st.Transaction, 128), Label: neutralize(st.Label, 128),
			Executed: st.Executed, Errors: st.Errors, Rows: st.Rows,
			Latency: latency(st.Latency),
		})
	}

	for i, r := range res.Refused {
		if i >= maxSQLRefusedReported {
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"%d statements were refused and the first %d are named.",
				len(res.Refused), maxSQLRefusedReported))
			break
		}
		doc.Refused = append(doc.Refused, sqlRefusedDoc{
			Statement: neutralize(r.Statement, 300), Code: neutralize(r.Code, 64),
			Reason: neutralize(r.Reason, 200),
		})
	}

	doc.Observation = sqlObservationDoc{
		Observed:             res.BackendsSeen != nil,
		PeakOpenTransactions: res.PeakOpenTransactions,
		PeakActiveBackends:   res.PeakActiveBackends,
		BackendsSeen:         res.BackendsSeen,
	}
	if res.BackendsSeen == nil {
		doc.Observation.Note = "The run's own backends were never sampled, so this result " +
			"does not say whether any two transactions were ever in flight together: " +
			neutralize(res.ObserverNote, 200)
	} else {
		doc.Observation.Note = "Read from pg_stat_activity while the run was going. This is " +
			"how many of this run's own backends the server had inside a transaction at " +
			"one instant, which is the evidence that the clients really overlapped."
	}

	doc.Contention = describeSQLContention(res)
	if doc.Contention.PairsTotal > len(doc.Contention.Pairs) {
		doc.Notes = append(doc.Notes, fmt.Sprintf(
			"%d distinct blocking pairs were seen and the %d that cost the most waiting "+
				"are shown. Read the rest with af load sql -o json.",
			doc.Contention.PairsTotal, len(doc.Contention.Pairs)))
	}

	for _, b := range breaches {
		doc.Breaches = append(doc.Breaches, sqlBreachDoc{
			What: b.What, Detail: b.Detail, Threshold: b.Threshold, Observed: b.Observed,
		})
	}
	if res.Unverified() {
		doc.Notes = append(doc.Notes,
			"This run committed no transaction, so it measured neither a throughput nor "+
				"a latency and every threshold in it passed over nothing: "+
				res.UnverifiedDetail()+".")
	}
	if inert {
		doc.Notes = append(doc.Notes,
			"load.sql.thresholds sets mean_increase and no transaction carried a baseline "+
				"to compare against, so that threshold was in force and measured nothing.")
	}
	return doc
}

func sqlWorkloadSummary(
	out sqlWorkloadOutcome, breaches []sqlload.Breach, inert bool,
	native, hypothesis string,
) string {
	res := out.Result
	var b strings.Builder

	if res.Unverified() {
		fmt.Fprintf(&b, "Nothing was measured: %s. ", res.UnverifiedDetail())
	} else {
		fmt.Fprintf(&b,
			"%d transactions committed in %s at %.1f a second, %d failed, %d retried. "+
				"Transaction p95 %.0fms, p99 %.0fms. %d statements touched %d rows. ",
			res.Transactions, res.Duration.Round(time.Millisecond), res.TPS,
			res.TransactionsFailed, res.Retries,
			res.Overall.P95Ms, res.Overall.P99Ms, res.Statements, res.Rows)
		fmt.Fprintf(&b, "The mix came from %s. ", sqlSourceName(res.Source))
	}

	if res.Deadlocks > 0 || res.SerializationFailures > 0 {
		fmt.Fprintf(&b, "%d deadlocks and %d serialization failures, every one retried. ",
			res.Deadlocks, res.SerializationFailures)
	}
	// Said in the summary rather than left to the detail, because a result
	// that overlapped nothing and a result nobody watched both read as a
	// concurrent run from the numbers above.
	if res.BackendsSeen == nil {
		b.WriteString("The run's own backends were never sampled, so nothing here says " +
			"the clients really overlapped. ")
	} else {
		fmt.Fprintf(&b,
			"%d clients held %d separate sessions and the server had %d of them inside a "+
				"transaction at once. ",
			res.Clients, *res.BackendsSeen, derefInt(res.PeakOpenTransactions))
	}
	if res.ClientsStopped > 0 {
		fmt.Fprintf(&b,
			"%d of %d clients stopped before the run ended, so the rate above is at a "+
				"concurrency nobody chose. ", res.ClientsStopped, res.Clients)
	}
	if len(res.Refused) > 0 {
		fmt.Fprintf(&b,
			"%d %s read out of the mix and not run, so less was exercised than these "+
				"numbers suggest. ",
			len(res.Refused), plural(len(res.Refused), "statement was", "statements were"))
	}

	switch {
	case native == report.VerdictUnverified && inert:
		b.WriteString("A threshold was in force and evaluated nothing, so this is not a pass. ")
	case native == report.VerdictUnverified:
		b.WriteString("Nothing was measured against a threshold, so this is not a pass. ")
	case len(breaches) > 0:
		fmt.Fprintf(&b, "%d %s in load.sql.thresholds %s crossed. ",
			len(breaches), plural(len(breaches), "threshold", "thresholds"),
			plural(len(breaches), "was", "were"))
	default:
		b.WriteString("No threshold in load.sql.thresholds was crossed. ")
	}

	if hypothesis != "" {
		fmt.Fprintf(&b, "Your stated hypothesis, unevaluated: %q.", neutralize(hypothesis, 500))
	}
	return strings.TrimSpace(b.String())
}

// derefInt reads a pointer the observer may not have filled.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// sqlLoadOptions is the whole mapping from a validated call onto the engine's
// own knobs.
//
// Its own function because it is the part that fails silently. A field left out
// here is a knob the schema publishes, the validator accepts, the description
// promises and nothing acts on: the call succeeds, the run happens, and it
// happens at the manifest's value rather than the caller's. Nothing downstream
// can notice, because a workload at eight clients is a perfectly good workload.
// As a function it can be put to a test that reads every field back.
func sqlLoadOptions(req sqlWorkloadRequest) env.SQLLoadOptions {
	return env.SQLLoadOptions{
		Clients:      req.Clients,
		Duration:     req.Duration,
		Transactions: req.Transactions,
		ThinkTime:    req.ThinkTime,
		Seed:         req.Seed,
		Select:       req.Only,
	}
}

// sendSQLWorkloadThrough runs the workload through the orchestrator.
//
// The branch, the database, the mix and the thresholds are all decided here
// from the manifest and the checkout, and no argument in the tool schema
// reaches any of them. A caller says how hard and how long, inside the
// schema's bounds, and cannot say what is run or where.
func (f *orchestratorFactory) sendSQLWorkload(
	ctx context.Context, req sqlWorkloadRequest,
) (sqlWorkloadOutcome, error) {
	o, err := f.build()
	if err != nil {
		return sqlWorkloadOutcome{}, err
	}
	meanIncrease, errorRate := o.SQLThresholds()

	res, plan, err := o.SQLLoad(ctx, sqlLoadOptions(req))
	// A result and an error together is the shape sqlload.Run settles on
	// deliberately: a cancelled or partially failed run still measured
	// something, and a caller that read a non nil error as "nothing to report"
	// would throw away the more useful half of a failure. af load sql reads it
	// the same way, and so does engine/internal/workload.
	if res == nil {
		if err == nil {
			err = fmt.Errorf("the workload returned neither a result nor an error")
		}
		return sqlWorkloadOutcome{}, err
	}
	return sqlWorkloadOutcome{
		Result: res, Plan: plan, MeanIncrease: meanIncrease, ErrorRate: errorRate,
	}, nil
}

// describeSQLContention renders the wait queue reading for a model.
//
// The unwatched run is written out as a sentence rather than left as an
// absence. A model handed a result with no contention section will say the run
// found no contention, which is the single most reassuring thing it could say
// and would be an invention. Observed false plus a note that says so is what
// makes that sentence unavailable.
func describeSQLContention(res *sqlload.Result) sqlContentionDoc {
	out := sqlContentionDoc{
		Observed:   res.LockWaits != nil,
		LockWaits:  res.LockWaits,
		LockWaitMS: res.LockWaitMS,
		PairsTotal: len(res.LockWaitPairs),
		Note:       neutralize(res.LockWaitNote, 800),
	}
	if !out.Observed {
		return out
	}
	pairs := res.LockWaitPairs
	if len(pairs) > maxSQLLockPairsReported {
		pairs = pairs[:maxSQLLockPairsReported]
	}
	for _, w := range pairs {
		out.Pairs = append(out.Pairs, sqlLockPairDoc{
			BlockedTransaction:  neutralize(w.BlockedTransaction, 128),
			BlockedStatement:    neutralize(w.BlockedStatement, 128),
			BlockingTransaction: neutralize(w.BlockingTransaction, 128),
			BlockingStatement:   neutralize(w.BlockingStatement, 128),
			BlockingState:       neutralize(w.BlockingState, 64),
			BlockingInRun:       w.BlockingInRun,
			Relation:            neutralize(w.Relation, 128),
			LockType:            neutralize(w.LockType, 64),
			Mode:                neutralize(w.Mode, 64),
			Waits:               w.Waits,
			WaitedMS:            w.WaitedMS,
		})
	}
	return out
}

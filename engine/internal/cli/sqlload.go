package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// SQLLoadJSON is the machine readable result of a SQL workload.
//
// Flat, and every number the text output prints is in it. A JSON document that
// carries fewer facts than the table above it is a document somebody has to
// re-run the command to read.
type SQLLoadJSON struct {
	Source       string `json:"source"`
	Clients      int    `json:"clients"`
	Duration     string `json:"duration"`
	Transactions int    `json:"transactions"`
	Failed       int    `json:"transactions_failed"`
	Retries      int    `json:"retries"`
	Deadlocks    int    `json:"deadlocks"`
	// SerializationFailures is counted apart from the error map for the same
	// reason deadlocks are: it is one of the two a concurrent workload exists
	// to provoke and a reader should not need its SQLSTATE to find it.
	SerializationFailures int                         `json:"serialization_failures"`
	Statements            int                         `json:"statements"`
	StatementsFailed      int                         `json:"statements_failed"`
	Rows                  int64                       `json:"rows"`
	TPS                   float64                     `json:"tps"`
	ErrorRate             float64                     `json:"error_rate"`
	Overall               load.Latency                `json:"overall"`
	ByTransaction         []sqlload.TransactionResult `json:"transactions_by_kind"`
	ByStatement           []sqlload.StatementResult   `json:"statements_by_label"`
	Errors                map[string]int              `json:"errors,omitempty"`
	Refused               []sqlload.Refused           `json:"refused"`
	ClientsStopped        int                         `json:"clients_stopped"`
	StoppedBecause        map[string]int              `json:"stopped_because,omitempty"`

	// The observation, carried as pointers so that "nobody looked" and "no
	// overlap" stay different answers all the way out to a consumer.
	PeakActiveBackends   *int   `json:"peak_active_backends"`
	PeakOpenTransactions *int   `json:"peak_open_transactions"`
	BackendsSeen         *int   `json:"backends_seen"`
	ObserverNote         string `json:"observer_note,omitempty"`

	Breaches []sqlload.Breach `json:"breaches,omitempty"`
	// InertMeanIncrease says a mean_increase threshold was in force and no
	// transaction carried a baseline for it to be measured against.
	InertMeanIncrease bool `json:"inert_mean_increase,omitempty"`
	// Unverified says the run committed nothing, so every threshold in it
	// passed over an empty measurement.
	Unverified bool `json:"unverified,omitempty"`
}

func newLoadSQLCommand(e *Env) *cobra.Command {
	var branch string
	var concurrency int
	var duration time.Duration
	var transactions int
	var thinkTime time.Duration
	var seed int64
	var only []string

	cmd := &cobra.Command{
		Use:   "sql",
		Short: "Run a concurrent SQL workload against the branch's database",
		Long: strings.TrimSpace(`
Clients, each on its own connection, running whole transactions against the
database directly rather than through the application.

Everything else this engine sends goes over HTTP, so the number it reports is
the application's latency with the database somewhere inside it. That is the
right measurement for an application change and the wrong one for a database
change. Somebody changing an index, a lock, a storage parameter or a query
wants transactions per second and statement latency, and can only reach them
through whatever the application happens to do on a route they can reach.

The statements come from a document in the repository, or from
pg_stat_statements on the branch, which is the traffic that really ran weighted
by how often it ran. A derived mix cannot recover the values, because the
statistics normalise them away, so it asks the server for the parameter types
and generates values of those types. It refuses a write unless the manifest
allows one, and every run reports the rows its statements actually touched, so
a reader can tell a fast query from a query that found nothing.

The run reports how many of its own backends the server had inside a
transaction at one instant, read from pg_stat_activity while it was going. N
clients are not N concurrent sessions and that number is the evidence rather
than the claim.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The same defence in depth af load run takes. A workload is sent
			// AT an environment rather than creating one, and an environment
			// left up from a run on the base branch is still one a fork's pull
			// request could point statements at.
			if fork := forkGate(e); fork.Refused {
				return refuseFork(fork)
			}
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Running a SQL workload")

			opts := env.SQLLoadOptions{Seed: seed, Select: only}
			// Changed() rather than the variable, for the reason loadRate
			// documents at length: a flag holds its default whether or not
			// anybody set it, and passing a default down as a choice is what
			// made load.scale unreachable from the command line.
			if cmd.Flags().Changed("concurrency") {
				opts.Clients = concurrency
			}
			if cmd.Flags().Changed("duration") {
				opts.Duration = duration
			}
			if cmd.Flags().Changed("transactions") {
				opts.Transactions = transactions
			}
			if cmd.Flags().Changed("think-time") {
				opts.ThinkTime = thinkTime
			}
			opts.Progress = func(p sqlload.Progress) {
				e.Out.Printf("  %s  %d committed, %d failed, %.0f a second, p95 %.0fms, %d clients\n",
					p.Elapsed.Round(time.Second), p.Transactions, p.Failed, p.TPS, p.P95Ms, p.Clients)
			}

			res, plan, err := o.SQLLoad(cmd.Context(), opts)
			if err != nil && res == nil {
				return err
			}

			meanIncrease, errorRate := o.SQLThresholds()
			breaches := res.Breaches(meanIncrease, errorRate)
			inert := res.InertMeanIncrease(meanIncrease)
			verdict := sqlLoadExit(res, breaches, meanIncrease)

			if e.Out.Format == FormatJSON {
				if err := e.Out.JSON(sqlLoadJSON(res, plan, breaches, inert)); err != nil {
					return err
				}
				if verdict != nil {
					return silent(verdict)
				}
				return nil
			}

			printSQLLoad(e, res, plan)
			if len(breaches) > 0 {
				e.Out.Println("")
				e.Out.Section("Thresholds exceeded")
				for _, b := range breaches {
					e.Out.Printf("  %s %s: %s\n", e.Out.S(StyleBad, SymbolFail), b.What, b.Detail)
				}
			}
			if inert {
				e.Out.Println("")
				e.Out.Section("A threshold measured nothing")
				e.Out.Printf("  %s mean_increase %.2f: no transaction in this run carried a "+
					"baseline, so nothing was compared\n",
					e.Out.S(StyleBad, SymbolFail), meanIncrease)
			}
			if verdict != nil {
				return silent(verdict)
			}
			return nil
		},
	}
	// Named concurrency rather than clients, and the name is load bearing
	// rather than cosmetic. Every hosted result carries the argv that
	// reproduces it, and a hosted knob may exist only if the plain command has
	// a flag for it: engine/internal/workload refuses anything else rather
	// than dropping it silently, and the test that holds that rule reads the
	// real command tree and looks up the flag by the knob's own name. The
	// control plane's knob is called concurrency, af load scenario's flag is
	// called concurrency, and a third spelling here would have meant a hosted
	// SQL workload could never set how many clients it runs.
	cmd.Flags().IntVar(&concurrency, "concurrency", 8,
		"How many clients run at once, each on its own connection")
	cmd.Flags().DurationVar(&duration, "duration", 60*time.Second, "How long to run for")
	cmd.Flags().IntVar(&transactions, "transactions", 0,
		"How many transactions each client runs, instead of a duration")
	cmd.Flags().DurationVar(&thinkTime, "think-time", 0, "How long a client waits between transactions")
	cmd.Flags().Int64Var(&seed, "seed", 1, "Makes two runs execute the same sequence")
	cmd.Flags().StringArrayVar(&only, "only", nil,
		"Run only these transactions, by name. Repeat the flag for several")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to run against, defaulting to the checked out one")
	return cmd
}

// sqlLoadExit is the verdict a SQL workload exits with.
//
// Three ways to fail and the order is the order of usefulness. A run that
// committed nothing goes first, because every threshold it carries passed over
// an empty measurement and reporting a breach count of zero there would be the
// green over nothing this product exists to stop. Then a measured breach. Then
// a threshold that was in force and evaluated nothing, which exits non-zero for
// the same reason af load run's inert p95 does.
func sqlLoadExit(res *sqlload.Result, breaches []sqlload.Breach, meanIncrease float64) error {
	if res.Unverified() {
		return aferrors.Coded(aferrors.AFLOD021, "detail", res.UnverifiedDetail())
	}
	if len(breaches) > 0 {
		return aferrors.Coded(aferrors.AFLOD022, "count", fmt.Sprint(len(breaches)))
	}
	if res.InertMeanIncrease(meanIncrease) {
		return aferrors.Coded(aferrors.AFLOD021,
			"detail", "the mean_increase threshold was in force and no transaction carried a "+
				"baseline, so it was evaluated against nothing")
	}
	return nil
}

func sqlLoadJSON(res *sqlload.Result, plan *env.SQLLoadPlan, breaches []sqlload.Breach, inert bool) SQLLoadJSON {
	doc := SQLLoadJSON{
		Source: res.Source, Clients: res.Clients,
		Duration:     res.Duration.Round(time.Millisecond).String(),
		Transactions: res.Transactions, Failed: res.TransactionsFailed,
		Retries: res.Retries, Deadlocks: res.Deadlocks,
		SerializationFailures: res.SerializationFailures,
		Statements:            res.Statements, StatementsFailed: res.StatementsFailed,
		Rows: res.Rows, TPS: res.TPS, ErrorRate: res.ErrorRate, Overall: res.Overall,
		ByTransaction: res.PerTransaction, ByStatement: res.PerStatement,
		Errors: res.Errors, Refused: res.Refused,
		ClientsStopped: res.ClientsStopped, StoppedBecause: res.StoppedBecause,
		PeakActiveBackends: res.PeakActiveBackends, PeakOpenTransactions: res.PeakOpenTransactions,
		BackendsSeen: res.BackendsSeen, ObserverNote: res.ObserverNote,
		Breaches: breaches, InertMeanIncrease: inert, Unverified: res.Unverified(),
	}
	if plan != nil && doc.Clients == 0 {
		doc.Clients = plan.Clients
	}
	return doc
}

// printSQLLoad renders the run for somebody with thirty seconds.
func printSQLLoad(e *Env, res *sqlload.Result, plan *env.SQLLoadPlan) {
	e.Out.Println("")
	shape := fmt.Sprintf("  %s statements", describeSQLSource(res.Source))
	if plan != nil && plan.Description != "" {
		shape += ", " + plan.Description
	}
	e.Out.Printf("%s.\n", shape)

	// The observation before the numbers, because it is what says whether the
	// numbers are of a concurrent run at all.
	if res.BackendsSeen == nil {
		e.Out.Printf("  %s The run's own backends were never sampled: %s.\n",
			e.Out.S(StyleWarn, SymbolWarn), res.ObserverNote)
	} else {
		e.Out.Printf("  %d clients held %d separate sessions, and the server had %d of them "+
			"inside a transaction at once (%d executing).\n",
			res.Clients, *res.BackendsSeen, deref(res.PeakOpenTransactions), deref(res.PeakActiveBackends))
	}

	e.Out.Printf("  %d transactions committed in %s at %.1f a second, %d failed, %d retried.\n",
		res.Transactions, res.Duration.Round(time.Millisecond), res.TPS,
		res.TransactionsFailed, res.Retries)
	e.Out.Printf("  Transaction p50 %.1fms, p95 %.1fms, p99 %.1fms. %d statements touched %d rows.\n\n",
		res.Overall.P50Ms, res.Overall.P95Ms, res.Overall.P99Ms, res.Statements, res.Rows)

	rows := make([][]string, 0, len(res.PerStatement))
	for _, st := range res.PerStatement {
		rows = append(rows, []string{
			st.Transaction, st.Label, fmt.Sprint(st.Executed),
			fmt.Sprintf("%.1fms", st.Latency.P95Ms), fmt.Sprint(st.Rows), fmt.Sprint(st.Errors),
		})
	}
	e.Out.Table([]Column{
		Flex("TRANSACTION"), Flex("STATEMENT"), Num("RAN"), Num("P95"), Num("ROWS"), Num("ERRORS"),
	}, rows)

	if res.Deadlocks > 0 || res.SerializationFailures > 0 {
		e.Out.Println("")
		e.Out.Printf("  %d deadlocks and %d serialization failures, every one of them retried.\n",
			res.Deadlocks, res.SerializationFailures)
	}
	for _, reason := range sortedKeys(res.Errors) {
		e.Out.Printf("  %s %d attempts: %s\n",
			e.Out.S(StyleWarn, SymbolWarn), res.Errors[reason], reason)
	}
	if res.ClientsStopped > 0 {
		for _, reason := range sortedKeys(res.StoppedBecause) {
			e.Out.Printf("  %s %d clients stopped before the run ended: %s\n",
				e.Out.S(StyleBad, SymbolFail), res.StoppedBecause[reason], reason)
		}
	}
	if len(res.Refused) > 0 {
		e.Out.Println("")
		e.Out.Printf("  %d statements were read and not taken:\n", len(res.Refused))
		for _, r := range res.Refused {
			e.Out.Printf("    %s %s\n", e.Out.S(StyleDim, r.Code), e.Out.Wrap(r.Statement, 6))
		}
	}
	if res.Unverified() {
		e.Out.Println("")
		e.Out.Printf("  %s This run committed nothing, so it measured neither a throughput "+
			"nor a latency: %s.\n",
			e.Out.S(StyleBad, SymbolFail), res.UnverifiedDetail())
	}
}

func describeSQLSource(source string) string {
	if source == sqlload.SourceStatementStatistics {
		return "pg_stat_statements"
	}
	return "declared"
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

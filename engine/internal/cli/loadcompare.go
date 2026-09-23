package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/internal/workload"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// af load compare is the only way a person reaches the base branch comparison,
// and that is the point.
//
// A capability nothing invokes is a shippable gap that looks like a feature.
// The differencing primitive has existed in engine/internal/workload for some
// time and was reachable only through `af workload compare`, which takes two
// result documents a person has to have produced by hand and which lives under
// a command marked Hidden because it is the control plane's plumbing. So the
// arithmetic was reachable and the measurement was not: nothing anywhere ran
// one workload against two builds. This command is that missing half, and it
// lands with the second environment and the thresholds that judge it.

// LoadCompareJSON is the machine readable result of a two build comparison.
type LoadCompareJSON struct {
	Comparison *workload.Comparison         `json:"comparison"`
	Judged     []workload.ComparisonVerdict `json:"thresholds"`
	Verdict    string                       `json:"verdict"`
	Baseline   loadCompareSideJSON          `json:"baseline"`
	Candidate  loadCompareSideJSON          `json:"candidate"`
	// Golden is the database version BOTH sides branched from, which is what
	// makes the difference worth anything.
	Golden string `json:"golden,omitempty"`
	// BaselineTornDown false is a leak somebody has to finish by hand, so it
	// is in the document rather than only in the terminal.
	BaselineTornDown bool     `json:"baseline_torn_down"`
	BaselineBranch   string   `json:"baseline_branch,omitempty"`
	Notes            []string `json:"notes"`
	// Rounds is every round's p95 per route on both sides, which is what each
	// route's change and interval were computed from. Published so that the
	// interval can be recomputed by hand, rather than taken on trust.
	Rounds []workload.RoundP95 `json:"rounds,omitempty"`
	// SQL is present only when the concurrent SQL workload was compared, and
	// carries the facts about it that a difference cannot: how many clients
	// ran, where the mix came from, and each side's own evidence that its
	// clients really overlapped.
	//
	// A new key rather than new keys beside the existing ones, and absent
	// entirely for an HTTP comparison, so that nothing a reader of the HTTP
	// document already parses changed name, shape or presence. Which workload
	// this was is already in comparison.kind, so there is no second field
	// saying it: two places to read one fact is one place to be wrong.
	SQL *loadCompareSQLJSON `json:"sql,omitempty"`
}

// loadCompareSQLJSON is what a SQL comparison knows and a difference does not.
type loadCompareSQLJSON struct {
	// Source is "declared" or "statement_statistics" and Description is what a
	// declared document calls itself.
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
	// Clients, ThinkTime and RoundTransactions were resolved once and used on
	// both sides. RoundTransactions is the per client bound ONE ROUND carried,
	// zero when the rounds were bounded by time alone.
	Clients           int    `json:"clients"`
	ThinkTime         string `json:"think_time"`
	RoundTransactions int    `json:"round_transactions,omitempty"`

	Baseline  loadCompareSQLSideJSON `json:"baseline"`
	Candidate loadCompareSQLSideJSON `json:"candidate"`
}

// loadCompareSQLSideJSON is one side's evidence that it ran the way it says.
//
// The observation is here rather than folded into the comparison because it is
// not a difference: N clients are not N concurrent database sessions, and a
// side whose clients never overlapped measured a serial workload. A
// comparison of two serial workloads is a perfectly consistent set of numbers
// about a run nobody asked for, and only these fields can say so. The pointers
// stay pointers all the way out, because "nobody looked" and "no overlap" are
// different answers.
type loadCompareSQLSideJSON struct {
	PeakActiveBackends   *int           `json:"peak_active_backends"`
	PeakOpenTransactions *int           `json:"peak_open_transactions"`
	BackendsSeen         *int           `json:"backends_seen"`
	ObserverNote         string         `json:"observer_note,omitempty"`
	ClientsStopped       int            `json:"clients_stopped"`
	StoppedBecause       map[string]int `json:"stopped_because,omitempty"`
	// Refused is what the mix would not run. One mix is built for both sides,
	// so the two lists are the same list; it is carried per side anyway
	// because a side that reported a different one would be a finding about
	// this harness rather than about the builds.
	Refused []sqlload.Refused `json:"refused"`
}

type loadCompareSideJSON struct {
	Rev string `json:"rev,omitempty"`
	// How says how the base ref was resolved, so a reader can tell
	// origin/main from a named tag without rerunning anything.
	How string `json:"how,omitempty"`
}

func newLoadCompareCommand(e *Env) *cobra.Command {
	var branch, baseRef, output string
	var duration time.Duration
	var scale float64
	var seed int64
	var keep bool
	var rounds int
	var warmup time.Duration
	var sql bool
	var concurrency, transactions int
	var thinkTime time.Duration
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Run the same traffic against the base branch too, and report what moved",
		Long: strings.TrimSpace(`
Brings a second environment up from the base revision, branches the same golden
for both so they answer queries over identical rows, sends both the same
weighted mix in the same order under the same seed, and reports every route and
every run wide number that moved.

This is the base branch comparison. It is a different question from the one
'af load run' answers: that measures one build against what production serves,
using the per route p95 in your traffic source, and it is the right question
when you want to know whether a route is slower than the fleet. This one
measures this build against the last one, which is the right question when you
want to know whether your change made it slower.

Each side is first sent a short warm-up that is thrown away, which takes the
first request of every route out of the numbers. Then each side is sent the mix
in rounds, interleaved so that neither side always goes first, with the same
seed for both sides in each round.

Each route is judged ROUND AGAINST ROUND. Every round is a small comparison of
its own, and the change is measured from how those comparisons agreed, with an
interval as wide as the host's own noise between rounds. The intervals hold at
ninety percent for every route together. A limit inside a route's interval is
neither a pass nor a fail, and the report prints the smallest change that route
could have shown on this host, so on a noisy machine the answer is "too close
to say" rather than a regression that is not there. More rounds or a longer
duration narrows it.

What it still cannot control is printed with every report rather than left
implied. The rounds are sequential, because two environments sending traffic
at once on one host would contend with each other and measure that instead.
A difference is a difference, and a threshold under load.comparison.thresholds
is what turns one into a verdict.

With --sql it compares the concurrent SQL workload instead: clients running
whole transactions against each build's own database rather than requests
against its application. Same mix, built once on this build so that neither
side reads its own pg_stat_statements, same client count, same think time and
the same per round seed. The unit of comparison becomes the transaction and
the statement inside it, and each one reports p50, p95 and p99 on both sides.
Throughput becomes committed transactions a second, judged against the same
load.comparison.thresholds.throughput_drop. It needs a load.sql block and
refuses without one.

The base environment is torn down unless --keep says otherwise. The
environment for this build is left running whether or not this brought it up.`),
		Example: strings.TrimSpace(`
af load compare
af load compare --baseline origin/main --duration 60s
af load compare --sql --concurrency 16
af load compare --seed 7 --keep`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Load is sent AT an environment rather than creating one, so the
			// fork gate is defence in depth here exactly as it is on af load
			// run: an environment left up from a run on the base branch is
			// still one a fork's pull request can point traffic at.
			if fork := forkGate(e); fork.Refused {
				return refuseFork(fork)
			}
			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			cfg := loadComparisonConfig(m)
			if cfg == nil {
				return aferrors.Coded(aferrors.AFLOD010,
					"detail", "this manifest declares no load.comparison block, so there is "+
						"nothing saying which revision to compare against; add one with "+
						"enabled: true")
			}
			if cfg.Enabled != nil && !*cfg.Enabled {
				e.Out.Status(e.Out.S(StyleDim, SymbolSkip),
					"the manifest turns the base branch comparison off",
					"set load.comparison.enabled to true to run it")
				return nil
			}
			if err := checkSQLCompareFlags(cmd.Flags(), sql, m); err != nil {
				return err
			}

			if sql {
				e.Out.Section("Comparing the SQL workload against the base branch")
			} else {
				e.Out.Section("Comparing against the base branch")
			}
			res, err := o.LoadCompare(cmd.Context(), env.LoadCompareOptions{
				Baseline: cfg.Baseline,
				BaseRef:  orDefaultString(baseRef, cfg.BaseRef),
				Duration: duration, Scale: scale, Seed: seed, Keep: keep,
				Rounds: rounds, Warmup: warmup,
				NoWarmup: noWarmup(cmd.Flags(), warmup),
				// Passed only when they were typed, for the reason loadRate
				// documents at length: a flag holds its default whether or not
				// anybody set it, and passing a default down as a choice is
				// what made load.scale unreachable from the command line. Here
				// it would silently override load.sql.clients with eight on
				// every run.
				SQL:     sql,
				Clients: changedInt(cmd.Flags(), "concurrency", concurrency),
				Transactions: changedInt(
					cmd.Flags(), "transactions", transactions),
				ThinkTime: changedDuration(cmd.Flags(), "think-time", thinkTime),
				// Each round is a step on the status line, so that "on this
				// step" measures the round in flight rather than the whole
				// compare since the environment came up.
				Progress: func(line string) { progressFor(e).Step(line) },
			})
			if errors.Is(err, env.ErrLoadBaselineSameCommit) {
				// A branch level with its base is a legitimate state rather
				// than a failure. Reporting it as one would fail the pipeline
				// of everybody who reran a check on an unchanged branch.
				e.Out.Status(e.Out.S(StyleDim, SymbolSkip),
					"there are not two builds to compare",
					"this branch is the same commit as its base")
				return nil
			}
			if res != nil && !res.BaselineTornDown && !keep && res.BaselineBranch != "" {
				defer func() {
					e.Out.Printf("  the base environment may still be up. Remove it with: "+
						"af down --branch %q\n", res.BaselineBranch)
				}()
			}
			if err != nil {
				return err
			}

			p95Increase, errorRate := o.Thresholds()
			// The digest of the manifest BOTH sides ran under. One digest
			// rather than one per side, deliberately: the base environment is
			// built from the base revision's code and driven by the
			// candidate's manifest, exactly as the oracle's baseline is, so
			// that a manifest change in the branch moves the application and
			// not the harness. Recording two digests here would suggest the
			// two sides were configured differently when they were not.
			digest := ""
			if manifestPath, perr := manifest.Find(e.WorkDir); perr == nil {
				digest = manifestDigest(manifestPath)
			}
			// One projection per workload, each carrying its OWN per side
			// thresholds: load.thresholds for the mix and load.sql.thresholds
			// for the SQL workload. Those are the single run limits, measured
			// against production or against what the statistics said the
			// statements used to cost, and they are here so that a verdict
			// TRANSITION between the two sides is visible. The base branch
			// deltas under load.comparison.thresholds are a different question
			// and Judge evaluates them over both sides at once, below.
			var baseline, candidate *workload.Result
			var rounds []workload.RoundP95
			if sql {
				meanIncrease, sqlErrorRate := o.SQLThresholds()
				baseline = workload.ProjectSQLLoad(res.BaselineSQL,
					workload.ProjectSQLLoadOptions{
						Branch: res.BaselineBranch, Command: "af load sql",
						ManifestDigest: digest,
						MeanIncrease:   meanIncrease, ErrorRate: sqlErrorRate,
					})
				candidate = workload.ProjectSQLLoad(res.CandidateSQL,
					workload.ProjectSQLLoadOptions{
						EnvID: o.EnvID(), Branch: o.Branch(),
						Command: "af load sql", ManifestDigest: digest,
						MeanIncrease: meanIncrease, ErrorRate: sqlErrorRate,
					})
				rounds = sqlRoundP95s(res)
			} else {
				baseline = workload.ProjectLoad(res.Baseline, workload.ProjectLoadOptions{
					Branch: res.BaselineBranch, Command: "af load run",
					ManifestDigest: digest, P95Increase: p95Increase, ErrorRate: errorRate,
				})
				candidate = workload.ProjectLoad(res.Candidate, workload.ProjectLoadOptions{
					EnvID: o.EnvID(), Branch: o.Branch(),
					Command: "af load run", ManifestDigest: digest,
					P95Increase: p95Increase, ErrorRate: errorRate,
				})
				rounds = roundP95s(res)
			}
			comparison, err := workload.Compare(baseline, candidate)
			if err != nil {
				return err
			}
			// Round against round wherever there are rounds to pair. The
			// pooled comparison above is kept only for the run wide measures
			// and for a single pass, whose notes say what it cannot see.
			if len(rounds) >= 2 {
				workload.ResolveByRounds(comparison, rounds)
			}
			comparison.Notes = append(comparison.Notes, res.Notes...)

			thresholds := comparisonThresholds(cfg)
			judged := workload.Judge(comparison, thresholds)
			verdict := workload.ComparisonOutcome(judged)

			if e.Out.Format == FormatJSON {
				if err := e.Out.JSON(
					loadCompareDoc(res, comparison, judged, verdict, rounds),
				); err != nil {
					return err
				}
				return loadCompareExit(thresholds, judged, verdict)
			}

			if sql {
				renderSQLComparison(e, res, comparison, judged, verdict)
			} else {
				renderLoadComparison(e, res, comparison, judged, verdict)
			}
			if output != "" {
				doc := loadCompareDoc(res, comparison, judged, verdict, rounds)
				body, merr := json.MarshalIndent(doc, "", "  ")
				if merr != nil {
					return merr
				}
				if werr := os.WriteFile(output, append(body, '\n'), 0o644); werr != nil {
					e.Out.Printf("  could not write the report to %s: %v\n", output, werr)
				}
			}
			return loadCompareExit(thresholds, judged, verdict)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to compare, defaulting to the checked out one")
	cmd.Flags().StringVar(&baseRef, "baseline", "",
		"Revision to compare against, overriding load.comparison.base_ref")
	cmd.Flags().DurationVar(&duration, "duration", 0,
		"How long to send for on each side, overriding the manifest")
	cmd.Flags().Float64Var(&scale, "scale", 0,
		"Fraction of production's arrival rate to send at each side, overriding the manifest")
	cmd.Flags().Int64Var(&seed, "seed", 0,
		"Seed for the request sequence. The same seed is used on both sides")
	cmd.Flags().IntVar(&rounds, "rounds", 0, fmt.Sprintf(
		"Interleaved rounds per side, %d when not set. 1 measures each side once, base first",
		env.DefaultCompareRounds))
	cmd.Flags().DurationVar(&warmup, "warmup", 0, fmt.Sprintf(
		"Mix sent at each side and discarded before measuring, %s when not set. 0s sends none",
		env.DefaultCompareWarmup))
	// An explicit flag, never an inference. The alternative considered and
	// rejected was comparing the SQL workload whenever load.sql is declared
	// and the HTTP mix otherwise, which would silently change what `af load
	// compare` measures in every repository that adds a load.sql block, and
	// change it in a pipeline rather than in front of somebody. A flag is one
	// word to type and it can be read in a log six months later.
	cmd.Flags().BoolVar(&sql, "sql", false,
		"Compare the SQL workload from load.sql instead of the HTTP mix")
	// The three SQL knobs take the same names af load sql gives them, because
	// a flag that means one thing under one command and is spelled differently
	// under another is a flag people get wrong. Each one applies to BOTH sides
	// and there is deliberately no way to set one per side.
	cmd.Flags().IntVar(&concurrency, "concurrency", 8,
		"Clients each side runs at once, overriding load.sql.clients. Needs --sql")
	cmd.Flags().IntVar(&transactions, "transactions", 0,
		"Transactions each client runs, split across the rounds, overriding "+
			"load.sql.transactions. Needs --sql")
	cmd.Flags().DurationVar(&thinkTime, "think-time", 0,
		"How long a client waits between transactions, overriding load.sql.think_time. "+
			"Needs --sql")
	cmd.Flags().BoolVar(&keep, "keep", false,
		"Leave the base environment up, for looking at a difference")
	// --report rather than --output, for the reason af oracle and af ci both
	// give: --output and -o are the root's own "text or json" flag, a local
	// flag silently wins, and the result was a command writing its report to a
	// file literally named json.
	cmd.Flags().StringVar(&output, "report", "", "Write the comparison here as well as to the terminal")
	return cmd
}

// checkSQLCompareFlags refuses a combination that cannot mean what it says.
//
// Three refusals and each one is a question with no honest answer rather than
// a matter of taste.
//
// --sql without a load.sql block asks for a workload that does not exist. It
// is AF-LOD-017, the same code `af load sql` refuses with for the same missing
// block, because it is the same fact and a second code for it would be a
// second thing to look up.
//
// A SQL knob without --sql would be read as a choice and used for nothing. It
// is refused rather than ignored: the same flag SET and IGNORED is how
// load.scale spent months unreachable from the command line, and the person
// who typed --concurrency 32 deserves to know it did nothing before the run
// rather than after.
//
// --scale with --sql is the same defect pointed the other way. Scale is a
// fraction of production's ARRIVAL RATE, and a SQL workload has no arrival
// rate at all: its clients run transactions back to back with think time
// between them. Accepting it silently would report a run at a concurrency
// nobody chose under a number the reader believes they set.
func checkSQLCompareFlags(flags *pflag.FlagSet, sql bool, m *schema.Manifest) error {
	sqlKnobs := []string{"concurrency", "transactions", "think-time"}
	if !sql {
		for _, name := range sqlKnobs {
			if flags.Changed(name) {
				return aferrors.Coded(aferrors.AFLOD010,
					"detail", "--"+name+" configures the SQL workload and this comparison is "+
						"sending the HTTP mix, so it would be read and used for nothing; add "+
						"--sql, or drop it")
			}
		}
		return nil
	}
	if flags.Changed("scale") {
		return aferrors.Coded(aferrors.AFLOD017,
			"detail", "--scale is a fraction of production's arrival rate and the SQL workload "+
				"has none: its clients run transactions back to back with think time between "+
				"them. Use --concurrency to change how many clients run, or --think-time to "+
				"change the wait between transactions")
	}
	if m == nil || m.Load == nil || m.Load.SQL == nil {
		return aferrors.Coded(aferrors.AFLOD017,
			"detail", "--sql compares the workload under load.sql and this manifest declares "+
				"no load.sql block, so there is no workload to run on either side; declare one, "+
				"or drop --sql to compare the HTTP mix")
	}
	return nil
}

// changedInt and changedDuration pass a flag's value down only when somebody
// typed it.
//
// A cobra flag holds its default whether or not anybody set it, so passing the
// variable straight down hands the engine a DEFAULT dressed as a CHOICE, and
// the manifest key it was meant to fall back on becomes unreachable. That is
// not a hypothetical: it is how load.scale was unreachable from the command
// line while `af explain` read the manifest's value back correctly.
func changedInt(flags *pflag.FlagSet, name string, v int) int {
	if flags.Changed(name) {
		return v
	}
	return 0
}

func changedDuration(flags *pflag.FlagSet, name string, v time.Duration) time.Duration {
	if flags.Changed(name) {
		return v
	}
	return 0
}

// sqlRoundP95s pairs the two sides' rounds as each UNIT's p95 in that round.
//
// The same pairing roundP95s does for routes, over the two units a SQL
// workload has: the transaction, keyed by its name alone, and the statement,
// keyed by its transaction and its label. The keys are built with
// workload.UnitKey rather than by hand, because the row this feeds is found by
// exactly that string and a second spelling of it would not error: the unit
// would simply never find its rounds and report "too few rounds ran this unit
// on both sides" forever.
//
// A unit a round did not run, or ran only failures of, is absent from that
// round rather than recorded as zero, for the same reason a route is: a zero
// enters the log ratio as an infinitely fast round.
func sqlRoundP95s(res *env.LoadCompareResult) []workload.RoundP95 {
	n := len(res.BaselineSQLRounds)
	if len(res.CandidateSQLRounds) < n {
		n = len(res.CandidateSQLRounds)
	}
	perUnit := func(r *sqlload.Result) map[string]float64 {
		out := map[string]float64{}
		if r == nil {
			return out
		}
		for _, tx := range r.PerTransaction {
			if tx.Executed > 0 && tx.Latency.P95Ms > 0 {
				out[workload.UnitKey("", tx.Name)] = tx.Latency.P95Ms
			}
		}
		for _, st := range r.PerStatement {
			if st.Executed > 0 && st.Latency.P95Ms > 0 {
				out[workload.UnitKey(st.Transaction, st.Label)] = st.Latency.P95Ms
			}
		}
		return out
	}
	out := make([]workload.RoundP95, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, workload.RoundP95{
			Base: perUnit(res.BaselineSQLRounds[k]), Candidate: perUnit(res.CandidateSQLRounds[k]),
		})
	}
	return out
}

// loadCompareDoc is the machine readable document, built in one place.
//
// One constructor rather than two identical literals, which is what it was:
// the JSON output and the --report file each built their own and the two had
// to be kept in step by eye. A field added to one and not the other is a
// report file that carries less than the same command's stdout.
func loadCompareDoc(
	res *env.LoadCompareResult, comparison *workload.Comparison,
	judged []workload.ComparisonVerdict, verdict string, rounds []workload.RoundP95,
) LoadCompareJSON {
	return LoadCompareJSON{
		Comparison: comparison, Judged: judged, Verdict: verdict,
		Baseline:         loadCompareSideJSON{Rev: res.Rev, How: res.How},
		Candidate:        loadCompareSideJSON{Rev: res.CandidateRev},
		Golden:           res.Golden,
		BaselineTornDown: res.BaselineTornDown,
		BaselineBranch:   res.BaselineBranch,
		Notes:            comparison.Notes,
		Rounds:           rounds,
		SQL:              loadCompareSQLDoc(res),
	}
}

// loadCompareSQLDoc is nil for an HTTP comparison, so the key is absent from
// the document rather than present and empty.
func loadCompareSQLDoc(res *env.LoadCompareResult) *loadCompareSQLJSON {
	if !res.SQL {
		return nil
	}
	return &loadCompareSQLJSON{
		Source: res.SQLSource, Description: res.SQLDescription,
		Clients: res.Clients, ThinkTime: res.ThinkTime.String(),
		RoundTransactions: res.RoundTransactions,
		Baseline:          sqlCompareSideDoc(res.BaselineSQL),
		Candidate:         sqlCompareSideDoc(res.CandidateSQL),
	}
}

func sqlCompareSideDoc(r *sqlload.Result) loadCompareSQLSideJSON {
	if r == nil {
		return loadCompareSQLSideJSON{Refused: []sqlload.Refused{}}
	}
	out := loadCompareSQLSideJSON{
		PeakActiveBackends: r.PeakActiveBackends, PeakOpenTransactions: r.PeakOpenTransactions,
		BackendsSeen: r.BackendsSeen, ObserverNote: r.ObserverNote,
		ClientsStopped: r.ClientsStopped, StoppedBecause: r.StoppedBecause,
		Refused: r.Refused,
	}
	if out.Refused == nil {
		out.Refused = []sqlload.Refused{}
	}
	return out
}

// roundP95s pairs the two sides' rounds, round k with round k, as each route's
// p95 in that round. A route a round did not send, or sent only failures to,
// is absent from that round rather than recorded as zero, because a zero
// would enter the log ratio as an infinitely fast round.
func roundP95s(res *env.LoadCompareResult) []workload.RoundP95 {
	n := len(res.BaselineRounds)
	if len(res.CandidateRounds) < n {
		n = len(res.CandidateRounds)
	}
	perRoute := func(r *load.Result) map[string]float64 {
		out := map[string]float64{}
		if r == nil {
			return out
		}
		for _, rr := range r.Routes {
			if rr.Latency.P95Ms > 0 {
				out[rr.Route] = rr.Latency.P95Ms
			}
		}
		return out
	}
	out := make([]workload.RoundP95, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, workload.RoundP95{
			Base: perRoute(res.BaselineRounds[k]), Candidate: perRoute(res.CandidateRounds[k]),
		})
	}
	return out
}

// noWarmup is whether the person asked for no warm-up.
//
// Typed and zero is "none"; not typed is "the default". The two cannot share a
// value, because the flag's zero is also its unset state, so whether the flag
// was set on the command line is what tells them apart. Reading the value
// alone would make `--warmup 0s`, the arm that shows what the warm-up is for,
// silently send the default warm-up instead.
func noWarmup(flags *pflag.FlagSet, warmup time.Duration) bool {
	return flags.Changed("warmup") && warmup <= 0
}

// loadComparisonConfig reads the block, treating an absent load block and an
// absent comparison block as the same answer.
func loadComparisonConfig(m *schema.Manifest) *schema.LoadComparison {
	if m == nil || m.Load == nil {
		return nil
	}
	return m.Load.Comparison
}

// comparisonThresholds carries the manifest's declared base branch limits into
// the shape the judge evaluates. An absent thresholds block is no declared
// limit rather than a zero limit.
func comparisonThresholds(cfg *schema.LoadComparison) workload.ComparisonThresholds {
	if cfg == nil || cfg.Thresholds == nil {
		return workload.ComparisonThresholds{}
	}
	return workload.ComparisonThresholds{
		P95Increase:       cfg.Thresholds.P95Increase,
		ThroughputDrop:    cfg.Thresholds.ThroughputDrop,
		ErrorRateIncrease: cfg.Thresholds.ErrorRateIncrease,
	}
}

// loadCompareExit decides the exit code.
//
// Three outcomes rather than two. A failing threshold exits non zero, which is
// obvious. A threshold that was DECLARED and could not be evaluated also exits
// non zero, which is not obvious and is the more important of the two: a
// comparison whose limits all went unverified has measured nothing, and
// exiting zero on it is exactly how this product once shipped a green nightly
// corpus that had never reached an agent. A comparison with no declared
// threshold at all is a report rather than a check and exits zero.
func loadCompareExit(
	t workload.ComparisonThresholds, judged []workload.ComparisonVerdict, verdict string,
) error {
	if !t.Declared() {
		return nil
	}
	switch verdict {
	case workload.VerdictFail:
		breaches := workload.ComparisonBreaches(judged)
		names := make([]string, 0, len(breaches))
		for _, b := range breaches {
			if b.Scope != "" {
				names = append(names, b.Name+" on "+b.Scope)
				continue
			}
			names = append(names, b.Name)
		}
		return silent(aferrors.Coded(aferrors.AFLOD023,
			"detail", strings.Join(names, ", ")))
	case workload.VerdictUnverified:
		return silent(aferrors.Coded(aferrors.AFLOD024,
			"detail", "every declared base branch threshold went unmeasured, so this "+
				"comparison judged nothing"))
	}
	return nil
}

func renderLoadComparison(
	e *Env, res *env.LoadCompareResult, c *workload.Comparison,
	judged []workload.ComparisonVerdict, verdict string,
) {
	e.Out.Println("")
	e.Out.Printf("  %s against %s\n", shortRev(res.CandidateRev), shortRev(res.Rev))
	if res.How != "" {
		e.Out.Printf("  the base was resolved %s\n", res.How)
	}

	rows := [][]string{}
	for _, m := range c.Measures {
		rows = append(rows, []string{m.Measure, numberOf(m.Baseline), numberOf(m.Candidate),
			ratioOf(m.Ratio), m.Direction})
	}
	if len(rows) > 0 {
		e.Out.Println("")
		e.Out.Table([]Column{{Title: "measure"}, {Title: "base"}, {Title: "this build"},
			{Title: "change"}, {Title: "moved"}}, rows)
	}

	// The per route table, which is the one somebody actually came for. A
	// comparison that printed only the run wide numbers would hide the single
	// slow route inside an average, which is the whole reason routes are
	// measured separately.
	//
	// The "can see" column is what this comparison could resolve on that
	// route, printed on every row whatever the verdict, because the number is
	// the deliverable as much as the direction is. A change of plus 585
	// percent beside a resolution of plus 1024 percent is a reading nobody
	// can mistake for a regression, and the same two numbers without the
	// second one is exactly the pull request this column exists to prevent.
	routes := [][]string{}
	for _, r := range c.Routes {
		routes = append(routes, []string{r.Route, numberOf(r.P95Baseline),
			numberOf(r.P95Candidate), ratioOf(r.P95Ratio), movedOf(r),
			resolutionOf(r.Resolution)})
	}
	if len(routes) > 0 {
		e.Out.Println("")
		e.Out.Table([]Column{{Title: "route"}, {Title: "base p95"}, {Title: "this build p95"},
			{Title: "change"}, {Title: "moved"}, {Title: "can see"}}, routes)
	}

	renderComparisonTail(e, c, judged, verdict)
}

// renderComparisonTail is everything after the tables: what broke, what could
// not be measured, what could not be resolved, what the comparison cannot see,
// and the verdict.
//
// Shared between the two workloads rather than written twice, and it is the
// part that most needs to be: every block in it exists because a comparison
// once reported a number without one of them. A second copy for the SQL path
// would be a second place for one of these blocks to go missing, and the
// symptom of a missing block here is silence, which reads as a clean run.
func renderComparisonTail(
	e *Env, c *workload.Comparison, judged []workload.ComparisonVerdict, verdict string,
) {
	breaches := workload.ComparisonBreaches(judged)
	if len(breaches) > 0 {
		e.Out.Println("")
		e.Out.Println("What crossed a declared threshold:")
		for _, b := range breaches {
			e.Out.Printf("  %s\n", e.Out.Wrap(b.Detail, 2))
		}
	}
	// A declared threshold that could not be measured is reported as loudly as
	// one that failed, because it is the absence of the check the manifest
	// asked for rather than a clean result.
	unverified := 0
	for _, j := range judged {
		if j.Value == workload.VerdictUnverified {
			unverified++
		}
	}
	if unverified > 0 {
		e.Out.Println("")
		e.Out.Printf("  %d declared %s could not be measured on both sides.\n",
			unverified, plural2(unverified, "threshold", "thresholds"))
	}
	// The blind rows get their reasons in full, because "could not resolve"
	// as a count is the kind of line a reader skims past on the way to the
	// verdict, and the sentence beneath it is what says whether to send for
	// longer or move to a quieter machine.
	blind := []workload.ComparisonVerdict{}
	for _, j := range judged {
		if j.Unresolvable {
			blind = append(blind, j)
		}
	}
	if len(blind) > 0 {
		e.Out.Println("")
		e.Out.Println("What this run could not resolve:")
		for _, b := range blind {
			e.Out.Printf("  %s\n", e.Out.Wrap(b.Scope+": "+b.Detail, 2))
		}
	}

	e.Out.Println("")
	e.Out.Println("What this comparison cannot see:")
	for _, n := range c.Notes {
		e.Out.Printf("  %s\n", e.Out.Wrap(n, 2))
	}
	e.Out.Println("")
	e.Out.Status(verdictSymbol(verdict), "the base branch comparison is "+verdict, "")
}

func verdictSymbol(verdict string) string {
	switch verdict {
	case workload.VerdictFail:
		return SymbolFail
	case workload.VerdictPass:
		return SymbolOK
	}
	return SymbolSkip
}

// movedOf is the direction, withheld when the run cannot support one.
//
// The identical build comparison printed "better" by up to 86 percent on one
// sample and "worse" by up to 586 on the next, for two commits differing by a
// comment. The arrow was as wrong as the number, and it is the part a reader
// acts on first. A difference smaller than the distance the number could have
// moved on its own has no sign this run is entitled to claim.
func movedOf(r workload.RouteDifference) string {
	if r.P95Ratio == nil {
		return r.Direction
	}
	if !r.Resolution.DirectionResolved(*r.P95Ratio) {
		return "too close to say"
	}
	return r.Direction
}

// resolutionOf renders what a route could see, in the same units as the change
// beside it so the two can be read against each other without arithmetic.
func resolutionOf(res workload.RouteResolution) string {
	if res.SmallestVisible == nil {
		return "nothing"
	}
	return fmt.Sprintf("%.0f%%", *res.SmallestVisible*100)
}

// ratioOf renders a ratio as a signed percentage, which is how somebody reads
// a regression. A nil ratio is "none" rather than "0 percent": no baseline and
// no change are different answers.
func ratioOf(v *float64) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprintf("%+.1f%%", *v*100)
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	if rev == "" {
		return "this build"
	}
	return rev
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func orDefaultString(chosen, fallback string) string {
	if chosen != "" {
		return chosen
	}
	return fallback
}

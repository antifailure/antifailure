package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// af workload is what a hosted control plane asks this engine to do.
//
// It replaces a case statement in a workflow file, and the three things it
// adds are the three things that case statement could not do. It refuses a
// knob the command it runs has no flag for, instead of dropping it silently.
// It writes a structured result, instead of leaving an exit code as the only
// thing anybody learns. And every result carries the plain af command that
// reproduces it, which is what makes a hosted number checkable on a laptop.
//
// It is not a fifth way to run anything. Each kind executes through the same
// orchestrator call af load run, af load scenario, af test and af explore
// already make, so a contract those commands hold is held here by
// construction rather than by a second implementation agreeing with the first.

func newWorkloadCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workload",
		Short: "Run a hosted workload definition and report what it measured",
		Long: strings.TrimSpace(`
A workload is a selection out of your manifest plus the knobs the command that
runs it actually has. There are four kinds and they are not four flavours of
one thing: a mix compiled from production telemetry, a declared HTTP journey, a
declared browser workflow, and a seeded exploration. They measure materially
different things and this command keeps them apart.

Everything it runs is already declared in antifailure.yaml. Nothing here can
send traffic your manifest does not name as safe.`),
	}
	cmd.AddCommand(newWorkloadRunCommand(e))
	cmd.AddCommand(newWorkloadTeardownCommand(e))
	cmd.AddCommand(newWorkloadPromoteCommand(e))
	cmd.AddCommand(newWorkloadCompareCommand(e))
	return cmd
}

func newWorkloadRunCommand(e *Env) *cobra.Command {
	var (
		kind, selection, duration, scale, seed, concurrency string
		runID, branch, result                               string
		timeout                                             time.Duration
		teardown                                            bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one workload and write what it measured",
		Long: strings.TrimSpace(`
Reads a workload definition, runs it through the command that kind names, and
writes a result document.

The result carries the plain command that reproduces the run. That is the point
of it: a hosted measurement nobody can reproduce is a number you have to
believe. Paste the command, get the same run.

A knob this workload's command has no flag for is refused rather than ignored.
A definition that sets concurrency on an observed_load fails before anything is
sent, because af load run has no concurrency flag and honouring it would be a
promise the run cannot keep.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			plan, parseErr := workload.Parse(workload.Request{
				RunID: runID, Kind: kind, Select: selection,
				Duration: duration, Scale: scale, Seed: seed, Concurrency: concurrency,
			})
			// A parse failure with no plan cannot produce a result document at
			// all, because there is nothing to say what was asked for.
			if plan == nil {
				return parseErr
			}

			o, _, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			// The repository root and the manifest digest come from the same
			// lookup the orchestrator did, rather than from the working
			// directory, so evidence paths are relative to the tree the run
			// actually read and not to wherever the command was typed.
			manifestPath, err := manifest.Find(e.WorkDir)
			if err != nil {
				return err
			}
			root := repoRoot(manifestPath)

			e.Out.Section("Running the " + string(plan.Kind) + " workload")
			res, err := workload.Execute(cmd.Context(), workload.Options{
				Plan: plan, Runner: o, Clock: e.Clock,
				Engine:         workload.Engine{Version: Version, Commit: Commit, Edition: Edition},
				Branch:         o.Branch(),
				Root:           root,
				ManifestDigest: manifestDigest(manifestPath),
				Timeout:        timeout,
				Teardown:       teardown,
			})
			if err != nil {
				return err
			}

			if writeErr := writeWorkloadResult(e, res, result); writeErr != nil {
				return writeErr
			}
			return workloadExit(res)
		},
	}
	// The list comes from the package rather than from a string here, because
	// a help text naming three of four kinds is drift nothing catches.
	cmd.Flags().StringVar(&kind, "kind", "",
		"Which kind of workload: "+workload.KindNames())
	cmd.Flags().StringVar(&selection, "select", "",
		"Comma separated names from the manifest this workload runs")
	cmd.Flags().StringVar(&duration, "duration", "",
		"How long to send load for, as a Go duration. observed_load only")
	cmd.Flags().StringVar(&scale, "scale", "",
		"Multiplier on production's rate. observed_load only")
	cmd.Flags().StringVar(&seed, "seed", "",
		"Makes two runs do the same thing. A number for load, free text for exploration")
	cmd.Flags().StringVar(&concurrency, "concurrency", "",
		"Ceiling on requests in flight. http_scenario only")
	cmd.Flags().StringVar(&runID, "run-id", "",
		"The control plane's identifier for this run, echoed back in the result")
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch to run against, defaulting to the checked out one")
	cmd.Flags().StringVar(&result, "result", "",
		"Write the result document here as well as to the terminal")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"Give up after this long and report a timeout rather than hanging")
	cmd.Flags().BoolVar(&teardown, "teardown", false,
		"Remove the environment when the work ends, however it ends")
	return cmd
}

// workloadOutcome names why a run is not a clean pass, or reports that it is.
//
// One function rather than two, because the terminal has to print the same
// answer the process exits with. Deciding it twice is how a run says pass on
// screen and exits 7, and the person reading has no way to tell which is
// wrong.
//
// The table, and the one row that differs from af test on purpose:
//
//	pass, flaky            0
//	fail                   8, the same as a failing workflow
//	blocked, unverified    7, verification failure
//	cancelled, timed out   9
//	resources still up     10, whatever the verdict was
//	a refused knob         2, usage
//
// af test exits 0 on unverified and does not count blocked against a run, and
// that single fact is how an entire nightly corpus in this repository went
// green having never once reached an agent. A hosted workload is a job whose
// exit code somebody gates on, so a run that measured nothing must not be
// indistinguishable from a run that found nothing. The result document says
// which it was; the code says only that it was not a clean pass.
func workloadOutcome(res *workload.Result) (aferrors.Code, string, bool) {
	// Standing resources outrank the verdict. A run that passed and left
	// containers behind costs money for as long as nobody looks, and reporting
	// it as clean is how nobody looks.
	if res.Teardown != nil && len(res.Teardown.Pending) > 0 {
		return aferrors.AFRUN030, fmt.Sprint(len(res.Teardown.Pending)), false
	}
	switch res.State {
	case workload.StateCancelled, workload.StateTimedOut:
		return aferrors.AFWLD014, orDefaultDetail(res), false
	case workload.StateFailed:
		if res.FailureCode == string(aferrors.AFWLD002) {
			return aferrors.AFWLD002, refusedKnobs(res.Refusals), false
		}
		return aferrors.AFWLD013, orDefaultDetail(res), false
	}
	switch res.Verdict {
	case workload.VerdictFail:
		return aferrors.AFWLD012, orDefaultDetail(res), false
	case workload.VerdictBlocked, workload.VerdictUnverified:
		return aferrors.AFWLD013, orDefaultDetail(res), false
	}
	return "", "", true
}

// codedOutcome builds the error a failing outcome carries.
func codedOutcome(res *workload.Result) error {
	code, detail, clean := workloadOutcome(res)
	if clean {
		return nil
	}
	switch code {
	case aferrors.AFRUN030:
		return aferrors.Coded(code, "count", detail)
	case aferrors.AFWLD002:
		return aferrors.Coded(code, "kind", string(res.Kind), "knobs", detail)
	default:
		return aferrors.Coded(code, "detail", detail)
	}
}

// workloadExit turns a result into the process's answer.
//
// Silent, because the render above has already said what happened and, in JSON
// mode, has already written the document a script is parsing. A second one
// after it would be a second document in the same stream.
func workloadExit(res *workload.Result) error {
	err := codedOutcome(res)
	if err == nil {
		return nil
	}
	return silent(err)
}

func orDefaultDetail(res *workload.Result) string {
	if strings.TrimSpace(res.Detail) != "" {
		return res.Detail
	}
	return "the run's verdict is " + res.Verdict
}

func refusedKnobs(rs []workload.Refusal) string {
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		names = append(names, r.Knob)
	}
	return strings.Join(names, ", ")
}

// writeWorkloadResult prints the result and writes it where a workflow can
// upload it.
//
// The document goes to the file whichever format the terminal is in, because a
// job that renders a human summary still has to hand the control plane
// something to store, and asking somebody to remember two flags to get both is
// how one of them is forgotten.
func writeWorkloadResult(e *Env, res *workload.Result, path string) error {
	if path != "" {
		body, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN001,
				"detail", "the result document could not be written to "+path)
		}
	}
	if e.Out.Format == FormatJSON {
		return e.Out.JSON(res)
	}
	renderWorkload(e, res)
	return nil
}

func renderWorkload(e *Env, res *workload.Result) {
	e.Out.Status(symbolForVerdict(res.Verdict), string(res.Kind)+" "+res.Verdict, res.Detail)
	for _, r := range res.Refusals {
		e.Out.Status(e.Out.S(StyleBad, SymbolFail), r.Knob, r.Reason)
	}

	m := res.Measured
	switch {
	case m.Requests != nil:
		e.Out.Printf("  %d requests, %s error rate, p95 %s\n",
			*m.Requests, percentOf(m.ErrorRate), millisOf(m.P95Ms))
	case m.Workflows != nil:
		e.Out.Printf("  %d passed, %d failed, %d flaky, %d blocked, %d unverified\n",
			deref(m.WorkflowsPassed), deref(m.WorkflowsFailed), deref(m.WorkflowsFlaky),
			deref(m.WorkflowsBlocked), deref(m.WorkflowsUnverified))
	case m.Goals != nil:
		e.Out.Printf("  %d of %d goals reached, %d findings\n",
			deref(m.GoalsReached), deref(m.Goals), deref(m.Findings))
	}
	if len(m.RefusedRoutes) > 0 {
		e.Out.Printf("  refused as unsafe: %s\n", strings.Join(m.RefusedRoutes, ", "))
	}
	if res.Teardown != nil {
		e.Out.Printf("  torn down: %d removed, %d pending\n",
			res.Teardown.Removed, len(res.Teardown.Pending))
	}

	// The code the process is about to exit with, in the words of the catalog.
	// Without it a person reading the terminal sees a verdict and a number and
	// has to work out which of the two meanings of "not a pass" they have.
	if err := codedOutcome(res); err != nil {
		e.Out.Error(err)
	}

	// Last, and on its own line, because it is the line somebody copies.
	e.Out.Println("")
	e.Out.Println("Reproduce this run:")
	e.Out.Printf("  %s\n", res.Reproduce.Command)
}

func symbolForVerdict(v string) string {
	switch v {
	case workload.VerdictPass:
		return SymbolOK
	case workload.VerdictFail:
		return SymbolFail
	}
	return SymbolSkip
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func percentOf(v *float64) string {
	if v == nil {
		return "unmeasured"
	}
	return fmt.Sprintf("%.2f percent", *v*100)
}

func millisOf(v *float64) string {
	if v == nil {
		return "unmeasured"
	}
	return fmt.Sprintf("%.0fms", *v)
}

// manifestDigest fingerprints the manifest the run read.
//
// Two runs of the same command line against different manifests are two
// different runs, and the reproducible command cannot say so on its own: the
// safe route list, the thresholds and the declared workflows all live in the
// file rather than on the command line.
func manifestDigest(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// af workload teardown
// ---------------------------------------------------------------------------

func newWorkloadTeardownCommand(e *Env) *cobra.Command {
	var branch, result string
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Remove the environment and report what was actually removed",
		Long: strings.TrimSpace(`
The same teardown 'af down' performs, with a machine readable acknowledgement.

It exists because a hosted teardown used to be a row marked torn down and a
comment saying the engine reads the row. Nothing read the row, so containers
kept running while the console said the environment was gone and the bill kept
growing. This reaches the runtime and says what it found: how many resources
were removed, and every one that is still standing with the reason it could
not be.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Removing the environment")
			td, downErr := o.Down(cmd.Context())
			ack := workload.TearDownResultOf(td, downErr)

			if result != "" {
				body, marshalErr := json.MarshalIndent(ack, "", "  ")
				if marshalErr != nil {
					return marshalErr
				}
				if writeErr := os.WriteFile(result, append(body, '\n'), 0o644); writeErr != nil {
					return aferrors.Wrap(writeErr, aferrors.AFRUN001,
						"detail", "the acknowledgement could not be written to "+result)
				}
			}
			if e.Out.Format == FormatJSON {
				if jsonErr := e.Out.JSON(ack); jsonErr != nil {
					return jsonErr
				}
			} else {
				e.Out.Printf("  %d removed, %d pending\n", ack.Removed, len(ack.Pending))
				for _, p := range ack.Pending {
					e.Out.Status(e.Out.S(StyleDim, SymbolSkip), p.Kind+" "+p.ID, p.Reason)
				}
			}
			if downErr != nil {
				return downErr
			}
			if len(ack.Pending) > 0 {
				return silent(aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(len(ack.Pending))))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch whose environment to remove")
	cmd.Flags().StringVar(&result, "result", "", "Write the acknowledgement here as well")
	return cmd
}

// ---------------------------------------------------------------------------
// af workload promote
// ---------------------------------------------------------------------------

func newWorkloadPromoteCommand(e *Env) *cobra.Command {
	var name, persona, seed, against string
	cmd := &cobra.Command{
		Use:   "promote [report.json]",
		Short: "Turn an exploration into a versioned workflow definition",
		Long: strings.TrimSpace(`
Reads an exploration report, produced by 'af explore -o json', and compiles one
exploration into the workflow definition a hosted workload runs.

What it will not pretend: the compiled workflow is planned again from the start
path on every run rather than replayed, so it can take a different route to the
same goal. Every promotion lists what the compilation could not carry over, one
sentence each, and records a digest of the journey the exploration walked so a
later exploration from the same seed can be compared against it.

An exploration that did not reach its goal is refused. The expectation a
compiled workflow asserts is the goal sentence, and a wander that never got
there is no evidence the goal is reachable.`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := readExplorationReport(e, args)
			if err != nil {
				return err
			}
			chosen, err := pickExploration(report, name)
			if err != nil {
				return err
			}
			if against != "" {
				return reportDrift(e, against, *chosen)
			}
			promotion, err := workload.Promote(*chosen, persona, seed)
			if err != nil {
				return err
			}
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(promotion)
			}
			renderPromotion(e, promotion)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "only", "",
		"Which exploration to promote, when the report holds more than one")
	cmd.Flags().StringVar(&persona, "persona", "",
		"Persona the compiled workflow runs as")
	cmd.Flags().StringVar(&seed, "seed", "",
		"Seed to quote in the replay command, defaulting to the exploration's own")
	cmd.Flags().StringVar(&against, "against", "",
		"A previous promotion document. Reports whether the route has moved since, instead of promoting")
	return cmd
}

func readExplorationReport(e *Env, args []string) (*explore.Report, error) {
	var body []byte
	var err error
	if len(args) == 1 && args[0] != "-" {
		body, err = os.ReadFile(args[0])
	} else {
		body, err = io.ReadAll(e.Stdin)
	}
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFWLD010,
			"exploration", "(none)", "detail", "the report could not be read: "+err.Error())
	}
	// The report may be an af explore JSON document, which wraps the
	// explorations beside a headline and findings, or the report itself. Both
	// are accepted because both are things a person has in a file, and
	// refusing one of them would send somebody to reshape a document by hand.
	var wrapper struct {
		Explorations []explore.Exploration `json:"explorations"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFWLD010,
			"exploration", "(none)", "detail", "the report is not valid JSON: "+err.Error())
	}
	if len(wrapper.Explorations) == 0 {
		return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", "(none)",
			"detail", "the report holds no explorations")
	}
	return &explore.Report{Explorations: wrapper.Explorations}, nil
}

func pickExploration(report *explore.Report, name string) (*explore.Exploration, error) {
	if name == "" {
		if len(report.Explorations) != 1 {
			names := make([]string, 0, len(report.Explorations))
			for _, x := range report.Explorations {
				names = append(names, x.Name)
			}
			return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", "(ambiguous)",
				"detail", "the report holds "+fmt.Sprint(len(report.Explorations))+
					" explorations; name one with --only: "+strings.Join(names, ", "))
		}
		return &report.Explorations[0], nil
	}
	for i := range report.Explorations {
		if report.Explorations[i].Name == name {
			return &report.Explorations[i], nil
		}
	}
	return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", name,
		"detail", "the report holds no exploration by that name")
}

func reportDrift(e *Env, path string, now explore.Exploration) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFWLD010, "exploration", now.Name,
			"detail", "the previous promotion could not be read: "+err.Error())
	}
	var previous workload.Promotion
	if err := json.Unmarshal(body, &previous); err != nil {
		return aferrors.Wrap(err, aferrors.AFWLD010, "exploration", now.Name,
			"detail", "the previous promotion is not valid JSON: "+err.Error())
	}
	drift := workload.CompareJourney(previous, now)
	if e.Out.Format == FormatJSON {
		return e.Out.JSON(drift)
	}
	symbol := SymbolOK
	if drift.Moved || !drift.StillReaches || !drift.SameSeed {
		symbol = SymbolSkip
	}
	e.Out.Status(symbol, drift.Exploration, drift.Detail)
	e.Out.Printf("  was %d steps, now %d\n", drift.WasSteps, drift.NowSteps)
	return nil
}

func renderPromotion(e *Env, p *workload.Promotion) {
	e.Out.Section("Promoting " + p.Source.Exploration)
	e.Out.Println("")
	e.Out.Raw(p.Body.ManifestBlock)
	e.Out.Println("")
	e.Out.Println("What this promotion does not carry over:")
	for _, d := range p.Dropped {
		e.Out.Printf("  %s\n", e.Out.Wrap(d, 2))
	}
	if len(p.Notes) > 0 {
		e.Out.Println("")
		e.Out.Println("What the exploration saw and the workflow will not:")
		for _, n := range p.Notes {
			e.Out.Printf("  %s\n", e.Out.Wrap(n, 2))
		}
	}
	e.Out.Println("")
	e.Out.Printf("Journey digest %s over %d steps.\n", p.Source.JourneyDigest[:12], p.Source.JourneySteps)
	e.Out.Println("Walk it again and compare with: af workload promote --against <this document>")
}

// ---------------------------------------------------------------------------
// af workload compare
// ---------------------------------------------------------------------------

func newWorkloadCompareCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compare <baseline.json> <candidate.json>",
		Short: "Difference two workload results of the same kind",
		Long: strings.TrimSpace(`
Reads two result documents and reports what moved: the run wide numbers, every
route on either side, and every threshold whose verdict changed.

It is not the differential oracle. 'af oracle' brings a second environment up
from a baseline revision, branches one golden for both so they start from
identical rows, and diffs the responses and the database. That is a far
stronger claim. This differences two runs that already happened, which is
cheaper, works over history, and is honest about what it cannot control: two
runs against two environments are not a controlled experiment, and every
comparison says so in its notes.`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			baseline, err := readWorkloadResult(args[0])
			if err != nil {
				return err
			}
			candidate, err := readWorkloadResult(args[1])
			if err != nil {
				return err
			}
			comparison, err := workload.Compare(baseline, candidate)
			if err != nil {
				return err
			}
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(comparison)
			}
			renderComparison(e, comparison)
			return nil
		},
	}
	return cmd
}

func readWorkloadResult(path string) (*workload.Result, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFWLD011,
			"detail", path+" could not be read: "+err.Error())
	}
	var res workload.Result
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFWLD011,
			"detail", path+" is not a workload result: "+err.Error())
	}
	if res.Schema != workload.ResultSchema {
		return nil, aferrors.Coded(aferrors.AFWLD011,
			"detail", path+" carries schema "+quoteOrNone(res.Schema)+
				" rather than "+workload.ResultSchema)
	}
	return &res, nil
}

func quoteOrNone(s string) string {
	if s == "" {
		return "no schema"
	}
	return `"` + s + `"`
}

func renderComparison(e *Env, c *workload.Comparison) {
	e.Out.Section("Comparing " + string(c.Kind))
	e.Out.Printf("  baseline %s on %s\n", c.Baseline.Verdict, orUnknown(c.Baseline.Branch))
	e.Out.Printf("  candidate %s on %s\n", c.Candidate.Verdict, orUnknown(c.Candidate.Branch))

	rows := [][]string{}
	for _, m := range c.Measures {
		rows = append(rows, []string{m.Measure, numberOf(m.Baseline), numberOf(m.Candidate), m.Direction})
	}
	if len(rows) > 0 {
		e.Out.Table([]Column{{Title: "measure"}, {Title: "baseline"},
			{Title: "candidate"}, {Title: "moved"}}, rows)
	}
	if c.Regressed > 0 {
		e.Out.Printf("\n  %d thresholds that passed on the baseline no longer do.\n", c.Regressed)
	}
	e.Out.Println("")
	e.Out.Println("What this comparison cannot see:")
	for _, n := range c.Notes {
		e.Out.Printf("  %s\n", e.Out.Wrap(n, 2))
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "an unnamed branch"
	}
	return s
}

func numberOf(v *float64) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprintf("%.3g", *v)
}

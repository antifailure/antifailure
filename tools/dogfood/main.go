// Command dogfood runs Antifailure against Antifailure and records what
// happened.
//
// The product's own pull request check is the product. Every failure, flake,
// slow step and confusing message this finds is one a customer would have
// found first, and the only difference between the two is who pays for it.
//
// It is a harness rather than a reimplementation. `af ci` is the command a
// customer wires into their own workflow, so that is the command this runs:
// anything this did differently would be a pipeline that works here and
// nowhere else, which is the opposite of the point. What it adds is the three
// things a customer does not need and a maintainer does.
//
//	A budget per step. A step that used to take two minutes and now takes
//	nine has regressed, and nothing about a green run says so. The budgets
//	live in this file, next to the reason for each number.
//
//	A record per run. `af` writes a typed event stream to
//	.antifailure/events, and this reduces one run of it to a small JSON
//	document that ten runs of can be compared. The ten green streak is a
//	claim about ten of these files, not about ten memories.
//
//	Teardown on every path, asserted afterwards. `af ci` tears down its own
//	environment; this checks that it did, because "the leak this product
//	exists to prevent" is a claim worth failing over rather than repeating.
//
// It is deliberately not in `just gate`. Its input is a staging database and a
// container runtime, neither of which exists on a laptop on a plane, and a
// gate that cannot run locally is a gate that stops meaning anything. It runs
// on a pull request and on a schedule, from .github/workflows/dogfood.yml.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	var (
		root   = flag.String("C", ".", "Repository root to run in")
		af     = flag.String("af", "", "Path to the af binary, defaulting to ./bin/af under the root")
		mode   = flag.String("mode", "pr", "pr or nightly")
		record = flag.String("record", "", "Write the run record here as JSON")
		report = flag.String("report", "", "Where af ci writes the pull request comment")
		// The same report as JSON, and it exists for one caller: the step that
		// posts this run's result to the hosted control plane's pull request
		// check. The check needs counts and an environment name, and reading
		// them back out of the Markdown would be a parser for prose.
		//
		// Without it the harness ran `af ci` with no --report-json, so the
		// workflow had no report.json, so the publish step had nothing to
		// send, so the check said "Nothing was verified" about a run that had
		// just verified everything. A flag the engine has had since it was
		// written, with no caller.
		reportJSON = flag.String("report-json", "",
			"Where af ci writes the same report as JSON, for the control plane")
		scale       = flag.Float64("budget-scale", 1, "Multiply every budget, for a slower machine")
		refresh     = flag.Bool("refresh-golden", false, "Refresh the golden first, as the nightly does")
		keep        = flag.Bool("keep", false, "Leave the environment up, for debugging")
		requireLoad = flag.Bool("require-load", false, "Require a completed load report with actual requests")
	)
	flag.Parse()

	if *mode != "pr" && *mode != "nightly" {
		fatal("mode is pr or nightly, not %q", *mode)
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		fatal("%v", err)
	}
	binary := *af
	if binary == "" {
		binary = filepath.Join(abs, "bin", "af")
	}
	if _, err := os.Stat(binary); err != nil {
		fatal("no af binary at %s. Build one with `go build -o bin/af ./engine/cmd/af`.", binary)
	}

	r := &runner{
		root: abs, af: binary, mode: *mode, scale: *scale,
		keep: *keep, refresh: *refresh, reportPath: *report, reportJSONPath: *reportJSON,
		requireLoad: *requireLoad,
	}
	run := r.do()

	if *record != "" {
		if err := writeRecord(*record, run); err != nil {
			fmt.Fprintf(os.Stderr, "dogfood: could not write the record: %v\n", err)
		}
	}
	run.print(os.Stdout)
	if !run.Green {
		os.Exit(1)
	}
}

// budget is how long a step may take before the run is red.
//
// Where these numbers come from, stated because the first version of this
// comment said something that was not true. It claimed every number was a real
// measurement times two taken on a GitHub hosted runner. They are laptop
// measurements times two, and at the time it was written no dogfood run had
// finished on a runner at all, so there was nothing from one to measure. A
// budget is a claim about how long something takes, and a claim about where a
// number came from is part of it.
//
// They are provisional until a green hosted run exists, and the doubling is
// what makes them provisional rather than wrong: it is wide enough that a
// runner being slower than a laptop does not read as a regression, and narrow
// enough to catch a step that has stopped working rather than slowed down.
// Replace each one with its hosted measurement once there is one.
//
// A budget that fires on a normal day is a budget somebody deletes, and a
// deleted budget catches nothing. That is the reason to be generous here, and
// it is not a reason to be vague about which machine the number came from.
type budget struct {
	step string
	max  time.Duration
	why  string
}

var budgets = []budget{
	{"doctor", 45 * time.Second,
		"It reads the local machine and asks the daemon for its version. " +
			"Anything longer is a runtime that is not answering, which is worth " +
			"failing on before an environment is half built."},
	{"golden refresh", 12 * time.Minute,
		"Copy, mask, verify and publish, over the whole staging control plane. " +
			"The measured run is 28 seconds against 2,600 rows, and the budget is " +
			"set for a corpus that grows rather than for the corpus today."},
	{"up", 25 * time.Minute,
		"Branching the golden is seconds. The rest is the control plane image, " +
			"and the number that matters is the console stage inside it: a cold " +
			"build of it measured 860 seconds on this machine, so a ten minute " +
			"budget was one this step could not have met on its first honest " +
			"attempt. A cold layer cache is the normal case on a hosted runner, " +
			"not the exception, which is what the earlier number assumed."},
	{"test", 12 * time.Minute,
		"Six workflows, two attempts each, driven by an agent. In recorded mode " +
			"the model is a cassette lookup, so this measures the browser and the " +
			"application rather than a provider's queue."},
	{"load", 4 * time.Minute,
		"Thirty seconds of traffic plus the ramp and the report."},
	{"down", 3 * time.Minute,
		"Removing containers, a network, a proxy and a database branch. Slower " +
			"than this means something is not answering, and a teardown that " +
			"hangs is how an environment outlives its pull request."},
	{"ci", 40 * time.Minute,
		"The whole thing, which is what a customer's pull request actually " +
			"waits on. It is not the sum of the steps above: it is the number " +
			"somebody watches a spinner for."},
}

func budgetFor(step string) (budget, bool) {
	for _, b := range budgets {
		if b.step == step {
			return b, true
		}
	}
	return budget{}, false
}

// runner drives one dogfood run.
type runner struct {
	root       string
	af         string
	mode       string
	scale      float64
	keep       bool
	refresh    bool
	reportPath string
	// Where `af ci` writes the machine readable copy. Separate from
	// reportPath because the Markdown is read by a person on the pull request
	// and this one is read by the control plane's check.
	reportJSONPath string
	requireLoad    bool
}

// Step is one command, timed and judged.
type Step struct {
	Name     string  `json:"name"`
	Command  string  `json:"command"`
	Seconds  float64 `json:"seconds"`
	Budget   float64 `json:"budget_seconds,omitempty"`
	Over     bool    `json:"over_budget,omitempty"`
	ExitCode int     `json:"exit_code"`
	// Tail is the last of the output, for a failure somebody has to read
	// without opening the artifact.
	Tail []string `json:"tail,omitempty"`
}

// Run is the record of one dogfood run.
//
// Written as JSON so that a streak is a fact about files rather than a claim
// in a document. Ten of these, all Green, with no Findings, is what the ten
// green streak means.
type Run struct {
	StartedAt time.Time `json:"started_at"`
	Mode      string    `json:"mode"`
	Commit    string    `json:"commit,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Seconds   float64   `json:"seconds"`
	Green     bool      `json:"green"`
	Steps     []Step    `json:"steps"`
	// Environment is what af ci reported bringing up, read from the event
	// stream rather than from the prose.
	Environment string `json:"environment,omitempty"`
	Golden      string `json:"golden,omitempty"`
	// Events counts what the run emitted, by type, which is the cheapest
	// summary that still shows a run doing less than the one before it.
	Events map[string]int `json:"events,omitempty"`
	// Leaked names resources still present after teardown. Any is a failure.
	Leaked []string `json:"leaked,omitempty"`
	// Swept says the leak check actually ran. Without it an empty Leaked is
	// two different facts wearing one value: nothing was left behind, or
	// nothing was looked for. The second was true of every run in CI.
	Swept bool `json:"swept"`
	// Verdicts counts what the workflows returned, by verdict. A run with
	// nothing but `blocked` in here reached no verdict at all, which is the
	// shape a streak must not be built out of.
	Verdicts map[string]int `json:"verdicts,omitempty"`
	// Findings are what this run surfaced that a person has to classify:
	// product bug, CI defect, or docs gap. One line each.
	Findings []string `json:"findings,omitempty"`
}

func (r *runner) do() *Run {
	started := time.Now()
	run := &Run{
		StartedAt: started, Mode: r.mode, Green: true,
		Commit: r.capture("git", "rev-parse", "HEAD"),
		Branch: r.capture("git", "rev-parse", "--abbrev-ref", "HEAD"),
	}

	// An interrupt has to reach teardown, not skip it. A cancelled job that
	// leaves a database branch behind is the exact failure this product sells
	// against, and a harness that causes it has no standing to report it.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	interrupted := false
	go func() {
		<-stop
		interrupted = true
	}()

	r.step(run, "doctor", r.af, "doctor")

	if r.refresh {
		r.step(run, "golden refresh", r.af, "golden", "refresh")
	}

	args := []string{"ci", "--no-color"}
	if r.reportPath != "" {
		args = append(args, "--report", r.reportPath)
	}
	if r.reportJSONPath != "" {
		args = append(args, "--report-json", r.reportJSONPath)
	}
	if r.mode == "nightly" {
		args = append(args, "--load")
	}
	if r.keep {
		args = append(args, "--keep")
	}
	ci := r.step(run, "ci", append([]string{r.af}, args...)...)

	r.readVerdicts(run)
	r.readLoadEvidence(run)

	// Only ask about events when the run reached the point of making an
	// environment. `af ci` refusing a directory with no manifest produces no
	// events, correctly, and reporting that as a defect to classify is the
	// same false positive as blaming a run for another terminal's containers.
	if ci.ExitCode == 0 || ci.Seconds > 5 {
		r.readEvents(run)
	}

	if !r.keep {
		r.sweep(run)
	}

	if interrupted {
		run.Green = false
		run.Findings = append(run.Findings, "The run was interrupted.")
	}

	run.Seconds = time.Since(started).Seconds()
	for _, s := range run.Steps {
		if s.Over {
			run.Green = false
			b, _ := budgetFor(s.Name)
			run.Findings = append(run.Findings, fmt.Sprintf(
				"%s took %.0fs against a budget of %.0fs. %s",
				s.Name, s.Seconds, s.Budget, b.why))
		}
		if s.ExitCode != 0 {
			run.Green = false
		}
	}
	return run
}

// step runs one command, times it, and judges it against its budget.
func (r *runner) step(run *Run, name string, argv ...string) Step {
	fmt.Fprintf(os.Stderr, "dogfood: %s\n", name)
	s := Step{Name: name, Command: strings.Join(argv, " ")}
	if b, ok := budgetFor(name); ok {
		s.Budget = (time.Duration(float64(b.max) * r.scale)).Seconds()
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = r.root
	cmd.Stdin = nil
	started := time.Now()
	out, err := cmd.CombinedOutput()
	s.Seconds = time.Since(started).Seconds()

	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		s.ExitCode = exit.ExitCode()
	default:
		s.ExitCode = -1
		out = append(out, []byte("\n"+err.Error())...)
	}
	s.Tail = tail(string(out), 200)
	if s.Budget > 0 && s.Seconds > s.Budget {
		s.Over = true
	}
	// Echoed as it happens. A job that fails at minute nineteen should not
	// require somebody to wait for the summary to see why.
	for _, line := range s.Tail {
		fmt.Fprintf(os.Stderr, "  %s\n", line)
	}
	run.Steps = append(run.Steps, s)
	return s
}

// readEvents derives the per step timings from the environment's own event
// log, and summarises what the run emitted.
//
// readVerdicts asks what the workflows actually did.
//
// Nothing here asked, and that was the hole. `af ci` exits zero when every
// workflow comes back blocked, because blocked means the environment or the
// runner could not carry a workflow through and a gap in our tooling must
// never fail somebody's build. Correct for the build, and it left this harness
// recording green for a run in which the agents drove nothing at all: two
// green runs went by that way, with all six workflows blocked on a sign-in
// form the runner could not find, and the record said green and listed no
// finding.
//
// A streak means ten runs that carried a workflow through. So a run that
// reached a verdict on none of them is a finding, which is what keeps it out
// of the streak. It is not a failure: the change under test did nothing wrong,
// and failing it would teach people to ignore this job.
//
// Read from the report rather than from the event stream, which is where the
// timings come from, because the engine emits no agent.* event at all. That is
// known and written down: engine/internal/events/emitters_test.go lists all
// four as unemitted, with the reason, which is that the runner reports over
// its own JSON boundary and nothing bridges it to the bus. Until something
// does, the report is the only place a verdict exists.
func (r *runner) readVerdicts(run *Run) {
	if r.reportPath == "" {
		return
	}
	body, err := os.ReadFile(r.reportPath)
	if err != nil {
		run.Findings = append(run.Findings, fmt.Sprintf(
			"af ci was asked to write its report to %s and it is not readable: %v. "+
				"Nothing here can say what the workflows did.", r.reportPath, err))
		return
	}
	counts := verdicts(string(body))
	if len(counts) == 0 {
		return
	}
	run.Verdicts = counts
	carried := 0
	for v, n := range counts {
		if v != "blocked" {
			carried += n
		}
	}
	if carried == 0 {
		run.Findings = append(run.Findings, fmt.Sprintf(
			"All %d workflows came back blocked, so this run proved nothing about the "+
				"product and does not count towards the streak. Blocked is the "+
				"environment or the runner, not the change.", counts["blocked"]))
	}
}

// verdicts counts the rows of the report's workflow table.
//
// The table is generated by report.Run.Markdown and the words are the ones
// report.symbol writes: passed, FAILED, flaky, warning, blocked, unverified.
var workflowRow = regexp.MustCompile("(?m)^\\| `[^`]+` \\| (passed|FAILED|flaky|warning|blocked|unverified) \\|")

func verdicts(report string) map[string]int {
	out := map[string]int{}
	for _, m := range workflowRow.FindAllStringSubmatch(report, -1) {
		out[m[1]]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// unreadable names the checks that did not happen, rather than leaving them
// looking like checks that passed.
//
// Every assertion in readEvents sits after the read, so an unreadable stream
// skips all of them and the record comes back with nothing to say. The sharpest
// one is the teardown assertion: `env.destroyed == 0` sets the run not green,
// and it is downstream of the read, so a missing log disables the guard whose
// entire job is to notice that nothing recorded the teardown. It cannot fire on
// the condition it was written for.
//
// So the run says so, in the terms a person acts on. It is a finding rather
// than a failure for the same reason blocked is: the change under test did
// nothing wrong, and failing it would teach people to ignore this job. What the
// finding buys is the streak, which is ten runs green with no findings, and a
// run that measured nothing must not be one of them.
func unreadable(run *Run, where string) {
	run.Findings = append(run.Findings, fmt.Sprintf(
		"Four checks did not run, because all four read the event stream and %s "+
			"could not be read: the per phase budgets, the assertion that something "+
			"recorded the environment being torn down, the count of resources the "+
			"engine reported leaking, and the leak sweep, which is scoped to the "+
			"environment the stream names. None of them passed. They were not asked.",
		where))
}

// The timings come from typed events rather than from the prose af ci prints,
// which matters for a reason beyond taste: the prose has no clock in it. A
// section heading tells you a step was reached and nothing about what it cost,
// so a budget built on it would be a budget on the whole command and the one
// slow step inside it would stay invisible. Every event carries the injected
// clock's timestamp, so a phase is the interval between the event that opens
// it and the event that closes it, measured by the engine rather than guessed
// at from outside.
func (r *runner) readEvents(run *Run) {
	dir := filepath.Join(r.root, stateDir, logDir)
	path, err := newestLog(dir)
	if err != nil || path == "" {
		run.Findings = append(run.Findings, fmt.Sprintf(
			"No event log under %s, so this run has no machine readable record of "+
				"what it did and no per step timing.", dir))
		unreadable(run, dir)
		return
	}
	events, err := readLog(path, run.StartedAt)
	if err != nil {
		run.Findings = append(run.Findings, "The event log could not be read: "+err.Error())
		unreadable(run, path)
		return
	}
	if len(events) == 0 {
		run.Findings = append(run.Findings, fmt.Sprintf(
			"The event log at %s carries nothing from this run.", path))
		unreadable(run, path)
		return
	}

	run.Events = map[string]int{}
	for _, e := range events {
		run.Events[e.Type]++
		if e.Env != "" {
			run.Environment = e.Env
		}
		if g, ok := e.Data["golden"].(string); ok && g != "" {
			run.Golden = g
		}
	}

	for _, p := range phases {
		from, to, ok := span(events, p.opens, p.closes)
		if !ok {
			continue
		}
		s := Step{Name: p.step, Command: p.opens + " to " + p.closes,
			Seconds: to.Sub(from).Seconds()}
		if b, ok := budgetFor(p.step); ok {
			s.Budget = (time.Duration(float64(b.max) * r.scale)).Seconds()
			s.Over = s.Seconds > s.Budget
		}
		run.Steps = append(run.Steps, s)
	}

	// A run that reached the end without a teardown event is the one shape
	// worth naming even when the leak check comes back clean, because the
	// check runs after and a slow teardown would pass it by accident.
	if run.Events["env.destroyed"] == 0 && !r.keep {
		run.Green = false
		run.Findings = append(run.Findings,
			"The run emitted no env.destroyed event, so nothing recorded that the "+
				"environment was torn down.")
	}
	if n := run.Events["resource.leaked"]; n > 0 {
		run.Green = false
		run.Findings = append(run.Findings, fmt.Sprintf(
			"The engine reported %d leaked resources during the run.", n))
	}
}

// phase is one interval worth timing, named by the events that bound it.
type phase struct {
	step   string
	opens  string
	closes string
}

// The intervals a budget is set against. Each is bounded by two events the
// engine already emits, so nothing here depends on a message somebody may
// reword.
// Two of these six cannot fire, and it is worth knowing which before reading a
// run that shows four steps and wondering where the other two went. The engine
// emits no agent.* and no load.* event: both sets are listed as unemitted in
// engine/internal/events/emitters_test.go, with the reason, because the runner
// and the load generator both return their results to the caller rather than
// putting them on the bus. So `test` and `load` are budgets against intervals
// nothing bounds. They are left here rather than deleted for the same reason
// that list keeps the types: the budget is the measurement somebody wants, and
// removing it would hide the gap instead of recording it.
var phases = []phase{
	{"golden refresh", "golden.refreshing", "golden.ready"},
	{"database branch", "db.branching", "db.branched"},
	{"up", "env.creating", "env.ready"},
	{"test", "agent.started", "agent.finished"},
	{"load", "load.sample", "load.finished"},
	{"down", "env.destroying", "env.destroyed"},
}

// logEvent is the part of an event this reads. The stream carries more.
type logEvent struct {
	TS   time.Time      `json:"ts"`
	Env  string         `json:"env"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// span returns the first opening event and the last closing one.
//
// Last rather than first, because a phase with several actors inside it (two
// services building, six workflows running) opens once and closes once per
// actor, and the interval somebody waits for is bounded by the last of them.
func span(events []logEvent, opens, closes string) (time.Time, time.Time, bool) {
	var from, to time.Time
	for _, e := range events {
		if e.Type == opens && from.IsZero() {
			from = e.TS
		}
		if e.Type == closes {
			to = e.TS
		}
	}
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// newestLog returns the most recently written log in dir.
func newestLog(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var newest string
	var newestAt time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestAt) {
			newest, newestAt = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return newest, nil
}

// readLog reads the events this run wrote.
//
// The log appends across runs, on purpose: it is the environment's history and
// rotates by size rather than by run. So everything before this run started is
// skipped, which is what makes a timing a measurement of this run rather than
// of the last one that happened to be slower.
func readLog(path string, since time.Time) ([]logEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []logEvent
	sc := bufio.NewScanner(f)
	// A build log line can be long, and the default 64 KiB limit would end
	// the scan silently in the middle of a run.
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var e logEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.TS.Before(since) {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// leaks asks the runtime what this run left behind.
//
// Scoped to this environment, not to the label alone. A CI runner holds one
// job and the two questions have the same answer there, but a laptop holds
// whatever else its owner is running, and the first version of this reported
// twelve leaks that belonged to a test suite in another terminal. A check that
// blames a run for somebody else's containers is a check people learn to
// ignore, which is worse than not having it.
//
// The environment identifier comes from the event stream, so a run that
// produced no events reports nothing rather than reporting everything.
// sweep asks whether teardown left anything behind, and records whether it was
// able to ask at all.
//
// Not swept and swept clean were the same answer here for as long as this file
// existed: leaks() returns nil on its first line for an unnamed environment,
// and nil is also what a clean sweep returns, so `"leaked": null` in the record
// meant either one and a reader had no way to tell them apart. The environment
// identifier comes only from the event stream, so every run that could not read
// the stream reported a clean teardown it had never looked for, and said
// nothing while doing it.
func (r *runner) sweep(run *Run) {
	if run.Environment == "" {
		run.Findings = append(run.Findings, "The leak check did not run. It is "+
			"scoped to the environment the event stream names and this run named "+
			"none, so nothing here says whether teardown left anything behind.")
		return
	}
	run.Swept = true
	run.Leaked = r.leaks(run.Environment)
	if len(run.Leaked) > 0 {
		run.Green = false
		run.Findings = append(run.Findings, fmt.Sprintf(
			"Teardown left %d resources behind: %s. An environment that outlives "+
				"its run is the leak this product exists to prevent, so this is a "+
				"product bug rather than a flake.",
			len(run.Leaked), strings.Join(run.Leaked, ", ")))
	}
}

func (r *runner) leaks(envID string) []string {
	if envID == "" {
		return nil
	}
	var found []string
	filter := "label=" + labelEnv + "=" + envID
	for _, q := range [][]string{
		{"ps", "-aq", "--filter", filter},
		{"network", "ls", "-q", "--filter", filter},
		{"volume", "ls", "-q", "--filter", filter},
	} {
		out := r.capture("docker", q...)
		for _, id := range strings.Fields(out) {
			found = append(found, q[0]+":"+id)
		}
	}
	sort.Strings(found)
	return found
}

// Three constants the engine owns and this module cannot import.
//
// tools/ is its own Go module and all three live under engine/internal, so a
// copy with its source named is a smaller price than widening those packages
// for three strings. What that trade costs is drift, and it cost it: this
// harness was written against `attachEventLog`, which wrote the stream to
// .antifailure/events. That function is gone. The log is written by
// telemetry's FileSink now, to .antifailure/logs, and the reader was never
// moved with the writer, so readEvents found no log on every run in CI.
//
// Everything downstream of the event stream was therefore dead while the run
// was still recorded green: the per phase budgets, the env.destroyed
// assertion, the resource.leaked count, and the leak sweep, which is scoped to
// the environment the event stream names and so swept nothing at all. Nothing
// that is not read can fail.
//
// TestCopiedConstantsMatchTheEngine reads the engine's own source and fails
// when any of these three drifts, which is the check that was missing.
const (
	// engine/internal/env.StateDir
	stateDir = ".antifailure"
	// engine/internal/telemetry.LogDir
	logDir = "logs"
	// engine/internal/dockerutil.LabelEnv
	labelEnv = "dev.antifailure.env"
)

func (r *runner) capture(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = r.root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (run *Run) print(w *os.File) {
	fmt.Fprintf(w, "\ndogfood: %s run on %s, %s\n",
		run.Mode, shortCommit(run.Commit), duration(run.Seconds))
	for _, s := range run.Steps {
		mark := "ok"
		switch {
		case s.ExitCode != 0:
			mark = fmt.Sprintf("FAILED (%d)", s.ExitCode)
		case s.Over:
			mark = fmt.Sprintf("OVER BUDGET (%s)", duration(s.Budget))
		}
		fmt.Fprintf(w, "  %-16s %8s  %s\n", s.Name, duration(s.Seconds), mark)
	}
	if len(run.Verdicts) > 0 {
		// Beside the timings, because a run that took four minutes and reached
		// no verdict looks exactly like a run that took four minutes and did
		// the work, and the timing is the part people read.
		kinds := make([]string, 0, len(run.Verdicts))
		for v, n := range run.Verdicts {
			kinds = append(kinds, fmt.Sprintf("%d %s", n, v))
		}
		sort.Strings(kinds)
		fmt.Fprintf(w, "  %-16s %8s  %s\n", "workflows", "", strings.Join(kinds, ", "))
	}
	if len(run.Findings) > 0 {
		fmt.Fprintf(w, "\n%s to classify as a product bug, a CI defect, or a docs gap:\n",
			plural(len(run.Findings), "thing", "things"))
		for _, f := range run.Findings {
			fmt.Fprintf(w, "  - %s\n", f)
		}
	}
	if run.Green {
		fmt.Fprintf(w, "\ngreen\n")
		return
	}
	fmt.Fprintf(w, "\nnot green\n")
}

func writeRecord(path string, run *Run) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	body, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func tail(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func duration(sec float64) string {
	d := time.Duration(sec * float64(time.Second))
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// plural writes a count with the right noun, because "1 things" reads as a
// tool nobody finished.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func shortCommit(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	if c == "" {
		return "an unknown commit"
	}
	return c
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dogfood: "+format+"\n", args...)
	os.Exit(2)
}

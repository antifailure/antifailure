package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Every budget names a step some phase or step actually produces.
//
// A budget for a step nothing emits is a budget that never fires, and it reads
// in review as a guard that is being enforced. This is the same argument
// gatecheck makes about a stale exemption.
func TestBudgets_EveryOneIsReachable(t *testing.T) {
	produced := map[string]bool{
		// The two steps run directly, by name.
		"doctor": true, "ci": true, "golden refresh": true,
	}
	for _, p := range phases {
		produced[p.step] = true
	}
	for _, b := range budgets {
		if !produced[b.step] {
			t.Errorf("budget %q is for a step nothing produces, so it can never fire", b.step)
		}
	}
}

// And every budget says why it is the number it is.
func TestBudgets_EveryOneCarriesItsReason(t *testing.T) {
	for _, b := range budgets {
		if len(b.why) < 40 {
			t.Errorf("budget %q has no reason worth reading: %q", b.step, b.why)
		}
		if b.max <= 0 {
			t.Errorf("budget %q has no limit", b.step)
		}
	}
}

// A phase is bounded by the last closing event, not the first.
//
// Two services build inside one up, and six workflows run inside one test. The
// interval somebody waits for ends when the last of them finishes, and timing
// to the first would report a five minute step as forty seconds.
func TestSpan_EndsAtTheLastClosingEvent(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	events := []logEvent{
		{TS: base, Type: "build.started"},
		{TS: base.Add(10 * time.Second), Type: "build.started"},
		{TS: base.Add(40 * time.Second), Type: "build.finished"},
		{TS: base.Add(5 * time.Minute), Type: "build.finished"},
	}
	from, to, ok := span(events, "build.started", "build.finished")
	if !ok {
		t.Fatal("the span was not found")
	}
	if got := to.Sub(from); got != 5*time.Minute {
		t.Errorf("span is %s, want 5m", got)
	}
}

// An unfinished phase is absent rather than zero.
func TestSpan_RefusesAPhaseThatNeverClosed(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if _, _, ok := span([]logEvent{{TS: base, Type: "env.creating"}},
		"env.creating", "env.ready"); ok {
		t.Error("a phase that never closed was reported as a measurement")
	}
}

// The log appends across runs, so a run reads only its own events.
//
// Without this, the first dogfood run on a machine is timed correctly and
// every one after it inherits the slowest run in the file's history.
func TestReadLog_SkipsWhatCameBeforeThisRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.ndjson")
	old := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	write(t, path,
		logEvent{TS: old, Type: "env.creating"},
		logEvent{TS: old.Add(time.Hour), Type: "env.ready"},
		logEvent{TS: now, Type: "env.creating"},
		logEvent{TS: now.Add(30 * time.Second), Type: "env.ready"},
	)

	events, err := readLog(path, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events from this run, want 2", len(events))
	}
	from, to, ok := span(events, "env.creating", "env.ready")
	if !ok {
		t.Fatal("no span")
	}
	if got := to.Sub(from); got != 30*time.Second {
		t.Errorf("up took %s, want 30s: the previous run's events were counted", got)
	}
}

// A line that is not JSON does not end the read.
//
// A crashed process can leave a partial line, and one truncated write must not
// discard every event after it. This is the same rule the masking rules follow
// about one bad row.
func TestReadLog_ToleratesAPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.ndjson")
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	write(t, path, logEvent{TS: now, Type: "env.creating"})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"ts\":\"2026-08-29T09:00:0\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	write(t, path, logEvent{TS: now.Add(time.Second), Type: "env.ready"})

	events, err := readLog(path, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events, want 2: a partial line ended the read", len(events))
	}
}

// A run that leaked is not green, whatever else it did.
func TestRun_ALeakIsNotGreen(t *testing.T) {
	run := &Run{Green: true, Events: map[string]int{"resource.leaked": 2}}
	// The same shape the runner applies.
	if n := run.Events["resource.leaked"]; n > 0 {
		run.Green = false
	}
	if run.Green {
		t.Error("a run that leaked resources was reported green")
	}
}

// The record round trips, because a streak is a claim about these files.
func TestRecord_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "record.json")
	want := &Run{
		StartedAt: time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC),
		Mode:      "pr", Green: true, Seconds: 421,
		Steps:  []Step{{Name: "up", Seconds: 92, Budget: 600}},
		Events: map[string]int{"env.ready": 1},
	}
	if err := writeRecord(path, want); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Run
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || !got.Green || len(got.Steps) != 1 || got.Steps[0].Name != "up" {
		t.Errorf("record did not round trip: %+v", got)
	}
}

func TestDuration_ReadsAsSomebodyWouldSayIt(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0s", 42: "42s", 60: "1m00s", 421: "7m01s",
	} {
		if got := duration(in); got != want {
			t.Errorf("duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func write(t *testing.T, path string, events ...logEvent) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
}

// The leak check asks about this run's environment, not about the label.
//
// The regression, seen on the first real run of this tool: it reported twelve
// leaked resources that belonged to a test suite running in another terminal.
// A check that blames a run for somebody else's containers is a check people
// learn to ignore, and an ignored leak check is worse than none, because the
// leak is the thing this product sells against.
func TestLeaks_SaysNothingWithoutAnEnvironment(t *testing.T) {
	r := &runner{root: t.TempDir()}
	if got := r.leaks(""); got != nil {
		t.Errorf("a run that produced no environment reported %v as leaked", got)
	}
}

// TestCopiedConstantsMatchTheEngine reads the engine's own source for the three
// constants this file copies, and fails when a copy has drifted from it.
//
// This is the check that was not here. The event log directory was copied as
// "events" while the engine wrote "logs", so readEvents found no log on every
// run there has ever been, and everything downstream of the event stream was
// dead while the run was still recorded green. Every unit test in this file
// passed throughout, because they all call readLog with a path a test made.
//
// Two artifacts, each individually valid, jointly wrong. The only thing that
// catches that class is a check that reads both.
func TestCopiedConstantsMatchTheEngine(t *testing.T) {
	for _, c := range []struct {
		name  string
		file  string
		decl  string
		local string
	}{
		{"StateDir", "engine/internal/env/env.go", "StateDir", stateDir},
		{"LogDir", "engine/internal/telemetry/telemetry.go", "LogDir", logDir},
		{"LabelEnv", "engine/internal/dockerutil/dockerutil.go", "LabelEnv", labelEnv},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join("..", "..", c.file)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			re := regexp.MustCompile(`(?m)^\s*(?:const\s+)?` + c.decl + `\s*=\s*"([^"]*)"`)
			m := re.FindSubmatch(src)
			if m == nil {
				t.Fatalf("%s declares no %s = \"...\" any more, so the copy in this "+
					"file cannot be checked against it", c.file, c.decl)
			}
			if got := string(m[1]); got != c.local {
				t.Errorf("%s.%s is %q and the copy here is %q. The copy is what the "+
					"harness reads with, so it is the harness that is wrong.",
					c.file, c.decl, got, c.local)
			}
		})
	}
}

// TestVerdicts_AllBlockedIsAFinding reads the report `af ci` actually wrote in
// the Dogfood run of 2026-08-31, downloaded from the run's own artifact.
//
// That run was recorded green with no findings and all six workflows blocked,
// which is the record this harness is supposed to prevent. A hand written
// table would have proved that the regular expression matches a table I wrote;
// this proves it matches the one `af ci` writes.
func TestVerdicts_AllBlockedIsAFinding(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "blocked-run.report.md"))
	if err != nil {
		t.Fatal(err)
	}
	counts := verdicts(string(body))
	if counts["blocked"] != 6 {
		t.Fatalf("counted %v, want six blocked: the report's table is not being read", counts)
	}
	if len(counts) != 1 {
		t.Fatalf("counted %v, want blocked alone", counts)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	run := &Run{Green: true}
	(&runner{reportPath: path}).readVerdicts(run)
	if len(run.Findings) != 1 {
		t.Fatalf("findings %v, want one saying nothing was carried through", run.Findings)
	}
	if !strings.Contains(run.Findings[0], "does not count towards the streak") {
		t.Errorf("finding does not say why it matters: %q", run.Findings[0])
	}
	// Blocked is the environment's problem, not the change's, so the job stays
	// green and the finding is what keeps the run out of the streak.
	if !run.Green {
		t.Error("a blocked run failed the job, which teaches people to ignore it")
	}
}

// TestVerdicts_APassIsNotAFinding is the other half. Without it the check
// above passes against a function that appends a finding unconditionally.
func TestVerdicts_APassIsNotAFinding(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "blocked-run.report.md"))
	if err != nil {
		t.Fatal(err)
	}
	// One row of the real report, given the verdict it was meant to have.
	carried := strings.Replace(string(body),
		"| `sign-in-with-a-link` | blocked |", "| `sign-in-with-a-link` | passed |", 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte(carried), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &Run{Green: true}
	(&runner{reportPath: path}).readVerdicts(run)
	if len(run.Findings) != 0 {
		t.Fatalf("findings %v, want none: one workflow reached a verdict", run.Findings)
	}
	if run.Verdicts["passed"] != 1 || run.Verdicts["blocked"] != 5 {
		t.Errorf("verdicts %v, want one passed and five blocked", run.Verdicts)
	}
}

// TestUnreadableStream_SaysWhichChecksDidNotRun is the second half of the
// directory mismatch, and the half that was silent.
//
// readEvents said "No event log under ..." and stopped, which is honest about
// the file and says nothing about the four assertions that live after the
// early return. Two runs in CI printed that one line and the word green, and
// what a reader took from it was that the log was missing. What was actually
// true is that nothing had checked the budgets, the teardown, the leaked
// resource count, or the leak sweep.
func TestUnreadableStream_SaysWhichChecksDidNotRun(t *testing.T) {
	run := &Run{Green: true}
	r := &runner{root: t.TempDir()}
	r.readEvents(run)

	joined := strings.Join(run.Findings, "\n")
	if !strings.Contains(joined, "No event log under") {
		t.Errorf("nothing said the log was missing: %v", run.Findings)
	}
	for _, want := range []string{
		"per phase budgets",
		"torn down",
		"leaking",
		"leak sweep",
		"They were not asked",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the findings do not mention %q, so a reader cannot tell a check "+
				"that did not run from one that passed:\n%s", want, joined)
		}
	}
	// Still green. The change under test did nothing wrong, and a job that
	// fails for this is a job people learn to ignore. The findings are what
	// keep the run out of the streak.
	if !run.Green {
		t.Error("an unreadable stream failed the job rather than reporting it")
	}
}

// TestLeakCheck_NotSweptIsNotClean is the one that was genuinely silent.
//
// leaks() returns nil on its first line for an unnamed environment, and nil is
// also what a clean sweep returns, so `"leaked": null` in the record meant
// either. The environment identifier only ever comes from the event stream, so
// with the stream unreadable every run in CI reported a clean teardown it had
// never looked for, and printed nothing at all about it.
func TestLeakCheck_NotSweptIsNotClean(t *testing.T) {
	r := &runner{root: t.TempDir()}

	unnamed := &Run{Green: true}
	r.sweep(unnamed)
	if unnamed.Swept {
		t.Error("an unnamed environment was recorded as swept")
	}
	if len(unnamed.Findings) != 1 || !strings.Contains(unnamed.Findings[0], "did not run") {
		t.Fatalf("findings %v, want one saying the sweep did not run", unnamed.Findings)
	}

	// A named environment with nothing left behind is the other value. Without
	// this half, the check above passes against a sweep that never runs.
	named := &Run{Green: true, Environment: "antifailure-nothing-here-000000"}
	r.sweep(named)
	if !named.Swept {
		t.Error("a named environment was not recorded as swept")
	}
	if len(named.Findings) != 0 {
		t.Errorf("findings %v, want none: the sweep ran and found nothing", named.Findings)
	}
	if len(named.Leaked) != 0 {
		t.Errorf("leaked %v, want none", named.Leaked)
	}
}

// The nightly corpus is every example, and only examples that exist.
//
// Both halves have been wrong at once. The matrix named examples/orders-api and
// examples/rails-shop, neither of which has ever been a directory, and left out
// examples/django-api and examples/next-app, which have manifests and had never
// been run by anything. Nothing read the matrix value either, so all three legs
// ran the repository root under three example names.
//
// A name with nothing behind it is worse than a missing one: the leg is titled
// after an example and reports on something else, or skips and reports success
// over nothing at all.
func TestTheNightlyCorpusIsEveryExample(t *testing.T) {
	root := filepath.Join("..", "..")

	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Manifest []string `yaml:"manifest"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	body, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "dogfood.yml"))
	if err != nil {
		t.Fatalf("could not read the workflow: %v", err)
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("could not parse the workflow: %v", err)
	}
	named := workflow.Jobs["nightly"].Strategy.Matrix.Manifest
	if len(named) == 0 {
		t.Fatal("the nightly job names no manifests, so the corpus runs on nothing")
	}

	inMatrix := map[string]bool{}
	for _, m := range named {
		inMatrix[m] = true
		if _, err := os.Stat(filepath.Join(root, m, "antifailure.yaml")); err != nil {
			t.Errorf("the nightly matrix names %s and %s/antifailure.yaml does not exist, "+
				"so that leg is named after an example it cannot run", m, m)
		}
	}

	// The repository's own manifest, which is not an example and is the one
	// thing this job was actually covering before it read the matrix at all.
	// Every leg ran the root, so fixing the interpolation without keeping the
	// root would have removed real coverage while turning the job green. That
	// trade is worth refusing out loud rather than in a comment somebody
	// deletes, so it is asserted here.
	if !inMatrix["."] {
		t.Error("the nightly matrix does not name '.', so nothing runs the repository's " +
			"own manifest. That was this job's only real coverage for the whole of its " +
			"history and it must not be dropped in the course of fixing the matrix.")
	}

	found, err := filepath.Glob(filepath.Join(root, "examples", "*", "antifailure.yaml"))
	if err != nil {
		t.Fatalf("could not look for examples: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("no example carries a manifest, which cannot be right")
	}
	for _, m := range found {
		dir := filepath.ToSlash(filepath.Join("examples", filepath.Base(filepath.Dir(m))))
		if !inMatrix[dir] {
			t.Errorf("%s has a manifest and the nightly matrix leaves it out, "+
				"so nothing has ever run it", dir)
		}
	}
}

// The nightly makes a golden before it asks for one.
//
// It never did, and the first scheduled run in the repository's history reached
// `af ci` and got AF-DB-012 on all three legs. The control plane job refreshes
// one in a step of its own; this job has no such step, so the refresh has to
// come through the harness. Either shape satisfies this, because what matters
// is that a golden exists and not which line makes it.
func TestTheNightlyMakesAGoldenBeforeItRunsThePipeline(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "dogfood.yml"))
	if err != nil {
		t.Fatalf("could not read the workflow: %v", err)
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("could not parse the workflow: %v", err)
	}

	for _, s := range workflow.Jobs["nightly"].Steps {
		if strings.Contains(s.Run, "--refresh-golden") ||
			strings.Contains(s.Run, "golden refresh") {
			return
		}
	}
	t.Error("no step in the nightly job refreshes a golden, so every leg reaches " +
		"af ci with none and fails with AF-DB-012")
}

// Every leg can reach the source database its own manifest names.
//
// This is the question TestTheNightlyMakesAGoldenBeforeItRunsThePipeline above
// is one step away from asking, and the gap between the two is a red nightly
// that ran for its whole history. That test asks whether some step refreshes a
// golden. A step that refreshes a golden and a step that can refresh a golden
// are different claims, and the nightly satisfied the first while failing the
// second on its most important leg: the job carried the `--refresh-golden`
// flag, so the test passed, and carried neither the Postgres service nor the
// AF_STAGING_DATABASE_URL that the repository's own manifest names, so the
// refresh refused with AF-DB-016 before it copied a byte. `af ci` then had no
// golden and reported that it had checked nothing, honestly, which is the only
// reason anybody noticed.
//
// So this reads the manifests instead of the workflow's own vocabulary, and
// asserts three things per leg:
//
//   - a manifest that names database.source_url_env has that variable set by
//     the job that runs it, because job level env does not cross jobs and that
//     is precisely how the nightly lost it;
//   - the value names a loopback address, because the alternative is a public
//     runner holding a route to a database that somebody's rows are in, and
//     that is the single worst thing this repository could do;
//   - a service container in the same job publishes the port the value names,
//     because a variable pointing at nothing fails at connect time instead of
//     with AF-DB-016 and is just as red.
//
// A manifest that names no source asserts nothing at all. Three of the four
// legs are in that case today and need no Postgres, so widening this to "every
// leg has a database" would be a check that enforces a habit rather than a
// requirement.
func TestEveryLegCanReachTheSourceItsManifestNames(t *testing.T) {
	root := filepath.Join("..", "..")

	type service struct {
		Ports []string `yaml:"ports"`
	}
	var workflow struct {
		Jobs map[string]struct {
			Env      map[string]string  `yaml:"env"`
			Services map[string]service `yaml:"services"`
			Strategy struct {
				Matrix struct {
					Manifest []string `yaml:"manifest"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	body, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "dogfood.yml"))
	if err != nil {
		t.Fatalf("could not read the workflow: %v", err)
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("could not parse the workflow: %v", err)
	}

	// Which manifests a job runs, taken from the -C it passes rather than from
	// a list here. A job that grew a leg without this test noticing would be
	// the same defect one level up.
	dashC := regexp.MustCompile(`-C\s+"?([^\s"\\]+)"?`)

	checked := 0
	for name, job := range workflow.Jobs {
		var legs []string
		for _, step := range job.Steps {
			for _, m := range dashC.FindAllStringSubmatch(step.Run, -1) {
				arg := m[1]
				// The matrix value, however this job spells it.
				if strings.Contains(arg, "matrix.manifest") || arg == "$MANIFEST" {
					legs = append(legs, job.Strategy.Matrix.Manifest...)
					continue
				}
				legs = append(legs, arg)
			}
		}

		seen := map[string]bool{}
		for _, leg := range legs {
			if seen[leg] {
				continue
			}
			seen[leg] = true

			var m struct {
				Database struct {
					SourceURLEnv string `yaml:"source_url_env"`
				} `yaml:"database"`
			}
			raw, err := os.ReadFile(filepath.Join(root, leg, "antifailure.yaml"))
			if err != nil {
				// Covered by TestTheNightlyCorpusIsEveryExample, which names
				// the defect better than this test would.
				continue
			}
			if err := yaml.Unmarshal(raw, &m); err != nil {
				t.Errorf("%s: could not parse %s/antifailure.yaml: %v", name, leg, err)
				continue
			}
			if m.Database.SourceURLEnv == "" {
				// Builds its golden from nothing, which the engine does
				// deliberately and reports. Nothing to supply.
				continue
			}
			checked++

			value, ok := job.Env[m.Database.SourceURLEnv]
			if !ok || strings.TrimSpace(value) == "" {
				t.Errorf("job %q runs %s, whose manifest names %s as the database to copy, "+
					"and the job sets no value for it. The refresh will refuse with AF-DB-016 "+
					"and af ci will then have no golden to branch, so this leg checks nothing. "+
					"A job level env: block in another job does not reach this one.",
					name, leg, m.Database.SourceURLEnv)
				continue
			}

			host, port := hostPort(value)
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				t.Errorf("job %q points %s at host %q. Every leg of this workflow runs on a "+
					"public runner, so the only database it may reach is one this job stood "+
					"up itself. Copying a real customer's database here is the single worst "+
					"thing this repository could do.", name, m.Database.SourceURLEnv, host)
				continue
			}
			if port == "" {
				t.Errorf("job %q sets %s to %q, which names no port, so nothing here can say "+
					"what it reaches", name, m.Database.SourceURLEnv, value)
				continue
			}

			published := false
			for _, svc := range job.Services {
				for _, p := range svc.Ports {
					if strings.HasPrefix(p, port+":") || p == port {
						published = true
					}
				}
			}
			if !published {
				t.Errorf("job %q points %s at port %s and no service container in that job "+
					"publishes it, so the refresh fails on connect rather than on AF-DB-016 "+
					"and the leg is red either way", name, m.Database.SourceURLEnv, port)
			}
		}
	}

	// The assertion that keeps this test from passing by looking at nothing.
	//
	// Every branch above is a continue on a leg that needs no source, so a
	// tree in which no manifest names one, or in which the -C parsing stopped
	// matching the workflow's shape, would run zero comparisons and report
	// success. That is the failure this whole file exists to argue against.
	if checked == 0 {
		t.Error("no leg of any job was found to name a source database, so this test " +
			"compared nothing. Either every manifest dropped database.source_url_env, " +
			"or the -C arguments in dogfood.yml stopped being readable here.")
	}
}

// hostPort pulls the host and port out of a Postgres connection string without
// requiring it to parse as a URL, because a workflow expression in the middle
// of one is still a value this test has to be able to talk about.
func hostPort(value string) (string, string) {
	rest := value
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

// A job that runs the check can report the check.
//
// THE DEFECT THIS IS THE INSTRUMENT FOR, and it is the most visible one this
// repository has ever carried, because every contributor who opened a pull
// request saw it. The hosted App posts a check named `Antifailure` on every
// commit here, and on every commit it said:
//
//	Nothing was verified
//	The workflow run finished successfully and Antifailure never reported a
//	result, so nothing was verified.
//
// It was telling the truth. This job ran the whole product against the control
// plane's own schema for twenty minutes and then reported the result to a pull
// request comment and to nowhere else. There was no `id-token: write`, so the
// runner set no ACTIONS_ID_TOKEN_REQUEST_URL and the job could not prove what
// it was; there was no control plane address, so the engine's event sink did
// not try; and there was no step that posted a report, so nothing arrived. The
// job exited zero, the comment appeared, and the pipeline looked fine, which is
// the exact shape of failure this repository names most often: a gate that
// passes while checking nothing. Here the product itself was the thing saying
// so on every pull request, and it went unread for the life of the check.
//
// So this asserts the four things that together make a run reportable, because
// any one of them missing is silence and three of the four were missing:
//
//   - `id-token: write` on the job, which is what lets it prove what it is;
//   - an address for the control plane, without which every route is off;
//   - `--report-json`, because the check needs counts and the Markdown is
//     prose;
//   - a step that posts to /v1/pr/report, because a report written to a file
//     is a report nobody receives.
//
// It reads the workflow rather than trusting the comments in it, and it is
// keyed on the job that runs the harness rather than on a job name, so a job
// that starts running `af ci` tomorrow is covered without anybody remembering
// this file.
func TestAJobThatRunsTheCheckCanReportIt(t *testing.T) {
	type step struct {
		Name string            `yaml:"name"`
		If   string            `yaml:"if"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
	}
	var workflow struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Env         map[string]string `yaml:"env"`
			Steps       []step            `yaml:"steps"`
		} `yaml:"jobs"`
	}
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "dogfood.yml"))
	if err != nil {
		t.Fatalf("could not read the workflow: %v", err)
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("could not parse the workflow: %v", err)
	}

	// Only the pull request job. The nightly runs on a schedule, where there is
	// no pull request and so no check to report to, and demanding a credential
	// of it would be a check enforcing a habit rather than a requirement.
	checked := 0
	for name, job := range workflow.Jobs {
		runsTheCheck := false
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "tools/dogfood") && strings.Contains(s.Run, "--mode pr") {
				runsTheCheck = true
			}
		}
		if !runsTheCheck {
			continue
		}
		checked++

		if job.Permissions["id-token"] != "write" {
			t.Errorf("job %q runs the check and has no `id-token: write`, so the runner sets no "+
				"identity variables, the job cannot prove what it is, and the check on every pull "+
				"request says nothing was verified", name)
		}
		if job.Env["AF_CONTROL_PLANE"] == "" {
			t.Errorf("job %q runs the check and names no AF_CONTROL_PLANE, so there is nowhere "+
				"to report to and every route to the control plane is skipped", name)
		}

		wantsJSON, claims, publishes := false, false, false
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "--report-json") {
				wantsJSON = true
			}
			if strings.Contains(s.Run, "/v1/pr/callback-token") {
				claims = true
			}
			if strings.Contains(s.Run, "/v1/pr/report") {
				publishes = true
			}
		}
		if !wantsJSON {
			t.Errorf("job %q never asks for --report-json, so there are no counts to send and "+
				"the only report is Markdown, which reading would be a parser for prose", name)
		}
		if !claims {
			t.Errorf("job %q never asks for a callback credential, so the control plane is never "+
				"told which of this repository's workflow runs is the one checking the commit", name)
		}
		if !publishes {
			t.Errorf("job %q runs the whole product and posts to /v1/pr/report nowhere, so the "+
				"result reaches a comment and the check hears nothing", name)
		}

		// AND THE FORK CASE, WHICH IS THE ONE THAT MUST NOT GO RED.
		//
		// GitHub withholds the workflow identity from a pull request opened
		// from a fork, on purpose, and sets neither runner variable. A step
		// under `set -u` that reads one of them bare aborts with "unbound
		// variable" and exit 1, so the single case that has to degrade
		// gracefully, an outside contributor who can fix nothing, would be the
		// one case that fails. The step reads them through `:-` and says so
		// instead. Proved rather than assumed: bash exits 1 on the bare read
		// and 0 on the guarded one.
		for _, st := range job.Steps {
			if !strings.Contains(st.Run, "/v1/pr/callback-token") {
				continue
			}
			if !strings.Contains(st.Run, "set -u") && !strings.Contains(st.Run, "set -euo") {
				continue
			}
			for _, v := range []string{
				"ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL",
			} {
				if strings.Contains(st.Run, "${"+v+"}") {
					t.Errorf("job %q reads ${%s} bare under set -u, so a pull request from a "+
						"fork, where GitHub sets neither, ends the step with an unbound "+
						"variable and a red check nobody outside this repository can fix. "+
						"Read it as ${%s:-} and say what happened", name, v, v)
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no job in dogfood.yml runs the harness in pull request mode, so this test " +
			"checked nothing. Either the job was renamed out from under it or the check is gone")
	}
}

// The claim comes before the work, not beside the report.
//
// A run that introduces itself only at the end is a run the control plane
// cannot name while it matters. For the whole twenty minutes the check would
// read "Waiting for a runner" when a runner is plainly working, and a job that
// dies at minute ten leaves an environment the control plane has no run id to
// cancel, because the only route it has into the runtime holding that
// environment is cancelling the workflow run.
//
// It is also what makes the control plane's side safe to tighten. A workflow
// run that has not claimed cannot end a generation any more, so a late claim
// would trade one silence for another.
func TestTheClaimComesBeforeTheWork(t *testing.T) {
	type step struct {
		Run string `yaml:"run"`
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "dogfood.yml"))
	if err != nil {
		t.Fatalf("could not read the workflow: %v", err)
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("could not parse the workflow: %v", err)
	}

	for name, job := range workflow.Jobs {
		claimed, ran := -1, -1
		for i, s := range job.Steps {
			if claimed < 0 && strings.Contains(s.Run, "/v1/pr/callback-token") {
				claimed = i
			}
			if ran < 0 && strings.Contains(s.Run, "tools/dogfood") &&
				strings.Contains(s.Run, "--mode pr") {
				ran = i
			}
		}
		if claimed < 0 || ran < 0 {
			continue
		}
		if claimed > ran {
			t.Errorf("job %q claims its callback credential at step %d and runs the check at "+
				"step %d, so the check reads as waiting for a runner while one is working and a "+
				"run that dies leaves an environment nothing can name", name, claimed, ran)
		}
	}
}

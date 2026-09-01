package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// af start is a status command, so the thing worth testing is not that it
// prints something. It is that each rung reports the state it actually
// observed, that it never collapses one state into another, and that the one
// command it prints is the one that moves the reader forward.
//
// The verdict shape it has to preserve is the one this repository has already
// been bitten by twice: a step reported as fine because nothing looked at it.
// So there is a test asserting the invariant over every rung rather than one
// per rung, because a rung added later inherits the guarantee.

const startManifest = `version: 1
name: fixture
services:
  - name: web
    kind: web
    path: .
    port: 3000
    health_path: /health
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
personas:
  - name: owner
    email: owner@example.test
    role: owner
    login: password
workflows:
  - name: sign in
    description: Sign in as the owner and check that the dashboard lists the orders.
    persona: owner
    start_path: /
    expect:
      - the page lists the orders
`

// fakeProber answers the machine questions from values a test chose, so these
// run the same on a laptop with Docker as on a CI runner without one. The
// alternative was reading the real machine, which made two of these tests
// report on this developer's home directory and pass or fail with it.
type fakeProber struct {
	lookPath      map[string]string
	dockerVersion string
	dockerOS      string
	dockerErr     error
	stat          func(string) (os.FileInfo, error)
}

func (f fakeProber) LookPath(name string) (string, error) {
	if p, ok := f.lookPath[name]; ok {
		return p, nil
	}
	return "", errors.New(name + " not found")
}

func (f fakeProber) DockerInfo(context.Context) (string, string, error) {
	if f.dockerErr != nil {
		return "", "", f.dockerErr
	}
	return f.dockerVersion, f.dockerOS, nil
}

func (fakeProber) DialTimeout(string, string, time.Duration) error { return nil }
func (fakeProber) LookupHost(string) ([]string, error)             { return []string{"127.0.0.1"}, nil }
func (fakeProber) FreeDiskBytes(string) (uint64, error)            { return 200 << 30, nil }
func (fakeProber) ListenTCP(int) error                             { return nil }
func (fakeProber) Getenv(string) string                            { return "" }

func (f fakeProber) Stat(path string) (os.FileInfo, error) {
	if f.stat != nil {
		return f.stat(path)
	}
	return os.Stat(path)
}

// startProbeFor is a machine with Docker running, no environments held, and no
// installed af at all, which is the truthful state when the running binary is
// a test. installState reports that as not checked, so the rungs these tests
// are about are the ones that decide the next step. Its own three branches get
// their own test below with probes that say otherwise.
func startProbeFor(t *testing.T, home string) startProbe {
	t.Helper()
	return startProbe{
		Prober: fakeProber{
			lookPath:      map[string]string{"docker": "/usr/bin/docker", "git": "/usr/bin/git"},
			dockerVersion: "28.5.1", dockerOS: "linux",
		},
		environments: func(context.Context, *Env) ([]environment, error) { return nil, nil },
		home:         func() (string, error) { return home, nil },
	}
}

// startEnv is a working directory with nothing else in it, plus a fixed clock,
// so the leftover check reasons about a time this test chose.
func startEnv(t *testing.T, dir string) (*Env, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &Env{
		Out:     NewOutput(&out, &out),
		WorkDir: dir,
		Clock:   clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)),
		Getenv:  func(string) string { return "" },
	}, &out
}

// writeManifest writes the fixture plus the Dockerfile it names, because the
// manifest validator resolves build paths and a manifest naming a file that is
// not there is a different test from the one being written.
func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	write(t, dir, "Dockerfile", "FROM scratch\n")
	write(t, dir, "antifailure.yaml", body)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// collapse turns any run of whitespace into one space, so an assertion about
// what was said is not an assertion about where the terminal broke the line.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func stageNamed(t *testing.T, stages []stage, name string) stage {
	t.Helper()
	for _, s := range stages {
		if s.name == name {
			return s
		}
	}
	var names []string
	for _, s := range stages {
		names = append(names, s.name)
	}
	t.Fatalf("no rung named %q; the rungs are %v", name, names)
	return stage{}
}

// The invariant that keeps this command honest. Any rung reporting that it did
// not look must say why, or it is a silent skip reading as a considered
// decision, which is the exact shape this repository calls worse than no check.
func TestEveryUncheckedRungSaysWhyItWasNotChecked(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest)
	e, _ := startEnv(t, dir)

	for _, s := range firstRun(t.Context(), e, startProbeFor(t, t.TempDir())) {
		if s.state != StageUnchecked {
			continue
		}
		if s.why == "" {
			t.Errorf("rung %q reports that it was not checked and does not say why", s.name)
		}
		if s.detail == "" {
			t.Errorf("rung %q reports that it was not checked with no detail at all", s.name)
		}
	}
}

// Every rung that is not finished has to name something to run, or the reader
// is told where they are and not what to do, which is the diagnostic this
// repository's doctor command exists to avoid being.
func TestEveryUnfinishedRungNamesSomethingToRun(t *testing.T) {
	dir := t.TempDir()
	e, _ := startEnv(t, dir)

	for _, s := range firstRun(t.Context(), e, startProbeFor(t, t.TempDir())) {
		switch s.state {
		case StagePending, StageBlocked:
			if s.command == "" {
				t.Errorf("rung %q is %s and names no command", s.name, s.state)
			}
		case StageUnchecked:
			// A rung waiting on an earlier one is answered by the earlier
			// one's command, so it is allowed to name none of its own.
			if !s.downstream && s.why == "" {
				t.Errorf("rung %q was declined with no reason", s.name)
			}
		}
	}
}

func TestWithNoManifestTheNextStepIsAfInit(t *testing.T) {
	dir := t.TempDir()
	e, _ := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))

	m := stageNamed(t, stages, "a manifest")
	if m.state != StagePending {
		t.Errorf("the manifest rung is %q with no manifest, want pending", m.state)
	}
	if m.command != "af init" {
		t.Errorf("the manifest rung offers %q, want af init", m.command)
	}
	// Pending rather than blocked: not having written a manifest yet is where a
	// first run starts, not something broken.
	if _, blocked := nextStep(stages); blocked {
		t.Error("an empty directory is reported as blocked, which makes a first run look like a failure")
	}
}

// Every rung below the manifest is unchecked and says so as one thing waiting
// on another, rather than five paragraphs each explaining the same absence
// underneath a next step that is "write a manifest".
func TestTheRungsBelowTheManifestWaitOnItRatherThanGuessing(t *testing.T) {
	dir := t.TempDir()
	e, _ := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))

	for _, name := range []string{"the database source", "a golden", "an environment",
		"workflows to run", "evidence on disk"} {
		s := stageNamed(t, stages, name)
		if s.state != StageUnchecked {
			t.Errorf("rung %q is %q with no manifest, want unchecked", name, s.state)
		}
		if !s.downstream {
			t.Errorf("rung %q is not marked as waiting on the manifest, so it is printed as a decision", name)
		}
	}
}

func TestAManifestThatDoesNotParseBlocksAndNamesTheFirstProblem(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "antifailure.yaml", "version: 1\nname: fixture\nservices:\n  - name: web\n    port: not-a-number\n")
	e, _ := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))

	m := stageNamed(t, stages, "a manifest")
	if m.state != StageBlocked {
		t.Fatalf("an unparseable manifest is %q, want blocked:\n%s", m.state, m.detail)
	}
	if !strings.Contains(m.detail, "problem") {
		t.Errorf("the detail %q does not say how many problems there are", m.detail)
	}
	next, blocked := nextStep(stages)
	if !blocked {
		t.Error("a manifest that does not parse is not reported as blocking")
	}
	if next == nil || next.name != "a manifest" {
		t.Errorf("the next step is %v, want the manifest rung", next)
	}
}

// A manifest with no workflows is the exact shape of a green run over nothing:
// af up succeeds, af test refuses, and the refusal arrives after the several
// minutes af up took. Saying it before af up costs nothing.
func TestAManifestWithNoWorkflowsIsBlockedBeforeAnythingIsBuilt(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, strings.Split(startManifest, "workflows:")[0])
	e, _ := startEnv(t, dir)

	w := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "workflows to run")
	if w.state != StageBlocked {
		t.Errorf("a manifest with no workflows is %q, want blocked", w.state)
	}
	if !strings.Contains(w.detail, "nothing to run") {
		t.Errorf("the detail %q does not say that af test would examine nothing", w.detail)
	}
}

func TestAManifestWithWorkflowsCountsThem(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest)
	e, _ := startEnv(t, dir)

	w := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "workflows to run")
	if w.state != StageDone {
		t.Fatalf("a manifest with one workflow is %q, want done: %s", w.state, w.detail)
	}
	if !strings.Contains(w.detail, "1 workflow") || !strings.Contains(w.detail, "1 persona") {
		t.Errorf("the detail %q does not count the workflows and personas", w.detail)
	}
}

// The golden rung is the one this command refuses to answer, and the refusal is
// the feature. Listing goldens goes through Orchestrator.open, which creates
// .antifailure, takes this branch's lock and migrates the state database, so a
// status command that asked would be unusable while af up was running. It must
// never claim a golden exists, and it must always say what to run instead.
func TestTheGoldenRungNeverClaimsAGoldenExists(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
  masking_rules: masking.yaml
`)
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	e, _ := startEnv(t, dir)

	g := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "a golden")
	if g.state != StageUnchecked {
		t.Errorf("the golden rung is %q, want unchecked; nothing here can know whether one exists", g.state)
	}
	if g.command != "af golden list" {
		t.Errorf("the golden rung offers %q, want af golden list", g.command)
	}
	if !strings.Contains(g.why, "lock") {
		t.Errorf("the reason %q does not say why this command will not ask", g.why)
	}
	if g.downstream {
		t.Error("the golden rung is marked as waiting on something, but it was declined on purpose")
	}
}

// A manifest naming masking rules that are not on disk cannot make a golden,
// and it is worth knowing before af up rather than during af golden refresh.
//
// This is the rung that was written as a check on the field being empty, which
// could never have fired: the manifest normaliser fills masking_rules in with
// masking.yaml whenever nothing said otherwise, so every loaded manifest names
// one. The question that can be answered is whether the file is there.
func TestMaskingRulesThatAreNotOnDiskBlock(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
`)
	e, _ := startEnv(t, dir)

	g := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "a golden")
	if g.state != StageBlocked {
		t.Errorf("a manifest naming absent masking rules is %q, want blocked: %s", g.state, g.detail)
	}
	if !strings.Contains(g.detail, "masking.yaml") {
		t.Errorf("the detail %q does not name the file that is missing", g.detail)
	}

	// And with the file there it goes back to being the rung this command
	// declines to answer, rather than staying red.
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	g = stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "a golden")
	if g.state != StageUnchecked {
		t.Errorf("with the rules present the golden rung is %q, want unchecked: %s", g.state, g.detail)
	}
}

// installState's three branches, each with a machine that says so.
func TestTheInstallRungReadsTheShellRatherThanTheRunningBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform does not report the running binary's path: %v", err)
	}
	dir := t.TempDir()
	e, _ := startEnv(t, dir)

	t.Run("the shell finds this one", func(t *testing.T) {
		p := startProbeFor(t, t.TempDir())
		p.Prober = fakeProber{lookPath: map[string]string{"af": self}}
		s := installState(e, p)
		if s.state != StageDone {
			t.Errorf("af on PATH and pointing here is %q, want done: %s", s.state, s.detail)
		}
	})

	t.Run("the shell finds a different one", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "af")
		if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		p := startProbeFor(t, t.TempDir())
		p.Prober = fakeProber{lookPath: map[string]string{"af": other}}
		s := installState(e, p)
		if s.state != StageBlocked {
			t.Errorf("a second af earlier on PATH is %q, want blocked: %s", s.state, s.detail)
		}
	})

	t.Run("nothing on PATH but something installed", func(t *testing.T) {
		home := t.TempDir()
		bin := filepath.Join(home, ".antifailure", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "af"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		s := installState(e, startProbeFor(t, home))
		if s.state != StagePending {
			t.Fatalf("an installed af the shell cannot find is %q, want pending: %s", s.state, s.detail)
		}
		if !strings.Contains(s.command, bin) {
			t.Errorf("the command %q does not name the directory to add", s.command)
		}
	})

	t.Run("nothing on PATH and nothing installed", func(t *testing.T) {
		// A development build. Telling somebody to put their build directory
		// on their PATH is correct about the fact and useless as advice, so
		// this rung declines rather than inventing a step.
		s := installState(e, startProbeFor(t, t.TempDir()))
		if s.state != StageUnchecked {
			t.Errorf("a build with no install is %q, want unchecked: %s", s.state, s.detail)
		}
		if s.command != "" {
			t.Errorf("a build with no install was told to run %q", s.command)
		}
	})
}

// The model key boundary. No key is a supported mode, so the rung is done and
// optional, it is never the next step, and nothing here reads or asks for a
// key.
func TestNoModelKeyIsDoneAndOptionalAndNeverTheNextStep(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest)
	e, _ := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))

	k := stageNamed(t, stages, "a model key")
	if k.state != StageDone {
		t.Errorf("no model key is reported as %q, want done; running without one is supported", k.state)
	}
	if !k.optional {
		t.Error("the model key rung is not marked optional, so it reads as an unfinished step forever")
	}
	if !strings.Contains(k.detail, "deterministic planner") {
		t.Errorf("the detail %q does not say what happens without a key", k.detail)
	}
	if next, _ := nextStep(stages); next != nil && next.name == "a model key" {
		t.Error("af start told somebody to set a model key before they had run anything")
	}
}

// nextStep has to prefer the thing that is broken over the thing that is
// merely next, because the broken one is what would make the next one fail.
func TestABlockedRungWinsOverAPendingOne(t *testing.T) {
	stages := []stage{
		{name: "first", state: StagePending, command: "a"},
		{name: "second", state: StageBlocked, command: "b"},
		{name: "third", state: StagePending, command: "c"},
	}
	next, blocked := nextStep(stages)
	if !blocked {
		t.Fatal("a blocked rung was not reported as blocking")
	}
	if next == nil || next.name != "second" {
		t.Errorf("the next step is %v, want the blocked rung", next)
	}
}

func TestAnOptionalRungIsNeverTheNextStep(t *testing.T) {
	stages := []stage{
		{name: "optional", state: StagePending, command: "a", optional: true},
		{name: "required", state: StagePending, command: "b"},
	}
	next, _ := nextStep(stages)
	if next == nil || next.name != "required" {
		t.Errorf("the next step is %v, want the required rung", next)
	}
}

// Exit 0 with rungs left is the normal state of a first run in progress, and
// exit 3 is reserved for something broken. Collapsing those two would make
// "not there yet" indistinguishable from "wrong", which is exactly what this
// command exists to separate.
func TestTheExitCodeSeparatesUnfinishedFromBroken(t *testing.T) {
	dir := t.TempDir()
	e, _ := startEnv(t, dir)
	if err := renderStart(e, firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))); err != nil {
		t.Errorf("an unfinished first run exited non zero: %v", err)
	}

	write(t, dir, "antifailure.yaml", "version: 1\nname: fixture\nservices:\n  - name: web\n    port: not-a-number\n")
	e2, _ := startEnv(t, dir)
	err := renderStart(e2, firstRun(t.Context(), e2, startProbeFor(t, t.TempDir())))
	if err == nil {
		t.Fatal("a manifest that does not parse exited 0")
	}
	// Read the way Execute reads it. ExitCodeOf only consults the catalog, and
	// a silent failure carries its code on itself so the report is not printed
	// twice.
	var quiet *silentError
	if !errors.As(err, &quiet) {
		t.Fatalf("a broken rung returned %T, which Execute would render as a second message", err)
	}
	if got := quiet.ExitCode(); got != aferrors.ExitConfiguration {
		t.Errorf("a broken rung exits %d, want %d", got, aferrors.ExitConfiguration)
	}
}

func TestTheJSONFormCarriesEveryRungAndTheNextCommand(t *testing.T) {
	dir := t.TempDir()
	e, out := startEnv(t, dir)
	e.Out.Format = FormatJSON
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))
	if err := renderStart(e, stages); err != nil {
		t.Fatal(err)
	}

	var doc StartJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("the JSON form does not parse: %v\n%s", err, out.String())
	}
	if len(doc.Stages) != len(stages) {
		t.Errorf("the JSON form has %d rungs and the report has %d", len(doc.Stages), len(stages))
	}
	// The next step is asserted as the first unfinished rung's own command
	// rather than as a literal, because which rung that is depends on the
	// machine the test runs on and the invariant does not.
	want := ""
	for _, st := range stages {
		if st.optional {
			continue
		}
		if st.state == StagePending || st.state == StageBlocked {
			want = st.command
			break
		}
	}
	if want == "" {
		t.Fatal("an empty directory produced no unfinished rung at all")
	}
	if doc.Next != want {
		t.Errorf("the JSON next step is %q, want %q", doc.Next, want)
	}
	if doc.Complete {
		t.Error("an empty directory is reported as a completed first run")
	}
	for _, s := range doc.Stages {
		if s.State == "" {
			t.Errorf("rung %q has no state", s.Name)
		}
	}
}

// The text form has to print the reason for every declined rung somewhere the
// reader will see it, rather than leaving a symbol in a list.
func TestTheTextFormPrintsTheReasonForEveryDeclinedRung(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
  masking_rules: masking.yaml
`)
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	e, out := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))
	_ = renderStart(e, stages)

	body := out.String()
	if !strings.Contains(body, "Not checked here") {
		t.Fatalf("the report has a declined rung and no section explaining it:\n%s", body)
	}
	for _, s := range stages {
		if s.state != StageUnchecked || s.downstream {
			continue
		}
		// Collapsed, because the reason is wrapped to the terminal and a
		// literal match would be testing the wrap rather than the reason.
		if !strings.Contains(collapse(body), collapse(s.why)) {
			t.Errorf("the reason for %q is not printed:\n%s", s.name, body)
		}
	}
}

// A key that is set is reported without the key. This is a boundary rather than
// a nicety: the one thing af start must never do is put a key anywhere a
// reader, a screenshot or a script can pick it up.
func TestAConfiguredKeyIsReportedWithoutTheKey(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest)
	const key = "sk-ant-thisisnotarealkeyatall0000000000"
	e, out := startEnv(t, dir)
	e.Getenv = func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return key
		}
		return ""
	}
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))
	_ = renderStart(e, stages)

	k := stageNamed(t, stages, "a model key")
	if k.state != StageDone {
		t.Errorf("a configured key is reported as %q, want done", k.state)
	}
	if strings.Contains(k.detail, key) || strings.Contains(out.String(), key) {
		t.Fatal("af start printed the model key")
	}
	if !strings.Contains(k.detail, "anthropic") {
		t.Errorf("the detail %q does not say which provider is configured", k.detail)
	}
}

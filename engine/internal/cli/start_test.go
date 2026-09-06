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
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
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
		// A daemon that answers and holds no goldens, so the rung is answered
		// rather than declined. The tests about the pool hand this their own
		// images.
		goldens: func(context.Context, *Env, *schema.Manifest) ([]provider.GoldenVersion, error) {
			return nil, nil
		},
	}
}

// goldensOf makes the probe list exactly these images.
func goldensOf(p startProbe, goldens ...provider.GoldenVersion) startProbe {
	p.goldens = func(context.Context, *Env, *schema.Manifest) ([]provider.GoldenVersion, error) {
		return goldens, nil
	}
	return p
}

// identityOf is what a golden has to record for this directory's manifest to
// branch it, computed by the code af up uses, so a test that wants a golden to
// count as this project's does not paraphrase the rule it is testing.
func identityOf(t *testing.T, dir string) string {
	t.Helper()
	e, _ := startEnv(t, dir)
	m, root, st := manifestState(e)
	if m == nil {
		t.Fatalf("no manifest to compute an identity from: %s", st.detail)
	}
	o, err := env.New(env.Options{Root: root, Manifest: m, Clock: e.Clock, Getenv: e.Getenv})
	if err != nil {
		t.Fatal(err)
	}
	id, err := o.GoldenIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// dockerWithSource is the manifest that produced the failure: a Docker
// database copied from a variable that this shell does not hold, with masking
// rules on disk, on a machine that already has a verified golden for it.
const dockerWithSource = `
database:
  provider: docker
  version: 17
  source_url_env: AF_START_TEST_SOURCE_URL
  masking_rules: masking.yaml
`

var madeYesterday = time.Date(2026, 9, 5, 23, 37, 4, 0, time.UTC)

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

	for _, name := range []string{"the database source", "masking rules", "a golden", "an environment",
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

// The golden rung answers for Docker, and it answers with af up's own rule.
//
// It used to decline, because Orchestrator.Goldens goes through open and takes
// this branch's lock. The Docker provider's listing is an ImageList and never
// needed the lock, so a machine holding five verified goldens for the project
// was told "whether a golden exists was not checked" and sent to set a secret.
// Answering brings the other risk back: a rung that says "a golden" over an
// image af up would refuse. So the two refusals af up makes are asserted here,
// one image each: made for another project, and never verified.
func TestTheGoldenRungNeverClaimsAnotherProjectsOrAnUnverifiedGolden(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+dockerWithSource)
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	mine := identityOf(t, dir)
	e, _ := startEnv(t, dir)
	probe := goldensOf(startProbeFor(t, t.TempDir()),
		provider.GoldenVersion{ID: "gv_20260906000000_other000", Verified: true,
			Provenance: mine + "-not", CreatedAt: madeYesterday},
		provider.GoldenVersion{ID: "gv_20260906000001_unverif0", Verified: false,
			Provenance: mine, CreatedAt: madeYesterday},
	)
	stages := firstRun(t.Context(), e, probe)

	g := stageNamed(t, stages, "a golden")
	if g.state == StageDone {
		t.Fatalf("the golden rung is done over another project's golden and an unverified one: %s", g.detail)
	}
	if g.state != StagePending {
		t.Errorf("the golden rung is %q, want pending: %s", g.state, g.detail)
	}
	if g.command != "af golden refresh" {
		t.Errorf("the golden rung offers %q, want af golden refresh", g.command)
	}
	if !strings.Contains(g.detail, "1 belongs to other projects") {
		t.Errorf("the detail %q does not count the golden made for something else", g.detail)
	}
	if !strings.Contains(g.detail, "1 is unverified") {
		t.Errorf("the detail %q does not count the unverified golden", g.detail)
	}
	// And with no usable golden, an unset source is still a blocker, because
	// af up would refuse with nothing to branch.
	src := stageNamed(t, stages, "the database source")
	if src.state != StageBlocked {
		t.Errorf("with no usable golden the unset source is %q, want blocked: %s", src.state, src.detail)
	}
	if next, _ := nextStep(stages); next == nil || next.command != "af secret set AF_START_TEST_SOURCE_URL" {
		t.Errorf("the next step is %v, want the secret", next)
	}
}

// The failure itself. A verified golden made for this project exists, the
// variable naming production is unset, and af up would run: it branches the
// golden that is there and skips the scheduled refresh while the source holds
// nothing. So the golden rung is done and names the golden, the source rung is
// a warning about the next refresh rather than a blocker, and Next is af up.
// installedRunnerAt gives a test the runner rung as a machine that ran
// `af runner install` has it: a home of its own with a complete runner under
// .antifailure, and no browsers of the developer's leaking in. Without it the
// rung reads whatever the machine running the tests holds, which on a laptop
// was an installed runner and on the CI runner was nothing, so the tests that
// assert the next step passed on one and failed on the other.
func installedRunnerAt(t *testing.T) {
	t.Helper()
	f := newRunnerFixture(t)
	f.homeRunner(t)
}

func TestAVerifiedGoldenForThisProjectMakesAnUnsetSourceAWarningAndNextIsAfUp(t *testing.T) {
	installedRunnerAt(t)
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+dockerWithSource+`  golden:
    schedule: "0 3 * * *"
`)
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	mine := identityOf(t, dir)
	e, out := startEnv(t, dir)
	probe := goldensOf(startProbeFor(t, t.TempDir()),
		provider.GoldenVersion{ID: "gv_20260906043704_d1c46d45", Verified: true,
			Provenance: mine, CreatedAt: madeYesterday})
	stages := firstRun(t.Context(), e, probe)

	g := stageNamed(t, stages, "a golden")
	if g.state != StageDone {
		t.Fatalf("a verified golden for this project leaves the rung %q, want done: %s", g.state, g.detail)
	}
	if !strings.Contains(g.detail, "gv_20260906043704_d1c46d45") {
		t.Errorf("the detail %q does not name the golden", g.detail)
	}
	if !strings.Contains(g.detail, "made for this project") {
		t.Errorf("the detail %q does not say whose golden it is", g.detail)
	}
	if !strings.Contains(g.detail, madeYesterday.Local().Format("2006-01-02 15:04")) {
		t.Errorf("the detail %q does not say when it was made", g.detail)
	}
	if !strings.Contains(g.detail, "masking.yaml") {
		t.Errorf("the detail %q does not say which rules a branch is masked by", g.detail)
	}

	src := stageNamed(t, stages, "the database source")
	if src.state != StageWarn {
		t.Fatalf("an unset source beside a usable golden is %q, want a warning: %s", src.state, src.detail)
	}
	if !strings.Contains(src.detail, "next refresh needs it") {
		t.Errorf("the detail %q does not say what the source is now for", src.detail)
	}
	if !strings.Contains(src.detail, "0 3 * * *") {
		t.Errorf("the detail %q does not mention the refresh schedule the manifest sets", src.detail)
	}
	if src.command != "af secret set AF_START_TEST_SOURCE_URL" {
		t.Errorf("the warning offers %q, want the command that clears it", src.command)
	}

	next, blocked := nextStep(stages)
	if blocked {
		t.Error("a machine that can run af up is reported as blocked")
	}
	if next == nil || next.command != "af up" {
		t.Errorf("the next step is %v, want af up", next)
	}
	if err := renderStart(e, stages); err != nil {
		t.Errorf("a warning made af start exit non zero: %v", err)
	}
	body := collapse(out.String())
	if !strings.Contains(body, "af secret set AF_START_TEST_SOURCE_URL") {
		t.Errorf("the text form does not print the command that clears the warning:\n%s", out.String())
	}
}

// With the source set, the same machine reports the source as done, and the
// value is never printed.
func TestASetSourceIsDoneAndNeverPrinted(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+dockerWithSource)
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	const value = "postgres://reader:notarealpassword@prod.example.test/app"
	e, out := startEnv(t, dir)
	e.Getenv = func(k string) string {
		if k == "AF_START_TEST_SOURCE_URL" {
			return value
		}
		return ""
	}
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))
	_ = renderStart(e, stages)
	src := stageNamed(t, stages, "the database source")
	if src.state != StageDone {
		t.Errorf("a set source is %q, want done: %s", src.state, src.detail)
	}
	if strings.Contains(out.String(), "notarealpassword") {
		t.Fatal("af start printed the source connection string")
	}
}

// No source named and no golden: af up makes the first golden itself, so the
// rung must not send the reader to a refresh the run would do for them.
func TestNoSourceAndNoGoldenPointsAtAfUp(t *testing.T) {
	installedRunnerAt(t)
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
`)
	e, _ := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))
	g := stageNamed(t, stages, "a golden")
	if g.state != StagePending {
		t.Fatalf("no golden is %q, want pending: %s", g.state, g.detail)
	}
	if g.command != "af up" {
		t.Errorf("with no source the golden rung offers %q, want af up", g.command)
	}
	if next, blocked := nextStep(stages); blocked || next == nil || next.command != "af up" {
		t.Errorf("the next step is %v (blocked %v), want af up", next, blocked)
	}
}

// A hosted provider's listing needs credentials and the lock, so that rung is
// still declined, still says why, and still names the command that answers.
func TestAHostedProviderGoldenRungIsDeclinedWithItsReason(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: neon
  project: proj-1234
  version: 17
`)
	e, _ := startEnv(t, dir)
	g := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "a golden")
	if g.state != StageUnchecked {
		t.Errorf("the neon golden rung is %q, want unchecked: %s", g.state, g.detail)
	}
	if !strings.Contains(g.why, "lock") {
		t.Errorf("the reason %q does not say why this command will not ask", g.why)
	}
	if g.command != "af golden list" {
		t.Errorf("the golden rung offers %q, want af golden list", g.command)
	}
	if g.downstream {
		t.Error("the golden rung is marked as waiting on something, but it was declined on purpose")
	}
}

// A daemon that does not answer is reported as not checked, never as no
// golden, because "none" would send somebody to a refresh against a daemon
// that is down.
func TestADaemonThatDoesNotListIsNotChecked(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+dockerWithSource)
	e, _ := startEnv(t, dir)
	probe := startProbeFor(t, t.TempDir())
	probe.goldens = func(context.Context, *Env, *schema.Manifest) ([]provider.GoldenVersion, error) {
		return nil, errors.New("dial unix /var/run/docker.sock: connect: no such file")
	}
	g := stageNamed(t, firstRun(t.Context(), e, probe), "a golden")
	if g.state != StageUnchecked {
		t.Errorf("a daemon that did not list is %q, want unchecked: %s", g.state, g.detail)
	}
	if !strings.Contains(g.why, "docker.sock") {
		t.Errorf("the reason %q does not carry the daemon's error", g.why)
	}
}

func TestAWarningIsNeverTheNextStepAndNeverBlocks(t *testing.T) {
	stages := []stage{
		{name: "warned", state: StageWarn, command: "a"},
		{name: "required", state: StagePending, command: "b"},
	}
	next, blocked := nextStep(stages)
	if blocked {
		t.Error("a warning was reported as blocking")
	}
	if next == nil || next.name != "required" {
		t.Errorf("the next step is %v, want the pending rung", next)
	}
}

// An absent masking file is not a defect, and this test says so because the
// first version of it asserted the opposite and was wrong.
//
// The reasoning that produced the wrong version was sound as far as it went:
// the normaliser fills masking_rules in with masking.yaml, so every loaded
// manifest names one, and the answerable question is whether the file is there.
// What it never checked is what the engine DOES about it. env/golden.go treats
// os.IsNotExist on that path as "use the built in rules" and says in its own
// comment that a missing file is the common case. So the rung called every
// freshly initialised repository broken, since af init names the default path
// and writes no file.
//
// Found by running af init and then af start on an ordinary Express app rather
// than by reading either. What the rung reports now is which rules would apply,
// which is true either way and is not visible from the manifest alone.
func TestAnAbsentMaskingFileIsReportedRatherThanBlamed(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
`)
	e, _ := startEnv(t, dir)

	g := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "a golden")
	if g.state == StageBlocked {
		t.Errorf("an absent masking file is reported as blocking, which is what af init writes "+
			"on every first run: %s", g.detail)
	}
	if !strings.Contains(g.detail, "built in rules") {
		t.Errorf("the detail %q does not say which rules would apply", g.detail)
	}

	// And with a file there it names the file instead, so the two states are
	// distinguishable rather than both reading as fine.
	write(t, dir, "masking.yaml", "version: 1\nrules: []\n")
	g = stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "a golden")
	if !strings.Contains(g.detail, "masking.yaml") {
		t.Errorf("with the rules present the detail %q does not name them", g.detail)
	}
	if g.state == StageBlocked {
		t.Errorf("the golden rung is blocked with the rules present: %s", g.detail)
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

// The masking rules rung, in each of its states. Done when the file the
// manifest names exists; pending with the command that writes it when a
// source is named and the file is not there; waiting on the source otherwise,
// because with no source there is no schema to write rules from.
func TestTheMaskingRulesRungFollowsTheSourceAndTheFile(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
`)
	e, _ := startEnv(t, dir)
	probe := startProbeFor(t, t.TempDir())

	s := stageNamed(t, firstRun(t.Context(), e, probe), "masking rules")
	if s.state != StageUnchecked || !s.downstream {
		t.Fatalf("with no source the rung is %q (downstream %v), want unchecked and waiting: %s",
			s.state, s.downstream, s.detail)
	}
	if !strings.Contains(s.why, "source_url_env") {
		t.Errorf("the reason %q does not name the setting that would change it", s.why)
	}

	writeManifest(t, dir, startManifest+`
database:
  provider: docker
  version: 17
  source_url_env: PRODUCTION_DATABASE_URL
`)
	s = stageNamed(t, firstRun(t.Context(), e, probe), "masking rules")
	if s.state != StagePending {
		t.Fatalf("with a source and no file the rung is %q, want pending: %s", s.state, s.detail)
	}
	if s.command != "af mask init" {
		t.Errorf("the rung offers %q, want af mask init", s.command)
	}

	write(t, dir, "masking.yaml", "rules: []\n")
	s = stageNamed(t, firstRun(t.Context(), e, probe), "masking rules")
	if s.state != StageDone {
		t.Fatalf("with the file there the rung is %q, want done: %s", s.state, s.detail)
	}
	if !strings.Contains(s.detail, "masking.yaml") {
		t.Errorf("the detail %q does not name the file", s.detail)
	}
}

// The same demotion the source rung got. With a usable golden, af up branches
// it whatever masking.yaml says, so an absent file is a warning about the next
// refresh and never the next command.
func TestAnAbsentMaskingFileBesideAUsableGoldenIsAWarningNotTheNextStep(t *testing.T) {
	installedRunnerAt(t)
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+dockerWithSource)
	mine := identityOf(t, dir)
	e, _ := startEnv(t, dir)
	probe := goldensOf(startProbeFor(t, t.TempDir()),
		provider.GoldenVersion{ID: "gv_20260906043704_d1c46d45", Verified: true,
			Provenance: mine, CreatedAt: madeYesterday})
	stages := firstRun(t.Context(), e, probe)

	s := stageNamed(t, stages, "masking rules")
	if s.state != StageWarn {
		t.Fatalf("an absent masking file beside a usable golden is %q, want a warning: %s", s.state, s.detail)
	}
	if s.command != "af mask init" {
		t.Errorf("the warning offers %q, want af mask init", s.command)
	}
	if !strings.Contains(s.detail, "next refresh") {
		t.Errorf("the detail %q does not say the file is for the next refresh", s.detail)
	}
	if next, _ := nextStep(stages); next == nil || next.command != "af up" {
		t.Errorf("the next step is %v, want af up", next)
	}
}

// The rung sits directly after the source it depends on, so the list reads in
// the order the reader will do things.
func TestTheMaskingRulesRungComesRightAfterTheSource(t *testing.T) {
	dir := t.TempDir()
	e, _ := startEnv(t, dir)
	stages := firstRun(t.Context(), e, startProbeFor(t, t.TempDir()))
	for i, s := range stages {
		if s.name == "the database source" {
			if i+1 >= len(stages) || stages[i+1].name != "masking rules" {
				t.Fatalf("the rung after the source is %q, want masking rules", stages[i+1].name)
			}
			return
		}
	}
	t.Fatal("no source rung")
}

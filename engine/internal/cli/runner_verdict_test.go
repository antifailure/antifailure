package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// af runner check said complete: true about a tree it had just said it could
// not read.
//
// A runner whose package.json cannot be parsed reports its dependencies as
// `skip`, with the detail "not checked", which is correct and deliberate: an
// unanswered question is not a proven failure and reporting it as one would
// send somebody to reinstall over a manifest that is the thing wrong with the
// tree. The verdict was then computed as "nothing proved a blockage, therefore
// ready", so the command printed the honest per-check line and the dishonest
// conclusion under it, exited 0, and told any script reading the exit code
// that a tree nobody could inspect was fine.
//
// The fix is a third state and not a flipped boolean. `complete` and
// `not complete` cannot say "I could not tell", and flipping it would have
// merged an unread manifest with a missing node, which are different things to
// do next. `af ci` had already drawn this exact line: a run in which every
// workflow was blocked "has not declined to blame the application; it has not
// looked at it, and exiting zero tells the pipeline that it did".
//
// Every test here is written against the unreadable manifest.

// unreadableManifestRunner is a runner whose source is present and whose
// manifest cannot be parsed, which is the tree the old verdict called
// complete.
func unreadableManifestRunner(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("// runner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAnUnreadableRunnerIsUndeterminedRatherThanComplete(t *testing.T) {
	f := newRunnerFixture(t)
	unreadableManifestRunner(t, filepath.Join(f.home, ".antifailure", "runner"))

	report, out, _ := runCheck(t, f.project)

	if report.Complete {
		t.Errorf("complete: true about a runner whose manifest could not be read\n%s", out)
	}
	if report.Verdict != VerdictUndetermined {
		t.Errorf("verdict %q, want %q\n%s", report.Verdict, VerdictUndetermined, out)
	}
	if len(report.Unanswered) == 0 {
		t.Errorf("the verdict is undetermined and the document names no unanswered question\n%s", out)
	}
	// The per-check line was always right. It is the conclusion drawn from it
	// that was wrong, so both are asserted: a fix that made the finding say
	// "fail" would satisfy the verdict and lose the distinction.
	if got := item(t, report, "dependencies"); got.Result != SymbolSkip {
		t.Errorf("dependencies reported %q, want skip: nothing was proved about this tree", got.Result)
	}
}

func TestUndeterminedDoesNotExitZero(t *testing.T) {
	f := newRunnerFixture(t)
	unreadableManifestRunner(t, filepath.Join(f.home, ".antifailure", "runner"))

	_, out, err := runCheck(t, f.project)
	if err == nil {
		t.Fatalf("af runner check exited 0 on a runner it could not inspect\n%s", out)
	}
	if code := exitCodeOfSilent(t, err); code != aferrors.ExitInterruptedClean {
		t.Errorf("exit code %d, want %d: an unread tree is not a configuration error, it is nothing measured",
			code, aferrors.ExitInterruptedClean)
	}
}

func TestTheThreeVerdictsGetThreeExitCodes(t *testing.T) {
	// The whole point of a third state is that a caller can tell the three
	// apart, and two of them sharing a code would be the boolean again wearing
	// a longer name.
	if err := runnerCheckExit(VerdictReady); err != nil {
		t.Errorf("a ready runner exits non zero: %v", err)
	}
	blocked := exitCodeOfSilent(t, runnerCheckExit(VerdictBlocked))
	undetermined := exitCodeOfSilent(t, runnerCheckExit(VerdictUndetermined))
	if blocked != aferrors.ExitConfiguration {
		t.Errorf("blocked exits %d, want %d", blocked, aferrors.ExitConfiguration)
	}
	if undetermined != aferrors.ExitInterruptedClean {
		t.Errorf("undetermined exits %d, want %d", undetermined, aferrors.ExitInterruptedClean)
	}
	if blocked == undetermined {
		t.Errorf("blocked and undetermined both exit %d, so a script cannot tell a proven failure from an unanswered question", blocked)
	}
}

func TestAProvenFailureBeatsAnUnansweredQuestion(t *testing.T) {
	// Both mean do not proceed, and between them the actionable one is the
	// proof. A reader with a missing node in front of them should be told
	// about the node, not about the manifest that could not be parsed beside
	// it.
	results := []runnerCheck{
		{label: "dependencies", symbol: SymbolSkip, decides: true, detail: "not checked: package.json could not be parsed"},
		{label: "node", symbol: SymbolFail, decides: true, detail: "not found"},
	}
	if v := runnerVerdict(results); v != VerdictBlocked {
		t.Errorf("verdict %q with a proven failure present, want blocked", v)
	}
	// And with the failure removed, the same set is undetermined rather than
	// ready. Without this the assertion above passes on a verdict that is
	// always blocked.
	if v := runnerVerdict(results[:1]); v != VerdictUndetermined {
		t.Errorf("verdict %q with only the unanswered question, want undetermined", v)
	}
}

func TestABrowserNobodyCouldLookForIsNotUndetermined(t *testing.T) {
	// The browser is not a deciding question. af runner install treats a
	// failed download as non fatal and af test returns unverified rather than
	// a wrong answer without one, so a platform whose browser cache location
	// is unknown must not turn the whole verdict into "I could not tell".
	results := []runnerCheck{
		{label: "runner", symbol: SymbolOK, decides: true},
		{label: "dependencies", symbol: SymbolOK, decides: true},
		{label: "node", symbol: SymbolOK, decides: true},
		{label: "browser", symbol: SymbolSkip, detail: "not checked: this platform's browser cache location is not known here"},
	}
	if v := runnerVerdict(results); v != VerdictReady {
		t.Errorf("verdict %q when only the browser went unchecked, want ready", v)
	}
	if got := unanswered(results); len(got) != 0 {
		t.Errorf("the browser was reported as an unanswered deciding question: %v", got)
	}
}

func TestTheRenderedReportSaysWhatItCouldNotCheck(t *testing.T) {
	f := newRunnerFixture(t)
	unreadableManifestRunner(t, filepath.Join(f.home, ".antifailure", "runner"))
	// A browser the fixture can find, so that NOTHING in this report carries a
	// remedy. Without it the browser line warns with "Download it with: af
	// runner install", the closing hint is suppressed because a remedy already
	// named that command, and the assertion below that the hint is absent
	// could not have failed however the branch was written. Measured: the
	// mutation that sends undetermined through the blocked branch stayed green
	// until this fixture changed.
	browsers := t.TempDir()
	if err := os.MkdirAll(filepath.Join(browsers, "chromium-1234"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", browsers)

	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf), WorkDir: f.project, Getenv: func(string) string { return "" }}
	cmd := newRunnerCheckCommand(e)
	cmd.SetContext(t.Context())
	err := cmd.RunE(cmd, nil)
	out := buf.String()

	if err == nil {
		t.Fatalf("the rendered report exited 0 on a runner it could not inspect\n%s", out)
	}
	if !strings.Contains(out, "Not ready and not broken") {
		t.Errorf("the report does not say the verdict is neither:\n%s", out)
	}
	if !strings.Contains(out, "package.json could not be parsed") {
		t.Errorf("the report does not name what it could not read:\n%s", out)
	}
	// The remedy for a blockage is the wrong thing to print here. Nothing is
	// missing; something is unreadable, and reinstalling over it changes
	// nothing about the manifest.
	if strings.Contains(out, "Install it with") {
		t.Errorf("the report told somebody to install over a manifest that cannot be parsed:\n%s", out)
	}
}

func TestAfStartDoesNotCallAnUnreadableRunnerInstalled(t *testing.T) {
	// The same defect one command over, and the reason it is fixed here too:
	// this rung skipped past a `skip` with the other unremarkable answers and
	// reported the runner step done. af start already has StageUnchecked for
	// exactly this case and every other rung uses it.
	f := newRunnerFixture(t)
	unreadableManifestRunner(t, filepath.Join(f.home, ".antifailure", "runner"))

	e := &Env{Out: NewOutput(&bytes.Buffer{}, &bytes.Buffer{}), WorkDir: f.project, Getenv: func(string) string { return "" }}
	s := runnerState(t.Context(), e, startProbe{home: func() (string, error) { return f.home, nil }})
	if s.state != StageUnchecked {
		t.Errorf("the runner rung reported %q about a manifest it could not parse, want %q (detail %q)",
			s.state, StageUnchecked, s.detail)
	}
	if !strings.Contains(s.why, "package.json") {
		t.Errorf("why %q does not name what could not be read", s.why)
	}
}

package cli

// Internal, because the dashboard wiring is unexported on purpose: which
// display a run gets is a decision the up command makes, not something another
// package should be able to make for it.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/hud"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func testOrchestrator(t *testing.T) *env.Orchestrator {
	t.Helper()
	o, err := env.New(env.Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Version: 1, Name: "demo"},
		Branch:   "feature/hud",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Progress: func(string) {},
	})
	require.NoError(t, err)
	return o
}

// A buffer is not a terminal, and neither is a regular file. Both are what the
// output stream actually is in a test and in a pipeline, and a size guessed
// for either produces a frame laid out for a screen nobody is looking at.
func TestTerminalSize_SaysNoForAnythingThatIsNotATerminal(t *testing.T) {
	_, _, ok := terminalSize(&bytes.Buffer{})
	require.False(t, ok, "a buffer reported a terminal size")

	path := filepath.Join(t.TempDir(), "out.log")
	f, err := os.Create(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	_, _, ok = terminalSize(f)
	require.False(t, ok, "a regular file reported a terminal size")
}

// Where there is no terminal the run still gets a display: the line per event
// fallback, which is what a build log can actually show. Falling back rather
// than refusing is the difference between --hud working in CI and --hud being
// a flag that only works on a laptop.
func TestAttachDashboard_FallsBackToPlainWithoutATerminal(t *testing.T) {
	var out bytes.Buffer
	e := &Env{Out: NewOutput(&out, io.Discard), Stdin: strings.NewReader("")}

	view := attachDashboard(e, testOrchestrator(t))

	require.NotNil(t, view.plain, "no display was attached at all")
	require.Nil(t, view.program, "a buffer is not a terminal and must not get a frame")
}

// With no display, run is the identity: it performs the work and returns its
// error. This is the ordinary af up, and the whole dashboard mechanism has to
// be invisible to it.
func TestDashboardRun_WithoutADisplayJustDoesTheWork(t *testing.T) {
	var view *dashboard
	calls := 0
	want := errors.New("the lifecycle failed")

	got := view.run(context.Background(), func() error {
		calls++
		return want
	})

	require.Equal(t, 1, calls, "the work should run exactly once")
	require.ErrorIs(t, got, want)
}

// The plain fallback does not take the terminal, so run performs the work
// directly there too.
func TestDashboardRun_WithThePlainFallbackDoesTheWorkInline(t *testing.T) {
	view := &dashboard{plain: hud.NewPlain(io.Discard, "demo")}
	calls := 0

	require.NoError(t, view.run(context.Background(), func() error {
		calls++
		return nil
	}))
	require.Equal(t, 1, calls)
}

// With a frame, run returns only once both the work and the display have
// finished. The display finishes because run closes the program itself, which
// is what stops a failed run leaving an empty dashboard on screen forever: a
// failure before the session opens closes no bus, so nothing else would.
func TestDashboardRun_ClosesTheDisplayWhenTheWorkEndsWithoutABus(t *testing.T) {
	p := hud.NewProgram(hud.New("demo", 80, 24), strings.NewReader(""), io.Discard)
	view := &dashboard{program: p}
	want := errors.New("failed before anything opened")

	got := view.run(context.Background(), func() error { return want })

	require.ErrorIs(t, got, want,
		"the lifecycle error is the command's answer and must survive the display")
}

// --hud and --format json are two answers to one question about a single
// stream. Refusing is better than picking, because either choice silently
// throws away what the person asked for.
func TestUp_RefusesTheDashboardAndJSONTogether(t *testing.T) {
	var out, errW bytes.Buffer
	code := Execute(context.Background(), []string{"up", "--hud", "-o", "json"}, Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: t.TempDir(),
	})

	require.NotZero(t, code, "the conflict should be an error, not a silent choice")
	require.Contains(t, errW.String()+out.String(), "--hud")
}

// Up returns a nil result on every path that fails before an environment
// exists, which is a good many of them: a lock another af up already holds, an
// unresolved secret, an egress rule that will not compile, a state directory it
// cannot create. Rendering the services off that nil result dereferenced it, so
// the command a first run is most likely to fail on answered with a
// segmentation fault and a Go stack trace instead of the diagnosed error it had
// already built.
//
// Found by running two af up commands at once on one repository, which is an
// ordinary thing to do by accident. The fixture here is the deterministic
// version of the same branch: a state directory that cannot be created because
// a file is already sitting on its name.
func TestUp_AFailureBeforeAnEnvironmentExistsIsReportedRatherThanPanicking(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte("version: 1\nname: svc\nservices:\n  - name: svc\n    kind: web\n    port: 9000\n    command: /bin/svc\negress:\n  default: block\n"), 0o600))
	// A file where the state directory has to go, so MkdirAll fails and Up
	// returns AF-RUN-040 with no result at all.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".antifailure"), []byte("not a directory"), 0o600))

	var out, errW bytes.Buffer
	code := Execute(context.Background(), []string{"up"}, Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: dir,
	})

	require.NotZero(t, code)
	require.Contains(t, errW.String(), "AF-RUN-040",
		"the diagnosed error is what the user needs, and it existed the whole time")
	require.NotContains(t, out.String()+errW.String(), "panic:")
}

// failingWriter refuses everything, the way a full disk or a closed pipe does.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// A command that could not write its output did not succeed. Reporting zero
// would tell a script that the output it never received is complete, and that
// is the one thing it must not be told.
func TestExecute_ReportsFailureWhenTheOutputStreamIsBroken(t *testing.T) {
	var errW bytes.Buffer
	broken := failingWriter{err: errors.New("no space left on device")}

	code := Execute(context.Background(), []string{"version"}, Options{
		Stdout: broken, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: t.TempDir(),
	})

	require.NotZero(t, code, "a command whose output went nowhere reported success")
	require.Contains(t, errW.String(), "could not write output")
	require.Contains(t, errW.String(), "no space left on device",
		"the reason has to reach the person, not just the exit code")
}

// The ordinary case is unchanged: a working stream still exits zero.
func TestExecute_StillSucceedsWhenTheOutputStreamWorks(t *testing.T) {
	var out, errW bytes.Buffer
	code := Execute(context.Background(), []string{"version"}, Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: t.TempDir(),
	})
	require.Zero(t, code, errW.String())
	require.Contains(t, out.String(), "antifailure")
}

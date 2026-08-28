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

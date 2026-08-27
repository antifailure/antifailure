package hud_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/hud"
	"pgregory.net/rapid"
)

var update = flag.Bool("update-frames", false, "rewrite the golden frames")

// populated builds a model in a state worth looking at: services in different
// states, some egress decisions, a masking run in progress, two agents, and an
// error.
func populated(width, height int) *hud.Model {
	const env = "pr-482"
	m := hud.New(env, width, height)
	seq := uint64(0)
	next := func(t events.Type, data map[string]any) events.Event {
		seq++
		e := ev(seq, t, data)
		// The model filters other environments, so the fixture has to agree
		// with the model about which one it is watching. It did not, and every
		// event was correctly discarded, which produced an empty frame that a
		// golden test would have frozen forever. Looking at the output is what
		// caught it.
		e.Env = env
		return e
	}

	m.Apply(next("service.started", map[string]any{"service": "web", "kind": "web", "state": "running", "url": "http://127.0.0.1:8080"}))
	m.Apply(next("service.started", map[string]any{"service": "worker", "kind": "worker", "state": "running"}))
	m.Apply(next("service.started", map[string]any{"service": "a-very-long-service-name-that-will-not-fit", "kind": "cron", "state": "starting"}))

	for i := 0; i < 12; i++ {
		m.Apply(next("egress.decision", map[string]any{"decision": "allow"}))
	}
	m.Apply(next("egress.decision", map[string]any{"decision": "deny", "host": "telemetry.vendor.example"}))
	m.Apply(next("egress.decision", map[string]any{"decision": "mock"}))

	m.Apply(next(events.MaskProgress, map[string]any{"phase": "masking", "percent": float64(64)}))
	m.Apply(next(events.MaskVerified, map[string]any{"version": "gv_20260101_ab12cd"}))

	m.Apply(next("agent.started", map[string]any{"agent": "smoke", "state": "running", "passed": 7, "failed": 0}))
	m.Apply(next("agent.started", map[string]any{"agent": "checkout-flow", "state": "failed", "passed": 3, "failed": 1}))

	boom := next("env.failed", nil)
	boom.Level = events.LevelError
	boom.Msg = "the worker exited with status 1"
	m.Apply(boom)
	return m
}

// The spec's sizes. Both are checked because the layout changes between them
// and a frame that is right at one size proves nothing about the other.
func TestGoldenFrames(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		focus         hud.Pane
	}{
		{"80x24-services", 80, 24, hud.PaneServices},
		{"80x24-log", 80, 24, hud.PaneLog},
		{"160x50-services", 160, 50, hud.PaneServices},
		{"160x50-database", 160, 50, hud.PaneDatabase},
		{"120x30-wide", 120, 30, hud.PaneNetwork},
		{"80x24-empty", 80, 24, hud.PaneServices},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := populated(c.width, c.height)
			if strings.HasSuffix(c.name, "-empty") {
				m = hud.New("pr-482", c.width, c.height)
			}
			got := m.Render(c.focus)

			path := filepath.Join("testdata", c.name+".frame")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden frame at %s. Run `go test ./internal/hud -update-frames`: %v", path, err)
			}
			if got != string(want) {
				t.Errorf("frame differs from %s.\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
			}
		})
	}
}

// No line may be wider than the terminal. This is the bug that makes a HUD
// unusable rather than merely ugly: one long line wraps, every pane below it
// shifts, and the whole frame becomes noise.
func TestNoLineEverExceedsTheTerminalWidth(t *testing.T) {
	for _, width := range []int{80, 100, 120, 160, 200} {
		m := populated(width, 40)
		for _, focus := range []hud.Pane{hud.PaneServices, hud.PaneNetwork, hud.PaneDatabase, hud.PaneAgents, hud.PaneLog} {
			for i, line := range strings.Split(m.Render(focus), "\n") {
				if n := len([]rune(line)); n > width {
					t.Errorf("width %d focus %v: line %d is %d cells:\n%q", width, focus, i, n, line)
				}
			}
		}
	}
}

// The layout has to actually change, or the two golden sizes are testing the
// same code twice.
func TestTheLayoutChangesWithWidth(t *testing.T) {
	if hud.LayoutFor(80) != hud.LayoutNarrow {
		t.Error("80 columns should stack")
	}
	if hud.LayoutFor(120) != hud.LayoutWide {
		t.Error("120 columns should put panes side by side")
	}
	if hud.LayoutFor(160) != hud.LayoutFull {
		t.Error("160 columns should show three columns")
	}

	narrow := populated(80, 30).Render(hud.PaneServices)
	full := populated(160, 30).Render(hud.PaneServices)
	if narrow == full {
		t.Error("the two layouts render identically, so one of them is not being used")
	}
}

// The focused pane must be distinguishable without colour, because the HUD has
// to read in a terminal that has none and in a recording where it did not
// survive.
func TestFocusIsVisibleWithoutColour(t *testing.T) {
	m := populated(120, 30)
	a := m.Render(hud.PaneServices)
	b := m.Render(hud.PaneLog)
	if a == b {
		t.Fatal("focus must change the frame")
	}
	if !strings.Contains(a, "▸ SERVICES") {
		t.Error("the focused pane should be marked in the text itself")
	}
}

func TestTabOrderWrapsBothWays(t *testing.T) {
	p := hud.PaneServices
	for i := 0; i < 5; i++ {
		p = p.Next()
	}
	if p != hud.PaneServices {
		t.Errorf("five Next calls should return to the start, got %v", p)
	}
	if hud.PaneServices.Prev() != hud.PaneLog {
		t.Errorf("Prev from the first pane should wrap to the last, got %v", hud.PaneServices.Prev())
	}
}

// An empty region reads as broken. Every pane says what it is waiting for.
func TestEveryPaneHasAnEmptyState(t *testing.T) {
	m := hud.New("pr-482", 120, 30)
	out := m.Render(hud.PaneServices)
	for _, want := range []string{"no services yet", "no agents running", "waiting for events", "idle"} {
		if !strings.Contains(out, want) {
			t.Errorf("the empty frame should say %q:\n%s", want, out)
		}
	}
}

// Dropping is shown, not hidden. The number is how somebody knows to distrust
// the rest of the frame.
func TestDroppedEventsAppearOnTheStatusLine(t *testing.T) {
	m := hud.New("env1", 100, 20)
	m.Apply(ev(5, "env.ready", nil))
	m.Apply(ev(5, "env.ready", nil))
	if !strings.Contains(m.Render(hud.PaneServices), "dropped") {
		t.Errorf("a dropped event must be visible:\n%s", m.Render(hud.PaneServices))
	}
}

// A terminal smaller than anything sensible must still render rather than
// panic or produce negative-width output.
func TestARidiculouslySmallTerminalStillRenders(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {10, 3}, {0, 0}, {39, 9}} {
		m := populated(size[0], size[1])
		out := m.Render(hud.PaneServices)
		if out == "" {
			t.Errorf("%dx%d rendered nothing", size[0], size[1])
		}
	}
}

// The rendering must be a function of the events alone. A frame that reads the
// clock cannot be golden tested, and the failure is a suite that goes red at
// midnight.
func TestRenderingIsDeterministic(t *testing.T) {
	a := populated(120, 30).Render(hud.PaneServices)
	b := populated(120, 30).Render(hud.PaneServices)
	if a != b {
		t.Error("the same events must always draw the same frame")
	}
}

// The spec's exit criterion, applied to the rendering as well as the model.
func TestAnySequenceOfEventsRendersWithoutPanicking(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		width := rapid.IntRange(0, 220).Draw(t, "width")
		height := rapid.IntRange(0, 60).Draw(t, "height")
		m := hud.New("env1", width, height)

		n := rapid.IntRange(0, 60).Draw(t, "n")
		for i := 0; i < n; i++ {
			m.Apply(events.Event{
				Env: "env1",
				Seq: uint64(rapid.IntRange(0, 30).Draw(t, "seq")),
				Type: events.Type(rapid.SampledFrom([]string{
					"service.started", "egress.decision", "mask.progress", "agent.started",
				}).Draw(t, "type")),
				Level: events.LevelInfo,
				Msg:   rapid.StringN(0, 300, -1).Draw(t, "msg"),
				Data: map[string]any{
					"service":  rapid.StringN(0, 300, -1).Draw(t, "service"),
					"decision": rapid.SampledFrom([]any{"allow", "deny", nil}).Draw(t, "decision"),
					"percent":  rapid.SampledFrom([]any{float64(0), float64(150), float64(-5), nil}).Draw(t, "percent"),
				},
			})
		}

		for p := hud.PaneServices; p <= hud.PaneLog; p++ {
			out := m.Render(p)
			// The width invariant has to hold for generated input too, which
			// is where an unbounded name would otherwise escape the column.
			w := width
			if w < 40 {
				w = 40
			}
			for _, line := range strings.Split(out, "\n") {
				if len([]rune(line)) > w {
					t.Fatalf("a line escaped the terminal at width %d: %q", width, line)
				}
			}
		}
	})
}

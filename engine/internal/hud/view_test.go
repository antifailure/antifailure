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

// fixtureEvent is one event in a scripted stream. Scripted as data rather
// than as calls so that a test can check what the fixtures contain, which is
// how the invented event type below was caught.
type fixtureEvent struct {
	t    events.Type
	msg  string
	data map[string]any
}

// busy is a state worth looking at: services in different states, some egress
// decisions, a masking run in progress, two agents, and an error.
//
// Catalog constants, not string literals. The first version used
// "service.started", which is not an event type: the display routes on the
// "service." prefix so it rendered anyway, and the golden frames were a
// picture of a stream the engine never emits.
var busy = []fixtureEvent{
	{t: events.ServiceReady, data: map[string]any{"service": "web", "kind": "web", "state": "running", "url": "http://127.0.0.1:8080"}},
	{t: events.ServiceReady, data: map[string]any{"service": "worker", "kind": "worker", "state": "running"}},
	{t: events.ServiceStarting, data: map[string]any{"service": "a-very-long-service-name-that-will-not-fit", "kind": "cron", "state": "starting"}},

	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "allow"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "deny", "host": "telemetry.vendor.example"}},
	{t: events.EgressDecision, data: map[string]any{"decision": "mock"}},

	{t: events.MaskProgress, data: map[string]any{"phase": "masking", "percent": float64(64)}},
	{t: events.MaskVerified, data: map[string]any{"version": "gv_20260101_ab12cd"}},

	{t: events.AgentStarted, data: map[string]any{"agent": "smoke", "state": "running", "passed": 7, "failed": 0}},
	{t: events.AgentStarted, data: map[string]any{"agent": "checkout-flow", "state": "failed", "passed": 3, "failed": 1}},

	{t: events.EnvFailed, msg: "the worker exited with status 1"},
}

// upRun is the stream af up emits, in the order it emits it, with the fields
// the engine really attaches. It is the one fixture that has to stay in step
// with internal/env: if a pane goes blank here, the dashboard goes blank on a
// real run, and reading the code would not have told you.
var upRun = []fixtureEvent{
	{t: events.EnvCreating, msg: "creating pr-482", data: map[string]any{"branch": "feature/checkout"}},
	{t: events.BuildStarted, msg: "web: build starting", data: map[string]any{"service": "web"}},
	{t: events.BuildLog, msg: "#4 [2/6] RUN npm ci", data: map[string]any{"service": "web"}},
	{t: events.BuildFinished, msg: "web: built in 12s", data: map[string]any{"service": "web", "cached": false, "seconds": 12.4}},
	{t: events.BuildStarted, msg: "worker: build starting", data: map[string]any{"service": "worker"}},
	{t: events.BuildFinished, msg: "worker: image is unchanged", data: map[string]any{"service": "worker", "cached": true, "seconds": 0.2}},
	{t: events.GoldenReady, msg: "golden gv_20260101_ab12cd is verified", data: map[string]any{"phase": "golden", "version": "gv_20260101_ab12cd", "verified": true}},
	{t: events.DBBranching, msg: "branching from gv_20260101_ab12cd", data: map[string]any{"phase": "branching", "version": "gv_20260101_ab12cd"}},
	{t: events.DBBranched, msg: "branched from gv_20260101_ab12cd", data: map[string]any{"phase": "branched", "version": "gv_20260101_ab12cd"}},
	{t: events.ResourceCreated, msg: "network", data: map[string]any{"provider": "local", "kind": "network", "id": "af-pr-482"}},
	{t: events.ServiceStarting, msg: "web is starting", data: map[string]any{"service": "web", "kind": "web", "state": "starting"}},
	{t: events.ServiceStarting, msg: "worker is starting", data: map[string]any{"service": "worker", "kind": "worker", "state": "starting"}},
	{t: events.ServiceReady, msg: "web is running", data: map[string]any{"service": "web", "kind": "web", "state": "running", "url": "http://127.0.0.1:41273"}},
	{t: events.ServiceReady, msg: "worker is running", data: map[string]any{"service": "worker", "kind": "worker", "state": "running"}},
	{t: events.EnvReady, msg: "pr-482 is ready", data: map[string]any{"url": "http://127.0.0.1:41273", "proxied": true}},
}

// apply plays a scripted stream into a new model.
func apply(script []fixtureEvent, width, height int) *hud.Model {
	const env = "pr-482"
	m := hud.New(env, width, height)
	for i, f := range script {
		e := ev(uint64(i+1), f.t, f.data)
		// The model filters other environments, so the fixture has to agree
		// with the model about which one it is watching. It did not, and every
		// event was correctly discarded, which produced an empty frame that a
		// golden test would have frozen forever. Looking at the output is what
		// caught it.
		e.Env = env
		e.Msg = f.msg
		if f.t == events.EnvFailed {
			e.Level = events.LevelError
		}
		m.Apply(e)
	}
	return m
}

func populated(width, height int) *hud.Model { return apply(busy, width, height) }

// Every type a fixture uses has to be in the catalog. A display that routes on
// a prefix will happily render "service.started", and a golden frame taken
// from it is a picture of something the engine cannot produce.
func TestFixturesUseOnlyRealEventTypes(t *testing.T) {
	real := map[events.Type]bool{}
	for _, tp := range events.AllTypes() {
		real[tp] = true
	}
	for name, script := range map[string][]fixtureEvent{"busy": busy, "upRun": upRun} {
		for i, f := range script {
			if !real[f.t] {
				t.Errorf("%s[%d] uses %q, which is not an event type in the catalog", name, i, f.t)
			}
		}
	}
}

// The spec's sizes. Both are checked because the layout changes between them
// and a frame that is right at one size proves nothing about the other.
func TestGoldenFrames(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		focus         hud.Pane
		script        []fixtureEvent
	}{
		{"80x24-services", 80, 24, hud.PaneServices, busy},
		{"80x24-log", 80, 24, hud.PaneLog, busy},
		{"160x50-services", 160, 50, hud.PaneServices, busy},
		{"160x50-database", 160, 50, hud.PaneDatabase, busy},
		{"120x30-wide", 120, 30, hud.PaneNetwork, busy},
		{"80x24-empty", 80, 24, hud.PaneServices, nil},
		// The one frame taken from the stream af up really emits. It is here
		// so that a change to what the engine publishes shows up as a picture
		// of a dashboard with a blank pane, rather than as nothing at all.
		{"120x34-up-run", 120, 34, hud.PaneLog, upRun},
		// The frame the documentation shows. Narrow enough to read in a code
		// block on a phone, and generated rather than typed: the hand written
		// one in guides/dashboard.md had invented column widths and network
		// counts the stream it claimed to show cannot produce.
		{"96x26-guide", 96, 26, hud.PaneServices, upRun},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := apply(c.script, c.width, c.height)
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

// guidePath is the page whose example frame comes from the golden below.
const guidePath = "../../../docs/src/content/docs/guides/dashboard.md"

const (
	guideStart = "<!-- frame:start -->"
	guideEnd   = "<!-- frame:end -->"
)

// The documentation's example frame is the rendered one, not a typed
// approximation of it.
//
// G8 asks for screenshots regenerated by the pipeline rather than committed by
// hand, and a terminal frame is this product's screenshot. The first version
// of that page was typed from memory: its columns were the wrong width and its
// network counts came from a stream the example could not have produced.
//
// Regenerate with: go test ./internal/hud -update-frames
func TestGuideFrameIsTheRenderedOne(t *testing.T) {
	raw, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("the guide is not where this test expects it: %v", err)
	}
	page := string(raw)

	i := strings.Index(page, guideStart)
	j := strings.Index(page, guideEnd)
	if i < 0 || j < 0 || j < i {
		t.Fatalf("the guide has no %s and %s pair, so there is nowhere to put the frame",
			guideStart, guideEnd)
	}

	frame := apply(upRun, 96, 26).Render(hud.PaneServices)
	// Trailing spaces are how the renderer pads a pane to its width, and they
	// are invisible in a code block while making every regeneration a diff.
	var trimmed []string
	for _, line := range strings.Split(frame, "\n") {
		trimmed = append(trimmed, strings.TrimRight(line, " "))
	}
	want := page[:i] + guideStart + "\n\n```\n" +
		strings.TrimRight(strings.Join(trimmed, "\n"), "\n") + "\n```\n\n" + page[j:]

	if *update {
		if err := os.WriteFile(guidePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if page != want {
		t.Errorf("the guide's frame is not the rendered one. "+
			"Regenerate with: go test ./internal/hud -update-frames\n--- got ---\n%s",
			page[i:j])
	}
}

package live

import (
	"bufio"
	"bytes"
	"go/build"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecodeReadsAnEventAndRefusesRubbish(t *testing.T) {
	ev, ok := Decode([]byte(`{"t":"step","agent":"a","seq":3,"text":"Press Continue"}`))
	if !ok {
		t.Fatal("a well-formed line did not decode")
	}
	if ev.T != KindStep || ev.Agent != "a" || ev.Seq != 3 || ev.Text != "Press Continue" {
		t.Fatalf("decoded the wrong event: %+v", ev)
	}
	// A blank line, a malformed line, and valid JSON with no kind are all
	// skipped rather than mistaken for events.
	if _, ok := Decode([]byte("   ")); ok {
		t.Fatal("a blank line decoded as an event")
	}
	if _, ok := Decode([]byte("{not json")); ok {
		t.Fatal("a malformed line decoded as an event")
	}
	if _, ok := Decode([]byte(`{"x":1}`)); ok {
		t.Fatal("a line with no kind decoded as an event")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	ev := Event{T: KindFrame, Agent: "a", Seq: 7, Mime: "image/jpeg", W: 1280, H: 800, B64: "Zm9v"}
	back, ok := Decode(Encode(ev))
	if !ok {
		t.Fatal("an encoded event did not decode")
	}
	if back.B64 != "Zm9v" || back.W != 1280 || back.Seq != 7 {
		t.Fatalf("round trip lost the frame: %+v", back)
	}
	// One event is one line.
	line := Encode(ev)
	if !bytes.HasSuffix(line, []byte("\n")) || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("encode did not produce exactly one line: %q", line)
	}
}

func TestHubKeepsAgentsInFirstSeenOrder(t *testing.T) {
	h := NewHub()
	h.Publish(Event{T: KindAgent, Agent: "b", Surface: "web", State: "pending"})
	h.Publish(Event{T: KindAgent, Agent: "a", Surface: "web", State: "pending"})
	h.Publish(Event{T: KindAgent, Agent: "b", Surface: "web", State: "live"})
	snap := h.Snapshot()
	if len(snap.Agents) != 2 {
		t.Fatalf("want 2 agents, got %d", len(snap.Agents))
	}
	// b was seen first, so it stays first even though a later event touched it.
	if snap.Agents[0].ID != "b" || snap.Agents[1].ID != "a" {
		t.Fatalf("order not first-seen: %s, %s", snap.Agents[0].ID, snap.Agents[1].ID)
	}
	if snap.Agents[0].State != "live" {
		t.Fatalf("agent b state not updated: %s", snap.Agents[0].State)
	}
}

func TestHubKeepsTheNewestFrameNotTheLastArrived(t *testing.T) {
	h := NewHub()
	h.Publish(Event{T: KindFrame, Agent: "a", Seq: 5, W: 10, H: 10, B64: "new"})
	// A stale frame arriving after a fresh one, which a loaded stream can do.
	h.Publish(Event{T: KindFrame, Agent: "a", Seq: 2, W: 10, H: 10, B64: "old"})
	snap := h.Snapshot()
	if snap.Agents[0].Frame == nil {
		t.Fatal("no frame kept")
	}
	// The newest by sequence wins, not the last to arrive.
	if snap.Agents[0].Frame.B64 != "new" || snap.Agents[0].Frame.Seq != 5 {
		t.Fatalf("a stale frame replaced a newer one: %+v", snap.Agents[0].Frame)
	}
}

func TestHubRecordsStepsAndCounts(t *testing.T) {
	h := NewHub()
	h.Publish(Event{T: KindHello, Run: "run-1"})
	h.Publish(Event{T: KindStep, Agent: "a", Seq: 1, Text: "Open /"})
	h.Publish(Event{T: KindStep, Agent: "a", Seq: 2, Text: "Press Continue"})
	h.Publish(Event{T: KindDone, Passed: 3, Failed: 1})
	snap := h.Snapshot()
	if snap.Run != "run-1" {
		t.Fatalf("run id not recorded: %q", snap.Run)
	}
	if len(snap.Agents[0].Steps) != 2 || snap.Agents[0].Steps[1].Text != "Press Continue" {
		t.Fatalf("steps not recorded in order: %+v", snap.Agents[0].Steps)
	}
	if !snap.Counts.Present || snap.Counts.Passed != 3 || snap.Counts.Failed != 1 {
		t.Fatalf("counts not recorded: %+v", snap.Counts)
	}
}

func TestSnapshotDoesNotAliasHubState(t *testing.T) {
	h := NewHub()
	h.Publish(Event{T: KindStep, Agent: "a", Seq: 1, Text: "one"})
	snap := h.Snapshot()
	// Mutating the snapshot must not reach back into the Hub.
	snap.Agents[0].Steps[0].Text = "mutated"
	again := h.Snapshot()
	if again.Agents[0].Steps[0].Text != "one" {
		t.Fatal("snapshot aliased the hub's own slice")
	}
}

func TestSocketServerFeedsTheHubFromNDJSON(t *testing.T) {
	// A short path on purpose: a unix socket's path has a hard length limit
	// (104 bytes on macOS), and a test temp dir name blows past it. The engine
	// creates its live socket in a short directory for the same reason.
	dir, err := os.MkdirTemp("", "afl")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "l.sock")
	srv, err := Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	hub := NewHub()
	go srv.Serve(hub)

	// A client that speaks the exact wire the runner speaks: connect, write
	// NDJSON, close. This is the runner -> socket -> hub path end to end.
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	w := bufio.NewWriter(conn)
	for _, ev := range []Event{
		{T: KindHello, Run: "r"},
		{T: KindAgent, Agent: "a", Surface: "web", State: "live", Persona: "owner", Workflow: "signup"},
		{T: KindFrame, Agent: "a", Seq: 1, Mime: "image/jpeg", W: 800, H: 600, B64: "aW1n"},
	} {
		w.Write(Encode(ev))
	}
	w.Flush()
	conn.Close()

	// Poll: the server reads on its own goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := hub.Snapshot()
		if len(snap.Agents) == 1 && snap.Agents[0].Frame != nil && snap.Agents[0].Frame.B64 == "aW1n" {
			if snap.Agents[0].Persona != "owner" {
				t.Fatalf("persona not carried across the socket: %q", snap.Agents[0].Persona)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the frame written to the socket never reached the hub")
}

func TestFrameFromTheSocketReachesTheTerminalView(t *testing.T) {
	// The whole chain a watcher depends on: a frame written to the socket the
	// way the runner writes it, decoded by the server, folded into the hub, and
	// shown in the rendered terminal view. If any link breaks, the frame's
	// metadata is absent from the render.
	dir, err := os.MkdirTemp("", "afl")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	srv, err := Listen(filepath.Join(dir, "l.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	hub := NewHub()
	go srv.Serve(hub)

	conn, err := net.Dial("unix", srv.Path())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Write(Encode(Event{T: KindAgent, Agent: "signup", Surface: "web", State: "live", Persona: "owner", Workflow: "signup"}))
	conn.Write(Encode(Event{T: KindFrame, Agent: "signup", Seq: 1, Mime: "image/jpeg", W: 1440, H: 900, B64: "aW1n"}))
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out := Render(hub.Snapshot(), RenderOpts{Focus: 0, Color: false})
		if strings.Contains(out, "1440x900") && strings.Contains(out, "owner / signup") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a frame written to the socket never showed up in the terminal view")
}

func TestWriteSSEFramesAnEvent(t *testing.T) {
	var buf bytes.Buffer
	ev := Event{T: KindStep, Agent: "a", Seq: 1, Text: "Open /"}
	if err := WriteSSE(&buf, ev); err != nil {
		t.Fatalf("write sse: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "event: step\ndata: ") {
		t.Fatalf("sse frame missing its event/data lines: %q", out)
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Fatalf("sse frame not terminated by a blank line: %q", out)
	}
	// The data line decodes back to the event a browser would read.
	dataLine := strings.TrimPrefix(strings.SplitN(out, "\n", 2)[1], "data: ")
	back, ok := Decode([]byte(strings.TrimSpace(dataLine)))
	if !ok || back.Text != "Open /" {
		t.Fatalf("sse data did not decode back: %q", dataLine)
	}
}

func sampleState() State {
	return State{
		Run: "run-xyz",
		Agents: []Agent{
			{ID: "signup", Persona: "owner", Workflow: "signup", Surface: "web", State: "live",
				Frame: &Frame{Seq: 4, W: 1280, H: 800, At: "12:00:01"},
				Steps: []Step{{Seq: 1, Text: "Open /signup", URL: "http://x/signup"}}},
			{ID: "cli", Persona: "", Workflow: "run the migration", Surface: "terminal", State: "ended",
				Verdict: "pass",
				Steps:   []Step{{Seq: 1, Text: "$ af up"}, {Seq: 2, Text: "migration held under 12s"}}},
		},
	}
}

func TestRenderShowsAgentsAndMarksTheFocused(t *testing.T) {
	s := sampleState()
	out := Render(s, RenderOpts{Focus: 0, Color: false})
	if !strings.Contains(out, "owner / signup") {
		t.Fatalf("focused agent label missing:\n%s", out)
	}
	if !strings.Contains(out, "LIVE") {
		t.Fatalf("live state chip missing:\n%s", out)
	}
	// The pane below the rail shows the focused agent's step.
	if !strings.Contains(out, "Open /signup") {
		t.Fatalf("focused pane did not show its step:\n%s", out)
	}
	// A web (pixel) surface shows frame metadata since a terminal cannot show
	// the image.
	if !strings.Contains(out, "1280x800") {
		t.Fatalf("pixel surface frame metadata missing:\n%s", out)
	}
}

func TestRenderSwitchingFocusChangesWhatIsShown(t *testing.T) {
	s := sampleState()
	first := Render(s, RenderOpts{Focus: 0, Color: false})
	second := Render(s, RenderOpts{Focus: 1, Color: false})
	if first == second {
		t.Fatal("switching the focused agent did not change the output")
	}
	// Focusing the terminal agent shows its cast, which the web pane did not.
	if !strings.Contains(second, "migration held under 12s") {
		t.Fatalf("second agent's cast not shown when focused:\n%s", second)
	}
	if strings.Contains(first, "migration held under 12s") {
		t.Fatalf("first pane leaked the second agent's cast:\n%s", first)
	}
}

func TestRenderColorOffHasNoEscapes(t *testing.T) {
	out := Render(sampleState(), RenderOpts{Focus: 0, Color: false})
	if strings.Contains(out, "\x1b[") {
		t.Fatal("color was off but the output carried ANSI escapes")
	}
	on := Render(sampleState(), RenderOpts{Focus: 0, Color: true})
	if !strings.Contains(on, "\x1b[") {
		t.Fatal("color was on but the output carried no ANSI escapes")
	}
}

func TestRenderEmptyStateSaysConnecting(t *testing.T) {
	out := Render(State{}, RenderOpts{Color: false})
	if !strings.Contains(out, "Connecting") {
		t.Fatalf("empty state did not render an honest connecting line:\n%s", out)
	}
}

func TestSwitchKey(t *testing.T) {
	// Number keys select by index when the agent exists.
	if got := SwitchKey("2", 0, 3); got != 1 {
		t.Fatalf("key 2 -> %d, want 1", got)
	}
	// A number past the end leaves focus where it was.
	if got := SwitchKey("9", 1, 3); got != 1 {
		t.Fatalf("key 9 out of range -> %d, want 1", got)
	}
	// Arrows and vi keys step and clamp.
	if got := SwitchKey("down", 0, 3); got != 1 {
		t.Fatalf("down -> %d, want 1", got)
	}
	if got := SwitchKey("down", 2, 3); got != 2 {
		t.Fatalf("down at end -> %d, want 2 (clamped)", got)
	}
	if got := SwitchKey("up", 0, 3); got != 0 {
		t.Fatalf("up at start -> %d, want 0 (clamped)", got)
	}
	// Tab wraps.
	if got := SwitchKey("tab", 2, 3); got != 0 {
		t.Fatalf("tab at end -> %d, want 0 (wrap)", got)
	}
	// An unknown key does nothing, and no agents means index 0.
	if got := SwitchKey("z", 1, 3); got != 1 {
		t.Fatalf("unknown key -> %d, want 1", got)
	}
	if got := SwitchKey("2", 0, 0); got != 0 {
		t.Fatalf("no agents -> %d, want 0", got)
	}
}

// TestFrameBytesNeverReachTheControlPlane is the boundary guard. A frame's
// bytes are ephemeral and must never be written to the control plane. This is
// enforced structurally two ways: the live package must not import the
// controlplane client, and the control-plane packages must carry no
// frame-shaped handling. Either failing means somebody wired video into the
// store, which is the promise the console makes and this test protects.
func TestFrameBytesNeverReachTheControlPlane(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("could not read this package's imports: %v", err)
	}
	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		if strings.Contains(imp, "controlplane") {
			t.Fatalf("the live package imports %q; frames must not reach the control plane", imp)
		}
	}

	// The control-plane and reporting sources must carry nothing that handles a
	// frame's bytes. If this ever matches, a frame path was added to the store.
	forbidden := []string{"b64", "image/jpeg", "screencast", "livefram", "framebytes"}
	roots := []string{"../controlplane", "../env"}
	scanned := 0
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			scanned++
			lower := strings.ToLower(string(body))
			for _, token := range forbidden {
				if strings.Contains(lower, token) {
					t.Fatalf("%s mentions %q; a frame's bytes must never reach the control plane", path, token)
				}
			}
			return nil
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no control-plane or env sources; the boundary was not actually checked")
	}
}

package live

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"go/build"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/termimg"
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
	conn.Write(Encode(Event{
		T: KindAgent, Agent: "signup", Surface: "web", State: "live",
		Persona: "owner", Workflow: "signup",
		Personality: "Edge-Case Explorer", Traits: "steady · bold · nav first",
	}))
	conn.Write(Encode(Event{T: KindFrame, Agent: "signup", Seq: 1, Mime: "image/jpeg", W: 1440, H: 900, B64: "aW1n"}))
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = Render(hub.Snapshot(), RenderOpts{
			Focus: 0, Width: 100, Height: 24, Color: false, Images: noImages,
		})
		// The frame's own detail, the personality the engine drew, and the
		// profile under it: every field a pane heads itself with, carried the
		// whole way from the socket.
		if strings.Contains(last, "1440x900") &&
			strings.Contains(last, "Edge-Case Explorer") &&
			strings.Contains(last, "steady · bold · nav first") &&
			strings.Contains(last, "signup · as owner · web") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a frame written to the socket never showed up in the terminal view:\n%s", last)
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

// sampleState is a swarm: three agents on one workflow with three different
// personalities, and a fourth on a terminal surface, which is the shape a run
// with a diversity block actually produces.
func sampleState() State {
	return State{
		Run: "run-xyz",
		Agents: []Agent{
			{
				ID: "checkout#0", Persona: "owner", Workflow: "checkout", Surface: "web",
				State: "live", Personality: "Visual Follower", Traits: "impatient · bold · follows prominence",
				Frame: &Frame{Seq: 4, W: 1280, H: 800, At: "12:00:01", B64: jpegFixture},
				Steps: []Step{{Seq: 1, Text: "Open /checkout", URL: "http://x/checkout"}},
			},
			{
				ID: "checkout#1", Persona: "owner", Workflow: "checkout", Surface: "web",
				State: "live", Personality: "Text-Oriented User", Traits: "unhurried · cautious · reads labels",
				Frame: &Frame{Seq: 6, W: 1280, H: 800, At: "12:00:02", B64: jpegFixture},
				Steps: []Step{{Seq: 1, Text: "Read the delivery terms"}},
			},
			{
				ID: "checkout#2", Persona: "owner", Workflow: "checkout", Surface: "web",
				State: "pending", Personality: "Edge-Case Explorer", Traits: "steady · bold · nav first",
			},
			{
				ID: "migrate", Workflow: "run the migration", Surface: "terminal",
				State: "ended", Verdict: "pass",
				Steps: []Step{{Seq: 1, Text: "$ af up"}, {Seq: 2, Text: "migration held under 12s"}},
			},
		},
	}
}

// jpegFixture is a real, tiny JPEG in the base64 shape the runner sends, so the
// image tests draw an image rather than assert on a placeholder string that
// would pass whatever the encoder did with it.
var jpegFixture = func() string {
	img := image.NewRGBA(image.Rect(0, 0, 48, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 48; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 5), G: uint8(y * 8), B: 0x40, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}()

// noImages is a terminal that draws nothing, which is the zero value and the
// safe default.
var noImages = termimg.Capability{Why: "a test, with no terminal to draw on"}

func TestRenderShowsEveryAgentAtOnceWithItsPersonality(t *testing.T) {
	// The whole point of the view: one frame shows the swarm, and the swarm is
	// legible as different agents rather than as one agent repeated.
	out := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages})
	for _, want := range []string{
		"Visual Follower", "Text-Oriented User", "Edge-Case Explorer",
		"impatient · bold · follows prominence",
		"unhurried · cautious · reads labels",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the swarm did not show %q:\n%s", want, out)
		}
	}
	// Every state is on screen at once, not just the focused one.
	for _, want := range []string{"LIVE", "PENDING", "PASS"} {
		if !strings.Contains(out, want) {
			t.Fatalf("state %q missing from the swarm:\n%s", want, out)
		}
	}
	// And the agent with no personality plan says so rather than leaving a
	// blank line that reads as a pane that failed to load.
	if !strings.Contains(out, "no personality assigned") {
		t.Fatalf("an agent with no personality plan showed nothing:\n%s", out)
	}
}

func TestRenderHeadsEachPaneWithItsPersonality(t *testing.T) {
	// The personality is the heading, beside the pane's number, because that is
	// the thing that tells four panes running one workflow apart.
	out := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages})
	if !strings.Contains(out, "1 Visual Follower") {
		t.Fatalf("pane 1 is not headed by its personality:\n%s", out)
	}
	if !strings.Contains(out, "2 Text-Oriented User") {
		t.Fatalf("pane 2 is not headed by its personality:\n%s", out)
	}
	// The workflow is still there, on the line below, for somebody who needs it.
	if !strings.Contains(out, "checkout · as owner · web") {
		t.Fatalf("the workflow line is missing:\n%s", out)
	}
}

func TestRenderReflowsToTheWidth(t *testing.T) {
	// Wide enough for four panes across, and narrow enough for one. The check
	// is whether two headings share a line, which is what a column actually is.
	wide := Render(sampleState(), RenderOpts{Width: 200, Height: 40, Images: noImages})
	if !strings.Contains(wide, "1 Visual Follower") || !lineWith(wide, "1 Visual Follower", "2 Text-Oriented User") {
		t.Fatalf("at 200 columns the first two panes are not side by side:\n%s", wide)
	}
	narrow := Render(sampleState(), RenderOpts{Width: 44, Height: 60, Images: noImages})
	if lineWith(narrow, "1 Visual Follower", "2 Text-Oriented User") {
		t.Fatalf("at 44 columns the panes did not stack:\n%s", narrow)
	}
	if !strings.Contains(narrow, "Text-Oriented User") {
		t.Fatalf("stacking lost an agent:\n%s", narrow)
	}
}

// lineWith reports whether one line of the frame holds both strings, which is
// how a test asks "are these two panes in the same row".
func lineWith(out, a, b string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, a) && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

func TestRenderNeverLetsALineWrap(t *testing.T) {
	// A line wider than the terminal wraps, and a wrapped line pushes every
	// pane below it down a row, which puts the pictures in the wrong place.
	for _, width := range []int{40, 44, 80, 100, 120, 160, 200} {
		out := Render(sampleState(), RenderOpts{Width: width, Height: 40, Images: noImages})
		for i, line := range strings.Split(out, "\n") {
			if got := visible(line); got > width {
				t.Fatalf("at width %d line %d is %d cells wide: %q", width, i, got, line)
			}
		}
	}
}

func TestRenderDrawsRealImagesWhenTheTerminalCan(t *testing.T) {
	images := termimg.Capability{Protocol: termimg.Kitty, CellWidth: 10, CellHeight: 20, Why: "a test"}
	out := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: images})

	// Two agents have a frame; the pending one and the terminal one do not.
	if got := strings.Count(out, "\x1b_Ga=T"); got != 2 {
		t.Fatalf("want 2 images drawn, got %d:\n%q", got, out)
	}
	// Each pane's image carries its own id, so a terminal that keys images by
	// id replaces that pane's picture rather than stacking a new one a second.
	if !strings.Contains(out, "i=1,p=1") || !strings.Contains(out, "i=2,p=1") {
		t.Fatalf("the two panes did not get their own image ids:\n%q", out)
	}
	// And the text fallback is NOT drawn over the top of them.
	if strings.Contains(out, "this terminal draws no pictures") {
		t.Fatalf("a terminal that draws images was told it does not:\n%s", out)
	}
}

func TestRenderPutsEveryImageAfterAllOfTheText(t *testing.T) {
	// The ordering is the whole reason the images survive. Each protocol paints
	// pixels over the cells it covers, the layout leaves blank lines where a
	// picture goes, and writing a blank into one of those cells wipes the
	// pixels. Drawn last, the pictures land on top of the text just painted.
	images := termimg.Capability{Protocol: termimg.ITerm2, CellWidth: 10, CellHeight: 20, Why: "a test"}
	out := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: images})
	first := strings.Index(out, "\x1b]1337;File=")
	if first < 0 {
		t.Fatal("no image was drawn at all")
	}
	// Nothing printable after the first image. Everything from there on is
	// escape sequences: the placements themselves.
	for _, r := range out[first:] {
		if r == '\n' {
			t.Fatalf("a newline follows the first image, so text is drawn over it:\n%q", out[first:])
		}
	}
}

func TestRenderPlacesEachImageInItsOwnPane(t *testing.T) {
	// Two panes side by side must get two different screen positions. Both
	// landing in the same place is the failure that looks like one image
	// flickering between two agents.
	images := termimg.Capability{Protocol: termimg.ITerm2, CellWidth: 10, CellHeight: 20, Why: "a test"}
	out := Render(sampleState(), RenderOpts{Width: 200, Height: 40, Images: images})
	positions := cursorMoves(out)
	if len(positions) != 2 {
		t.Fatalf("want 2 positioned images, got %d: %v", len(positions), positions)
	}
	if positions[0] == positions[1] {
		t.Fatalf("both panes drew their picture at %s", positions[0])
	}
	// Side by side at this width, so the same row and different columns.
	r0, c0 := splitPos(t, positions[0])
	r1, c1 := splitPos(t, positions[1])
	if r0 != r1 {
		t.Fatalf("two panes on one row drew at rows %d and %d", r0, r1)
	}
	if c1 <= c0 {
		t.Fatalf("the second pane's picture is not to the right of the first: %d, %d", c0, c1)
	}
	// And inside the frame rather than over the header.
	if r0 < 3 {
		t.Fatalf("a picture was drawn at row %d, over the header", r0)
	}
}

// cursorMoves pulls the absolute cursor positions out of a frame, which is
// where each image was placed.
func cursorMoves(out string) []string {
	var found []string
	for _, part := range strings.Split(out, "\x1b7\x1b[")[1:] {
		if end := strings.Index(part, "H"); end >= 0 {
			found = append(found, part[:end])
		}
	}
	return found
}

func splitPos(t *testing.T, pos string) (int, int) {
	t.Helper()
	parts := strings.Split(pos, ";")
	if len(parts) != 2 {
		t.Fatalf("not a cursor position: %q", pos)
	}
	row, err1 := strconv.Atoi(parts[0])
	col, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		t.Fatalf("not a cursor position: %q", pos)
	}
	return row, col
}

func TestRenderFallsBackToTextAndSaysWhy(t *testing.T) {
	// The fallback is deliberate rather than an empty region: it names the
	// frame that did arrive, so it is clear the run is producing pictures and
	// only the display cannot show them, and the footer says which terminal
	// this is.
	out := Render(sampleState(), RenderOpts{
		Width: 160, Height: 40,
		Images: termimg.Capability{Why: "this terminal answered and reported no image protocol"},
	})
	if strings.Contains(out, "\x1b_G") || strings.Contains(out, "\x1b]1337") || strings.Contains(out, "\x1bP") {
		t.Fatalf("a terminal that draws nothing was sent an image escape:\n%q", out)
	}
	if !strings.Contains(out, "frame 4 · 1280x800") {
		t.Fatalf("the fallback did not name the frame that arrived:\n%s", out)
	}
	if !strings.Contains(out, "this terminal answered and reported no image protocol") {
		t.Fatalf("the footer did not say why there is no picture:\n%s", out)
	}
	// The steps are still there, so a terminal with no pictures is still a
	// usable view rather than a punishment.
	if !strings.Contains(out, "Open /checkout") {
		t.Fatalf("the fallback lost the steps:\n%s", out)
	}
}

func TestRenderTellsRefusedApartFromNeverAsked(t *testing.T) {
	// Two reasons for an empty pane that are not the same fact. A view that
	// showed the same sentence for both would be telling somebody their
	// terminal cannot do something it was never asked to do.
	refused := Render(sampleState(), RenderOpts{
		Width: 120, Height: 30,
		Images: termimg.Capability{Why: "this terminal reported no image protocol"},
	})
	off := Render(sampleState(), RenderOpts{
		Width: 120, Height: 30,
		Images: termimg.Capability{Why: "pictures were switched off with --no-images"},
	})
	if refused == off {
		t.Fatal("a terminal that cannot draw and one that was told not to render identically")
	}
	if !strings.Contains(off, "switched off") {
		t.Fatalf("the deliberate switch is not reported:\n%s", off)
	}
}

func TestRenderPagesRatherThanSquashing(t *testing.T) {
	// Twelve agents in a short terminal. Squeezing them all in would cost every
	// pane the line it exists for, so the grid pages and says that it did.
	s := State{Run: "big"}
	for i := 0; i < 12; i++ {
		s.Agents = append(s.Agents, Agent{
			ID: "w" + strconv.Itoa(i), Workflow: "checkout", Surface: "web", State: "live",
			Personality: "Persona " + strconv.Itoa(i),
		})
	}
	out := Render(s, RenderOpts{Width: 120, Height: 16, Images: noImages, Focus: 0})
	if !strings.Contains(out, "of 12") {
		t.Fatalf("the footer did not say how many agents are off the page:\n%s", out)
	}
	if !strings.Contains(out, "Persona 0") {
		t.Fatalf("the focused agent is not on the page shown:\n%s", out)
	}

	// Focusing an agent further down brings its page up. A marker on a pane
	// nobody can see is not a switcher.
	later := Render(s, RenderOpts{Width: 120, Height: 16, Images: noImages, Focus: 11})
	if !strings.Contains(later, "Persona 11") {
		t.Fatalf("focusing agent 12 did not bring its page up:\n%s", later)
	}
}

func TestRenderHandlesOneAgentAndTwelve(t *testing.T) {
	one := State{Run: "one", Agents: []Agent{{
		ID: "solo", Workflow: "checkout", Surface: "web", State: "live",
		Personality: "Visual Follower", Steps: []Step{{Text: "Open /"}},
	}}}
	out := Render(one, RenderOpts{Width: 120, Height: 30, Images: noImages})
	if !strings.Contains(out, "Visual Follower") || !strings.Contains(out, "Open /") {
		t.Fatalf("a single agent did not render:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if visible(line) > 120 {
			t.Fatalf("one agent overflowed the width: %q", line)
		}
	}
}

func TestRenderSoloGivesOneAgentTheScreen(t *testing.T) {
	s := sampleState()
	swarm := Render(s, RenderOpts{Width: 120, Height: 30, Images: noImages, Focus: 1})
	solo := Render(s, RenderOpts{Width: 120, Height: 30, Images: noImages, Focus: 1, Solo: true})
	if solo == swarm {
		t.Fatal("solo rendered the same frame as the swarm")
	}
	if !strings.Contains(solo, "Text-Oriented User") {
		t.Fatalf("solo did not show the focused agent:\n%s", solo)
	}
	if strings.Contains(solo, "Visual Follower") {
		t.Fatalf("solo leaked another agent into the view:\n%s", solo)
	}
	// The way back is on screen, because a full screen view with no way out is
	// a trap.
	if !strings.Contains(solo, "back to the swarm") {
		t.Fatalf("solo does not say how to get back:\n%s", solo)
	}
}

func TestRenderMarksTheFocusWithoutColour(t *testing.T) {
	// The mark has to survive NO_COLOR and a recording that lost its colour, so
	// it is a heavier rule as well as a colour.
	first := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages, Focus: 0})
	second := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages, Focus: 1})
	if first == second {
		t.Fatal("moving the focus changed nothing")
	}
	if !strings.Contains(first, "━") {
		t.Fatalf("the focused pane carries no heavier rule:\n%s", first)
	}
}

func TestRenderColorOffHasNoEscapes(t *testing.T) {
	out := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages, Color: false})
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("color was off but the output carried ANSI escapes:\n%q", out)
	}
	on := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages, Color: true})
	if !strings.Contains(on, "\x1b[") {
		t.Fatal("color was on but the output carried no ANSI escapes")
	}
	// Colour off is about markup, not about content. A picture is content, so
	// it is still drawn, and it is the only escape in the frame.
	images := termimg.Capability{Protocol: termimg.Kitty, CellWidth: 10, CellHeight: 20, Why: "a test"}
	plain := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: images, Color: false})
	if !strings.Contains(plain, "\x1b_Ga=T") {
		t.Fatal("colour off also switched the pictures off")
	}
}

func TestRenderEmptyStateSaysConnecting(t *testing.T) {
	out := Render(State{}, RenderOpts{Width: 80, Height: 20, Images: noImages})
	if !strings.Contains(out, "Connecting") {
		t.Fatalf("empty state did not render an honest connecting line:\n%s", out)
	}
}

// TestRenderWritesNoEmDashAndNoDoubleHyphen is the house writing rule applied
// to the one surface a file scanner cannot reach: strings composed at run time
// out of several sources. prosecheck reads Markdown and the web trees, so
// nothing it runs on would ever see this frame.
func TestRenderWritesNoEmDashAndNoDoubleHyphen(t *testing.T) {
	frames := []string{
		Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: noImages}),
		Render(sampleState(), RenderOpts{Width: 44, Height: 40, Images: noImages, Solo: true}),
		Render(State{}, RenderOpts{Width: 80, Height: 20, Images: noImages}),
	}
	for i, frame := range frames {
		if strings.Contains(frame, "\u2014") {
			t.Fatalf("frame %d has an em dash in it:\n%s", i, frame)
		}
		if strings.Contains(frame, "--") {
			t.Fatalf("frame %d has a double hyphen in it:\n%s", i, frame)
		}
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

func TestRenderSaysWhenThePaneIsTooShortForAPicture(t *testing.T) {
	// A third reason for an empty region, and the one that turns up the moment
	// a dozen agents share a small window: the terminal draws pictures and this
	// pane has no room for one. An unexplained gap reads as a pane that failed.
	images := termimg.Capability{Protocol: termimg.Kitty, CellWidth: 10, CellHeight: 20, Why: "a test"}
	roomy := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Images: images})
	if strings.Contains(roomy, "too short for a picture") {
		t.Fatalf("a pane with room said it had none:\n%s", roomy)
	}
	if !strings.Contains(roomy, "\x1b_Ga=T") {
		t.Fatal("the roomy arm drew no picture, so the cramped arm proves nothing")
	}

	// The same agents in a window too short to give any pane an image. Four
	// panes across at this width, so the height is what runs out: three lines
	// of heading and one of step leave fewer than the three rows a picture
	// needs to be a picture rather than a smear.
	cramped := Render(sampleState(), RenderOpts{Width: 160, Height: 9, Images: images})
	if strings.Contains(cramped, "\x1b_Ga=T") {
		t.Fatalf("a pane with no room drew a picture anyway:\n%q", cramped)
	}
	if !strings.Contains(cramped, "this pane is too short for a picture") {
		t.Fatalf("the cramped pane did not say why there is no picture:\n%s", cramped)
	}
	// And it does not blame the terminal for something the terminal can do.
	if strings.Contains(cramped, "this terminal draws no pictures") {
		t.Fatalf("a terminal that draws pictures was told it does not:\n%s", cramped)
	}
}

// TestPaletteIsLegibleOnItsOwnBackground measures every colour the view draws
// against the background it was chosen for.
//
// A comment claiming a ratio is not a check. This is the measurement, and it is
// the one that found the problem: the neon the live mark is drawn in reads 1.78
// to 1 on the warm paper the launch film's terminal uses, where LIVE is very
// nearly invisible, and 9.18 to 1 on a dark terminal, where nobody would ever
// see it go wrong.
func TestPaletteIsLegibleOnItsOwnBackground(t *testing.T) {
	// The two backgrounds the product is actually looked at on: the launch
	// film's warm paper, and an ordinary dark terminal.
	paper := [3]int{0xf7, 0xf4, 0xee}
	dark := [3]int{0x14, 0x17, 0x1a}

	for name, c := range map[string]struct {
		p  palette
		bg [3]int
	}{"dark": {darkPalette, dark}, "light": {lightPalette, paper}} {
		for _, tn := range []tone{toneDim, toneNeon, tonePass, toneFail, toneWarn} {
			code, ok := c.p[tn]
			if !ok {
				t.Fatalf("the %s palette has no colour for tone %d", name, tn)
			}
			rgb, ok := sgrRGB(code)
			if !ok {
				t.Fatalf("the %s palette's tone %d is not a 256 colour code: %q", name, tn, code)
			}
			// 4.5 to 1 is the ordinary text threshold. A status word somebody
			// has to read at a glance is ordinary text.
			if got := contrast(rgb, c.bg); got < 4.5 {
				t.Fatalf("the %s palette's tone %d reads %.2f to 1 on its own background; "+
					"a reader cannot see it", name, tn, got)
			}
		}
	}

	// And the falsification arm: the dark palette's neon really is unreadable on
	// paper, which is the whole reason there are two palettes. If this ever
	// passes, the split has stopped being necessary and the measurement above
	// has stopped meaning anything.
	neon, _ := sgrRGB(darkPalette[toneNeon])
	if got := contrast(neon, paper); got >= 4.5 {
		t.Fatalf("the dark neon reads %.2f to 1 on paper, so the two palettes are measuring nothing", got)
	}
}

// sgrRGB turns an SGR 256 colour code back into its channels, through the same
// cube the terminal uses.
func sgrRGB(code string) ([3]int, bool) {
	var n int
	if _, err := fmt.Sscanf(code, "\x1b[38;5;%dm", &n); err != nil {
		return [3]int{}, false
	}
	switch {
	case n >= 232:
		v := 8 + (n-232)*10
		return [3]int{v, v, v}, true
	case n >= 16:
		levels := [6]int{0, 95, 135, 175, 215, 255}
		i := n - 16
		return [3]int{levels[i/36], levels[(i/6)%6], levels[i%6]}, true
	}
	return [3]int{}, false
}

// contrast is the WCAG ratio between two colours.
func contrast(a, b [3]int) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c [3]int) float64 {
	channel := func(v int) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c[0]) + 0.7152*channel(c[1]) + 0.0722*channel(c[2])
}

func TestRenderTakesItsPaletteFromTheTerminalsOwnBackground(t *testing.T) {
	onDark := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Color: true, Images: noImages})
	onLight := Render(sampleState(), RenderOpts{
		Width: 160, Height: 40, Color: true, Light: true, Images: noImages,
	})
	if onDark == onLight {
		t.Fatal("a light terminal got the palette tuned for a dark one")
	}
	if !strings.Contains(onDark, darkPalette[toneNeon]) {
		t.Fatal("the dark frame does not use the dark neon")
	}
	if strings.Contains(onLight, darkPalette[toneNeon]) {
		t.Fatalf("the light frame carries the dark neon, which reads 1.78 to 1 on paper:\n%q", onLight)
	}
	if !strings.Contains(onLight, lightPalette[toneNeon]) {
		t.Fatal("the light frame does not use the light neon")
	}
	// Colour off is still colour off on either background.
	plain := Render(sampleState(), RenderOpts{Width: 160, Height: 40, Light: true, Images: noImages})
	if strings.Contains(plain, "\x1b[") {
		t.Fatal("a light terminal with colour off still got escapes")
	}
}

func TestAPaneWithNoPictureGivesTheRoomToTheCast(t *testing.T) {
	// A terminal surface produces no frames at all, so its steps ARE its
	// content. This pane showed seventeen blank lines and one sentence, which
	// is the surface that needs the room most getting none of it.
	s := State{Agents: []Agent{{
		ID: "migrate", Workflow: "run the migration", Surface: "terminal", State: "live",
		Steps: []Step{
			{Text: "$ af up"}, {Text: "postgres 17 ready in 4.1s"},
			{Text: "migration 0007 applied"}, {Text: "migration held under 12s"},
			{Text: "health check passed"},
		},
	}}}
	images := termimg.Capability{Protocol: termimg.Kitty, CellWidth: 10, CellHeight: 20, Why: "a test"}
	out := Render(s, RenderOpts{Width: 90, Height: 24, Images: images})

	// Every step, not only the newest.
	for _, want := range []string{"$ af up", "postgres 17 ready in 4.1s", "migration 0007 applied", "health check passed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the cast lost %q:\n%s", want, out)
		}
	}
	// A terminal surface gets no picture, so nothing was drawn over it either.
	if strings.Contains(out, "\x1b_Ga=T") {
		t.Fatal("a terminal surface with no frame was sent an image")
	}

	// A pane that IS showing a picture keeps its room for the picture: the cast
	// must not grow into it. Without this arm the change above could have
	// crowded out every image and the test above would still pass.
	withFrame := sampleState()
	drawn := Render(withFrame, RenderOpts{Width: 160, Height: 40, Images: images})
	if !strings.Contains(drawn, "\x1b_Ga=T") {
		t.Fatal("the pane with a frame stopped drawing it")
	}
	// The second agent in the sample has exactly one step; a pane drawing a
	// picture shows that one and does not fill the pane with repeats of it.
	if n := strings.Count(drawn, "Read the delivery terms"); n != 1 {
		t.Fatalf("a pane with a picture showed its step %d times, want 1:\n%s", n, drawn)
	}
}

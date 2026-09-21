package cli

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/live"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/termimg"
)

// watchFixture is a hub carrying the swarm a run with a diversity block
// produces: two agents on one workflow with different personalities, one of
// them with a frame.
func watchFixture(t *testing.T) *live.Hub {
	t.Helper()
	hub := live.NewHub()
	hub.Publish(live.Event{T: live.KindHello, Run: "run-1"})
	hub.Publish(live.Event{
		T: live.KindAgent, Agent: "checkout#0", Surface: "web", State: "live",
		Persona: "owner", Workflow: "checkout",
		Personality: "Visual Follower", Traits: "impatient · bold · follows prominence",
	})
	hub.Publish(live.Event{
		T: live.KindAgent, Agent: "checkout#1", Surface: "web", State: "live",
		Persona: "owner", Workflow: "checkout",
		Personality: "Text-Oriented User", Traits: "unhurried · cautious · reads labels",
	})
	hub.Publish(live.Event{
		T: live.KindFrame, Agent: "checkout#0", Seq: 2,
		Mime: "image/jpeg", W: 1280, H: 800, B64: watchJPEG(t),
	})
	hub.Publish(live.Event{T: live.KindStep, Agent: "checkout#1", Seq: 1, Text: "Read the delivery terms"})
	return hub
}

// watchJPEG is a real JPEG, so the encoder under the view has something it can
// actually decode. A placeholder string would make every assertion below pass
// whatever the drawing did with it.
func watchJPEG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 25))
	for y := 0; y < 25; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: 0x30, B: uint8(y * 9), A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// TestWatchViewDrawsTheSwarmAndItsPictures is the wiring check. The renderer
// and the image encoder each have their own tests; this one proves the command
// actually hands them the terminal's capability, because a capability detected
// and never passed on is a feature that compiles and draws nothing.
func TestWatchViewDrawsTheSwarmAndItsPictures(t *testing.T) {
	m := watchModel{
		hub:    watchFixture(t),
		width:  160,
		height: 40,
		images: termimg.Capability{
			Protocol: termimg.Kitty, CellWidth: 10, CellHeight: 20, Why: "a test",
		},
		start: time.Now(),
	}
	out := m.View()
	if !strings.Contains(out, "Visual Follower") || !strings.Contains(out, "Text-Oriented User") {
		t.Fatalf("the swarm's personalities are not on screen:\n%s", out)
	}
	if !strings.Contains(out, "\x1b_Ga=T") {
		t.Fatal("the terminal can draw and no image escape reached the frame")
	}
	if !strings.Contains(out, "images kitty") {
		t.Fatalf("the footer does not report the protocol in use:\n%s", out)
	}
}

func TestWatchViewFallsBackAndSaysWhy(t *testing.T) {
	m := watchModel{
		hub: watchFixture(t), width: 160, height: 40,
		images: termimg.Capability{Why: "this terminal reported no image protocol"},
		start:  time.Now(),
	}
	out := m.View()
	if strings.Contains(out, "\x1b_G") || strings.Contains(out, "\x1b]1337") {
		t.Fatalf("a terminal that draws nothing was sent an image escape:\n%q", out)
	}
	// The run is still watchable: the frame that arrived is named, and so is
	// the reason there is no picture of it.
	if !strings.Contains(out, "1280x800") {
		t.Fatalf("the fallback did not name the frame:\n%s", out)
	}
	if !strings.Contains(out, "this terminal reported no image protocol") {
		t.Fatalf("the footer does not say why:\n%s", out)
	}
}

func TestWatchKeysSwitchAgentsAndOpenOneFull(t *testing.T) {
	m := watchModel{hub: watchFixture(t), width: 120, height: 30, start: time.Now()}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if got := next.(watchModel).focus; got != 1 {
		t.Fatalf("pressing 2 moved the focus to %d, want 1", got)
	}

	full, _ := next.(watchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !full.(watchModel).solo {
		t.Fatal("f did not give the focused agent the whole screen")
	}
	out := full.(watchModel).View()
	if !strings.Contains(out, "Text-Oriented User") {
		t.Fatalf("solo did not show the focused agent:\n%s", out)
	}
	if strings.Contains(out, "Visual Follower") {
		t.Fatalf("solo leaked the other agent in:\n%s", out)
	}

	back, _ := full.(watchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if back.(watchModel).solo {
		t.Fatal("f did not come back to the swarm")
	}
}

func TestWatchTakesItsSizeFromTheTerminal(t *testing.T) {
	// A resize mid run is ordinary rather than an edge case, and the height
	// decides how many panes fit and how tall a picture may be. It used to be
	// read and thrown away, which is why this is a test rather than an
	// assumption.
	m := watchModel{hub: watchFixture(t), width: 80, height: 24, start: time.Now()}
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	got := resized.(watchModel)
	if got.width != 200 || got.height != 60 {
		t.Fatalf("a resize gave %dx%d, want 200x60", got.width, got.height)
	}
	if lines := strings.Count(got.View(), "\n"); lines < 24 {
		t.Fatalf("the taller terminal drew only %d lines", lines)
	}
}

func TestImageCapabilitySaysWhichKindOfNoItMeans(t *testing.T) {
	e := &Env{Out: &Output{}, Getenv: func(string) string { return "" }}

	off := imageCapability(e, true)
	if off.Protocol != termimg.NoImages || !strings.Contains(off.Why, "switched off") {
		t.Fatalf("--no-images did not report a deliberate switch: %+v", off)
	}

	// A test's buffer is not a terminal, so there is nothing to ask and nothing
	// that would draw the answer. The reason has to say that rather than blame
	// the terminal for a capability it was never asked about.
	e.Stdin = &bytes.Buffer{}
	e.Out.Out = &bytes.Buffer{}
	piped := imageCapability(e, false)
	if piped.Protocol != termimg.NoImages {
		t.Fatalf("a buffer was read as a terminal: %+v", piped)
	}
	if !strings.Contains(piped.Why, "not a terminal") {
		t.Fatalf("the reason does not say there is no terminal: %q", piped.Why)
	}
	if piped.Why == off.Why {
		t.Fatal("a switched off run and a piped run gave the same reason")
	}
}

func TestPlainLineNamesThePersonality(t *testing.T) {
	// The non-terminal path is what a pipe, a log and CI see. On a run with a
	// diversity block, several agents run the same workflow as the same
	// persona, so the personality is the only thing that tells their lines
	// apart.
	line := plainLine(live.Event{
		T: live.KindAgent, Agent: "checkout#1", State: "live",
		Persona: "owner", Personality: "Edge-Case Explorer",
	})
	if !strings.Contains(line, "Edge-Case Explorer") {
		t.Fatalf("the plain line drops the personality: %q", line)
	}
	if !strings.Contains(line, "owner") || !strings.Contains(line, "checkout#1") {
		t.Fatalf("the plain line lost the persona or the agent: %q", line)
	}
}

func TestWatchTakesItsPaletteFromTheTerminalsBackground(t *testing.T) {
	// The terminal is asked what colour it is painted in the same round trip
	// that asks what it draws, and the answer has to reach the renderer. A
	// background detected and then dropped is a live mark at 1.78 to 1 on the
	// launch film's paper terminal, which is the defect this exists to stop.
	base := watchModel{hub: watchFixture(t), width: 160, height: 40, color: true, start: time.Now()}

	onDark := base
	onDark.images = termimg.Capability{Background: termimg.BackgroundDark, Why: "a test"}
	onLight := base
	onLight.images = termimg.Capability{Background: termimg.BackgroundLight, Why: "a test"}

	if onDark.View() == onLight.View() {
		t.Fatal("a light terminal and a dark one drew the same colours")
	}
	// The one code that must not appear on paper: the dark neon.
	if strings.Contains(onLight.View(), "\x1b[38;5;40m") {
		t.Fatalf("the light terminal got the dark neon:\n%q", onLight.View())
	}
	if !strings.Contains(onDark.View(), "\x1b[38;5;40m") {
		t.Fatal("the dark terminal did not get the dark neon")
	}
}

// TestTheDashboardIsTheOnlyWriterOnTheScreen is the two writers defect.
//
// `af watch` draws a full screen dashboard and ALSO built a Progress that
// rewrites a status line to the same stream once a second. Against a real
// environment that put six "elapsed, on this step" lines over the panes in a
// six second run, with the permanent step records between them. The fix is that
// the lifecycle is asked for silence, and silence is load bearing twice: the
// prose is not written, and progressFor is never reached, so the goroutine that
// would rewrite the line is never started either.
func TestTheDashboardIsTheOnlyWriterOnTheScreen(t *testing.T) {
	// Driven through the command's OWN choice rather than through a hand made
	// options value. Testing the mechanism and not the wiring is how a fix
	// passes its test while the command that needed it never asks for it: the
	// first version of this test did exactly that, and removing the request
	// from watchLifecycle went unnoticed.
	emit := func(e *Env) {
		progressEmitter(e, watchLifecycle(e, "a-branch"), redact.New())("api: building")
	}

	// The dashboard path: nothing reaches the screen, and no status line
	// goroutine exists to reach it later.
	var screen bytes.Buffer
	drawn := &Env{
		Out:   &Output{Out: &screen, TTY: true, Width: 100},
		Clock: clock.NewFake(time.Unix(0, 0)),
	}
	emit(drawn)
	if screen.Len() != 0 {
		t.Fatalf("the run wrote %q over the dashboard", screen.String())
	}
	if drawn.Progress != nil {
		t.Fatal("a status line was built under the dashboard, and it rewrites itself once a second")
	}

	// The falsification arm. Without the silence the same call DOES write,
	// which is what proves the assertion above is measuring the switch rather
	// than measuring a path that never writes anything anyway.
	var loud bytes.Buffer
	plain := &Env{
		// Not a terminal, so there is no dashboard to protect and
		// watchLifecycle asks for the ordinary prose. This is the same call
		// with the same arguments; only the terminal differs.
		Out:   &Output{Out: &loud, TTY: false, Width: 100},
		Clock: clock.NewFake(time.Unix(0, 0)),
	}
	emit(plain)
	// The ordinary path DOES build a status line, and that is the other half of
	// what is being asserted: a real goroutine is rewriting a real stream once
	// a second. It has to be stopped here or goleak fails the package, which is
	// itself the clearest statement of what the dashboard path avoids.
	if plain.Progress == nil {
		t.Fatal("the ordinary path built no status line, so the silent arm proves nothing")
	}
	defer plain.Progress.Close()
	if loud.Len() == 0 {
		t.Fatal("a run that was not silenced wrote nothing, so the silent arm proves nothing")
	}
	if !strings.Contains(loud.String(), "api: building") {
		t.Fatalf("the ordinary path lost the progress line: %q", loud.String())
	}
}

// TestTheVerdictSurvivesTheSilence is the other half, and it is the failure
// mode of the fix rather than of the bug: silencing too much would trade a
// corrupted dashboard for a run that never says what it found. The verdict goes
// through e.Out, which the silence does not touch, after the program has exited
// and given the terminal back.
func TestTheVerdictSurvivesTheSilence(t *testing.T) {
	var out bytes.Buffer
	e := &Env{Out: &Output{Out: &out, TTY: true, Width: 100}, Clock: clock.NewFake(time.Unix(0, 0))}
	progressEmitter(e, watchLifecycle(e, "a-branch"), redact.New())("api: building")

	// The Outcome is an anonymous struct on WorkflowResult, so the value is
	// built through the field rather than through a named type.
	result := env.WorkflowResult{Workflow: "read-the-spend"}
	result.Outcome.Verdict = "pass"
	printWatchSummary(e, &env.TestReport{
		Results: []env.WorkflowResult{result},
		Passed:  1,
	})
	if !strings.Contains(out.String(), "read-the-spend") {
		t.Fatalf("the verdict did not reach the terminal:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 passed") {
		t.Fatalf("the tally did not reach the terminal:\n%s", out.String())
	}
	// And the run's own prose still did not.
	if strings.Contains(out.String(), "api: building") {
		t.Fatalf("the silenced progress reached the terminal after all:\n%s", out.String())
	}
}

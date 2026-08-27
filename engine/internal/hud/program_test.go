package hud

// Internal, because the Bubble Tea adapter is deliberately unexported: the
// state and the rendering are the public surface, and a terminal program is
// not something another package should be able to half-drive.

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// ev builds an event. Defined here rather than shared with the external test
// package, because this file is inside the package and the two cannot see each
// other's helpers.
func ev(seq uint64, t events.Type, data map[string]any) events.Event {
	return events.Event{
		Env: "env1", Seq: seq, Type: t, Level: events.LevelInfo,
		TS: time.Date(2026, 1, 1, 0, 0, int(seq), 0, time.UTC), Data: data,
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newTea() teaModel {
	return teaModel{m: New("env1", 120, 30), prog: &Program{events: make(chan events.Event, 4)}}
}

func TestTabMovesFocusForwardAndShiftTabBack(t *testing.T) {
	m := newTea()
	if m.focus != PaneServices {
		t.Fatalf("focus should start on services, got %v", m.focus)
	}

	next, _ := m.Update(key("tab"))
	if got := next.(teaModel).focus; got != PaneNetwork {
		t.Errorf("tab should move to network, got %v", got)
	}
	back, _ := next.(teaModel).Update(key("shift+tab"))
	if got := back.(teaModel).focus; got != PaneServices {
		t.Errorf("shift+tab should move back, got %v", got)
	}
}

// Changing pane resets the scroll, because carrying one pane's offset into
// another shows the new pane from a position that means nothing in it.
func TestChangingPaneResetsTheScroll(t *testing.T) {
	m := newTea()
	scrolled, _ := m.Update(key("down"))
	scrolled, _ = scrolled.(teaModel).Update(key("down"))
	if scrolled.(teaModel).offset != 2 {
		t.Fatalf("offset should be 2, got %d", scrolled.(teaModel).offset)
	}

	moved, _ := scrolled.(teaModel).Update(key("tab"))
	if got := moved.(teaModel).offset; got != 0 {
		t.Errorf("changing pane should reset the offset, got %d", got)
	}
}

func TestScrollingUpStopsAtTheTop(t *testing.T) {
	m := newTea()
	for i := 0; i < 5; i++ {
		next, _ := m.Update(key("up"))
		m = next.(teaModel)
	}
	if m.offset != 0 {
		t.Errorf("offset must not go negative, got %d", m.offset)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c", "esc"} {
		_, cmd := newTea().Update(key(k))
		if cmd == nil {
			t.Errorf("%q should quit", k)
			continue
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q should return a quit, got %T", k, cmd())
		}
	}
}

// People drag terminal edges. The frame is a pure function of the size, so a
// resize is a number to update rather than state to invalidate.
func TestResizeChangesTheFrame(t *testing.T) {
	m := newTea()
	before := m.View()

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	rm := resized.(teaModel)
	if rm.m.Width != 200 || rm.m.Height != 60 {
		t.Fatalf("size not applied: %dx%d", rm.m.Width, rm.m.Height)
	}
	if rm.View() == before {
		t.Error("the frame should be redrawn at the new size")
	}
	for _, line := range strings.Split(rm.View(), "\n") {
		if len([]rune(line)) > 200 {
			t.Errorf("a line escaped the resized terminal: %q", line)
		}
	}
}

// A resize to something absurd must not panic, because a terminal being
// dragged passes through every width on the way.
func TestResizingToAbsurdSizesDoesNotPanic(t *testing.T) {
	m := newTea()
	for _, size := range [][2]int{{0, 0}, {1, 1}, {5, 200}, {400, 2}, {-1, -1}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if next.(teaModel).View() == "" {
			t.Errorf("%dx%d rendered nothing", size[0], size[1])
		}
	}
}

func TestAnEventIsAppliedAndTheProgramAsksForTheNext(t *testing.T) {
	m := newTea()
	next, cmd := m.Update(eventMsg(ev(1, "service.started", map[string]any{"service": "web", "state": "running"})))
	if len(next.(teaModel).m.Services()) != 1 {
		t.Error("the event should have reached the model")
	}
	if cmd == nil {
		t.Error("the program must ask for the next event, or it consumes exactly one")
	}
}

// The contract the whole type exists for. A watcher that can apply
// backpressure to the thing it watches will do so exactly when that thing is
// busiest.
func TestSendNeverBlocksAndCountsWhatItDrops(t *testing.T) {
	p := &Program{events: make(chan events.Event, 2)}

	if !p.Send(ev(1, "env.ready", nil)) || !p.Send(ev(2, "env.ready", nil)) {
		t.Fatal("the first two should fit")
	}
	// The queue is full. This must return rather than block; the test hanging
	// here is the failure.
	if p.Send(ev(3, "env.ready", nil)) {
		t.Error("a full queue should report the drop")
	}
	if p.Dropped() != 1 {
		t.Errorf("Dropped = %d, want 1", p.Dropped())
	}

	// Draining lets it accept again, so a burst does not disable the HUD.
	<-p.events
	if !p.Send(ev(4, "env.ready", nil)) {
		t.Error("after draining, sending should succeed again")
	}
}

func TestSendIsSafeFromManyGoroutines(t *testing.T) {
	p := &Program{events: make(chan events.Event, 8)}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			for j := 0; j < 100; j++ {
				p.Send(ev(uint64(n*100+j+1), "env.ready", nil))
			}
			done <- struct{}{}
		}(i)
	}
	// Drain concurrently so the senders are not all just dropping.
	go func() {
		for range p.events {
		}
	}()
	for i := 0; i < 8; i++ {
		<-done
	}
}

// A closed queue ends the program rather than spinning on a closed channel.
func TestAClosedQueueQuits(t *testing.T) {
	p := &Program{events: make(chan events.Event)}
	close(p.events)
	if _, ok := waitFor(p)().(tea.QuitMsg); !ok {
		t.Error("a closed queue should quit the program")
	}
}

// The adapters exist so that attaching the HUD is one line. They are checked
// here because nothing in the engine attaches them yet, and a constructor with
// no caller and no test is indistinguishable from one that does not compile.
func TestSinkForwardsToTheProgramWithoutBlocking(t *testing.T) {
	p := &Program{events: make(chan events.Event, 1)}
	s := Sink(p)
	if s.Name() != "hud" {
		t.Errorf("Name = %q", s.Name())
	}

	// One fits, the second is dropped, and neither returns an error: the bus
	// counts its own drops and the status line shows ours.
	for i := 0; i < 2; i++ {
		if err := s.Deliver(context.Background(), ev(uint64(i+1), "env.ready", nil)); err != nil {
			t.Fatalf("Deliver must not error: %v", err)
		}
	}
	if p.Dropped() != 1 {
		t.Errorf("the overflow should be counted, got %d", p.Dropped())
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPlainSinkWritesAndSummarisesOnClose(t *testing.T) {
	var b strings.Builder
	pl := NewPlain(&b, "env1")
	s := PlainSink(pl)

	if err := s.Deliver(context.Background(), ev(1, events.MaskProgress, map[string]any{"percent": 10})); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Errorf("progress should be folded, got %q", b.String())
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(b.String(), "not shown individually") {
		t.Errorf("closing should print the summary, got %q", b.String())
	}
}

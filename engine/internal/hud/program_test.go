package hud

// Internal, because the Bubble Tea adapter is deliberately unexported: the
// state and the rendering are the public surface, and a terminal program is
// not something another package should be able to half-drive.

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// TestMain checks the whole package for leaked goroutines once every test has
// finished. Verifying inside one test would see the goroutines other tests are
// still winding down and blame them on this one.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

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
	return teaModel{m: New("env1", 120, 30), prog: newProgram(4)}
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
	p := newProgram(2)

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
	p := newProgram(8)

	var senders sync.WaitGroup
	senders.Add(8)
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer senders.Done()
			for j := 0; j < 100; j++ {
				p.Send(ev(uint64(n*100+j+1), events.EnvReady, nil))
			}
		}(i)
	}

	// Drain concurrently so the senders are not all just dropping, and stop on
	// the same signal the real consumer uses. The first version of this ranged
	// over the channel and nothing ever closed it, so the drainer outlived the
	// test; goleak found that the day the package started checking.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-p.events:
			case <-p.done:
				return
			}
		}
	}()

	senders.Wait()
	p.Close()
	<-drained
}

// Close ends the program, and everything already queued is drawn first.
//
// The draining half is the part worth pinning. A run that finishes fast has
// its last few events sitting in the queue at the moment the bus closes the
// sink, and quitting on the signal alone would drop exactly the events that
// say how the run ended.
func TestCloseQuitsAfterDrainingTheQueue(t *testing.T) {
	p := newProgram(4)
	p.Send(ev(1, events.EnvCreating, nil))
	p.Send(ev(2, events.EnvReady, nil))
	p.Close()

	for want := uint64(1); want <= 2; want++ {
		msg, ok := waitFor(p)().(eventMsg)
		if !ok {
			t.Fatalf("event %d should still be delivered after Close", want)
		}
		if got := events.Event(msg).Seq; got != want {
			t.Fatalf("delivered seq %d, want %d", got, want)
		}
	}
	if _, ok := waitFor(p)().(tea.QuitMsg); !ok {
		t.Error("a drained queue on a closed program should quit")
	}
}

// Close is idempotent, and a send after it is a counted drop rather than a
// panic. Both matter because two callers legitimately close a program: the bus
// when it shuts its sinks down, and the command when the run failed before a
// bus existed. Closing the event channel made the second call fatal and made
// any engine goroutine still emitting fatal too.
func TestCloseIsIdempotentAndSendAfterCloseIsADrop(t *testing.T) {
	p := newProgram(4)
	p.Close()
	p.Close()

	if p.Send(ev(1, events.EnvReady, nil)) {
		t.Error("Send reported delivery on a closed program")
	}
	if p.Dropped() != 1 {
		t.Errorf("the send after close should be counted, got %d", p.Dropped())
	}
	if _, ok := waitFor(p)().(tea.QuitMsg); !ok {
		t.Error("a closed program should quit")
	}
}

// The adapters exist so that attaching the HUD is one line. af up --hud is the
// caller; this checks the contract the bus relies on, which is that Deliver
// never blocks and never errors however far behind the display has fallen.
func TestSinkForwardsToTheProgramWithoutBlocking(t *testing.T) {
	p := newProgram(1)
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

// TestCloseIsIdempotent pins the contract two real callers depend on: the bus
// closes the sink at the end of a run, and a command closes the program when
// the run failed before a bus existed. Before this, the second call closed an
// already closed channel and took the process down with it.
func TestCloseIsIdempotent(t *testing.T) {
	p := NewProgram(New("env", 80, 24), strings.NewReader(""), io.Discard)
	p.Close()
	p.Close()
	p.Close()

	if p.Send(events.Event{Env: "env"}) {
		t.Fatal("Send reported success on a closed program")
	}
}

// Run leaves nothing behind. The cancellation watcher used to receive on
// ctx.Done() alone, and a context.Background has a nil Done channel, so every
// dashboard that ended normally left a goroutine blocked forever. A command
// line process exits and hides that; a test binary and a long lived host do
// not.
func TestRunDoesNotLeakItsCancellationWatcher(t *testing.T) {
	p := NewProgram(New("env1", 80, 24), strings.NewReader(""), io.Discard)
	done := make(chan error, 1)
	go func() { done <- p.Run(context.Background()) }()

	p.Close()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

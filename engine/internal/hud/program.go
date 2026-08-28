package hud

import (
	"context"
	"io"
	"sync"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// queueDepth is how many events the program will hold before it starts
// dropping them.
//
// The number is small on purpose. A deep queue does not stop events being lost,
// it delays the moment you find out and makes the display lag reality while it
// drains, which is worse than a counter that says twelve events were skipped.
const queueDepth = 256

// Program runs the dashboard on a terminal.
//
// Send is safe to call from any goroutine and NEVER BLOCKS. That is the whole
// contract: the HUD is watching the engine, and a watcher that can apply
// backpressure to the thing it watches will do so exactly when the engine is
// busiest, which is when the display matters most and when stalling it costs
// most. When the queue is full the event is counted and discarded, and the
// count is on the status line.
type Program struct {
	tea     *tea.Program
	events  chan events.Event
	dropped atomic.Int64
	// done is closed instead of the event channel, and that is deliberate.
	// Closing the channel a producer writes to means every Send after the
	// close is a panic on a send to a closed channel, not a dropped event, and
	// the producers here are the engine's own goroutines. Signalling on a
	// second channel makes Send total: it always either delivers or counts a
	// drop, whatever the display is doing.
	done chan struct{}
	// closeOnce makes Close idempotent. Two people legitimately close this:
	// the bus, when it shuts its sinks down at the end of a run, and the
	// caller, when the run failed before a bus ever existed and nothing else
	// is going to stop the display.
	closeOnce sync.Once
}

// eventMsg carries one event into the Bubble Tea update loop.
type eventMsg events.Event

// dropMsg tells the model how many events were discarded at the door, so the
// status line can add them to the ones the model dropped for being out of
// sequence. Both are losses and a reader does not care which mechanism lost
// them.
type dropMsg int64

// teaModel adapts Model to Bubble Tea. It is separate from Model so that the
// state and the rendering stay usable, and testable, without a terminal.
type teaModel struct {
	m     *Model
	focus Pane
	// offset scrolls the focused pane. Bounded when it is applied rather than
	// when it is set, because the number of lines available is a property of
	// the frame being drawn.
	offset int
	prog   *Program
}

func (t teaModel) Init() tea.Cmd { return waitFor(t.prog) }

// waitFor blocks on the queue in Bubble Tea's own goroutine and hands the next
// event back as a message. Re-issued after every event, which is how a Bubble
// Tea program consumes a channel without holding the update loop.
//
// Queued events win over the quit signal, in the first select, so that closing
// the program still shows what had already arrived. Without that a run that
// ends quickly draws a dashboard of everything except its last few events.
func waitFor(p *Program) tea.Cmd {
	return func() tea.Msg {
		select {
		case e := <-p.events:
			return eventMsg(e)
		default:
		}
		select {
		case e := <-p.events:
			return eventMsg(e)
		case <-p.done:
			return tea.Quit()
		}
	}
}

func (t teaModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// A resize mid-render is the ordinary case rather than an edge case:
		// people drag terminal edges. The frame is a pure function of the size,
		// so there is nothing to invalidate, only a number to update.
		t.m.Width = msg.Width
		t.m.Height = msg.Height
		return t, nil

	case eventMsg:
		t.m.Apply(events.Event(msg))
		return t, waitFor(t.prog)

	case dropMsg:
		return t, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return t, tea.Quit
		case "tab", "right", "l":
			t.focus = t.focus.Next()
			t.offset = 0
		case "shift+tab", "left", "h":
			t.focus = t.focus.Prev()
			t.offset = 0
		case "down", "j":
			t.offset++
		case "up", "k":
			if t.offset > 0 {
				t.offset--
			}
		case "home", "g":
			t.offset = 0
		}
		return t, nil
	}
	return t, nil
}

func (t teaModel) View() string { return t.m.Render(t.focus) }

// NewProgram builds a dashboard bound to a terminal.
//
// output is where frames are written and input is where keys are read, both
// injected rather than assumed, so a test can drive the program without a
// terminal and so a caller that has already decided stdout is not a terminal
// can use Plain instead.
func NewProgram(m *Model, input io.Reader, output io.Writer) *Program {
	p := newProgram(queueDepth)
	p.tea = tea.NewProgram(
		teaModel{m: m, prog: p},
		tea.WithInput(input),
		tea.WithOutput(output),
		// The alternate screen leaves the scrollback intact, so quitting the
		// HUD gives back the terminal the person had before it started.
		tea.WithAltScreen(),
	)
	return p
}

// newProgram builds the queue and the quit signal together.
//
// Every Program comes through here, tests included. A struct literal elsewhere
// could leave done nil, and a nil done channel makes Close panic and Send
// block forever, which is exactly the pair of failures this type exists to
// prevent.
func newProgram(depth int) *Program {
	return &Program{
		events: make(chan events.Event, depth),
		done:   make(chan struct{}),
	}
}

// Send offers an event to the dashboard and never blocks.
//
// Returns false when the event was dropped, which callers are free to ignore;
// the count is reported on the status line either way.
func (p *Program) Send(e events.Event) bool {
	select {
	case <-p.done:
		// The display has stopped. Counted as a drop rather than ignored,
		// because an engine that kept emitting after the dashboard went away
		// is worth seeing in the number.
		p.dropped.Add(1)
		return false
	default:
	}
	select {
	case p.events <- e:
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

// Dropped reports how many events were discarded because the queue was full.
func (p *Program) Dropped() int64 { return p.dropped.Load() }

// Run draws until the user quits or the context is cancelled.
func (p *Program) Run(ctx context.Context) error {
	// The watcher has to be able to stop for a reason other than cancellation,
	// or it outlives every run that ended normally. A context.Background has a
	// nil Done channel, so a bare receive on it blocks forever and leaks a
	// goroutine per dashboard. goleak found this the first time a test ran a
	// program to completion.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.tea.Quit()
		case <-stop:
		}
	}()
	_, err := p.tea.Run()
	close(stop)
	return err
}

// Close stops accepting events. The program quits once the queue drains.
//
// Safe to call more than once and from more than one goroutine.
func (p *Program) Close() { p.closeOnce.Do(func() { close(p.done) }) }

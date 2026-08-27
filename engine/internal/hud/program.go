package hud

import (
	"context"
	"io"
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
func waitFor(p *Program) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-p.events
		if !ok {
			return tea.Quit()
		}
		return eventMsg(e)
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
	p := &Program{events: make(chan events.Event, queueDepth)}
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

// Send offers an event to the dashboard and never blocks.
//
// Returns false when the event was dropped, which callers are free to ignore;
// the count is reported on the status line either way.
func (p *Program) Send(e events.Event) bool {
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
	go func() {
		<-ctx.Done()
		p.tea.Quit()
	}()
	_, err := p.tea.Run()
	return err
}

// Close stops accepting events. The program quits once the queue drains.
func (p *Program) Close() { close(p.events) }

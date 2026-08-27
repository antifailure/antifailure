// Package hud holds the terminal dashboard's state.
//
// Deliberately split from the rendering. Everything that can go wrong in a HUD
// goes wrong here rather than in the drawing: events arrive out of order,
// arrive faster than a person can read, carry names too long for the column
// they belong in, or describe an environment nobody asked about. A renderer
// built on top of a model that has already dealt with those is a function from
// state to characters, and that is the part a golden frame test can pin down.
//
// The rule the whole package is built around: THE HUD NEVER BLOCKS THE BUS.
// A dashboard that applies backpressure to the thing it is watching does not
// slow down a display, it slows down the product, and it does so exactly when
// the product is busiest and the display matters most. So when events arrive
// faster than they can be shown, they are counted rather than queued.
package hud

import (
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// ReorderWindow is how many out-of-sequence events are held before the model
// gives up waiting for the gap to fill.
//
// Small on purpose. Events carry a per environment sequence, so a gap means
// either a message still in flight or one that was dropped, and those need
// opposite responses: wait a moment, or carry on and say so. Holding a large
// window turns a dropped event into a HUD that silently stops updating, which
// is the worse failure, because a stalled dashboard looks like a stalled
// system.
const ReorderWindow = 16

// TailLines is how much of the log tail is kept. Bounded because a HUD that
// remembers everything is a memory leak with a user interface.
const TailLines = 500

// Model is the dashboard's state.
//
// A Model is not safe for concurrent use. It is driven from one goroutine that
// owns it, which is what lets the counters be plain integers rather than atomic
// ones, and what keeps Apply cheap enough to call on every event.
type Model struct {
	// Env is the environment being watched, or empty for all of them.
	Env string

	// Width and Height are the terminal size in cells.
	Width, Height int

	services map[string]*Service
	// order preserves the order services were first seen, so the pane does not
	// reshuffle itself every time a map is ranged over. A dashboard whose rows
	// move between frames is unreadable even when every row is correct.
	order []string

	network  NetworkPane
	database DatabasePane
	agents   map[string]*Agent
	errors   []events.Event
	tail     []events.Event

	// counts is every event seen, by type, including the ones sampled away.
	// The pane shows a rate rather than a list when the rate is high, and this
	// is where the number comes from.
	counts map[events.Type]int

	// dropped counts events discarded because their sequence had already
	// passed. Shown rather than hidden: a HUD that quietly drops is a HUD that
	// lies by omission.
	dropped int

	// out of order handling, per environment.
	nextSeq map[string]uint64
	held    map[string][]events.Event

	started time.Time
	last    time.Time
}

// Service is one row of the services pane.
type Service struct {
	Name   string
	Kind   string
	State  string
	URL    string
	Ready  bool
	Detail string
}

// NetworkPane summarises what the egress proxy decided.
type NetworkPane struct {
	Allowed    int
	Denied     int
	Mocked     int
	Recorded   int
	LastDenied string
}

// DatabasePane tracks the golden and masking progress.
type DatabasePane struct {
	Phase    string
	Percent  int
	Version  string
	Verified bool
	Findings int
}

// Agent is one row of the agents pane.
type Agent struct {
	Name   string
	State  string
	Passed int
	Failed int
	Detail string
}

// New returns an empty model sized for a terminal.
func New(env string, width, height int) *Model {
	return &Model{
		Env:      env,
		Width:    width,
		Height:   height,
		services: map[string]*Service{},
		agents:   map[string]*Agent{},
		counts:   map[events.Type]int{},
		nextSeq:  map[string]uint64{},
		held:     map[string][]events.Event{},
	}
}

// Apply feeds one event into the model, releasing any held events that the new
// one unblocks.
//
// It never blocks and never returns an error. There is no useful thing a
// dashboard can do with a malformed event except count it and carry on, and
// the alternative, refusing to draw, hides the events that were fine.
func (m *Model) Apply(e events.Event) {
	for _, ready := range m.sequence(e) {
		m.applyInOrder(ready)
	}
}

// sequence returns the events that are now safe to show, in order.
//
// Events with no sequence number at all are passed straight through: a zero Seq
// means the producer does not number its events, and refusing to display those
// would be choosing to show nothing.
func (m *Model) sequence(e events.Event) []events.Event {
	if e.Seq == 0 {
		return []events.Event{e}
	}

	key := e.Env
	want := m.nextSeq[key]
	if want == 0 {
		// First event for this environment sets the expectation. Starting from
		// whatever arrives first rather than from 1 is what lets the HUD attach
		// to an environment that is already running.
		want = e.Seq
		m.nextSeq[key] = want
	}

	switch {
	case e.Seq < want:
		// Already past. Either a duplicate or a straggler that arrived after
		// the window gave up on it.
		m.dropped++
		return nil

	case e.Seq > want:
		m.held[key] = append(m.held[key], e)
		if len(m.held[key]) <= ReorderWindow {
			return nil
		}
		// The window is full, so the gap is not going to fill. Give up on the
		// missing sequence, count it, and release what has been waiting.
		m.dropped++
		m.nextSeq[key] = m.lowestHeld(key)
		return m.drain(key)
	}

	m.nextSeq[key] = e.Seq + 1
	return append([]events.Event{e}, m.drain(key)...)
}

// lowestHeld is the smallest sequence still waiting, which is where the model
// resumes after abandoning a gap.
func (m *Model) lowestHeld(key string) uint64 {
	lowest := ^uint64(0)
	for _, h := range m.held[key] {
		if h.Seq < lowest {
			lowest = h.Seq
		}
	}
	return lowest
}

// drain releases held events that are now contiguous.
func (m *Model) drain(key string) []events.Event {
	held := m.held[key]
	if len(held) == 0 {
		return nil
	}
	sort.Slice(held, func(i, j int) bool { return held[i].Seq < held[j].Seq })

	var out []events.Event
	for len(held) > 0 && held[0].Seq <= m.nextSeq[key] {
		if held[0].Seq == m.nextSeq[key] {
			out = append(out, held[0])
			m.nextSeq[key] = held[0].Seq + 1
		} else {
			// A duplicate of something already shown.
			m.dropped++
		}
		held = held[1:]
	}
	m.held[key] = held
	return out
}

// Dropped reports how many events were discarded, which the status line shows.
func (m *Model) Dropped() int { return m.dropped }

// Count reports how many events of a type have been seen, including any the
// panes summarised rather than listed.
func (m *Model) Count(t events.Type) int { return m.counts[t] }

// Services returns the service rows in the order they were first seen.
func (m *Model) Services() []Service {
	out := make([]Service, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, *m.services[name])
	}
	return out
}

// Network returns the egress summary.
func (m *Model) Network() NetworkPane { return m.network }

// Database returns the golden and masking summary.
func (m *Model) Database() DatabasePane { return m.database }

// Agents returns the agent rows, sorted by name so the pane is stable.
func (m *Model) Agents() []Agent {
	out := make([]Agent, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Errors returns every error level event, newest last.
func (m *Model) Errors() []events.Event { return m.errors }

// Tail returns the most recent events, oldest first, bounded by TailLines.
func (m *Model) Tail() []events.Event { return m.tail }

// Elapsed is how long the model has been watching, which the status line shows.
func (m *Model) Elapsed() time.Duration {
	if m.started.IsZero() {
		return 0
	}
	return m.last.Sub(m.started)
}

// applyInOrder folds one in-sequence event into the panes.
func (m *Model) applyInOrder(e events.Event) {
	if m.Env != "" && e.Env != "" && e.Env != m.Env {
		// Another environment's event. Counted, because the count is how a
		// person notices they are watching the wrong one, and not displayed.
		m.counts[e.Type]++
		return
	}

	m.counts[e.Type]++
	if m.started.IsZero() && !e.TS.IsZero() {
		m.started = e.TS
	}
	if e.TS.After(m.last) {
		m.last = e.TS
	}

	m.tail = append(m.tail, e)
	if len(m.tail) > TailLines {
		m.tail = m.tail[len(m.tail)-TailLines:]
	}
	if e.Level == events.LevelError {
		m.errors = append(m.errors, e)
	}

	switch {
	case strings.HasPrefix(string(e.Type), "service."), strings.HasPrefix(string(e.Type), "env."):
		m.applyService(e)
	case strings.HasPrefix(string(e.Type), "egress."), strings.HasPrefix(string(e.Type), "net."):
		m.applyNetwork(e)
	case strings.HasPrefix(string(e.Type), "db."), strings.HasPrefix(string(e.Type), "golden."),
		strings.HasPrefix(string(e.Type), "mask."):
		m.applyDatabase(e)
	case strings.HasPrefix(string(e.Type), "agent."):
		m.applyAgent(e)
	}
}

func (m *Model) applyService(e events.Event) {
	name := str(e.Data, "service")
	if name == "" {
		return
	}
	s, ok := m.services[name]
	if !ok {
		s = &Service{Name: name}
		m.services[name] = s
		m.order = append(m.order, name)
	}
	if k := str(e.Data, "kind"); k != "" {
		s.Kind = k
	}
	if u := str(e.Data, "url"); u != "" {
		s.URL = u
	}
	if st := str(e.Data, "state"); st != "" {
		s.State = st
	}
	if d := str(e.Data, "detail"); d != "" {
		s.Detail = d
	}
	if e.Msg != "" && s.Detail == "" {
		s.Detail = e.Msg
	}
	s.Ready = s.State == "running" || s.State == "ready"
}

func (m *Model) applyNetwork(e events.Event) {
	switch str(e.Data, "decision") {
	case "allow":
		m.network.Allowed++
	case "deny":
		m.network.Denied++
		if h := str(e.Data, "host"); h != "" {
			m.network.LastDenied = h
		}
	case "mock":
		m.network.Mocked++
	case "record":
		m.network.Recorded++
	}
}

func (m *Model) applyDatabase(e events.Event) {
	if p := str(e.Data, "phase"); p != "" {
		m.database.Phase = p
	} else {
		m.database.Phase = strings.TrimPrefix(string(e.Type), "mask.")
	}
	if v := str(e.Data, "version"); v != "" {
		m.database.Version = v
	}
	if n, ok := e.Data["percent"]; ok {
		m.database.Percent = toInt(n)
	}
	switch e.Type {
	case events.MaskVerified:
		m.database.Verified = true
	case events.MaskFinding:
		m.database.Findings++
	}
}

func (m *Model) applyAgent(e events.Event) {
	name := str(e.Data, "agent")
	if name == "" {
		return
	}
	a, ok := m.agents[name]
	if !ok {
		a = &Agent{Name: name}
		m.agents[name] = a
	}
	if s := str(e.Data, "state"); s != "" {
		a.State = s
	}
	if n, ok := e.Data["passed"]; ok {
		a.Passed = toInt(n)
	}
	if n, ok := e.Data["failed"]; ok {
		a.Failed = toInt(n)
	}
	if e.Msg != "" {
		a.Detail = e.Msg
	}
}

// str reads a string out of an event payload without panicking on a payload
// that is not the shape we expected. External data is read tolerantly; the
// alternative is a dashboard that crashes on a field somebody added.
func str(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	s, _ := data[key].(string)
	return s
}

// toInt coerces the several ways a number reaches us. JSON round trips make
// every integer a float64, so a type switch on int alone silently reads zero
// for every event that has been through a file or a socket.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	}
	return 0
}

// Truncate shortens a string to width cells, marking the cut with an ellipsis.
//
// Counts runes rather than bytes, because a service name with an accent in it
// would otherwise be cut mid-character and render as a replacement glyph, and
// the column would still be wrong.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

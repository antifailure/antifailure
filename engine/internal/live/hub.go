package live

import "sync"

// Frame is the latest image an agent produced. Ephemeral: the Hub keeps only
// the newest one per agent, and it is never persisted.
type Frame struct {
	Seq int
	W   int
	H   int
	B64 string
	At  string
}

// Step is one thing an agent did, in the sentence a person reads.
type Step struct {
	Seq    int
	Text   string
	URL    string
	Action string
	At     string
}

// Agent is one agent's live state as the Hub has assembled it.
type Agent struct {
	ID       string
	Persona  string
	Workflow string
	Surface  string
	State    string
	Verdict  string
	// Personality is what this agent behaves like, by name, and Traits is the
	// profile under it in a few words. Both empty for a run with no diversity
	// block.
	Personality string
	Traits      string
	// Frame is the latest frame, kept by highest Seq so a late lower-Seq frame
	// cannot replace a newer one. Nil for a terminal surface or before the
	// first frame.
	Frame *Frame
	Steps []Step
	// LastSeq is the highest Seq seen for this agent across steps and frames.
	LastSeq int
}

// Counts is the final tally, set by the done event. Present reports whether the
// run has finished, because a zero tally and no tally look the same otherwise.
type Counts struct {
	Present    bool
	Passed     int
	Failed     int
	Flaky      int
	Blocked    int
	Unverified int
}

// State is an immutable snapshot of the Hub, safe to render without holding the
// lock. Agents are in first-seen order, which is the order a watcher's switcher
// lists them.
type State struct {
	Run    string
	Agents []Agent
	Counts Counts
}

// maxSteps caps the transcript the Hub keeps per agent. A live view shows the
// recent tail, not the whole history, and an unbounded slice on a long run is a
// slow leak nobody would notice until it mattered.
const maxSteps = 500

// Hub collects the runner's events into a renderable State and fans each event
// out to live subscribers. Safe for concurrent use: the socket server writes
// while the renderer and any SSE relays read.
type Hub struct {
	mu     sync.Mutex
	run    string
	order  []string
	agents map[string]*Agent
	counts Counts
	subs   map[int]chan Event
	nextID int
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{agents: map[string]*Agent{}, subs: map[int]chan Event{}}
}

// Publish folds one event into the state and fans it out to subscribers. It
// never blocks on a slow subscriber: a watcher that cannot keep up loses an
// event, never the run.
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	switch ev.T {
	case KindHello:
		h.run = ev.Run
	case KindAgent:
		a := h.ensure(ev.Agent)
		a.Surface = ev.Surface
		a.State = ev.State
		if ev.Persona != "" {
			a.Persona = ev.Persona
		}
		if ev.Workflow != "" {
			a.Workflow = ev.Workflow
		}
		if ev.Verdict != "" {
			a.Verdict = ev.Verdict
		}
		// Sent on every one of an agent's lifecycle events and kept from the
		// first that carried it. A later event with the field absent must not
		// blank the pane's heading, which is what a plain assignment would do
		// the moment anything on the wire stopped repeating it.
		if ev.Personality != "" {
			a.Personality = ev.Personality
		}
		if ev.Traits != "" {
			a.Traits = ev.Traits
		}
	case KindStep:
		a := h.ensure(ev.Agent)
		a.Steps = append(a.Steps, Step{
			Seq: ev.Seq, Text: ev.Text, URL: ev.URL, Action: ev.Action, At: ev.At,
		})
		if len(a.Steps) > maxSteps {
			a.Steps = a.Steps[len(a.Steps)-maxSteps:]
		}
		if ev.Seq > a.LastSeq {
			a.LastSeq = ev.Seq
		}
	case KindFrame:
		a := h.ensure(ev.Agent)
		// A frame replaces the current one only when it is at least as new. A
		// stream can deliver a stale frame after a fresh one under load, and
		// showing the older image would be a visible regression on the pane.
		if a.Frame == nil || ev.Seq >= a.Frame.Seq {
			a.Frame = &Frame{Seq: ev.Seq, W: ev.W, H: ev.H, B64: ev.B64, At: ev.At}
		}
		if ev.Seq > a.LastSeq {
			a.LastSeq = ev.Seq
		}
	case KindDone:
		h.counts = Counts{
			Present: true,
			Passed:  ev.Passed, Failed: ev.Failed, Flaky: ev.Flaky,
			Blocked: ev.Blocked, Unverified: ev.Unverified,
		}
	}
	subs := make([]chan Event, 0, len(h.subs))
	for _, ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Full. The subscriber is behind; it will catch the state up from
			// the next Snapshot rather than block the producer here.
		}
	}
}

// ensure returns the agent by id, creating it in first-seen order if new. The
// caller holds the lock.
func (h *Hub) ensure(id string) *Agent {
	a, ok := h.agents[id]
	if !ok {
		a = &Agent{ID: id}
		h.agents[id] = a
		h.order = append(h.order, id)
	}
	return a
}

// Snapshot copies the current state. The copy is deep enough that the caller
// can read it after the lock is released without racing a Publish: the Agent
// values and their Steps and Frame are copied, not aliased.
func (h *Hub) Snapshot() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	agents := make([]Agent, 0, len(h.order))
	for _, id := range h.order {
		a := h.agents[id]
		copyA := *a
		copyA.Steps = append([]Step(nil), a.Steps...)
		if a.Frame != nil {
			f := *a.Frame
			copyA.Frame = &f
		}
		agents = append(agents, copyA)
	}
	return State{Run: h.run, Agents: agents, Counts: h.counts}
}

// Subscribe registers a channel that receives every subsequent event. The
// returned cancel removes it. The channel is buffered and Publish drops rather
// than blocks, so a subscriber must reconcile from Snapshot on start and treat
// the stream as a best-effort tail.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	ch := make(chan Event, 256)
	h.subs[id] = ch
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
	}
}

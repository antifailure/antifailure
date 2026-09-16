// Package live carries a run's frames and events out of the runner while it is
// still running, so a person can watch it happen.
//
// The runner is one job in and one document out (see runner/src/main.ts): the
// right contract for a verdict, the wrong one for watching, because the
// document is written only once the run is over. This package is the second,
// optional channel. The runner connects to a local socket the engine is
// listening on and writes newline delimited JSON events; the Hub collects them;
// `af watch` renders them in the terminal and the console reads them over a
// signed edge relay.
//
// One boundary is load bearing and it is the reason frames live here and
// nowhere else. A frame carries base64 image bytes, and it is EPHEMERAL: it
// exists to be rendered live and then forgotten. It reaches the terminal and
// the console straight from the runner edge and is never written to the control
// plane. The control plane holds counts, verdicts and a reference to the
// durable webm the browser already records, never a body. Nothing in this
// package hands a frame to controlplane.Client, and a test in this package
// fails if a frame's bytes ever reach the control-plane event shape.
package live

import (
	"bytes"
	"encoding/json"
)

// Protocol is the wire version the runner and the engine agree on. Bumped when
// the event shape changes, so a reader can refuse a stream it cannot render
// rather than mis-render it.
const Protocol = 1

// Event is one thing that happened, in the flat shape the wire carries. One
// struct rather than a discriminated union of five, because the only consumer
// that decodes it is this package and a flat struct decodes an NDJSON line with
// no per-type dispatch. T names which kind it is.
type Event struct {
	T        string `json:"t"`
	Run      string `json:"run,omitempty"`
	At       string `json:"at,omitempty"`
	Protocol int    `json:"protocol,omitempty"`

	// Agent identity and lifecycle (t == "agent"), and the agent a step or
	// frame belongs to.
	Agent    string `json:"agent,omitempty"`
	Surface  string `json:"surface,omitempty"`
	State    string `json:"state,omitempty"`
	Persona  string `json:"persona,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Verdict  string `json:"verdict,omitempty"`

	// Step and frame ordering. One counter per agent covers both, so the
	// latest of either is the highest Seq.
	Seq int `json:"seq,omitempty"`

	// Step (t == "step").
	Text   string `json:"text,omitempty"`
	URL    string `json:"url,omitempty"`
	Action string `json:"action,omitempty"`

	// Frame (t == "frame"). B64 is the ephemeral image; it never leaves the
	// edge for the control plane.
	Mime string `json:"mime,omitempty"`
	W    int    `json:"w,omitempty"`
	H    int    `json:"h,omitempty"`
	B64  string `json:"b64,omitempty"`

	// Done tally (t == "done").
	Passed     int `json:"passed,omitempty"`
	Failed     int `json:"failed,omitempty"`
	Flaky      int `json:"flaky,omitempty"`
	Blocked    int `json:"blocked,omitempty"`
	Unverified int `json:"unverified,omitempty"`
}

// The event kinds, named so a reader is not comparing bare strings.
const (
	KindHello = "hello"
	KindAgent = "agent"
	KindStep  = "step"
	KindFrame = "frame"
	KindDone  = "done"
)

// Decode reads one NDJSON line into an Event. It returns false for a blank or
// malformed line, or one with no kind, rather than an error: a stream is read
// one partial buffer at a time and a half-written trailing line is normal, not
// a fault, so the caller skips what it cannot read and keeps going.
func Decode(line []byte) (Event, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return Event{}, false
	}
	var ev Event
	if err := json.Unmarshal(trimmed, &ev); err != nil {
		return Event{}, false
	}
	if ev.T == "" {
		return Event{}, false
	}
	return ev, true
}

// Encode writes an Event as one NDJSON line, newline included. Used by the
// tests and by any producer that wants to speak the same wire the runner does.
func Encode(ev Event) []byte {
	b, err := json.Marshal(ev)
	if err != nil {
		// The Event shape is all plain scalars; marshalling it cannot fail. If
		// it somehow did, an empty line is skipped by Decode rather than
		// corrupting the stream.
		return []byte("\n")
	}
	return append(b, '\n')
}

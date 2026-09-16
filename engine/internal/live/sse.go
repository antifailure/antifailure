package live

import (
	"encoding/json"
	"io"
)

// WriteSSE writes one event as a Server-Sent Events frame: an `event:` line
// naming the kind and a `data:` line carrying the JSON, terminated by the blank
// line the protocol requires. This is how the console's live gateway relays the
// runner's stream to a browser EventSource, which is the same wire the terminal
// reads, just framed for HTTP.
//
// The frame's bytes ride this path exactly as they ride the socket. What keeps
// the boundary is where this is served FROM: a signed relay at the runner edge,
// never the control-plane store. Nothing here writes to controlplane.Client.
func WriteSSE(w io.Writer, ev Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "event: "); err != nil {
		return err
	}
	if _, err := io.WriteString(w, ev.T); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\ndata: "); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}

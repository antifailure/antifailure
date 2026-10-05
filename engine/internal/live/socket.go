package live

import (
	"bufio"
	"fmt"
	"net"
)

// Server listens on a local endpoint for the runner's live stream. The runner
// connects to the address and writes NDJSON events; each is decoded and
// published to the Hub. Local only, by design: the frames it carries are
// ephemeral and never leave the machine for the control plane.
//
// The endpoint is a unix socket inside a private directory everywhere except
// Windows, where it is a named pipe whose security descriptor admits only the
// user running the engine. Both are reachable only from this machine, need no
// port allocation or firewall reasoning, and are one string the runner hands
// unchanged to Node's net.connect, which speaks both. Address chooses which.
type Server struct {
	ln   net.Listener
	path string
}

// Listen opens the endpoint at addr, which comes from Address. The caller
// passes it to the runner in the job document; the runner connects and
// streams.
//
// A failure is returned at once and names the endpoint. Nothing waits on a
// listener that never came up: the runner treats an unreachable watcher as
// nobody watching, so a silent failure here would read as a live view with no
// frames in it rather than as the error it is.
func Listen(addr string) (*Server, error) {
	ln, err := listen(addr)
	if err != nil {
		return nil, fmt.Errorf("the live view could not listen at %s: %w", addr, err)
	}
	return &Server{ln: ln, path: addr}, nil
}

// Path is the endpoint's address, to hand to the runner.
func (s *Server) Path() string { return s.path }

// Serve accepts connections and feeds their events to the hub until the
// listener is closed. It returns when Close is called. Blocking: run it in a
// goroutine.
func (s *Server) Serve(hub *Hub) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// The listener was closed, which is the ordinary way this ends.
			return
		}
		go handle(conn, hub)
	}
}

// Close stops accepting and removes whatever the endpoint left on disk.
func (s *Server) Close() error {
	err := s.ln.Close()
	release(s.path)
	return err
}

// handle reads NDJSON lines from one connection and publishes each decoded
// event. ReadBytes rather than a bufio.Scanner because a frame line carries a
// base64 image and can be far larger than a scanner's default token, and a
// truncated frame line would be dropped in silence.
func handle(conn net.Conn, hub *Hub) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if ev, ok := Decode(line); ok {
				hub.Publish(ev)
			}
		}
		if err != nil {
			return
		}
	}
}

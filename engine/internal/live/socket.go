package live

import (
	"bufio"
	"net"
	"os"
)

// Server listens on a local unix socket for the runner's live stream. The
// runner connects to the path and writes NDJSON events; each is decoded and
// published to the Hub. Local only, by design: the frames it carries are
// ephemeral and never leave the machine for the control plane.
type Server struct {
	ln   net.Listener
	path string
}

// Listen opens the socket at path. The caller passes the path to the runner in
// the job document; the runner connects and streams. A unix socket rather than
// a port so it is reachable only from this machine and needs no allocation or
// firewall reasoning.
func Listen(path string) (*Server, error) {
	// A stale socket file from a crashed run would refuse the bind. Removing it
	// first is safe: the path is run scoped and nobody else owns it.
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return &Server{ln: ln, path: path}, nil
}

// Path is the socket path, to hand to the runner.
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

// Close stops accepting and removes the socket file.
func (s *Server) Close() error {
	err := s.ln.Close()
	_ = os.Remove(s.path)
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

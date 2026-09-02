package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Every decision has to say how the request arrived.
//
// Via is the field that answers "did the sandbox swap actually happen", and
// before this the explicit CONNECT path was the one path that never set it.
// That is not a corner: it is every HTTPS request from a client that honours
// its proxy variables and reaches a host the policy does not read inside,
// which is most of the traffic an environment makes. So the field that exists
// to tell a proxied call from a transparent one was blank for the majority of
// calls, and a reader could not tell "arrived transparently" from "nobody
// filled this in".
//
// The other three paths set it: serveHTTP says proxy, inspectTLS says inspect,
// and both transparent paths say transparent.
func connectThrough(t *testing.T, s *sidecar, target string) string {
	t.Helper()
	front := httptest.NewServer(s.proxy)
	t.Cleanup(front.Close)

	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	require.NoError(t, err)

	_, err = io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	require.NoError(t, err)
	_ = resp.Body.Close()
	// Closed here rather than in a cleanup, because an allowed tunnel is
	// recorded once it finishes and it does not finish while the client that
	// opened it is still holding it open. Waiting for that record with the
	// connection still up waits for ever.
	_ = conn.Close()
	return resp.Status
}

func TestConnect_ARefusedTunnelSaysHowItArrived(t *testing.T) {
	s := newSidecar(t, &schema.Egress{Default: schema.ModeBlock})

	connectThrough(t, s, "blocked.example.test:443")

	rec := s.waitFor(t, func(r record) bool { return r.Host == "blocked.example.test" })
	require.False(t, rec.Allowed)
	require.Equal(t, "proxy", rec.Via,
		"a CONNECT through the explicit proxy port arrived as a proxy request, and the log has to say so")
	require.True(t, rec.HostOnly,
		"a tunnel the sidecar does not read inside is decided from the host and port alone, which is what host_only records")
}

// The allowed tunnel too, because the refusal path builds the record before
// the tunnel is opened and the two could drift apart.
func TestConnect_AnAllowedTunnelSaysHowItArrived(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = upstream.Close() })
	go func() {
		for {
			c, aErr := upstream.Accept()
			if aErr != nil {
				return
			}
			_ = c.Close()
		}
	}()

	host, port, err := net.SplitHostPort(upstream.Addr().String())
	require.NoError(t, err)

	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: host, Mode: schema.ModeAllow}},
	})

	connectThrough(t, s, net.JoinHostPort(host, port))

	rec := s.waitFor(t, func(r record) bool {
		return r.Host == host && r.Method == http.MethodConnect
	})
	require.True(t, rec.Allowed)
	require.Equal(t, "proxy", rec.Via)
	require.True(t, rec.HostOnly)
}

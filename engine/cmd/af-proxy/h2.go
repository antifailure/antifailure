package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/livekey"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// HTTP/2, which is the protocol gRPC is defined over.
//
// The sidecar terminated TLS and then read HTTP/1.1 out of the connection
// unconditionally, with no ALPN offered at all. Two things followed from that
// and both of them break gRPC outright.
//
// The first is the handshake. gRPC is HTTP/2 and HTTP/2 over TLS is negotiated
// with ALPN, so every gRPC client offers h2 and requires the server to select
// it. A terminator that names no protocol leaves the negotiated protocol
// empty, and grpc-go refuses the connection there with "missing selected ALPN
// property" before a single request is written. The application does not see a
// policy refusal or a network error it can attribute; it sees its own client
// library declining to talk to what it believes is the origin.
//
// The second is the read. Negotiating h2 without changing how the connection
// is read would be worse than not negotiating it, because the client would
// then commit to HTTP/2 and the sidecar would try to parse an HTTP/2 frame
// stream as an HTTP/1.1 request line. So the negotiated protocol decides the
// reader, here, and the two are set in one place.
//
// Everything the sidecar decides for an HTTP/1.1 request is decided here for
// an HTTP/2 one: the host match, the mode, the live credential tripwire, the
// sandbox substitution, the rate limit and the decision record. A protocol
// that reached the network without passing the policy would be a containment
// hole far larger than the gap it closed.
//
// What is NOT the same is the shape of the answer. The HTTP/1.1 paths hold a
// raw connection and write a whole response onto it by hand; an HTTP/2 stream
// is written through an http.ResponseWriter. Rather than a second copy of
// capture, mock and synth for this path, the one implementation writes onto a
// pipe and its response is relayed, so a mode cannot behave differently
// depending on which protocol the application happened to speak.

// h2ALPN is the protocol set for a transport that may use HTTP/2 over TLS.
//
// HTTP/1.1 stays in the set. An upstream that does not offer h2 selects
// http/1.1 during the handshake and the request is forwarded over that, which
// is what should happen: the client's protocol and the origin's are separate
// negotiations and nothing requires them to agree.
func h2ALPN() *http.Protocols {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetHTTP2(true)
	return &p
}

// h2cPriorKnowledge is the protocol set for a transport that speaks HTTP/2 on
// a plain connection.
//
// There is no ALPN on a cleartext connection, so this is prior knowledge: the
// client committed to HTTP/2 by sending the connection preface and the
// upstream is expected to speak it too. HTTP/1.1 is left out on purpose,
// because a silent downgrade here would hand an HTTP/1.1 response to a client
// that has already framed its side as HTTP/2.
func h2cPriorKnowledge() *http.Protocols {
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	return &p
}

// newForwardTransport builds a transport that re-originates a request.
//
// A nil protocol set is the historical behaviour and is deliberate rather
// than incidental: net/http disables HTTP/2 on any transport carrying a custom
// dialer, and this one carries the address guard, so the forwarding half was
// HTTP/1.1 only whatever the client spoke. Terminating h2 and then forwarding
// over HTTP/1.1 would strip the trailers a gRPC status travels in, so the
// protocol has to be asked for explicitly on both halves.
func newForwardTransport(protocols *http.Protocols) *http.Transport {
	return &http.Transport{
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     60 * time.Second,
		// The origin's certificate is verified normally. Reading inside a
		// connection is not a licence to stop checking who is on the other
		// end of it; if anything it makes the check more important, because
		// the client can no longer do it itself.
		TLSHandshakeTimeout: 20 * time.Second,
		Protocols:           protocols,
	}
}

// h2Preface is the first thing a client sends on a cleartext HTTP/2
// connection, before any frame.
const h2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

// looksLikeH2C reports whether a plain connection is about to speak HTTP/2.
//
// Three bytes are peeked before twenty four, and that ordering is the whole
// care in this function. Peeking the full preface up front would block until
// twenty four bytes arrived, and a client that writes its request line and its
// headers in separate packets has sent fewer than that when it stops to draw
// breath. PRI is not the start of any HTTP/1.1 method, so three bytes settle
// it for every request that is not one, and only a connection that really does
// begin with PRI waits for the rest.
func looksLikeH2C(br *bufio.Reader) bool {
	head, err := br.Peek(3)
	if err != nil || string(head) != "PRI" {
		return false
	}
	full, err := br.Peek(len(h2Preface))
	return err == nil && string(full) == h2Preface
}

// serveInspectedH2 serves one terminated connection that negotiated h2.
func (p *proxy) serveInspectedH2(conn net.Conn, sni string) {
	p.serveH2Conn(conn, sni, 443, true, "inspect", p.transportH2, "https")
}

// serveTransparentH2C serves one cleartext connection that opened with the
// HTTP/2 preface.
//
// A gRPC client built with insecure credentials, which is what every local
// emulator's documented setup uses, speaks exactly this. It arrived on the
// transparent port 80 listener, where the HTTP/1.1 reader turned the preface
// into a request whose method was PRI and whose Host header was absent, and
// refused it for carrying no host. The refusal was correct about what it saw
// and said nothing about what had actually happened.
func (p *proxy) serveTransparentH2C(conn net.Conn, br *bufio.Reader) {
	p.serveH2Conn(&prefixedConn{Conn: conn, r: br}, "", 80, false, "transparent", p.transportH2C, "http")
}

// serveH2Conn runs an HTTP/2 server over one connection and decides every
// request on it.
func (p *proxy) serveH2Conn(
	conn net.Conn, sni string, port int, isTLS bool, via string,
	transport *http.Transport, scheme string,
) {
	ln := &oneConnListener{conn: conn, closed: make(chan struct{})}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The authority the client wrote, not the name in the handshake.
			// The two can disagree, and the request is going where the
			// authority says, which is the same reason the HTTP/1.1 reader
			// prefers the Host header over the server name.
			host := sni
			if r.Host != "" {
				host, _ = splitHostPort(r.Host, port)
			}
			p.decideH2(w, r, host, port, isTLS, via, transport, scheme)
		}),
		// A connection that never finishes a request must not hold a
		// goroutine forever. An open gRPC stream is not idle, so a long lived
		// call is unaffected by this.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       90 * time.Second,
		// The end of the connection is reported here rather than by a wrapper
		// around it, and that is the whole reason this hook exists. net/http
		// decides a terminated connection's protocol by asking the connection
		// it was handed for its ConnectionState, and a struct that embeds
		// net.Conn does not carry that method however faithfully it forwards
		// everything else. Wrapping the connection to learn when it closed
		// therefore left net/http unable to see that h2 had been negotiated,
		// so it read the HTTP/2 frame stream as an HTTP/1.1 request line and
		// forwarded the connection preface upstream as a request whose method
		// was PRI. Nothing in the wrapper looked wrong and every gRPC call
		// through the sidecar failed.
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				ln.finished()
			}
		},
	}
	if !isTLS {
		srv.Protocols = h2cPriorKnowledge()
	}
	// Serve returns once the listener refuses a second connection, which it
	// does only after this one is closed, so this call spans the life of the
	// connection exactly as the HTTP/1.1 read loop does.
	_ = srv.Serve(ln)
}

// decideH2 applies the whole policy to one HTTP/2 request.
//
// It is the same sequence as the HTTP/1.1 reader, in the same order, and the
// order is load bearing in the same place: the tripwire runs before the mode
// is acted on, so a credential that can act on production is refused whatever
// the rule says should happen to the host.
func (p *proxy) decideH2(
	w http.ResponseWriter, r *http.Request, host string, port int, isTLS bool, via string,
	transport *http.Transport, scheme string,
) {
	started := time.Now()
	preq := policy.Request{Host: host, Port: port, Method: r.Method, Path: r.URL.Path, TLS: isTLS}
	d := p.engine.Evaluate(preq)

	rec := record{
		Event: "decision", Method: r.Method, Host: host, Port: port, Path: r.URL.Path,
		TLS: isTLS, Mode: string(d.Mode), Rule: d.RuleHost, Reason: d.Reason(),
		Allowed: d.Allowed(), Via: via,
	}
	if found := p.tripwire(r, host); len(found) > 0 {
		rec.Status = http.StatusForbidden
		rec.Allowed = false
		rec.Reason = "This request carries a live credential: " + livekey.Describe(found)
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		relayRaw(w, func(raw io.Writer) {
			writeRawForbidden(raw, refusalForLiveCredential(preq, found))
		})
		return
	}

	switch d.Mode {
	case schema.ModeCapture:
		// Emitted after the answer rather than before it, so a capture this
		// build refuses is logged as the refusal it was, exactly as on the
		// other paths.
		relayRaw(w, func(raw io.Writer) { p.capture(raw, r, preq, d, &rec) })
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		return
	case schema.ModeSynth:
		relayRaw(w, func(raw io.Writer) { p.serveSynth(raw, r, host, &rec) })
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		return
	case schema.ModeMock:
		relayRaw(w, func(raw io.Writer) { p.serveMock(raw, r, host, &rec) })
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		return
	}

	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		relayRaw(w, func(raw io.Writer) { writeRefusalRaw(raw, d, preq) })
		return
	}

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	outbound.URL.Scheme = scheme
	outbound.URL.Host = host
	// TE is hop by hop everywhere else and is the one exception HTTP/2 makes,
	// under RFC 9113 section 8.2.2, for the single value "trailers". Every
	// gRPC client sends it, so it is put back after the hop by hop sweep
	// rather than left out of the sweep, which would let any other value
	// through.
	trailers := strings.EqualFold(strings.TrimSpace(outbound.Header.Get("Te")), "trailers")
	for _, h := range hopByHop {
		outbound.Header.Del(h)
	}
	if trailers {
		outbound.Header.Set("Te", "trailers")
	}
	if d.Mode == schema.ModeSandbox {
		applySandbox(outbound, host, p.credentials[d.Credential])
		rec.Substituted = p.credentials[d.Credential] != ""
	}
	if d.RateLimit != "" {
		if waited := p.limits.wait(d.RuleHost, d.RateLimit); waited > 0 {
			rec.WaitedMs = waited.Milliseconds()
			rec.Limit = describeRate(d.RateLimit)
		}
	}

	resp, err := transport.RoundTrip(outbound)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		http.Error(w, "af-proxy: could not reach "+host+": "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	rec.Status = resp.StatusCode
	rec.Bytes = relayH2Response(w, resp)
	rec.Duration = time.Since(started).String()
	p.emit(rec)
}

// relayH2Response copies an upstream response onto an HTTP/2 stream.
//
// The body is streamed and flushed rather than read into memory. A gRPC call
// can be open for hours and can carry more than fits anywhere, so a bounded
// read here would be a truncation on a path where truncation is silent: the
// stream would simply end early and the client would report a status the
// server never sent.
func relayH2Response(w http.ResponseWriter, resp *http.Response) int64 {
	// A gRPC server that fails before sending a message answers with one
	// headers frame carrying grpc-status and closing the stream. Go's client
	// surfaces those as ordinary response headers, because that is what they
	// are on the wire. Relaying them as headers would end this stream with no
	// trailers at all, and every gRPC client reads a missing trailer as a
	// broken server rather than as the error the server actually sent, so a
	// NotFound would reach the application as an internal protocol failure.
	deferred := http.Header{}
	if resp.Header.Get("Grpc-Status") != "" {
		for _, k := range []string{"Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin"} {
			for _, v := range resp.Header.Values(k) {
				deferred.Add(k, v)
			}
			resp.Header.Del(k)
		}
	}
	for k, vs := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Flushed before the body, because a gRPC client waits for the response
	// headers before it will send anything more on a bidirectional stream.
	// Holding them until the first frame of the body deadlocks a call whose
	// next message depends on the one before it.
	flush(w)

	n := copyFlushing(w, resp.Body)

	for k, vs := range deferred {
		for _, v := range vs {
			w.Header().Add(http.TrailerPrefix+k, v)
		}
	}
	for k, vs := range resp.Trailer {
		for _, v := range vs {
			w.Header().Add(http.TrailerPrefix+k, v)
		}
	}
	return n
}

// copyFlushing copies a body and flushes each piece as it arrives.
//
// io.Copy alone is wrong on this path. The HTTP/2 writer buffers, so a
// streaming response arrives at the application in whatever chunks the buffer
// happened to fill, and a server sending one message a second would be read as
// a server sending nothing for a minute.
func copyFlushing(w http.ResponseWriter, r io.Reader) int64 {
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			total += int64(written)
			flush(w)
			if writeErr != nil {
				return total
			}
		}
		if readErr != nil {
			return total
		}
	}
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// relayRaw runs a handler that writes a whole HTTP/1.1 response and relays
// what it wrote onto an HTTP/2 stream.
//
// Capture, mock, synth and every refusal write onto a raw connection, because
// the two HTTP/1.1 paths hold one and have nothing else to write onto. This
// adapter is what keeps a single implementation of each of those: a second
// copy written against ResponseWriter is a second thing to keep correct, and
// the two would disagree about a provider's shape long before anybody noticed.
//
// The response is piped rather than buffered, so a large mocked body is not
// held in memory, and the writer is joined before this returns, so the decision
// record the handler filled in is safe to read afterwards.
func relayRaw(w http.ResponseWriter, write func(io.Writer)) {
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		write(pw)
		_ = pw.Close()
	}()

	resp, err := http.ReadResponse(bufio.NewReader(pr), nil)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-done
		http.Error(w, "af-proxy: the answer for this request could not be written", http.StatusBadGateway)
		return
	}
	for k, vs := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flush(w)
	copyFlushing(w, resp.Body)
	_ = resp.Body.Close()
	_ = pr.Close()
	<-done
}

// isHopByHop reports whether a header belongs to one connection rather than to
// the message.
//
// It matters more here than on the HTTP/1.1 paths. Connection, Keep-Alive and
// Transfer-Encoding are not merely pointless on an HTTP/2 stream, they are
// forbidden by RFC 9113 section 8.2.2, and a client is entitled to treat one
// as a protocol error and drop the whole connection.
func isHopByHop(header string) bool {
	for _, h := range hopByHop {
		if strings.EqualFold(h, header) {
			return true
		}
	}
	return false
}

// errConnectionFinished ends the one connection server's accept loop.
var errConnectionFinished = errors.New("this connection has been served")

// oneConnListener hands an already accepted connection to an http.Server.
//
// net/http will only run its HTTP/2 server for a connection that arrives
// through a listener, and this connection arrived through a TLS handshake the
// sidecar performed itself. The second Accept blocks until the connection is
// closed rather than returning at once, so Serve outlives the connection it
// was given and the caller can wait on Serve instead of inventing its own
// signal for when the last stream ended.
//
// The connection is handed over exactly as it arrived, with nothing wrapped
// around it. What net/http is given here is a *tls.Conn and it has to stay
// one: the protocol dispatch asserts ConnectionState on it, and both the
// HTTP/2 server it selects on that basis and the older ALPN hook behind it
// read the connection's own type. Serve is told the connection ended through
// the server's ConnState hook instead.
type oneConnListener struct {
	conn net.Conn
	// handed is read and written only by Serve's accept loop, which is one
	// goroutine, so it needs no lock.
	handed bool
	once   sync.Once
	closed chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if !l.handed {
		l.handed = true
		return l.conn, nil
	}
	<-l.closed
	return nil, errConnectionFinished
}

func (l *oneConnListener) Close() error { return nil }

func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// finished releases the accept loop that is waiting for this connection to
// end.
//
// Guarded by a sync.Once because the caller watches two terminal states and
// should not have to know that net/http reports exactly one of them.
func (l *oneConnListener) finished() {
	l.once.Do(func() { close(l.closed) })
}

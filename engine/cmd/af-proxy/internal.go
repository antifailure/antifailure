package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// The environment's own traffic, forwarded rather than decided.
//
// Both functions here answer a request for a name inside the environment that
// arrived on the explicit proxy port. Nothing reaches these that a client with
// working no_proxy handling would have sent here at all, and nothing they
// reach was unreachable before: the resolver already answers an internal name
// with the real container's address, so a direct connection was always
// available. What was not available was making that connection through this
// port, and the result was that a service calling another service worked or
// did not depending on which HTTP library the application used.
//
// They emit a decision line each, like every other path, because a log that
// records only what the policy decided cannot answer "did anything reach the
// events store", and that is the question somebody asks about a twin.

// resolvesInside reports whether a name resolves into the environment's own
// network, and nowhere else.
//
// The shape of a name says it is internal; this says the address is. Every
// address has to be inside, not merely one of them, because a name that
// resolves to an internal address and an external one is a name whose next
// lookup could return either.
//
// A name that fails this is not refused here. It falls through to the policy,
// which decides it like any other host and writes the ordinary decision line,
// so a single label name pointing somewhere real is blocked by the default
// rather than forwarded by an exception.
func (p *proxy) resolvesInside(ctx context.Context, host string) bool {
	lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := p.lookupIP(lookup, host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !p.destinations.isLocal(a) {
			return false
		}
	}
	return true
}

// internalReason is what the decision log says about these.
const internalReason = "This name is inside the environment, so it is not egress and no rule " +
	"was consulted."

// serveInternal forwards a plain request to something inside the environment.
func (p *proxy) serveInternal(
	w http.ResponseWriter, r *http.Request, host string, port int, started time.Time,
) {
	rec := record{
		Event: "decision", Method: r.Method, Host: host, Port: port, Path: r.URL.Path,
		Mode: "allow", Reason: internalReason, Allowed: true, Via: "internal",
	}

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	for _, h := range hopByHop {
		outbound.Header.Del(h)
	}
	// p.transport rather than a fresh one, so the address guard applies here
	// as well: an internal NAME that resolves to something outside the
	// environment is refused by the dialer, which is the property that keeps
	// this from being a way around the policy.
	resp, err := p.transport.RoundTrip(outbound)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		http.Error(w, "af-proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	n, _ := io.Copy(w, resp.Body)

	rec.Status = resp.StatusCode
	rec.Bytes = n
	rec.Duration = time.Since(started).String()
	p.emit(rec)
}

// tunnelInternal opens a tunnel to something inside the environment.
func (p *proxy) tunnelInternal(
	w http.ResponseWriter, host string, port int, started time.Time,
) {
	rec := record{
		Event: "decision", Method: http.MethodConnect, Host: host, Port: port,
		TLS: true, Mode: "allow", Reason: internalReason, Allowed: true, Via: "internal",
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		rec.Error = "the connection cannot be hijacked"
		p.emit(rec)
		http.Error(w, "af-proxy: cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		rec.Error = err.Error()
		p.emit(rec)
		return
	}
	defer func() { _ = client.Close() }()

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		rec.Error = err.Error()
		p.emit(rec)
		return
	}
	upstream, err := p.dialGuarded(context.Background(), "tcp",
		net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		return
	}
	defer func() { _ = upstream.Close() }()

	rec.Status = http.StatusOK
	rec.Bytes = pipe(client, upstream)
	rec.Duration = time.Since(started).String()
	p.emit(rec)
}

package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The byte stream path is for the outbound calls that are not HTTP.
//
// Everything else in this sidecar reads a request. The transparent listener on
// 80 parses one with http.ReadRequest, the listener on 443 either tunnels the
// handshake or terminates it and parses requests inside, and the explicit port
// speaks the proxy protocol. All three are HTTP, and an application that
// speaks AMQP to Azure Service Bus, Kafka to Confluent Cloud, or the MongoDB
// wire protocol to Atlas is speaking none of them.
//
// What happened to those calls before this file existed was not a policy
// decision. The resolver answered every external name with this sidecar's
// address, so the connection arrived here, and nothing was listening on the
// port: on Docker the kernel answered with a reset, and on Kubernetes the
// NetworkPolicy had no rule permitting the port so the packet was dropped and
// the client waited for its own connect timeout. Either way the outcome was
// the same for an allowed host and for a blocked one, which means the
// containment was real and the product did not work.
//
// WHAT THIS PATH CAN AND CANNOT KNOW, because the difference is the whole
// design.
//
// It can know the host, when and only when the client wrote one. A TLS
// ClientHello carries the server name the client asked for, and that is a
// claim the client made about where it wanted to go, which is exactly what the
// transparent TLS path already decides on. Nothing about it is authenticated
// and nothing needs to be: a client that names a host it may not reach is
// refused, and a client that names one it may reach gets a connection to that
// host and no other, because this sidecar dials the name it decided on.
//
// It cannot know anything inside the connection. There is no request, no
// method, no path, and no header. So a rule that names paths or methods cannot
// apply, and the four modes that answer from inside a connection cannot be
// honoured here at all:
//
//   - capture has to understand a message to record one;
//   - mock has to understand a request to choose a fixture for it;
//   - synth has to describe a request to a model to invent a response;
//   - sandbox has to find the credential in the request in order to replace
//     it, and a sandbox rule that forwarded without replacing would send the
//     application's own live credential to the real provider, which is worse
//     than refusing and worse than blocking.
//
// Those four are REFUSED here rather than accepted and quietly downgraded to
// allow. A mode that is accepted and ignored is the defect this repository
// keeps finding in itself, and on this path the ignored version of two of them
// is a credential leaving the environment.
//
// So block and allow are the modes this path honours, every decision it makes
// is recorded as host_only and stream, and the report says how many
// connections were decided that way and to which hosts. A reader must be able
// to tell "allowed after the request was read" from "allowed on a name in a
// handshake and never looked inside again".

// streamPeekTimeout is how long the sidecar waits for the client to say where
// it is going.
//
// Short on purpose. A protocol whose server speaks first, which is MySQL,
// SMTP and the MongoDB handshake in some drivers, will send nothing here ever,
// and the honest answer to that is a refusal the developer reads in seconds
// rather than a connection that hangs for the length of their own timeout.
//
// A var rather than a const so the measurement suite can shorten it. The
// rows that send nothing are the ones that take the whole timeout, and they
// are exactly the rows worth measuring.
var streamPeekTimeout = 10 * time.Second

// serveStream decides one connection on a port that does not carry HTTP.
func (p *proxy) serveStream(proto schema.StreamProtocol) func(net.Conn) {
	return func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		started := time.Now()

		_ = conn.SetReadDeadline(time.Now().Add(streamPeekTimeout))
		br := bufio.NewReader(conn)
		sni, err := peekSNI(br)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			// Refused, and the reason names what was missing rather than
			// what failed. A connection this sidecar cannot attribute to a
			// host is one no rule can apply to, and forwarding it would be a
			// hole exactly the shape of connecting by address and skipping
			// the policy. It is the same reasoning as the transparent TLS
			// listener, reached far more often here because most of these
			// protocols have a cleartext form.
			p.emit(record{
				Event: "decision", Method: http.MethodConnect, Port: proto.Port,
				Mode: string(schema.ModeBlock), Allowed: false, Via: "stream",
				Stream: true, HostOnly: true,
				Reason:   streamRefusal(proto),
				Error:    err.Error(),
				Status:   http.StatusForbidden,
				Duration: time.Since(started).String(),
			})
			return
		}

		// Inside the environment, so not egress, for the same reason as every
		// other path. A datastore declared in the manifest is reached by name
		// and the resolver already sends it to the real container, so nothing
		// normally arrives here; a client that was given this sidecar's
		// address some other way still gets the connection it would have
		// made directly.
		if p.internal.has(sni) && p.resolvesInside(context.Background(), sni) {
			p.streamInternal(conn, br, sni, proto.Port, started)
			return
		}

		preq := policy.Request{
			Host: sni, Port: proto.Port, Method: http.MethodConnect, Path: "/", TLS: true,
		}
		d := p.engine.Evaluate(preq)

		rec := record{
			Event: "decision", Method: http.MethodConnect, Host: sni, Port: proto.Port,
			TLS: true, Mode: string(d.Mode), Rule: d.RuleHost, Reason: d.Reason(),
			Allowed: d.Allowed(), Via: "stream", Stream: true, HostOnly: true,
		}

		// The modes this path cannot honour are refused before the allow
		// check, because three of the four are modes whose whole point is to
		// stop the connection reaching the provider, and treating them as
		// allow because the port is not HTTP would turn a rule that says "do
		// not talk to Stripe" into a connection to Stripe.
		if reason, cannot := streamCannotHonour(d.Mode, proto); cannot {
			rec.Allowed = false
			rec.Mode = string(schema.ModeBlock)
			rec.Reason = reason
			rec.Status = http.StatusForbidden
			rec.Duration = time.Since(started).String()
			p.emit(rec)
			return
		}

		if !d.Allowed() {
			rec.Status = http.StatusForbidden
			rec.Duration = time.Since(started).String()
			p.emit(rec)
			// Closed without a reply, like the transparent TLS refusal. There
			// is nothing this sidecar could write onto the connection that
			// any of these protocols would read as prose, and inventing a
			// frame that looked like the broker's own error would be a lie
			// about who refused.
			return
		}

		// A synthetic CONNECT at / cannot prove that an unseen request meets
		// a path or method rule. Check every rule for the destination, just
		// as the HTTPS path does before choosing whether to inspect TLS.
		if p.engine.InspectsHost(sni, proto.Port) {
			rec.Allowed = false
			rec.Mode = string(schema.ModeBlock)
			rec.Reason = "A rule for this host and port requires request inspection, which a byte stream cannot provide."
			rec.Status = http.StatusForbidden
			rec.Duration = time.Since(started).String()
			p.emit(rec)
			return
		}

		// A listener is shared by every destination on its port. Another
		// host's explicit port must not widen a website-only rule or default
		// allow into a grant for this connection.
		_, namedPort, portErr := net.SplitHostPort(d.RuleHost)
		if portErr != nil || namedPort != strconv.Itoa(proto.Port) {
			rec.Allowed = false
			rec.Mode = string(schema.ModeBlock)
			rec.Reason = "A byte stream requires an allow rule naming this host and port explicitly."
			rec.Status = http.StatusForbidden
			rec.Duration = time.Since(started).String()
			p.emit(rec)
			return
		}

		if d.RateLimit != "" {
			if waited := p.limits.wait(d.RuleHost, d.RateLimit); waited > 0 {
				rec.WaitedMs = waited.Milliseconds()
				rec.Limit = describeRate(d.RateLimit)
			}
		}

		upstream, err := p.dialGuarded(context.Background(), "tcp",
			net.JoinHostPort(sni, strconv.Itoa(proto.Port)))
		if err != nil {
			rec.Error = err.Error()
			rec.Status = http.StatusBadGateway
			rec.Duration = time.Since(started).String()
			p.emit(rec)
			return
		}
		defer func() { _ = upstream.Close() }()

		rec.Status = http.StatusOK
		rec.Bytes = pipeBuffered(conn, br, upstream)
		rec.Duration = time.Since(started).String()
		p.emit(rec)
	}
}

// streamInternal forwards a byte stream to something inside the environment.
func (p *proxy) streamInternal(
	conn net.Conn, br *bufio.Reader, host string, port int, started time.Time,
) {
	rec := record{
		Event: "decision", Method: http.MethodConnect, Host: host, Port: port,
		TLS: true, Mode: "allow", Reason: internalReason, Allowed: true,
		Via: "internal", Stream: true, HostOnly: true,
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
	rec.Bytes = pipeBuffered(conn, br, upstream)
	rec.Duration = time.Since(started).String()
	p.emit(rec)
}

// streamCannotHonour reports whether a mode needs something this path does not
// have, and says so in the words of the rule's author.
//
// Refusing is the point. A mode accepted and ignored is a manifest that says
// one thing and an environment that does another, and for two of these four
// the ignored version sends the application's real credential to the real
// provider.
func streamCannotHonour(mode schema.Mode, proto schema.StreamProtocol) (string, bool) {
	need := ""
	switch mode {
	case schema.ModeCapture:
		need = "capture has to understand a message before it can record one"
	case schema.ModeMock:
		need = "mock has to understand a request before it can choose a fixture for it"
	case schema.ModeSynth:
		need = "synth has to describe a request to a model before it can invent a response"
	case schema.ModeSandbox:
		need = "sandbox has to find the credential in the request before it can replace it, " +
			"and forwarding without replacing would send the application's own credential " +
			"to the real provider"
	default:
		return "", false
	}
	return fmt.Sprintf(
		"This rule is %s, and this is a %s connection on port %d, which Antifailure does not "+
			"read inside: %s. The connection was refused rather than allowed, because a mode "+
			"that cannot be honoured must not quietly become allow. Use block or allow for "+
			"this host, or reach it over HTTP where the request can be read.",
		mode, proto.Name, proto.Port, need), true
}

// streamRefusal explains a connection that named no host.
func streamRefusal(proto schema.StreamProtocol) string {
	base := fmt.Sprintf(
		"This connection arrived on port %d, which Antifailure answers for %s, and it carried "+
			"no server name, so no rule could apply to it and it was refused. ",
		proto.Port, proto.Name)
	if proto.SNI {
		return base + "A TLS connection normally names the host it is for in its handshake. " +
			"A client connecting straight to an address rather than to a name sends no name, " +
			"and there is nothing in the bytes that follow for a rule to match."
	}
	return base + fmt.Sprintf(
		"%s carries no host name in its cleartext form, so a connection to it cannot be "+
			"attributed to a host and cannot be decided. If the broker supports TLS from "+
			"the first byte, connect over TLS so the handshake names the host.", proto.Name)
}

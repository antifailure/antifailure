package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/antifailure/antifailure/engine/internal/livekey"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The transparent listeners are the other half of what the DNS server starts.
//
// A name resolves to this sidecar, so the connection arrives here believing it
// is the origin. The destination is recovered from the protocol rather than
// from the socket: the Host header for HTTP, the server name in the TLS
// handshake for HTTPS. Neither is authenticated, and neither needs to be. A
// client that lies about where it is going only reaches whatever it named, and
// naming a host it is not allowed to reach gets it refused.
//
// What this does not do is see inside TLS. A rule that names paths or methods
// is evaluated on its host alone for an HTTPS request, and the decision log
// records that it was host-only, so the difference is visible rather than
// assumed away. Seeing inside needs a certificate the environment trusts,
// which is the next piece.

// serveTransparentHTTP handles a plain connection that believes it reached the
// origin.
func (p *proxy) serveTransparentHTTP(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	host, port := splitHostPort(req.Host, 80)
	if host == "" {
		writeRawError(conn, http.StatusBadRequest,
			"Antifailure could not tell which host this request was for, because it carried no Host header.")
		return
	}

	started := time.Now()
	preq := policy.Request{Host: host, Port: port, Method: req.Method, Path: req.URL.Path, TLS: false}
	d := p.engine.Evaluate(preq)

	rec := record{
		Event: "decision", Method: req.Method, Host: host, Port: port, Path: req.URL.Path,
		Mode: string(d.Mode), Rule: d.RuleHost, Reason: d.Reason(),
		Allowed: d.Allowed(), Via: "transparent",
	}
	if found := p.tripwire(req, host); len(found) > 0 {
		rec.Status = http.StatusForbidden
		rec.Allowed = false
		rec.Reason = "This request carries a live credential: " + livekey.Describe(found)
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRawForbidden(conn, refusalForLiveCredential(preq, found))
		return
	}

	if d.Mode == schema.ModeCapture {
		rec.Status = http.StatusOK
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		p.capture(conn, req, host)
		return
	}
	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRefusalRaw(conn, d, preq)
		return
	}

	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 30*time.Second)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRawError(conn, http.StatusBadGateway, "Antifailure could not reach "+host+": "+err.Error())
		return
	}
	defer func() { _ = upstream.Close() }()

	// The request that was already read has to be replayed, along with
	// anything the client buffered behind it.
	if err := req.Write(upstream); err != nil {
		rec.Error = err.Error()
		p.emit(rec)
		return
	}
	rec.Status = http.StatusOK
	rec.Bytes = pipeBuffered(conn, br, upstream)
	rec.Duration = time.Since(started).String()
	p.emit(rec)
}

// serveTransparentTLS handles a connection whose destination is in its
// handshake.
func (p *proxy) serveTransparentTLS(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	br := bufio.NewReader(conn)
	sni, err := peekSNI(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		// A connection with no server name cannot be attributed to a host, so
		// it cannot be decided, so it is refused. Allowing it would be a hole
		// exactly the shape of "connect by IP and skip the policy".
		p.emit(record{
			Event: "decision", Method: "CONNECT", Port: 443, TLS: true,
			Mode: "block", Allowed: false, Via: "transparent",
			Reason: "The connection carried no server name, so no rule could apply to it.",
			Error:  err.Error(),
		})
		return
	}

	// Whether to read inside is decided from the host alone, because that is
	// all a handshake shows and the choice has to be made before it completes.
	// A rule naming a path, or any mode that has to read or replace the
	// request, needs the inside; plain allow and plain block do not, and
	// tunnelling those keeps the environment working for clients that pin
	// their own certificates.
	if p.ca != nil && p.engine.InspectsHost(sni, 443) {
		p.inspectTLS(conn, br, sni)
		return
	}

	started := time.Now()
	preq := policy.Request{Host: sni, Port: 443, Method: http.MethodConnect, Path: "/", TLS: true}
	d := p.engine.Evaluate(preq)

	rec := record{
		Event: "decision", Method: http.MethodConnect, Host: sni, Port: 443, TLS: true,
		Mode: string(d.Mode), Rule: d.RuleHost, Reason: d.Reason(),
		Allowed: d.Allowed(), Via: "transparent", HostOnly: true,
	}
	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		// Closed without a reply. There is no way to say anything inside a
		// TLS handshake that the client will read as prose, so the honest
		// signal is a refused connection and a line in the decision log.
		return
	}

	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(sni, "443"), 30*time.Second)
	if err != nil {
		rec.Error = err.Error()
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

// pipeBuffered copies both directions, starting with whatever the reader has
// already buffered.
func pipeBuffered(client net.Conn, buffered *bufio.Reader, upstream net.Conn) int64 {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(client, upstream)
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
		close(done)
	}()
	sent, _ := io.Copy(upstream, buffered)
	if c, ok := upstream.(*net.TCPConn); ok {
		_ = c.CloseWrite()
	}
	<-done
	return sent
}

var errNoSNI = errors.New("the handshake carried no server name")

// peekSNI reads the server name out of a TLS ClientHello without consuming it.
//
// Written by hand against the record and handshake layouts rather than by
// terminating the connection with crypto/tls, because terminating it would
// mean presenting a certificate, and the point of this path is to decide
// whether the connection happens at all without touching what is inside it.
func peekSNI(br *bufio.Reader) (string, error) {
	// Record header: type, version, length.
	head, err := br.Peek(5)
	if err != nil {
		return "", err
	}
	if head[0] != 0x16 {
		return "", errors.New("not a TLS handshake")
	}
	recordLen := int(binary.BigEndian.Uint16(head[3:5]))
	if recordLen <= 0 || recordLen > 16384 {
		return "", errors.New("implausible record length")
	}
	buf, err := br.Peek(5 + recordLen)
	if err != nil {
		return "", err
	}
	return parseClientHello(buf[5:])
}

func parseClientHello(b []byte) (string, error) {
	// Handshake header: type, 3 byte length.
	if len(b) < 4 || b[0] != 0x01 {
		return "", errors.New("not a client hello")
	}
	body := b[4:]
	// Version and random.
	if len(body) < 34 {
		return "", errNoSNI
	}
	off := 34
	// Session id.
	if off >= len(body) {
		return "", errNoSNI
	}
	off += 1 + int(body[off])
	// Cipher suites.
	if off+2 > len(body) {
		return "", errNoSNI
	}
	off += 2 + int(binary.BigEndian.Uint16(body[off:off+2]))
	// Compression methods.
	if off >= len(body) {
		return "", errNoSNI
	}
	off += 1 + int(body[off])
	// Extensions.
	if off+2 > len(body) {
		return "", errNoSNI
	}
	extLen := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2
	end := off + extLen
	if end > len(body) {
		end = len(body)
	}

	for off+4 <= end {
		extType := binary.BigEndian.Uint16(body[off : off+2])
		size := int(binary.BigEndian.Uint16(body[off+2 : off+4]))
		off += 4
		if off+size > end {
			return "", errNoSNI
		}
		if extType == 0 { // server_name
			return parseServerName(body[off : off+size])
		}
		off += size
	}
	return "", errNoSNI
}

func parseServerName(b []byte) (string, error) {
	if len(b) < 2 {
		return "", errNoSNI
	}
	listLen := int(binary.BigEndian.Uint16(b[:2]))
	off := 2
	end := off + listLen
	if end > len(b) {
		end = len(b)
	}
	for off+3 <= end {
		nameType := b[off]
		size := int(binary.BigEndian.Uint16(b[off+1 : off+3]))
		off += 3
		if off+size > end {
			return "", errNoSNI
		}
		if nameType == 0 { // host_name
			return string(b[off : off+size]), nil
		}
		off += size
	}
	return "", errNoSNI
}

// writeRefusalRaw writes the refusal onto a connection that is not being
// served by net/http.
func writeRefusalRaw(conn net.Conn, d policy.Decision, req policy.Request) {
	body := refusalBody(d, req)
	fmt.Fprintf(conn,
		"HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain; charset=utf-8\r\n"+
			"Content-Length: %d\r\nX-Antifailure-Decision: %s\r\nConnection: close\r\n\r\n%s",
		len(body), d.Mode, body)
}

// writeRawForbidden writes a refusal body onto a raw connection.
func writeRawForbidden(conn io.Writer, body string) {
	fmt.Fprintf(conn,
		"HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain; charset=utf-8\r\n"+
			"Content-Length: %d\r\nX-Antifailure-Decision: block\r\nConnection: close\r\n\r\n%s",
		len(body), body)
}

func writeRawError(conn net.Conn, status int, message string) {
	fmt.Fprintf(conn,
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\n"+
			"Content-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(message)+1, message+"\n")
}

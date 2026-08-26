package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Reading inside TLS is what capture, mock, sandbox, and any rule naming a
// path require, and it is done only where one of those applies.
//
// The rest is tunnelled untouched. That is not laziness: terminating a
// connection means presenting a certificate, and a client that pins its own
// will refuse it. A tunnel works for every client; inspection works for the
// ones that trust the environment's certificate, which is every client in the
// environment because the certificate is injected into it. Doing it only where
// the policy needs it keeps the blast radius of that difference small, and
// makes the failure mode of a pinning client a rule the user can change rather
// than a mystery.
//
// The authority is generated per environment and lives as long as it does.
// A shared one would mean a certificate that outlives the thing it was for,
// sitting in a trust store on somebody's laptop.

// certAuthority signs a certificate per host, on demand.
type certAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func newCertAuthority(certPEM, keyPEM string) (*certAuthority, error) {
	if certPEM == "" || keyPEM == "" {
		return nil, errors.New("no environment certificate was provided")
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("the environment certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing the environment certificate: %w", err)
	}
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, errors.New("the environment key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing the environment key: %w", err)
	}
	return &certAuthority{cert: cert, key: key, cache: map[string]*tls.Certificate{}}, nil
}

// leaf returns a certificate for one host, signing it the first time.
func (a *certAuthority) leaf(host string) (*tls.Certificate, error) {
	a.mu.Lock()
	if c, ok := a.cache[host]; ok {
		a.mu.Unlock()
		return c, nil
	}
	a.mu.Unlock()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"Antifailure environment"}},
		// Backdated by an hour so that a container whose clock is behind the
		// host's does not reject a certificate issued a moment ago, which is
		// a genuinely common failure and an utterly baffling one.
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(30 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	out := &tls.Certificate{
		Certificate: [][]byte{der, a.cert.Raw},
		PrivateKey:  key,
		Leaf:        tmpl,
	}
	a.mu.Lock()
	a.cache[host] = out
	a.mu.Unlock()
	return out, nil
}

// GenerateAuthority makes a new environment authority.
//
// Exported so the engine can call it: the authority is created on the host,
// written into the sidecar, and injected into every service's trust store, and
// having one implementation means the certificate the sidecar signs with and
// the certificate the services trust cannot drift apart.
func GenerateAuthority(envID string, now time.Time) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			// Named for the environment, so somebody who finds one in a trust
			// store knows exactly what it was for and that it should not be
			// there any more.
			CommonName:   "Antifailure " + envID,
			Organization: []string{"Antifailure environment authority"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// One level. This signs leaves and nothing else, so it cannot be used
		// to mint another authority.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, nil
}

// inspectTLS terminates a connection, applies the policy to each request
// inside it, and forwards what is allowed.
func (p *proxy) inspectTLS(conn net.Conn, br *bufio.Reader, sni string) {
	leaf, err := p.ca.leaf(sni)
	if err != nil {
		p.emit(record{
			Event: "decision", Method: http.MethodConnect, Host: sni, Port: 443, TLS: true,
			Mode: "block", Allowed: false, Via: "inspect",
			Reason: "The environment certificate could not be issued for this host.",
			Error:  err.Error(),
		})
		return
	}

	server := tls.Server(&prefixedConn{Conn: conn, r: br}, &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		MinVersion:   tls.VersionTLS12,
	})
	_ = server.SetDeadline(time.Now().Add(30 * time.Second))
	if err := server.Handshake(); err != nil {
		// A client that pins its own certificate lands here. It is reported as
		// its own thing rather than as a policy refusal, because the fix is a
		// rule change and not a code change, and calling it blocked would send
		// somebody to the wrong line of the manifest.
		p.emit(record{
			Event: "decision", Method: http.MethodConnect, Host: sni, Port: 443, TLS: true,
			Mode: "block", Allowed: false, Via: "inspect",
			Reason: "This client refused the environment certificate, which usually means it pins " +
				"its own. Set this host to allow so that its traffic is tunnelled rather than inspected.",
			Error: err.Error(),
		})
		return
	}
	_ = server.SetDeadline(time.Time{})
	defer func() { _ = server.Close() }()

	// One connection can carry many requests, and each is decided on its own.
	// A client that keeps a connection open to an allowed path and then asks
	// for a refused one must be refused for the second, which is exactly what
	// a host level tunnel cannot do.
	reader := bufio.NewReader(server)
	for {
		_ = server.SetReadDeadline(time.Now().Add(120 * time.Second))
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		_ = server.SetReadDeadline(time.Time{})

		host := sni
		if req.Host != "" {
			host, _ = splitHostPort(req.Host, 443)
		}
		if !p.serveInspected(server, req, host) {
			return
		}
	}
}

// serveInspected decides one request read from inside a terminated connection
// and reports whether the connection should stay open.
func (p *proxy) serveInspected(w net.Conn, req *http.Request, host string) bool {
	started := time.Now()
	preq := policy.Request{Host: host, Port: 443, Method: req.Method, Path: req.URL.Path, TLS: true}
	d := p.engine.Evaluate(preq)

	rec := record{
		Event: "decision", Method: req.Method, Host: host, Port: 443, Path: req.URL.Path,
		TLS: true, Mode: string(d.Mode), Rule: d.RuleHost, Reason: d.Reason(),
		Allowed: d.Allowed(), Via: "inspect",
	}
	if d.Mode == schema.ModeCapture {
		rec.Status = http.StatusOK
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		p.capture(w, req, host)
		return false
	}
	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRefusalRaw(w, d, preq)
		// Closed rather than kept open. A refusal is the end of what this
		// connection was for, and leaving it open invites the client to retry
		// on it forever.
		return false
	}

	outbound := req.Clone(req.Context())
	outbound.RequestURI = ""
	outbound.URL.Scheme = "https"
	outbound.URL.Host = host
	for _, h := range hopByHop {
		outbound.Header.Del(h)
	}

	resp, err := p.transport.RoundTrip(outbound)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRawError(w, http.StatusBadGateway, "Antifailure could not reach "+host+": "+err.Error())
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	rec.Status = resp.StatusCode
	// Written with Write rather than by hand, so that chunked encoding,
	// trailers, and connection semantics are the standard library's problem
	// and not a source of subtle corruption in somebody's API response.
	if err := resp.Write(w); err != nil {
		rec.Error = err.Error()
		p.emit(rec)
		return false
	}
	rec.Bytes = resp.ContentLength
	if rec.Bytes < 0 {
		rec.Bytes = 0
	}
	rec.Duration = time.Since(started).String()
	p.emit(rec)
	return resp.Close == false && req.Close == false
}

// hopByHop are the headers that belong between two endpoints of a connection
// rather than to the request, and forwarding them is how a proxy ends up
// asking an origin to keep alive a connection that only makes sense here.
var hopByHop = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// prefixedConn replays bytes already read from a connection.
//
// The TLS handshake was peeked at to find the server name, so those bytes are
// buffered and the real handshake has to see them again.
type prefixedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

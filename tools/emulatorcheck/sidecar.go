package emulatorcheck

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/emulator"
)

// Sidecar is a stand in for the environment's egress sidecar, and it is
// deliberately the smallest thing that can carry the claim being tested.
//
// WHAT IT IS AND IS NOT. The engine's own sidecar, engine/cmd/af-proxy, is
// what ships: it holds the whole policy, the decision log, the credential
// substitution and the mock packs. It learns about emulators from proxy.json,
// which the engine writes after it resolves the registry, and that half is
// another lane's. This stands in for exactly the two behaviours the AWS
// surface depends on and for nothing else:
//
//  1. A request to a covered host is REROUTED to the emulator, and the
//     application is not told. The client speaks TLS to the name it always
//     spoke to.
//  2. The Host header and the Authorization header are PRESERVED. Virtual
//     hosted S3 addressing carries the bucket in Host and LocalStack reads it
//     from there, and SigV4 signs Host, so a rewrite of either breaks the
//     request in a way that looks like an emulator bug.
//
// A test that stood up the real sidecar would prove more. This one proves that
// an unmodified SDK, given nothing but a proxy and a certificate authority the
// environment already provides, reaches the emulator and gets real answers,
// with zero endpoint overrides in the code making the calls.
type Sidecar struct {
	// Emulator is where a covered request is sent, as host:port.
	Emulator string
	// Log, when set, receives one line per decision. The router that runs
	// inside a container writes to standard output, which is the only channel
	// out of it a test can read.
	Log io.Writer

	ca     tls.Certificate
	caPEM  []byte
	caPath string

	listener net.Listener
	server   *http.Server

	mu       sync.Mutex
	seen     []Observation
	leaf     map[string]*tls.Certificate
	leafOnce sync.Mutex
}

// Observation is one request the sidecar decided, recorded so that a test can
// assert on what the SDK actually sent rather than on what it meant to send.
type Observation struct {
	// Host is the value of the Host header as it reached the emulator.
	Host string
	// Method and Path are the request line.
	Method, Path string
	// Authorized reports whether an Authorization header was present.
	Authorized bool
	// Authorization is the header as it arrived, so that a test can prove it
	// was not rewritten. It is the SDK's own signature over a request signed
	// with a deliberately fake key, and the emulator verifies no signature, so
	// there is nothing here worth hiding. A live key never reaches this: the
	// engine's sidecar refuses one and the emulator has no route out.
	Authorization string
	// Emulated reports whether the request was routed to the emulator rather
	// than refused.
	Emulated bool
	// Service is the covered service that claimed the host, when one did.
	Service string
}

// LoadSidecar builds one around a certificate authority that already exists,
// which is what the standalone router does: the test mints the authority once,
// mounts it into the container that routes and into the container that calls,
// and both sides then agree about who signed the certificate.
func LoadSidecar(emulatorAddress string, certPEM, keyPEM []byte) (*Sidecar, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	pair.Leaf = leaf
	return &Sidecar{
		Emulator: emulatorAddress,
		ca:       pair,
		caPEM:    certPEM,
		leaf:     map[string]*tls.Certificate{},
	}, nil
}

// CA returns the authority in PEM, which is what a caller trusts.
func (s *Sidecar) CA() []byte { return s.caPEM }

// CAKeyPEM returns the authority's private key in PEM.
//
// It exists so that a test can hand the same authority to a router running in
// a container. The key lives for the length of one test run and signs nothing
// outside it.
func (s *Sidecar) CAKeyPEM() ([]byte, error) {
	key, ok := s.ca.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the authority is not an RSA key")
	}
	return pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), nil
}

// ServeTLS answers HTTPS directly on a listener, which is the shape the
// sidecar takes inside an environment.
//
// The proxy variables are one of two ways a service reaches the sidecar and
// the weaker one, because a library is free to ignore them. The other is DNS:
// every external name resolves to the sidecar's own address, so a client that
// ignores every variable still arrives here. This is that path, and it is what
// the JavaScript suite drives, because the AWS SDK for JavaScript reads no
// proxy variable at all. A Docker network alias per hostname stands in for the
// environment's resolver.
func (s *Sidecar) ServeTLS(ln net.Listener) error {
	server := &http.Server{
		ReadHeaderTimeout: 30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.forward(w, r, r.Host)
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return s.certificateFor(hello.ServerName)
			},
		},
	}
	s.server = server
	return server.ServeTLS(ln, "", "")
}

// ServeHTTP carries returned HTTP queue URLs through the same routing policy.
// The production sidecar listens on both ports; this fixture must do so too.
func (s *Sidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.forward(w, r, r.Host)
}

// NewSidecar builds the stand in and its certificate authority.
func NewSidecar(emulatorAddress string) (*Sidecar, error) {
	s := &Sidecar{Emulator: emulatorAddress, leaf: map[string]*tls.Certificate{}}
	if err := s.mintCA(); err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.listener = ln
	s.server = &http.Server{
		Handler:           http.HandlerFunc(s.serve),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = s.server.Serve(ln) }()
	return s, nil
}

// ProxyURL is what goes in HTTPS_PROXY. It is an environment variable the
// environment sets, not a line in the application.
func (s *Sidecar) ProxyURL() string { return "http://" + s.listener.Addr().String() }

// CABundlePath writes the authority to a file and returns its path, which is
// what AWS_CA_BUNDLE names. In a real environment the same certificate is in
// the image's trust store and no variable is needed at all.
func (s *Sidecar) CABundlePath() (string, error) {
	if s.caPath != "" {
		return s.caPath, nil
	}
	f, err := os.CreateTemp("", "af-emulatorcheck-ca-*.pem")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(s.caPEM); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	s.caPath = f.Name()
	return s.caPath, nil
}

// Close stops the sidecar and removes the certificate file.
func (s *Sidecar) Close() {
	if s.server != nil {
		_ = s.server.Close()
	}
	if s.caPath != "" {
		_ = os.Remove(s.caPath)
	}
}

// Observed returns what passed through, in order.
func (s *Sidecar) Observed() []Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Observation, len(s.seen))
	copy(out, s.seen)
	return out
}

func (s *Sidecar) record(o Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, o)
	if s.Log == nil {
		return
	}
	line, err := json.Marshal(o)
	if err != nil {
		return
	}
	// Discarded on purpose, and assigned rather than dropped so that is
	// visible. This is the decision log, and a test that could not write a
	// line of it has still made the observation: it is already in s.seen,
	// which is what every assertion reads. Failing a routing test because a
	// log write failed would report the wrong defect.
	_, _ = fmt.Fprintf(s.Log, "%s%s\n", ObservationPrefix, line)
}

// ObservationPrefix marks a decision in the router's output, so a test reading
// a container's log can tell one from whatever else was written there.
const ObservationPrefix = "emulatorcheck-decision "

func (s *Sidecar) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		// A plain HTTP request through the proxy, which an SDK speaking to an
		// http:// endpoint would make. Same decision, no TLS to terminate.
		s.forward(w, r, r.Host)
		return
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	leaf, err := s.certificateFor(host)
	if err != nil {
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer func() { _ = tlsConn.Close() }()

	// One connection can carry several requests, and an SDK reuses one.
	reader := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		req.URL.Scheme = "https"
		if req.Host == "" {
			req.Host = host
		}
		if err := s.answer(tlsConn, req); err != nil {
			return
		}
	}
}

// answer decides one request read inside a terminated connection.
func (s *Sidecar) answer(w io.Writer, req *http.Request) error {
	resp, err := s.decide(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Write(w)
}

// forward handles the plain HTTP case by writing through a ResponseWriter.
func (s *Sidecar) forward(w http.ResponseWriter, r *http.Request, host string) {
	resp, err := s.decide(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// decide is the whole policy this stand in has: a covered host goes to the
// emulator with its headers untouched, and everything else is refused.
func (s *Sidecar) decide(req *http.Request) (*http.Response, error) {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	e, ok := emulator.Named(emulator.AWSName)
	if !ok {
		return nil, fmt.Errorf("no aws emulator is built in")
	}
	svc, covered := e.ServiceFor(host)

	obs := Observation{
		Host:          req.Host,
		Method:        req.Method,
		Path:          req.URL.Path,
		Authorized:    req.Header.Get("Authorization") != "",
		Authorization: req.Header.Get("Authorization"),
		Emulated:      covered,
		Service:       svc.Name,
	}
	s.record(obs)

	if !covered {
		return refusal(req), nil
	}

	// The destination is rewritten. Nothing else is. The Host header travels
	// as the SDK wrote it, which for a virtual hosted bucket is where the
	// bucket name lives, and the Authorization header travels as the SDK
	// signed it.
	out := req.Clone(req.Context())
	out.RequestURI = ""
	out.URL.Scheme = "http"
	out.URL.Host = s.Emulator
	out.Host = req.Host

	// One header ADDED, and it is the standard one every TLS terminating
	// proxy adds. The application spoke https; this hop to the emulator is
	// plain http because the emulator has no certificate for a name it does
	// not own, and without being told, an API that builds a URL for the
	// client to call next builds an http one.
	//
	// Measured in CI on 2026-09-09. With SQS_ENDPOINT_STRATEGY=dynamic the
	// emulator returned the right HOST and the wrong SCHEME, so the Node
	// application resolved the name correctly and then died with `connect
	// ECONNREFUSED 172.18.0.3:80` against a router that answers on 443. The
	// name was right and the port was wrong, which is why this is the third
	// different error in three runs rather than the same one.
	out.Header.Set("X-Forwarded-Proto", "https")

	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client.Do(out)
}

// refusal is what a host outside the surface gets, and it is the shape the
// SHIPPED sidecar already writes rather than a better one invented here.
//
// engine/cmd/af-proxy/transparent.go answers a blocked host with 403,
// text/plain, an X-Antifailure-Decision header and a sentence a person can
// read in a stack trace. This mirrors that, because a stand in whose refusal
// is nicer than the real one is a stand in that proves the wrong thing.
//
// It used to write AWS's XML error shape, on the reasoning that the SDK
// parses that and an application's error handling is written against it. The
// reasoning was right and the premise was false: AWS HAS NO SINGLE ERROR
// SHAPE. S3 and the Query services are XML with a Code and a Message; Lambda,
// DynamoDB, Kinesis, Secrets Manager and Parameter Store are JSON, with the
// code in an x-amzn-errortype header. Measured on 2026-09-09 in CI, the first
// run of this suite anywhere: the Go SDK's Lambda client received the XML and
// reported `deserialization failed, failed to decode response body, invalid
// character '<' looking for beginning of value`, which is precisely the "a
// refusal the application reports as something else" the old comment set out
// to avoid. One shape cannot serve both halves of AWS, and choosing per host
// would mean this stand in carrying a protocol table for services it
// deliberately does not know about.
//
// So it refuses the way the engine refuses. The guarantee that survives is
// the one the guide states: an uncovered host is not answered, and it does
// not reach the emulator.
func refusal(req *http.Request) *http.Response {
	body := req.Host + " is outside the emulated AWS surface, so this environment " +
		"refuses it rather than answering it.\n"
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{
			"Content-Type":           []string{"text/plain; charset=utf-8"},
			"X-Antifailure-Decision": []string{"block"},
		},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func (s *Sidecar) mintCA() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Antifailure emulator check CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	s.ca = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}
	s.caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return nil
}

func (s *Sidecar) certificateFor(host string) (*tls.Certificate, error) {
	s.leafOnce.Lock()
	defer s.leafOnce.Unlock()
	if c, ok := s.leaf[host]; ok {
		return c, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 96))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, s.ca.Leaf, &key.PublicKey, s.ca.PrivateKey)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der, s.ca.Certificate[0]}, PrivateKey: key}
	s.leaf[host] = cert
	return cert, nil
}

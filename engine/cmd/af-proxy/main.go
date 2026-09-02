// Command af-proxy is the sidecar that decides what an environment may reach.
//
// It runs inside the environment on both networks: the inner one, which has no
// route to the internet, and the outer one, which does. Services are told to
// use it through the standard proxy variables. The thing that makes that
// trustworthy is not the variables, which any library is free to ignore, but
// the network: a service that ignores them has nowhere to send the packet. The
// failure mode of a badly behaved SDK is a connection error, not silent
// egress.
//
// It imports the same policy package the command line uses, so af net explain
// and this program cannot disagree about what a rule means. That is the whole
// reason the policy package has no dependencies beyond the standard library.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antifailure/antifailure/engine/internal/mockpack"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/livekey"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Config is what the runtime writes into the sidecar before it starts.
//
// A file rather than flags, because the sidecar's own address on the
// environment's network is only known after the container is created and
// attached, which is after its command line is fixed.
type Config struct {
	// Egress is the policy to enforce.
	Egress schema.Egress `json:"egress"`
	// Subnet is the environment's inner network in CIDR form.
	//
	// The sidecar finds its own address inside it rather than being told the
	// address, because Docker does not assign one until the container starts,
	// which is after the moment this file has to be written.
	Subnet string `json:"subnet"`
	// Internal are the names that must resolve normally rather than to this
	// sidecar: other services, the database, and the sidecar itself.
	Internal []string `json:"internal"`
	// EnvID identifies the environment in the decision log.
	EnvID string `json:"env_id"`
	// MockPacks are extra packs supplied by the manifest, as raw JSON. The
	// built in ones are compiled into the sidecar and always available.
	MockPacks []string `json:"mock_packs,omitempty"`
	// Credentials maps a rule's credential name to the sandbox value the
	// sidecar substitutes. Values never appear in a log line.
	Credentials map[string]string `json:"credentials,omitempty"`
	// Resolver is where internal names are forwarded, as host:port.
	//
	// Empty means Docker's embedded resolver, which is correct for the local
	// runtime and meaningless anywhere else. It is a name to forward to and
	// never a route out: an external name is still answered by this sidecar
	// whatever this is set to, so pointing it somewhere unexpected cannot
	// turn into a way around the policy.
	Resolver string `json:"resolver,omitempty"`
	// CACert and CAKey are the environment's certificate authority, in PEM.
	//
	// Present only when something in the policy needs to read inside TLS. An
	// environment whose rules are all plain allow or block never terminates a
	// connection and never needs one.
	CACert string `json:"ca_cert,omitempty"`
	CAKey  string `json:"ca_key,omitempty"`
}

func main() {
	configPath := flag.String("config", "/etc/antifailure/proxy.json", "path to the sidecar configuration")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("af-proxy: %v", err)
	}
	engine, err := policy.New(&cfg.Egress)
	if err != nil {
		log.Fatalf("af-proxy: %v", err)
	}

	p := &proxy{
		engine: engine, envID: cfg.EnvID, out: json.NewEncoder(os.Stdout),
		credentials: cfg.Credentials,
		limits:      newLimiter(),
		destinations: newDestinations(
			engine.Rules(), cfg.Subnet, engine.AllowsIPv6()),
		// Read from this process's environment rather than from the
		// configuration file, so a key never passes through something the
		// engine wrote to disk.
		synth: synthFromEnvironment(os.Getenv),
		transport: &http.Transport{
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     60 * time.Second,
			// The origin's certificate is verified normally. Reading inside a
			// connection is not a licence to stop checking who is on the other
			// end of it; if anything it makes the check more important,
			// because the client can no longer do it itself.
			TLSHandshakeTimeout: 20 * time.Second,
		},
	}
	// Set after construction because the dialer is a method on the proxy it
	// belongs to. Every re-originated request goes through it, so the address
	// guard applies to the inspected path as well as to the tunnelled one.
	p.transport.DialContext = p.dialGuarded

	packs, err := mockpack.Builtin()
	if err != nil {
		log.Fatalf("af-proxy: %v", err)
	}
	for _, raw := range cfg.MockPacks {
		pack, parseErr := mockpack.Parse([]byte(raw))
		if parseErr != nil {
			// Refused rather than skipped. A pack that silently did not load
			// would leave its host answering nothing, and the failure would
			// look like a missing route rather than a broken file.
			log.Fatalf("af-proxy: %v", parseErr)
		}
		packs = append(packs, pack)
	}
	p.mocks = mockpack.New(packs)

	if cfg.CACert != "" {
		ca, caErr := newCertAuthority(cfg.CACert, cfg.CAKey)
		if caErr != nil {
			log.Fatalf("af-proxy: %v", caErr)
		}
		p.ca = ca
	}

	self, err := addressInside(cfg.Subnet)
	if err != nil {
		log.Fatalf("af-proxy: %v", err)
	}
	resolver := cfg.Resolver
	if resolver == "" {
		resolver = dockerResolver
	}
	dns := newDNSServer(self, cfg.Internal, resolver, p.emit)

	// Every listener is started before anything is announced as ready, so a
	// service that begins its first outbound call the instant it starts finds
	// a decision rather than a closed port.
	errs := make(chan error, 4)
	udp, err := net.ListenPacket("udp", ":53")
	if err != nil {
		log.Fatalf("af-proxy: %v", err)
	}
	go func() { errs <- dns.serve(udp) }()

	go func() { errs <- p.listen(":80", p.serveTransparentHTTP) }()
	go func() { errs <- p.listen(":443", p.serveTransparentTLS) }()

	// The explicit proxy port stays, for clients that do read their proxy
	// variables. It is the same policy either way; this one can see the full
	// request on an HTTPS call's CONNECT line, which the transparent path
	// cannot, so a client that opts in gets a slightly better decision.
	go func() {
		srv := &http.Server{
			Addr:    ":" + strconv.Itoa(3128),
			Handler: p,
			// A request that is never finished must not hold a connection
			// forever, and an environment under load will have thousands.
			ReadHeaderTimeout: 20 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
		errs <- srv.ListenAndServe()
	}()

	p.emit(record{
		Event: "ready", Rules: len(engine.Rules()), Default: string(engine.Default()),
		Reason: describeDNS(self, cfg.Internal),
		// The count, never the values. A sandbox rule whose credential never
		// arrived forwards whatever the application sent, and the only way to
		// notice is a number that says zero.
		Credentials: len(cfg.Credentials),
	})

	log.Fatalf("af-proxy: %v", <-errs)
}

// addressInside finds this container's address on a given network.
//
// A sidecar with no address on the environment's network cannot intercept
// anything, and starting anyway would produce an environment that looks
// contained and is not, so this is fatal rather than a warning.
func addressInside(cidr string) (net.IP, error) {
	if cidr == "" {
		return nil, fmt.Errorf("no network was named for this sidecar to answer on")
	}
	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("%q is not a network: %w", cidr, err)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil && subnet.Contains(v4) {
			return v4, nil
		}
	}
	return nil, fmt.Errorf("this sidecar has no address on %s", cidr)
}

// listen accepts connections and hands each to a handler.
func (p *proxy) listen(addr string, handle func(net.Conn)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(conn)
	}
}

func loadConfig(path string) (*Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the configuration: %w", err)
	}
	var c Config
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("parsing the configuration: %w", err)
	}
	if c.Egress.Default == "" {
		// An absent default is block, the same as everywhere else. Defaulting
		// to allow here would make a malformed configuration open rather than
		// closed, which is the wrong direction for the one component whose
		// job is to refuse things.
		c.Egress.Default = schema.ModeBlock
	}
	return &c, nil
}

// record is one line of the decision log.
//
// Every request produces one, allowed or not. A log that only records refusals
// answers "why was this blocked" and not "did anything reach Stripe", and the
// second question is the one somebody asks after an incident.
type record struct {
	Event    string `json:"event"`
	Env      string `json:"env,omitempty"`
	At       string `json:"at,omitempty"`
	Method   string `json:"method,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Path     string `json:"path,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Allowed  bool   `json:"allowed"`
	Status   int    `json:"status,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Duration string `json:"duration,omitempty"`
	Error    string `json:"error,omitempty"`
	Rules    int    `json:"rules,omitempty"`
	Default  string `json:"default,omitempty"`
	Seq      uint64 `json:"seq,omitempty"`
	// Via says how the request arrived: as a proxy request from a client that
	// read its proxy variables, or transparently from one that did not.
	Via string `json:"via,omitempty"`
	// Substituted marks a request whose credential was replaced on the way
	// out, so a reader can tell a sandbox call from a live one.
	Substituted bool `json:"substituted,omitempty"`
	// Synthesized marks a response a model invented, so a workflow that
	// touched one reports unverified rather than passed.
	Synthesized bool `json:"synthesized,omitempty"`
	// WaitedMs is how long a rate limit held this request. Recorded because a
	// request that took a second is a request somebody will otherwise blame
	// on the application.
	WaitedMs int64 `json:"waited_ms,omitempty"`
	// Limit is that rate in words, "10 a second, bursting to 10". The
	// milliseconds alone say a request was slow and not what slowed it, and
	// the rule's raw spec is in the manifest rather than in front of whoever
	// is reading the log.
	Limit string `json:"limit,omitempty"`
	// Credentials counts the sandbox values loaded, on the ready line.
	Credentials int `json:"credentials,omitempty"`
	// Pack and Fixture name what answered a mocked request. A mock that
	// cannot say which fixture produced a response is a mock nobody can
	// debug.
	Pack    string `json:"pack,omitempty"`
	Fixture string `json:"fixture,omitempty"`
	// HostOnly marks a decision made without seeing the path or the method,
	// which is every HTTPS request until the environment certificate lands.
	// Recorded rather than assumed away, so a reader can tell the difference
	// between a rule that matched and a rule that could only half apply.
	HostOnly bool `json:"host_only,omitempty"`
}

type proxy struct {
	engine *policy.Engine
	envID  string
	out    *json.Encoder
	// ca signs a certificate per host, for the connections the policy needs
	// to read inside. Nil when the environment has no authority, in which
	// case every TLS connection is tunnelled.
	ca *certAuthority
	// transport re-originates inspected requests.
	transport *http.Transport
	// credentials are the sandbox values, by the name a rule refers to.
	credentials map[string]string
	// mocks answers requests for hosts set to mock.
	mocks *mockpack.Engine
	// limits shape traffic to a rule's declared rate, so a load run does not
	// get somebody's sandbox account throttled.
	limits *limiter
	// destinations refuse the addresses the environment must not reach
	// through this sidecar, whatever the policy says about the name.
	destinations *destinations
	// resolve turns a name into addresses. Nil means the system resolver,
	// which is what the sidecar always uses; a test sets it to say what a
	// name resolves to.
	resolve func(context.Context, string) ([]net.IP, error)
	// synth invents a response when a rule asks for one. Nil when no model
	// key is available, in which case a synth rule refuses and says so.
	synth *synthConfig
	seq   atomic.Uint64
	// mu serialises writes to the encoder. A JSON encoder is not safe for
	// concurrent use, and every request writes a line, so without it a busy
	// environment produces a decision log with interleaved bytes: the one
	// artifact whose whole value is that it can be trusted after the fact.
	mu sync.Mutex
}

func (p *proxy) emit(r record) {
	r.Env = p.envID
	if r.At == "" {
		r.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	r.Seq = p.seq.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.out.Encode(r)
}

// emitMessage writes a captured message to the log.
//
// It shares the encoder's lock with the decisions, so the two streams
// interleave by line rather than by byte, and a reader can take the log apart
// with nothing more than a JSON decoder per line.
func (p *proxy) emitMessage(m message) {
	m.Env = p.envID
	if m.At == "" {
		m.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	m.Seq = p.seq.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.out.Encode(m)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.serveConnect(w, r)
		return
	}
	p.serveHTTP(w, r)
}

// serveConnect handles the tunnel every HTTPS request opens.
//
// Only the host and port are visible here, which is a real limitation and is
// stated rather than hidden: a rule that names paths or methods cannot be
// enforced on an HTTPS request until the environment certificate lands, so
// such a rule is evaluated on its host alone. A rule that would have matched
// on the path is reported in the decision log as host-only, so the difference
// is visible to whoever reads it rather than silently assumed away.
func (p *proxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	host, port := splitHostPort(r.Host, 443)

	req := policy.Request{Host: host, Port: port, Method: http.MethodConnect, Path: "/", TLS: true}
	d := p.engine.Evaluate(req)

	rec := record{
		Event: "decision", Method: http.MethodConnect, Host: host, Port: port,
		TLS: true, Mode: string(d.Mode), Rule: d.RuleHost,
		Reason: d.Reason(), Allowed: d.Allowed(), Via: "proxy",
	}
	// Whether to read inside comes before whether to allow, and the order is
	// load bearing. Capture, mock, and sandbox all answer from inside the
	// tunnel and none of them counts as reaching out, so testing Allowed first
	// refuses the CONNECT and the request that would have been captured never
	// exists. That is exactly what happened: a client that honoured its proxy
	// variables got a 403 for a host set to capture, while a client that
	// ignored them was captured correctly, so the mode worked or did not
	// depending on which HTTP library the application happened to use.
	inspect := p.ca != nil && p.engine.InspectsHost(host, port)
	// A tunnel nobody reads inside was decided from the host and the port and
	// nothing else, exactly as the transparent one was, so it is recorded the
	// same way. The inspected tunnel is not marked, because the requests
	// inside it are decided on their paths and carry their own records.
	rec.HostOnly = !inspect

	if !inspect && !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		// A refused CONNECT gets a body even though most clients discard it,
		// because the ones that show it turn a mystifying failure into a
		// readable one at no cost.
		writeRefusal(w, d, req)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		rec.Error = "the connection cannot be hijacked"
		p.emit(rec)
		http.Error(w, "af-proxy: cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
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

	// A client that opted into the proxy still gets its request read when the
	// policy needs it read. The tunnel it just opened carries a TLS handshake
	// like any other, and the same rule decides what happens to it.
	if inspect {
		rec.Status = http.StatusOK
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		p.inspectTLS(client, buffered.Reader, host)
		return
	}

	// Background rather than the request's context. The connection was
	// hijacked a few lines up, so this handler owns it now and no longer
	// wants a deadline that belongs to a request net/http considers finished.
	upstream, err := p.dialGuarded(context.Background(), "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
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

// pipe copies in both directions and returns the bytes sent upstream.
func pipe(client, upstream net.Conn) int64 {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(client, upstream)
		// Closing the write side rather than the whole connection lets the
		// other direction finish, which is what a half closed TCP stream is
		// for and what a plain Close would cut off mid response.
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
		close(done)
	}()
	sent, _ := io.Copy(upstream, client)
	if c, ok := upstream.(*net.TCPConn); ok {
		_ = c.CloseWrite()
	}
	<-done
	return sent
}

// serveHTTP handles a plain request, where the whole thing is visible.
//
// This is the path a client that reads its proxy variables takes for an http
// URL, and until this was written it was the one path that did not enforce the
// whole policy. It read the mode and forwarded: the live credential tripwire
// never ran, a sandbox rule sent the application's own credential to the
// provider untouched, and capture, mock and synth were refused with a body
// claiming they were not wired up in this build. Which of those happened
// depended on whether the application's HTTP library honoured http_proxy,
// which is not a property anybody reasons about while writing a rule. The
// same defect was found and fixed on the CONNECT path and left here.
func (p *proxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !r.URL.IsAbs() {
		// A request that is not in absolute form was sent to the proxy as if
		// it were an origin server, which means something is pointed at the
		// wrong address rather than proxying through it.
		http.Error(w,
			"af-proxy: this is a proxy, not an origin server. Set HTTP_PROXY and HTTPS_PROXY to reach it.",
			http.StatusBadRequest)
		return
	}
	host, port := splitHostPort(r.URL.Host, 80)

	req := policy.Request{Host: host, Port: port, Method: r.Method, Path: r.URL.Path, TLS: false}
	d := p.engine.Evaluate(req)

	rec := record{
		Event: "decision", Method: r.Method, Host: host, Port: port, Path: r.URL.Path,
		Mode: string(d.Mode), Rule: d.RuleHost, Reason: d.Reason(), Allowed: d.Allowed(),
		Via: "proxy",
	}

	// Before anything is forwarded and before the mode is acted on, in every
	// mode, exactly as on the other two paths. A credential that can act on
	// production must not leave an environment running unreviewed code against
	// a copy of production data, and which HTTP library the application chose
	// has nothing to do with that.
	if found := p.tripwire(r, host); len(found) > 0 {
		rec.Status = http.StatusForbidden
		rec.Allowed = false
		rec.Reason = "This request carries a live credential: " + livekey.Describe(found)
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Antifailure-Decision", "block")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, refusalForLiveCredential(req, found))
		return
	}

	switch d.Mode {
	case schema.ModeCapture, schema.ModeMock, schema.ModeSynth:
		p.serveInsideTheEnvironment(w, r, host, d, &rec, started)
		return
	}

	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRefusal(w, d, req)
		return
	}

	if d.RateLimit != "" {
		if waited := p.limits.wait(d.RuleHost, d.RateLimit); waited > 0 {
			rec.WaitedMs = waited.Milliseconds()
			rec.Limit = describeRate(d.RateLimit)
		}
	}

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	// Hop by hop headers are ours, not the origin's, and forwarding them is
	// how a proxy ends up asking an upstream to keep a connection alive that
	// only makes sense between the client and the proxy.
	for _, h := range hopByHop {
		outbound.Header.Del(h)
	}
	if d.Mode == schema.ModeSandbox {
		// Whatever the application sent is discarded before the request
		// leaves, which is the whole difference between sandbox mode and
		// asking somebody to configure a sandbox key correctly.
		applySandbox(outbound, host, p.credentials[d.Credential])
		rec.Substituted = p.credentials[d.Credential] != ""
	}

	// p.transport rather than http.DefaultTransport, because the address
	// guard hangs off its dialer and the default one has no guard at all.
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

// serveInsideTheEnvironment answers a capture, mock or synth request that
// arrived through the explicit proxy port.
//
// All three write a whole HTTP response rather than filling in a
// ResponseWriter, because the other two paths hold a raw connection and have
// nothing else to write onto. Rather than a second implementation of each
// mode for this path, the connection is taken over and handed to the same
// code. The body is read first, because a hijacked request's Body is no
// longer safe to touch.
func (p *proxy) serveInsideTheEnvironment(
	w http.ResponseWriter, r *http.Request, host string, d policy.Decision,
	rec *record, started time.Time,
) {
	// The larger of the two limits the modes below apply, so neither is
	// handed a body this function truncated first. Each still applies its own.
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		rec.Error = "the connection cannot be taken over"
		rec.Duration = time.Since(started).String()
		p.emit(*rec)
		http.Error(w, "af-proxy: cannot answer this request", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		rec.Error = err.Error()
		rec.Duration = time.Since(started).String()
		p.emit(*rec)
		return
	}
	defer func() { _ = conn.Close() }()

	switch d.Mode {
	case schema.ModeCapture:
		rec.Status = http.StatusOK
		p.capture(conn, r, host)
	case schema.ModeMock:
		p.serveMock(conn, r, host, rec)
	case schema.ModeSynth:
		p.serveSynth(conn, r, host, rec)
	}
	rec.Duration = time.Since(started).String()
	p.emit(*rec)
}

// writeRefusal explains a refusal in the response body.
//
// The audience is a developer reading a stack trace at three in the afternoon,
// so it says what was refused, which rule refused it, and what to change. A
// bare 403 sends them to the wrong place every time.
func writeRefusal(w http.ResponseWriter, d policy.Decision, req policy.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Antifailure-Decision", string(d.Mode))
	if d.RuleHost != "" {
		w.Header().Set("X-Antifailure-Rule", d.RuleHost)
	}
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, refusalBody(d, req))
}

// refusalBody is what a developer reads in a stack trace at three in the
// afternoon: what was refused, which rule refused it, and what to change. A
// bare 403 sends them to the wrong place every time.
func refusalBody(d policy.Decision, req policy.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Antifailure refused this request.\n\n")
	fmt.Fprintf(&b, "  %s\n\n", req.String())
	fmt.Fprintf(&b, "%s\n\n", d.Reason())
	fmt.Fprintf(&b, "Ask about it with:\n\n  af net explain %s %s\n",
		req.Method, schemeOf(req)+"://"+req.Host+req.Path)
	return b.String()
}

func schemeOf(r policy.Request) string {
	if r.TLS {
		return "https"
	}
	return "http"
}

func splitHostPort(hostport string, fallback int) (string, int) {
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		if n, convErr := strconv.Atoi(p); convErr == nil {
			return h, n
		}
		return h, fallback
	}
	return hostport, fallback
}

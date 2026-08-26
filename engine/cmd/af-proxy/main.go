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

	"github.com/antifailure/antifailure/engine/internal/policy"
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

	p := &proxy{engine: engine, envID: cfg.EnvID, out: json.NewEncoder(os.Stdout)}

	self, err := addressInside(cfg.Subnet)
	if err != nil {
		log.Fatalf("af-proxy: %v", err)
	}
	dns := newDNSServer(self, cfg.Internal, dockerResolver, p.emit)

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
	seq    atomic.Uint64
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
		Reason: d.Reason(), Allowed: d.Allowed(),
	}
	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		// A refused CONNECT gets a body even though most clients discard it,
		// because the ones that show it turn a mystifying failure into a
		// readable one at no cost.
		writeRefusal(w, d, req)
		return
	}

	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 30*time.Second)
	if err != nil {
		rec.Error = err.Error()
		rec.Status = http.StatusBadGateway
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		http.Error(w, "af-proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

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
	}
	if !d.Allowed() {
		rec.Status = http.StatusForbidden
		rec.Duration = time.Since(started).String()
		p.emit(rec)
		writeRefusal(w, d, req)
		return
	}

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	// Hop by hop headers are ours, not the origin's, and forwarding them is
	// how a proxy ends up asking an upstream to keep a connection alive that
	// only makes sense between the client and the proxy.
	for _, h := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		outbound.Header.Del(h)
	}

	resp, err := http.DefaultTransport.RoundTrip(outbound)
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
	switch d.Mode {
	case schema.ModeCapture:
		b.WriteString("This host is set to capture, which is not wired up yet in this build.\n")
	case schema.ModeMock:
		b.WriteString("This host is set to mock, which is not wired up yet in this build.\n")
	default:
		fmt.Fprintf(&b, "Ask about it with:\n\n  af net explain %s %s\n",
			req.Method, schemeOf(req)+"://"+req.Host+req.Path)
	}
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

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/egress"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What an environment can carry, measured rather than described.
//
// Every test in this file is about one claim: an unmodified application makes
// outbound calls, and the ones that are not HTTP had no path through the
// sidecar at all. The measurement below is the lane's number and the tests
// after it are the pairs that make each row evidence rather than a story.

// protocolProbe is one outbound protocol and the bytes its real client sends
// first.
//
// The bytes matter. A test that fed every row a TLS ClientHello would report
// that every protocol works, because the thing being measured is whether the
// first bytes name a host, and half of these protocols do not send TLS at all
// in the form a developer reaches for first.
type protocolProbe struct {
	name string
	port int
	// sni is the server name the client puts in its handshake, or empty for a
	// protocol whose opening bytes are not a handshake.
	sni string
	// opening is what the client sends before anything else, for the
	// cleartext rows. Empty means the row speaks TLS and the test generates a
	// real ClientHello with crypto/tls instead of a literal.
	opening []byte
	// managed names a provider a real application pays for, so a reader can
	// tell a hypothetical row from one somebody is actually blocked by.
	managed string
}

// outboundProtocols is the corpus.
//
// Chosen by asking what an application that is not a website talks to: a
// broker, a managed database, a mail relay, a cache. Each row's opening bytes
// are what that protocol's client library actually writes, taken from the
// protocol's own specification rather than invented.
func outboundProtocols() []protocolProbe {
	return []protocolProbe{
		{
			name: "AMQP 1.0 over TLS", port: 5671, sni: "af.servicebus.windows.net",
			managed: "Azure Service Bus, CloudAMQP, Amazon MQ",
		},
		{
			name: "AMQP 0-9-1 cleartext", port: 5672,
			// The AMQP protocol header, which is what every client writes
			// before a single frame: the four letters and a version.
			opening: []byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1},
			managed: "a self-hosted RabbitMQ",
		},
		{
			name: "Kafka over TLS", port: 9093, sni: "af.confluent.cloud",
			managed: "Confluent Cloud, Amazon MSK, Azure Event Hubs",
		},
		{
			name: "Kafka cleartext", port: 9092,
			// An ApiVersions request: a four byte length and a request header
			// whose api key is 18.
			opening: []byte{0, 0, 0, 8, 0, 18, 0, 0, 0, 0, 0, 1},
			managed: "a self-hosted Kafka",
		},
		{
			name: "MongoDB over TLS", port: 27017, sni: "af.mongodb.net",
			managed: "MongoDB Atlas",
		},
		{
			name: "Redis over TLS", port: 6380, sni: "af.redis.cache.windows.net",
			managed: "Azure Cache for Redis, Redis Cloud",
		},
		{
			name: "Redis cleartext", port: 6379,
			// A RESP inline array: HELLO 3, which is what a modern client
			// sends to negotiate.
			opening: []byte("*2\r\n$5\r\nHELLO\r\n$1\r\n3\r\n"),
			managed: "a self-hosted Redis",
		},
		{
			name: "PostgreSQL", port: 5432,
			// The SSLRequest packet: a length and the magic number 80877103.
			// It is cleartext, and the TLS handshake only follows after the
			// server agrees, which is why this row cannot be decided.
			opening: []byte{0, 0, 0, 8, 4, 210, 22, 47},
			managed: "Amazon RDS, Neon, Supabase",
		},
		{
			name: "MySQL", port: 3306,
			// Nothing. MySQL's server speaks first, so a client sends no
			// bytes at all until it has read a greeting.
			opening: nil,
			managed: "Amazon RDS, PlanetScale",
		},
		{
			name: "SMTP submission", port: 587,
			// Nothing, for the same reason: the server sends its 220 banner
			// first and STARTTLS comes after.
			opening: nil,
			managed: "SendGrid, Amazon SES, Postmark",
		},
		{
			name: "SMTP over TLS", port: 465, sni: "smtp.af.example.com",
			managed: "SendGrid, Amazon SES on the implicit TLS port",
		},
		{
			name: "MQTT over TLS", port: 8883, sni: "af.iot.eu-west-1.amazonaws.com",
			managed: "AWS IoT Core",
		},
	}
}

// outcome is what happened to one probe.
type outcome struct {
	protocol string
	port     int
	// answered says whether anything was listening on the port at all.
	answered bool
	// decided says whether a policy decision was made about a named host.
	decided bool
	// host is the host the decision was about.
	host string
	// verdict is what a reader of the decision log would see.
	verdict string
}

// TestStream_HowManyOutboundProtocolsAnEnvironmentCanCarry is the lane's
// number.
//
// It measures both halves in one run. The BEFORE half asks which of these
// ports the sidecar answered on before the byte stream path existed, which is
// exactly the set of ports the HTTP listeners own. The AFTER half runs each
// protocol's real opening bytes through the real handler and reports what the
// decision log says.
//
// A row that is answered and not decided is still a refusal, and it is
// counted as one rather than as a success, because a connection nobody can
// attribute to a host is a connection no rule can apply to. The improvement
// this lane claims is the decided column, not the answered one.
func TestStream_HowManyOutboundProtocolsAnEnvironmentCanCarry(t *testing.T) {
	probes := outboundProtocols()

	// The rows whose client sends nothing wait the whole peek timeout, and
	// waiting ten seconds each to learn something the sidecar already knows
	// is not a measurement, it is a delay.
	restore := streamPeekTimeout
	streamPeekTimeout = 500 * time.Millisecond
	t.Cleanup(func() { streamPeekTimeout = restore })

	// The ports that carried anything before this lane. The HTTP listeners
	// and the explicit proxy port, which is the whole set main() opened.
	beforePorts := map[int]bool{80: true, 443: true, schema.ProxyListenPort: true}
	before := 0
	for _, p := range probes {
		if beforePorts[p.port] {
			before++
		}
	}

	after := 0
	var rows []outcome
	for _, p := range probes {
		rows = append(rows, runProbe(t, p))
	}
	for _, r := range rows {
		if r.decided {
			after++
		}
	}

	var table strings.Builder
	fmt.Fprintf(&table, "\n%-24s %6s %9s %8s  %s\n",
		"PROTOCOL", "PORT", "ANSWERED", "DECIDED", "WHAT THE DECISION LOG SAYS")
	for _, r := range rows {
		fmt.Fprintf(&table, "%-24s %6d %9t %8t  %s\n",
			r.protocol, r.port, r.answered, r.decided, r.verdict)
	}
	fmt.Fprintf(&table,
		"\n%d of %d outbound protocols reach a policy decision. Before this lane: %d of %d.\n",
		after, len(probes), before, len(probes))
	t.Log(table.String())

	require.Equal(t, 0, before,
		"not one of these protocols had a listener before, which is the finding")
	require.Greater(t, after, before, "the number has to move")
	// Every row whose managed form is TLS is decidable and every cleartext row
	// is not, and the split is the honest half of the claim.
	for _, r := range rows {
		if strings.Contains(r.protocol, "TLS") {
			require.True(t, r.decided, "%s names its host in a handshake and must be decided", r.protocol)
		} else {
			require.False(t, r.decided,
				"%s carries no host name, so a decision about it would be invented", r.protocol)
		}
	}
}

// runProbe plays one protocol's opening bytes at the byte stream handler.
func runProbe(t *testing.T, p protocolProbe) outcome {
	t.Helper()
	// Each probe names its own destination and port. Opening a listener for
	// another host must not grant this probe access through default allow.
	host := p.sni
	if host == "" {
		host = "probe.test"
	}
	rules := []schema.EgressRule{{Host: net.JoinHostPort(host, strconv.Itoa(p.port)), Mode: schema.ModeAllow}}
	s := newSidecar(t, &schema.Egress{Default: schema.ModeAllow, Rules: rules})

	listening := false
	for _, proto := range schema.StreamPorts(rules) {
		if proto.Port == p.port {
			listening = true
		}
	}
	if !listening {
		return outcome{protocol: p.name, port: p.port, answered: false,
			verdict: "nothing listens on this port"}
	}
	proto := protocolAt(p.port)
	// Every name in the corpus points at loopback, which no rule names, so
	// the address guard refuses the dial. The decision is still made and
	// recorded, and no measurement run reaches Azure.
	s.resolve = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.IPv4(127, 0, 0, 1)}, nil
	}

	client, server := net.Pipe()
	go s.serveStream(proto)(server)

	go func() {
		if p.sni != "" {
			// A real handshake from crypto/tls rather than a literal, so the
			// bytes the sidecar parses are the bytes a client sends.
			_ = tls.Client(client, &tls.Config{
				ServerName:         p.sni,
				InsecureSkipVerify: true, //nolint:gosec // nothing answers; the handshake never completes
			}).Handshake()
			return
		}
		if len(p.opening) > 0 {
			_, _ = client.Write(p.opening)
		}
		// A protocol whose server speaks first writes nothing and waits.
	}()

	rec := s.waitFor(t, func(r record) bool { return r.Via == "stream" && r.Port == p.port })
	_ = client.Close()

	return outcome{
		protocol: p.name, port: p.port, answered: true,
		decided: rec.Host != "" && rec.Allowed,
		host:    rec.Host,
		verdict: firstSentence(rec.Reason),
	}
}

// protocolAt returns the table's entry for a port.
func protocolAt(port int) schema.StreamProtocol {
	for _, proto := range schema.ByteStreamProtocols {
		if proto.Port == port {
			return proto
		}
	}
	return schema.StreamProtocol{Port: port, Name: "an unnamed protocol"}
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

// The pair that makes the whole path evidence: an allowed broker is reached,
// and a blocked one is not, over the same protocol, through the same code,
// differing only in the name in the handshake.
//
// Without the control, a suite that only asserted the refusal would pass
// against a sidecar that refused everything, which is what the environment
// already did before this lane and is the failure being fixed.
func TestStream_AnAllowedHostIsReachedAndABlockedOneIsNot(t *testing.T) {
	broker := newRecordingBroker(t)

	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: net.JoinHostPort("allowed.broker.test", strconv.Itoa(broker.port)), Mode: schema.ModeAllow},
			// The loopback address the fixture lives on, named so the address
			// guard permits it. Naming an address is consent, which is the
			// only way to reach loopback through this sidecar.
			{Host: broker.host, Mode: schema.ModeAllow},
			{Host: "blocked.broker.test", Mode: schema.ModeBlock},
		},
	})
	s.resolve = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(broker.host)}, nil
	}
	proto := protocolAt(5671)
	// The sidecar dials the port it is serving, so a fixture that is not on
	// that port is never reached and the control below can only ever fail.
	// The protocol keeps AMQPS's name, which is what its refusals read, and
	// takes the fixture's port. Binding 5671 itself would trade this bug for
	// a test that fails whenever anything else on the machine holds the port.
	proto.Port = broker.port

	// The control. An allowed name reaches the broker and the broker says so.
	client, server := net.Pipe()
	go s.serveStream(proto)(server)
	go func() {
		_ = tls.Client(client, &tls.Config{
			ServerName:         "allowed.broker.test",
			InsecureSkipVerify: true, //nolint:gosec // the fixture is a raw socket, not a TLS server
		}).Handshake()
	}()
	require.True(t, broker.sawConnection(t),
		"an allowed host was not reached, so the byte stream path does not carry anything")
	_ = client.Close()

	allowed := s.waitFor(t, func(r record) bool { return r.Host == "allowed.broker.test" })
	require.True(t, allowed.Allowed)
	require.True(t, allowed.Stream, "the decision has to say nothing inside it was read")
	require.True(t, allowed.HostOnly)
	require.Equal(t, "stream", allowed.Via)

	// The attack. A blocked name over the same protocol, and the broker must
	// see nothing new.
	reached := broker.count()
	client2, server2 := net.Pipe()
	go s.serveStream(proto)(server2)
	go func() {
		_ = tls.Client(client2, &tls.Config{
			ServerName:         "blocked.broker.test",
			InsecureSkipVerify: true, //nolint:gosec // the connection is meant to be refused
		}).Handshake()
	}()
	blocked := s.waitFor(t, func(r record) bool { return r.Host == "blocked.broker.test" })
	_ = client2.Close()

	require.False(t, blocked.Allowed, "a blocked host was allowed over the byte stream path")
	require.Equal(t, reached, broker.count(),
		"the broker was reached by a connection the policy refused")
}

// A byte stream that names no host is refused, under default allow.
//
// The same reasoning as the transparent TLS listener and reached far more
// often here, because most of these protocols have a cleartext form that a
// developer tries first. Allowing it would be a hole exactly the shape of
// connecting by address and skipping the policy.
func TestStream_AConnectionThatNamesNoHostIsRefused(t *testing.T) {
	s := newSidecar(t, &schema.Egress{Default: schema.ModeAllow})
	proto := protocolAt(5671)

	client, server := net.Pipe()
	go s.serveStream(proto)(server)
	go func() {
		_ = tls.Client(client, &tls.Config{
			ServerName:         "",
			InsecureSkipVerify: true, //nolint:gosec // nothing answers
		}).Handshake()
	}()

	rec := s.waitFor(t, func(r record) bool { return r.Via == "stream" })
	_ = client.Close()

	require.False(t, rec.Allowed, "a connection naming no host was allowed under default allow")
	require.Empty(t, rec.Host, "there was no host to attribute it to, and none was invented")
	require.True(t, rec.Stream)
	require.Contains(t, rec.Reason, "no server name")
}

// A cleartext protocol is refused, and the refusal names the protocol and the
// way out.
//
// The message is the deliverable here as much as the refusal is. A developer
// whose RabbitMQ call fails reads a decision log, and "port 5672" tells them
// nothing while "AMQP carries no host name in its cleartext form, reach the
// broker over TLS" tells them what to change.
func TestStream_ACleartextProtocolIsRefusedAndSaysWhy(t *testing.T) {
	s := newSidecar(t, &schema.Egress{Default: schema.ModeAllow})
	proto := protocolAt(5672)

	client, server := net.Pipe()
	go s.serveStream(proto)(server)
	go func() { _, _ = client.Write([]byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1}) }()

	rec := s.waitFor(t, func(r record) bool { return r.Via == "stream" })
	_ = client.Close()

	require.False(t, rec.Allowed)
	require.Contains(t, rec.Reason, "AMQP")
	require.Contains(t, rec.Reason, "cleartext form")
	require.Contains(t, rec.Reason, "over TLS")
}

// The four modes a byte stream cannot honour are refused rather than treated
// as allow.
//
// This is the whole honesty requirement in one test. A mode accepted and
// ignored is a manifest that says one thing and an environment that does
// another, and for sandbox the ignored version puts the application's own
// credential on the wire to the real provider. So the assertion is not that
// the log says something, it is that the broker was never reached.
func TestStream_AModeThatCannotBeHonouredIsRefusedRatherThanAllowed(t *testing.T) {
	for _, mode := range []schema.Mode{
		schema.ModeCapture, schema.ModeMock, schema.ModeSynth, schema.ModeSandbox,
	} {
		t.Run(string(mode), func(t *testing.T) {
			broker := newRecordingBroker(t)
			s := newSidecar(t, &schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: net.JoinHostPort("broker.test", strconv.Itoa(broker.port)), Mode: mode, Credential: "AF_SANDBOX"},
					{Host: broker.host, Mode: schema.ModeAllow},
				},
			})
			s.resolve = func(_ context.Context, _ string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(broker.host)}, nil
			}

			client, server := net.Pipe()
			go s.serveStream(protocolAt(broker.port))(server)
			go func() {
				_ = tls.Client(client, &tls.Config{
					ServerName:         "broker.test",
					InsecureSkipVerify: true, //nolint:gosec // the connection is meant to be refused
				}).Handshake()
			}()

			rec := s.waitFor(t, func(r record) bool { return r.Host == "broker.test" })
			_ = client.Close()

			require.False(t, rec.Allowed,
				"%s was treated as allow on a connection nothing can read inside", mode)
			require.Equal(t, 0, broker.count(),
				"a %s rule reached the provider, which for sandbox means the application's own credential did", mode)
			require.Equal(t, string(schema.ModeBlock), rec.Mode, "refused modes must count as refused in the containment report")
			require.Contains(t, rec.Reason, string(mode))
			require.Contains(t, rec.Reason, "does not read inside")
		})
	}
}

// The port set the sidecar listens on excludes the ports HTTP already owns.
//
// Two listeners on one port is a startup failure, and a sidecar that dies at
// startup is an environment that never comes up, so this is a crash rather
// than a subtlety.
func TestStream_ThePortSetDoesNotCollideWithTheHTTPListeners(t *testing.T) {
	for _, proto := range schema.StreamPorts([]schema.EgressRule{
		{Host: "example.test:80"}, {Host: "example.test:443"},
		{Host: "example.test:3128"}, {Host: "example.test:53"},
	}) {
		require.NotContains(t, []int{80, 443, schema.ProxyListenPort, 53}, proto.Port,
			"the byte stream path would try to listen on a port an HTTP listener already owns")
	}
}

// A host allowed with no port opens no byte stream listener at all.
//
// This is the other direction of the rule above and it is the one that decides
// containment. The set used to be SEEDED from ByteStreamProtocols, so every
// environment answered on all sixteen ports whether or not a manifest had
// asked for one, and that is not merely an idle listener. A connection
// accepted here is decided on the name in its TLS handshake, and a rule that
// spells no port matches every port, so an allowed host's 6379 and its 25 were
// evaluated as allow and FORWARDED to that host. A manifest that named one
// host for HTTP silently carried that host's Redis and its mail.
//
// The escape probe in engine/internal/runtime/local found it as
// raw-socket-to-an-allowed-host and smtp-to-an-allowed-host, and it is worth
// saying why the fix belongs here rather than there. That probe's only signal
// is whether a connect succeeded, so widening the ports it tolerates would
// destroy its ability to say no about every port, for every future change,
// silently. The invariant it enforces is that a port nothing granted does not
// accept a connection, and this function is what grants one.
func TestStream_AHostAllowedWithNoPortOpensNoStreamListener(t *testing.T) {
	// The policy the escape probe runs under: a default of block and one host
	// allowed, naming no port.
	opened := schema.StreamPorts([]schema.EgressRule{
		{Host: "example.com", Mode: schema.ModeAllow},
	})
	ports := make([]int, 0, len(opened))
	for _, p := range opened {
		ports = append(ports, p.Port)
	}
	require.Empty(t, ports,
		"a manifest that named no port opened listeners on %v, and a connection "+
			"accepted on one of them is forwarded on the name in its handshake",
		ports)
}

// A port nobody standardised is reachable by declaring it.
func TestStream_APortNamedByARuleIsListenedOn(t *testing.T) {
	ports := schema.StreamPorts([]schema.EgressRule{{Host: "broker.test:31337"}})
	found := false
	for _, p := range ports {
		if p.Port == 31337 {
			found = true
		}
	}
	require.True(t, found, "a rule naming a port did not open a listener for it")
}

func TestStream_AnotherHostsPortDoesNotGrantAWebsiteRuleThatPort(t *testing.T) {
	assertStreamRefused(t, nil)
}

func TestStream_RequestScopedRulesCannotGrantOpaqueConnections(t *testing.T) {
	for _, scope := range []string{"path_allow", "method_allow", "path_block", "method_block", "sandbox_path"} {
		t.Run(scope, func(t *testing.T) {
			assertStreamRefused(t, func(port int) []schema.EgressRule {
				host := net.JoinHostPort("website.test", strconv.Itoa(port))
				rule := schema.EgressRule{Host: host, Mode: schema.ModeAllow}
				switch scope {
				case "path_allow":
					rule.Paths = []string{"/"}
				case "method_allow":
					rule.Methods = []string{"CONNECT"}
				case "path_block":
					rule.Mode = schema.ModeBlock
					rule.Paths = []string{"/private"}
				case "method_block":
					rule.Mode = schema.ModeBlock
					rule.Methods = []string{"POST"}
				case "sandbox_path":
					rule.Mode = schema.ModeSandbox
					rule.Paths = []string{"/private"}
					rule.Credential = "AF_SANDBOX"
				}
				return []schema.EgressRule{rule, {Host: host, Mode: schema.ModeAllow}}
			})
		})
	}
}

func assertStreamRefused(t *testing.T, extra func(int) []schema.EgressRule) {
	t.Helper()
	broker := newRecordingBroker(t)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: net.JoinHostPort("broker.test", strconv.Itoa(broker.port)), Mode: schema.ModeAllow},
			{Host: "website.test", Mode: schema.ModeAllow},
			{Host: broker.host, Mode: schema.ModeAllow},
		},
	})
	if extra != nil {
		rules := append(s.engine.Rules(), extra(broker.port)...)
		var err error
		s.engine, err = policy.New(&schema.Egress{Default: schema.ModeBlock, Rules: rules})
		require.NoError(t, err)
	}
	s.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(broker.host)}, nil
	}
	client, server := net.Pipe()
	go s.serveStream(protocolAt(broker.port))(server)
	go func() {
		defer func() { _ = client.Close() }()
		_ = client.SetDeadline(time.Now().Add(time.Second))
		_ = tls.Client(client, &tls.Config{
			ServerName:         "website.test",
			InsecureSkipVerify: true, //nolint:gosec // the fixture observes whether a connection is made
		}).Handshake()
	}()
	rec := s.waitFor(t, func(r record) bool { return r.Host == "website.test" })
	require.Zero(t, broker.count(), "a rule that cannot grant an opaque connection reached the broker")
	require.False(t, rec.Allowed, "the decision reported an ungranted stream as allowed")
	require.Equal(t, string(schema.ModeBlock), rec.Mode)
	require.Equal(t, 403, rec.Status)
	require.True(t, rec.Stream)
	require.True(t, rec.HostOnly)
	require.Equal(t, "stream", rec.Via)
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	var decision local.Decision
	require.NoError(t, json.Unmarshal(data, &decision))
	report := egress.Observe([]local.Decision{decision})
	require.Equal(t, 1, report.Refused)
	require.Zero(t, report.Allowed)
	require.Equal(t, 1, report.Stream)
	require.Equal(t, []string{"website.test"}, report.StreamHosts)
}

// recordingBroker is a raw TCP fixture that counts connections.
//
// Raw rather than a TLS server, because what is being asserted is whether the
// sidecar opened a connection to it at all. Whether the handshake inside then
// succeeds is the client's business and the broker's, and this path never
// touches it.
type recordingBroker struct {
	host string
	port int
	ln   net.Listener
	seen chan struct{}
}

func newRecordingBroker(t *testing.T) *recordingBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	host, portText, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	b := &recordingBroker{host: host, port: port, ln: ln, seen: make(chan struct{}, 32)}
	go func() {
		for {
			c, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			b.seen <- struct{}{}
			go func() {
				_, _ = io.Copy(io.Discard, c)
				_ = c.Close()
			}()
		}
	}()
	return b
}

func (b *recordingBroker) sawConnection(t *testing.T) bool {
	t.Helper()
	select {
	case <-b.seen:
		return true
	case <-time.After(10 * time.Second):
		return false
	}
}

func (b *recordingBroker) count() int { return len(b.seen) }

// The table is sorted and has no duplicate ports.
//
// Three processes read it and one of them opens a listener per entry, so a
// duplicate is a startup failure and an unsorted table makes two runs of the
// measurement above read as different.
func TestStream_ThePortTableIsSortedAndUnique(t *testing.T) {
	ports := make([]int, 0, len(schema.ByteStreamProtocols))
	for _, p := range schema.ByteStreamProtocols {
		require.NotEmpty(t, p.Name, "port %d has no name, so its refusal cannot say what it is", p.Port)
		ports = append(ports, p.Port)
	}
	require.True(t, sort.IntsAreSorted(ports), "the port table is not sorted")
	seen := map[int]bool{}
	for _, p := range ports {
		require.False(t, seen[p], "port %d appears twice", p)
		seen[p] = true
	}
}

// The broker receives the client's bytes and its reply reaches the same client.
// The TLS handshake is end to end, with the broker's certificate verified by
// the client. No certificate authority is installed on the sidecar.
func TestStream_AllowedBytesRoundTripWithoutTLSInterception(t *testing.T) {
	assertStreamRoundTrip(t, false)
}

func TestStream_InternalBytesRoundTripWithoutAnEgressGrant(t *testing.T) {
	assertStreamRoundTrip(t, true)
}

func assertStreamRoundTrip(t *testing.T, internal bool) {
	t.Helper()
	const name = "broker.test"
	certPEM, keyPEM, err := GenerateAuthority("stream-origin-fixture", time.Now())
	require.NoError(t, err)
	ca, err := newCertAuthority(certPEM, keyPEM)
	require.NoError(t, err)
	leaf, err := ca.leaf(name)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(certPEM)))
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*leaf}, MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	request := []byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0}
	reply := []byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0, 42}
	received := make(chan []byte, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		got := make([]byte, len(request))
		if _, readErr := io.ReadFull(conn, got); readErr != nil {
			return
		}
		received <- got
		_, _ = conn.Write(reply)
	}()
	s := newSidecar(t, &schema.Egress{Default: schema.ModeBlock, Rules: []schema.EgressRule{
		{Host: net.JoinHostPort(name, portText), Mode: schema.ModeAllow},
		{Host: host, Mode: schema.ModeAllow},
	}})
	if internal {
		s = newSidecar(t, &schema.Egress{Default: schema.ModeBlock})
		s.internal = newInside([]string{name})
		s.destinations = newDestinations(nil, "127.0.0.0/8", false)
	}
	s.resolve = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP(host)}, nil }
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go s.serveStream(protocolAt(port))(server)
	secure := tls.Client(client, &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
	_ = secure.SetDeadline(time.Now().Add(5 * time.Second))
	require.NoError(t, secure.Handshake(), "the broker's certificate must reach the client unchanged")
	_, err = secure.Write(request)
	require.NoError(t, err)
	got := make([]byte, len(reply))
	_, err = io.ReadFull(secure, got)
	require.NoError(t, err)
	require.Equal(t, reply, got)
	select {
	case upstream := <-received:
		require.Equal(t, request, upstream)
	case <-time.After(5 * time.Second):
		t.Fatal("the broker never received the client's bytes")
	}
	_ = secure.Close()
	rec := s.waitFor(t, func(r record) bool { return r.Host == name })
	if internal {
		require.Equal(t, "internal", rec.Via)
	} else {
		require.Equal(t, "stream", rec.Via)
	}
	require.True(t, rec.Allowed)
	require.True(t, rec.Stream)
	require.True(t, rec.HostOnly)
	require.Positive(t, rec.Bytes)
}

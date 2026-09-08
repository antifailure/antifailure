package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What the emulate route has to get right, and it is mostly about what it does
// NOT touch.
//
// The mode's whole claim is that the application is unchanged, so every
// assertion here is about a request arriving at the emulator looking exactly
// as the application sent it. The two headers that carry the most weight are
// the two easiest to rewrite by accident: Host, because a proxy that sets
// URL.Host usually sets the header with it, and Authorization, because the
// mode beside this one replaces it on purpose.

// fakeEmulatorServer stands in for LocalStack. It records what arrived.
type fakeEmulatorServer struct {
	*httptest.Server
	mu    sync.Mutex
	hosts []string
	auths []string
	paths []string
}

func newFakeEmulatorServer(t *testing.T) *fakeEmulatorServer {
	t.Helper()
	f := &fakeEmulatorServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hosts = append(f.hosts, r.Host)
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, "<ListBucketResult><Name>mybucket</Name></ListBucketResult>")
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeEmulatorServer) lastHost() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hosts) == 0 {
		return ""
	}
	return f.hosts[len(f.hosts)-1]
}

func (f *fakeEmulatorServer) lastAuth() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.auths) == 0 {
		return ""
	}
	return f.auths[len(f.auths)-1]
}

func (f *fakeEmulatorServer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hosts)
}

// emulatingSidecar builds a sidecar with one emulate rule and one emulator
// behind it.
//
// The subnet is loopback, which is how the address guard is told that the
// emulator is inside the environment. In a real run the emulator is on the
// environment's own network and the same predicate answers the same way; here
// the fixture is on 127.0.0.1 and would otherwise be refused as a confused
// deputy target, which is the guard working correctly.
func emulatingSidecar(
	t *testing.T, host string, addr string, emulator string,
) (*sidecar, *schema.Egress) {
	t.Helper()
	egress := &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: host, Mode: schema.ModeEmulate, Emulator: "localstack"},
		},
	}
	s := newSidecar(t, egress)
	eng, err := policy.New(egress)
	require.NoError(t, err)
	s.destinations = newDestinations(eng.Rules(), "127.0.0.0/8", eng.AllowsIPv6())
	s.transport.DialContext = s.dialGuarded
	if emulator != "" {
		s.emulators = map[string]emulatorRoute{emulator: {Address: addr}}
	}
	return s, egress
}

// answer is a response already read into memory.
//
// Read before the serving goroutine is joined, and not after. net.Pipe is
// unbuffered, so a body left on the wire while the writer closes its end is a
// body that arrives as io.ErrClosedPipe. That is the kind of flake that reads
// as a broken emulator.
type answer struct {
	status int
	header http.Header
	body   string
}

// sendEmulated drives one request through the inspected path, which is the
// path a real HTTPS call takes, and returns what the client received.
func sendEmulated(t *testing.T, s *sidecar, req *http.Request) answer {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = server.Close() }()
		host, _ := splitHostPort(req.Host, 443)
		s.serveInspected(server, req, host)
	}()

	_ = client.SetReadDeadline(time.Now().Add(20 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(client), req)
	require.NoError(t, err)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)
	_ = resp.Body.Close()
	<-done
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func TestEmulate_TheHostHeaderIsNotRewritten(t *testing.T) {
	t.Parallel()
	// The bucket lives in the hostname under virtual hosted addressing, so an
	// emulator told the host is af-emu-localstack:4566 has been told the
	// bucket is called af-emu. This is the single assertion that keeps S3
	// working through this mode.
	const host = "mybucket.s3.us-east-1.amazonaws.com"
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "*.s3.*.amazonaws.com", hostPortOf(t, fake.URL), "localstack")

	req, err := http.NewRequest(http.MethodGet, "https://"+host+"/key", nil)
	require.NoError(t, err)
	got := sendEmulated(t, s, req)

	require.Equal(t, host, fake.lastHost(),
		"the emulator was told a different host than the application asked for, which "+
			"renames every virtual hosted bucket")
	require.Contains(t, got.body, "ListBucketResult",
		"the emulator's answer did not reach the application")
}

func TestEmulate_TheAuthorizationHeaderIsForwardedUntouched(t *testing.T) {
	t.Parallel()
	// Sandbox mode replaces a credential because the request leaves the
	// environment. This one does not leave, so replacing it would break a
	// SigV4 signature for no benefit at all.
	// AKIDEXAMPLE, which is the key id AWS publishes in its own SigV4 test
	// vectors, and NOT an AKIA one. livekey recognises AKIA followed by at
	// least sixteen characters, so a plausible looking AKIATEST... vector
	// trips the wire, the request is refused before it is routed, and the
	// emulator sees nothing. The test then fails claiming the Authorization
	// header was rewritten, which is the opposite of what happened. That is
	// how this test failed the first time it ran.
	const signed = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260907/us-east-1/s3/" +
		"aws4_request, SignedHeaders=host;x-amz-date, Signature=abc123"
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "localstack")

	req, err := http.NewRequest(http.MethodGet, "https://s3.amazonaws.com/mybucket", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", signed)
	sendEmulated(t, s, req)

	require.Equal(t, signed, fake.lastAuth(),
		"the Authorization header was changed on the way to the emulator")
}

func TestEmulate_TheDecisionRecordsTheKeyIDAndNeverTheSecret(t *testing.T) {
	t.Parallel()
	// An emulator verifies no signature, so the only thing between a
	// misconfigured application and a real cloud account is the tripwire, and
	// a refusal is auditable only if the ACCEPTED requests say which key they
	// carried.
	const keyID = "AKIDEXAMPLE"
	// A distinctive string rather than a credential shaped one. What this
	// test asserts is that the value does not reach the log, and any value
	// nothing else writes proves that; a literal shaped like an AWS secret
	// would be refused by the repository scanner for looking like one.
	const secret = "af-conformance-this-must-not-reach-the-log"
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "localstack")

	req, err := http.NewRequest(http.MethodGet, "https://s3.amazonaws.com/mybucket", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+keyID+
		"/20260907/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=deadbeef")
	req.Header.Set("X-Amz-Content-Sha256", secret)
	sendEmulated(t, s, req)

	var decided *record
	for _, r := range s.decisions() {
		if r.Event == "decision" && r.Mode == string(schema.ModeEmulate) {
			d := r
			decided = &d
		}
	}
	require.NotNil(t, decided, "the emulated request produced no decision line")
	require.Equal(t, "localstack", decided.Emulator,
		"the decision does not say which emulator answered, so an environment running two "+
			"of them has a log nobody can read")
	require.Equal(t, keyID, decided.KeyID,
		"the decision does not record the key id the request was signed with")

	raw, err := json.Marshal(decided)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret,
		"the decision log carries the secret, which puts a credential in the logs of the "+
			"thing that exists to keep credentials out of them")
}

func TestEmulate_ARuleNamingAnEmulatorThatIsNotRunningIsRefused(t *testing.T) {
	t.Parallel()
	// Refused rather than forwarded somewhere plausible, and refused rather
	// than blocked. Blocking would read as a missing egress rule and send
	// somebody to edit the manifest they already wrote correctly.
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "azurite")

	req, err := http.NewRequest(http.MethodGet, "https://s3.amazonaws.com/mybucket", nil)
	require.NoError(t, err)
	got := sendEmulated(t, s, req)

	require.Equal(t, http.StatusBadGateway, got.status)
	require.Contains(t, got.body, "localstack",
		"the refusal does not name the emulator the rule asked for")
	require.Contains(t, got.body, "azurite",
		"the refusal does not list what IS running, so a typo and a build with nothing "+
			"registered read identically")
	require.Zero(t, fake.calls(),
		"the request reached an emulator the rule did not name")
}

func TestEmulate_TheRefusalSaysSoWhenNothingIsRegistered(t *testing.T) {
	t.Parallel()
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "")

	req, err := http.NewRequest(http.MethodGet, "https://s3.amazonaws.com/mybucket", nil)
	require.NoError(t, err)
	got := sendEmulated(t, s, req)

	require.Contains(t, got.body, "no emulators at all",
		"a build with nothing registered gets the same message as a typo, and the two "+
			"want completely different next steps")
}

func TestEmulate_ALiveCredentialIsRefusedBeforeTheEmulatorSeesIt(t *testing.T) {
	t.Parallel()
	// The tripwire runs before the mode is acted on, in every mode. An
	// emulator verifies nothing, so a request that reached it would have put
	// a production credential into a third party container's memory and logs.
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "localstack")

	req, err := http.NewRequest(http.MethodGet, "https://s3.amazonaws.com/mybucket", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIA"+strings.Repeat("A", 16)+
		"/20260907/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=deadbeef")
	got := sendEmulated(t, s, req)

	require.Equal(t, http.StatusForbidden, got.status)
	require.Zero(t, fake.calls(),
		"a live credential reached the emulator")
}

func TestEmulate_ThePlainProxyPathRoutesToo(t *testing.T) {
	t.Parallel()
	// The third path, and the one that has been forgotten twice. Whether an
	// application's HTTP library honours http_proxy is not a property anybody
	// reasons about while writing a rule, and it must not decide whether a
	// mode works.
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "localstack")

	front := httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(front.Close)

	proxyURL, err := url.Parse(front.URL)
	require.NoError(t, err)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   20 * time.Second,
	}
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://s3.amazonaws.com/mybucket", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "ListBucketResult",
		"a client that reads its proxy variables did not reach the emulator")
	require.Equal(t, "s3.amazonaws.com", fake.lastHost(),
		"the plain proxy path rewrote the host header the inspected path preserves")
}

func TestAccessKeyID_ReadsTheIdentifierAndNeverTheSecret(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, header, want string
	}{
		{"sigv4", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260907/us-east-1/" +
			"s3/aws4_request, SignedHeaders=host, Signature=abc", "AKIDEXAMPLE"},
		{"sigv4 with no scope", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE", "AKIDEXAMPLE"},
		{"azure shared key", "SharedKey devstoreaccount1:ZmFrZXNpZ25hdHVyZQ==", "devstoreaccount1"},
		{"azure shared key lite", "SharedKeyLite myaccount:ZmFrZQ==", "myaccount"},
		// A bearer token is a secret in its entirety, so there is no
		// identifier to record and guessing one would put the token in the
		// log. Empty is the right answer and it is asserted rather than left
		// to be discovered.
		{"bearer", "Bearer sk-live-000000000000", ""},
		{"empty", "", ""},
		{"unrecognised", "Basic dXNlcjpwYXNz", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := accessKeyID(c.header)
			require.Equal(t, c.want, got)
			if c.header != "" && got != "" {
				require.NotContains(t, c.header[strings.Index(c.header, got)+len(got):], got,
					"the identifier appears twice, so this vector cannot tell an identifier "+
						"from a substring of the signature")
			}
		})
	}
}

func hostPortOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Host
}

// The claim, proved against a real TLS handshake.
//
// Everything else in this file drives serveInspected directly, which is the
// function a terminated connection feeds and is therefore the right unit. This
// one does the handshake as well, because the sentence being sold is "your SDK
// is configured for production and stays that way", and the half of it that
// nobody would believe on a diagram is that a client CHECKING the certificate
// for s3.amazonaws.com accepts what it is given.
//
// The client here verifies. There is no InsecureSkipVerify, because a test
// that turned verification off would prove the one thing that does not need
// proving.
func TestEmulate_AVerifyingClientReachesTheEmulatorAtTheProvidersHostname(t *testing.T) {
	certPEM, keyPEM, err := GenerateAuthority("emulate", time.Now())
	require.NoError(t, err)

	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "localstack")
	s.ca, err = newCertAuthority(certPEM, keyPEM)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(certPEM)))

	client, server := net.Pipe()
	go s.serveTransparentTLS(server)

	answer := make(chan string, 1)
	go func() {
		// ServerName is the provider's own hostname, which is what an
		// unmodified SDK sends. Nothing here knows an emulator exists.
		conn := tls.Client(client, &tls.Config{
			ServerName: "s3.amazonaws.com", RootCAs: roots, MinVersion: tls.VersionTLS12,
		})
		if hsErr := conn.Handshake(); hsErr != nil {
			answer <- "handshake: " + hsErr.Error()
			return
		}
		if _, wErr := io.WriteString(conn,
			"GET /mybucket HTTP/1.1\r\nHost: s3.amazonaws.com\r\nConnection: close\r\n\r\n"); wErr != nil {
			answer <- "write: " + wErr.Error()
			return
		}
		body, _ := io.ReadAll(io.LimitReader(conn, 1<<16))
		answer <- string(body)
	}()

	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	got := <-answer
	_ = client.Close()

	require.Contains(t, got, "ListBucketResult",
		"a client that verified the certificate for s3.amazonaws.com did not receive the "+
			"emulator's answer. This is the whole claim: no endpoint override, no client "+
			"built differently for tests, and the emulator answers anyway.\n%s", got)
	require.Equal(t, "s3.amazonaws.com", fake.lastHost(),
		"the emulator was told a different host than the client asked for")
}

func TestEmulate_AnOversizedBodyOnThePlainPortIsRefusedRatherThanTruncated(t *testing.T) {
	t.Parallel()
	// The failure this closes is silent and permanent. Capture can lose the
	// tail of a message and the worst case is a log entry somebody reads. An
	// emulated PUT that lost its tail is an object the emulator now holds,
	// wrong, with a 200 in front of it, and every later read of that object
	// agrees with itself.
	fake := newFakeEmulatorServer(t)
	s, _ := emulatingSidecar(t, "s3.amazonaws.com", hostPortOf(t, fake.URL), "localstack")

	front := httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(front.Close)

	proxyURL, err := url.Parse(front.URL)
	require.NoError(t, err)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   30 * time.Second,
	}
	big := strings.Repeat("x", insideBodyLimit+1024)
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPut, "http://s3.amazonaws.com/mybucket/key", strings.NewReader(big))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode,
		"an oversized emulated body was accepted, which means it was cut")
	require.Zero(t, fake.calls(),
		"a truncated body reached the emulator, so it is now holding a short object "+
			"behind a success")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "Use https",
		"the refusal does not name the way out, and there is one: the inspected path "+
			"streams the body through with no limit")
}

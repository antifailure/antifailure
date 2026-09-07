package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A name inside the environment is not egress, whichever door it arrives
// through.
//
// The resolver has always treated a single label name as internal and
// forwarded the lookup, so a service calling another service, or the database,
// or a datastore, resolves to the real container and never comes near this
// port. A client that reads http_proxy and ignores no_proxy sends it here
// anyway, and busybox wget is exactly that client: measured, it posts to the
// proxy for a host no_proxy names.
//
// Before this the sidecar evaluated such a request against the egress policy
// and refused it, so whether one part of an environment could reach another
// depended on which HTTP library the application happened to use. That is the
// same shape as the two defects already fixed on these paths: a mode that
// worked or did not depending on the client.

// TestInternal_APlainRequestForAnEnvironmentNameIsForwarded is the case the
// events store hits.
func TestInternal_APlainRequestForAnEnvironmentNameIsForwarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "3")
	}))
	t.Cleanup(upstream.Close)
	_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
	require.NoError(t, err)

	// Default block, which is what an environment with no egress rules has,
	// and the host is named in the internal list the runtime writes.
	s := newSidecar(t, &schema.Egress{Default: schema.ModeBlock})
	s.proxy.internal = newInside([]string{"events"})
	// The environment's own network, which is loopback here because that is
	// where the fixture is. It is what makes the address internal as well as
	// the name: the guard permits the environment's own subnet, and the
	// internal path requires the name to resolve into it.
	s.proxy.destinations = newDestinations(nil, "127.0.0.0/8", false)
	// The name has to resolve to the fixture, because the whole point is that
	// the sidecar dials what the environment's resolver would have returned.
	s.proxy.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.IPv4(127, 0, 0, 1)}, nil
	}

	body, status := requestThrough(t, s, "http://events:"+port+"/?query=SELECT+count()")
	require.Equal(t, http.StatusOK, status,
		"a request to a name inside the environment was refused as egress, so a client that "+
			"ignores no_proxy cannot reach the environment's own store")
	require.Equal(t, "3", body)

	rec := s.waitFor(t, func(r record) bool { return r.Host == "events" })
	require.True(t, rec.Allowed)
	require.Equal(t, "internal", rec.Via,
		"the decision log has to say the request was internal, or nobody reading it can "+
			"tell an internal call from one the policy allowed")
	require.Contains(t, rec.Reason, "inside the environment")
}

// TestInternal_AnExternalNameIsStillDecided is the control.
//
// Without it the test above would pass against a sidecar that forwarded
// everything, which is the failure this whole component exists to prevent.
func TestInternal_AnExternalNameIsStillDecided(t *testing.T) {
	s := newSidecar(t, &schema.Egress{Default: schema.ModeBlock})
	s.proxy.internal = newInside([]string{"events"})

	_, status := requestThrough(t, s, "http://api.stripe.com/v1/charges")
	require.Equal(t, http.StatusForbidden, status,
		"an external host was forwarded, so the internal case is a hole rather than an "+
			"exception")
}

// TestInternal_ANameThatIsInternalByShapeAndResolvesOutsideIsStillDecided is
// the hole this path would otherwise open.
//
// A name with no dot is internal by shape, and a name is not an address. On a
// machine whose resolver carries a search domain, `reports` resolves to
// something on the corporate network, and the sidecar is the one thing in an
// environment with a route out. So the internal path requires the name to
// resolve INTO the environment's own network, and a name that does not falls
// through to the policy rather than being forwarded by an exception.
func TestInternal_ANameThatIsInternalByShapeAndResolvesOutsideIsStillDecided(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "reached the corporate network")
	}))
	t.Cleanup(upstream.Close)
	_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
	require.NoError(t, err)

	s := newSidecar(t, &schema.Egress{Default: schema.ModeBlock})
	s.proxy.internal = newInside(nil)
	// The environment's network is somewhere else entirely, so the fixture on
	// loopback is outside it.
	s.proxy.destinations = newDestinations(
		[]schema.EgressRule{{Host: "127.0.0.1"}}, "10.99.0.0/16", false)
	s.proxy.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.IPv4(127, 0, 0, 1)}, nil
	}

	body, status := requestThrough(t, s, "http://reports:"+port+"/")
	require.Equal(t, http.StatusForbidden, status,
		"a single label name that resolves outside the environment was forwarded without a "+
			"policy decision, which is an escape wearing an internal name")
	require.NotContains(t, body, "reached the corporate network")
}

// TestInside_ASingleLabelIsInsideAndADottedNameIsNot is the predicate itself.
func TestInside_ASingleLabelIsInsideAndADottedNameIsNot(t *testing.T) {
	in := newInside([]string{"db", "events"})
	for _, name := range []string{"db", "events", "web", "localhost", "anything.localhost"} {
		require.True(t, in.has(name), "%s is inside the environment", name)
	}
	for _, name := range []string{
		"api.stripe.com", "events.example.com", "169.254.169.254.nip.io",
	} {
		require.False(t, in.has(name), "%s is not inside the environment", name)
	}
}

// requestThrough sends one request to the explicit proxy port.
func requestThrough(t *testing.T, s *sidecar, target string) (string, int) {
	t.Helper()
	front := httptest.NewServer(s.proxy)
	t.Cleanup(front.Close)

	proxyURL, err := url.Parse(front.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	resp, err := client.Get(target)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body), resp.StatusCode
}

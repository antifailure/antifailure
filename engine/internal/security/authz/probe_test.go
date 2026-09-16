package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/authz"
)

const probeMarker = "CANARY-org-a-2f9c11"

// goldenWith builds a golden view carrying one planted marker, which is what the
// unauthenticated probe recognises as protected content leaking to anon.
func goldenWith(value string) security.GoldenView {
	return security.NewGoldenView([]security.Canary{{Tenant: "org_a", Kind: "secret", Value: value}})
}

func endpointInput(baseURL, ref string, g security.GoldenView) security.Input {
	return security.Input{
		Targets: []change.Target{{Kind: change.TargetEndpoint, Ref: ref, Because: []string{"api/orders: an added route"}}},
		Env:     security.Environment{BaseURL: baseURL},
		Golden:  g,
		Policy:  failPolicy(),
	}
}

// The vulnerable twin returns a planted marker to an unauthenticated caller on
// an added endpoint. Probe must surface it as an unauthenticated_access finding
// whose where is the route, with no value in any field.
func TestProbe_LeakyEndpointFires(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/orders" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"orders":[{"id":"invoice-99887766","token":"` + probeMarker + `"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := authz.New()
	got, err := f.Probe(context.Background(), endpointInput(srv.URL, "/api/orders", goldenWith(probeMarker)))
	require.NoError(t, err)
	require.Len(t, got, 1, "an unauthenticated caller received planted content")
	require.Equal(t, authz.RuleUnauthenticatedAccess, got[0].Rule)
	require.Equal(t, "/api/orders", got[0].Where)
	require.NotContains(t, got[0].Detail, probeMarker, "the finding must never carry the leaked value")
	require.NotContains(t, got[0].Detail, "invoice-99887766", "the finding must never carry a raw id")
}

// The safe twin refuses the unauthenticated caller. There is no finding, because
// the boundary held.
func TestProbe_ProtectedEndpointIsClean(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/orders" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := authz.New()
	got, err := f.Probe(context.Background(), endpointInput(srv.URL, "/api/orders", goldenWith(probeMarker)))
	require.NoError(t, err)
	require.Empty(t, got, "a 401 to an unauthenticated caller is the boundary holding")
}

// A 200 that returns content WITHOUT the planted marker is not a leak: the
// endpoint served public content, which is what most 200s are. Keying on the
// marker rather than on the status is what keeps the false positive rate
// survivable.
func TestProbe_PublicContentWithoutMarkerIsClean(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"welcome"}`))
	}))
	defer srv.Close()

	f := authz.New()
	got, err := f.Probe(context.Background(), endpointInput(srv.URL, "/api/orders", goldenWith(probeMarker)))
	require.NoError(t, err)
	require.Empty(t, got, "a 200 without the planted marker is not a leak")
}

// A conventionally public route that leaks the marker is still not flagged: an
// anonymous reach of a public surface is expected.
func TestProbe_PublicRouteNeverFlags(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(probeMarker))
	}))
	defer srv.Close()

	f := authz.New()
	got, err := f.Probe(context.Background(), endpointInput(srv.URL, "/health", goldenWith(probeMarker)))
	require.NoError(t, err)
	require.Empty(t, got, "a public route reached by anon is expected, even if it carries a marker")
}

// With no planted markers the detector cannot be proven, so the family stays
// silent rather than guess that a 200 body was protected. Silence here is the
// honest answer, and it is why the probe never false-fires on an app with no
// golden.
func TestProbe_NoMarkersMeansNoGuessing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"anything":"here"}`))
	}))
	defer srv.Close()

	f := authz.New()
	in := endpointInput(srv.URL, "/api/orders", security.NewGoldenView(nil))
	got, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, got, "without a proven detector the family does not guess a leak")
}

// An unreachable twin is a BLOCKED probe: an error, never a pass with no
// findings. Reporting it as a clean run is the single most damaging answer this
// family could give.
func TestProbe_UnreachableTwinIsAnError(t *testing.T) {
	t.Parallel()
	f := authz.New()
	// Port 1 refuses immediately on this machine, so the twin is unreachable.
	in := endpointInput("http://127.0.0.1:1", "/api/orders", goldenWith(probeMarker))
	got, err := f.Probe(context.Background(), in)
	require.Error(t, err, "an unreachable twin blocks the run rather than passing it")
	require.Nil(t, got)
}

// A target whose route is a framework route file is mapped to the served route
// and probed there, so a diff that names a handler file still exercises the
// endpoint.
func TestProbe_DerivesRouteFromAFrameworkFile(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/orders" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(probeMarker))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := authz.New()
	in := endpointInput(srv.URL, "web/apps/api/app/api/orders/route.ts", goldenWith(probeMarker))
	got, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, got, 1, "the route.ts file maps to /api/orders and the leak there is found")
	require.Equal(t, "/api/orders", got[0].Where)
}

// A parameterised route file maps to the collection prefix, because an
// unauthenticated probe has no id to fill the parameter with and a collection
// that leaks to anon is the hole either way.
func TestProbe_DynamicSegmentBecomesCollection(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/orders" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(probeMarker))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := authz.New()
	in := endpointInput(srv.URL, "web/apps/api/app/api/orders/[id]/route.ts", goldenWith(probeMarker))
	got, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "/api/orders", got[0].Where)
}

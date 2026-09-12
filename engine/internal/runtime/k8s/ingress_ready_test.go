package k8s

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestIngressReadinessWaitsForTheBackendAfterPodsAreReady(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(404)
			return
		}
		if requests.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	require.NoError(t, waitForIngress(context.Background(), server.URL, "health", 2*time.Second))
	require.EqualValues(t, 3, requests.Load())
}

func TestUnavailableIngressNeverBecomesReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	err := waitForIngress(context.Background(), server.URL, "", 25*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestIngressReadinessDoesNotFollowAuthenticationRedirects(t *testing.T) {
	var followed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			followed.Store(true)
			w.WriteHeader(503)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer server.Close()
	require.NoError(t, waitForIngress(context.Background(), server.URL, "", time.Second))
	require.False(t, followed.Load())
}

// TestAPublishedURLIsProbedBeforeTheServiceIsReported is the cell the three
// tests above cannot cover between them.
//
// THE DEFECT they would all have missed. waitForIngress is correct and was
// reachable from nothing for as long as it took to write it. Every test here
// calls it directly, so deleting its one call site in startService leaves this
// file entirely green while `af up` goes back to printing a URL that serves
// "no available server" and a pull request comment goes back to linking to it.
// A real cluster catches that, in Up_ReportsAReachableURL, which is the run
// that takes half an hour on a runner nobody has locally. This one takes
// milliseconds and asserts the call, its arguments and its consequence.
func TestAPublishedURLIsProbedBeforeTheServiceIsReported(t *testing.T) {
	r, _ := startupAPI(t, func(_ int32, w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			// Echo the object back, which is what the API server does and
			// what the client decodes the created resource from. Read it
			// whole first: io.Copy of the request body straight into the
			// response writer reaches its ReadFrom path and arrives empty,
			// which the client reports as a missing version and kind.
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			_, _ = w.Write(body)
			return
		}
		sendStartupPods(w, startupPod(true, true))
	})
	r.proxyRef, r.domain = "trusted-sidecar:build", "127.0.0.1.sslip.io"
	s := provider.ServiceSpec{Name: "web", Kind: "web", Image: "customer:web", Port: 8080, HealthPath: "healthz"}
	spec := provider.EnvSpec{EnvID: "urlprobe", Services: []provider.ServiceSpec{s}}
	journal := func(string, string) error { return nil }

	var asked []string
	r.ingressProbe = func(_ context.Context, address, path string, _ time.Duration) error {
		asked = append(asked, address+" "+path)
		return errors.New("no available server")
	}
	running, err := r.startService(context.Background(), spec, s, "urlprobe", "10.43.0.9", journal, func(string) {})
	require.Error(t, err)
	require.Equal(t, []string{"http://urlprobe-web.127.0.0.1.sslip.io healthz"}, asked,
		"the probe has to be given the URL the runtime is about to report, and the declared health path")

	r.ingressProbe = func(context.Context, string, string, time.Duration) error { return nil }
	running, err = r.startService(context.Background(), spec, s, "urlprobe", "10.43.0.9", journal, func(string) {})
	require.NoError(t, err)
	require.True(t, running.Ready)
}

// TestAServiceWithNoPublishedURLIsNotProbed keeps the check above from turning
// into a wait every worker pays. A service with no domain has no URL to probe,
// and a probe of the empty string would hang until the health timeout.
func TestAServiceWithNoPublishedURLIsNotProbed(t *testing.T) {
	r, _ := startupAPI(t, func(_ int32, w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			_, _ = w.Write(body)
			return
		}
		sendStartupPods(w, startupPod(true, true))
	})
	r.proxyRef = "trusted-sidecar:build"
	s := provider.ServiceSpec{Name: "web", Kind: "web", Image: "customer:web", Port: 8080}
	spec := provider.EnvSpec{EnvID: "nourl", Services: []provider.ServiceSpec{s}}
	r.ingressProbe = func(context.Context, string, string, time.Duration) error {
		t.Error("a service the runtime published no URL for must not be probed")
		return nil
	}
	running, err := r.startService(context.Background(), spec, s, "nourl",
		"10.43.0.9", func(string, string) error { return nil }, func(string) {})
	require.NoError(t, err)
	require.Empty(t, running.URL)
	require.True(t, running.Ready)
}

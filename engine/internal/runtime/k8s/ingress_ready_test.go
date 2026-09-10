package k8s

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
)

// The polling loop, against a server that answers the way the real one does.
//
// Everything here is about the loop rather than the transport, because the loop
// is where a device grant client goes wrong: it either gives up on the first
// "not yet" or it ignores slow_down, hammers the endpoint, gets rate limited,
// and reports a failure that looks like the server being broken.
//
// The sleeps are injected, so these run in microseconds and still assert the
// real intervals. A test that slept for the true five seconds is a test
// somebody eventually deletes for being slow, and then nothing covers this.

type scriptedServer struct {
	t       *testing.T
	replies []func(w http.ResponseWriter)
	calls   int
}

func (s *scriptedServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device/code", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"device_code": "dc_test", "user_code": "BCDF-GHJK",
			"verification_uri":          "https://cp.test/device",
			"verification_uri_complete": "https://cp.test/device?code=BCDF-GHJK",
			"expires_in":                900, "interval": 5,
		})
	})
	mux.HandleFunc("/auth/device/token", func(w http.ResponseWriter, _ *http.Request) {
		if s.calls >= len(s.replies) {
			s.t.Fatalf("the client polled %d times, more than the script has answers for", s.calls+1)
		}
		reply := s.replies[s.calls]
		s.calls++
		reply(w)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func pending(w http.ResponseWriter) {
	writeJSON(w, 400, map[string]string{"error": "authorization_pending", "error_description": "Waiting."})
}
func slowDown(w http.ResponseWriter) {
	writeJSON(w, 400, map[string]string{"error": "slow_down", "error_description": "Too fast."})
}
func granted(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]any{
		"access_token": "afu_granted", "token_type": "Bearer",
		"expires_in": 7776000, "scope": "environments.view runs.view",
	})
}

func TestPollWaitsAndThenReceivesTheToken(t *testing.T) {
	s := &scriptedServer{t: t, replies: []func(http.ResponseWriter){pending, pending, granted}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	client := auth.NewClient(srv.URL)
	start, err := client.Begin(context.Background(), "a laptop", nil)
	require.NoError(t, err)
	require.Equal(t, "BCDF-GHJK", start.UserCode)

	var slept []time.Duration
	tok, err := client.Poll(context.Background(), start, func(d time.Duration) { slept = append(slept, d) }, nil)
	require.NoError(t, err)
	require.Equal(t, "afu_granted", tok.AccessToken)

	// Two waits for two "not yet" answers, at the interval the server named.
	require.Equal(t, []time.Duration{5 * time.Second, 5 * time.Second}, slept)
}

// RFC 8628: slow_down means add five seconds and keep going. Getting this wrong
// is the difference between a login that completes and one that gets rate
// limited by the endpoint it is hammering.
func TestSlowDownLengthensTheInterval(t *testing.T) {
	s := &scriptedServer{t: t, replies: []func(http.ResponseWriter){pending, slowDown, slowDown, granted}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	client := auth.NewClient(srv.URL)
	start, err := client.Begin(context.Background(), "a laptop", nil)
	require.NoError(t, err)

	var slept []time.Duration
	_, err = client.Poll(context.Background(), start, func(d time.Duration) { slept = append(slept, d) }, nil)
	require.NoError(t, err)

	// 5 for the pending, then 10 and 15 as each slow_down adds five. A client
	// that ignored slow_down would show 5, 5, 5 here.
	require.Equal(t, []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second}, slept)
}

func TestADeclinedLoginStopsImmediately(t *testing.T) {
	s := &scriptedServer{t: t, replies: []func(http.ResponseWriter){
		pending,
		func(w http.ResponseWriter) {
			writeJSON(w, 400, map[string]string{"error": "access_denied", "error_description": "Declined."})
		},
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	client := auth.NewClient(srv.URL)
	start, _ := client.Begin(context.Background(), "a laptop", nil)
	_, err := client.Poll(context.Background(), start, func(time.Duration) {}, nil)
	// Distinct from a timeout: the person said no, and telling them to wait
	// longer would be wrong.
	require.ErrorIs(t, err, auth.ErrDeclined)
	require.Equal(t, 2, s.calls, "the client kept polling after being declined")
}

func TestAnExpiredRequestStopsImmediately(t *testing.T) {
	s := &scriptedServer{t: t, replies: []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			writeJSON(w, 400, map[string]string{"error": "expired_token", "error_description": "Gone."})
		},
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	client := auth.NewClient(srv.URL)
	start, _ := client.Begin(context.Background(), "a laptop", nil)
	_, err := client.Poll(context.Background(), start, func(time.Duration) {}, nil)
	require.ErrorIs(t, err, auth.ErrLoginExpired)
	require.Equal(t, 1, s.calls)
}

// A cancelled context stops the loop. Somebody pressing ctrl-c during a login
// must not have to wait out the fifteen minute expiry.
func TestPollStopsWhenTheContextIsCancelled(t *testing.T) {
	s := &scriptedServer{t: t, replies: []func(http.ResponseWriter){pending, pending, pending, pending}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := auth.NewClient(srv.URL)
	start, _ := client.Begin(ctx, "a laptop", nil)

	calls := 0
	_, err := client.Poll(ctx, start, func(time.Duration) {
		calls++
		if calls == 2 {
			cancel()
		}
	}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

// An approval that returns no token is a server bug, and the client has to say
// so rather than storing an empty credential that fails mysteriously later.
func TestAnEmptyTokenIsRefused(t *testing.T) {
	s := &scriptedServer{t: t, replies: []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			writeJSON(w, 200, map[string]any{"token_type": "Bearer"})
		},
	}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	client := auth.NewClient(srv.URL)
	start, _ := client.Begin(context.Background(), "a laptop", nil)
	_, err := client.Poll(context.Background(), start, func(time.Duration) {}, nil)
	require.ErrorContains(t, err, "returned no token")
}

func TestWhoamiReportsNotSignedInFor401(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 401, map[string]string{"error": "This token is not valid."})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := auth.NewClient(srv.URL).Whoami(context.Background(), "afu_revoked")
	// Typed, so af whoami can say "run af login" instead of printing a status
	// code at somebody.
	require.ErrorIs(t, err, auth.ErrNotSignedIn)
}

func TestWhoamiSendsTheToken(t *testing.T) {
	var seen string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("authorization")
		writeJSON(w, 200, map[string]any{
			"login": "somebody", "organization": "antifailure", "role": "admin",
			"scopes": []string{"environments.view"}, "tokenPrefix": "afu_abcdefgh",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id, err := auth.NewClient(srv.URL).Whoami(context.Background(), "afu_live")
	require.NoError(t, err)
	require.Equal(t, "Bearer afu_live", seen)
	require.Equal(t, "antifailure", id.Organization)
	require.Equal(t, "admin", id.Role)
}

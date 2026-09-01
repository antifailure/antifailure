package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// probeAgainst points a configuration at a test server and probes it.
//
// A real HTTP server rather than a stubbed round tripper. The thing being
// tested is how this reads what a provider actually sends back, including the
// headers it authenticates with and the status codes it distinguishes, and a
// stub would be a test of the stub.
func probeAgainst(t *testing.T, provider string, key string, h http.HandlerFunc) model.Result {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	p, ok := model.Lookup(provider)
	require.True(t, ok)
	cfg := model.Config{
		Provider: p,
		Key:      secrets.New(key),
		Model:    "test-model",
		BaseURL:  srv.URL,
	}
	return model.Probe(context.Background(), srv.Client(), cfg, time.Now)
}

func anthropicOK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "application/json")
	_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"hi"}]}`))
}

func TestProbe_SuccessSendsTheProviderAuthentication(t *testing.T) {
	t.Parallel()

	t.Run("anthropic", func(t *testing.T) {
		t.Parallel()
		var gotKey, gotVersion, gotModel string
		var gotMaxTokens float64
		res := probeAgainst(t, "anthropic", "sk-ant-key", func(w http.ResponseWriter, r *http.Request) {
			gotKey = r.Header.Get("x-api-key")
			gotVersion = r.Header.Get("anthropic-version")
			require.Equal(t, "/v1/messages", r.URL.Path)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			gotModel, _ = body["model"].(string)
			gotMaxTokens, _ = body["max_tokens"].(float64)
			anthropicOK(w, r)
		})
		require.True(t, res.OK(), res.Detail)
		require.Equal(t, "sk-ant-key", gotKey)
		require.Equal(t, "2023-06-01", gotVersion)
		require.Equal(t, "test-model", gotModel)
		// One token, because this is meant to be run whenever somebody is
		// unsure and a check people avoid because of the price is a check
		// nobody runs.
		require.Equal(t, float64(1), gotMaxTokens)
	})

	t.Run("openai", func(t *testing.T) {
		t.Parallel()
		var gotAuth string
		res := probeAgainst(t, "openai", "sk-oai-key", func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("authorization")
			require.Equal(t, "/v1/chat/completions", r.URL.Path)
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
		})
		require.True(t, res.OK(), res.Detail)
		require.Equal(t, "Bearer sk-oai-key", gotAuth)
	})
}

// Every failure this can tell apart, told apart. A revoked key, an empty
// balance, a model that does not exist, a throttle and an outage all fail, they
// all have different fixes, and being told only that the call failed sends
// somebody to the wrong one first.
func TestProbe_ClassifiesEachFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		status   int
		body     string
		want     model.Outcome
		nextStep string
	}{
		{
			name:     "a revoked or wrong key",
			status:   401,
			body:     `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			want:     model.OutcomeKeyRejected,
			nextStep: "af model set anthropic",
		},
		{
			// Anthropic reports an exhausted balance as a 400, which is the
			// same status it uses for a malformed request. Telling somebody to
			// check their request when their balance is empty wastes an
			// afternoon.
			name:     "an empty balance reported as a 400",
			status:   400,
			body:     `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`,
			want:     model.OutcomeNoCredit,
			nextStep: "Retrying will not help",
		},
		{
			// OpenAI reports the same condition as a 429, which is the status
			// it also uses for ordinary throttling, and the two need opposite
			// advice.
			name:     "an empty balance reported as a 429",
			status:   429,
			body:     `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota"}}`,
			want:     model.OutcomeNoCredit,
			nextStep: "Retrying will not help",
		},
		{
			name:     "a model name that does not exist",
			status:   404,
			body:     `{"type":"error","error":{"type":"not_found_error","message":"model: test-model"}}`,
			want:     model.OutcomeUnknownModel,
			nextStep: "AF_MODEL",
		},
		{
			name:     "an ordinary rate limit",
			status:   429,
			body:     `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
			want:     model.OutcomeRateLimited,
			nextStep: "The key works",
		},
		{
			name:     "the provider having a bad afternoon",
			status:   503,
			body:     `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
			want:     model.OutcomeProviderDown,
			nextStep: "says nothing about the key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := probeAgainst(t, "anthropic", "sk-ant", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			require.False(t, res.OK())
			require.Equal(t, tc.want, res.Outcome)
			require.Equal(t, tc.status, res.Status)
			// The provider's own sentence survives, because it is the most
			// useful thing on the screen when this fails.
			require.NotEmpty(t, res.Detail)
			require.Contains(t, res.NextStep, tc.nextStep)
		})
	}
}

// A 200 is not evidence on its own. A reverse proxy in front of a model that is
// not running answers 200 with an error page and a misconfigured gateway
// answers 200 with an empty object; reporting either as a working key is the
// worst failure this command has, because it certifies a setup that fails on
// the first real run.
func TestProbe_A200ThatIsNotACompletionIsNotSuccess(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{}`, `{"ok":true}`, `<html>gateway</html>`, ``} {
		res := probeAgainst(t, "anthropic", "sk-ant", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		})
		require.False(t, res.OK(), "a 200 body of %q was reported as a working key", body)
		require.Equal(t, model.OutcomeUnreadable, res.Outcome)
	}
}

// A custom endpoint is a first class path, so its failures need their own
// advice. Sending a self-hoster to check their model name when their gateway
// does not serve the path is the wrong half of the message.
func TestProbe_A404OnACustomEndpointBlamesTheEndpoint(t *testing.T) {
	t.Parallel()
	res := probeAgainst(t, "anthropic", "sk-ant", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	require.Equal(t, model.OutcomeUnknownModel, res.Outcome)
	require.Contains(t, res.NextStep, "without /v1/messages on the end")
}

// A gateway that says which of the two it is gets believed. Guessing that a
// custom endpoint does not serve the path, when it has just answered with a
// message naming the model, would be a guess overriding an answer.
func TestProbe_ACustomEndpointNamingTheModelIsBelieved(t *testing.T) {
	t.Parallel()
	res := probeAgainst(t, "anthropic", "sk-ant", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w,
			`{"error":{"type":"not_found_error","message":"model: test-model"}}`)
	})
	require.Equal(t, model.OutcomeUnknownModel, res.Outcome)
	require.Contains(t, res.NextStep, "AF_MODEL")
}

// deadTransport fails every request without touching the network.
//
// Used so that the unreachable path can be exercised against the provider's own
// base URL. The alternative is a test that resolves api.anthropic.com, which
// would pass or fail depending on the machine it runs on.
type deadTransport struct{}

func (deadTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
}

func TestProbe_NothingAnsweringIsUnreachable(t *testing.T) {
	t.Parallel()
	p, _ := model.Lookup("anthropic")

	t.Run("the provider's own endpoint", func(t *testing.T) {
		t.Parallel()
		cfg := model.Config{
			Provider: p, Key: secrets.New("sk-ant"), Model: "m",
			BaseURL: p.DefaultBaseURL,
		}
		res := model.Probe(context.Background(),
			&http.Client{Transport: deadTransport{}}, cfg, time.Now)
		require.Equal(t, model.OutcomeUnreachable, res.Outcome)
		// The sentence that stops this becoming a support ticket about egress
		// policy, which is the wrong place to look and the first place
		// somebody using this product would look.
		require.Contains(t, res.NextStep, "not subject to any manifest's egress policy")
		require.Contains(t, res.NextStep, "api.anthropic.com")
	})

	t.Run("a custom endpoint", func(t *testing.T) {
		t.Parallel()
		cfg := model.Config{
			Provider: p, Key: secrets.New("sk-ant"), Model: "m",
			BaseURL: "http://127.0.0.1:1",
		}
		res := model.Probe(context.Background(),
			&http.Client{Timeout: 5 * time.Second}, cfg, time.Now)
		require.Equal(t, model.OutcomeUnreachable, res.Outcome)
		// Nothing is wrong with the provider, so the message must not send a
		// self-hoster to look at one.
		require.Contains(t, res.NextStep, "custom endpoint")
		require.NotContains(t, res.NextStep, "api.anthropic.com")
	})
}

func TestProbe_ATimeoutIsNotAnUnreachableEndpoint(t *testing.T) {
	t.Parallel()
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	}))
	// Order matters and cleanups run last registered first. Close waits for
	// every handler still running, and this handler only returns when the
	// channel is closed, so releasing it has to be registered after Close or
	// the test deadlocks in its own teardown rather than failing.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(blocked) })

	p, _ := model.Lookup("anthropic")
	cfg := model.Config{Provider: p, Key: secrets.New("sk"), Model: "m", BaseURL: srv.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res := model.Probe(ctx, srv.Client(), cfg, time.Now)

	require.Equal(t, model.OutcomeTimedOut, res.Outcome)
	require.Contains(t, res.NextStep, "--timeout")
}

// The guarantee that matters most, tested against the case that can actually
// break it. A custom endpoint is somebody else's code, gateways do echo request
// headers into error bodies, and this product tells people to point one at a
// local model. So a gateway that hands the key straight back must not be able
// to put it on a terminal, in a log, or in a support bundle.
func TestProbe_AGatewayCannotEchoTheKeyBackOut(t *testing.T) {
	t.Parallel()
	const key = "sk-ant-api03-supersecretvalue"

	res := probeAgainst(t, "anthropic", key, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// Exactly what a careless gateway does: repeats what it was given.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"message": "rejected credential " + r.Header.Get("x-api-key"),
			},
		})
	})

	require.False(t, res.OK())
	require.NotContains(t, res.Detail, key)
	require.NotContains(t, res.NextStep, key)
	// The tail is redacted too. A message quoting the last characters of a key
	// is not the leak the whole key is, and it is still more than anything
	// here has a reason to print.
	require.NotContains(t, res.Detail, key[len(key)-8:])
	require.Contains(t, res.Detail, "[redacted]")
}

// A very long body cannot push a key or anything else out onto a terminal in
// bulk, and an endpoint that is not what it claims can answer with a great deal
// of HTML.
func TestProbe_ClipsWhatItPrints(t *testing.T) {
	t.Parallel()
	res := probeAgainst(t, "anthropic", "sk-ant", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w,
			`{"error":{"message":"`+strings.Repeat("x", 5000)+`"}}`)
	})
	require.Less(t, len(res.Detail), 300)
	require.Contains(t, res.Detail, "...")
}

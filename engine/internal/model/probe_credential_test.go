package model_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// A base URL points at a gateway as often as at a provider, and a gateway's
// address can carry a user and password or a token as its user name. Detail
// and NextStep are printed by af doctor and returned to an agent.

func gatewayConfig(t *testing.T, baseURL string) model.Config {
	t.Helper()
	p, ok := model.Lookup("anthropic")
	require.True(t, ok)
	return model.Config{Provider: p, Key: secrets.New("sk-test"), Model: "test-model", BaseURL: baseURL}
}

func TestProbeNeverQuotesTheCredentialOfABaseURLThatDoesNotParse(t *testing.T) {
	cfg := gatewayConfig(t, "https://gw:Md3Pass@gateway.example.test:4x3")

	res := model.Probe(context.Background(), http.DefaultClient, cfg, time.Now)
	require.Equal(t, model.OutcomeUnreachable, res.Outcome)
	require.NotContains(t, res.Detail, "Md3Pass")
	require.NotContains(t, res.NextStep, "Md3Pass")
	require.Contains(t, res.Detail, "invalid port", "the result no longer says what is wrong")
}

func TestProbeNeverQuotesTheCredentialOfAnUnreachableGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	cfg := gatewayConfig(t, "http://gw:Md4Pass@"+host)

	res := model.Probe(context.Background(), http.DefaultClient, cfg, time.Now)
	require.Equal(t, model.OutcomeUnreachable, res.Outcome)
	require.Contains(t, res.NextStep, "custom endpoint")
	require.NotContains(t, res.NextStep, "Md4Pass")
}

func TestProbeNamesTheHostOfAGatewayThatTimesOutAndNotItsToken(t *testing.T) {
	// The handler waits on a channel the test owns as well as on the request.
	// Waiting on the request alone hung Close: the server never saw the client
	// give up, so the connection stayed active and Close waited on it for good.
	// Cleanups run last in, first out, so release is closed before Close runs.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	cfg := gatewayConfig(t, "http://Tk7Tok9@"+strings.TrimPrefix(srv.URL, "http://"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res := model.Probe(ctx, srv.Client(), cfg, time.Now)
	require.Equal(t, model.OutcomeTimedOut, res.Outcome)
	require.Contains(t, res.Detail, "127.0.0.1")
	require.NotContains(t, res.Detail, "Tk7Tok9")
}

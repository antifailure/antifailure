package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
)

// The provider client, against a server that answers the way the real one does.
//
// Two things are worth testing here and the rest is plumbing.
//
// The first is that a key goes UP and nowhere else: not into a query string,
// not into a URL that a proxy log would keep, not into an error message when
// the request fails. A transport error that quoted the request body would put a
// key in whatever captured the terminal, and that is the kind of leak nobody
// finds because it only happens when something else is already going wrong.
//
// The second is that a refusal for want of a scope is distinguishable from
// every other refusal, because the fix is a specific command and a caller told
// only "forbidden" goes looking for a permissions problem that does not exist.

// recorder captures what the client actually sent.
type recorder struct {
	method string
	path   string
	// raw is the request line as it went over the wire, before Go decoded
	// percent escapes into URL.Path. The distinction matters for the escaping
	// test below: a segment escaped correctly still shows up decoded in
	// URL.Path, so asserting on that would fail a client that is right.
	raw   string
	query string
	auth  string
	body  string
}

func serverThatRecords(t *testing.T, status int, reply any) (*auth.Client, *recorder) {
	t.Helper()
	got := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.raw = r.RequestURI
		got.auth, got.body = r.Header.Get("authorization"), string(body)
		writeJSON(w, status, reply)
	}))
	t.Cleanup(srv.Close)
	return auth.NewClient(srv.URL), got
}

// Assembled rather than written out, by the convention the TypeScript tests
// already follow: tools/scanrepo refuses a repository carrying anything its
// detector reads as a live credential, and a literal fixture is a repository
// that fails its own gate.
var testKey = strings.Join([]string{"sk", "ant", "api03"}, "-") +
	"-zzzzzzzzzzzzzzzzzzzzzzzzzzzz9999"

func TestSetProviderKey_SendsTheKeyInTheBodyAndNowhereElse(t *testing.T) {
	client, got := serverThatRecords(t, 200, map[string]any{
		"provider": "anthropic", "last4": "9999", "fingerprint": "abc123",
		"replaced": true, "sameAsBefore": false,
	})

	saved, err := client.SetProviderKey(context.Background(), "afu_token", "anthropic", testKey)
	require.NoError(t, err)
	require.Equal(t, "9999", saved.Last4)
	require.True(t, saved.Replaced)

	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/v1/providers/anthropic", got.path)
	require.Equal(t, "Bearer afu_token", got.auth)

	// The key is in the body and in nothing else. A query string is kept by
	// every proxy, load balancer and access log between here and the server.
	require.Contains(t, got.body, testKey)
	require.Empty(t, got.query, "a provider key must never reach a query string")
	require.NotContains(t, got.path, "sk-")
}

func TestSetProviderKey_TransportErrorDoesNotQuoteTheKey(t *testing.T) {
	// The leak that only happens when something is already going wrong, which
	// is why it survives review: the happy path never prints anything.
	client := auth.NewClient("http://127.0.0.1:1") // nothing listens
	_, err := client.SetProviderKey(context.Background(), "afu_token", "anthropic", testKey)
	require.Error(t, err)
	require.NotContains(t, err.Error(), testKey)
	require.NotContains(t, err.Error(), "zzzz")
}

func TestSetProviderKey_ServerRefusalDoesNotEchoTheKey(t *testing.T) {
	client, _ := serverThatRecords(t, 400, map[string]any{
		"error": "That looks like an Anthropic key. An OpenAI key starts with sk-.",
	})
	_, err := client.SetProviderKey(context.Background(), "afu_token", "openai", testKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Anthropic key")
	require.NotContains(t, err.Error(), testKey)
}

func TestProviders_MissingScopeIsItsOwnError(t *testing.T) {
	client, _ := serverThatRecords(t, 403, map[string]any{
		"error": "This token does not carry providers.write. Run: af login --scope providers.write -- and approve it in the browser.",
	})
	_, err := client.SetProviderKey(context.Background(), "afu_token", "anthropic", testKey)
	require.ErrorIs(t, err, auth.ErrScopeMissing)
	// The server's own wording survives, because it names the scope.
	require.Contains(t, err.Error(), "providers.write")
}

func TestProviders_ARoleRefusalIsNotAScopeRefusal(t *testing.T) {
	// Both are 403 and they mean opposite things. Telling a member to run
	// af login --scope would send them round a loop that cannot help them.
	client, _ := serverThatRecords(t, 403, map[string]any{
		"error": "Changing a provider key needs owner or admin. You are member.",
	})
	_, err := client.SetProviderKey(context.Background(), "afu_token", "anthropic", testKey)
	require.Error(t, err)
	require.NotErrorIs(t, err, auth.ErrScopeMissing)
	require.Contains(t, err.Error(), "owner or admin")
}

func TestProviders_AnInvalidTokenIsNotSignedIn(t *testing.T) {
	client, _ := serverThatRecords(t, 401, map[string]any{"error": "This token is not valid."})
	_, err := client.ListProviders(context.Background(), "afu_token")
	require.ErrorIs(t, err, auth.ErrNotSignedIn)
}

func TestListProviders_ReadsTheShapeTheServerSends(t *testing.T) {
	// Written from the server's actual response rather than from memory: the
	// field names here are camelCase because that is what the route emits, and
	// a struct tag that disagreed would decode to zero without an error.
	client, got := serverThatRecords(t, 200, map[string]any{
		"sealing": true,
		"keys": []map[string]any{{
			"provider": "anthropic", "last4": "9999", "fingerprint": "abc123",
			"createdAt": "2026-08-01T00:00:00Z", "rotatedAt": nil,
		}},
		"budgets": []map[string]any{{
			"provider": "anthropic", "period": "2026-08-01",
			"capUsd": 50, "spentUsd": 12.5, "remainingUsd": 37.5,
		}},
	})

	out, err := client.ListProviders(context.Background(), "afu_token")
	require.NoError(t, err)
	require.True(t, out.Sealing)
	require.Len(t, out.Keys, 1)
	require.Equal(t, "9999", out.Keys[0].Last4)
	require.Equal(t, "abc123", out.Keys[0].Fingerprint)
	require.Equal(t, "2026-08-01T00:00:00Z", out.Keys[0].CreatedAt)
	require.Len(t, out.Budgets, 1)
	require.InDelta(t, 50, out.Budgets[0].CapUSD, 0.001)
	require.InDelta(t, 12.5, out.Budgets[0].SpentUSD, 0.001)
	require.InDelta(t, 37.5, out.Budgets[0].RemainingUSD, 0.001)

	require.Equal(t, http.MethodGet, got.method)
	require.Empty(t, got.body)
}

func TestListProviders_HasNoWayToReadAKeyBack(t *testing.T) {
	// The structural claim. A server answering with a key -- because somebody
	// added such a field later -- has nowhere to put it, so the CLI cannot
	// start printing one by accident.
	var out auth.Providers
	require.NoError(t, json.Unmarshal([]byte(`{
	  "keys": [{"provider":"anthropic","last4":"9999","key":"`+testKey+`"}]
	}`), &out))
	require.Len(t, out.Keys, 1)
	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), testKey)
}

func TestRemoveProviderKey_ReportsWhetherThereWasOne(t *testing.T) {
	client, got := serverThatRecords(t, 200, map[string]any{"provider": "anthropic", "revoked": false})
	revoked, err := client.RemoveProviderKey(context.Background(), "afu_token", "anthropic")
	require.NoError(t, err)
	// Not an error. Removing a key that is not there reaches the state asked
	// for, and this is the call somebody retries during an incident.
	require.False(t, revoked)
	require.Equal(t, http.MethodDelete, got.method)
	require.Equal(t, "/v1/providers/anthropic", got.path)
}

func TestSetProviderBudget_SendsANumber(t *testing.T) {
	client, got := serverThatRecords(t, 200, map[string]any{
		"provider": "openai", "period": "2026-08-01",
		"capUsd": 0, "spentUsd": 0, "remainingUsd": 0,
	})
	budget, err := client.SetProviderBudget(context.Background(), "afu_token", "openai", 0)
	require.NoError(t, err)
	require.Equal(t, "openai", budget.Provider)
	require.InDelta(t, 0, budget.CapUSD, 0.001)

	// A number, not a string. The server refuses anything it would have to
	// coerce, precisely because every coercion lands on zero.
	require.Contains(t, got.body, `"capUsd":0`)
	require.NotContains(t, got.body, `"capUsd":"0"`)
	require.Equal(t, "/v1/providers/openai/budget", got.path)
}

func TestProviderPaths_EscapeWhatIsPutInThem(t *testing.T) {
	// The provider is checked against a closed list before it reaches here, so
	// this is a second line rather than the only one. It is worth having
	// because the closed list lives in another package and the two can drift.
	client, got := serverThatRecords(t, 200, map[string]any{"revoked": false})
	_, err := client.RemoveProviderKey(context.Background(), "afu_token", "../../v1/logout")
	require.NoError(t, err)
	// On the wire, which is what a router sees. The slashes are escaped, so the
	// name stays one segment and cannot address another endpoint.
	require.Equal(t, "/v1/providers/..%2F..%2Fv1%2Flogout", got.raw)
	require.NotContains(t, got.raw, "/v1/logout")
}

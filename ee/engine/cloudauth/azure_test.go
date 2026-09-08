// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudauth

// The Entra exchange, against a local server speaking the documented form.
//
// This proves what the code sends and what it does with each answer. It does
// not prove that Entra accepts the request, and nothing without a tenant can.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAzureAsksTheAuthorityItIsGivenForTheResourceScope(t *testing.T) {
	// Two things at once and both have burned somebody. The authority, because
	// Azure Government and the China cloud mint tokens on different hosts and a
	// compiled in host refuses exactly the customers who ask for this. And the
	// scope, because Entra's v2 endpoint wants the resource with "/.default"
	// appended, and a resource sent bare comes back as an error about the
	// scope rather than about the resource.
	var gotPath, gotScope, gotGrant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotPath, gotScope, gotGrant = r.URL.Path, r.Form.Get("scope"), r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a-token","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)

	source := &AzureTokenSource{
		TenantID: "a-tenant", ClientID: "a-client",
		ClientSecret: "assembled-at-run-time",
		Authority:    server.URL,
		Resource:     "https://ossrdbms-aad.database.windows.net",
	}
	token, err := source.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, "a-token", token)
	require.Equal(t, "/a-tenant/oauth2/v2.0/token", gotPath)
	require.Equal(t, "https://ossrdbms-aad.database.windows.net/.default", gotScope)
	require.Equal(t, "client_credentials", gotGrant)
	require.Equal(t, "the service principal a-client", source.How())
}

func TestAzureDefaultsTheResourceToKeyVault(t *testing.T) {
	// The one caller today is the Key Vault source, and a zero Resource that
	// silently signed for nothing would produce a token the vault refuses.
	var gotScope string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotScope = r.Form.Get("scope")
		_, _ = w.Write([]byte(`{"access_token":"a-token","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)

	source := &AzureTokenSource{
		TenantID: "t", ClientID: "c", ClientSecret: "assembled-at-run-time",
		Authority: server.URL,
	}
	_, err := source.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, AzureKeyVaultResource+"/.default", gotScope)
}

func TestAzureReportsARefusalAsARejectedCredential(t *testing.T) {
	// The distinction the one-refresh rule is built on. A refused client secret
	// reported as an unreachable host sends somebody to the network for a
	// credential problem, and it never gets its one renewal.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_client","message":"..."}}`))
	}))
	t.Cleanup(server.Close)

	source := &AzureTokenSource{
		TenantID: "t", ClientID: "c", ClientSecret: "assembled-at-run-time",
		Authority: server.URL,
	}
	_, err := source.Token(t.Context())
	require.ErrorIs(t, err, ErrRejected)
	require.Contains(t, err.Error(), "invalid_client")
	require.NotContains(t, err.Error(), "assembled-at-run-time",
		"a refusal must not quote the secret it was refused for")
}

func TestAzureHoldsTheTokenRatherThanMintingOnePerCall(t *testing.T) {
	// A token minted per lookup turns twenty variables into twenty exchanges,
	// which Entra rate limits, and it is the mistake the expiry field exists to
	// prevent.
	exchanges := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges++
		_, _ = w.Write([]byte(`{"access_token":"a-token","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)

	source := &AzureTokenSource{
		TenantID: "t", ClientID: "c", ClientSecret: "assembled-at-run-time",
		Authority: server.URL,
	}
	for range 3 {
		_, err := source.Token(t.Context())
		require.NoError(t, err)
	}
	require.Equal(t, 1, exchanges)

	// And Reset actually discards it, which is what the one refresh spends.
	source.Reset()
	_, err := source.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, exchanges)
}

// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"

	"github.com/stretchr/testify/require"
)

func TestAzureIdentityReachesTheDatabaseAPI(t *testing.T) {
	var exchanges atomic.Int32
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("scope") != defaultEndpoint+"/.default" || r.Form.Get("client_secret") != "AF_FAKE_SECRET" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		exchanges.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"AF_FAKE_ACCESS_TOKEN","expires_in":3600}`))
	}))
	defer authority.Close()
	values := map[string]string{"AZURE_TENANT_ID": "tenant", "AZURE_CLIENT_ID": "client", "AZURE_CLIENT_SECRET": "AF_FAKE_SECRET", "AZURE_AUTHORITY_HOST": authority.URL}
	getenv := func(name string) string { return values[name] }
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AF_FAKE_ACCESS_TOKEN" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer api.Close()
	client, err := newARMAPI(Options{Endpoint: api.URL, Getenv: getenv})
	require.NoError(t, err)
	_, err = client.listServers(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, exchanges.Load())
}

func TestRegisteredProviderUsesTheSelectedApplicationDatabase(t *testing.T) {
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"AF_FAKE_TOKEN","expires_in":3600}`))
	}))
	defer authority.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AF_FAKE_TOKEN" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/databases") {
			_, _ = w.Write([]byte(`{"value":[{"name":"orders"},{"name":"billing"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"branch","properties":{"fullyQualifiedDomainName":"branch.postgres.database.azure.com","administratorLogin":"chosen-admin"}}`))
	}))
	defer api.Close()
	values := map[string]string{SubscriptionVariable: "subscription", ResourceGroupVariable: "group", DefaultVariable: "AF_FAKE_BRANCH_KEY", DatabaseVariable: "billing", EndpointVariable: api.URL, "AZURE_TENANT_ID": "tenant", "AZURE_CLIENT_ID": "client", "AZURE_CLIENT_SECRET": "AF_FAKE_SECRET", "AZURE_AUTHORITY_HOST": authority.URL}
	registry := extension.NewRegistry()
	Register(registry)
	registered, ok := registry.DatabaseProviderNamed(Name)
	require.True(t, ok)
	opened, err := registered.Open(context.Background(), extension.DatabaseConfig{Database: schema.Database{Project: "source"}, Lookup: func(_ context.Context, name string) (secret.Value, bool, error) {
		value, ok := values[name]
		return secret.New(value), ok, nil
	}})
	require.NoError(t, err)
	defer opened.Close()
	connection, err := opened.ConnString(context.Background(), provider.Branch{ProviderRef: "branch"}, provider.ConnDirect)
	require.NoError(t, err)
	parsed, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	require.Equal(t, "/billing", parsed.Path)
	require.Equal(t, "chosen-admin", parsed.User.Username())
	require.Equal(t, "verify-full", parsed.Query().Get("sslmode"))
}
func TestAzureIdentityFailuresNeverReachTheDatabaseAPI(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty token", true: "identity error"}[broken], func(t *testing.T) {
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{"value":[]}`)) }))
			defer api.Close()
			client, err := newARMAPI(Options{Endpoint: api.URL, Token: func(context.Context) (string, error) {
				if broken {
					return "", errors.New("identity unavailable")
				}
				return "", nil
			}})
			require.NoError(t, err)
			_, err = client.listServers(context.Background())
			require.Error(t, err)
			if broken {
				require.Contains(t, err.Error(), "identity unavailable")
			}
			require.Zero(t, calls.Load())
		})
	}
}

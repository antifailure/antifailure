// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package cloudsql

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestGoogleIdentityReachesTheDatabaseAPI(t *testing.T) {
	var exchanges atomic.Int32
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("assertion") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		exchanges.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"AF_FAKE_ACCESS_TOKEN","expires_in":3600}`))
	}))
	defer authority.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]string{"type": "service_account", "client_email": "fixture@example.test", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": authority.URL})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "credentials.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	getenv := func(name string) string {
		if name == "GOOGLE_APPLICATION_CREDENTIALS" {
			return path
		}
		return ""
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AF_FAKE_ACCESS_TOKEN" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer api.Close()
	client, err := newAdminAPI(Options{Endpoint: api.URL, Getenv: getenv})
	require.NoError(t, err)
	_, err = client.listInstances(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, exchanges.Load())
}
func TestGoogleIdentityFailuresNeverReachTheDatabaseAPI(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty token", true: "identity error"}[broken], func(t *testing.T) {
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{"items":[]}`)) }))
			defer api.Close()
			client, err := newAdminAPI(Options{Endpoint: api.URL, Token: func(context.Context) (string, error) {
				if broken {
					return "", errors.New("identity unavailable")
				}
				return "", nil
			}})
			require.NoError(t, err)
			_, err = client.listInstances(context.Background())
			require.Error(t, err)
			if broken {
				require.Contains(t, err.Error(), "identity unavailable")
			}
			require.Zero(t, calls.Load())
		})
	}
}

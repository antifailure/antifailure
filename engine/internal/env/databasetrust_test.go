package env

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/envcert"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

type trustDB struct {
	*fakeDB
	bundle  string
	failure error
	mode    string
}

func (d *trustDB) TrustBundle(context.Context, provider.Branch) (string, error) {
	return d.bundle, d.failure
}
func (d *trustDB) ConnString(context.Context, provider.Branch, provider.ConnMode) (secret.Value, error) {
	mode := d.mode
	if mode == "" {
		mode = "verify-ca"
	}
	return secret.New("postgres://database/app?sslmode=" + mode + "&sslrootcert=/host-only/instance.pem"), nil
}

type trustRegistration struct{ db provider.Database }

func (r trustRegistration) Name() string { return "acmedb" }
func (r trustRegistration) Open(context.Context, extension.DatabaseConfig) (provider.Database, error) {
	return r.db, nil
}

func TestDatabaseTrustReachesTheRuntimeAndVerifiesTheServer(t *testing.T) {
	ca, err := envcert.Generate("database", time.Now())
	require.NoError(t, err)
	rootBlock, _ := pem.Decode([]byte(ca.CertPEM))
	require.NotNil(t, rootBlock)
	root, err := x509.ParseCertificate(rootBlock.Bytes)
	require.NoError(t, err)
	keyPair, err := tls.X509KeyPair([]byte(ca.CertPEM), []byte(ca.KeyPEM.Reveal()))
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, keyPair.PrivateKey)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: leafKey}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	database := &trustDB{fakeDB: newFakeDB("acmedb"), bundle: ca.CertPEM}
	runtime := &fakeRT{name: "acmert"}
	registry := extension.NewRegistry()
	registry.AddDatabaseProvider(trustRegistration{database})
	registry.AddRuntimeProvider(&fakeRTProvider{name: "acmert", rt: runtime})
	orchestrator := registeredOrchestrator(t, registry)
	_, err = orchestrator.Up(context.Background())
	require.NoError(t, err)
	require.Len(t, runtime.ups, 1)
	spec := runtime.ups[0]
	require.True(t, spec.CAKeyPEM.IsZero(), "database trust must not create an HTTP signing authority")
	require.Empty(t, spec.CACertPEM)
	for _, connection := range []secret.Value{spec.DatabaseURL, spec.MigrationDatabaseURL} {
		address, err := url.Parse(connection.Reveal())
		require.NoError(t, err)
		require.Equal(t, provider.DatabaseTrustBundlePath, address.Query().Get("sslrootcert"))
		require.Equal(t, "verify-ca", address.Query().Get("sslmode"))
	}
	// Model the same bundle file installed by the runtime, then perform a real
	// TLS exchange. The wrong or omitted database CA cannot pass this handshake.
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, []byte(spec.DatabaseCACertPEM), 0600))
	installed, err := os.ReadFile(path)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(installed))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
}

func TestDatabaseTrustFailureStopsBeforeRuntimeCreation(t *testing.T) {
	ca, err := envcert.Generate("database-failure", time.Now())
	require.NoError(t, err)
	for _, test := range []struct {
		name, bundle string
		mode         string
		failure      error
	}{
		{name: "lookup failed", failure: errors.New("trust lookup failed")},
		{name: "invalid certificate", bundle: "not a certificate"},
		{name: "private key", bundle: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-public")}))},
		{name: "verification disabled", bundle: ca.CertPEM, mode: "require"},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := &trustDB{fakeDB: newFakeDB("acmedb"), bundle: test.bundle, failure: test.failure, mode: test.mode}
			runtime := &fakeRT{name: "acmert"}
			registry := extension.NewRegistry()
			registry.AddDatabaseProvider(trustRegistration{database})
			registry.AddRuntimeProvider(&fakeRTProvider{name: "acmert", rt: runtime})
			_, err := registeredOrchestrator(t, registry).Up(context.Background())
			require.Error(t, err)
			require.Empty(t, runtime.ups)
		})
	}
}

func TestDatabaseTrustDoesNotTrustTheHTTPInspectionAuthority(t *testing.T) {
	httpCA, err := envcert.Generate("http", time.Now())
	require.NoError(t, err)
	databaseCA, err := envcert.Generate("database", time.Now())
	require.NoError(t, err)
	spec := provider.EnvSpec{CACertPEM: httpCA.CertPEM, CAKeyPEM: httpCA.KeyPEM, DatabaseURL: secret.New("postgres://database/app?sslmode=verify-full")}
	require.NoError(t, installDatabaseTrust(&spec, databaseCA.CertPEM))
	require.Equal(t, httpCA.CertPEM, spec.CACertPEM)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(spec.DatabaseCACertPEM)))
	block, _ := pem.Decode([]byte(httpCA.CertPEM))
	require.NotNil(t, block)
	certificate, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	_, err = certificate.Verify(x509.VerifyOptions{Roots: roots})
	require.Error(t, err, "the proxy's HTTP signing authority can impersonate the database")
}

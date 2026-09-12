// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package cloudsql_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/ee/engine/db/cloudsql/fakecloudsql"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

type testCA struct {
	pem, leaf   string
	pair        tls.Certificate
	key         *ecdsa.PrivateKey
	certificate *x509.Certificate
}

func makeCA(t *testing.T, dns string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AF fixture CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: dns}, DNSNames: []string{dns}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	require.NoError(t, err)
	return testCA{pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), leaf: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})), pair: tls.Certificate{Certificate: [][]byte{leafDER, der}, PrivateKey: leafKey}, key: key, certificate: root}
}

// A real PostgreSQL SSLRequest exchange, followed by TLS and the actual test
// database protocol. The CA checks therefore happen in the same pgx path used
// by masking, Health and the engine's direct consumers.
func postgresTLS(t *testing.T, certificate tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	backend, err := url.Parse(requirePostgres(t))
	require.NoError(t, err)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				var opening [8]byte
				if _, err := io.ReadFull(conn, opening[:]); err != nil {
					return
				}
				if binary.BigEndian.Uint32(opening[:4]) != 8 || binary.BigEndian.Uint32(opening[4:]) != 80877103 {
					return
				}
				if _, err := conn.Write([]byte{'S'}); err != nil {
					return
				}
				secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				if err := secure.Handshake(); err != nil {
					return
				}
				upstream, err := net.DialTimeout("tcp", backend.Host, 5*time.Second)
				if err != nil {
					return
				}
				defer func() { _ = upstream.Close() }()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(secure, upstream); _ = secure.Close(); close(done) }()
				_, _ = io.Copy(upstream, secure)
				_ = upstream.Close()
				<-done
			}()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	return net.JoinHostPort("localhost", port)
}

type trustMetadata struct {
	ca                  testCA
	mode, dns, mutation string
}

func trustOptions(t *testing.T, s *fakecloudsql.Server, address string, metadata trustMetadata) cloudsql.Options {
	t.Helper()
	opts := options(t, s)
	opts.ProxyAddress = address
	opts.TLSMode = ""
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer AF_FAKE_CLOUDSQL_TOKEN", req.Header.Get("Authorization"))
		path := req.URL.Path
		if req.Method == http.MethodGet && strings.HasSuffix(path, "/listServerCas") {
			return responseJSON(`{"certs":[],"activeVersion":"current"}`), nil
		}
		if req.Method == http.MethodGet && strings.HasSuffix(path, "/listServerCertificates") {
			body := map[string]any{"caCerts": []map[string]string{{"cert": metadata.ca.pem}}, "serverCerts": []map[string]string{{"cert": metadata.ca.leaf, "sha1Fingerprint": "active"}}, "activeVersion": "active"}
			if metadata.mutation == "missing-active" {
				body["activeVersion"] = "missing"
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			return responseJSON(string(encoded)), nil
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || req.Method != http.MethodGet || !strings.Contains(path, "/instances/") || strings.HasSuffix(path, "/users") || strings.HasSuffix(path, "/databases") {
			return resp, err
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, err
		}
		_ = resp.Body.Close()
		name := body["name"].(string)
		certificate := metadata.ca.pem
		switch metadata.mutation {
		case "missing-ca":
			certificate = ""
		case "malformed-ca":
			certificate = "not PEM"
		case "leaf-ca":
			certificate = metadata.ca.leaf
		}
		certInstance := name
		if metadata.mutation == "other-instance" {
			certInstance = "another-instance"
		}
		body["serverCaCert"] = map[string]string{"cert": certificate, "instance": certInstance}
		body["dnsName"] = metadata.dns
		body["settings"].(map[string]any)["ipConfiguration"] = map[string]string{"serverCaMode": metadata.mode}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return responseJSON(string(encoded)), nil
	})
	return opts
}

func trustBranch(t *testing.T) (*fakecloudsql.Server, provider.Branch) {
	t.Helper()
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	g, err := p.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	b, err := p.Branch(context.Background(), g.ID, "trust")
	require.NoError(t, err)
	return server, b
}

func TestInstanceCATrustReachesTheRealTLSClientAndIsCleanedUp(t *testing.T) {
	server, b := trustBranch(t)
	authority := makeCA(t, "different-dns.test")
	address := postgresTLS(t, authority.pair)
	opts := trustOptions(t, server, address, trustMetadata{ca: authority, mode: "GOOGLE_MANAGED_INTERNAL_CA"})
	p, err := newScoped(context.Background(), server, opts)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	// Existing engine readers supply only EnvID. The signed metadata supplies
	// the omitted version without relaxing explicit Branch retry checks.
	raw, err := p.ConnString(context.Background(), provider.Branch{EnvID: b.EnvID}, provider.ConnDirect)
	require.NoError(t, err)
	parsed, err := url.Parse(raw.Reveal())
	require.NoError(t, err)
	require.Equal(t, "verify-ca", parsed.Query().Get("sslmode"))
	require.NoError(t, reachable(raw.Reveal()))
	bundle, err := p.TrustBundle(context.Background(), provider.Branch{EnvID: b.EnvID})
	require.NoError(t, err)
	require.Equal(t, authority.pem, bundle)
	path := parsed.Query().Get("sslrootcert")
	require.NotEmpty(t, path)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, bundle, string(contents))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), dir.Mode().Perm())
	foreign := filepath.Join(t.TempDir(), "foreign.pem")
	require.NoError(t, os.WriteFile(foreign, []byte("untouched"), 0600))
	require.NoError(t, p.Close())
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(foreign)
	require.NoError(t, err)
	_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.Error(t, err)
}

func TestAnUntrustedInstanceCAIsRejectedByTheActualClient(t *testing.T) {
	server, b := trustBranch(t)
	actual := makeCA(t, "localhost")
	advertised := makeCA(t, "localhost")
	p, err := newScoped(context.Background(), server, trustOptions(t, server, postgresTLS(t, actual.pair), trustMetadata{ca: advertised, mode: "GOOGLE_MANAGED_INTERNAL_CA"}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	raw, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, reachable(raw.Reveal()), "a different instance's CA authenticated this TLS endpoint")
}

func TestSharedAndCustomerCAsRequireActualHostnameVerification(t *testing.T) {
	for _, mode := range []string{"GOOGLE_MANAGED_CAS_CA", "CUSTOMER_MANAGED_CAS_CA"} {
		t.Run(mode, func(t *testing.T) {
			server, b := trustBranch(t)
			authority := makeCA(t, "localhost")
			p, err := newScoped(context.Background(), server, trustOptions(t, server, postgresTLS(t, authority.pair), trustMetadata{ca: authority, mode: mode, dns: "localhost"}))
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			raw, err := p.ConnString(context.Background(), b, provider.ConnDirect)
			require.NoError(t, err)
			parsed, err := url.Parse(raw.Reveal())
			require.NoError(t, err)
			require.Equal(t, "verify-full", parsed.Query().Get("sslmode"))
			require.NoError(t, reachable(raw.Reveal()))
		})
	}
}

func TestIncompleteOrUnverifiableTrustMetadataIsRefused(t *testing.T) {
	for _, mutation := range []string{"missing-mode", "unknown-mode", "missing-ca", "malformed-ca", "leaf-ca", "other-instance", "missing-dns", "wrong-dns", "missing-active"} {
		t.Run(mutation, func(t *testing.T) {
			server, b := trustBranch(t)
			authority := makeCA(t, "localhost")
			metadata := trustMetadata{ca: authority, mode: "GOOGLE_MANAGED_INTERNAL_CA", mutation: mutation}
			switch mutation {
			case "missing-mode":
				metadata.mode = ""
			case "unknown-mode":
				metadata.mode = "new-unknown-ca"
			case "missing-dns", "wrong-dns", "missing-active":
				metadata.mode = "GOOGLE_MANAGED_CAS_CA"
				metadata.dns = "localhost"
			}
			if mutation == "missing-dns" {
				metadata.dns = ""
			}
			if mutation == "wrong-dns" {
				metadata.dns = "wrong.test"
			}
			p, err := newScoped(context.Background(), server, trustOptions(t, server, "localhost:5432", metadata))
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
			require.Error(t, err)
			_, err = p.TrustBundle(context.Background(), b)
			require.Error(t, err)
		})
	}
}

func TestGoldenMaskReceivesAUsableVerifiedConnection(t *testing.T) {
	server := newFake(t, seedSQL)
	authority := makeCA(t, "localhost")
	p, err := newScoped(context.Background(), server, trustOptions(t, server, postgresTLS(t, authority.pair), trustMetadata{ca: authority, mode: "GOOGLE_MANAGED_INTERNAL_CA"}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	spec := goldenSpec()
	mask := spec.Mask
	called := false
	spec.Mask = func(ctx context.Context, raw secret.Value) error {
		called = true
		parsed, err := url.Parse(raw.Reveal())
		require.NoError(t, err)
		require.Equal(t, "verify-ca", parsed.Query().Get("sslmode"))
		require.NotEmpty(t, parsed.Query().Get("sslrootcert"))
		return mask(ctx, raw)
	}
	_, err = p.RefreshGolden(context.Background(), spec)
	require.NoError(t, err)
	require.True(t, called)
}

func TestTLSCannotBeWeakenedForRemoteConnections(t *testing.T) {
	server := newFake(t, seedSQL)
	for _, mode := range []string{"require", "prefer", "allow", "unknown", "disable"} {
		t.Run(mode, func(t *testing.T) {
			opts := options(t, server)
			opts.TLSMode = mode
			opts.ProxyAddress = "remote.test:5432"
			_, err := newScoped(context.Background(), server, opts)
			require.Error(t, err)
		})
	}
}

func TestSharedCARejectsAnotherServerWithTheSameCA(t *testing.T) {
	server, b := trustBranch(t)
	authority := makeCA(t, "localhost")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), DNSNames: []string{"another-instance.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, authority.certificate, &key.PublicKey, authority.key)
	require.NoError(t, err)
	actual := tls.Certificate{Certificate: [][]byte{der, authority.pair.Certificate[1]}, PrivateKey: key}
	p, err := newScoped(context.Background(), server, trustOptions(t, server, postgresTLS(t, actual), trustMetadata{ca: authority, mode: "GOOGLE_MANAGED_CAS_CA", dns: "localhost"}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	raw, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, reachable(raw.Reveal()), "a shared CA authenticated a server with another instance's hostname")
}

func TestTheCertificateAPIRequiresAnAuthenticatedTransport(t *testing.T) {
	server := newFake(t, seedSQL)
	for _, endpoint := range []string{"http://remote.test", "http://localhost.attacker.test", "https://AF_FAKE_USER:AF_FAKE_PASSWORD@api.test", "https://api.test?token=AF_FAKE_TOKEN", "https://api.test#fragment", "/relative"} {
		t.Run(endpoint, func(t *testing.T) {
			opts := options(t, server)
			opts.Endpoint = endpoint
			_, err := cloudsql.New(context.Background(), opts)
			require.Error(t, err)
		})
	}
	for _, endpoint := range []string{"https://sqladmin.googleapis.com", server.URL()} {
		opts := options(t, server)
		opts.Endpoint = endpoint
		p, err := cloudsql.New(context.Background(), opts)
		require.NoError(t, err)
		require.NoError(t, p.Close())
	}
}

// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// The verified connection path, through a real PostgreSQL SSLRequest and TLS
// handshake in front of the real Postgres, with a certificate authority the
// test generates. No connection here meets a certificate RDS issued.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

type testCA struct {
	pem  string
	pair tls.Certificate
}

// makeCA returns a root and a leaf for one DNS name, signed by that root.
func makeCA(t *testing.T, dns string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	root := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AF fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: dns}, DNSNames: []string{dns},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	require.NoError(t, err)
	return testCA{
		pem:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		pair: tls.Certificate{Certificate: [][]byte{leafDER, der}, PrivateKey: leafKey},
	}
}

// postgresTLS answers the PostgreSQL SSLRequest, terminates TLS with the given
// certificate, and relays to the test Postgres, so the CA checks happen in the
// same driver path the masking step, Health and the engine use.
func postgresTLS(t *testing.T, certificate tls.Certificate) (string, int) {
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
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
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
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	return "localhost", n
}

func TestVerifiedTLSAndPrivateTrustLifecycle(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	ca := makeCA(t, "localhost")
	host, port := postgresTLS(t, ca.pair)
	s.SetEndpoint(host, port)

	opts := options(t, s)
	opts.TLSMode = "" // the default, which is what is under test
	p, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	rds.SetTrustForTest(p, ca.pem)

	// The whole refresh and branch run over verify-full, because both connect
	// to close inherited logins and the refresh also masks and verifies.
	version := refresh(t, p)
	b, err := p.Branch(context.Background(), version.ID, "tls-env")
	require.NoError(t, err)
	connection, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(connection.Reveal()))

	u, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	require.Equal(t, "verify-full", u.Query().Get("sslmode"))
	path := u.Query().Get("sslrootcert")
	require.NotEmpty(t, path)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
	bundle, err := p.TrustBundle(context.Background(), b)
	require.NoError(t, err)
	require.Equal(t, ca.pem, bundle)

	// The right root is not enough when the endpoint's name differs.
	s.SetEndpoint("127.0.0.1", port)
	bad, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, reachable(bad.Reveal()), "a certificate for another name was accepted")

	// And a different signer is refused by the real handshake.
	other := makeCA(t, "localhost")
	wrongHost, wrongPort := postgresTLS(t, other.pair)
	s.SetEndpoint(wrongHost, wrongPort)
	bad, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, reachable(bad.Reveal()), "a certificate from another authority was accepted")

	require.NoError(t, p.Close())
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err), "the trust bundle file outlived the provider")
	_, err = p.TrustBundle(context.Background(), b)
	require.Error(t, err)
}

// Anything between verify-full and disable is refused at startup: it encrypts a
// copy of production without checking who is on the other end.
func TestAWeakerTLSModeIsRefusedAtStartup(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	for _, mode := range []string{"require", "prefer", "allow", "verify-ca"} {
		opts := options(t, s)
		opts.TLSMode = mode
		_, err := scopedNew(context.Background(), opts)
		require.Error(t, err, mode)
		require.Contains(t, err.Error(), rds.TLSModeVariable, mode)
	}
}

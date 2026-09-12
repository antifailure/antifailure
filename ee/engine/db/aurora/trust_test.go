// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package aurora_test

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
	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/stretchr/testify/require"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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

func TestRDSVerifiedTLSAndPrivateTrustLifecycle(t *testing.T) {
	s := newFake(t, seedSQL, "")
	ca := makeCA(t, "localhost")
	addr := postgresTLS(t, ca.pair)
	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	s.SetEndpoint(host, n)
	opts := options(t, s)
	opts.TLSMode = ""
	p, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	aurora.SetTrustForTest(p, ca.pem)
	g, _ := spec("tls")
	v, err := p.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	b, err := p.Branch(context.Background(), v.ID, "tls-env")
	require.NoError(t, err)
	connection, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, dial(connection))
	u, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	require.Equal(t, "verify-full", u.Query().Get("sslmode"))
	path := u.Query().Get("sslrootcert")
	require.NotEmpty(t, path)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), st.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), dir.Mode().Perm())
	bundle, err := p.TrustBundle(context.Background(), b)
	require.NoError(t, err)
	require.Equal(t, ca.pem, bundle)
	// The correct root is insufficient when the endpoint name differs.
	s.SetEndpoint("127.0.0.1", n)
	bad, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, dial(bad))
	// A different signer is also refused by the real database handshake.
	other := makeCA(t, "localhost")
	wrongAddr := postgresTLS(t, other.pair)
	wrongHost, wrongPort, err := net.SplitHostPort(wrongAddr)
	require.NoError(t, err)
	wrongN, err := strconv.Atoi(wrongPort)
	require.NoError(t, err)
	s.SetEndpoint(wrongHost, wrongN)
	bad, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, dial(bad))
	require.NoError(t, p.Close())
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
	_, err = p.TrustBundle(context.Background(), b)
	require.Error(t, err)
}

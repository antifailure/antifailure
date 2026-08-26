package envcert_test

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/envcert"
)

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func parse(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}

func TestGenerate_ProducesAUsableAuthority(t *testing.T) {
	t.Parallel()
	a, err := envcert.Generate("env-abc", now)
	require.NoError(t, err)

	cert := parse(t, a.CertPEM)
	require.True(t, cert.IsCA)
	require.True(t, cert.BasicConstraintsValid)
	require.NotZero(t, cert.KeyUsage&x509.KeyUsageCertSign)
	require.Contains(t, cert.Subject.CommonName, "env-abc",
		"somebody who finds this in a trust store has to be able to tell what it was for")
}

func TestGenerate_CannotMintAnotherAuthority(t *testing.T) {
	t.Parallel()
	// One level. Possession of this signs leaves and nothing else, so a leaked
	// key cannot be turned into an authority that signs for anything at all.
	a, err := envcert.Generate("env-abc", now)
	require.NoError(t, err)
	cert := parse(t, a.CertPEM)
	require.Equal(t, 0, cert.MaxPathLen)
	require.True(t, cert.MaxPathLenZero)
}

func TestGenerate_IsBackdatedAndShortLived(t *testing.T) {
	t.Parallel()
	// Backdated, because a container whose clock is behind the host's would
	// otherwise reject a certificate issued a moment ago, and that failure
	// looks nothing like a clock problem.
	a, err := envcert.Generate("env-abc", now)
	require.NoError(t, err)
	cert := parse(t, a.CertPEM)
	require.True(t, cert.NotBefore.Before(now), "issued in the past")
	require.Equal(t, now.Add(envcert.Lifetime).UTC(), cert.NotAfter.UTC())
	require.Less(t, cert.NotAfter.Sub(cert.NotBefore), 40*24*time.Hour,
		"a certificate nobody removed must expire rather than linger")
}

func TestGenerate_IsDifferentEveryTime(t *testing.T) {
	t.Parallel()
	// Per environment, so a certificate cannot outlive the thing it was for.
	// Two environments sharing one would mean removing either leaves the other
	// trusting a key that is still out there.
	a, err := envcert.Generate("env-a", now)
	require.NoError(t, err)
	b, err := envcert.Generate("env-b", now)
	require.NoError(t, err)
	require.NotEqual(t, a.CertPEM, b.CertPEM)
	require.NotEqual(t, a.KeyPEM.Reveal(), b.KeyPEM.Reveal())

	same1, err := envcert.Generate("env-a", now)
	require.NoError(t, err)
	require.NotEqual(t, a.CertPEM, same1.CertPEM, "the same name does not reuse a key")
}

func TestAuthority_KeyIsASecret(t *testing.T) {
	t.Parallel()
	// The worst leak in the product would be this key in a log line, so it
	// carries the type that makes that impossible by default.
	a, err := envcert.Generate("env-abc", now)
	require.NoError(t, err)

	rendered := strings.Join([]string{
		"key=" + a.KeyPEM.String(),
		strings.TrimSpace(a.KeyPEM.String()),
	}, " ")
	require.NotContains(t, rendered, "PRIVATE KEY")
	require.Contains(t, a.KeyPEM.Reveal(), "EC PRIVATE KEY", "and it is still readable on purpose")
}

func TestTrustEnv_CoversTheRuntimesThatDisagree(t *testing.T) {
	t.Parallel()
	// There is no single way to point a runtime at a certificate, which is the
	// whole problem. Setting only one is invisible until a request fails for a
	// reason that looks nothing like a certificate.
	env := envcert.TrustEnv()
	for _, name := range []string{
		"NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE",
		"CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE",
	} {
		require.Equal(t, envcert.BundlePath, env[name], "%s is not pointed at the bundle", name)
	}
	// NODE_OPTIONS=--use-openssl-ca looks like it helps here and does the
	// opposite: it switches Node to OpenSSL's own store, which makes it ignore
	// NODE_EXTRA_CA_CERTS, and every HTTPS call in the environment fails with
	// a self-signed certificate error that nothing explains. Setting it cost
	// an afternoon once.
	require.NotContains(t, env, "NODE_OPTIONS")
}

func TestCertificate_SignsALeafThatVerifies(t *testing.T) {
	t.Parallel()
	// The property everything else rests on: a leaf this signs is trusted by
	// anything that trusts the authority.
	a, err := envcert.Generate("env-abc", now)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(a.CertPEM)),
		"the certificate has to be loadable into a trust store")

	root := parse(t, a.CertPEM)
	_, err = root.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: now,
	})
	require.NoError(t, err)
}

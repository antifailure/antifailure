// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/url"
	"os"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

func trustOptions() Options {
	return Options{Subscription: "test", ResourceGroup: "test", SourceServer: "source", BranchKey: secret.New("AF_FAKE_BRANCH_KEY")}
}

func TestAzureTrustIsPublicAvailableToClientsAndCleanedUp(t *testing.T) {
	p, err := New(trustOptions())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	t.Cleanup(func() { _ = os.RemoveAll(p.trustDir) })
	bundle, err := p.TrustBundle(context.Background(), provider.Branch{})
	require.NoError(t, err)
	want := map[string]bool{
		"c741f70f4b2a8d88bf2e71c14122ef53ef10eba0cfa5e64cfa20f418853073e0": true,
		"358df39d764af9e1b766e9c972df352ee15cfac227af6ad1d70e8e4a6edcba02": true,
		"cb3ccbb76031e5e0138f8dd39a23f9de47ffc35e43c1144cea27d46a5ab1cb5f": true,
		"4348a0e9444c78cb265e058d5e8944b4d84f9662bd26db257f8934a443c70161": true,
	}
	for rest := []byte(bundle); len(rest) > 0; {
		block, remaining := pem.Decode(rest)
		require.NotNil(t, block)
		require.Equal(t, "CERTIFICATE", block.Type)
		certificate, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		require.True(t, certificate.IsCA)
		// A trust anchor is authenticated by its published fingerprint, not
		// its self-signature. The legacy DigiCert root self-signs with SHA1;
		// Go correctly rejects SHA1 for chain links but does not check a
		// trusted root's self-signature during certificate verification.
		require.Equal(t, certificate.RawSubject, certificate.RawIssuer)
		digest := sha256.Sum256(certificate.Raw)
		fingerprint := hex.EncodeToString(digest[:])
		require.True(t, want[fingerprint], "unexpected or repeated trust anchor")
		delete(want, fingerprint)
		rest = remaining
	}
	require.Empty(t, want)
	connection := p.connString("source.postgres.database.azure.com", 5432, "admin", "AF_FAKE_PASSWORD", "app")
	parsed, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	require.Equal(t, "verify-full", parsed.Query().Get("sslmode"))
	path := parsed.Query().Get("sslrootcert")
	require.NotEmpty(t, path)
	file, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, bundle, string(file))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, p.Close())
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRemoteAzureCannotDisableServerIdentityVerification(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca"} {
		t.Run(mode, func(t *testing.T) {
			opts := trustOptions()
			opts.TLSMode = mode
			p, err := New(opts)
			if p != nil {
				_ = p.Close()
			}
			require.ErrorContains(t, err, "require verify-full")
		})
	}
}

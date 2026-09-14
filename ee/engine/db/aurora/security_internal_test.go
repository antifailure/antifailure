// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package aurora

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceIdentityRejectsWrongRegionAccountPartitionAndMissingNetwork(t *testing.T) {
	for _, arn := range []string{"", "arn:aws:rds:us-east-1:123456789012:cluster:source", "arn:aws:rds:eu-west-1:12345678901x:cluster:source", "arn:aws-cn:rds:eu-west-1:123456789012:cluster:source", "arn:aws:rds:eu-west-1:123456789012:cluster:other"} {
		p := &Provider{api: &client{region: "eu-west-1"}}
		c := dbCluster{ARN: arn, Identifier: "source", Subnet: "subnet", SecurityGroups: []struct {
			ID string `xml:"VpcSecurityGroupId"`
		}{{ID: "sg-one"}}}
		require.Error(t, p.bindSource(c))
	}
	p := &Provider{api: &client{region: "eu-west-1"}}
	c := dbCluster{ARN: "arn:aws:rds:eu-west-1:123456789012:cluster:source", Identifier: "source"}
	require.Error(t, p.bindSource(c))
	c.Subnet = "subnet"
	c.SecurityGroups = []struct {
		ID string `xml:"VpcSecurityGroupId"`
	}{{ID: "sg-one"}}
	require.NoError(t, p.bindSource(c))
	require.NotEmpty(t, p.scope)
}

func TestOfficialTrustBundlesContainOnlyPinnedSelfSignedRoots(t *testing.T) {
	for _, tc := range []struct {
		bundle, digest string
		count          int
	}{
		{commercialRoots, "e5bb2084ccf45087bda1c9bffdea0eb15ee67f0b91646106e466714f9de3c7e3", 108},
		{governmentRoots, "694a8e0f4376f3133dbd76732b7644264c8a8f4c17b66d306cbec18aae58e46a", 6},
	} {
		sum := sha256.Sum256([]byte(tc.bundle))
		require.Equal(t, tc.digest, hex.EncodeToString(sum[:]))
		rest := []byte(tc.bundle)
		count := 0
		for len(rest) > 0 {
			block, next := pem.Decode(rest)
			if block == nil {
				break
			}
			rest = next
			cert, err := x509.ParseCertificate(block.Bytes)
			require.NoError(t, err)
			require.True(t, cert.IsCA)
			require.NoError(t, cert.CheckSignatureFrom(cert))
			count++
		}
		require.Equal(t, tc.count, count)
	}
}

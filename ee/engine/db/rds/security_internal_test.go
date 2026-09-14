// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// source builds an instance description in the shape AWS's model gives it.
func source(arn, subnetGroup string, groups ...string) dbInstance {
	in := dbInstance{ARN: arn, Identifier: "source"}
	in.SubnetGroup.Name = subnetGroup
	for _, id := range groups {
		in.SecurityGroups = append(in.SecurityGroups, struct {
			ID string `xml:"VpcSecurityGroupId"`
		}{ID: id})
	}
	return in
}

// Every part of the ARN that decides which resources are this provider's is
// checked, and an instance whose networking cannot be read is refused rather
// than restored into the account's defaults.
func TestSourceIdentityRejectsWrongRegionAccountPartitionKindAndMissingNetwork(t *testing.T) {
	for _, arn := range []string{
		"",
		"arn:aws:rds:us-east-1:123456789012:db:source",
		"arn:aws:rds:eu-west-1:12345678901x:db:source",
		"arn:aws:rds:eu-west-1:1234567890:db:source",
		"arn:aws-cn:rds:eu-west-1:123456789012:db:source",
		"arn:aws:rds:eu-west-1:123456789012:cluster:source",
		"arn:aws:rds:eu-west-1:123456789012:db:other",
		"arn:aws:ec2:eu-west-1:123456789012:db:source",
	} {
		p := &Provider{api: &client{region: "eu-west-1"}}
		require.Error(t, p.bindSource(source(arn, "subnets", "sg-one")), arn)
		require.Empty(t, p.scope, arn)
	}

	p := &Provider{api: &client{region: "eu-west-1"}}
	require.Error(t, p.bindSource(source("arn:aws:rds:eu-west-1:123456789012:db:source", "")),
		"an instance with no subnet group and no security group was accepted")
	require.Error(t, p.bindSource(source("arn:aws:rds:eu-west-1:123456789012:db:source", "subnets")),
		"an instance with no security group was accepted")
	require.Error(t, p.bindSource(source("arn:aws:rds:eu-west-1:123456789012:db:source", "subnets", "")),
		"an empty security group was accepted")

	good := source("arn:aws-us-gov:rds:eu-west-1:123456789012:db:source", "subnets", "sg-one", "sg-two")
	require.NoError(t, p.bindSource(good))
	require.NotEmpty(t, p.scope)
	require.Equal(t, "aws-us-gov", p.partition)
	require.Equal(t, "subnets", p.api.subnetGroup)
	require.Equal(t, []string{"sg-one", "sg-two"}, p.api.securityGroups)
}

// Ownership needs the ARN, the marker and the scope together. A resource with
// the right tags under another account's ARN, or with the right ARN and
// another source's scope, is not this provider's.
func TestOwnershipNeedsTheArnTheMarkerAndTheScope(t *testing.T) {
	p := &Provider{api: &client{region: "eu-west-1"}}
	require.NoError(t, p.bindSource(source("arn:aws:rds:eu-west-1:123456789012:db:source", "subnets", "sg-one")))

	tags := []tagXML{{Key: tagMarker, Value: Name}, {Key: tagScope, Value: p.scope}}
	in := dbInstance{ARN: "arn:aws:rds:eu-west-1:123456789012:db:af-b-x", Identifier: "af-b-x", Tags: tags}
	require.True(t, p.ownsInstance(in))

	other := in
	other.ARN = "arn:aws:rds:eu-west-1:999999999999:db:af-b-x"
	require.False(t, p.ownsInstance(other), "another account's instance was owned")

	unscoped := in
	unscoped.Tags = []tagXML{{Key: tagMarker, Value: Name}, {Key: tagScope, Value: "another"}}
	require.False(t, p.ownsInstance(unscoped), "another source's instance was owned")

	itself := in
	itself.Identifier, itself.ARN = "source", "arn:aws:rds:eu-west-1:123456789012:db:source"
	require.False(t, p.ownsInstance(itself), "the source instance itself was owned")

	snap := dbSnapshot{ARN: "arn:aws:rds:eu-west-1:123456789012:snapshot:af-g-x", Identifier: "af-g-x", Tags: tags}
	require.True(t, p.ownsSnapshot(snap))
	snap.ARN = "arn:aws:rds:eu-west-1:123456789012:db:af-g-x"
	require.False(t, p.ownsSnapshot(snap), "an instance ARN was accepted for a snapshot")
}

// The receipt covers every field that decides what a resource is, so changing
// any one of them makes a different receipt.
func TestReceiptChangesWithEveryFieldItBinds(t *testing.T) {
	p := &Provider{scope: "scope"}
	p.branchKey = secret.New("key")
	base := p.receipt(kindBranch, "af-b-x", map[string]string{tagVersion: "v1", tagEnv: "env"})
	for name, other := range map[string]string{
		"kind":       p.receipt(kindGolden, "af-b-x", map[string]string{tagVersion: "v1", tagEnv: "env"}),
		"identifier": p.receipt(kindBranch, "af-b-y", map[string]string{tagVersion: "v1", tagEnv: "env"}),
		"version":    p.receipt(kindBranch, "af-b-x", map[string]string{tagVersion: "v2", tagEnv: "env"}),
		"env":        p.receipt(kindBranch, "af-b-x", map[string]string{tagVersion: "v1", tagEnv: "env2"}),
	} {
		require.NotEqual(t, base, other, "the receipt does not bind the %s", name)
	}
	q := &Provider{scope: "another-scope", branchKey: secret.New("key")}
	require.NotEqual(t, base, q.receipt(kindBranch, "af-b-x", map[string]string{tagVersion: "v1", tagEnv: "env"}),
		"the receipt does not bind the scope")
	k := &Provider{scope: "scope", branchKey: secret.New("another-key")}
	require.NotEqual(t, base, k.receipt(kindBranch, "af-b-x", map[string]string{tagVersion: "v1", tagEnv: "env"}),
		"the receipt does not depend on the branch key")
}

// The bundles are AWS's, identical to the aurora provider's, and hold only
// self signed certificate authorities.
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

// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

// What this adapter does without an AWS account.
//
// The signing lives in ee/engine/cloudauth now, and so does the proof of it:
// the canonical example AWS publishes is checked there, against the service's
// own published answer rather than against our own idea of the algorithm. What
// is left here is the part that belongs to the store, which is what it says
// when it cannot be built and what it says when no credentials answered.
//
// Everything else in this adapter needs a real account and is marked `written`
// rather than `proven` in STATUS.md until it has one.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The key id below is AWS's own published example value. It is documented as an
// example, it authenticates nothing, and a message that quoted a real one would
// be the failure the assertion beneath it exists to catch.
const exampleKeyID = "AKIA" + "IOSFODNN7EXAMPLE"

func TestAWSRefusesToBeBuiltWithoutARegion(t *testing.T) {
	_, err := NewAWSSecretsManager(AWSConfig{Getenv: func(string) string { return "" }})
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "region")
	require.Contains(t, err.Error(), "eu-west-1",
		"the message should say why a region is not optional")
}

func TestAWSSaysWhichPlacesItLookedForCredentials(t *testing.T) {
	// The message somebody reads when nothing answered. "No credentials" on its
	// own leaves them guessing which of four mechanisms was supposed to supply
	// them, and the answer is usually that the one they configured, a profile
	// or a web identity token file, is not one this source reads.
	source, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1",
		// An environment with nothing in it, and no metadata service to reach,
		// which is what a laptop looks like.
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)

	ok, why := source.Available(withFeatures(t.Context(), "enterprise_secrets"))
	require.False(t, ok)
	require.Contains(t, why, "AWS_ACCESS_KEY_ID")
	require.Contains(t, why, "~/.aws/credentials")
	t.Logf("reports: %s (%s)", source.Name(), why)
}

func TestAWSHalfSuppliedCredentialsAreNamedRatherThanIgnored(t *testing.T) {
	// A key id with no secret is a mistake somebody made, not an absence. Left
	// to fall through it would look identical to having configured nothing.
	source, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1",
		Getenv: func(name string) string {
			if name == "AWS_ACCESS_KEY_ID" {
				return exampleKeyID
			}
			return ""
		},
	})
	require.NoError(t, err)
	ok, why := source.Available(withFeatures(t.Context(), "enterprise_secrets"))
	require.False(t, ok)
	require.Contains(t, why, "AWS_SECRET_ACCESS_KEY")
	require.NotContains(t, why, exampleKeyID, "a message must not quote a key id")
}

func TestAWSNamesTheSecretOrThePrefixSoTwoSourcesAreTellableApart(t *testing.T) {
	// The name appears in AF-SEC-001 next to every other source, and two AWS
	// sources for two accounts have to be distinguishable in that list.
	one, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", SecretID: "antifailure/production",
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	two, err := NewAWSSecretsManager(AWSConfig{
		Region: "us-east-1", Prefix: "antifailure/staging/",
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.NotEqual(t, one.Name(), two.Name())
	require.Contains(t, one.Name(), "antifailure/production")
	require.Contains(t, two.Name(), "us-east-1")
}

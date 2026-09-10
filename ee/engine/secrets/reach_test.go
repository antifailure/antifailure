// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

// Reach has to reach the STORE, not only the place the credential comes from.
//
// This file exists because the same defect was written three times, found once,
// and left in place twice. The first live Key Vault run found that the Azure
// adapter's Reach acquired a Microsoft Entra token and never touched the vault,
// so a vault behind a firewall, a private endpoint, or a typo handed back a
// good token and the source reported itself perfectly usable. Azure was fixed.
// Google and AWS had the same fault and kept it, because the thing that found
// it was a live run and neither of those has ever had one.
//
// THE REASON THE OFFLINE SUITES COULD NOT SEE IT is worth more than the fault.
// Each fake is ONE process serving both the credential endpoint and the store,
// and each conformance harness built its unreachable source by pointing the
// whole fake at a dead address. That breaks both halves together, so the
// credential failure arrives first and hides the fact that nothing ever asked
// the store anything. A test can only see this defect if the two hosts are
// SPLIT: a credential endpoint that answers, and a store address that does not.
// That is the single arrangement every test in this file makes, and it is why
// these are not folded into the conformance runs beside them.
//
// Each test carries its own positive control, because "Reach failed" is the
// expected result here and a Reach that failed for an unrelated reason, a
// broken fake or a credential that never worked, would look identical.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGCPReachIsNotSatisfiedByATokenAlone(t *testing.T) {
	licensed := withFeatures(t.Context(), "enterprise_secrets")
	server := fakeSecretManager(t)

	// The control. Both hosts are the fake, which is the arrangement the
	// conformance run uses, and it passes either side of the fix. If this ever
	// fails, the assertion below is measuring a broken fixture rather than the
	// adapter.
	reachable := New(newGCPBackend(
		GCPConfig{Project: "ok", Version: "latest", Endpoint: server.URL},
		tokenEndpoint(t, server.URL)))
	ok, why := reachable.Available(licensed)
	require.True(t, ok, "the control must be usable, or the split case proves nothing: %s", why)

	// The defect. Google's token endpoint answers and Secret Manager does not,
	// which is what a project with the API not enabled, a VPC Service Controls
	// perimeter, or a typo in the project id all look like from here.
	split := New(newGCPBackend(
		GCPConfig{Project: "ok", Version: "latest", Endpoint: "http://127.0.0.1:1"},
		tokenEndpoint(t, server.URL)))

	// Asserted first at the backend, so the failure names Reach rather than
	// anything Available wraps around it.
	require.Error(t, split.backend.Reach(t.Context()),
		"Reach acquired a token and never asked Secret Manager anything, so a project "+
			"whose API is not enabled reports itself usable")

	ok, why = split.Available(licensed)
	require.False(t, ok,
		"a source that cannot reach Secret Manager reported itself usable, so AF-SEC-001 "+
			"would list it as a place the value could have come from")
	require.NotEmpty(t, why, "an unreachable store must say why, or AF-SEC-001 prints \"not present\"")
	t.Logf("reports: %s (%s)", split.Name(), why)
}

func TestAWSReachIsNotSatisfiedByCredentialsAlone(t *testing.T) {
	licensed := withFeatures(t.Context(), "enterprise_secrets")
	server := fakeSecretsManager(t)

	// Credentials that resolve out of the environment, which is the path that
	// makes NO network call at all. That is why this store had the fault worse
	// than Azure did: Azure at least proved that Entra answered.
	env := func(name string) string {
		switch name {
		case "AWS_ACCESS_KEY_ID":
			return exampleKeyID
		case "AWS_SECRET_ACCESS_KEY":
			return exampleSecretKey
		}
		return ""
	}

	// The control.
	reachable, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", Endpoint: server.URL + "/ok/", Getenv: env})
	require.NoError(t, err)
	ok, why := reachable.Available(licensed)
	require.True(t, ok, "the control must be usable, or the case below proves nothing: %s", why)

	// The defect. The keys are present and valid-looking and Secrets Manager is
	// not there, which is what a VPC endpoint pointed at the wrong place, a
	// region that is not enabled on the account, or a typo in Endpoint look
	// like from here.
	split, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", Endpoint: "http://127.0.0.1:1/", Getenv: env})
	require.NoError(t, err)

	require.Error(t, split.backend.Reach(t.Context()),
		"Reach found credentials and never asked Secrets Manager anything, so a store "+
			"that is not there at all reports itself usable")

	ok, why = split.Available(licensed)
	require.False(t, ok,
		"a source that cannot reach Secrets Manager reported itself usable, so AF-SEC-001 "+
			"would list it as a place the value could have come from")
	require.NotEmpty(t, why, "an unreachable store must say why")
	t.Logf("reports: %s (%s)", split.Name(), why)
}

// The probe must not spend the credential it is checking for.
//
// A reachability check that signs its request would send a live credential to
// whatever answers at the configured address, and the address being wrong is
// precisely the case the check exists to detect. A typo in a hostname that
// somebody else has registered would then be handed a working signature.
//
// Asserted on the request the fake actually received rather than by reading the
// adapter, because this is a property of what goes on the wire.
func TestAReachProbeCarriesNoCredential(t *testing.T) {
	licensed := withFeatures(t.Context(), "enterprise_secrets")

	t.Run("aws", func(t *testing.T) {
		server := fakeSecretsManager(t)
		source, err := NewAWSSecretsManager(AWSConfig{
			Region: "eu-west-1", Endpoint: server.URL + "/ok/",
			Getenv: func(name string) string {
				switch name {
				case "AWS_ACCESS_KEY_ID":
					return exampleKeyID
				case "AWS_SECRET_ACCESS_KEY":
					return exampleSecretKey
				}
				return ""
			},
		})
		require.NoError(t, err)
		ok, why := source.Available(licensed)
		require.True(t, ok, why)

		seen := probeHeaders(t, server, awsListProbe)
		require.Empty(t, seen.Get("Authorization"),
			"the probe signed itself, so a mistyped endpoint receives a usable signature")
	})

	t.Run("gcp", func(t *testing.T) {
		server := fakeSecretManager(t)
		source := New(newGCPBackend(
			GCPConfig{Project: "ok", Version: "latest", Endpoint: server.URL},
			tokenEndpoint(t, server.URL)))
		ok, why := source.Available(licensed)
		require.True(t, ok, why)

		seen := probeHeaders(t, server, gcpListProbe)
		require.Empty(t, seen.Get("Authorization"),
			"the probe sent the bearer token, so a mistyped endpoint receives a live token")
	})
}

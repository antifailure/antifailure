package policyenforce_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/policyenforce"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// write puts a policy document on disk and returns a getenv naming it.
func write(t *testing.T, body string) func(string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return func(name string) string {
		if name == policyenforce.PolicyEnv {
			return path
		}
		return ""
	}
}

func unset(string) string { return "" }

// ---------------------------------------------------------------------------
// The behaviour, not the presence of the function
// ---------------------------------------------------------------------------

// The test this package did not have. Every other test here constructs a hook
// by hand and asks it a question, which proves the rules and proves nothing
// about whether any binary ever builds one. This one goes through the same call
// the enterprise binary makes and asks the registry, which is what the engine
// actually consults.
func TestARegisteredPolicyRefusesThroughTheRegistry(t *testing.T) {
	t.Parallel()
	getenv := write(t, "denied_hosts:\n  - api.stripe.com\n")

	registry := extension.NewRegistry()
	hook, err := policyenforce.RegisterFromEnvironment(registry, getenv)
	require.NoError(t, err)
	require.NotNil(t, hook)

	err = registry.CheckPolicy(licensed(t), request())
	require.Error(t, err, "the registry permitted an environment the policy denies")
	require.ErrorContains(t, err, "AF-EE-010")
	require.ErrorContains(t, err, "api.stripe.com")
}

// The negative control for the test above. Removing the AddPolicy call in
// RegisterFromEnvironment leaves the test above passing only if the registry
// were consulted some other way, so this proves the registry is empty without
// the registration rather than refusing by default.
func TestAnUnregisteredPolicyRefusesNothing(t *testing.T) {
	t.Parallel()
	registry := extension.NewRegistry()
	require.NoError(t, registry.CheckPolicy(licensed(t), request()))
	require.True(t, registry.Empty())
}

func TestWithNoVariableNothingIsRegistered(t *testing.T) {
	t.Parallel()
	registry := extension.NewRegistry()

	hook, err := policyenforce.RegisterFromEnvironment(registry, unset)
	require.NoError(t, err, "an installation with no organization policy is not an error")
	require.Nil(t, hook)
	require.True(t, registry.Empty(), "something was registered for an unset variable")
}

func TestTheRegisteredHookIsNamedInTheRegistry(t *testing.T) {
	t.Parallel()
	registry := extension.NewRegistry()
	_, err := policyenforce.RegisterFromEnvironment(registry, write(t, "denied_hosts: [evil.example]\n"))
	require.NoError(t, err)

	require.Equal(t, []string{"policy:organization-policy"}, registry.Registered())
}

// ---------------------------------------------------------------------------
// Failing loudly rather than degrading to no policy
// ---------------------------------------------------------------------------

func TestAPolicyFileThatIsNotThereStopsTheProcess(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	getenv := func(name string) string {
		if name == policyenforce.PolicyEnv {
			return missing
		}
		return ""
	}

	registry := extension.NewRegistry()
	_, err := policyenforce.RegisterFromEnvironment(registry, getenv)
	require.Error(t, err, "a named policy file that cannot be read must not degrade to no policy")
	require.ErrorContains(t, err, policyenforce.PolicyEnv)
	require.True(t, registry.Empty())
}

func TestAMisspelledKeyIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()
	// denied_host, singular. Ignored, this parses into an empty policy that
	// refuses nothing, and the administrator who wrote it cannot tell that from
	// a policy nobody violated.
	_, err := policyenforce.Parse([]byte("denied_host:\n  - api.stripe.com\n"))
	require.Error(t, err)
	require.ErrorContains(t, err, "denied_host")
}

func TestABoundNothingEnforcesIsRefused(t *testing.T) {
	t.Parallel()
	_, err := policyenforce.Parse([]byte("max_lifetime_hours: 4\n"))
	require.Error(t, err, "a bound with no reaper behind it was accepted")
	require.ErrorContains(t, err, "no reaper")
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

func TestEveryRuleSurvivesTheDocument(t *testing.T) {
	t.Parallel()
	policy, err := policyenforce.Parse([]byte(`
required_masked_columns:
  - "*.email"
denied_hosts:
  - api.stripe.com
allowed_modes:
  - block
  - capture
synth_requires_approval: true
allowed_providers:
  - docker
allowed_regions:
  - westeurope
`))
	require.NoError(t, err)
	require.Equal(t, policyenforce.Policy{
		RequiredMaskedColumns: []string{"*.email"},
		DeniedHosts:           []string{"api.stripe.com"},
		AllowedModes:          []string{"block", "capture"},
		SynthRequiresApproval: true,
		AllowedProviders:      []string{"docker"},
		AllowedRegions:        []string{"westeurope"},
	}, policy)
}

// Every rule in a document has to reach the hook, not only survive the decode.
// A field parsed into a struct nothing reads is the same defect one layer down.
func TestEveryParsedRuleIsAskedOfAnEnvironment(t *testing.T) {
	t.Parallel()
	ctx := licensed(t)

	for _, tc := range []struct {
		name     string
		document string
		expect   string
	}{
		{"required masking", "required_masked_columns: [\"*.card_number\"]", "required masking"},
		{"egress deny list", "denied_hosts: [api.stripe.com]", "egress deny list"},
		{"allowed egress modes", "allowed_modes: [block]", "allowed egress modes"},
		{"allowed providers", "allowed_providers: [neon]", "allowed database providers"},
		{"data residency", "allowed_regions: [westeurope]", "data residency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			registry := extension.NewRegistry()
			_, err := policyenforce.RegisterFromEnvironment(registry, write(t, tc.document+"\n"))
			require.NoError(t, err)

			req := request()
			req.Region = "eastus"
			err = registry.CheckPolicy(ctx, req)
			require.Error(t, err, "%s parsed and refused nothing", tc.name)
			require.ErrorContains(t, err, tc.expect)
		})
	}
}

func TestSynthRequiresApprovalReachesTheHook(t *testing.T) {
	t.Parallel()
	registry := extension.NewRegistry()
	_, err := policyenforce.RegisterFromEnvironment(registry,
		write(t, "synth_requires_approval: true\n"))
	require.NoError(t, err)

	req := request()
	req.EgressModes["api.openai.com"] = "synth"
	req.EgressHosts = append(req.EgressHosts, "api.openai.com")

	err = registry.CheckPolicy(licensed(t), req)
	require.ErrorContains(t, err, "synth requires approval")
}

// A policy read from a file must still stop enforcing when the licence lapses.
// The gate lives in Check rather than in registration, and reading the policy
// from somewhere new is exactly the change that would move it by accident.
func TestAFileReadPolicyStillObeysTheLicence(t *testing.T) {
	t.Parallel()
	registry := extension.NewRegistry()
	_, err := policyenforce.RegisterFromEnvironment(registry,
		write(t, "denied_hosts: [api.stripe.com]\n"))
	require.NoError(t, err)

	require.NoError(t, registry.CheckPolicy(context.Background(), request()),
		"a policy from a file was enforced with no licence in the context")
}

func TestRulesNamesWhatIsInForce(t *testing.T) {
	t.Parallel()
	hook, err := policyenforce.FromEnvironment(write(t,
		"denied_hosts: [a.example, b.example]\nsynth_requires_approval: true\n"))
	require.NoError(t, err)

	require.Equal(t, []string{
		"egress deny list (2 hosts)",
		"synth requires approval",
	}, policyenforce.Rules(hook))
	require.Nil(t, policyenforce.Rules(nil))
}

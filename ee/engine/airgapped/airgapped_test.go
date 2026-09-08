// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package airgapped_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/airgapped"
	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

func licensed(features ...license.Feature) context.Context {
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: features,
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func clean(t *testing.T) {
	t.Helper()
	airgap.Reset()
	t.Cleanup(airgap.Reset)
}

func TestWithoutTheVariableNothingIsSealedAndNothingIsSaid(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()

	on, notes, err := airgapped.RegisterFromEnvironment(licensed(license.FeatureAirGapped), reg, env(nil))
	require.NoError(t, err)
	require.False(t, on)
	require.Empty(t, notes)
	require.False(t, airgap.Sealed())
}

func TestTheVariableWithoutTheLicenceRefusesToStart(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()

	_, _, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureSSO), reg, env(map[string]string{airgapped.ModeEnv: "1"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "air_gapped")
	require.Contains(t, err.Error(), "believing it was sealed")
	require.False(t, airgap.Sealed(),
		"the one thing that must not happen is starting with the network open "+
			"while the configuration says it is closed")
}

func TestTheVariableWithTheLicenceSealsTheProcess(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()

	on, notes, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), reg,
		env(map[string]string{
			airgapped.ModeEnv:  "1",
			airgapped.AllowEnv: "registry.internal:5000, 10.4.0.0/16",
		}))
	require.NoError(t, err)
	require.True(t, on)
	require.True(t, airgap.Sealed())
	require.Equal(t, []string{"10.4.0.0/16", "registry.internal:5000"}, airgap.Allowed())
	require.Contains(t, strings.Join(notes, "\n"), "registry.internal:5000")

	require.ErrorIs(t, airgap.Check(airgap.SiteReleaseCheck, "tcp", "api.github.com:443"),
		airgap.ErrSealed)
	require.NoError(t, airgap.Check(airgap.SiteImagePull, "tcp", "registry.internal:5000"))
}

func TestAnEmptyAllowListSaysSoRatherThanLookingConfigured(t *testing.T) {
	clean(t)
	_, notes, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), extension.NewRegistry(),
		env(map[string]string{airgapped.ModeEnv: "1"}))
	require.NoError(t, err)
	require.Contains(t, strings.Join(notes, "\n"), "only this machine is reachable")
}

func TestAnAllowListWithATypoStopsTheBinary(t *testing.T) {
	clean(t)
	_, _, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), extension.NewRegistry(),
		env(map[string]string{
			airgapped.ModeEnv:  "1",
			airgapped.AllowEnv: "https://registry.internal/v2/",
		}))
	require.Error(t, err)
	require.False(t, airgap.Sealed())
}

func TestTheWordsThatMeanYesAreReadAsYes(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on", "enabled", "TRUE", " yes "} {
		clean(t)
		on, _, err := airgapped.RegisterFromEnvironment(
			licensed(license.FeatureAirGapped), extension.NewRegistry(),
			env(map[string]string{airgapped.ModeEnv: value}))
		require.NoErrorf(t, err, "%q", value)
		require.Truef(t, on, "%q left the installation open while its configuration asked for an air gap", value)
	}
	for _, value := range []string{"", "0", "false", "no", "off"} {
		clean(t)
		on, _, err := airgapped.RegisterFromEnvironment(
			licensed(license.FeatureAirGapped), extension.NewRegistry(),
			env(map[string]string{airgapped.ModeEnv: value}))
		require.NoErrorf(t, err, "%q", value)
		require.Falsef(t, on, "%q", value)
	}
}

func TestTheHookRefusesEveryEgressModeThatWouldLeaveTheNetwork(t *testing.T) {
	clean(t)
	airgap.Seal("a test")
	ctx := licensed(license.FeatureAirGapped)

	for mode, why := range map[string]string{
		"allow":   "forwards the request to the real host",
		"sandbox": "still forwards to the real host",
		"synth":   "asks a model provider",
	} {
		err := airgapped.Hook{}.Check(ctx, extension.EnvironmentRequest{
			EgressHosts: []string{"api.stripe.com"},
			EgressModes: map[string]string{"api.stripe.com": mode},
		})
		require.Errorf(t, err, "%s mode reaches the internet and was permitted", mode)
		require.Containsf(t, err.Error(), "api.stripe.com", "%s", mode)
		require.Containsf(t, err.Error(), mode, "%s", mode)
		require.NotEmpty(t, why)
	}
}

func TestTheHookLeavesTheModesThatReachNothing(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped), extension.EnvironmentRequest{
		EgressModes: map[string]string{
			"api.stripe.com": "capture",
			"api.openai.com": "mock",
			"example.com":    "block",
		},
	})
	require.NoError(t, err,
		"an air gapped installation is supposed to be able to run an application "+
			"whose third party calls are captured, and refusing those would make "+
			"the mode useless rather than strict")
}

func TestTheHookNamesEveryOffendingRuleRatherThanTheFirst(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	err := airgapped.Hook{}.Check(licensed(license.FeatureAirGapped), extension.EnvironmentRequest{
		EgressModes: map[string]string{
			"api.stripe.com": "allow",
			"api.openai.com": "synth",
			"s3.amazonaws.com": "sandbox",
			"logs.internal":  "capture",
		},
	})
	require.Error(t, err)
	for _, host := range []string{"api.stripe.com", "api.openai.com", "s3.amazonaws.com"} {
		require.Containsf(t, err.Error(), host, "an operator fixing this needs the whole list, not the first one")
	}
	require.NotContains(t, err.Error(), "logs.internal")
	require.Contains(t, err.Error(), "3 egress rules")
}

func TestTheHookDoesNothingWhenTheProcessIsNotSealed(t *testing.T) {
	clean(t)
	require.NoError(t, airgapped.Hook{}.Check(context.Background(), extension.EnvironmentRequest{
		EgressModes: map[string]string{"api.stripe.com": "allow"},
	}))
}

func TestTheHookKeepsRefusingWhenTheLicenceLapsesUnderIt(t *testing.T) {
	clean(t)
	airgap.Seal("a test")

	// The opposite of every other licence check in this product, on purpose.
	// An installation deployed behind an air gap must not start creating
	// environments that reach the internet because a purchase order was slow.
	err := airgapped.Hook{}.Check(context.Background(), extension.EnvironmentRequest{
		EgressModes: map[string]string{"api.stripe.com": "allow"},
	})
	require.Error(t, err)
}

func TestTheFeatureNowHasADeclaredEnforcementSite(t *testing.T) {
	sites := feature.Sites(license.FeatureAirGapped)
	require.NotEmpty(t, sites,
		"air_gapped was one of nine features the licence sold and nothing enforced")
	require.Contains(t, sites, "ee/engine/airgapped.Hook")
}

func TestTheHookIsRegisteredSoTheEngineActuallyConsultsIt(t *testing.T) {
	clean(t)
	reg := extension.NewRegistry()
	_, _, err := airgapped.RegisterFromEnvironment(
		licensed(license.FeatureAirGapped), reg,
		env(map[string]string{airgapped.ModeEnv: "1"}))
	require.NoError(t, err)

	// Defined, wired, effective. A hook constructed and never added to the
	// registry is the exact shape of the gap this whole lane exists to close.
	err = reg.CheckPolicy(context.Background(), extension.EnvironmentRequest{
		EgressModes: map[string]string{"api.stripe.com": "allow"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "air gapped")
}

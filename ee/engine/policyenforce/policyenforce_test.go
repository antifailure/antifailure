// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package policyenforce_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/ee/engine/policyenforce"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// licensed returns a context with the policy feature granted.
func licensed(t *testing.T) context.Context {
	t.Helper()
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: []license.Feature{license.FeaturePolicy},
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

func request() extension.EnvironmentRequest {
	return extension.EnvironmentRequest{
		Org: "acme", Repository: "acme/app", Branch: "main", EnvID: "af-1",
		EgressHosts: []string{"api.stripe.com", "api.resend.com"},
		EgressModes: map[string]string{
			"api.stripe.com": "sandbox",
			"api.resend.com": "capture",
		},
		MaskedColumns: []string{"users.email", "users.name"},
		Provider:      "docker",
	}
}

// ---------------------------------------------------------------------------
// The licence gate
// ---------------------------------------------------------------------------

func TestWithoutTheLicenceNothingIsEnforced(t *testing.T) {
	t.Parallel()
	// A hook registered under a licence that has since lapsed must stop
	// enforcing. Checking at registration rather than per call would leave an
	// expired customer holding a feature they cannot turn off without a restart.
	hook := policyenforce.NewHook(policyenforce.Policy{
		DeniedHosts: []string{"api.stripe.com"},
	}, nil)

	require.NoError(t, hook.Check(context.Background(), request()),
		"policy was enforced with no licence in the context")
}

func TestWithTheLicenceThePolicyRefuses(t *testing.T) {
	t.Parallel()
	hook := policyenforce.NewHook(policyenforce.Policy{
		DeniedHosts: []string{"api.stripe.com"},
	}, nil)

	err := hook.Check(licensed(t), request())
	require.Error(t, err)
	var refusal *policyenforce.Refusal
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "egress deny list", refusal.Policy)
}

// ---------------------------------------------------------------------------
// Egress
// ---------------------------------------------------------------------------

func TestADeniedHostMayStillBeNamedIfItIsBlocked(t *testing.T) {
	t.Parallel()
	// Refusing the rule outright would mean a repository cannot document that
	// it deliberately blocks something, which is the behaviour the policy is
	// trying to encourage.
	req := request()
	req.EgressModes["api.stripe.com"] = "block"

	hook := policyenforce.NewHook(policyenforce.Policy{
		DeniedHosts: []string{"api.stripe.com"},
	}, nil)
	require.NoError(t, hook.Check(licensed(t), req))
}

func TestADenyEntryWithAWildcardCoversSubdomainsAndNotTheApex(t *testing.T) {
	t.Parallel()
	hook := policyenforce.NewHook(policyenforce.Policy{
		DeniedHosts: []string{"*.internal.acme.com"},
	}, nil)

	sub := request()
	sub.EgressHosts = []string{"db.internal.acme.com"}
	sub.EgressModes = map[string]string{"db.internal.acme.com": "allow"}
	require.Error(t, hook.Check(licensed(t), sub))

	// The apex is a different host and is frequently operated differently,
	// which is the same rule the egress engine follows.
	apex := request()
	apex.EgressHosts = []string{"internal.acme.com"}
	apex.EgressModes = map[string]string{"internal.acme.com": "allow"}
	require.NoError(t, hook.Check(licensed(t), apex))
}

func TestAllowedModesExcludeEverythingNotNamed(t *testing.T) {
	t.Parallel()
	hook := policyenforce.NewHook(policyenforce.Policy{
		AllowedModes: []string{"block", "capture", "mock"},
	}, nil)

	err := hook.Check(licensed(t), request())
	require.Error(t, err, "sandbox mode was permitted by a policy that did not name it")

	safe := request()
	safe.EgressModes["api.stripe.com"] = "mock"
	require.NoError(t, hook.Check(licensed(t), safe))
}

func TestTheSameViolationIsReportedEveryTime(t *testing.T) {
	t.Parallel()
	// Two violations, and the one reported must not depend on map iteration
	// order, or the message changes between identical runs of the same build.
	req := request()
	req.EgressHosts = []string{"b.example.com", "a.example.com"}
	req.EgressModes = map[string]string{
		"a.example.com": "allow", "b.example.com": "allow",
	}
	hook := policyenforce.NewHook(policyenforce.Policy{
		DeniedHosts: []string{"a.example.com", "b.example.com"},
	}, nil)

	first := hook.Check(licensed(t), req)
	require.Error(t, first)
	for range 50 {
		require.Equal(t, first.Error(), hook.Check(licensed(t), req).Error())
	}
	require.Contains(t, first.Error(), "a.example.com", "violations should report in sorted order")
}

// ---------------------------------------------------------------------------
// Synth
// ---------------------------------------------------------------------------

func TestSynthIsRefusedWithoutAnApproval(t *testing.T) {
	t.Parallel()
	req := request()
	req.EgressModes["api.stripe.com"] = "synth"

	hook := policyenforce.NewHook(policyenforce.Policy{SynthRequiresApproval: true}, nil)
	err := hook.Check(licensed(t), req)
	require.Error(t, err)
	// The message says why synth is treated differently, because "policy
	// refuses this" without a reason is a policy people route around.
	require.Contains(t, err.Error(), "unverified")
}

func TestSynthIsPermittedWithAnApproval(t *testing.T) {
	t.Parallel()
	req := request()
	req.EgressModes["api.stripe.com"] = "synth"

	hook := policyenforce.NewHook(
		policyenforce.Policy{SynthRequiresApproval: true},
		func(envID string) policyenforce.Approval {
			require.Equal(t, "af-1", envID, "the approval was looked up for the wrong environment")
			return policyenforce.Approval{Synth: true, By: "ada"}
		},
	)
	require.NoError(t, hook.Check(licensed(t), req))
}

func TestApprovalIsPerEnvironmentAndNotPerRepository(t *testing.T) {
	t.Parallel()
	// An approved run and an unapproved run of the same repository must decide
	// differently, or one approval permanently approves everything after it.
	hook := policyenforce.NewHook(
		policyenforce.Policy{SynthRequiresApproval: true},
		func(envID string) policyenforce.Approval {
			return policyenforce.Approval{Synth: envID == "af-approved"}
		},
	)

	approved := request()
	approved.EnvID = "af-approved"
	approved.EgressModes["api.stripe.com"] = "synth"
	require.NoError(t, hook.Check(licensed(t), approved))

	other := request()
	other.EnvID = "af-other"
	other.EgressModes["api.stripe.com"] = "synth"
	require.Error(t, hook.Check(licensed(t), other))
}

func TestANilApprovalLookupApprovesNothing(t *testing.T) {
	t.Parallel()
	// A control plane that cannot be reached must not approve things by being
	// absent. The safe direction is the default.
	req := request()
	req.EgressModes["api.stripe.com"] = "synth"
	hook := policyenforce.NewHook(policyenforce.Policy{SynthRequiresApproval: true}, nil)
	require.Error(t, hook.Check(licensed(t), req))
}

// ---------------------------------------------------------------------------
// Masking
// ---------------------------------------------------------------------------

func TestARequiredColumnMustBeCovered(t *testing.T) {
	t.Parallel()
	hook := policyenforce.NewHook(policyenforce.Policy{
		RequiredMaskedColumns: []string{"users.ssn"},
	}, nil)

	err := hook.Check(licensed(t), request())
	require.Error(t, err)
	require.Contains(t, err.Error(), "users.ssn")
	// It says what to do, and where: the rule lives in the repository.
	require.Contains(t, err.Error(), "commit it")
}

func TestAWildcardRequirementIsSatisfiedByAMatch(t *testing.T) {
	t.Parallel()
	hook := policyenforce.NewHook(policyenforce.Policy{
		RequiredMaskedColumns: []string{"*.email"},
	}, nil)
	require.NoError(t, hook.Check(licensed(t), request()))
}

func TestARequirementMatchingNothingIsTreatedAsUnsatisfied(t *testing.T) {
	t.Parallel()
	// A policy that quietly passes when the thing it protects is absent stops
	// protecting the moment somebody renames a table.
	req := request()
	req.MaskedColumns = []string{"orders.total"}

	hook := policyenforce.NewHook(policyenforce.Policy{
		RequiredMaskedColumns: []string{"*.ssn"},
	}, nil)
	require.Error(t, hook.Check(licensed(t), req))
}

func TestMaskingComparisonIgnoresCase(t *testing.T) {
	t.Parallel()
	req := request()
	req.MaskedColumns = []string{"Users.Email"}

	hook := policyenforce.NewHook(policyenforce.Policy{
		RequiredMaskedColumns: []string{"users.email"},
	}, nil)
	require.NoError(t, hook.Check(licensed(t), req))
}

// ---------------------------------------------------------------------------
// Placement
// ---------------------------------------------------------------------------

func TestProviderAndRegionAreRestricted(t *testing.T) {
	t.Parallel()
	hook := policyenforce.NewHook(policyenforce.Policy{
		AllowedProviders: []string{"neon"},
		AllowedRegions:   []string{"weu"},
	}, nil)

	wrongProvider := request()
	require.ErrorContains(t, hook.Check(licensed(t), wrongProvider), "docker")

	rightProvider := request()
	rightProvider.Provider = "neon"
	rightProvider.Region = "scus"
	require.ErrorContains(t, hook.Check(licensed(t), rightProvider), "residency")

	compliant := request()
	compliant.Provider = "neon"
	compliant.Region = "weu"
	require.NoError(t, hook.Check(licensed(t), compliant))
}

func TestAnUnknownRegionIsNotRefusedWhenTheRuntimeReportsNone(t *testing.T) {
	t.Parallel()
	// A local Docker runtime reports no region. Refusing every local
	// environment because a residency policy exists would make the policy
	// unusable for anybody who also develops on a laptop.
	req := request()
	req.Region = ""
	hook := policyenforce.NewHook(policyenforce.Policy{AllowedRegions: []string{"weu"}}, nil)
	require.NoError(t, hook.Check(licensed(t), req))
}

// ---------------------------------------------------------------------------
// The property that matters
// ---------------------------------------------------------------------------

func TestAStricterPolicyNeverPermitsMore(t *testing.T) {
	t.Parallel()
	// The whole guarantee in one property. Adding a rule can only refuse more,
	// never fewer, whatever the rule and whatever the environment.
	rng := rand.New(rand.NewPCG(3, 5))
	ctx := licensed(t)

	hosts := []string{"api.stripe.com", "api.resend.com", "sentry.io", "app.posthog.com"}
	modes := []string{"block", "allow", "capture", "mock", "sandbox", "synth"}
	columns := []string{"users.email", "users.ssn", "orders.card", "accounts.token"}

	for range 500 {
		req := extension.EnvironmentRequest{
			Org: "acme", Repository: "acme/app", EnvID: "af-1",
			EgressModes: map[string]string{},
			Provider:    []string{"docker", "neon", "supabase"}[rng.IntN(3)],
		}
		for _, host := range hosts {
			if rng.IntN(2) == 0 {
				continue
			}
			req.EgressHosts = append(req.EgressHosts, host)
			req.EgressModes[host] = modes[rng.IntN(len(modes))]
		}
		for _, column := range columns {
			if rng.IntN(2) == 0 {
				req.MaskedColumns = append(req.MaskedColumns, column)
			}
		}

		base := policyenforce.Policy{}
		if rng.IntN(2) == 0 {
			base.DeniedHosts = []string{hosts[rng.IntN(len(hosts))]}
		}

		// The stricter one is the base plus one more restriction.
		stricter := base
		switch rng.IntN(4) {
		case 0:
			stricter.DeniedHosts = append(append([]string(nil), base.DeniedHosts...),
				hosts[rng.IntN(len(hosts))])
		case 1:
			stricter.RequiredMaskedColumns = []string{columns[rng.IntN(len(columns))]}
		case 2:
			stricter.SynthRequiresApproval = true
		case 3:
			stricter.AllowedProviders = []string{"neon"}
		}

		require.True(t, policyenforce.Stricter(base, stricter))

		basePermits := policyenforce.NewHook(base, nil).Check(ctx, req) == nil
		stricterPermits := policyenforce.NewHook(stricter, nil).Check(ctx, req) == nil

		if stricterPermits {
			require.Truef(t, basePermits,
				"a stricter policy permitted an environment the looser one refused\nrequest: %+v\nbase: %+v\nstricter: %+v",
				req, base, stricter)
		}
	}
}

func TestAnEmptyPolicyRefusesNothing(t *testing.T) {
	t.Parallel()
	// An organization that has configured nothing must behave exactly like the
	// community edition, or enabling the enterprise binary silently changes what
	// their repositories are allowed to do.
	hook := policyenforce.NewHook(policyenforce.Policy{}, nil)
	ctx := licensed(t)

	for _, mode := range []string{"block", "allow", "capture", "mock", "sandbox", "synth"} {
		req := request()
		req.EgressModes["api.stripe.com"] = mode
		require.NoErrorf(t, hook.Check(ctx, req), "an empty policy refused %s mode", mode)
	}
}

// ---------------------------------------------------------------------------
// The hook is a real extension.PolicyHook
// ---------------------------------------------------------------------------

func TestItPlugsIntoTheCommunityExtensionPoint(t *testing.T) {
	t.Parallel()
	// Compile-time and behavioural proof that this reaches the engine at all.
	// A policy that cannot be registered is a policy nobody is enforcing.
	var hook extension.PolicyHook = policyenforce.NewHook(
		policyenforce.Policy{DeniedHosts: []string{"api.stripe.com"}}, nil)

	registry := extension.NewRegistry()
	registry.AddPolicy(hook)
	require.False(t, registry.Empty())
	require.Equal(t, []string{"policy:organization-policy"}, registry.Registered())

	err := registry.CheckPolicy(licensed(t), request())
	require.Error(t, err)

	var refusal *policyenforce.Refusal
	require.True(t, errors.As(err, &refusal), "the refusal did not survive the registry")
	require.Contains(t, err.Error(), "AF-EE-010")
}

func TestTheRefusalCarriesTheDocumentedErrorCode(t *testing.T) {
	t.Parallel()
	// AF-EE-010 is in the error catalog with a cause and a next step, so the
	// refusal has to actually carry it or the catalog entry is dead.
	hook := policyenforce.NewHook(policyenforce.Policy{
		RequiredMaskedColumns: []string{"users.ssn"},
	}, nil)
	err := hook.Check(licensed(t), request())
	require.ErrorContains(t, err, "AF-EE-010")
}

func TestFeatureIsDeclaredSoItCannotBeSoldAndNeverChecked(t *testing.T) {
	t.Parallel()
	require.Contains(t, feature.Sites(license.FeaturePolicy),
		"ee/engine/policyenforce.Hook")
}

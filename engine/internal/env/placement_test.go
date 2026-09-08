package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/edition"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Where an environment actually goes, which until this file nothing decided.
//
// engine/internal/scheduler was imported by exactly one file in the repository,
// its own test. Run.Requires was filled by nothing, because schema.Runtime had
// no field that could carry a requirement. So the scheduler could sort a queue,
// balance repositories, age a batch run and refuse an unsatisfiable placement,
// and no command could reach any of it. These tests are the call sites that
// were missing, and they assert the observable end of the chain rather than
// that the pieces exist: a manifest declaring a requirement is placed on the
// target that meets it, and one declaring a requirement nothing meets is
// refused by a message that names the requirement.

func placed(t *testing.T, r *schema.Runtime) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Runtime: r},
		Branch:   "main",
		Clock:    clock.New(),
	})
	require.NoError(t, err)
	return o
}

// licensed is a context carrying an installation permitted to place.
func licensed() context.Context {
	return edition.With(context.Background(), edition.Status{
		Name: "enterprise", State: "active",
		Features: []string{edition.FeatureMultiRuntime},
	})
}

func target(name, region string, provider schema.RuntimeProvider) schema.RuntimeTarget {
	return schema.RuntimeTarget{
		Name: name, Provider: provider,
		Tags: map[string]string{schema.RegionTag: region},
	}
}

func TestPlacement_PutsTheEnvironmentOnTheTargetThatMeetsTheRequirement(t *testing.T) {
	t.Parallel()
	// The second target is the one that matches, so a placement that simply
	// took the first would pass a test written against a one entry list and
	// fail here. The order is deliberate.
	o := placed(t, &schema.Runtime{
		Requires: map[string]string{schema.RegionTag: "eu-west-1"},
		Targets: []schema.RuntimeTarget{
			target("us", "us-east-1", schema.RuntimeLocal),
			target("eu", "eu-west-1", schema.RuntimeLocal),
		},
	})
	chosen, err := o.placement(licensed())
	require.NoError(t, err)
	require.Equal(t, "eu", chosen.Name)
}

func TestPlacement_RefusesARequirementNoTargetOffersAndNamesIt(t *testing.T) {
	t.Parallel()
	o := placed(t, &schema.Runtime{
		Requires: map[string]string{schema.RegionTag: "eu-west-2"},
		Targets: []schema.RuntimeTarget{
			target("us", "us-east-1", schema.RuntimeLocal),
			target("eu", "eu-west-1", schema.RuntimeLocal),
		},
	})
	_, err := o.placement(licensed())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFSCH001))
	// The unmet requirement, not a count of targets. "No runtime is available"
	// and "nothing satisfies region=eu-west-2" send somebody to two different
	// people, and only the second one can be acted on.
	require.Contains(t, err.Error(), "region=eu-west-2")
}

func TestPlacement_TakesTheFirstTargetWhenNothingIsRequired(t *testing.T) {
	t.Parallel()
	// Preference order, and it has to be stable: af up, af status, af logs and
	// af down each decide independently, and a placement that varied between
	// them would have af status asking the wrong cluster about an environment
	// and reporting that it does not exist.
	o := placed(t, &schema.Runtime{
		Targets: []schema.RuntimeTarget{
			target("first", "us-east-1", schema.RuntimeLocal),
			target("second", "eu-west-1", schema.RuntimeLocal),
		},
	})
	for i := 0; i < 8; i++ {
		chosen, err := o.placement(licensed())
		require.NoError(t, err)
		require.Equal(t, "first", chosen.Name)
	}
}

func TestPlacement_IsRefusedWithoutTheLicenceAndSaysWhichFeature(t *testing.T) {
	t.Parallel()
	// The entitlement turned off and the refusal observed, which is the only
	// form of this claim worth having. Reading the gate in the source says a
	// check is written; this says it answers no.
	o := placed(t, &schema.Runtime{
		Targets: []schema.RuntimeTarget{
			target("us", "us-east-1", schema.RuntimeLocal),
			target("eu", "eu-west-1", schema.RuntimeLocal),
		},
	})
	_, err := o.placement(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFEE011))
	require.Contains(t, err.Error(), "multi_runtime")
	require.Contains(t, err.Error(), "2 placement targets")
}

func TestPlacement_IsRefusedWhenTheLicenceCarriesEveryOtherFeature(t *testing.T) {
	t.Parallel()
	// A licence is not a boolean, and a gate that passed for any licence at all
	// would be indistinguishable from the one above on a context with no
	// edition attached. This is the case that separates them.
	o := placed(t, &schema.Runtime{
		Targets: []schema.RuntimeTarget{
			target("us", "us-east-1", schema.RuntimeLocal),
			target("eu", "eu-west-1", schema.RuntimeLocal),
		},
	})
	ctx := edition.With(context.Background(), edition.Status{
		Name: "enterprise", State: "active",
		Features: []string{"sso", "scim", "policy_enforcement", "compliance_packs"},
	})
	_, err := o.placement(ctx)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFEE011))
}

func TestPlacement_OneTargetIsALabelAndNeedsNoLicence(t *testing.T) {
	t.Parallel()
	// One target decides nothing. It says where the single runtime this build
	// already had is, so that an organization residency policy has something to
	// read, and charging for a label would be charging for the community
	// edition. The count is what is sold.
	o := placed(t, &schema.Runtime{
		Targets: []schema.RuntimeTarget{target("only", "eu-west-1", schema.RuntimeLocal)},
	})
	chosen, err := o.placement(context.Background())
	require.NoError(t, err)
	require.Equal(t, "only", chosen.Name)
}

func TestNewRuntime_BuildsTheRuntimeTheChosenTargetNames(t *testing.T) {
	t.Parallel()
	// The end of the chain. A decision that named a target and then built
	// something else would satisfy every test above and place the environment
	// in the wrong place, which is the failure newRuntime's own comment is
	// written against.
	o := placed(t, &schema.Runtime{
		Requires: map[string]string{schema.RegionTag: "eu-west-1"},
		Targets: []schema.RuntimeTarget{
			{
				Name: "cluster", Provider: schema.RuntimeKubernetes,
				Domain: "eu.example.com", NamespacePrefix: "af",
				KubeconfigContext: "no-such-context-in-any-kubeconfig",
				Tags:              map[string]string{schema.RegionTag: "us-east-1"},
			},
			{
				Name: "laptop", Provider: schema.RuntimeLocal,
				Domain: "localhost", NamespacePrefix: "af",
				Tags: map[string]string{schema.RegionTag: "eu-west-1"},
			},
		},
	})
	rt, err := o.newRuntime(licensed())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.Equal(t, "local", rt.Name())
}

func TestNewRuntime_WithNoTargetsIsTheProviderTheManifestNames(t *testing.T) {
	t.Parallel()
	// The control. Every manifest in the world declares no targets, and if the
	// placement path had taken over the unplaced one, all of them would now go
	// through a licence gate. A community build must still bring up an
	// environment with no licence at all.
	o := placed(t, &schema.Runtime{Provider: schema.RuntimeLocal})
	rt, err := o.newRuntime(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.Equal(t, "local", rt.Name())
}

func TestPlacement_RefusesWhenThereIsNoTargetAtAllRatherThanPlacingAnywhere(t *testing.T) {
	t.Parallel()
	// A manifest built in memory rather than parsed, which is what the MCP
	// server and the library callers do, so validation never saw it. The engine
	// has to refuse it here too: a requirement with nothing to satisfy it must
	// not fall through to the default runtime, because falling through is how a
	// residency requirement becomes a comment.
	o := placed(t, &schema.Runtime{
		Requires: map[string]string{schema.RegionTag: "eu-west-1"},
		Targets:  []schema.RuntimeTarget{target("us", "us-east-1", schema.RuntimeLocal)},
	})
	_, err := o.placement(licensed())
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFSCH001))
	require.Contains(t, err.Error(), "region=eu-west-1")
}

func TestRuntimeIdentity_IsTheTargetNameSoTheRegistryCanTellTwoClustersApart(t *testing.T) {
	t.Parallel()
	// What the control plane writes into the environments row, and what its
	// runtime registry compares against the runtimes an organization agreed to.
	// It was the literal "local" for every environment ever created, so a
	// Kubernetes environment reported that it came up on the local runtime and
	// the registry's whole reason for existing, seeing an environment running
	// somewhere nobody agreed to, could not see one.
	//
	// The target name rather than the kind, because "kubernetes" is the same
	// word for every cluster a fleet has and the registry's question is which
	// one.
	o := placed(t, &schema.Runtime{
		Requires: map[string]string{schema.RegionTag: "eu-central-1"},
		Targets: []schema.RuntimeTarget{
			target("virginia", "us-east-1", schema.RuntimeLocal),
			target("frankfurt", "eu-central-1", schema.RuntimeLocal),
		},
	})
	require.Equal(t, "frankfurt", o.runtimeIdentity(licensed(), nil))
}

func TestRuntimeIdentity_FallsBackToTheRuntimeItActuallyBuilt(t *testing.T) {
	t.Parallel()
	// An unplaced manifest, which is every manifest that existed before this.
	// The runtime's own name is at least true, and it is what the registry has
	// always been given for a local environment.
	o := placed(t, &schema.Runtime{Provider: schema.RuntimeLocal})
	rt, err := o.newRuntime(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	require.Equal(t, "local", o.runtimeIdentity(context.Background(), &session{runtime: rt}))
}

func TestPlacement_RefusesAManifestWithNoRuntimeBlockRatherThanCrashing(t *testing.T) {
	t.Parallel()
	// Normalization fills the block in on anything parsed, so this is the
	// caller that built one in memory and skipped it. A refusal rather than a
	// nil dereference halfway through bringing an environment up.
	o := placed(t, nil)
	_, err := o.placement(licensed())
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFSCH003))
	require.Contains(t, err.Error(), "no runtime block")
}

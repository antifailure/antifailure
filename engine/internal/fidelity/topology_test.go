package fidelity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The topology dimension: how many instances of each service are running,
// against how many the manifest asked for.
//
// The gap it closes is the one the services dimension cannot: a service that
// is up is reported reproduced whether one of its three instances is running
// or all three, so every bug that only appears above one instance was
// invisible in the one report whose job is to say what a twin does not
// reproduce.

// asked sets the instance count a service declares and what the runtime is
// running for it, leaving the rest of the working observation alone.
func asked(want, got int) fidelity.Observation {
	obs := full()
	obs.Manifest.Services[0].Replicas = want
	obs.Running[0].Instances = got
	obs.Running[1].Instances = 1
	return obs
}

func TestAServiceRunningTheCountItAskedForIsReproduced(t *testing.T) {
	t.Parallel()
	c := componentState(t, fidelity.Build(asked(3, 3)), schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Reproduced, c.State)
	require.Equal(t, "3 of 3 instances", c.Detail)
}

// The bug class this dimension exists for, and the one the services dimension
// reports as reproduced.
func TestAServiceRunningFewerInstancesThanItAskedForIsAbsent(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(asked(3, 1))

	c := componentState(t, inv, schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Absent, c.State)
	require.Contains(t, c.Detail, "1 of 3 instances")
	require.Contains(t, c.Detail, "only breaks above one instance")

	// The same service, in the same report, in the dimension that cannot see
	// it. Both are correct and that is the argument for the second one: the
	// service IS up, and the environment is NOT the shape the manifest asked
	// for.
	require.Equal(t, fidelity.Reproduced,
		componentState(t, inv, schema.FidelityServices, "web").State)
}

// The score is the deliverable, so the state has to reach it.
func TestAShortInstanceCountLowersTheScore(t *testing.T) {
	t.Parallel()
	whole := fidelity.Build(asked(3, 3)).Score()
	short := fidelity.Build(asked(3, 1)).Score()

	require.Equal(t, whole.Counted, short.Counted,
		"the two environments differ by an instance count, not by a component")
	require.Equal(t, whole.Reproduced-1, short.Reproduced)

	full, ok := whole.Percent()
	require.True(t, ok)
	partial, ok := short.Percent()
	require.True(t, ok)
	require.Less(t, partial, full,
		"running one of three instances scored the same as running three, which is the defect")
}

// More than asked for is a mismatch too. A rolling update leaves the old pods
// beside the new ones, and an instance nothing removed stays.
func TestAServiceRunningMoreInstancesThanItAskedForIsAbsent(t *testing.T) {
	t.Parallel()
	c := componentState(t, fidelity.Build(asked(2, 5)), schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Absent, c.State)
	require.Contains(t, c.Detail, "5 instances running and 2 asked for")
}

func TestAServiceThatAskedForInstancesAndIsNotRunningIsAbsent(t *testing.T) {
	t.Parallel()
	obs := asked(3, 3)
	obs.Running = obs.Running[1:]
	c := componentState(t, fidelity.Build(obs), schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Absent, c.State)
	require.Contains(t, c.Detail, "3 instances asked for and nothing is running")
}

// The rule the whole file rests on. A service that names no count has said
// nothing about how many instances production runs, and one is what an omitted
// key means rather than something anybody compared.
func TestAServiceThatNamesNoCountIsUnmeasuredRatherThanReproduced(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(asked(3, 3))

	c := componentState(t, inv, schema.FidelityTopology, "worker")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "names no count")

	var named bool
	for _, e := range inv.Score().Excluded {
		if e.Dimension == schema.FidelityTopology && e.Component == "worker" {
			named = true
		}
	}
	require.True(t, named, "the service was excluded from the score and not named, which is the defect")
}

// The common case, and the reason it is one line rather than one unmeasured
// component per service: every manifest in this repository names no counts at
// all, and a report that printed a row for each of them would be saying the
// same sentence five times.
func TestAManifestThatNamesNoCountAtAllExcludesTheDimensionWhole(t *testing.T) {
	t.Parallel()
	d, ok := fidelity.Build(full()).Dimension(schema.FidelityTopology)
	require.True(t, ok)
	require.Empty(t, d.Components)
	require.Contains(t, d.NotApplicable, "no service says how many instances it runs")
	require.Contains(t, d.NotApplicable, "runs one of each")
}

func TestTheDimensionSaysNothingWhenTheManifestDeclaresNoServices(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Manifest.Services = nil
	d, ok := fidelity.Build(obs).Dimension(schema.FidelityTopology)
	require.True(t, ok)
	require.Equal(t, "the manifest declares no services", d.NotApplicable)
}

func TestARuntimeThatCouldNotBeAskedLeavesEveryCountUnmeasured(t *testing.T) {
	t.Parallel()
	obs := asked(3, 3)
	obs.ServicesReason = "the runtime could not be reached: no such daemon"
	c := componentState(t, fidelity.Build(obs), schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "no such daemon")
}

// A runtime that predates instance counts reports none, and every other reader
// treats that as one rather than as none. This dimension is the one place
// where "at least one" is not an answer to the question being asked.
func TestARuntimeThatReportsNoCountIsUnmeasuredRatherThanOne(t *testing.T) {
	t.Parallel()
	c := componentState(t, fidelity.Build(asked(3, 0)), schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "did not say how many instances")
	require.Contains(t, c.Detail, "asked for 3")
}

// A manifest can require this dimension, and the two failures have to stay
// distinct: measured and short is a fact about the environment, and nothing
// measurable is a fact about what could be seen.
func TestRequiringTopologySeparatesShortFromUnmeasurable(t *testing.T) {
	t.Parallel()
	require.Contains(t, schema.AllFidelityDimensions(), schema.FidelityTopology)

	// Every service names a count and every count is met.
	met := asked(3, 3)
	met.Manifest.Services[1].Replicas = 1
	got := fidelity.Build(met).Check([]schema.FidelityDimension{schema.FidelityTopology})
	require.Len(t, got, 1)
	require.True(t, got[0].Met)
	require.True(t, got[0].Measurable)

	// One of them short. Measurable and not met, which exits differently from
	// a dimension nothing could measure.
	short := asked(3, 1)
	short.Manifest.Services[1].Replicas = 1
	got = fidelity.Build(short).Check([]schema.FidelityDimension{schema.FidelityTopology})
	require.Len(t, got, 1)
	require.False(t, got[0].Met)
	require.True(t, got[0].Measurable)
	require.Contains(t, got[0].Because, "web is absent")

	// One of them naming no count at all. Neither met nor broken.
	got = fidelity.Build(asked(3, 3)).Check([]schema.FidelityDimension{schema.FidelityTopology})
	require.Len(t, got, 1)
	require.False(t, got[0].Met)
	require.False(t, got[0].Measurable)
	require.Contains(t, got[0].Because, "worker")
}

func TestTheTopologyDimensionIsInEveryReport(t *testing.T) {
	t.Parallel()
	// Always all of them, in AllFidelityDimensions order, so a dimension that
	// measured nothing is visibly there rather than missing from the document.
	text := fidelity.Build(asked(3, 1)).Explain()
	require.True(t, strings.Contains(text, "topology"),
		"the topology dimension is missing from a report of an environment that has one")
	require.Contains(t, text, "1 of 3 instances")

	inv := fidelity.Build(fidelity.Observation{})
	require.Len(t, inv.Dimensions, len(schema.AllFidelityDimensions()))
	require.Equal(t, schema.FidelityTopology, inv.Dimensions[len(inv.Dimensions)-1].Name)
}

// The observation this file builds on carries a real runtime type, so a change
// to it moves these numbers for a reason that has nothing to do with topology.
func TestTheInstanceCountReadIsTheRuntimesOwn(t *testing.T) {
	t.Parallel()
	obs := asked(3, 3)
	require.IsType(t, provider.RunningService{}, obs.Running[0])
	require.Equal(t, 3, obs.Running[0].Instances)
	require.Equal(t, 3, obs.Manifest.Services[0].Replicas)
}

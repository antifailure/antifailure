package fidelity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What the report says about a store somebody declared.
//
// The dimension already named a store it recognised from a service image, and
// said it could not measure it. What it could not say was whether anybody had
// CHOSEN that. An empty ClickHouse nobody noticed and an empty Redis somebody
// decided on read identically from inside a running environment, and the
// declared stance is the only thing that tells them apart afterwards.

func analyticsStack(t *testing.T) fidelity.Observation {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "analytics-stack.yaml"))
	require.NoError(t, err)
	m, err := manifest.Parse(body, "antifailure.yaml", "")
	require.NoError(t, err)

	obs := full()
	obs.Manifest = m
	// The three store services, running, and the two stance jobs this
	// environment's own run recorded. Without them the fixture is an
	// environment that declares three stores and holds none, which is a real
	// answer the tests below assert separately and is not the ordinary one.
	for _, name := range []string{"events", "cache", "bus"} {
		obs.Running = append(obs.Running,
			provider.RunningService{Name: name, Kind: "worker", Ready: true})
	}
	obs.Stances = []fidelity.Stance{
		{Store: "cache", Running: true},
		{Store: "bus", Running: true, Ran: true},
	}
	return obs
}

func TestADeclaredStanceIsCarriedIntoTheReport(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(analyticsStack(t))

	events := componentState(t, inv, schema.FidelityDatastores, "events")
	// Absent rather than unmeasured, and this is the change L4.7 made. The
	// manifest asked for a masked, verified copy of production in this store
	// and this build has none, which is a fact about the environment rather
	// than a gap in what can be seen. See TestADeclaredGoldenStoreIsCounted
	// for what that does to the score.
	require.Equal(t, fidelity.Absent, events.State)
	require.Contains(t, events.Detail, "a clickhouse declared golden")
	require.Contains(t, events.Detail,
		"no golden, no attestation, no tables and no rows")

	bus := componentState(t, inv, schema.FidelityDatastores, "bus")
	require.Contains(t, bus.Detail, "a kafka declared topics_only")
	// What this environment DID, not what the manifest said. The sentence used
	// to be "nothing here created a topic", which was true when it was written
	// and became false the moment af up learned to create them, and a report
	// describing a build that no longer exists is the worst failure an
	// instrument has.
	require.Equal(t, fidelity.Substituted, bus.State)
	require.Contains(t, bus.Detail, "created 2 topics and 3 consumer groups in it")
	require.Contains(t, bus.Detail, "no messages")
}

func TestABrokerThatIsRunningAndWasNeverShapedIsNotCountedAsShaped(t *testing.T) {
	t.Parallel()
	// The environment somebody brought up with a build that had no stance
	// jobs. Same manifest, same services, same images, and a broker with
	// nothing in it. Only the run journal can tell the two apart, so a store
	// whose job this environment recorded nothing about stays unmeasured
	// rather than being credited with the shape its manifest asks for.
	obs := analyticsStack(t)
	obs.Stances = []fidelity.Stance{
		{Store: "cache", Running: true},
		{Store: "bus", Running: true, RanReason: "this environment's run recorded no job"},
	}
	bus := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, "bus")
	require.Equal(t, fidelity.Unmeasured, bus.State)
	require.Contains(t, bus.Detail, "recorded no job")
}

func TestAStoreNothingAskedAboutIsNotReportedAsMissing(t *testing.T) {
	t.Parallel()
	// Silence must not read as a negative. A caller that never took the stance
	// observation would otherwise have every declared cache reported absent,
	// which is a real zero in a real denominator for a question nobody put.
	obs := analyticsStack(t)
	obs.Stances = nil
	for _, name := range []string{"cache", "bus"} {
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, name)
		require.Equalf(t, fidelity.Unmeasured, c.State, "%s", name)
		require.Contains(t, c.Detail, "nothing here asked what this environment did about it")
	}
}

func TestADeclaredStoreThatIsNotRunningIsAbsent(t *testing.T) {
	t.Parallel()
	// The manifest asked the environment to hold a store and it does not hold
	// one. Absent is a fact about the environment rather than a gap in what
	// can be seen, so it belongs in the denominator whatever the stance says.
	obs := analyticsStack(t)
	obs.Stances = []fidelity.Stance{{Store: "cache"}, {Store: "bus"}}
	for _, name := range []string{"cache", "bus"} {
		c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, name)
		require.Equalf(t, fidelity.Absent, c.State, "%s", name)
		require.Contains(t, c.Detail, "no service of that name is running")
	}
}

func TestAStoreNothingCouldBeAskedAboutIsUnmeasured(t *testing.T) {
	t.Parallel()
	// The runtime could not be reached, so nothing here knows whether the
	// store is running. That is a different answer from knowing it is not, and
	// reporting it absent would put a real zero in a real denominator for a
	// question nobody managed to ask.
	obs := analyticsStack(t)
	obs.Stances = []fidelity.Stance{
		{Store: "cache", RunningReason: "the runtime could not be reached"},
		{Store: "bus", RunningReason: "the runtime could not be reached"},
	}
	c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, "cache")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "the runtime could not be reached")
}

func TestADerivedStoreReportsTheCommandThatBuiltIt(t *testing.T) {
	t.Parallel()
	// The rebuild is the whole argument for the stance, so the report names
	// it: what ran, where it ran, and what it read. A reader deciding whether
	// to trust a search result in this twin needs all three.
	obs := analyticsStack(t)
	obs.Manifest.Datastores = append(obs.Manifest.Datastores, schema.Datastore{
		Name: "search", Engine: "elasticsearch", Stance: schema.StanceDerived,
		From: "primary", Because: "an index cloned from production is stale against the branch",
		Rebuild: &schema.DatastoreRebuild{Service: "web", Command: "bin/reindex --all"},
	})
	obs.Running = append(obs.Running,
		provider.RunningService{Name: "search", Kind: "worker", Ready: true})
	obs.Stances = append(obs.Stances, fidelity.Stance{Store: "search", Running: true, Ran: true})

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, "search")
	require.Equal(t, fidelity.Substituted, c.State)
	require.Contains(t, c.Detail, "an elasticsearch declared derived from primary")
	require.Contains(t, c.Detail, `rebuilt it from primary by running "bin/reindex --all" in web`)
	require.Contains(t, c.Detail, "rather than copied from production")
}

func TestADeclaredEmptyStoreCarriesTheReasonSomebodyWrote(t *testing.T) {
	t.Parallel()
	// The sentence is the author's, word for word, and it is in the report
	// rather than in a comment nobody reading the report will ever see.
	c := componentState(t, fidelity.Build(analyticsStack(t)), schema.FidelityDatastores, "cache")
	require.Contains(t, c.Detail, "a redis declared empty")
	require.Contains(t, c.Detail,
		"because a cache is rebuilt from the primary and a copy would be noise")
}

func TestADeclaredEmptyStoreIsStillNotCountedAsReproduced(t *testing.T) {
	t.Parallel()
	// Substituted, not reproduced, and the distinction is the point. An empty
	// cache holds nothing production holds. It stands in and behaves, which is
	// what this package means by substituted, and it counts against the score
	// because a twin whose cache is empty is not a twin of production's cache.
	//
	// Not unmeasured either, which is what it used to be. That held it out of
	// the number in both directions, so an honest position and an accident
	// scored the same.
	c := componentState(t, fidelity.Build(analyticsStack(t)), schema.FidelityDatastores, "cache")
	require.Equal(t, fidelity.Substituted, c.State)
	require.Contains(t, c.Detail, "it is running and holds nothing, which is the stance")
	require.NotEqual(t, fidelity.Reproduced, c.State)
}

func TestAStoreWithNoDeclaredReasonSaysSo(t *testing.T) {
	t.Parallel()
	// The absence is said out loud rather than left as a shorter sentence. An
	// empty store somebody decided on and one nobody explained score the same
	// here, and this sentence is the only thing that tells a reviewer which
	// one they are reading.
	obs := analyticsStack(t)
	for i := range obs.Manifest.Datastores {
		if obs.Manifest.Datastores[i].Name == "bus" {
			obs.Manifest.Datastores[i].Because = ""
		}
	}
	c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, "bus")
	require.Contains(t, c.Detail, "with no reason declared")
}

func TestThePrimaryIsNotReportedTwice(t *testing.T) {
	t.Parallel()
	// It is normalized into the datastores list, and the database dimension
	// measures it properly: which golden, whether verified, whether the
	// attestation still checks out. Reporting it here as well would count one
	// store twice and would put the one store this build DOES reproduce into
	// the dimension whose subject is the ones it does not.
	inv := fidelity.Build(analyticsStack(t))
	d, ok := inv.Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	for _, c := range d.Components {
		require.NotEqual(t, schema.PrimaryDatastore, c.Name,
			"the primary is in the datastores dimension as well as the database one")
	}
	// Three stores and the question about the pair. The fourth line is named
	// rather than absorbed into a count, because a test that only counted
	// would go on passing if the cross store line silently replaced a store.
	require.Len(t, d.Components, 4)
	require.Equal(t, fidelity.CrossStoreComponent, d.Components[3].Name)
}

func TestADeclaredStoreIsNotAlsoReportedAsTheServiceRunningIt(t *testing.T) {
	t.Parallel()
	// The fixture declares three stores AND runs three services carrying
	// their images, which is what a real manifest looks like. Recognising the
	// service as well would report each store twice, once with a stance and
	// once without.
	inv := fidelity.Build(analyticsStack(t))
	d, _ := inv.Dimension(schema.FidelityDatastores)
	seen := map[string]int{}
	for _, c := range d.Components {
		seen[c.Name]++
	}
	for name, n := range seen {
		require.Equalf(t, 1, n, "%s is reported %d times", name, n)
	}
}

func TestAnUndeclaredStoreIsStillRecognisedFromItsService(t *testing.T) {
	t.Parallel()
	// The manifest written before the list existed does not stop being
	// measured. It gets the older sentence, which says nothing about a stance
	// because there is none to say anything about.
	inv := fidelity.Build(withStores(imageService("events", "clickhouse/clickhouse-server:24.3")))
	c := componentState(t, inv, schema.FidelityDatastores, "events")
	require.Contains(t, c.Detail, "came up empty")
	require.NotContains(t, c.Detail, "declared")
}

func TestTheReportNamesEveryStanceDistinctly(t *testing.T) {
	t.Parallel()
	// A reader with thirty seconds sees the rendered report, not the struct,
	// so the stance has to survive the rendering.
	out := fidelity.Build(analyticsStack(t)).Explain()

	// The datastores block, not the whole report. The mutation pass caught
	// the version of this that searched everything: the services dimension
	// says "declared and not running" about the same three names, so an
	// assertion about the report as a whole passes on the strength of a
	// sentence from a different dimension.
	block := datastoresBlock(t, out)
	for _, want := range []string{"declared golden", "declared empty", "declared topics_only"} {
		require.Containsf(t, block, want, "the datastores block does not say %q:\n%s", want, block)
	}
}

// datastoresBlock returns the datastores dimension's own lines, which is the
// only part of the report this file has anything to say about.
func datastoresBlock(t *testing.T, report string) string {
	t.Helper()
	for _, block := range strings.Split(report, "\n\n") {
		if strings.HasPrefix(block, string(schema.FidelityDatastores)) {
			return block
		}
	}
	t.Fatalf("the rendered report has no datastores block:\n%s", report)
	return ""
}

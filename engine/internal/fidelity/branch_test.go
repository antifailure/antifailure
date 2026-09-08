package fidelity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The report reading the branch rather than the declaration.
//
// The defect these are the check for is one the product documented about
// itself: the datastores dimension was built from the manifest alone, so it
// reported a store declared golden as absent and named the golden, the
// attestation, the tables and the rows it could not see, whether or not the
// environment had branched one. An instrument that understates a twin holding
// a masked, verified second store is better than one that overstates it and is
// still an instrument saying something untrue about what it can see.

// branchedEvents is the store an environment holds once af up has branched it.
func branchedEvents() fidelity.Store {
	return fidelity.Store{
		Name:     "events",
		Golden:   "gv_20260907101500_9f3c2b71",
		Attested: true,
		Attestation: "6 columns read back over 1000 rows sampled, signed and still " +
			"matching its signature",
		Tables: 4,
		Rows:   1000000,
	}
}

// branchedTwin is the analytics twin AFTER af up, which is the only thing that
// separates it from the observation the score benchmark measures.
func branchedTwin(t *testing.T) fidelity.Observation {
	t.Helper()
	obs := analyticsTwin(t)
	obs.Stores = []fidelity.Store{branchedEvents()}
	return obs
}

func TestABranchedGoldenStoreReportsWhatTheBranchHolds(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(branchedTwin(t))

	data := componentState(t, inv, schema.FidelityDatastores, "events data")
	require.Contains(t, data.Detail, "4 tables over 1000000 rows",
		"the environment holds a masked copy of production's events and the report does not say what it holds")
	require.Contains(t, data.Detail, "branched from gv_20260907101500_9f3c2b71")
	// Unmeasured rather than reproduced, and this is the primary database's
	// own rule arriving for the second store: a branch nothing was compared
	// against production has not been shown to reproduce it. The report says
	// what the branch holds and says what nothing here knows about production,
	// which is two facts rather than a verdict with no denominator.
	require.Equal(t, fidelity.Unmeasured, data.State)
	require.Contains(t, data.Detail, "nothing here says what production's events holds")

	prov := componentState(t, inv, schema.FidelityDatastores, "events provenance")
	require.Equal(t, fidelity.Reproduced, prov.State)
	require.Contains(t, prov.Detail, "golden gv_20260907101500_9f3c2b71")
	require.Contains(t, prov.Detail, "6 columns read back over 1000 rows sampled")
}

func TestAGoldenStoreNothingBranchedIsStillAbsentWithTheFourFactsNamed(t *testing.T) {
	t.Parallel()
	// The line the lane that wrote this dimension put there, and it has to
	// survive. An environment that has not branched its second store is not a
	// twin of a stack whose events are the product, and the report saying so
	// is the number going down on purpose.
	inv := fidelity.Build(analyticsTwin(t))

	events := componentState(t, inv, schema.FidelityDatastores, "events")
	require.Equal(t, fidelity.Absent, events.State)
	require.Contains(t, events.Detail, "no golden, no attestation, no tables and no rows")

	d, ok := inv.Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	for _, c := range d.Components {
		require.NotContains(t, c.Name, " data",
			"a store nothing branched is reported as if something had")
	}
}

func TestAStoreOnAnotherStanceIsNotReportedFromABranch(t *testing.T) {
	t.Parallel()
	// The narrowness of this change, asserted rather than described. A store
	// declared empty, derived or topics_only stays unmeasured whatever arrives
	// in the observation, because nothing in this build starts one, rebuilds
	// one or creates a topic in one, and a store reported reproduced on the
	// strength of a declaration is the report believing a manifest instead of
	// an environment.
	obs := analyticsTwin(t)
	obs.Stores = []fidelity.Store{{
		Name: "cache", Golden: "gv_20260907101500_9f3c2b71", Attested: true,
		Attestation: "signed and still matching its signature", Tables: 1, Rows: 5,
	}}
	inv := fidelity.Build(obs)

	cache := componentState(t, inv, schema.FidelityDatastores, "cache")
	require.Equal(t, fidelity.Unmeasured, cache.State)
	require.Contains(t, cache.Detail, "a redis declared empty")
}

func TestABranchWhoseGoldenLostItsAttestationFailsProvenanceAndNotData(t *testing.T) {
	t.Parallel()
	// Two components rather than one verdict over both, which is the reason
	// the database dimension has two. The branch holds production's rows and
	// nothing can show what was done to them before they arrived, and a single
	// verdict would hide whichever of those failed.
	obs := branchedTwin(t)
	obs.Stores[0].Attested = false
	obs.Stores[0].Attestation = "golden gv_20260907101500_9f3c2b71 is no longer marked verified"
	inv := fidelity.Build(obs)

	require.Equal(t, fidelity.Absent,
		componentState(t, inv, schema.FidelityDatastores, "events provenance").State)
	require.Contains(t,
		componentState(t, inv, schema.FidelityDatastores, "events provenance").Detail,
		"no longer marked verified")
	require.Equal(t, "4 tables over 1000000 rows, branched from gv_20260907101500_9f3c2b71"+
		", and nothing here says what production's events holds, so whether this branch "+
		"reproduces it is unknown. The volume profile that answers that for the primary "+
		"database, under database.volume, has no equivalent for a second store yet",
		componentState(t, inv, schema.FidelityDatastores, "events data").Detail,
		"a golden that lost its attestation changed what the report says the branch holds")
}

func TestAStoreTheProviderCouldNotBeAskedAboutIsExcludedAndNamed(t *testing.T) {
	t.Parallel()
	// Unmeasured, in neither half of the number, and named in the exclusions
	// list the headline points at. A store nothing could read is not a store
	// shown to be missing, and reporting the second as the first is how a
	// report stops being believed.
	obs := branchedTwin(t)
	obs.Stores[0].BranchReason = "the events datastore could not be asked what it holds: connection refused"
	obs.Stores[0].GoldenReason = "the events datastore could not list its goldens: connection refused"
	inv := fidelity.Build(obs)

	require.Equal(t, fidelity.Unmeasured,
		componentState(t, inv, schema.FidelityDatastores, "events data").State)
	require.Equal(t, fidelity.Unmeasured,
		componentState(t, inv, schema.FidelityDatastores, "events provenance").State)

	var named int
	for _, e := range inv.Score().Excluded {
		if e.Dimension == schema.FidelityDatastores && strings.HasPrefix(e.Component, "events ") {
			require.Contains(t, e.Because, "connection refused")
			named++
		}
	}
	require.Equal(t, 2, named, "a store nothing could read was dropped from the report rather than named")
}

func TestAStoreWithNoSourceIsASubstitutionRatherThanAReproduction(t *testing.T) {
	t.Parallel()
	// The overstating half of the same defect. A golden built with no source
	// has production's shape and none of its rows, and a branch of one is not
	// a copy of production however many tables it carries.
	obs := branchedTwin(t)
	for i := range obs.Manifest.Datastores {
		if obs.Manifest.Datastores[i].Name == "events" {
			obs.Manifest.Datastores[i].SourceURLEnv = ""
		}
	}
	obs.Stores[0].Rows = 0
	inv := fidelity.Build(obs)

	data := componentState(t, inv, schema.FidelityDatastores, "events data")
	require.Equal(t, fidelity.Substituted, data.State,
		"a store with no source was improved to a reproduction, or lost to an unknown")
	require.Contains(t, data.Detail, "production's shape with none of its rows")
}

func TestEveryBranchedStoreIsReportedSeparately(t *testing.T) {
	t.Parallel()
	// Two golden stores in one environment, which is what a stack with events
	// and a search index looks like. Four components, all distinct, because a
	// name collision here would silently drop one store's answer out of the
	// score.
	obs := branchedTwin(t)
	obs.Manifest.Datastores = append(obs.Manifest.Datastores, schema.Datastore{
		Name: "search", Engine: "elasticsearch", Stance: schema.StanceGolden,
		SourceURLEnv: "PRODUCTION_SEARCH_URL",
	})
	obs.Stores = append(obs.Stores, fidelity.Store{
		Name: "search", Golden: "gv_20260907101500_11223344", Attested: true,
		Attestation: "2 columns read back over 40 rows sampled", Tables: 1, Rows: 40,
	})
	d, ok := fidelity.Build(obs).Dimension(schema.FidelityDatastores)
	require.True(t, ok)

	seen := map[string]int{}
	for _, c := range d.Components {
		seen[c.Name]++
	}
	require.Equal(t, map[string]int{
		"cache": 1, "events data": 1, "events provenance": 1,
		"search data": 1, "search provenance": 1,
	}, seen)
}

func TestTheRenderedReportNamesTheGoldenTheAttestationTheTablesAndTheRows(t *testing.T) {
	t.Parallel()
	// The four facts the documented defect said the report named as missing
	// from a store that had them, in the block a reader with thirty seconds
	// actually sees. Asserted against the datastores block rather than the
	// whole report, because the database dimension above it says four things
	// of exactly the same shape about the primary Postgres.
	block := datastoresBlock(t, fidelity.Build(branchedTwin(t)).Explain())
	for _, want := range []string{
		"gv_20260907101500_9f3c2b71",
		"6 columns read back over 1000 rows sampled",
		"4 tables",
		"1000000 rows",
	} {
		require.Containsf(t, block, want, "the datastores block does not say %q:\n%s", want, block)
	}
}

// The number this lane owes, and the proof that the old one was wrong.
//
// The same manifest, the same environment, and the only difference is whether
// af up branched the store the manifest declares golden. 90 percent was not a
// twin missing ten percent of production; it was the report unable to see a
// store that was there.
func TestATwinThatHoldsItsSecondStoreScoresTheStoreItHolds(t *testing.T) {
	t.Parallel()
	before := measureScore(t, fidelity.Build(analyticsTwin(t)))
	after := measureScore(t, fidelity.Build(branchedTwin(t)))

	require.Equal(t, scoreOf{reproduced: 8, counted: 9, percent: 89}, before)
	require.Equal(t, scoreOf{reproduced: 9, counted: 9, percent: 100}, after)
	require.Greater(t, after.percent, before.percent,
		"the report reads the branch and still scores the environment as if it had none")
}

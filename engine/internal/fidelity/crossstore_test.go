package fidelity_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The line where two stores are present.
//
// It is the clause the exit condition promised and nothing checked. Six of the
// seven clauses in "a masked, verified, attested clone of your Postgres and
// your ClickHouse, with the same person masked identically in both, and a
// report that says what it did not reproduce" had a command behind them. This
// one did not, so the honest reading of it was that a customer had our word.

// The name Explain prints, which has to survive its twelve character column.
const crossStoreLine = fidelity.CrossStoreComponent

// A twin with one store never gets the line. The question does not arise, and
// a sentence about it on every single store manifest is noise that makes the
// real one easier to miss.
func TestCrossStore_OneStoreDoesNotAskTheQuestion(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(full())
	d, ok := inv.Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	for _, c := range d.Components {
		require.NotEqual(t, crossStoreLine, c.Name,
			"there is only one store, so there is no pair to be identical across")
	}
	require.LessOrEqual(t, len(crossStoreLine), 12,
		"Explain trims a component name to twelve characters, so a longer name reaches "+
			"the reader as an ellipsis and says nothing")
}

// Nothing having compared them is UNMEASURED, which keeps it out of the score
// in both directions and carries the sentence saying how to check.
func TestCrossStore_NothingComparedThemIsUnmeasuredAndSaysSo(t *testing.T) {
	t.Parallel()
	obs := withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))
	inv := fidelity.Build(obs)

	c := componentState(t, inv, schema.FidelityDatastores, crossStoreLine)
	require.Equal(t, fidelity.Unmeasured, c.State,
		"an unverified guarantee has not been shown to be wrong, and scoring it absent "+
			"would be the report inventing a finding")
	require.Contains(t, c.Detail, "af mask crossstore")
}

// The reason the observation carries is printed as written, so a reader is
// told which of the several ways of not looking actually happened.
func TestCrossStore_TheReasonItCouldNotLookIsCarriedThrough(t *testing.T) {
	t.Parallel()
	obs := withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))
	obs.CrossStoreReason = "fewer than two datastores name a source_url_env"
	c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, crossStoreLine)

	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Equal(t, "fewer than two datastores name a source_url_env", c.Detail)
}

// Verified identical is REPRODUCED and earns its place in the numerator,
// because something read both catalogs and compared them.
func TestCrossStore_VerifiedIdenticalIsReproducedAndCountsForTheScore(t *testing.T) {
	t.Parallel()
	obs := withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))
	obs.CrossStore = &fidelity.CrossStore{
		Stores: []string{"primary", "events"}, Checked: 4, Identical: 4,
	}
	inv := fidelity.Build(obs)

	c := componentState(t, inv, schema.FidelityDatastores, crossStoreLine)
	require.Equal(t, fidelity.Reproduced, c.State)
	require.Contains(t, c.Detail, "4 of 4 join keys")
	require.Contains(t, c.Detail, "primary and events")
	require.Contains(t, c.Detail, "no rows")

	unverified := withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))
	before, okBefore := fidelity.Build(unverified).Score().Percent()
	after, okAfter := inv.Score().Percent()
	require.True(t, okBefore)
	require.True(t, okAfter)
	require.Greater(t, after, before,
		"a guarantee that was checked and held has to move the number, or checking it "+
			"changed nothing anybody can see")
}

// A pair that disagreed is ABSENT with the pair named. This is the line the
// whole thing is for: one identity masked into two people is a twin that is
// confidently wrong, and every report built on it is plausible.
func TestCrossStore_ADisagreementIsAbsentAndNamesThePair(t *testing.T) {
	t.Parallel()
	obs := withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))
	obs.CrossStore = &fidelity.CrossStore{
		Stores: []string{"primary", "events"}, Checked: 4, Identical: 3,
		Detail: "public.person.email and af.events.email: masked with different transforms",
	}
	c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, crossStoreLine)

	require.Equal(t, fidelity.Absent, c.State)
	require.Contains(t, c.Detail, "3 of 4")
	require.Contains(t, c.Detail, "one identity becomes two people")
	require.Contains(t, c.Detail, "af.events.email")
}

// Zero of zero is not a pass here either. Two stores that share no identifier
// have proved nothing, and the check itself refuses to call that a pass.
func TestCrossStore_ZeroOfZeroIsNotReproduced(t *testing.T) {
	t.Parallel()
	obs := withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))
	obs.CrossStore = &fidelity.CrossStore{
		Stores: []string{"primary", "events"}, Checked: 0, Identical: 0,
	}
	c := componentState(t, fidelity.Build(obs), schema.FidelityDatastores, crossStoreLine)

	require.Equal(t, fidelity.Unmeasured, c.State,
		"zero of zero must not read as everything having agreed")
	require.Contains(t, c.Detail, "nothing is proved")
}

package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The two decisions between a manifest and the check, both of which have an
// obvious wrong answer that looks right.

func analyticsManifest() *schema.Manifest {
	// In the order normalization leaves them: a declared store keeps the index
	// it has in the file, and the entry database: turns into is APPENDED, so
	// primary is last here on purpose.
	return &schema.Manifest{
		Datastores: []schema.Datastore{
			{Name: "events", Engine: "clickhouse", Stance: schema.StanceGolden,
				SourceURLEnv: "CLICKHOUSE_URL"},
			{Name: "cache", Engine: "redis", Stance: schema.StanceEmpty,
				Because: "a cache is rebuilt from the primary"},
			{Name: schema.PrimaryDatastore, Engine: "postgres", Stance: schema.StanceGolden,
				SourceURLEnv: "PRODUCTION_DATABASE_URL"},
		},
	}
}

func fixedLookup(name string) (secrets.Value, error) {
	return secrets.New("postgres://x/" + name), nil
}

func TestCrossStoreStores_ThePrimaryIsReportedFirst(t *testing.T) {
	t.Parallel()
	_, stores, err := crossStoreStores(analyticsManifest(), fixedLookup)
	require.NoError(t, err)
	require.Len(t, stores, 2)
	require.Equal(t, "primary", stores[0].Name,
		"normalization appends the primary, so a report built in file order would say "+
			"across events and primary and put the store every reader thinks of first last")
	require.Equal(t, "events", stores[1].Name)
}

// A store that names no source_url_env is named rather than dropped. Dropping
// it would let a check over two of five stores report a hundred percent, which
// is a true number about a smaller question than the one that was asked.
func TestCrossStoreStores_AStoreWithNoSourceIsNamedRatherThanSkipped(t *testing.T) {
	t.Parallel()
	res, stores, err := crossStoreStores(analyticsManifest(), fixedLookup)
	require.NoError(t, err)

	require.Equal(t, []string{"cache"}, res.WithoutSource)
	require.Equal(t, []string{"primary", "events", "cache"}, res.Declared,
		"every declared store is in the answer, whether or not it could be read")
	for _, s := range stores {
		require.NotEqual(t, "cache", s.Name)
	}
}

// The variable's NAME travels with the store, because a message about a store
// that could not be read has to say which setting to look at, and the value is
// the one thing it must never say.
func TestCrossStoreStores_TheVariableNameTravelsAndTheValueDoesNot(t *testing.T) {
	t.Parallel()
	_, stores, err := crossStoreStores(analyticsManifest(), fixedLookup)
	require.NoError(t, err)
	require.Equal(t, "PRODUCTION_DATABASE_URL", stores[0].Var)
	require.Equal(t, "CLICKHOUSE_URL", stores[1].Var)
	require.Equal(t, "clickhouse", stores[1].Engine)
}

// A manifest built in a test rather than parsed has not been normalized, so
// the primary's engine is filled in here as well. Without it the primary would
// be opened by an engine nothing recognises and refused by name, which is a
// confident refusal about the one store that is always Postgres.
func TestCrossStoreStores_TheUnnormalizedPrimaryIsStillPostgres(t *testing.T) {
	t.Parallel()
	m := &schema.Manifest{Datastores: []schema.Datastore{
		{Name: schema.PrimaryDatastore, SourceURLEnv: "PRODUCTION_DATABASE_URL"},
	}}
	_, stores, err := crossStoreStores(m, fixedLookup)
	require.NoError(t, err)
	require.Len(t, stores, 1)
	require.Equal(t, "postgres", stores[0].Engine)
}

// observeCrossStore, which is what puts the line in the fidelity report.
//
// It had no test at all. Every other path into the cross store check has one,
// and this one was reachable only by running the whole inventory against two
// live stores, so the two branches that decide NOT to dial anything were
// covered by nothing. They are the branches that matter most here: a report
// must not start opening connections to whatever a manifest happens to name,
// and the reason it did not look has to reach the reader, because an
// unmeasured line with no reason is the report saying nothing twice.
//
// Neither case touches the session, which is why nil is safe to pass and why
// that is worth saying out loud rather than leaving as a surprise.

func TestObserveCrossStore_NoManifestIsAReasonRatherThanASilentBlank(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{}
	var obs fidelity.Observation
	o.observeCrossStore(context.Background(), nil, &obs)

	require.Nil(t, obs.CrossStore)
	require.Contains(t, obs.CrossStoreReason, "no manifest",
		"an unmeasured line with no reason tells a reader less than no line at all")
}

// Fewer than two stores naming a source is the ordinary case, and it must not
// dial anything. Reading a schema means opening a connection, and a report
// that started connecting to whatever a manifest named would be doing it on
// behalf of somebody who asked for a report.
func TestObserveCrossStore_FewerThanTwoSourcesIsNotAnAttempt(t *testing.T) {
	t.Parallel()
	m := analyticsManifest()
	for i := range m.Datastores {
		if m.Datastores[i].Name != schema.PrimaryDatastore {
			m.Datastores[i].SourceURLEnv = ""
		}
	}
	o := &Orchestrator{opts: Options{Manifest: m}}
	var obs fidelity.Observation
	// A nil session, which is the assertion as much as the reasons below: this
	// branch returns before anything needs one, so a change that made it open
	// the state database or a store would panic here rather than pass.
	o.observeCrossStore(context.Background(), nil, &obs)

	require.Nil(t, obs.CrossStore)
	require.Contains(t, obs.CrossStoreReason, "source_url_env")
	require.Contains(t, obs.CrossStoreReason, "af mask crossstore",
		"the reason has to carry the command, or the reader is told it is unmeasured "+
			"and not how to measure it")
}

// The control for the two above: two stores that DO name a source get past the
// guard, so the guard is refusing on the count rather than on everything.
func TestObserveCrossStore_TwoSourcesGetPastTheGuard(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{opts: Options{Manifest: analyticsManifest()}}
	var obs fidelity.Observation
	require.Panics(t, func() { o.observeCrossStore(context.Background(), nil, &obs) },
		"two stores name a source, so this must reach the work and use the session; "+
			"a guard that refused here as well would make the reason above unfalsifiable")
}

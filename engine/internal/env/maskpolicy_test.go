package env

// What a masking hook is told, and when.
//
// extension.EnvironmentRequest carried a MaskedColumns field that nothing ever
// filled. The enterprise policy read it, so required_masked_columns was
// evaluated against an empty list on every environment: an organization that
// configured the rule got a licence, a compliance control that printed the
// rule, and no refusal on any repository ever.
//
// It could not be filled where it was. checkPolicy runs before anything is
// created, deliberately, and what it has is a manifest. A manifest enumerates
// services and does not enumerate columns, so the only list that could be
// built there is the repository's masking rules expanded against the tables
// the manifest happens to name, which is not the set of tables that exist.
//
// So the question moved to the one place the answer is a fact: during a golden
// refresh, after masking.ReadCatalog has read the schema and after the rules
// have been assigned to it, and before the executor rewrites the first row.
// These tests run against a real Postgres for the same reason the event tests
// next door do: the columns come from a real catalogue, and a test that
// asserted the hook was called would pass on a plan with no tables in it.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// spyMasking records what it was asked and answers with what it was told to.
type spyMasking struct {
	err  error
	seen *extension.MaskingRequest
}

func (s *spyMasking) Name() string { return "spy" }

func (s *spyMasking) CheckMasking(_ context.Context, req extension.MaskingRequest) error {
	copied := req
	s.seen = &copied
	return s.err
}

// registerMasking plugs a hook into an orchestrator's own registry.
//
// Its own rather than extension.Default, because the default is the shipped
// community registry and a test that registered into it would change what
// every other test in this package runs against.
func registerMasking(o *Orchestrator, hook extension.MaskingHook) {
	registry := extension.NewRegistry()
	registry.AddMasking(hook)
	o.opts.Extensions = registry
}

// maskPolicySession is the smallest session maskDatabase touches: somewhere
// for its events to go and nothing else.
func maskPolicySession(t *testing.T, o *Orchestrator) *session {
	t.Helper()
	bus := events.NewBus(o.opts.Clock)
	t.Cleanup(func() { _ = bus.Close() })
	return &session{bus: bus}
}

// The test that would have caught it. Before this change no masking hook was
// called at all, so seen stays nil and every assertion below fails.
func TestMaskDatabase_AsksTheHookWithTheColumnsTheCatalogueActuallyHas(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)
	spy := &spyMasking{}
	registerMasking(o, spy)

	_, _, err := o.maskDatabase(
		t.Context(), maskPolicySession(t, o), url,
		maskEventsKey(t), maskEventsRules(t), "h1")
	require.NoError(t, err)

	require.NotNil(t, spy.seen, "the masking hook was never asked")

	// The columns the plan will rewrite, schema qualified, read off the plan
	// rather than off the rules.
	require.Contains(t, spy.seen.MaskedColumns, "public.customers.email")
	require.Contains(t, spy.seen.MaskedColumns, "public.customers.name")
	require.Contains(t, spy.seen.MaskedColumns, "public.orders.email")

	// A structural column is not masked, and a hook that could not tell that
	// from an absent column would have to guess.
	require.NotContains(t, spy.seen.MaskedColumns, "public.customers.id")
	require.Contains(t, spy.seen.CatalogColumns, "public.customers.id")
	require.Contains(t, spy.seen.CatalogColumns, "public.orders.customer_id")

	require.Equal(t, "h1", spy.seen.RulesHash)
	require.Equal(t, o.envID, spy.seen.EnvID)
}

// The false refusal this design exists to avoid.
//
// This orchestrator's manifest names no tables at all, which is the ordinary
// case: a manifest enumerates services. A check that expanded a required
// pattern against the manifest would see nothing and refuse a repository whose
// database holds two perfectly masked email columns. The catalogue comes from
// the database, so both tables are here.
func TestMaskDatabase_ATableNoManifestNamesIsStillInTheRequest(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)
	require.Nil(t, o.opts.Manifest, "the fixture stopped being the no-manifest case")
	spy := &spyMasking{}
	registerMasking(o, spy)

	_, _, err := o.maskDatabase(
		t.Context(), maskPolicySession(t, o), url,
		maskEventsKey(t), maskEventsRules(t), "h1")
	require.NoError(t, err, "a plan was refused for naming a table the manifest does not")

	require.NotNil(t, spy.seen)
	require.Contains(t, spy.seen.MaskedColumns, "public.customers.email")
	require.Contains(t, spy.seen.MaskedColumns, "public.orders.email")
}

// A refusal has to arrive before the rows are gone.
//
// Masking is destructive and irreversible. A hook asked after the executor ran
// would be reviewing a database it could no longer stop anybody from
// branching, and the refusal would cost the data it was protecting.
func TestMaskDatabase_ARefusalStopsTheRunWithTheRowsIntact(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)
	refusal := errors.New("organization policy requires customers.email to be masked")
	registerMasking(o, &spyMasking{err: refusal})

	_, _, err := o.maskDatabase(
		t.Context(), maskPolicySession(t, o), url,
		maskEventsKey(t), maskEventsRules(t), "h1")
	require.ErrorIs(t, err, refusal, "the refusal did not reach the caller")

	conn, connErr := pgx.Connect(t.Context(), url.Reveal())
	require.NoError(t, connErr)
	defer func() { _ = conn.Close(context.Background()) }()

	var email string
	require.NoError(t, conn.QueryRow(t.Context(),
		"SELECT email FROM customers ORDER BY id LIMIT 1").Scan(&email))
	require.Equal(t, "ada@example.com", email,
		"the executor ran anyway, so the refusal arrived after the data was gone")
}

// The community build registers nothing, and with nothing registered the call
// has to be indistinguishable from its absence.
func TestMaskDatabase_WithNothingRegisteredNothingIsAsked(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)
	o.opts.Extensions = extension.NewRegistry()

	rows, tables, err := o.maskDatabase(
		t.Context(), maskPolicySession(t, o), url,
		maskEventsKey(t), maskEventsRules(t), "h1")
	require.NoError(t, err)
	require.Positive(t, rows, "an empty registry changed what masking did")
	require.Positive(t, tables)
}

package fidelity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
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
	return obs
}

func TestADeclaredStanceIsCarriedIntoTheReport(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(analyticsStack(t))

	events := componentState(t, inv, schema.FidelityDatastores, "events")
	require.Equal(t, fidelity.Unmeasured, events.State)
	require.Contains(t, events.Detail, "a clickhouse declared golden")

	bus := componentState(t, inv, schema.FidelityDatastores, "bus")
	require.Contains(t, bus.Detail, "a kafka declared topics_only")
	require.Contains(t, bus.Detail, "nothing here created a topic")
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
	// Unmeasured, including for empty, and this is the honest answer rather
	// than the cautious one. Nothing in this build starts a second store, so a
	// store reported reproduced because somebody declared it empty would be
	// the report believing a manifest instead of an environment.
	c := componentState(t, fidelity.Build(analyticsStack(t)), schema.FidelityDatastores, "cache")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "nothing here started it")
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
	require.Len(t, d.Components, 3)
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
	for _, want := range []string{"declared golden", "declared empty", "declared topics_only"} {
		require.Containsf(t, out, want, "the rendered report does not say %q", want)
	}
	require.NotContains(t, strings.SplitN(out, "\n\n", 2)[0], "declared",
		"the datastores dimension has moved above services in the report")
}

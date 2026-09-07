package fidelity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// withStores returns the working observation plus a second datastore, declared
// the only way a manifest can declare one today: a service running a prebuilt
// image.
func withStores(services ...schema.Service) fidelity.Observation {
	obs := full()
	obs.Manifest.Services = append(obs.Manifest.Services, services...)
	return obs
}

// running marks a declared service as up, so that the services dimension
// reports it reproduced rather than absent.
func running(obs fidelity.Observation, name string) fidelity.Observation {
	obs.Running = append(obs.Running, provider.RunningService{Name: name, Kind: "worker", Ready: true})
	return obs
}

func imageService(name, image string) schema.Service {
	return schema.Service{
		Name: name, Kind: schema.ServiceWorker,
		Build: &schema.Build{Strategy: schema.BuildImage, Image: image},
	}
}

// The finding this dimension exists for. An analytics product's twin holds a
// masked Postgres and an empty ClickHouse, and every dimension the inventory
// had scored the parts that worked and said nothing about the store holding
// every event.
func TestAnEmptyClickHouseIsNamedRatherThanOmitted(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(withStores(imageService("events", "clickhouse/clickhouse-server:24.3")))

	c := componentState(t, inv, schema.FidelityDatastores, "events")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "clickhouse")
	require.Contains(t, c.Detail, "clickhouse/clickhouse-server:24.3")
	require.Contains(t, c.Detail, "came up empty")
}

func TestAnUnmeasuredStoreIsExcludedFromTheScoreAndNamedInTheReport(t *testing.T) {
	t.Parallel()
	// The whole point of unmeasured: it is in neither half of the number. A
	// store counted as reproduced would inflate the score with a container
	// nothing read, and one counted as absent would deflate it with a claim
	// nothing checked.
	// Both sides declare and run one extra service, so the services dimension
	// contributes the same to each and the only difference between them is
	// that one of the images is a datastore. Comparing against an environment
	// with one service fewer would have measured the service, not the store.
	app := running(withStores(imageService("events", "ghcr.io/acme/api:1.4.0")), "events")
	store := running(withStores(imageService("events", "clickhouse/clickhouse-server:24.3")), "events")

	base := fidelity.Build(app).Score()
	with := fidelity.Build(store).Score()
	require.Equal(t, base.Reproduced, with.Reproduced)
	require.Equal(t, base.Counted, with.Counted)

	var named bool
	for _, e := range with.Excluded {
		if e.Dimension == schema.FidelityDatastores && e.Component == "events" {
			named = true
		}
	}
	require.True(t, named, "the store was excluded from the score and not named, which is the defect")

	text := fidelity.Build(store).Explain()
	require.Contains(t, text, "Not measured, and so not counted:")
	require.Contains(t, text, "datastores")
	require.Contains(t, text, "events")
}

func TestEveryStoreIsNamedNotJustTheFirst(t *testing.T) {
	t.Parallel()
	inv := fidelity.Build(withStores(
		imageService("events", "clickhouse/clickhouse-server:24.3"),
		imageService("cache", "redis:7-alpine"),
		imageService("bus", "confluentinc/cp-kafka:7.5.0"),
	))
	d, ok := inv.Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	require.Len(t, d.Components, 3)
	// Sorted, so two runs of the same environment print the same bytes.
	require.Equal(t, "bus", d.Components[0].Name)
	require.Equal(t, "cache", d.Components[1].Name)
	require.Equal(t, "events", d.Components[2].Name)
	require.Contains(t, d.Components[0].Detail, "kafka")
	require.Contains(t, d.Components[1].Detail, "redis")
}

func TestAStoreIsRecognisedByNameWhenItsImageIsNobodyElses(t *testing.T) {
	t.Parallel()
	// The second signal. A project that builds its own image of a store from
	// a private registry still calls the service clickhouse, because every
	// other service in the manifest has to name it to reach it.
	inv := fidelity.Build(withStores(imageService("clickhouse", "registry.internal.example/acme/olap:4")))
	c := componentState(t, inv, schema.FidelityDatastores, "clickhouse")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "clickhouse")
}

func TestAnApplicationServiceIsNotADatastore(t *testing.T) {
	t.Parallel()
	// The control. A check that calls everything a datastore is a check that
	// says nothing, and the two application services in the working
	// observation must not appear here.
	inv := fidelity.Build(withStores(imageService("api", "ghcr.io/acme/api:1.4.0")))
	d, ok := inv.Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	require.Empty(t, d.Components)
	require.NotEmpty(t, d.NotApplicable)
}

func TestWithNoSecondStoreTheDimensionSaysHowItLooked(t *testing.T) {
	t.Parallel()
	// A dimension that reports nothing without saying how it looked is the
	// shape this whole file exists to remove. The sentence names both signals,
	// so a reader whose store was missed can see why.
	d, ok := fidelity.Build(full()).Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	require.Empty(t, d.Components)
	require.Contains(t, d.NotApplicable, "no service declares a datastore beside the primary database")
	require.Contains(t, d.NotApplicable, "image")
	require.Contains(t, d.NotApplicable, "name")
}

func TestRequiringDatastoresFailsAsUnmeasurableRatherThanAsAPass(t *testing.T) {
	t.Parallel()
	// A manifest that requires this dimension must not be told it passed.
	// Unmeasurable is neither met nor broken and exits with its own code,
	// which is the distinction the whole package rests on.
	got := fidelity.Build(withStores(imageService("events", "clickhouse/clickhouse-server:24.3"))).
		Check([]schema.FidelityDimension{schema.FidelityDatastores})
	require.Len(t, got, 1)
	require.False(t, got[0].Met)
	require.False(t, got[0].Measurable)
	require.Contains(t, got[0].Because, "events")
}

func TestTheDimensionIsPresentInEveryReport(t *testing.T) {
	t.Parallel()
	// Always all of them, in AllFidelityDimensions order, so that a dimension
	// which measured nothing is visibly there rather than missing from the
	// document.
	require.Contains(t, schema.AllFidelityDimensions(), schema.FidelityDatastores)
	text := fidelity.Build(full()).Explain()
	require.True(t, strings.Contains(text, "datastores"),
		"the datastores dimension is missing from a report of an environment that has none")
}

package env

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The jobs an environment runs to bring a store to its declared stance.
//
// The runtime is handed a service name and a command and knows nothing about
// stances, which is what keeps a second runtime from having to reimplement a
// broker's command line. This file is the other side of that: the manifest
// turning into those two strings, and the refusals that happen before a single
// image runs rather than after an environment has come up green.

func stanceFixture(t *testing.T, m *schema.Manifest) (*Orchestrator, *[]string) {
	t.Helper()
	var progress []string
	o, err := New(Options{
		Root: t.TempDir(), Manifest: m, Branch: "main",
		Clock: clock.New(), Redactor: redact.New(),
		Getenv:   func(string) string { return "" },
		Progress: func(line string) { progress = append(progress, line) },
	})
	require.NoError(t, err)
	return o, &progress
}

func brokerManifest(topics []schema.DatastoreTopic) *schema.Manifest {
	return &schema.Manifest{
		Name: "app",
		Services: []schema.Service{
			{Name: "web", Kind: schema.ServiceWeb, Port: 3000},
			{Name: "bus", Kind: schema.ServiceWorker},
		},
		Datastores: []schema.Datastore{{
			Name: "bus", Engine: "kafka", Stance: schema.StanceTopicsOnly, Topics: topics,
		}},
	}
}

func TestStanceJobs_ComposesTheBrokersOwnTopicCommand(t *testing.T) {
	o, _ := stanceFixture(t, brokerManifest([]schema.DatastoreTopic{
		{Name: "events", Partitions: 12, ConsumerGroups: []string{"ingest"}},
	}))
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	require.Equal(t, "bus", jobs[0].Store)
	require.Equal(t, string(schema.StanceTopicsOnly), jobs[0].Stance)
	// The BROKER's own image, not the application's. The commands that create
	// a topic ship in it, and a hand written client would be a
	// reimplementation of them whose first wrong partition assignment would be
	// trusted.
	require.Equal(t, "bus", jobs[0].Service)
	require.Contains(t, jobs[0].Command, "--topic events --partitions 12")
	require.Contains(t, jobs[0].Command, "--group ingest")
	// Addressed by the broker's own name on the environment's network, which
	// is where it is reachable from: an environment publishes no port for a
	// store other services reach by name.
	require.Contains(t, jobs[0].Command, "B=bus:9092")
}

func TestStanceJobs_AddressesTheBrokerOnItsDeclaredPort(t *testing.T) {
	// A manifest that moved the broker off its usual port did so for a reason,
	// and the topic creation has to reach the same place every consumer does.
	m := brokerManifest([]schema.DatastoreTopic{{Name: "events"}})
	m.Services[1].Port = 19092
	o, _ := stanceFixture(t, m)

	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Contains(t, jobs[0].Command, "B=bus:19092")
}

func TestStanceJobs_RefusesABrokerThisBuildCannotShape(t *testing.T) {
	// Refused before a single image runs. An unknown broker started with
	// nothing in it is the empty stance under a different word, and the
	// manifest said something else.
	m := brokerManifest([]schema.DatastoreTopic{{Name: "events"}})
	m.Datastores[0].Engine = "rabbitmq"
	o, _ := stanceFixture(t, m)

	_, err := o.stanceJobs()
	require.Error(t, err)
	require.Contains(t, err.Error(), `the datastore "bus" declares stance topics_only`)
	require.Contains(t, err.Error(), "this build creates topics for kafka")
}

func TestStanceJobs_CarriesADerivedStoresOwnCommandAndService(t *testing.T) {
	o, _ := stanceFixture(t, &schema.Manifest{
		Name: "app",
		Services: []schema.Service{
			{Name: "web", Kind: schema.ServiceWeb, Port: 3000},
			{Name: "search", Kind: schema.ServiceWorker},
		},
		Datastores: []schema.Datastore{{
			Name: "search", Engine: "elasticsearch", Stance: schema.StanceDerived,
			From:    "primary",
			Rebuild: &schema.DatastoreRebuild{Service: "web", Command: "bin/reindex --all"},
		}},
	})
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	require.Equal(t, "search", jobs[0].Store)
	require.Equal(t, string(schema.StanceDerived), jobs[0].Stance)
	// The APPLICATION's image, because the code that knows how to index this
	// product's rows is the product's code, and the command as written.
	require.Equal(t, "web", jobs[0].Service)
	require.Equal(t, "bin/reindex --all", jobs[0].Command)
}

func TestStanceJobs_AnEmptyStoreIsSaidOutLoudAndRunsNothing(t *testing.T) {
	// Starting the store is what the service declaration already does, and a
	// cache that came up empty is in the state its stance asks for. What was
	// missing was never a container: it was somebody being told that this
	// store is empty because a person decided it should be.
	o, progress := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "cache", Kind: schema.ServiceWorker}},
		Datastores: []schema.Datastore{{
			Name: "cache", Engine: "redis", Stance: schema.StanceEmpty,
			Because: "a cache is rebuilt from the primary and a copy would be noise",
		}},
	})
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Empty(t, jobs)

	require.Contains(t, strings.Join(*progress, "\n"),
		"the datastore cache starts empty on purpose, because a cache is rebuilt from the "+
			"primary and a copy would be noise")
}

func TestStanceJobs_AnEmptyStoreWithNoReasonSaysThereIsNone(t *testing.T) {
	// Validation requires a because, so this is the manifest a newer build
	// wrote or an older one loaded. The absence is said rather than left as a
	// shorter sentence: an empty store somebody decided on and one nobody
	// explained look identical from inside a running environment.
	o, progress := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "cache", Kind: schema.ServiceWorker}},
		Datastores: []schema.Datastore{{
			Name: "cache", Engine: "redis", Stance: schema.StanceEmpty,
		}},
	})
	_, err := o.stanceJobs()
	require.NoError(t, err)
	require.Contains(t, strings.Join(*progress, "\n"), "and the manifest gives no reason")
}

func TestStanceJobs_LeavesTheGoldenStanceToTheProvider(t *testing.T) {
	// A golden is branched, not built. Running a job for one would be the
	// engine doing twice what the datastore provider already does.
	o, progress := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "web", Kind: schema.ServiceWeb, Port: 3000}},
		Datastores: []schema.Datastore{
			{Name: "events", Engine: "clickhouse", Stance: schema.StanceGolden},
		},
	})
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Empty(t, jobs)
	require.Empty(t, *progress)
}

func TestStanceJobs_SkipsThePrimaryDatabase(t *testing.T) {
	// database: normalizes into the entry called primary, and o.database
	// brings it up. A job for it would be a second thing acting on the one
	// store this product has always reproduced.
	o, _ := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "web", Kind: schema.ServiceWeb, Port: 3000}},
		Datastores: []schema.Datastore{
			{Name: schema.PrimaryDatastore, Engine: "postgres", Stance: schema.StanceGolden},
		},
	})
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestStanceJobs_RefusesADerivedStoreWithNoRebuild(t *testing.T) {
	// Validation refuses this first, so reaching here is a manifest that
	// arrived by another route. Skipping it silently would be an index nobody
	// built in an environment whose manifest says it holds one.
	o, _ := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "search", Kind: schema.ServiceWorker}},
		Datastores: []schema.Datastore{{
			Name: "search", Engine: "elasticsearch", Stance: schema.StanceDerived, From: "primary",
		}},
	})
	_, err := o.stanceJobs()
	require.Error(t, err)
	require.Contains(t, err.Error(), "is derived and declares no rebuild")
	require.Contains(t, err.Error(), "from primary")
}

func TestStanceJobs_RefusesAStanceThisBuildDoesNotKnow(t *testing.T) {
	// A manifest read by a build older than the one that wrote it. Doing
	// nothing quietly is how a store declared something specific comes up as
	// an empty container.
	o, _ := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "cache", Kind: schema.ServiceWorker}},
		Datastores: []schema.Datastore{
			{Name: "cache", Engine: "redis", Stance: schema.DatastoreStance("mirrored")},
		},
	})
	_, err := o.stanceJobs()
	require.Error(t, err)
	require.Contains(t, err.Error(), `declares the stance "mirrored"`)
	require.Contains(t, err.Error(), "golden, empty, derived and topics_only")
}

func TestStanceJobs_KeepsTheManifestsOrder(t *testing.T) {
	// A rebuild may read a store another job creates, and the manifest is
	// where the author says which comes first. Reordering them here would be
	// this file deciding something the author already decided.
	m := &schema.Manifest{
		Name: "app",
		Services: []schema.Service{
			{Name: "web", Kind: schema.ServiceWeb, Port: 3000},
			{Name: "bus", Kind: schema.ServiceWorker},
			{Name: "search", Kind: schema.ServiceWorker},
		},
		Datastores: []schema.Datastore{
			{Name: "bus", Engine: "kafka", Stance: schema.StanceTopicsOnly,
				Topics: []schema.DatastoreTopic{{Name: "events"}}},
			{Name: "search", Engine: "elasticsearch", Stance: schema.StanceDerived, From: "primary",
				Rebuild: &schema.DatastoreRebuild{Service: "web", Command: "bin/reindex"}},
		},
	}
	o, _ := stanceFixture(t, m)
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Equal(t, "bus", jobs[0].Store)
	require.Equal(t, "search", jobs[1].Store)
}

// The evidence the report reads back, and the one way it can name the wrong
// store.

func TestNamesTheStanceJob_RecognisesBothRuntimesNaming(t *testing.T) {
	// The local runtime journals af-svc-ENV-store-stance and the Kubernetes
	// one journals NAMESPACE/store-stance. A match that knew only one of them
	// would report the other runtime's environments unmeasured forever, with
	// nothing failing anywhere.
	require.True(t, namesTheStanceJob("af-svc-shop-main-abc123-bus-stance", "bus"))
	require.True(t, namesTheStanceJob("af-shop-main-abc123/bus-stance", "bus"))
	require.True(t, namesTheStanceJob("bus-stance", "bus"))
}

func TestNamesTheStanceJob_DoesNotCreditOneStoreWithAnothersJob(t *testing.T) {
	// The character in front of the suffix is the whole of this. Without it a
	// store called e matches the job of a store called cache, and one store's
	// report carries another's evidence.
	require.False(t, namesTheStanceJob("af-svc-shop-cache-stance", "e"))
	require.False(t, namesTheStanceJob("af-svc-shop-bus-stance", "cache"))
	require.False(t, namesTheStanceJob("af-svc-shop-bus-migrate", "bus"))
	require.False(t, namesTheStanceJob("", "bus"))
}

func TestStanceJobs_LeavesAProviderBackedStoreAlone(t *testing.T) {
	// Validation accepts a store that names a provider and no service of its
	// own name, on the grounds that the provider may be a managed store the
	// environment can already reach. This build does nothing about it: a
	// datastore provider is what refreshes, masks, verifies and branches a
	// golden, and no other stance has any of those done to it.
	//
	// So there is no job, and the honest half is that the report says it could
	// not check the store rather than counting it either way. That is asserted
	// in the fidelity package; here the point is that nothing is invented for
	// it.
	o, _ := stanceFixture(t, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "web", Kind: schema.ServiceWeb, Port: 3000}},
		Datastores: []schema.Datastore{{
			Name: "cache", Engine: "redis", Provider: "elasticache",
			Stance: schema.StanceEmpty, Because: "a cache is rebuilt from the primary",
		}},
	})
	jobs, err := o.stanceJobs()
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestManifestRunsAServiceCalled_AnswersForBothShapes(t *testing.T) {
	// The predicate that decides which of two honest sentences the report
	// carries about a provider backed store. A store with a provider AND a
	// service of its own name is started by this environment and is observed
	// like any other.
	m := &schema.Manifest{Services: []schema.Service{{Name: "cache"}, {Name: "web"}}}
	require.True(t, manifestRunsAServiceCalled(m, "cache"))
	require.False(t, manifestRunsAServiceCalled(m, "bus"))
	require.False(t, manifestRunsAServiceCalled(nil, "cache"))
}

// observeStances, which is where the report stops believing the manifest.

// stanceObservation runs observeStances against a real state database, with
// the journal records a run would have left.
func stanceObservation(
	t *testing.T, m *schema.Manifest, running []string, journalled []string,
) []fidelity.Stance {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	c := clock.New()
	st, err := state.Open(ctx, root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	o, err := New(Options{
		Root: root, Manifest: m, Branch: "main", Clock: c, Redactor: redact.New(),
		Getenv:   func(string) string { return "" },
		Progress: func(string) {},
	})
	require.NoError(t, err)

	j := journal.New(st, c, nil)
	for _, name := range journalled {
		_, err := j.Intent(ctx, o.envID, "local", journal.KindContainer, name, nil)
		require.NoError(t, err)
	}

	obs := &fidelity.Observation{EnvID: o.envID, Manifest: m}
	for _, name := range running {
		obs.Running = append(obs.Running, provider.RunningService{Name: name, Ready: true})
	}
	o.observeStances(ctx, &session{db: st, journal: j}, obs)
	return obs.Stances
}

func stanceFor(t *testing.T, all []fidelity.Stance, store string) fidelity.Stance {
	t.Helper()
	for _, st := range all {
		if st.Store == store {
			return st
		}
	}
	t.Fatalf("no stance was observed for %q", store)
	return fidelity.Stance{}
}

// twoStores is a cache that starts empty and a broker whose shape a run
// creates, each started by a service of its own name.
func twoStores() *schema.Manifest {
	return &schema.Manifest{
		Name: "app",
		Services: []schema.Service{
			{Name: "web", Kind: schema.ServiceWeb, Port: 3000},
			{Name: "cache", Kind: schema.ServiceWorker},
			{Name: "bus", Kind: schema.ServiceWorker},
		},
		Datastores: []schema.Datastore{
			{Name: "cache", Engine: "redis", Stance: schema.StanceEmpty, Because: "rebuilt from the primary"},
			{Name: "bus", Engine: "kafka", Stance: schema.StanceTopicsOnly,
				Topics: []schema.DatastoreTopic{{Name: "events"}}},
		},
	}
}

func TestObserveStances_ReadsTheRunsOwnJournalRatherThanTheManifest(t *testing.T) {
	// The question a manifest cannot answer. An environment brought up by a
	// build with no stance jobs runs the same services from the same file with
	// a broker that has nothing in it, and the journal is the only thing in
	// this product that records what a particular run did.
	m := twoStores()
	with := stanceObservation(t, m, []string{"web", "cache", "bus"}, nil)
	require.False(t, stanceFor(t, with, "bus").Ran)
	require.Contains(t, stanceFor(t, with, "bus").RanReason, "recorded no topics_only job")
}

func TestObserveStances_CreditsAJobThisEnvironmentRecorded(t *testing.T) {
	m := twoStores()
	// The name the local runtime journals, which pkg/provider composes so that
	// the runtime writing it and this reading it cannot drift apart.
	all := stanceObservation(t, m, []string{"web", "cache", "bus"},
		[]string{"af-svc-ANY-bus-stance"})
	bus := stanceFor(t, all, "bus")
	require.True(t, bus.Running)
	require.True(t, bus.Ran, "the run journalled the job and the report did not see it")
}

func TestObserveStances_AsksNothingAboutAnEmptyStoresJob(t *testing.T) {
	// The one stance with no job. Asking the journal about it would produce an
	// absence that means nothing and would read as one that means something.
	all := stanceObservation(t, twoStores(), []string{"web", "cache", "bus"}, nil)
	cache := stanceFor(t, all, "cache")
	require.True(t, cache.Running)
	require.False(t, cache.Ran)
	require.Empty(t, cache.RanReason)
}

func TestObserveStances_ReportsAStoreThatIsNotRunning(t *testing.T) {
	all := stanceObservation(t, twoStores(), []string{"web"}, nil)
	require.False(t, stanceFor(t, all, "cache").Running)
	require.Empty(t, stanceFor(t, all, "cache").RunningReason,
		"the runtime answered, so there is no reason it could not be asked")
}

func TestObserveStances_SaysWhenTheRuntimeCouldNotBeAsked(t *testing.T) {
	// Not knowing whether a store is running is a different answer from
	// knowing it is not, and only one of them belongs in a denominator.
	m := twoStores()
	root := t.TempDir()
	c := clock.New()
	st, err := state.Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	o, err := New(Options{
		Root: root, Manifest: m, Branch: "main", Clock: c, Redactor: redact.New(),
		Getenv: func(string) string { return "" }, Progress: func(string) {},
	})
	require.NoError(t, err)

	obs := &fidelity.Observation{
		EnvID: o.envID, Manifest: m,
		ServicesReason: "the runtime could not be reached",
	}
	o.observeStances(t.Context(), &session{db: st, journal: journal.New(st, c, nil)}, obs)
	require.Equal(t, "the runtime could not be reached", stanceFor(t, obs.Stances, "cache").RunningReason)
	require.False(t, stanceFor(t, obs.Stances, "cache").Running)
}

func TestObserveStances_SaysThatAProviderBackedStoreWasNotChecked(t *testing.T) {
	// Validation accepts a store that names a provider and no service of its
	// own name. This build neither starts it nor asks that provider about it,
	// because a datastore provider is what branches a golden. Saying so beats
	// reporting it absent, which would be claiming to know that a managed
	// store somebody else supplies is not there.
	m := &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "web", Kind: schema.ServiceWeb, Port: 3000}},
		Datastores: []schema.Datastore{{
			Name: "cache", Engine: "redis", Provider: "elasticache",
			Stance: schema.StanceEmpty, Because: "rebuilt from the primary",
		}},
	}
	cache := stanceFor(t, stanceObservation(t, m, []string{"web"}, nil), "cache")
	require.False(t, cache.Running)
	require.Contains(t, cache.RunningReason, "it names the provider elasticache")
	require.Contains(t, cache.RunningReason, "nothing here can say what it holds")
}

func TestObserveStances_ObservesAProviderBackedStoreThisEnvironmentStarts(t *testing.T) {
	// The control on the sentence above. A store with a provider AND a service
	// of its own name IS started here, so it is observed like any other rather
	// than excused by the presence of a provider key.
	m := twoStores()
	m.Datastores[0].Provider = "elasticache"
	cache := stanceFor(t, stanceObservation(t, m, []string{"web", "cache"}, nil), "cache")
	require.True(t, cache.Running)
	require.Empty(t, cache.RunningReason)
}

func TestObserveStances_SaysNothingAboutAGoldenStore(t *testing.T) {
	// The golden stance is measured from the branch, by observeDatastores, and
	// a second entry here would be one store answered for twice.
	m := twoStores()
	m.Datastores = append(m.Datastores, schema.Datastore{
		Name: "events", Engine: "clickhouse", Stance: schema.StanceGolden,
	})
	all := stanceObservation(t, m, []string{"web", "cache", "bus", "events"}, nil)
	for _, st := range all {
		require.NotEqual(t, "events", st.Store)
	}
	require.Len(t, all, 2)
}

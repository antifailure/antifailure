package detect_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/detect"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A compose file running ClickHouse beside Postgres left no trace at all in
// the manifest af init wrote. The image classifier recognised it, the finding
// was made, and mergeDatabase read the ones it understood and dropped the rest
// on the floor. So the twin of an analytics product came up holding a masked
// Postgres and an empty everything else, and nothing in the file the developer
// read said so.
//
// These tests are about what detection now proposes. The stance is the
// decision and it is never defaulted, so the thing being asserted throughout
// is that a proposal exists, carries a reason, and is either declared or
// reported by name.

// analyticsStack is a repository whose compose file runs five stores.
func analyticsStack() map[string]string {
	return map[string]string{
		"package.json": `{"name":"insight","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  db:
    image: postgres:16
  events:
    image: clickhouse/clickhouse-server:24.3
  cache:
    image: redis:7
  broker:
    image: confluentinc/cp-kafka:7.6.0
  search:
    image: elasticsearch:8.13.0
`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	}
}

func proposalFor(t *testing.T, got []detect.ProposedDatastore, name string) detect.ProposedDatastore {
	t.Helper()
	for _, p := range got {
		if p.Name == name {
			return p
		}
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	t.Fatalf("no datastore proposal named %q; found %v", name, names)
	return detect.ProposedDatastore{}
}

func TestDatastores_EveryStoreBesideThePrimaryIsProposed(t *testing.T) {
	t.Parallel()
	// The finding this lane exists for. Four of these five classifications
	// were made and then discarded, and Kafka was not classified at all.
	res := run(t, "insight", analyticsStack())
	got := res.Datastores

	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	require.ElementsMatch(t, []string{"broker", "cache", "events", "search"}, names,
		"a store the compose file runs must not vanish between the finding and the manifest")
}

func TestDatastores_ThePrimaryIsNotProposedTwice(t *testing.T) {
	t.Parallel()
	// database: normalizes into the entry called primary, so a second entry
	// for Postgres would be two records of one store.
	res := run(t, "insight", analyticsStack())
	for _, p := range res.Datastores {
		require.NotEqual(t, "postgres", p.Engine,
			"the primary arrives through database: and must not also arrive as a datastore")
	}
}

func TestDatastores_EveryProposalCarriesAStanceAndAReason(t *testing.T) {
	t.Parallel()
	// A stance is DECLARED and never defaulted, which is the rule
	// schema.DatastoreStance exists to enforce. A proposal with no reason is
	// a decision nobody can check afterwards.
	res := run(t, "insight", analyticsStack())
	reasons := map[string]bool{}
	for _, p := range res.Datastores {
		require.NotEmpty(t, p.Stance, "%s was proposed with no stance", p.Name)
		require.Contains(t, schema.AllDatastoreStances(), p.Stance,
			"%s proposes a stance the manifest does not accept", p.Name)
		require.NotEmpty(t, p.Because, "%s has a stance nobody can check", p.Name)
		reasons[p.Because] = true
	}
	require.Len(t, reasons, 4,
		"a sentence that explains four stores explains none of them")
}

func TestDatastores_TheCacheIsEmptyAndTheEventsAreGolden(t *testing.T) {
	t.Parallel()
	// The two stances that matter most and point opposite ways. An analytics
	// product with an empty ClickHouse tests every chart against nothing; a
	// cloned Redis is noise, because it is rebuilt from the primary.
	got := run(t, "insight", analyticsStack()).Datastores
	require.Equal(t, schema.StanceGolden, proposalFor(t, got, "events").Stance)
	require.Equal(t, schema.StanceEmpty, proposalFor(t, got, "cache").Stance)
	require.Equal(t, schema.StanceTopicsOnly, proposalFor(t, got, "broker").Stance)

	search := proposalFor(t, got, "search")
	require.Equal(t, schema.StanceDerived, search.Stance)
	require.Equal(t, schema.PrimaryDatastore, search.From,
		"a derived store with nothing to derive from is refused by the validator")
}

func TestDatastores_TheComposeServiceNameIsWhatTheStoreIsCalled(t *testing.T) {
	t.Parallel()
	// The developer calls it events. Renaming it to clickhouse would make the
	// manifest describe a store they do not recognise, in their own
	// repository, on the first file this product ever writes for them.
	got := run(t, "insight", analyticsStack()).Datastores
	require.Equal(t, "clickhouse", proposalFor(t, got, "events").Engine)
	require.Equal(t, "redis", proposalFor(t, got, "cache").Engine)
	require.Equal(t, "kafka", proposalFor(t, got, "broker").Engine)
	require.Equal(t, "elasticsearch", proposalFor(t, got, "search").Engine)
}

func TestDatastores_OnlyTheOnesThisBuildCanProvideReachTheManifest(t *testing.T) {
	t.Parallel()
	// The direction that is easy to get wrong. Declaring an engine no provider
	// serves produces a manifest af up refuses, and af init promises the
	// opposite: the draft is normalized and validated before it is written so
	// that the file it leaves behind is one every later command accepts. The
	// validator cannot catch this, because Datastore.Engine is an open set on
	// purpose and the refusal happens at run time in the provider lookup.
	res := run(t, "insight", analyticsStack())
	var engines []string
	for _, d := range res.Draft.Datastores {
		engines = append(engines, d.Engine)
	}
	require.Equal(t, []string{"clickhouse"}, engines,
		"a datastore this build has no provider for must not be written into the draft")
}

func TestDatastores_AStoreLeftOutIsStillNamed(t *testing.T) {
	t.Parallel()
	// The one thing worse than a store nobody declared is a store nobody
	// mentioned. The sentence has to say the stance it would have taken,
	// because the stance is the decision and the reader is the one who has to
	// make it.
	got := run(t, "insight", analyticsStack()).Datastores
	cache := proposalFor(t, got, "cache")
	require.False(t, cache.Provisionable)

	note := cache.UnprovisionableNote()
	require.Contains(t, note, "docker-compose.yml", "the note must say where the store was found")
	require.Contains(t, note, "cache", "the note must call the store what the developer calls it")
	require.Contains(t, note, string(schema.StanceEmpty),
		"the note must say the stance it would have taken")
	require.Contains(t, note, "rebuilt from the primary",
		"the note must carry the reason, not a generic one")
	require.Contains(t, note, "clickhouse",
		"the note must say which engines this build does provide")
	require.NotContains(t, strings.ToLower(note), "unsupported",
		"the store works in their compose file; what is missing is this engine's side")
}

func TestDatastores_TheDraftWithDatastoresStillValidates(t *testing.T) {
	t.Parallel()
	// A draft the validator refuses is never written, so a proposal that
	// produces one is a detection bug rather than a manifest one.
	files := analyticsStack()
	requireDraftValidates(t, run(t, "insight", files).Draft, files)
}

func TestDatastores_AMongoWithNoPostgresIsTheDatabaseAndNotASecondStore(t *testing.T) {
	t.Parallel()
	// mergeDatabase already asks about this case by name. Proposing a stance
	// for it as well would be one fact stated twice in two vocabularies.
	res := run(t, "notes", map[string]string{
		"package.json": `{"name":"notes","scripts":{"start":"node server.js"},
			"dependencies":{"express":"4.0.0","mongoose":"8.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  mongo:
    image: mongo:7
`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	})
	require.Empty(t, res.Datastores,
		"the application's own database must not also be proposed as a store beside itself")
}

func TestDatastores_AMongoBesideAPostgresIsASecondStore(t *testing.T) {
	t.Parallel()
	// The other ordering of the same question, and the one the guard must not
	// swallow.
	res := run(t, "hybrid", map[string]string{
		"package.json": `{"name":"hybrid","scripts":{"start":"node server.js"},
			"dependencies":{"express":"4.0.0","mongoose":"8.0.0","pg":"8.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  db:
    image: postgres:16
  docs:
    image: mongo:7
`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	})
	docs := proposalFor(t, res.Datastores, "docs")
	require.Equal(t, "mongodb", docs.Engine)
	require.Equal(t, schema.StanceGolden, docs.Stance,
		"a store holding application records is a twin of nothing when it is empty")
}

func TestDatastores_AServiceCalledPrimaryDoesNotCollideWithTheDatabase(t *testing.T) {
	t.Parallel()
	// primary is reserved for the entry database: normalizes into, and the
	// validator refuses a second store under that name. A compose service
	// somebody happened to call primary is not a reason to produce a manifest
	// af init would then refuse to write.
	files := map[string]string{
		"package.json": `{"name":"insight","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  db:
    image: postgres:16
  primary:
    image: clickhouse/clickhouse-server:24.3
`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	}
	res := run(t, "insight", files)
	for _, d := range res.Draft.Datastores {
		require.NotEqual(t, schema.PrimaryDatastore, d.Name,
			"two stores called primary is a manifest the validator refuses")
	}
	requireDraftValidates(t, res.Draft, files)
}

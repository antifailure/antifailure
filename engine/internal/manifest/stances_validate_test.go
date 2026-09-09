package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The declarations a stance needs before the environment can keep it.
//
// L4.1 made a stance a required word. This file is the half that makes it a
// position: a stance the engine now acts on has to say enough for the engine
// to act, and a key that cannot be kept is refused when somebody writes it
// rather than discovered when an environment comes up green with an empty
// broker in it.

// withRunner declares the store's own service, which every stance but golden
// needs, so that each refusal below is about the thing it names rather than
// about the store nothing runs.
const withRunner = `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: events
    kind: worker
datastores:
  - name: events
    engine: kafka
`

func TestParse_RefusesADerivedStoreWithNoRebuild(t *testing.T) {
	t.Parallel()
	// The rebuild is what makes derived a stance rather than a label. Without
	// a command the engine has a store declared as rebuilt from the branch and
	// no way to rebuild it, which is an empty search index in an environment
	// whose manifest says it holds one.
	body := withRunner + "    stance: derived\n    from: primary\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" is derived and nothing says how to rebuild it.`)
	require.Contains(t, msg, "It runs once the branch is ready, inside the environment")
}

func TestParse_RefusesARebuildOnAStanceThatIsNotDerived(t *testing.T) {
	t.Parallel()
	// A key that reads as configuration and behaves as decoration is the whole
	// defect this list is about. A rebuild on an empty store is somebody
	// expecting a command to run that never will.
	body := withRunner + "    stance: empty\n    because: a cache is rebuilt from the primary\n" +
		"    rebuild:\n      service: web\n      command: bin/reindex\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" declares a rebuild and its stance is empty.`)
}

func TestParse_RefusesARebuildNamingAServiceTheManifestDoesNotDeclare(t *testing.T) {
	t.Parallel()
	// Refused here rather than found by the runtime, where it arrives as a
	// container create failing on an image nobody declared.
	body := withRunner + "    stance: derived\n    from: primary\n" +
		"    rebuild:\n      service: indexer\n      command: bin/reindex\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `No service is named "indexer".`)
}

func TestParse_RefusesARebuildWithNoCommandAndNoService(t *testing.T) {
	t.Parallel()
	// Two separate problems, because a rebuild that named a service and ran
	// nothing and one that ran something nowhere are different mistakes and
	// the second is not a consequence of the first.
	body := withRunner + "    stance: derived\n    from: primary\n    rebuild: {}\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The rebuild of the datastore "events" has no command.`)
	require.Contains(t, msg, `The rebuild of the datastore "events" names no service.`)
}

func TestParse_RefusesATopicsOnlyStoreWithNoTopics(t *testing.T) {
	t.Parallel()
	// A broker the environment starts and creates nothing in is
	// indistinguishable from the empty stance, and the manifest said something
	// else. The stance's whole content is the list.
	body := withRunner + "    stance: topics_only\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" is topics_only and lists no topics.`)
	require.Contains(t, msg, "A broker with nothing in it is the empty stance")
}

func TestParse_RefusesTopicsOnAStanceThatIsNotTopicsOnly(t *testing.T) {
	t.Parallel()
	body := withRunner + "    stance: empty\n    because: a cache is rebuilt from the primary\n" +
		"    topics:\n      - name: orders\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" declares topics and its stance is empty.`)
}

func TestParse_RefusesATopicWithNoName(t *testing.T) {
	t.Parallel()
	// A consumer subscribing to a name that is not there reads nothing and
	// says nothing, so an unnamed topic is a line of manifest that cannot
	// produce one.
	body := withRunner + "    stance: topics_only\n    topics:\n      - partitions: 3\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "The topic has no name.")
}

func TestParse_RefusesTwoTopicsWithOneName(t *testing.T) {
	t.Parallel()
	// One topic is created once, and the second entry's partition count and
	// groups would be silently the ones that did not win.
	body := withRunner + "    stance: topics_only\n    topics:\n" +
		"      - name: orders\n        partitions: 3\n      - name: orders\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Two topics are both named "orders".`)
	require.Contains(t, msg, "datastores[0].topics[0]")
}

func TestParse_RefusesANegativePartitionCount(t *testing.T) {
	t.Parallel()
	// Zero is legal and means one, which is what a broker does with an
	// unspecified count. A negative number is not a count of anything.
	body := withRunner + "    stance: topics_only\n    topics:\n" +
		"      - name: orders\n        partitions: -3\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The topic "orders" asks for -3 partitions.`)
}

func TestParse_RefusesAConsumerGroupWithNoName(t *testing.T) {
	t.Parallel()
	body := withRunner + "    stance: topics_only\n    topics:\n" +
		"      - name: orders\n        consumer_groups: [\"\"]\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The topic "orders" names a consumer group with no name.`)
}

func TestParse_RefusesAStoreNothingInTheManifestRuns(t *testing.T) {
	t.Parallel()
	// The engine provides the container for a golden store and for nothing
	// else. A cache, a broker and a search index are ordinary services running
	// stock images, which is how every compose file in the world declares
	// them, and the datastore entry says what happens to their contents.
	//
	// Refused here rather than discovered later, because the later discovery
	// is the failure the stance key exists to remove: the run comes up green,
	// the report says the manifest declared a cache, and there is no cache.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: cache
    engine: redis
    stance: empty
    because: a cache is rebuilt from the primary and a copy would be noise
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg,
		`The datastore "cache" declares the stance empty and nothing in this manifest runs it.`)
	require.Contains(t, msg, "Declare a service called cache running the store's image")
}

func TestParse_AcceptsAStoreRunByAProviderRatherThanAService(t *testing.T) {
	t.Parallel()
	// The control on the refusal above, and it is not a formality. A store
	// naming a provider may be a managed one with an address the environment
	// can already reach, and requiring a local service for it would refuse the
	// correct manifest.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: cache
    engine: redis
    provider: elasticache
    stance: empty
    because: a cache is rebuilt from the primary and a copy would be noise
`
	m := mustParse(t, body)
	require.Equal(t, "elasticache", m.Datastores[0].Provider)
}

func TestParse_DoesNotRequireAServiceForAGoldenStore(t *testing.T) {
	t.Parallel()
	// The other control. A golden store's container is the engine's own: it
	// refreshes a golden, masks it, verifies it and branches it, and the
	// branch is what the services reach. Requiring a service for one would
	// refuse every manifest the datastores list was written for.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: events
    engine: clickhouse
    stance: golden
`
	m := mustParse(t, body)
	require.Equal(t, "events", m.Datastores[0].Name)
}

package broker_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/datastore/broker"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The command that turns a declared shape into a broker that has one.
//
// Every assertion here is about a sentence in a shell script, which is an
// unusual thing to test and is the only thing there is to test: the script
// runs inside an environment, in an image this process does not have, against
// a broker this process cannot reach. What can be checked here is that the
// composed command asks for what the manifest declared and refuses everything
// it cannot ask for, and the conformance suite is where a real broker answers.

func topics() []schema.DatastoreTopic {
	return []schema.DatastoreTopic{
		{Name: "events", Partitions: 12, ConsumerGroups: []string{"ingest", "enrich"}},
		{Name: "alerts"},
	}
}

func TestCommandCreatesEveryDeclaredTopic(t *testing.T) {
	t.Parallel()
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)

	require.Contains(t, cmd, "--topic events --partitions 12")
	require.Contains(t, cmd, "--topic alerts --partitions 1")
	require.Contains(t, cmd, "B=bus:9092")
}

func TestCommandAsksForOnePartitionWhenNoneIsDeclared(t *testing.T) {
	t.Parallel()
	// Zero means one, which is what a broker does with an unspecified count.
	// Passing the zero through would be asking for a topic with no partitions,
	// which every broker refuses, so the whole job would fail on the one topic
	// whose count nobody thought about.
	cmd, err := broker.Command("kafka", "bus", 9092,
		[]schema.DatastoreTopic{{Name: "alerts"}})
	require.NoError(t, err)
	require.Contains(t, cmd, "--partitions 1")
	require.NotContains(t, cmd, "--partitions 0")
}

func TestCommandCreatesEachConsumerGroupAtTheEarliestOffset(t *testing.T) {
	t.Parallel()
	// Committing an offset is what CREATES the group, and to-earliest is what
	// makes it read the twin's own messages. A group with no committed offset
	// does not exist until a consumer joins one, and that consumer then reads
	// from the END: everything the twin's own producers wrote before it
	// started is skipped, silently, and the only symptom is a count of zero.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	require.Contains(t, cmd, "--group ingest --topic events --reset-offsets --to-earliest")
	require.Contains(t, cmd, "--group enrich --topic events --reset-offsets --to-earliest")
	require.Equal(t, 2, strings.Count(cmd, "--reset-offsets"),
		"one group is created per declared group and no more")
}

func TestCommandIsSafeToRunTwice(t *testing.T) {
	t.Parallel()
	// A second af up against a live environment has to reach the same shape
	// rather than failing on the topic the first one created.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(cmd, "--if-not-exists"))
}

func TestCommandAsksForOneReplicaBecauseATwinIsOneBroker(t *testing.T) {
	t.Parallel()
	// Asking for more on a single node broker fails the create outright, and a
	// replication factor is a property of production's cluster rather than of
	// the shape a consumer needs.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	require.Contains(t, cmd, "--replication-factor 1")
	require.NotContains(t, cmd, "--replication-factor 3")
}

func TestCommandWaitsForTheBrokerBeforeItUsesIt(t *testing.T) {
	t.Parallel()
	// A broker declares no health path in anybody's compose file, so the
	// runtime's readiness wait has nothing to wait on and this job starts
	// against a container that has been started rather than one that has
	// opened its listener. Without the wait the very first create loses a race
	// that has nothing to do with the manifest.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	require.Contains(t, cmd, `until "$T" --bootstrap-server "$B" --list`)
	require.Contains(t, cmd, "did not answer a metadata request within two minutes")
	// The wait comes before the first create, or it is not a wait.
	require.Less(t, strings.Index(cmd, "until "), strings.Index(cmd, "--create"))
}

func TestCommandStopsOnTheFirstFailure(t *testing.T) {
	t.Parallel()
	// Without set -e a create that failed lets the next line run and the last
	// line print a success nobody earned, and the job exits zero.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(cmd, "set -e\n"), "the command does not stop on failure:\n%s", cmd)
}

func TestCommandFinishesWithALineAReaderCanFind(t *testing.T) {
	t.Parallel()
	// So that a reader of the logs can tell a command that finished from one
	// whose shell exited early.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	require.Contains(t, cmd, broker.DoneLine)
	require.True(t, strings.HasSuffix(strings.TrimSpace(cmd), broker.DoneLine+`"`))
}

func TestCommandLooksForTheToolsInEveryLayoutTheImagesUse(t *testing.T) {
	t.Parallel()
	// Four layouts, because the images people actually run disagree. Picking
	// one and hoping fails on three of them with "not found", which reads as a
	// broken environment rather than as a layout this did not look in.
	cmd, err := broker.Command("kafka", "bus", 9092, topics())
	require.NoError(t, err)
	for _, path := range []string{
		`"$1"`, `"$1.sh"`, `"/opt/kafka/bin/$1.sh"`, `"/opt/bitnami/kafka/bin/$1.sh"`,
	} {
		require.Containsf(t, cmd, path, "no layout %s", path)
	}
	require.Contains(t, cmd, "T=$(find_tool kafka-topics)")
	require.Contains(t, cmd, "G=$(find_tool kafka-consumer-groups)")
}

func TestCommandRefusesAnEngineThisBuildCannotShape(t *testing.T) {
	t.Parallel()
	// A refusal rather than a best effort. Doing nothing quietly would leave
	// the environment with a store declared topics_only and no topic in it,
	// which is the empty stance under a different word, and the manifest said
	// something else.
	_, err := broker.Command("rabbitmq", "bus", 5672, topics())
	require.Error(t, err)
	require.Contains(t, err.Error(), "this build creates topics for kafka")
	require.Contains(t, err.Error(), "the store runs rabbitmq")
	require.Contains(t, err.Error(), "Declare stance: empty with a because")
}

func TestCommandAcceptsTheEngineNameHoweverItIsWritten(t *testing.T) {
	t.Parallel()
	// The manifest's engine is free text, and a manifest that says Kafka
	// rather than kafka is not asking for a different broker.
	for _, engine := range []string{"kafka", "KAFKA", " Kafka "} {
		_, err := broker.Command(engine, "bus", 9092, topics())
		require.NoErrorf(t, err, "engine %q", engine)
	}
}

func TestCommandRefusesATopicNameAShellWouldReinterpret(t *testing.T) {
	t.Parallel()
	// Every name reaches the command line unquoted, so a name outside the set
	// a broker accepts is refused here rather than escaped: the alternative is
	// quoting rules that have to be right in every shell the image might ship,
	// and a manifest is a file somebody can edit.
	for _, name := range []string{"events; rm -rf /", "$(id)", "a b", "", strings.Repeat("x", 250)} {
		_, err := broker.Command("kafka", "bus", 9092,
			[]schema.DatastoreTopic{{Name: name}})
		require.Errorf(t, err, "the topic name %q was accepted", name)
		require.Contains(t, err.Error(), "nothing was created")
	}
}

func TestCommandRefusesAConsumerGroupNameAShellWouldReinterpret(t *testing.T) {
	t.Parallel()
	// The same rule one level down, and it is a separate check because a group
	// name reaches a different command and the first version of this validated
	// only the topic.
	_, err := broker.Command("kafka", "bus", 9092, []schema.DatastoreTopic{
		{Name: "events", ConsumerGroups: []string{"ingest; id"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a consumer group name a broker accepts")
}

func TestCommandRefusesAHostTheEnvironmentDoesNotResolve(t *testing.T) {
	t.Parallel()
	_, err := broker.Command("kafka", "bus;id", 9092, topics())
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a name this environment resolves")
}

func TestCommandRefusesAPortThatIsNotOne(t *testing.T) {
	t.Parallel()
	// Zero is what DefaultPort answers for an engine with no recipe, so a port
	// that reached this as zero is a bug upstream rather than a manifest, and
	// composing bus:0 would fail inside the environment with a message about
	// a connection refused.
	for _, port := range []int{0, -1, 65536} {
		_, err := broker.Command("kafka", "bus", port, topics())
		require.Errorf(t, err, "the port %d was accepted", port)
		require.Contains(t, err.Error(), "is not a port")
	}
}

func TestCommandRefusesAShapeWithNothingInIt(t *testing.T) {
	t.Parallel()
	// A topics_only store with no topic is the empty stance, and the manifest
	// said something else. Validation refuses it first; this is the second
	// door, because this function is also reachable from a manifest a newer
	// build wrote.
	_, err := broker.Command("kafka", "bus", 9092, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no topics were declared")
}

func TestDefaultPortAnswersForTheEnginesThisBuildShapes(t *testing.T) {
	t.Parallel()
	require.Equal(t, 9092, broker.DefaultPort("kafka"))
	require.Equal(t, 9092, broker.DefaultPort(" Kafka "))
	// Zero for an engine with no recipe, which never reaches a caller: Command
	// refuses that engine first, by name.
	require.Equal(t, 0, broker.DefaultPort("rabbitmq"))
	require.Equal(t, []string{"kafka"}, broker.Engines())
}

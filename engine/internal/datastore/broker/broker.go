// Package broker turns a topics_only datastore's declared shape into the
// command that creates it inside the environment.
//
// A twin's broker holds no messages, on purpose. Replaying production's
// traffic into one is the copy that a cache's clone is: noise wearing the
// word fidelity. What a consumer needs in order to run against a twin is the
// SHAPE, and the shape is three things that do not exist in an empty broker.
//
// The topic, because a consumer subscribing to a name that is not there reads
// nothing and reports nothing, and the run goes green having tested one poll
// loop against a name that will exist in production.
//
// The partition count, because ordering is per partition and a consumer group
// with more members than partitions leaves members idle. A twin whose topic
// has one partition where production has twelve cannot produce a reordering
// bug at all, so a test that passes against it says nothing about the one
// that fails in production.
//
// The consumer group, because a consumer joining a group nobody created reads
// from the END by default. The twin's own producers write before the consumer
// starts, the consumer joins, and every one of those messages is skipped: a
// pipeline that works in production silently processes nothing here, and the
// only symptom is a count of zero that nothing explains.
//
// The commands are the broker's own, run in the broker's own image. Nothing
// here speaks a wire protocol: a hand written Kafka client would be a
// reimplementation of the thing the image already ships, and the first version
// of it that got a partition assignment wrong would be trusted.
package broker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// DefaultPort is where a broker of this engine listens, for a service that
// declares no port.
//
// Zero for an engine this build has no recipe for, which never reaches a
// caller: Command refuses that engine first, by name.
func DefaultPort(engine string) int {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "kafka":
		return 9092
	default:
		return 0
	}
}

// Engines are the brokers this build can create topics in, for a message that
// names what is covered rather than saying that something is not.
func Engines() []string { return []string{"kafka"} }

// Command returns the shell command that creates every declared topic and
// consumer group, or refuses an engine this build has no recipe for.
//
// A REFUSAL rather than a best effort. An unknown broker would leave the
// environment with a store declared topics_only and no topic in it, which is
// the empty stance under a different word, and the manifest said something
// else. The same rule the golden stance already follows for an engine with no
// masking dialect: refuse, name the engines that are covered, and let somebody
// declare a stance this build can actually keep.
func Command(engine, host string, port int, topics []schema.DatastoreTopic) (string, error) {
	if strings.ToLower(strings.TrimSpace(engine)) != "kafka" {
		return "", fmt.Errorf(
			"this build creates topics for %s and the store runs %s, so its declared topics "+
				"could not be created. Declare stance: empty with a because, or run the store "+
				"on an engine this build can shape",
			strings.Join(Engines(), ", "), engine)
	}
	if len(topics) == 0 {
		return "", fmt.Errorf("no topics were declared, so there is nothing to create")
	}
	if !validHost.MatchString(host) {
		return "", fmt.Errorf("%q is not a name this environment resolves", host)
	}
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("%d is not a port", port)
	}

	var b strings.Builder
	b.WriteString(kafkaPrelude)
	b.WriteString("B=" + host + ":" + strconv.Itoa(port) + "\n")
	b.WriteString(kafkaWait)
	for _, t := range topics {
		if !validKafkaName.MatchString(t.Name) {
			return "", fmt.Errorf(
				"%q is not a topic name a broker accepts, so nothing was created", t.Name)
		}
		partitions := t.Partitions
		if partitions < 1 {
			partitions = 1
		}
		// --if-not-exists, because a second `af up` against a live
		// environment must reach the same shape rather than failing on the
		// topic the first one created. Replication factor one, because a twin
		// is one broker: asking for more on a single node broker fails the
		// create outright, and a replication factor is a property of
		// production's cluster rather than of the shape a consumer needs.
		fmt.Fprintf(&b,
			"\"$T\" --bootstrap-server \"$B\" --create --if-not-exists --topic %s "+
				"--partitions %d --replication-factor 1\n",
			t.Name, partitions)
		for _, g := range t.ConsumerGroups {
			if !validKafkaName.MatchString(g) {
				return "", fmt.Errorf(
					"%q is not a consumer group name a broker accepts, so nothing was created", g)
			}
			// Committing an offset is what CREATES the group, and to-earliest
			// is what makes it read the twin's own messages rather than only
			// what arrives after it starts. A group with no committed offset
			// does not exist until a consumer joins one and then reads from
			// the end, which is the skip this stance exists to prevent.
			fmt.Fprintf(&b,
				"\"$G\" --bootstrap-server \"$B\" --group %s --topic %s "+
					"--reset-offsets --to-earliest --execute >/dev/null\n",
				g, t.Name)
		}
	}
	b.WriteString("echo \"" + DoneLine + "\"\n")
	return b.String(), nil
}

// DoneLine is the last thing the command prints, so a reader of the logs can
// tell a command that finished from one whose shell exited early.
const DoneLine = "antifailure: the declared topics and consumer groups exist"

// kafkaPrelude finds the broker's own tools wherever the image put them.
//
// Four layouts, because the images people actually run disagree: the
// Confluent images put kafka-topics on the PATH with no extension, the Apache
// image ships /opt/kafka/bin/kafka-topics.sh, Bitnami puts the same script
// under /opt/bitnami/kafka/bin, and some put the .sh form on the PATH. Picking
// one and hoping would fail on three of the four with "not found", which reads
// as a broken environment rather than as a layout this did not look in.
//
// set -e, so that a create that fails stops the run rather than letting the
// next line print a success nobody earned.
const kafkaPrelude = `set -e
find_tool() {
  for c in "$1" "$1.sh" "/opt/kafka/bin/$1.sh" "/opt/bitnami/kafka/bin/$1.sh" "/usr/bin/$1"; do
    if command -v "$c" >/dev/null 2>&1; then printf '%s' "$c"; return 0; fi
  done
  echo "antifailure: this image has no $1, so the declared topics could not be created" >&2
  return 1
}
T=$(find_tool kafka-topics)
G=$(find_tool kafka-consumer-groups)
`

// kafkaWait blocks until the broker answers, and fails saying so when it never
// does.
//
// The runtime starts this job once every service container has been CREATED
// and started, which is not the same as a broker having finished electing
// itself a controller and opened its listener. A broker declares no health
// path in anybody's compose file, so there is nothing for the readiness wait
// to have waited on, and without this the very first create would race the
// listener and fail an environment for a reason that has nothing to do with
// the manifest.
//
// --list rather than a TCP probe, because the port accepting a connection is
// not the same as the broker being able to answer a metadata request, and the
// second is what every line after this needs.
//
// Two minutes, then a refusal that says what it waited for. A job that hung
// forever would take the whole af up with it and report nothing.
const kafkaWait = `i=0
until "$T" --bootstrap-server "$B" --list >/dev/null 2>&1; do
  i=$((i+1))
  if [ "$i" -ge 60 ]; then
    echo "antifailure: $B did not answer a metadata request within two minutes, so the declared topics could not be created" >&2
    exit 1
  fi
  sleep 2
done
`

// validKafkaName is what a broker accepts as a topic or a group.
//
// It is also what makes the composed command safe to run in a shell. Every
// name reaches the command line unquoted, so a name outside this set is
// refused here rather than escaped: the alternative is quoting rules that have
// to be right in every shell the image might ship, and a manifest is a file
// somebody can edit.
var validKafkaName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,249}$`)

// validHost is the name the store answers to on the environment's network.
var validHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

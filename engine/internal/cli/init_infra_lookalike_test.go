package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A Kafka broker and the console that reads it is how a compose file with
// Kafka in it usually looks, and the console is named after what it reads.
// provectuslabs/kafka-ui matched "kafka" by substring, so af init removed a web
// console from the services and reported a second Kafka store in its place.
//
// Confluent's own compose files call the broker "broker", which says nothing
// about Kafka, so the dependencies on it are the other half of this: once the
// console is a service again, its depends_on is read, and a dependency on a
// store the environment provides must not reach the manifest as a service it
// never declares.
const kafkaWithConsole = `services:
  web:
    build: .
    ports:
      - "3000:3000"
    depends_on:
      - broker
      - kafka-ui
  broker:
    image: bitnami/kafka:3.7
  kafka-ui:
    image: provectuslabs/kafka-ui:latest
    ports:
      - "8080:8080"
    depends_on:
      - broker
    environment:
      KAFKA_CLUSTERS_0_BOOTSTRAPSERVERS: broker:9092
`

func TestInitInfraLookalike_AKafkaConsoleIsAServiceAndNotASecondBroker(t *testing.T) {
	t.Parallel()
	dir := imageSiblingRepo(t, kafkaWithConsole)

	got := runCLI(t, dir, nil, "init", "--non-interactive", "--output", "json")
	require.Equal(t, 0, got.code, "af init refused:\n%s%s", got.stdout, got.stderr)

	var report struct {
		Datastores []struct {
			Name   string `json:"name"`
			Engine string `json:"engine"`
		} `json:"datastores"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))
	var stores []string
	for _, d := range report.Datastores {
		stores = append(stores, d.Name+"="+d.Engine)
	}
	require.Equal(t, []string{"broker=kafka"}, stores,
		"the broker is the one Kafka store; the console that reads it is not a store")

	require.Equal(t, []writtenService{
		{Name: "kafka-ui", Kind: schema.ServiceWeb, Port: 8080, Strategy: schema.BuildImage, Image: "provectuslabs/kafka-ui:latest"},
		theApplication,
	}, writtenServices(t, dir))

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	var m schema.Manifest
	require.NoError(t, yaml.Unmarshal(body, &m))
	deps := map[string][]string{}
	for _, s := range m.Services {
		deps[s.Name] = s.DependsOn
	}
	require.Equal(t, []string{"kafka-ui"}, deps["shopfront"],
		"a dependency on a service compose declares is kept, and one on the store the environment provides is not")
	require.Empty(t, deps["kafka-ui"],
		"the console depends only on the broker, which the environment provides rather than declares")

	// af init must never write a file af up would then refuse.
	explained := runCLI(t, dir, nil, "explain")
	require.Zero(t, explained.code, explained.stderr)
}

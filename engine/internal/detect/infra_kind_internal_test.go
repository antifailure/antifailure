package detect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// infraKind decides that a compose service is a database, a cache or a queue
// the environment provides, and not a service. Every answer it gives removes
// the service from the manifest and writes a store in its place, so a wrong
// yes is not a small labelling error: provectuslabs/kafka-ui, a web console,
// became a Kafka datastore with a topics_only stance, and postgrest/postgrest,
// an API server, became the application's Postgres.
//
// It matched by substring, and the images that sit beside a real store are
// named after the store they serve: its exporter, its admin console, its REST
// proxy. Every name below is a repository published under that name, checked
// against the registry rather than invented, and the lookalikes are the ones
// that stand beside each engine in ordinary compose files.
func TestInfraKind_TheRepositoryNameDecidesAndNotASubstringOfIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		image string
		want  string
	}{
		// postgres
		{"postgres:16", "postgres"},
		{"pgvector/pgvector:pg16", "postgres"},
		{"ankane/pgvector:latest", "postgres"},
		{"timescale/timescaledb:latest-pg16", "postgres"},
		{"timescale/timescaledb-ha:pg16", "postgres"},
		{"supabase/postgres:15.1.0.147", "postgres"},
		{"postgis/postgis:16-3.4", "postgres"},
		{"bitnami/postgresql:16", "postgres"},
		{"postgres-exporter", ""},
		{"prometheuscommunity/postgres-exporter:v0.15.0", ""},
		{"postgrest/postgrest:v12.2.3", ""},
		{"supabase/postgres-meta:v0.83.2", ""},
		{"pgadmin", ""},
		{"dpage/pgadmin4:8", ""},

		// redis
		{"redis:7", "redis"},
		{"redis/redis-stack:latest", "redis"},
		{"redis/redis-stack-server:latest", "redis"},
		{"bitnami/redis:7.2", "redis"},
		{"valkey/valkey:8", "redis"},
		{"bitnami/valkey:8.0", "redis"},
		{"redis-commander", ""},
		{"rediscommander/redis-commander:latest", ""},
		{"oliver006/redis_exporter:v1.62.0", ""},
		{"redis/redisinsight:latest", ""},

		// mysql
		{"mysql:8", "mysql"},
		{"mariadb:11", "mysql"},
		{"bitnami/mysql:8.4", "mysql"},
		{"bitnami/mariadb:11.4", "mysql"},
		{"mysql/mysql-server:8.0", "mysql"},
		{"prom/mysqld-exporter:v0.15.1", ""},
		{"phpmyadmin:5", ""},

		// mongodb
		{"mongo:7", "mongodb"},
		{"mongodb/mongodb-community-server:7.0-ubi8", "mongodb"},
		{"bitnami/mongodb:7.0", "mongodb"},
		{"mongo-express", ""},
		{"mongo-express:1.0", ""},
		{"percona/mongodb_exporter:0.40", ""},

		// rabbitmq
		{"rabbitmq:3-management", "rabbitmq"},
		{"bitnami/rabbitmq:3.13", "rabbitmq"},
		{"kbudde/rabbitmq-exporter:v1.0.0", ""},

		// elasticsearch
		{"elasticsearch:8.13.0", "elasticsearch"},
		{"docker.elastic.co/elasticsearch/elasticsearch:8.13.0", "elasticsearch"},
		{"bitnami/elasticsearch:8", "elasticsearch"},
		{"opensearchproject/opensearch:2", "elasticsearch"},
		{"opensearchproject/opensearch-dashboards:2", ""},

		// objectstore
		{"minio/minio:latest", "objectstore"},
		{"quay.io/minio/minio:latest", "objectstore"},
		{"bitnami/minio:2024", "objectstore"},
		{"bitnami/minio-client:2024", ""},

		// clickhouse
		{"clickhouse/clickhouse-server:24.3", "clickhouse"},
		{"yandex/clickhouse-server:22.1", "clickhouse"},
		{"bitnami/clickhouse:24", "clickhouse"},
		{"clickhouse/clickhouse-keeper:24.3", ""},

		// kafka
		{"bitnami/kafka:3.7", "kafka"},
		{"apache/kafka:3.8.0", "kafka"},
		{"apache/kafka-native:3.8.0", "kafka"},
		{"confluentinc/cp-kafka:7.6.0", "kafka"},
		{"confluentinc/cp-server:7.6.0", "kafka"},
		{"confluentinc/confluent-local:7.6.0", "kafka"},
		{"wurstmeister/kafka:2.13-2.8.1", "kafka"},
		{"redpandadata/redpanda:v24.2.4", "kafka"},
		{"docker.redpanda.com/redpandadata/redpanda:v24.2.4", "kafka"},
		{"kafka-ui", ""},
		{"provectuslabs/kafka-ui:latest", ""},
		{"obsidiandynamics/kafdrop:4.0.1", ""},
		{"confluentinc/cp-kafka-rest:7.6.0", ""},
		{"confluentinc/cp-kafka-connect:7.6.0", ""},
		{"confluentinc/cp-zookeeper:7.6.0", ""},
		{"danielqsj/kafka-exporter:v1.8.0", ""},
		{"redpandadata/console:v2.7.2", ""},

		// Registry, tag and digest are not part of the repository name.
		{"registry.example.com:5000/mirror/postgres:16", "postgres"},
		{"redis@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "redis"},
		{"REDIS:7", "redis"},

		// Nothing that is not a published store.
		{"", ""},
		{"nginx:alpine", ""},
		{"acme/shopfront:dev", ""},
	} {
		t.Run(c.image, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, infraKind(c.image), "infraKind(%q)", c.image)
		})
	}
}

package fidelity

import (
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The datastore dimension, which is the one that was missing.
//
// Every other dimension in this package was written against something the
// engine already measured. This one is written against something it does not,
// and that is the point of it. schema.Database is a single struct and it is
// Postgres: there is one golden, one masking pass, one verification scan and
// one branch. A ClickHouse, a Redis, a Kafka or an Elasticsearch declared as a
// service starts as an empty container, and until this file existed the
// inventory said nothing at all about it.
//
// The failure that shape produces is not a wrong number, it is a confident
// one. An analytics product's twin holds a masked Postgres and zero events,
// because the events live in ClickHouse and ClickHouse came up empty. Every
// query path that matters is untested, and the report scored the environment
// on its services, its branch, its hosts, its personas and its traffic and
// called it faithful. The instrument whose entire job is to say "this is not
// production" was the last thing that would have told anybody.
//
// So the dimension reports these as unmeasured, with the reason, rather than
// leaving them out. Unmeasured keeps them out of the score in both directions,
// which is correct: nothing here has shown that a second datastore reproduces
// production and nothing here has shown that it does not. What changes is that
// the report now carries the sentence, in the exclusions list every reader of
// the headline is pointed at.

// datastores reports every datastore in the environment other than the primary
// database.
//
// A datastore is recognised from the manifest rather than from the running
// container, because the manifest is what declares one. A service is a
// datastore when it runs a prebuilt image this file recognises, or when it
// carries the name of one, and the second signal is there because a project
// that builds its own ClickHouse image from a private registry still calls the
// service clickhouse.
func datastores(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityDatastores}

	found := make([]Component, 0, len(obs.Manifest.Services))
	for _, s := range obs.Manifest.Services {
		engine, image := datastoreEngine(s)
		if engine == "" {
			continue
		}
		found = append(found, Component{
			Name:   s.Name,
			State:  Unmeasured,
			Detail: datastoreReason(engine, image),
		})
	}
	if len(found) == 0 {
		// Named rather than silent, and the sentence says what was looked for
		// as well as what was not found. A dimension that reports nothing
		// without saying how it looked is the shape this whole file exists to
		// remove.
		d.NotApplicable = "no service declares a datastore beside the primary database, " +
			"which is recognised here by a service's image or its name"
		return d
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	d.Components = found
	return d
}

// datastoreReason says why the component could not be measured, in the words
// somebody has to act on.
func datastoreReason(engine, image string) string {
	what := "a " + engine
	if image != "" {
		what += " running " + image
	}
	return what + ", and this build reproduces the primary database only, so nothing here " +
		"read its contents and nothing here can say whether it holds production's data or came up empty"
}

// datastoreEngine names the datastore a service runs, and the image it runs it
// from, or two empty strings when the service is not one.
//
// This is deliberately not detect.infraKind, which answers a nearby question
// and must keep answering it. That one decides, while reading a compose file,
// which service is infrastructure the environment provides rather than code to
// build, and its answer picks the project's database provider. Teaching it
// about Kafka would make a broker the candidate for that, which is a different
// and worse mistake than the one this fixes. The two lists overlap because two
// correct answers to two questions about the same images overlap.
func datastoreEngine(s schema.Service) (engine, image string) {
	if s.Build != nil && s.Build.Image != "" {
		image = s.Build.Image
		if e := engineOfImage(image); e != "" {
			return e, image
		}
	}
	if e := engineOfName(s.Name); e != "" {
		return e, image
	}
	return "", ""
}

// engineOfImage recognises a datastore from the image a service runs.
//
// The registry prefix, the tag and the digest are stripped first, so that
// ghcr.io/acme/clickhouse-server:24.3 and clickhouse/clickhouse-server both
// answer clickhouse. An unrecognised image is not a datastore here, which is
// the honest limit of this: a store nobody named and whose image nothing
// recognises is still invisible, and the dimension's own sentence says the
// two signals it used.
func engineOfImage(image string) string {
	base := strings.ToLower(strings.TrimSpace(image))
	if base == "" {
		return ""
	}
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.SplitN(base, ":", 2)[0]
	base = strings.SplitN(base, "@", 2)[0]

	for _, e := range imageEngines {
		for _, token := range e.tokens {
			if strings.Contains(base, token) {
				return e.engine
			}
		}
	}
	return ""
}

// engineOfName recognises a datastore from what the service is called.
//
// The second signal, and it is not redundant. A project that builds its own
// image of a store from a private registry, or pins one behind a mirror this
// list has never seen, still calls the service redis or clickhouse, because
// every other service in the manifest has to name it to reach it.
func engineOfName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	for _, e := range imageEngines {
		for _, alias := range e.names {
			if n == alias {
				return e.engine
			}
		}
	}
	return ""
}

// imageEngines is the recognition table, ordered so that a more specific token
// wins over a less specific one that also matches.
//
// postgres is here as well as under database:, because a second Postgres
// declared as a service is a second datastore and nothing branches it. The
// primary is the one under database:, and it is measured by the database
// dimension rather than this one.
var imageEngines = []struct {
	engine string
	tokens []string
	names  []string
}{
	{"clickhouse", []string{"clickhouse"}, []string{"clickhouse"}},
	{"elasticsearch", []string{"elasticsearch", "opensearch"}, []string{"elasticsearch", "opensearch"}},
	{"kafka", []string{"kafka", "redpanda"}, []string{"kafka", "redpanda"}},
	{"pulsar", []string{"pulsar"}, []string{"pulsar"}},
	{"nats", []string{"nats"}, []string{"nats"}},
	{"rabbitmq", []string{"rabbitmq"}, []string{"rabbitmq"}},
	{"redis", []string{"redis", "valkey", "dragonfly"}, []string{"redis", "valkey", "cache"}},
	{"memcached", []string{"memcached"}, []string{"memcached"}},
	{"mongodb", []string{"mongo"}, []string{"mongo", "mongodb"}},
	{"mysql", []string{"mysql", "mariadb", "percona"}, []string{"mysql", "mariadb"}},
	{"cassandra", []string{"cassandra", "scylla"}, []string{"cassandra", "scylla"}},
	{"neo4j", []string{"neo4j"}, []string{"neo4j"}},
	{"influxdb", []string{"influxdb"}, []string{"influxdb"}},
	{"object store", []string{"minio", "seaweedfs"}, []string{"minio"}},
	{"vector store", []string{"qdrant", "weaviate", "milvus", "chroma"}, []string{"qdrant", "weaviate", "milvus", "chroma"}},
	{"postgres", []string{"postgres", "pgvector", "timescale", "supabase"}, []string{"postgres", "postgresql"}},
}

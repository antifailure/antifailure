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
// So the dimension reports these rather than leaving them out, and which of
// two states each gets turns on what the manifest declared for it.
//
// A store the manifest declares golden is ABSENT, and it is counted. The
// manifest asked for a masked, verified copy of production in it and this
// build has none, which is a fact about the environment and not a gap in what
// can be seen. That is the line that makes the score go down on exactly the
// stack this dimension was added for.
//
// A store recognised from a service image, or declared with any other stance,
// is UNMEASURED, which keeps it out of the score in both directions: nothing
// here has shown that it reproduces production and nothing here has shown that
// it does not. Either way the report carries the sentence, in the exclusions
// list every reader of the headline is pointed at.

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

	found := make([]Component, 0, len(obs.Manifest.Datastores)+len(obs.Manifest.Services))
	declared := map[string]bool{}
	for _, ds := range obs.Manifest.Datastores {
		// The primary is the entry database: normalizes into, and the database
		// dimension above measures it properly: which golden it came from,
		// whether that golden was verified, whether the attestation still
		// checks out. Reporting it here as well would count one store twice
		// and would put the one store this build DOES reproduce into the
		// dimension whose subject is the ones it does not.
		if ds.Name == schema.PrimaryDatastore {
			continue
		}
		declared[ds.Name] = true
		found = append(found, declaredComponent(ds))
	}

	for _, svc := range obs.Manifest.Services {
		if declared[svc.Name] {
			// Declared and running as a service is the ordinary case for a
			// store the environment starts itself. The declaration is the
			// better answer of the two, because it carries the stance.
			continue
		}
		engine, image := datastoreEngine(svc)
		if engine == "" {
			continue
		}
		found = append(found, Component{
			Name:   svc.Name,
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

// declaredComponent is one store the manifest declares, in the state the
// declaration and this build together put it in.
//
// TWO STATES, and which one a stance gets is the whole of this function.
//
// A stance of golden is ABSENT. The manifest asked for a masked, verified copy
// of production in this store, this build has no such thing, and that is a
// fact about the environment rather than a gap in what can be seen: there is
// one golden, one masking pass, one verification scan and one branch, and all
// four are the primary Postgres. Nothing has to read a ClickHouse to know that
// nothing built a golden for it. So it is counted, in the denominator, and the
// score goes down, which is the point: an analytics product's twin holding
// masked Postgres metadata and zero events must not score as a faithful twin,
// and until this it did, because unmeasured kept the one store the product is
// about out of the number in both directions.
//
// Every other stance stays UNMEASURED. Nothing in this build starts a second
// store, rebuilds one from the branch or creates a topic in one, so whether an
// empty store came up empty on purpose or came up at all is genuinely unknown
// here. A store reported reproduced because somebody declared it empty would
// be the report believing a manifest instead of an environment, which is the
// failure one level up from the one the dimension was added for. L4.4 is the
// lane that makes those three into first class outcomes; this one must not
// pre-empt it by scoring a declaration.
func declaredComponent(ds schema.Datastore) Component {
	c := Component{Name: ds.Name, Detail: declaredReason(ds)}
	if ds.Stance == schema.StanceGolden {
		c.State = Absent
		return c
	}
	c.State = Unmeasured
	return c
}

// declaredReason says what the manifest chose for a store and what this build
// has done about it, which are two different sentences and both belong here.
func declaredReason(ds schema.Datastore) string {
	var b strings.Builder
	b.WriteString("a ")
	b.WriteString(ds.Engine)
	b.WriteString(" declared ")
	b.WriteString(string(ds.Stance))
	if ds.Stance == schema.StanceDerived && ds.From != "" {
		b.WriteString(" from ")
		b.WriteString(ds.From)
	}
	if ds.Because != "" {
		b.WriteString(", because ")
		b.WriteString(ds.Because)
	}
	b.WriteString(", and ")
	b.WriteString(stanceGap(ds.Stance))
	return b.String()
}

// stanceGap is what this build has NOT done for a stance, in the words
// somebody has to act on.
func stanceGap(stance schema.DatastoreStance) string {
	switch stance {
	case schema.StanceEmpty:
		return "nothing here started it, so the declaration is recorded and unchecked"
	case schema.StanceDerived:
		return "nothing here ran that rebuild, so nothing knows whether it would succeed"
	case schema.StanceTopicsOnly:
		return "nothing here created a topic, so the broker is a declaration rather than a shape"
	case schema.StanceGolden:
		// The four facts named one by one rather than summarised. The database
		// dimension reports a branch as its golden, its attestation, its
		// tables and its rows; this store has none of those, and naming each
		// is what tells somebody which four things would have to appear before
		// this line changes.
		return "nothing here built one: no golden, no attestation, no tables and no rows. " +
			"There is one golden, one masking pass, one verification scan and one branch, " +
			"and all four are the primary Postgres"
	default:
		// A stance no build of this engine knows. Validation refuses one, so
		// reaching here means a manifest parsed by a newer engine than the one
		// reading it, and the honest sentence is that this build did nothing
		// rather than a guess at what the stance meant.
		return "this build does not know that stance, so nothing here acted on it"
	}
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

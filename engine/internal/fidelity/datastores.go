package fidelity

import (
	"fmt"
	"sort"
	"strconv"
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
// So the dimension reports these rather than leaving them out, and what each
// gets turns on what the manifest declared for it and on what the environment
// then did about it.
//
// A store the manifest declares golden AND the environment branched is
// reported the way the primary database is: one component for what the branch
// holds and one for where it came from, carrying the golden, the attestation,
// the tables and the rows. That half of this file was written when nothing
// could branch a second store, so the dimension read the declaration alone and
// called a full store absent, which is a lie in the generous direction and is
// still a lie. An instrument that understates the twin is not the instrument
// this repository argues for.
//
// What the branch holds is an UNKNOWN rather than a reproduction, and it says
// why. Nothing here records what production's second store holds, so nothing
// can say whether the branch reproduces it, and the primary database learned
// that lesson first: a branch of two hundred rows was reported as reproducing
// a production of four billion. The tables and the rows are still in the
// report, and the verdict over them is the one nobody has earned yet.
//
// A store declared golden that nothing branched is ABSENT, and it is counted.
// The manifest asked for a masked, verified copy of production in it and this
// environment has none, which is a fact about the environment and not a gap in
// what can be seen. That is the line that makes the score go down on exactly
// the stack this dimension was added for.
//
// A store declared with one of the other three stances is SUBSTITUTED when
// this environment did what the stance asks and the store is running, ABSENT
// when it is not running, and UNMEASURED when the run that brought this
// environment up recorded no such job. That was UNMEASURED in every case until
// `af up` learned to act on those three, and the sentence attached to each of
// them said that nothing here started the store, ran the rebuild or created a
// topic, which stopped being true the moment the jobs existed. Substituted
// puts them in the denominator: an empty cache does not reproduce production's
// cache, the score goes down for saying so, and the detail beside it carries
// the declared reason so a reader can see the position rather than a gap.
//
// A store recognised from a service IMAGE and declared nowhere is still
// UNMEASURED, which keeps it out of the score in both directions: nobody chose
// a stance for it, so nothing here has shown that it reproduces production and
// nothing here has shown that it does not. Either way the report carries the
// sentence, in the exclusions list every reader of the headline is pointed at.

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

	// The stores this environment holds, by name. A store that is not in here
	// was not branched, and the declaration is then the only thing there is to
	// report about it.
	branched := make(map[string]Store, len(obs.Stores))
	for _, st := range obs.Stores {
		branched[st.Name] = st
	}
	// What the environment did about each store that is not a golden. A store
	// with no entry here was never asked about, which is the zero Stance and
	// is reported as such rather than as a store that answered no.
	stances := make(map[string]Stance, len(obs.Stances))
	for _, st := range obs.Stances {
		stances[st.Store] = st
	}

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
		// GOLDEN AND BRANCHED, both. The stance alone is a declaration and an
		// observation alone is a store nobody asked for a copy of production
		// in, and neither on its own is grounds for reporting what a branch
		// holds. A store declared empty, derived or topics_only stays
		// unmeasured whatever arrives here, which is the rule the lane that
		// wrote this dimension established and the one this change is
		// deliberately narrower than.
		if st, ok := branched[ds.Name]; ok && ds.Stance == schema.StanceGolden {
			found = append(found, storeDataComponent(ds, st),
				storeProvenanceComponent(ds.Name, st))
			continue
		}
		found = append(found, declaredComponent(ds, stances[ds.Name]))
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
	// Appended after the sort, so the question about the pair reads under the
	// stores it is about rather than alphabetically among them.
	if c, ok := crossStoreComponent(obs, len(found)); ok {
		found = append(found, c)
	}
	d.Components = found
	return d
}

// CrossStoreComponent is the line's name in the report.
//
// Short deliberately. Explain renders a component name in a twelve character
// column and trims what does not fit, so "one person, masked the same in every
// store" would reach a reader as "one person, ..." and say nothing. The name is
// the subject and the detail is the sentence.
//
// EXPORTED because it is the one component of this dimension that is not a
// store, and something that counts stores has to be able to say so. The
// benchmark that measures stores per environment reads len(d.Components) and
// takes every name in it as a store, which was true until this line existed.
// A caller left to match the string itself would be a second copy of this
// name, kept in step with the first by nobody.
const CrossStoreComponent = "cross store"

// crossStoreComponent is the line that appears where two stores are present.
//
// It exists because the guarantee had no surface. Determinism across stores is
// a property of the masking construction, so a customer's twin almost
// certainly HAS it, and "almost certainly" is what this product refuses to
// print. masking.CrossStoreCheck is the verifier and until it had a command it
// was reachable by nobody, which made the honest form of the sentence "the
// same person is masked identically in both, and you have our word for it".
//
// THREE states and the middle one is the whole point.
//
// Nothing compared them is UNMEASURED, so it is in neither half of the
// fraction and the report carries the sentence saying how to check. A twin
// with an unverified guarantee has not been shown to be wrong, and scoring it
// as absent would be the report inventing a finding.
//
// Verified identical is REPRODUCED, and it earns its place in the numerator,
// because something read both catalogs and compared them.
//
// A pair that disagreed is ABSENT with the pair named, which is the line this
// whole thing is for: one identity masked into two people is a twin that is
// confidently wrong, and every join across the two stores is then plausible
// and false.
//
// Nothing at all when there is only one store, because the question does not
// arise and a line saying so on every single store manifest is noise that
// makes the real one easier to miss.
func crossStoreComponent(obs Observation, others int) (Component, bool) {
	if others == 0 {
		return Component{}, false
	}
	c := Component{Name: CrossStoreComponent}
	if obs.CrossStore == nil {
		c.State = Unmeasured
		c.Detail = obs.CrossStoreReason
		if c.Detail == "" {
			c.Detail = "nothing compared the stores, so whether one identity masks to one " +
				"person across them is a property of the construction rather than a checked " +
				"fact; check it with af mask crossstore"
		}
		return c, true
	}
	cs := *obs.CrossStore
	stores := strings.Join(cs.Stores, " and ")
	if cs.Checked == 0 {
		// Zero of zero. This is the case the check itself refuses to call a
		// pass, and it must not become one here either: a report over two
		// stores that share no identifier has proved nothing.
		c.State = Unmeasured
		c.Detail = "no column identifies the same thing in more than one of " + stores +
			", so nothing was compared and nothing is proved"
		return c, true
	}
	if cs.Identical == cs.Checked {
		c.State = Reproduced
		c.Detail = fmt.Sprintf(
			"%d of %d join keys mask identically across %s, read from the catalogs and no rows",
			cs.Identical, cs.Checked, stores)
		return c, true
	}
	c.State = Absent
	c.Detail = fmt.Sprintf(
		"%d of %d join keys mask identically across %s, so one identity becomes two people "+
			"and every join across them returns the wrong person: %s",
		cs.Identical, cs.Checked, stores, cs.Detail)
	return c, true
}

// storeDataComponent answers whether one store's branch holds production's
// data.
//
// dataComponent in build.go with one arm fewer, and it is dropped because a
// datastore has no such thing rather than because this does not look for it.
// There is no subset of a second store: the slice is configured for the
// primary database and taken from it. Nothing here reads a floor either,
// because the count is what the store says it holds rather than a walk over
// its rows.
//
// The empty arm is the manifest's own entry rather than an observed fact,
// which is the one place this reads a declaration on purpose. A store that
// names no source variable gets a golden of production's shape with none of
// its rows, and that is decided by the manifest before anything runs.
//
// Kept beside declaredComponent rather than folded into build.go's version.
// The two answer about different providers from different observations. What
// they must agree on is the rule below rather than the code: a component is
// not called a reproduction until something has compared it against
// production, and neither of them can call one that on its own.
func storeDataComponent(ds schema.Datastore, s Store) Component {
	c := Component{Name: ds.Name + " data"}
	switch {
	case s.BranchReason != "":
		// The branch could not be counted, so there is nothing to hold
		// against production either. One unknown, reported once.
		c.State, c.Detail = Unmeasured, s.BranchReason
		return c
	case ds.SourceURLEnv == "":
		// A golden built with no source has production's shape and none of its
		// rows, and reporting that as a copy of production would be this
		// dimension overstating in exactly the way it was changed to stop
		// understating. The refresh says the same sentence out loud when it
		// makes one.
		c.State = Substituted
		c.Detail = describeStore(s) +
			", and the store declares no source, so this is production's shape with none of its rows"
	default:
		c.State = Reproduced
		c.Detail = describeStore(s) + ", branched from " + orUnknown(s.Golden)
	}
	return withoutAVolumeProfile(c, ds)
}

// withoutAVolumeProfile applies the primary database's own rule to a second
// store: a branch nothing was compared against has not been shown to reproduce
// anything.
//
// againstProduction in build.go makes that rule for the database, because a
// golden built from a staging server holding two hundred rows was reported as
// reproducing a production holding four billion, in the same words and with
// the same verdict as a full copy. The second store is copied from whatever
// address source_url_env names, so it carries the identical uncertainty, and
// reporting it reproduced three hours after that was closed for the primary
// would be the same defect one dimension lower.
//
// So the verdict is downgraded and the reason names what is missing. It does
// NOT name a command to run, because there is not one: database.volume records
// what production's Postgres holds and there is no equivalent for a second
// store yet. Naming a fix that does not exist would be worse than naming the
// gap, and the report telling somebody what this product cannot yet answer is
// the thing it is for.
//
// It never improves a verdict, exactly as againstProduction never does. A
// store with no source is still production's shape with none of its rows.
func withoutAVolumeProfile(c Component, ds schema.Datastore) Component {
	if c.State == Reproduced {
		c.State = Unmeasured
	}
	c.Detail += ", and nothing here says what production's " + ds.Name +
		" holds, so whether this branch reproduces it is unknown. The volume profile that " +
		"answers that for the primary database, under database.volume, has no equivalent " +
		"for a second store yet"
	return c
}

// storeProvenanceComponent answers whether one store's branch can be shown to
// have come from a golden that was masked and verified.
//
// Separate from the data for the reason provenanceComponent is separate from
// it: a branch full of production's shape whose provenance nothing can check
// is not the same result as one whose attestation verifies, and a single
// verdict over both would hide whichever failed. On the second store that
// distinction is sharper rather than softer, because the second store is
// where the events are.
func storeProvenanceComponent(name string, s Store) Component {
	c := Component{Name: name + " provenance"}
	switch {
	case s.GoldenReason != "":
		c.State, c.Detail = Unmeasured, s.GoldenReason
	case !s.Attested:
		c.State, c.Detail = Absent, orUnknown(s.Attestation)
	default:
		c.State = Reproduced
		c.Detail = "golden " + s.Golden + ", " + s.Attestation
	}
	return c
}

// describeStore renders what one store's branch holds.
func describeStore(s Store) string {
	return fmt.Sprintf("%s over %s",
		plural(int64(s.Tables), "table", "tables"), plural(s.Rows, "row", "rows"))
}

// declaredComponent is one store the manifest declares and this environment
// does NOT hold a branch of, in the state this environment put it in.
//
// FOUR STATES now, one per stance, and which one a store gets turns on what
// the environment did rather than on what the manifest said. A store the
// environment branched never reaches here: it is reported by the two
// components above, from what the branch holds.
//
// A stance of golden is ABSENT. The manifest asked for a masked, verified copy
// of production in this store, this environment has no such thing, and that is
// a fact about the environment rather than a gap in what can be seen: nothing
// branched one, so there is nothing to read. So it is counted, in the
// denominator, and the score goes down, which is the point: an analytics
// product's twin holding masked Postgres metadata and zero events must not
// score as a faithful twin.
//
// The other three used to be UNMEASURED, and the sentence attached to each of
// them said that nothing here started the store, ran the rebuild or created a
// topic. That was true when it was written and it is now false, which is the
// worse of the two failures an instrument can have: the report was describing
// a build that no longer exists. `af up` starts the store, runs the rebuild
// and creates the topics, and a job that fails fails the environment.
//
// So each of them is now SUBSTITUTED when the environment did it, and that
// choice is deliberate in both directions.
//
// Not Reproduced, because none of the three is production's data and the whole
// argument for the stances is that it should not be. An empty cache holds
// nothing production holds. A rebuilt index holds documents built from the
// branch, which is the point of it and is not a copy of production's index. A
// broker created with topics and consumer groups holds no message at all.
// Substituted is the state this package defines for something that stands in
// and behaves without being the real thing, and all three are exactly that.
//
// Not Unmeasured either, which is what they were, and this is the change that
// moves the number. Unmeasured keeps a component out of the score in both
// directions, so a twin of a product whose events live in Kafka scored the
// same whether its broker had the declared topics in it or was an empty
// container nobody had touched. Substituted puts it in the denominator: the
// score goes DOWN for declaring a cache empty, honestly, because an empty
// cache does not reproduce production's cache, and the detail beside it says
// out loud that somebody chose that and why.
//
// A store that is NOT running is ABSENT whatever its stance says. The manifest
// asked the environment to hold a store and it does not hold one, and that is
// the failure the run by something check in validation refuses at the other
// end.
//
// A store that is running and whose job this environment's own run did NOT
// record stays UNMEASURED, and it is the honest answer rather than a
// concession. An environment brought up by a build that had no stance jobs is
// running the same containers from the same manifest with a broker that has no
// topic in it, and nothing here can tell that apart from one whose topics were
// created except by asking what the run recorded.
func declaredComponent(ds schema.Datastore, st Stance) Component {
	c := Component{Name: ds.Name}
	if ds.Stance == schema.StanceGolden {
		c.State, c.Detail = Absent, declaredReason(ds, goldenGap)
		return c
	}
	switch {
	case st.RunningReason != "":
		c.State = Unmeasured
		c.Detail = declaredReason(ds, st.RunningReason)
	case !st.Running:
		c.State = Absent
		c.Detail = declaredReason(ds, "no service of that name is running in this environment, "+
			"so the environment does not hold the store the manifest declared")
	case ds.Stance == schema.StanceEmpty:
		// The one stance with no job. Starting the store IS the stance, so
		// the store running is the whole of the observation and there is
		// nothing a run could separately have recorded.
		c.State = Substituted
		c.Detail = declaredReason(ds, "it is running and holds nothing, which is the stance")
	case !st.Ran:
		c.State = Unmeasured
		c.Detail = declaredReason(ds, st.RanReason)
	default:
		c.State = Substituted
		c.Detail = declaredReason(ds, ranDetail(ds))
	}
	return c
}

// ranDetail says what this environment did about a store whose job it ran.
//
// The counts are the manifest's, and that is deliberate and is not the report
// believing a declaration: the run created exactly what was declared or failed
// the environment, so the declared shape and the created shape are the same
// list. What the observation supplies is that the run HAPPENED here, which is
// the part a manifest cannot say.
func ranDetail(ds schema.Datastore) string {
	switch ds.Stance {
	case schema.StanceTopicsOnly:
		groups := 0
		for _, t := range ds.Topics {
			groups += len(t.ConsumerGroups)
		}
		return fmt.Sprintf(
			"this environment's run created %s and %s in it, with no messages, and a create "+
				"that failed would have failed the environment",
			plural(int64(len(ds.Topics)), "topic", "topics"),
			plural(int64(groups), "consumer group", "consumer groups"))
	case schema.StanceDerived:
		out := "this environment's run rebuilt it"
		if ds.From != "" {
			out += " from " + ds.From
		}
		if ds.Rebuild != nil {
			out += " by running " + strconv.Quote(ds.Rebuild.Command) + " in " + ds.Rebuild.Service
		}
		return out + ", so what it holds was built from this branch rather than copied from " +
			"production and left stale against it"
	default:
		return "this environment's run applied the stance"
	}
}

// goldenGap is what a store declared golden and never branched is missing.
//
// The four facts named one by one rather than summarised. The database
// dimension reports a branch as its golden, its attestation, its tables and
// its rows; this store has none of those, and naming each is what tells
// somebody which four things would have to appear before this line changes.
const goldenGap = "nothing here built one: no golden, no attestation, no tables and no rows. " +
	"af golden refresh makes a golden of this store and af up branches it, and " +
	"this environment holds neither"

// declaredReason says what the manifest chose for a store and what this
// environment then did about it, which are two different sentences and both
// belong here.
//
// The declared BECAUSE is carried through as written, and it is the reason
// this function exists rather than a state alone. An empty store somebody
// decided on and an empty store nobody explained look identical in a running
// environment and score identically here; the sentence is the only thing that
// tells a reviewer which one they are looking at.
func declaredReason(ds schema.Datastore, did string) string {
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
	} else if ds.Stance != schema.StanceGolden {
		// Said out loud rather than left as a shorter sentence. The stance key
		// exists so that a store's contents are a decision somebody made, and
		// a decision with no reason written down is the half of it this
		// report can still see is missing.
		b.WriteString(", with no reason declared")
	}
	b.WriteString(", and ")
	b.WriteString(did)
	return b.String()
}

// datastoreReason says why the component could not be measured, in the words
// somebody has to act on.
func datastoreReason(engine, image string) string {
	what := "a " + engine
	if image != "" {
		what += " running " + image
	}
	// Recognised from a service and declared nowhere, so nobody chose a stance
	// for it and nothing copied anything into it. The sentence says that
	// rather than "this build reproduces the primary database only", which was
	// true when this file was written and stopped being true the moment a
	// second store could hold a golden: what is missing here is the
	// declaration, and the reader can add one.
	return what + " that nothing in the manifest's datastores list names, so nobody chose a " +
		"stance for it, nothing here copied anything into it, and nothing here can say " +
		"whether it holds production's data or came up empty"
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

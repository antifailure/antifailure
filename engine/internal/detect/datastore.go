package detect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The second store, proposed from what the repository already runs.
//
// A compose file that runs ClickHouse beside Postgres left no trace at all in
// the manifest af init wrote. The image classifier recognised it, the finding
// was made, and mergeDatabase read the ones it understood and dropped the
// rest on the floor: Kafka was not classified at all, and ClickHouse, Redis
// and Elasticsearch were classified and then discarded. So the twin of an
// analytics product came up holding a masked Postgres and an empty everything
// else, and nothing in the file the developer read said so.
//
// What this file does is name them. A stance is DECLARED and never defaulted,
// which is the rule schema.DatastoreStance exists to enforce, so detection
// proposes one per store with the reason written out, and the proposal is
// something a person confirms at af init rather than something they have to
// discover they needed.
//
// The half of this that is easy to get wrong is the other direction. Writing
// an entry for a store this build cannot bring up would produce a manifest
// af up refuses, and af init promises the opposite: the draft is normalized
// and validated before it is written precisely so that the file it leaves
// behind is one every later command accepts. The validator would not catch it,
// because Datastore.Engine is an open set on purpose and the refusal happens
// at run time in the provider lookup. So provisionable is asked per store, the
// answer comes from the same list the engine's own refusal is written from,
// and a store this build cannot serve is REPORTED rather than declared.

// ProposedDatastore is one store detection found beside the primary database,
// and the stance it would take.
//
// Provisionable is what decides whether it reaches the manifest. Both kinds
// are in this list on purpose: the one thing worse than a store nobody
// declared is a store nobody mentioned.
type ProposedDatastore struct {
	// Name is what the store is called in the manifest and in the report.
	Name string
	// Engine is what it runs, as the datastores list spells it.
	Engine string
	// Stance is what detection proposes doing about its contents.
	Stance schema.DatastoreStance
	// Because is the reason for the stance, written for a person.
	Because string
	// From names the store a derived one is rebuilt from.
	From string
	// Evidence is the file that showed detection this store exists.
	Evidence string
	// Image is the container image the store was recognised from.
	Image string
	// Provisionable reports whether this build can bring the engine up. A
	// store that is not is left out of the manifest and named in the summary.
	Provisionable bool
}

// stanceProposal is the stance detection proposes per engine, and why.
//
// Section 4.3 of the plan is the source of these, and the reasons are written
// out per engine rather than shared, for the same reason the cloud catalog's
// refusals are: a sentence that explains five stores explains none of them,
// and this one is copied verbatim into the fidelity report.
type stanceProposal struct {
	stance  schema.DatastoreStance
	because string
	from    string
}

var stanceProposals = map[string]stanceProposal{
	"clickhouse": {
		stance: schema.StanceGolden,
		because: "the events are here rather than in Postgres, so a twin without them tests " +
			"every chart and every analytics migration against nothing",
	},
	"redis": {
		stance:  schema.StanceEmpty,
		because: "a cache is rebuilt from the primary and a copy of one would be noise",
	},
	"kafka": {
		stance: schema.StanceTopicsOnly,
		because: "a broker wants its topics and consumer groups rather than a replay of " +
			"production traffic, which would be delivered to whatever is consuming",
	},
	"elasticsearch": {
		stance: schema.StanceDerived,
		from:   schema.PrimaryDatastore,
		because: "an index rebuilt from the branch cannot be stale against it, and a clone " +
			"of production's index can",
	},
	"mongodb": {
		stance: schema.StanceGolden,
		because: "this holds application records rather than a cache, so an empty one is a " +
			"twin of nothing",
	},
}

// datastoreEngineOf maps the image classifier's answer onto the engine name a
// datastores entry spells, or empty for a classification that is not a second
// store.
//
// postgres is absent deliberately. It is the primary, it arrives through
// database:, and normalize turns that into the entry named primary. A second
// entry for it would be two records of one store.
func datastoreEngineOf(kind string) string {
	switch kind {
	case "clickhouse", "redis", "kafka", "elasticsearch", "mongodb":
		return kind
	}
	return ""
}

// mergeDatastores turns the datastore findings into proposals, and the
// provisionable proposals into manifest entries.
//
// A mongodb with no Postgres beside it is the application's own database
// rather than a second store, and mergeDatabase already asks about that case
// by name. Proposing a stance for it too would be the same fact said twice in
// two different vocabularies, so it is proposed only when a Postgres was found
// as well.
func mergeDatastores(findings []Finding, hasPostgres bool) ([]ProposedDatastore, []schema.Datastore) {
	seen := map[string]bool{}
	var proposed []ProposedDatastore

	for _, f := range OfKind(findings, KindDatastore) {
		engine := f.Subject
		if engine == "mongodb" && !hasPostgres {
			continue
		}
		p, ok := stanceProposals[engine]
		if !ok {
			continue
		}
		name := sanitizeServiceName(f.Extra["service"])
		if name == "" {
			name = engine
		}
		if name == schema.PrimaryDatastore {
			// primary is reserved for the entry database: normalizes into,
			// and the validator refuses a second store under that name. A
			// compose service somebody called primary is not a reason to
			// produce a manifest af init would then refuse to write.
			name = engine
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		proposed = append(proposed, ProposedDatastore{
			Name:          name,
			Engine:        engine,
			Stance:        p.stance,
			Because:       p.because,
			From:          p.from,
			Evidence:      f.Evidence,
			Image:         f.Value,
			Provisionable: provider.ProvidesDatastoreEngine(engine),
		})
	}

	sort.SliceStable(proposed, func(i, j int) bool { return proposed[i].Name < proposed[j].Name })

	var entries []schema.Datastore
	for _, p := range proposed {
		if !p.Provisionable {
			continue
		}
		entries = append(entries, schema.Datastore{
			Name:    p.Name,
			Engine:  p.Engine,
			Stance:  p.Stance,
			Because: p.Because,
			From:    p.From,
		})
	}
	return proposed, entries
}

// UnprovisionableNote is the sentence af init prints about a store it found
// and did not declare.
//
// It says the stance it would have taken, because the stance is the decision
// and a person reading this is the one who has to make it. It does not say
// "unsupported": the store is running in their compose file and works there,
// and what is missing is this engine's ability to hold a copy of it.
func (p ProposedDatastore) UnprovisionableNote() string {
	engines := strings.Join(provider.BuiltInDatastoreEngines(), ", ")
	from := ""
	if p.From != "" {
		from = fmt.Sprintf(" from %s", p.From)
	}
	return fmt.Sprintf(
		"%s runs %s (%s). The stance for it would be %s%s, because %s. "+
			"It is not in the manifest, because this build brings up %s and would refuse a "+
			"datastore it has no provider for.",
		p.Evidence, p.Name, p.Image, p.Stance, from, p.Because, engines)
}

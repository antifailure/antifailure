package env

import (
	"fmt"

	"github.com/antifailure/antifailure/engine/internal/datastore/broker"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The three stances that are not a golden, turned into things the environment
// does.
//
// Every one of them had a manifest key and no behaviour. A store declared
// empty, derived or topics_only got a progress line saying that this build
// brings up the golden stance only, and after that the environment either
// started a container because a service happened to carry the same name or did
// not start one at all, and nothing afterwards could tell those two apart. The
// stance was a comment.
//
// What each one becomes:
//
// EMPTY is the service's own container and nothing else, which is the correct
// answer for a cache and is the reason the stance exists. There is no job:
// starting the store is what a service declaration already does, and a cache
// that came up empty is already in the state its stance asks for. What was
// missing was never a container, it was the report saying that this store is
// empty because somebody decided it should be, and that is fidelity's half.
//
// DERIVED is a command run to completion inside the environment once every
// service is up. A search index cloned from production is stale against the
// branch the moment the branch is masked, because the documents in it name
// people who do not exist in the twin's Postgres. An index BUILT from the
// branch cannot be stale against it.
//
// TOPICS_ONLY is the broker's own topic and consumer group commands, composed
// from the declared shape and run in the broker's own image. The messages are
// deliberately not copied. What a consumer needs is the topic, the partition
// count and a group with a committed offset, and none of those three exist in
// an empty broker.
//
// A job that fails fails the environment. That is the whole reason they are
// jobs rather than best effort: a broker with no topics and an index nobody
// built look exactly like a working twin until the first thing that reads
// either one gets nothing back.

// stanceJobs is every command this environment runs to bring a declared store
// to the state its stance asks for.
//
// Built here rather than in the runtime because the manifest is here. The
// runtime is handed a service name and a command and knows nothing about
// stances, which is what keeps a second runtime from having to reimplement
// the broker's command line.
func (o *Orchestrator) stanceJobs() ([]provider.StanceJob, error) {
	return o.stanceJobsFor(o.opts.Manifest)
}

// stanceJobsFor builds the jobs for one manifest.
//
// Named apart from the orchestrator's own manifest because a run can hold two.
// The comparison against a previous release brings up a SECOND environment
// from that release's manifest, and a broker in it with no topics is the same
// silent empty this lane exists to remove, one environment over: the previous
// release's consumers would poll a name that is not there and the comparison
// would report them as agreeing with the new release's, both having processed
// nothing.
func (o *Orchestrator) stanceJobsFor(m *schema.Manifest) ([]provider.StanceJob, error) {
	var jobs []provider.StanceJob
	for _, ds := range datastoreDeclarations(m) {
		switch ds.Stance {
		case schema.StanceGolden:
			// Branched, not built. datastores() above does that one.
		case schema.StanceEmpty:
			// Nothing to run, and the progress line is the point. An empty
			// store that says nothing is the failure the stance key exists to
			// remove, so the run says it out loud with the declared reason
			// attached and the report says it again afterwards.
			o.progress(fmt.Sprintf("the datastore %s starts empty on purpose%s",
				ds.Name, becauseSuffix(ds.Because)))
		case schema.StanceDerived:
			// Validation refuses a derived store with no rebuild, so a nil
			// here is a manifest that reached this by another route. Skipping
			// it silently would be an index nobody built in an environment
			// whose manifest says it holds one.
			if ds.Rebuild == nil {
				return nil, aferrors.Coded(aferrors.AFMAN002,
					"path", o.opts.Root+"/antifailure.yaml",
					"detail", fmt.Sprintf(
						"the datastore %q is derived and declares no rebuild, so nothing "+
							"here could build it from %s", ds.Name, orNothing(ds.From)))
			}
			jobs = append(jobs, provider.StanceJob{
				Store:   ds.Name,
				Stance:  string(ds.Stance),
				Service: ds.Rebuild.Service,
				Command: ds.Rebuild.Command,
			})
		case schema.StanceTopicsOnly:
			job, err := o.topicJob(m, ds)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, job)
		default:
			// A stance this build does not know, which validation refuses.
			// Reaching here means a manifest read by a build older than the
			// one that wrote it, and doing nothing quietly is how a store
			// declared something specific comes up as an empty container.
			return nil, aferrors.Coded(aferrors.AFMAN002,
				"path", o.opts.Root+"/antifailure.yaml",
				"detail", fmt.Sprintf(
					"the datastore %q declares the stance %q and this build knows %s",
					ds.Name, ds.Stance, stanceList()))
		}
	}
	return jobs, nil
}

// topicJob composes the command that creates one broker's declared shape.
//
// It runs in the BROKER's own image, addressed by the broker's own name on the
// environment's network. Nothing here speaks a wire protocol: the image ships
// the tools that create a topic, and a hand written client would be a
// reimplementation of them whose first wrong partition assignment would be
// trusted.
func (o *Orchestrator) topicJob(m *schema.Manifest, ds schema.Datastore) (provider.StanceJob, error) {
	command, err := broker.Command(ds.Engine, ds.Name, brokerPort(m, ds), ds.Topics)
	if err != nil {
		// Refused rather than skipped. A broker this build cannot shape comes
		// up with no topic in it, which is the empty stance under a different
		// word, and the manifest said something else.
		return provider.StanceJob{}, aferrors.Coded(aferrors.AFMAN002,
			"path", o.opts.Root+"/antifailure.yaml",
			"detail", fmt.Sprintf("the datastore %q declares stance topics_only and %s",
				ds.Name, err.Error()))
	}
	return provider.StanceJob{
		Store:   ds.Name,
		Stance:  string(ds.Stance),
		Service: ds.Name,
		Command: command,
	}, nil
}

// brokerPort is where the store listens inside the environment.
//
// The service's own declared port wins, because a manifest that moved the
// broker off its usual port did so for a reason and the topic creation has to
// reach the same place every consumer does. The engine's default is the
// fallback for a service that declares none, which is the ordinary case: a
// broker other services reach by name has no reason to publish a port.
func brokerPort(m *schema.Manifest, ds schema.Datastore) int {
	if m != nil {
		for _, svc := range m.Services {
			if svc.Name == ds.Name && svc.Port > 0 {
				return svc.Port
			}
		}
	}
	return broker.DefaultPort(ds.Engine)
}

// becauseSuffix renders the declared reason, or says that there is none.
//
// The absence is said out loud rather than left as a shorter sentence. An
// empty store somebody decided on and an empty store nobody explained look
// identical in a running environment, and this line is one of the two places
// that tells them apart.
func becauseSuffix(because string) string {
	if because == "" {
		return ", and the manifest gives no reason"
	}
	return ", because " + because
}

// orNothing names the store a derived one reads, or says there is not one.
func orNothing(from string) string {
	if from == "" {
		return "anything"
	}
	return from
}

// stanceList names the stances this build knows, for a manifest that declares
// one it does not.
func stanceList() string {
	out := ""
	for i, s := range schema.AllDatastoreStances() {
		switch {
		case i == 0:
			out = string(s)
		case i == len(schema.AllDatastoreStances())-1:
			out += " and " + string(s)
		default:
			out += ", " + string(s)
		}
	}
	return out
}

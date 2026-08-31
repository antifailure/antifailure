package env

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Reconciliation: the half of the journal that had never run.
//
// engine/internal/journal opens with "the rule the whole product rests on:
// everything that is created has a recorded, compensating deletion", and the
// recording half was real. The compensating half was not. Journal.Replay had no
// caller anywhere in the engine, journal.NewRegistry had none either, so there
// was no deleter registry for Replay to consult even in principle, and
// Journal.Commit was never called so no record had ever left the intent state.
// Records went in and nothing ever read them or removed them.
//
// It was invisible because `af down` works, and it works by sweeping the Docker
// daemon for the environment's labels. That is a real teardown and it covers
// the common case completely. What a label sweep cannot cover is exactly the
// set the journal exists for: a resource created in the instant before a crash,
// a resource at a provider that is not the local daemon, and every kind in the
// journal's own list that no sweep looks for. It also meant the journal table
// grew forever on every machine, because nothing ever marked a record
// compensated.
//
// The replay runs at the END of teardown and nowhere else. Running it at the
// start of `af up` would be worse than not running it at all: an environment
// that is up has live journal records by design, and replaying them would
// delete the environment somebody had just asked for. The records are a
// description of what exists, not of what is owed, until teardown says
// otherwise.

// reconcile compensates whatever the label sweep did not.
//
// It is additive to the sweep rather than a replacement for it. The sweep is
// faster and finds resources the journal never heard of, which is the case
// after a crash between the intent and the commit; the replay finds resources
// the sweep cannot see. Running both and merging what each leaves behind is the
// only combination where neither gap is silent.
func (o *Orchestrator) reconcile(ctx context.Context, s *session, envID string, td *Teardown) {
	cli, err := dockerutil.Client()
	if err == nil {
		defer func() { _ = cli.Close() }()
	}
	registry, err := o.deleters(s, cli, err)
	if err != nil {
		// Reported as pending rather than swallowed. Being unable to build the
		// registry means nothing can be compensated, and reporting a clean
		// teardown in that state is the lie this whole file exists to stop.
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "journal", ID: envID,
			Reason: fmt.Sprintf("the journal could not be replayed: %v", err),
		})
		return
	}

	result, err := s.journal.Replay(ctx, envID, registry)
	if err != nil && !errors.Is(err, context.Canceled) {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "journal", ID: envID, Reason: err.Error(),
		})
	}
	td.Removed += result.Compensated
	if result.Compensated > 0 {
		o.progress(fmt.Sprintf(
			"the journal compensated %d resources the sweep did not find", result.Compensated))
	}

	// Failures and skips are reported one by one rather than as a count,
	// because "three resources are still recorded" tells an operator nothing
	// they can act on and "the network shop-main-a1b2 is still recorded"
	// tells them where to look.
	for _, e := range result.Errors {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "journal", ID: envID, Reason: e.Error(),
		})
	}
	for _, rec := range result.Skipped {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: string(rec.Kind), ID: rec.IdemKey,
			Reason: fmt.Sprintf(
				"this build has no way to delete a %s at %s, so it was left rather than forgotten",
				rec.Kind, rec.Provider),
		})
	}
}

// deleters builds the compensating action for every kind this engine creates.
//
// The registry is built per teardown rather than kept as package state, because
// one of the deleters closes over the database provider this environment
// selected, and that is a per-run decision.
func (o *Orchestrator) deleters(s *session, cli *client.Client, cliErr error) (*journal.Registry, error) {
	reg := journal.NewRegistry()
	if cliErr != nil {
		// A daemon that is not there is not a reason to compensate nothing at
		// all: the database branch may be at a provider that has no daemon in
		// it. So the Docker deleters are left unregistered, which makes their
		// records Skipped and therefore reported, rather than silently treated
		// as compensated.
		return o.databaseDeleters(reg, s), nil
	}

	// The local runtime records a resource by the NAME it is about to create
	// it under, before creating it, and Docker addresses containers, networks
	// and volumes by name as readily as by identifier. So the idempotency key
	// is the handle, and a record written in the instant before a crash, whose
	// resource may or may not exist, is compensated by the same call as one
	// that was fully created. That is what makes the intent state useful
	// rather than merely honest.
	//
	// A NAME IS NOT EVIDENCE OF OWNERSHIP, which is why all three deleters go
	// through dockerutil rather than through the Docker client. A record says
	// what this engine meant to create; what the name resolves to on the
	// daemon today is a separate question, and on a machine where somebody has
	// their own container called the same thing the two answers differ. Every
	// one of these checks the managed label and refuses with ErrNotOurs
	// otherwise, so the worst a replay can do to a resource it does not own is
	// report it. `af down`'s label sweep has always had this property because
	// a filter can only match what it labelled; the replay reaches resources by
	// name and so has to assert it, and for two of these three it did not.
	handle := func(rec journal.Record) string {
		if rec.ExternalID != "" {
			return rec.ExternalID
		}
		return rec.IdemKey
	}

	reg.Register("local", journal.KindContainer, journal.DeleterFunc(
		func(ctx context.Context, rec journal.Record) error {
			return dockerutil.RemoveContainer(ctx, cli, handle(rec))
		}))
	reg.Register("local", journal.KindNetwork, journal.DeleterFunc(
		func(ctx context.Context, rec journal.Record) error {
			return dockerutil.RemoveNetwork(ctx, cli, handle(rec))
		}))
	reg.Register("local", journal.KindVolume, journal.DeleterFunc(
		func(ctx context.Context, rec journal.Record) error {
			return dockerutil.RemoveVolume(ctx, cli, handle(rec))
		}))

	return o.databaseDeleters(reg, s), nil
}

// databaseDeleters registers the branch compensation.
//
// Destroying a branch twice must succeed, which every provider is required to
// do and which the conformance suite checks, so this is safe to run after the
// ordinary teardown has already destroyed it.
func (o *Orchestrator) databaseDeleters(reg *journal.Registry, s *session) *journal.Registry {
	if s.dbProv == nil {
		return reg
	}
	destroy := journal.DeleterFunc(func(ctx context.Context, rec journal.Record) error {
		return s.dbProv.Destroy(ctx, provider.Branch{EnvID: rec.Env, ProviderRef: rec.ExternalID})
	})
	reg.Register(s.dbProv.Name(), journal.KindDatabaseBranch, destroy)
	reg.Register(s.dbProv.Name(), journal.Kind("database"), destroy)
	// Records written before the provider name was recorded correctly all say
	// "docker", whichever provider actually made them. Registering the legacy
	// name against the selected provider is the only thing that can compensate
	// them, and being wrong costs one destroy call for a branch that is not
	// there, which succeeds.
	reg.Register("docker", journal.Kind("database"), destroy)
	reg.Register("docker", journal.KindDatabaseBranch, destroy)
	return reg
}

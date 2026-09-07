package env

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The second store, brought up beside the first.
//
// Everything in env.go about a database is about ONE database, because
// schema.Database is one struct and it is Postgres. A manifest can now declare
// datastores, and until this file existed nothing read that list at run time:
// the validator refused a store with no stance and the report named it, and an
// `af up` did nothing about it at all. An analytics product's twin therefore
// still held a masked Postgres and zero events.
//
// What this does is the same six steps the database gets, per declared store:
// choose a golden made for this project, refresh one when there is none, mask
// it, verify it, branch it, and hand the branch's address to the services. The
// steps are not re-implemented; the provider interface is the same one, the
// masking rules are the same rules, and the verification is the same scanner.

// datastoreURLPrefix and datastoreURLSuffix bracket the variable a service
// reads a store's address from.
//
// AF_DATASTORE_EVENTS_URL for a store called events. One predictable rule
// rather than a per engine convention, because the engine's job is to say
// where the store is and the application's job is to know what to do with it,
// and a name invented per engine would be a list this file has to keep in step
// with every application's configuration.
const (
	datastoreURLPrefix = "AF_DATASTORE_"
	datastoreURLSuffix = "_URL"
)

// DatastoreURLVar is the variable a service reads one store's address from.
func DatastoreURLVar(name string) string {
	var b strings.Builder
	b.WriteString(datastoreURLPrefix)
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	b.WriteString(datastoreURLSuffix)
	return b.String()
}

// datastoreHandle is one declared store and the provider serving it.
type datastoreHandle struct {
	decl schema.Datastore
	prov provider.Datastore
	// server is the local ClickHouse this store lives on, when it lives on
	// one. It is what the runtime attaches to the environment's network so a
	// service can reach the store by name.
	server *clickhouse.LocalServer
}

// datastoreDeclarations returns the stores a manifest declares other than the
// primary.
//
// The primary is the entry database: normalizes into, and it is brought up by
// o.database. Bringing it up twice would branch two databases and hand the
// services the second one.
func datastoreDeclarations(m *schema.Manifest) []schema.Datastore {
	if m == nil {
		return nil
	}
	out := make([]schema.Datastore, 0, len(m.Datastores))
	for _, ds := range m.Datastores {
		if ds.Name == schema.PrimaryDatastore {
			continue
		}
		out = append(out, ds)
	}
	return out
}

// openDatastores opens a provider per declared store, once per session.
//
// Lazily, and only from the two commands that need one. Opening a ClickHouse
// datastore starts the machine's ClickHouse if it is not already running, and
// doing that on every `af status` would make a read only command wait a minute
// on a container it never asks anything.
func (o *Orchestrator) openDatastores(ctx context.Context, s *session, mustExist bool) error {
	if s.stores != nil {
		return nil
	}
	decls := datastoreDeclarations(o.opts.Manifest)
	if len(decls) == 0 {
		s.stores = []*datastoreHandle{}
		return nil
	}
	stores := make([]*datastoreHandle, 0, len(decls))
	for _, ds := range decls {
		h, err := o.newDatastoreProvider(ctx, ds, mustExist)
		if err != nil {
			for _, opened := range stores {
				_ = opened.prov.Close()
			}
			return err
		}
		if h == nil {
			continue
		}
		stores = append(stores, h)
	}
	s.stores = stores
	return nil
}

// newDatastoreProvider builds the provider for one declared store, or nil when
// there is nothing to open.
//
// A registered provider is consulted after the built in one and never before
// it, which is the rule newDatabaseProvider follows and for the same reason: a
// registration adds a provider and can never take one over.
func (o *Orchestrator) newDatastoreProvider(
	ctx context.Context, ds schema.Datastore, mustExist bool,
) (*datastoreHandle, error) {
	if p, ok := o.extensions().DatastoreProviderNamed(ds.Provider); ok && ds.Provider != "" {
		built, err := p.Open(ctx, extension.DatastoreConfig{
			Root: o.opts.Root, Name: ds.Name, Datastore: ds,
			StateDir: o.opts.Root + "/" + StateDir,
			Now:      o.opts.Clock.Now, Lookup: o.lookupForProvider,
		})
		if err != nil {
			return nil, err
		}
		if built == nil {
			// The same explicit nil check newDatabaseProvider makes: a nil
			// pointer in a non-nil interface passes every guard and crashes
			// in Close.
			return nil, aferrors.Coded(aferrors.AFEXT002,
				"socket", extension.SocketDatastoreProvider, "name", ds.Provider)
		}
		return &datastoreHandle{decl: ds, prov: built}, nil
	}

	switch ds.Engine {
	case "clickhouse":
		server, err := clickhouse.EnsureLocalServer(ctx, clickhouse.LocalServerOptions{
			Getenv:        o.opts.Getenv,
			Progress:      o.progress,
			OnlyIfPresent: mustExist,
		})
		if err != nil {
			if mustExist && errorsIsNoLocalServer(err) {
				// Nothing to tear down. The store lived on a container that
				// is not there, and everything it held went with it.
				return nil, nil
			}
			return nil, err
		}
		p, err := clickhouse.New(clickhouse.Options{
			ServerURL: server.URL, Name: ds.Name,
			Clock: o.opts.Clock, Progress: o.progress,
		})
		if err != nil {
			return nil, err
		}
		return &datastoreHandle{decl: ds, prov: p, server: &server}, nil
	default:
		// Named and not built here, and saying so beats every alternative. A
		// silent skip would leave the store empty in an environment whose
		// manifest says it holds a masked copy, which is the exact failure
		// the datastores list was added to stop.
		return nil, aferrors.Coded(aferrors.AFMAN002,
			"path", o.opts.Root+"/antifailure.yaml",
			"detail", fmt.Sprintf(
				"the datastore %q declares the engine %q and this build has no provider for "+
					"it. The engines it can provide are: clickhouse. Remove the datastore, or "+
					"register a provider for %q and name it in the datastore's provider key",
				ds.Name, ds.Engine, ds.Engine))
	}
}

func errorsIsNoLocalServer(err error) bool {
	return strings.Contains(err.Error(), clickhouse.ErrNoLocalServer.Error())
}

// datastores brings every declared store to the state its stance asks for, and
// returns the variables the services read them through.
//
// The names it returns beside them are the stores the ENVIRONMENT provides. A
// manifest that declares both a datastore called events and a service called
// events is declaring one thing twice: the service is how the store used to be
// started and the datastore is what it holds. Starting both would put two
// ClickHouses on one network under one name, and Docker would answer half the
// application's queries with the empty one.
func (o *Orchestrator) datastores(
	ctx context.Context, s *session,
) (vars map[string]secrets.Value, provided []string, err error) {
	if err := o.openDatastores(ctx, s, false); err != nil {
		return nil, nil, err
	}
	if len(s.stores) == 0 {
		return nil, nil, nil
	}
	vars = map[string]secrets.Value{}

	for _, h := range s.stores {
		if h.decl.Stance != schema.StanceGolden {
			// Every other stance is a lane of its own and none of them is
			// this one. Said out loud rather than skipped silently, because
			// an undeclared empty store is the failure the stance key exists
			// to prevent and an unimplemented one that says nothing is the
			// same failure wearing a manifest entry.
			o.progress(fmt.Sprintf(
				"the datastore %s declares %s, and this build brings up the golden stance "+
					"only, so nothing here started it", h.decl.Name, h.decl.Stance))
			continue
		}
		url, err := o.datastoreGolden(ctx, s, h)
		if err != nil {
			return nil, nil, err
		}
		vars[DatastoreURLVar(h.decl.Name)] = url
		provided = append(provided, h.decl.Name)
	}
	return vars, provided, nil
}

// datastoreGolden chooses or makes a golden for one store, branches it, and
// returns the branch's address on this machine.
func (o *Orchestrator) datastoreGolden(
	ctx context.Context, s *session, h *datastoreHandle,
) (secrets.Value, error) {
	prov, err := o.provenanceOf()
	if err != nil {
		return secrets.Value{}, err
	}
	// The project's identity AND the store's name. A golden pool is shared,
	// and the lesson pickGolden's comment records cost two environments their
	// data: selecting the newest verified version and nothing else branches
	// somebody else's production. A digest that did not name the store would
	// make one store's golden selectable for another's, which is the same
	// mistake one level down.
	want := prov.digest() + ":" + h.decl.Name

	goldens, err := h.prov.ListGoldens(ctx)
	if err != nil {
		return secrets.Value{}, err
	}
	version, refused := pickGolden(goldens, want)
	if version == "" && h.decl.SourceURLEnv != "" {
		// A configured source and nothing to branch is a refusal rather than
		// an empty store, which is the rule the primary database already has
		// and the reason it has it: a refresh reads production, it is slow,
		// and the person running `af up` did not ask for one. Reading
		// production silently on an ordinary up is the surprise; being told to
		// run `af golden refresh`, which now refreshes this store too, is not.
		//
		// The more specific refusal wins. A variable that names production and
		// holds nothing is one unset variable, and answering that with a count
		// of other people's goldens sends somebody to the wrong command.
		if _, srcErr := o.datastoreSource(ctx, h.decl); srcErr != nil {
			return secrets.Value{}, srcErr
		}
		return secrets.Value{}, aferrors.Coded(aferrors.AFDB012, "count", fmt.Sprint(refused))
	}
	if version == "" {
		o.progress(fmt.Sprintf("no golden for the datastore %s yet, creating one", h.decl.Name))
		made, refreshErr := o.refreshDatastore(ctx, s, h, want)
		if refreshErr != nil {
			return secrets.Value{}, refreshErr
		}
		version = made.Version
	} else {
		o.progress(fmt.Sprintf("the datastore %s branches golden %s, %d other versions were "+
			"made for something else", h.decl.Name, version, refused))
	}

	rec, err := s.journal.Intent(ctx, o.envID, h.prov.Name(),
		journal.KindDatastoreBranch, o.envID+"/"+h.decl.Name, nil)
	if err != nil {
		return secrets.Value{}, err
	}
	branch, err := h.prov.Branch(ctx, version, o.envID)
	if err != nil {
		return secrets.Value{}, err
	}
	if err := s.journal.Commit(ctx, rec.ID, branch.ProviderRef); err != nil {
		return secrets.Value{}, err
	}
	url, err := h.prov.ConnString(ctx, branch)
	if err != nil {
		return secrets.Value{}, err
	}
	o.opts.Redactor.Register(url.Reveal())
	o.event(s, events.Progress,
		fmt.Sprintf("the datastore %s is branched from %s", h.decl.Name, version))
	return url, nil
}

// refreshDatastore makes a golden of one store.
//
// The masking and the verification are the engine's, not the provider's, the
// same inversion of control the database has: the provider produces a
// candidate and calls back, so the rules have one implementation and a
// provider cannot choose to skip them. What differs per engine is only the
// dialect, and the engine refuses a store it has no dialect for rather than
// publishing an unmasked golden of it.
func (o *Orchestrator) refreshDatastore(
	ctx context.Context, s *session, h *datastoreHandle, want string,
) (DatastoreGolden, error) {
	if _, err := masking.DialectFor(h.decl.Engine); err != nil {
		return DatastoreGolden{}, aferrors.Coded(aferrors.AFMSK010, "detail", fmt.Sprintf(
			"the datastore %s declares stance golden and this build has no masking dialect "+
				"for %s, so a copy of it could be neither masked nor verified. Declare "+
				"stance: empty with a because, or use an engine this build can mask: %s",
			h.decl.Name, h.decl.Engine, strings.Join(masking.DialectNames(), ", ")))
	}
	key, err := o.MaskingKey(ctx, s)
	if err != nil {
		return DatastoreGolden{}, err
	}
	rules, hash, err := o.rules()
	if err != nil {
		return DatastoreGolden{}, err
	}
	source, err := o.datastoreSource(ctx, h.decl)
	if err != nil {
		return DatastoreGolden{}, err
	}
	if source.IsZero() {
		// The sentence the primary database prints for the same case, about
		// the store this wave exists for. An empty analytics store is a
		// legitimate starting point and an unannounced one is how somebody
		// ends up trusting a blank ClickHouse.
		o.progress(fmt.Sprintf(
			"the datastore %s has no source, so its golden holds no rows. Set "+
				"source_url_env on it and add that secret to the repository.", h.decl.Name))
	}

	var masked clickhouse.MaskResult
	var report verify.Report
	gv, err := h.prov.RefreshGolden(ctx, provider.GoldenSpec{
		SourceURL: source, RulesHash: hash, Provenance: want,
		Mask: func(ctx context.Context, candidate secrets.Value) error {
			// maskErr rather than the err this function already has. Both the
			// closure and the statement it is an argument to would write that
			// one, and a reader cannot tell which assignment wins by looking.
			var maskErr error
			masked, maskErr = clickhouse.Mask(ctx, candidate, clickhouse.MaskOptions{
				Key: key, Rules: rules, RulesHash: hash,
				Progress: func(line string) { o.progress(line) },
			})
			return maskErr
		},
		Verify: func(ctx context.Context, candidate secrets.Value) (string, error) {
			var scanErr error
			report, scanErr = clickhouse.Scan(ctx, candidate, clickhouse.ScanOptions{
				Unruled:  masked.CopiedUnchanged,
				Now:      o.opts.Clock.Now,
				Progress: func(line string) { o.progress("verification: " + line) },
			})
			if scanErr != nil {
				return "", aferrors.Wrap(scanErr, aferrors.AFMSK002,
					"detector", "scan", "table", "any", "column", "any")
			}
			if !report.Clean() {
				// The refusal the whole product rests on, arriving for the
				// second store. A golden that fails here is never published,
				// so it can never be branched, so no environment can hold it.
				if len(report.Findings) > 0 {
					f := report.Findings[0]
					return "", aferrors.Coded(aferrors.AFMSK002,
						"detector", f.Detector, "table", f.Schema+"."+f.Table,
						"column", f.Column)
				}
				table, column, detail := describeSkip(report.Skipped[0])
				return "", aferrors.Coded(aferrors.AFMSK011,
					"table", table, "column", column, "detail", detail)
			}
			return signDatastoreReport(report, hash, want)
		},
	})
	if err != nil {
		return DatastoreGolden{}, err
	}
	o.progress(fmt.Sprintf(
		"the datastore %s golden %s holds %d rows across %d tables, masked and verified over "+
			"%d columns", h.decl.Name, gv.ID, masked.Rows, masked.Tables, report.Columns))
	return DatastoreGolden{
		Name: h.decl.Name, Version: gv.ID, Rows: masked.Rows,
		Tables: masked.Tables, Columns: report.Columns, Empty: source.IsZero(),
	}, nil
}

// DatastoreGolden is what one store's refresh did.
type DatastoreGolden struct {
	// Name is the store's name in the manifest.
	Name string
	// Version is the golden that was published.
	Version string
	// Rows and Tables are what the masking rewrote.
	Rows   int64
	Tables int
	// Columns is what the verification scan read back.
	Columns int
	// Empty reports that the store declares no source, so the golden holds no
	// rows. It is carried rather than inferred from Rows, because a source
	// that exists and holds nothing is a different fact from no source at all
	// and only one of them is somebody's mistake.
	Empty bool
}

// refreshDatastores refreshes a golden for every declared store.
//
// Called from the same place the database's refresh happens, so that
// `af golden refresh` refreshes the whole twin rather than the part of it that
// existed first. A project whose events are the point would otherwise have one
// command that refreshes the metadata and no command at all for the events.
func (o *Orchestrator) refreshDatastores(
	ctx context.Context, s *session, result *GoldenResult,
) error {
	if err := o.openDatastores(ctx, s, false); err != nil {
		return err
	}
	prov, err := o.provenanceOf()
	if err != nil {
		return err
	}
	for _, h := range s.stores {
		if h.decl.Stance != schema.StanceGolden {
			continue
		}
		made, err := o.refreshDatastore(ctx, s, h, prov.digest()+":"+h.decl.Name)
		if err != nil {
			return err
		}
		result.Datastores = append(result.Datastores, made)
	}
	return nil
}

// signDatastoreReport produces the attestation a golden is published on.
//
// The same two calls verifyDatabase makes and in the same order, because the
// attestation is what ListGoldens reads Verified back out of, and a golden
// carrying an empty one is a golden nothing may branch.
func signDatastoreReport(report verify.Report, hash, provenance string) (string, error) {
	_, priv, err := verify.GenerateKey()
	if err != nil {
		return "", err
	}
	att, err := verify.Sign(report, "", hash, provenance, priv)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(att)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// datastoreSource resolves the variable naming a store's production address.
func (o *Orchestrator) datastoreSource(
	ctx context.Context, ds schema.Datastore,
) (secrets.Value, error) {
	if ds.SourceURLEnv == "" {
		return secrets.Value{}, nil
	}
	value, _, found, err := o.secretChain().Lookup(ctx, ds.SourceURLEnv)
	if err != nil {
		return secrets.Value{}, err
	}
	if !found || value.IsZero() {
		// A manifest that NAMES production and a shell that does not hold it
		// is a refusal rather than an empty golden. This is AF-DB-016
		// arriving for the second store, and the failure it prevents is the
		// same one: a pull request from a fork gets no secrets, so the
		// variable is empty in exactly the runs nobody watches, and the
		// environment comes up looking correct with none of production's
		// shape in it.
		return secrets.Value{}, aferrors.Coded(aferrors.AFDB016, "variable", ds.SourceURLEnv)
	}
	o.opts.Redactor.Register(value.Reveal())
	return value, nil
}

// datastoreTeardown removes every declared store's branch for an environment.
//
// It runs after the runtime sweep and beside the database's, for the same
// reason the database's does: a service still running against a store that has
// been taken away produces a page of errors that has nothing to do with why
// the environment went away.
func (o *Orchestrator) datastoreTeardown(ctx context.Context, s *session, envID string, td *Teardown) {
	// mustExist, so that a teardown does not START a ClickHouse in order to
	// drop databases that went away with the container.
	if err := o.openDatastores(ctx, s, true); err != nil {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "datastore", ID: envID, Reason: err.Error(),
		})
		return
	}
	for _, h := range s.stores {
		if err := h.prov.Destroy(ctx, provider.Branch{EnvID: envID}); err != nil {
			td.Pending = append(td.Pending, provider.PendingResource{
				Kind: "datastore/" + h.decl.Name, ID: envID, Reason: err.Error(),
			})
			continue
		}
		td.Removed++
		o.progress("removed the " + h.decl.Name + " datastore branch")
	}
}

// datastoreDeleters registers the compensating delete for a store's branch.
//
// Destroying a branch twice must succeed, which the datastore conformance
// suite checks, so this is safe to run after the ordinary teardown has already
// destroyed it.
func (o *Orchestrator) datastoreDeleters(reg *journal.Registry, s *session) *journal.Registry {
	for _, h := range s.stores {
		h := h
		reg.Register(h.prov.Name(), journal.KindDatastoreBranch,
			journal.DeleterFunc(func(ctx context.Context, rec journal.Record) error {
				return h.prov.Destroy(ctx, provider.Branch{
					EnvID: rec.Env, ProviderRef: rec.ExternalID,
				})
			}))
	}
	return reg
}

// attachDatastores puts every local store on the environment's network and
// rewrites its address to the name a service resolves it by.
//
// The same move the database gets, arriving for the second store. A datastore
// that is not a container on this machine is left alone: its address already
// works from wherever the services run, which is what the provider's own
// interface says and this is where that becomes true rather than a comment.
func (o *Orchestrator) attachDatastores(
	ctx context.Context, s *session, vars map[string]secrets.Value,
	recordIntent func(kind, id string) error,
) (map[string]secrets.Value, error) {
	onThisMachine := 0
	for _, h := range s.stores {
		if h.server != nil {
			onThisMachine++
		}
	}
	if onThisMachine == 0 {
		return vars, nil
	}
	if !s.runtime.Capabilities().AttachesLocalDatabase {
		// A store that is a container on this machine and a runtime that is
		// not on this machine. Every service would come up holding an address
		// inside a daemon its cluster cannot route to, and the failure would
		// arrive later as an application that cannot reach its analytics
		// store, which sends people looking at their own code.
		return nil, aferrors.Coded(aferrors.AFRUN044, "detail", fmt.Sprintf(
			"the datastores are containers on this machine and the %s runtime cannot reach "+
				"them. Bring them up on a store the environment can already reach",
			s.runtime.Name()))
	}
	networked, ok := s.runtime.(interface {
		EnsureNetworks(context.Context, string, func(string, string) error) (string, error)
		AttachStore(context.Context, local.Attachable, string, string, string, secrets.Value) (secrets.Value, error)
	})
	if !ok {
		return nil, aferrors.Coded(aferrors.AFRUN044, "detail", fmt.Sprintf(
			"the %s runtime declares that it attaches local stores and does not implement it",
			s.runtime.Name()))
	}
	// Idempotent, and called again here rather than threaded down from the
	// database's attach, because a project may branch its database at a cloud
	// provider and still hold its events on this machine. The network is the
	// environment's either way.
	networkID, err := networked.EnsureNetworks(ctx, o.envID, recordIntent)
	if err != nil {
		return nil, err
	}

	out := make(map[string]secrets.Value, len(vars))
	for name, value := range vars {
		out[name] = value
	}
	for _, h := range s.stores {
		if h.server == nil {
			continue
		}
		name := DatastoreURLVar(h.decl.Name)
		host, ok := out[name]
		if !ok {
			continue
		}
		inside, err := networked.AttachStore(
			ctx, h.server, h.server.ContainerRef, networkID, h.decl.Name, host)
		if err != nil {
			return nil, err
		}
		o.opts.Redactor.Register(inside.Reveal())
		out[name] = inside
	}
	return out, nil
}

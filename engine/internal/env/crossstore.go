package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/crossstore"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The cross store check, reachable at last.
//
// masking.CrossStoreCheck was written with the dialects and had zero
// production callers: every caller was a test or a benchmark, "cross store"
// appeared nowhere in the command reference, and no tool mentioned it. So
// "the same person is masked identically in both stores" was verified in our
// continuous integration, on our fixtures, and for a customer it was a
// sentence somebody said. This is the wiring that turns it into a command they
// can run against their own two stores and get their own number.
//
// It reads catalogs and no rows, which is what makes it safe to point at
// production: the check masks probe values of its own through both stores'
// rules and compares the outputs.

// CrossStoreResult is what the check found, with the stores it was given.
type CrossStoreResult struct {
	Report crossstore.Report
	// Declared is every datastore the manifest declares, whether or not it
	// named a source. Reported so the answer can say which stores were left
	// out and why, rather than quietly comparing the two that were easy.
	Declared []string
	// WithoutSource names the declared datastores that name no
	// source_url_env, so nothing here could read their schema at all. It is
	// the list somebody has to act on to make the number cover their whole
	// stack.
	WithoutSource []string
}

// CrossStoreCheck compares what every declared datastore's rules do to the
// same identifier.
//
// A datastore that names no source_url_env is not an error and is not skipped
// silently: it is named in WithoutSource, because a check that quietly
// compared two of five stores and reported a hundred percent would be exactly
// the instrument this repository keeps finding in itself.
func (o *Orchestrator) CrossStoreCheck(ctx context.Context) (*CrossStoreResult, error) {
	// The local state and NOTHING else. The key is the only thing this check
	// needs from the machine it runs on, and openReading would also build a
	// database provider: on Docker that means a daemon. This check reads two
	// remote catalogs and no local environment, so a customer running it in
	// continuous integration against their own two stores must not be told
	// that a container runtime is unreachable.
	s, err := o.openForKey(ctx)
	if err != nil {
		return nil, err
	}
	defer s.close()
	return o.crossStoreCheck(ctx, s)
}

// crossStoreCheck is the work, taking a session that is already open.
//
// The session is a parameter rather than opened here because the fidelity
// inventory calls this with one it already holds. Opening a second handle on
// the state database would be a second writer on a file that tolerates one,
// and MaskingKey writes when no key has been generated yet, so the first run
// on a fresh checkout is exactly when the two would meet.
func (o *Orchestrator) crossStoreCheck(ctx context.Context, s *session) (*CrossStoreResult, error) {
	if o.opts.Manifest == nil {
		return nil, aferrors.Coded(aferrors.AFMSK010,
			"detail", "the cross store check reads the manifest's datastores and there is no manifest")
	}
	rules, hash, err := o.rules()
	if err != nil {
		return nil, err
	}
	key, err := o.MaskingKey(ctx, s)
	if err != nil {
		return nil, err
	}

	res, stores, err := crossStoreStores(o.opts.Manifest,
		func(name string) (secrets.Value, error) { return o.lookupStoreURL(ctx, name) })
	if err != nil {
		return nil, err
	}

	report, err := crossstore.Check(ctx, crossstore.Request{
		Key: key, Rules: rules, RulesHash: hash, Stores: stores,
	})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
	}
	res.Report = report
	return res, nil
}

// crossStoreStores turns the manifest's datastores into the stores to compare,
// and names the ones it could not turn into anything.
//
// A pure function of the manifest and a lookup, so the two decisions in it can
// be checked without a database, a daemon or a masking key. Both have an
// obvious wrong answer that looks like a right one: dropping a store that
// names no source would let a check over two of five stores report a hundred
// percent, and reporting the primary second would put the store every reader
// thinks of first at the end of every sentence.
func crossStoreStores(
	m *schema.Manifest, lookup func(string) (secrets.Value, error),
) (*CrossStoreResult, []crossstore.Store, error) {
	res := &CrossStoreResult{}
	var stores []crossstore.Store
	// The primary first, whatever order the file put it in. Normalization
	// APPENDS the entry database: turns into, so a manifest that declares a
	// ClickHouse would otherwise produce "across events and primary".
	for _, ds := range primaryFirst(m.Datastores) {
		res.Declared = append(res.Declared, ds.Name)
		if ds.SourceURLEnv == "" {
			// Named rather than skipped. A store nothing could read is a hole
			// in the number, and a check that quietly compared the two stores
			// that were easy would be the instrument this repository keeps
			// finding in itself.
			res.WithoutSource = append(res.WithoutSource, ds.Name)
			continue
		}
		url, err := lookup(ds.SourceURLEnv)
		if err != nil {
			return nil, nil, err
		}
		stores = append(stores, crossstore.Store{
			Name: ds.Name, Engine: engineOf(ds), Var: ds.SourceURLEnv, URL: url,
		})
	}
	return res, stores, nil
}

// primaryFirst returns the datastores with the primary at the front.
func primaryFirst(stores []schema.Datastore) []schema.Datastore {
	out := make([]schema.Datastore, 0, len(stores))
	for _, ds := range stores {
		if ds.Name == schema.PrimaryDatastore {
			out = append(out, ds)
		}
	}
	for _, ds := range stores {
		if ds.Name != schema.PrimaryDatastore {
			out = append(out, ds)
		}
	}
	return out
}

// engineOf is the engine a datastore runs, with the primary's default filled
// in. Normalization already does this, and it is repeated here because a
// manifest built in a test rather than parsed has not been through it.
func engineOf(ds schema.Datastore) string {
	if ds.Engine != "" {
		return ds.Engine
	}
	if ds.Name == schema.PrimaryDatastore {
		return "postgres"
	}
	return ""
}

// openForKey opens the local state, which is where a generated masking key is
// kept, and opens nothing else.
func (o *Orchestrator) openForKey(ctx context.Context) (*session, error) {
	s := &session{}
	stateDir := filepath.Join(o.opts.Root, StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	var err error
	if s.db, err = state.Open(ctx, stateDir); err != nil {
		return nil, err
	}
	return s, nil
}

// lookupStoreURL resolves a variable name through the same chain the source
// connection string is read through.
//
// An unset variable comes back as the zero value rather than as an error, and
// that is the difference from lookupSecret, which refuses one. Here the
// absence is a fact the report has to carry: the store is named, the reason is
// named, and the run continues so the stores that ARE reachable still produce
// a number. Refusing would let one unset variable hide whether the two stores
// that matter agree.
//
// A keyring that will not open is reported rather than treated as an unset
// variable, for the reason sourceURL says: calling it unset sends the reader
// to export something they had already stored.
func (o *Orchestrator) lookupStoreURL(ctx context.Context, name string) (secrets.Value, error) {
	value, _, found, err := o.secretChain().Lookup(ctx, name)
	if err != nil {
		return secrets.Value{}, aferrors.Wrap(err, aferrors.AFSEC005,
			"name", name, "detail", err.Error())
	}
	if !found || strings.TrimSpace(value.Reveal()) == "" {
		return secrets.Value{}, nil
	}
	// Registered before it is used, so that it is redacted everywhere rather
	// than everywhere somebody remembered.
	o.opts.Redactor.Register(value.Reveal())
	return value, nil
}

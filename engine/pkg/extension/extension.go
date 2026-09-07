// Package extension is where the community engine lets something else in.
//
// MIT, like the rest of the engine. Nothing here is enterprise code and nothing
// here imports any: these are the sockets, and the enterprise edition is one
// thing that can plug into them. A customer's own build is another.
//
// Every hook has a no-op default, and the no-op is the shipped behaviour. That
// is what makes the community edition complete rather than crippled: with
// nothing registered the engine does exactly what it did before these existed,
// and the community test suite runs unchanged with the hooks present. There is
// a test asserting that.
//
// The hooks are deliberately few and deliberately shaped so that a hook cannot
// weaken anything. A policy hook may refuse an environment and cannot permit
// one the manifest would refuse; an audit sink may observe and cannot alter.
// Anything that could loosen a control from outside the repository would be a
// way to change what an environment masks without review.
package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// EnvironmentRequest is what a policy hook is asked about, before anything is
// created.
type EnvironmentRequest struct {
	Org        string
	Repository string
	Branch     string
	EnvID      string
	// EgressHosts are the hosts the manifest permits, so a hook can refuse one
	// that organization policy forbids.
	EgressHosts []string
	// EgressModes is the mode each host is permitted in, keyed by host.
	EgressModes map[string]string
	// MaskedColumns is table.column for every column the plan will mask, so a
	// hook can require that a column pattern is covered.
	MaskedColumns []string
	// Provider is the database provider the environment will use.
	Provider string
	// Region is where it will run, when the runtime reports one.
	Region string
}

// PolicyHook may refuse an environment before it is created.
//
// It can only refuse. There is no return value that permits something the
// manifest did not, because a hook that could widen an egress policy would be a
// way to change what an environment can reach without changing the repository,
// which is the one thing this system exists to make impossible.
type PolicyHook interface {
	// Name identifies the hook in the refusal.
	Name() string
	// Check returns an error to refuse. The error reaches the user, so it says
	// which policy refused and what would satisfy it.
	Check(ctx context.Context, req EnvironmentRequest) error
}

// LifecycleEvent is something that happened to an environment, for hooks that
// meter or record.
type LifecycleEvent struct {
	Org        string
	Repository string
	EnvID      string
	Kind       string
	// SizeClass is how big the environment is, for metering.
	SizeClass string
	// Seconds is how long it existed, on a teardown event.
	Seconds float64
}

// LifecycleHook observes environments coming and going.
//
// It cannot refuse anything and it cannot change anything. An error from it is
// recorded and does not stop the lifecycle, because a metering pipeline that is
// down must not prevent an environment from being torn down: that turns a
// billing outage into a resource leak.
type LifecycleHook interface {
	Name() string
	Observe(ctx context.Context, event LifecycleEvent) error
}

// AuditEntry is one recorded action.
type AuditEntry struct {
	Org        string
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Origin     string
	Detail     map[string]any
}

// AuditSink receives audit entries for forwarding.
//
// Observation only. The primary audit log is written regardless of what any
// sink does, so a sink that is unreachable loses forwarding and never loses the
// entry.
type AuditSink interface {
	Name() string
	Write(ctx context.Context, entry AuditEntry) error
}

// SecretSource is somewhere else a declared variable can be looked up.
//
// The community engine already looks in this shell's environment, in .env, in
// the encrypted local store, and in the system keyring. An organization that
// keeps its credentials in Vault or in a cloud secret manager wants one more
// place, and wants it without every developer copying values onto their laptop
// first.
//
// The signature is deliberately made of nothing but standard library types.
// The engine's own Source interface lives in engine/internal/secrets and takes
// a secrets.Value, and an internal package is by construction unimportable from
// outside engine/..., which is what keeps the enterprise module a separate
// module rather than a directory with a build tag. So the value crosses this
// boundary as a plain string and the engine wraps it in a redacting Value the
// moment it arrives, in exactly one place, the same way it already wraps the
// raw strings it reads out of the environment and out of a .env file.
//
// A source added here is asked last, after everything local. That is the same
// ordering rule the built-in sources follow, most specific first: an export
// somebody typed is for this run, a file is for this repository, and a company
// secret manager is the default that both of those exist to override. A source
// asked first would make "try it with a different key" impossible.
type SecretSource interface {
	// Name identifies the source in an audit record and in the message listing
	// where a missing variable could have been, in the words somebody would
	// use: "HashiCorp Vault at vault.internal", not "vaultsource".
	Name() string
	// Available reports whether the source can be used, and why not when it
	// cannot. The reason is not decoration: AF-SEC-001 prints every source that
	// was considered along with why each did not answer, so a source that
	// returns false with an empty reason turns "the token expired" into
	// "not present" and sends somebody looking in the wrong place.
	//
	// It takes a context because the answer can depend on one. A licence can
	// lapse while the process is running, and a feature that checked its
	// entitlement once at registration would keep working for an organization
	// that stopped paying and could not be turned off without a restart.
	Available(ctx context.Context) (bool, string)
	// Lookup returns the value for a name.
	//
	// Found is separate from the value because a variable that exists and is
	// empty is a different thing from one that does not exist, and only one of
	// them should stop the search. An error is separate from both: a source
	// that cannot be reached must not silently fall through to a lower priority
	// source, because handing the application yesterday's value from somewhere
	// else is the worst available way for this to break.
	Lookup(ctx context.Context, name string) (value string, found bool, err error)
}

// CredentialRejectedError reports that a store refused the credential rather
// than refusing the request.
//
// A distinct type because the engine renders it as a distinct error. A store
// that is unreachable is a transient problem worth retrying; a store that says
// the credential is not valid is a configuration problem, and telling somebody
// to try again is telling them to wait for something that will not change.
//
// "After one refresh" is part of the claim and part of the contract. Every
// cloud store here authenticates with a token that expires, so a long-lived
// process will eventually present a stale one, and one renewal covers that
// entirely. A second rejection is a credential that has been revoked or was
// never right. A source that returns this without having tried a refresh, where
// a refresh was possible, is making a statement that is not true.
type CredentialRejectedError struct {
	// Source names the store, as Name does, so the message says which of an
	// organization's several credentials to rotate.
	Source string
	// Detail is what the store said, for the operator reading the log.
	Detail string
	// Err is the underlying failure, for errors.Is.
	Err error
}

func (e *CredentialRejectedError) Error() string {
	if e.Detail == "" {
		return e.Source + " rejected the credential"
	}
	return e.Source + " rejected the credential: " + e.Detail
}

func (e *CredentialRejectedError) Unwrap() error { return e.Err }

// ---------------------------------------------------------------------------
// Providers, stores and emulators.
//
// The four hooks above observe or refuse. The five sockets below SUPPLY: they
// are how a build outside this repository adds a database provider, a second
// datastore, a runtime, a place to publish goldens, or an emulated third party
// service, without forking the engine.
//
// Before these existed the engine chose every one of those from a switch with
// a default that refused, so the only way to add a provider was to edit
// engine/internal/env, which is unimportable from outside the module and is
// this repository's own source. "Extensible" meant "send us a patch".
//
// The same rule the hooks follow holds here and is the reason each socket is
// consulted AFTER the built-in switch rather than instead of it: a
// registration adds a choice and can never replace one. A registration whose
// name is a built-in provider's name is refused rather than ignored, because
// silently ignoring it is how somebody ships a build they believe overrides
// the Docker provider and does not.
//
// A registered provider is subject to every check a built-in one is. Nothing
// here is a way around masking, verification, provenance or the egress policy:
// those live in the orchestrator, above the provider, and a provider is asked
// for a database rather than trusted to say whether one is safe.

// DatabaseConfig is what a registered database provider is given when a
// manifest names it.
//
// The manifest block arrives as a COPY rather than a pointer. A provider that
// could edit the manifest could change what an environment masks after
// validation had already passed on it, and the engine reads these fields again
// after the provider has been built.
type DatabaseConfig struct {
	// Root is the repository root, so a refusal can name the manifest that
	// asked for this provider rather than describing it.
	Root string
	// Database is the manifest's database block, including Provider, Project,
	// APIKeyEnv and MaxBranches.
	Database schema.Database
	// Version is the resolved Postgres major version, which is the manifest's
	// value or the engine's default. Resolved here rather than in the provider
	// so that two providers cannot disagree about what "unset" means.
	Version int
	// StateDir is where the provider may keep local files. It belongs to this
	// repository's checkout, so a provider writing here does not leak state
	// between projects on one machine.
	StateDir string
	// Now is the engine's clock. Library code in the engine never calls
	// time.Now, so that TTLs and retries are testable without waiting for wall
	// time, and a provider given this inherits that.
	Now func() time.Time
	// Lookup resolves a declared credential through the engine's whole chain:
	// this shell's environment, .env, the encrypted local store, the keyring,
	// and any registered SecretSource, in that order.
	//
	// Providers do not read the process environment themselves. That is what
	// makes every credential a provider uses declared and auditable, and it is
	// why found and err are separate returns: a variable that exists and is
	// empty is not the same as one that is absent, and a store that could not
	// be reached is neither.
	Lookup func(ctx context.Context, name string) (secret.Value, bool, error)
}

// DatabaseProvider builds the database provider a manifest names.
//
// Name is the value database.provider takes. Open is called once per command,
// and the provider it returns is closed when the command finishes.
type DatabaseProvider interface {
	// Name is the value database.provider takes in a manifest.
	Name() string
	// Open builds the provider. Returning a nil provider and a nil error is a
	// contract violation the engine reports rather than dereferences.
	Open(ctx context.Context, cfg DatabaseConfig) (provider.Database, error)
}

// DatastoreConfig is what a registered datastore provider is given.
//
// Settings rather than a manifest block, because the manifest has no
// datastores list yet. When it gains one this grows a typed field beside
// Settings rather than replacing it, so a provider written against this keeps
// compiling.
type DatastoreConfig struct {
	// Root is the repository root.
	Root string
	// Name is the datastore's name in the environment, such as "events".
	Name string
	// StateDir is where the provider may keep local files.
	StateDir string
	// Settings carries provider specific values.
	Settings map[string]string
	// Now is the engine's clock.
	Now func() time.Time
	// Lookup resolves a declared credential, as DatabaseConfig.Lookup does.
	Lookup func(ctx context.Context, name string) (secret.Value, bool, error)
}

// DatastoreCaps declares what a datastore can do, so that a suite skips a
// behavior by name rather than passing one it never ran.
type DatastoreCaps struct {
	// Engine is the datastore engine, such as "clickhouse" or "redis".
	Engine string
	// Branching reports whether an environment can get its own copy.
	Branching bool
	// Golden reports whether the store can hold a masked, verified copy at
	// all. A cache that is correct to start empty declares this false, and
	// declaring it false is a legitimate answer rather than a missing feature.
	Golden bool
	// CopyOnWrite reports whether a branch shares storage with its golden, and
	// therefore whether branch time is independent of size.
	CopyOnWrite bool
}

// Datastore is a store other than the environment's primary Postgres.
//
// Deliberately smaller than provider.Database. A second datastore has no
// pooled endpoint, no reset, and no golden pool of its own to enumerate; what
// it has is a copy per environment and a way to say how faithful that copy is.
//
// It lives here rather than in engine/pkg/provider because the engine has no
// datastore lifecycle yet: nothing in the manifest declares one and nothing in
// the orchestrator branches one. When that lands the interface moves to
// engine/pkg/provider beside Database and an alias stays here, the way
// engine/internal/secrets keeps the name secrets.Value for a type that lives
// in engine/pkg/secret. Until then, REGISTERING ONE DOES NOTHING BEYOND
// APPEARING IN af license status: the registry holds it and no lifecycle asks
// for it. That is said plainly because a socket nothing consults looks exactly
// like a working feature from the outside, which is how this repository
// shipped an audit sink that forwarded nothing.
type Datastore interface {
	// Name identifies the datastore in output and in errors.
	Name() string
	// Capabilities declares what this datastore can do.
	Capabilities() DatastoreCaps
	// RefreshGolden builds a new masked, verified copy. A datastore that
	// declares Golden false returns ErrNoGolden.
	RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error)
	// Branch creates this environment's copy. Calling it twice with the same
	// environment identifier returns the same branch, the same idempotency
	// contract provider.Database has and for the same reason: the engine
	// retries after timeouts and a retry that creates a second resource is how
	// an orphan is made.
	Branch(ctx context.Context, version string, envID string) (provider.Branch, error)
	// Destroy removes a branch. Removing one that is already gone succeeds.
	Destroy(ctx context.Context, b provider.Branch) error
	// ConnString returns how to reach a branch, as a value that renders as
	// [redacted] everywhere text is produced.
	ConnString(ctx context.Context, b provider.Branch) (secret.Value, error)
	// Inventory lists everything this datastore holds, which the leak detector
	// compares against the journal.
	Inventory(ctx context.Context) ([]provider.Resource, error)
	// Health reports whether a branch is reachable.
	Health(ctx context.Context, b provider.Branch) (provider.Health, error)
	// Close releases the datastore's own resources.
	Close() error
}

// ErrNoGolden is returned by a datastore that holds no golden, which is a
// declared stance rather than a failure. A cache is rebuilt from the primary
// and a copy of one would be noise.
var ErrNoGolden = errors.New("extension: this datastore holds no golden")

// DatastoreProvider builds a datastore.
type DatastoreProvider interface {
	// Name is the value a manifest names this provider by.
	Name() string
	// Engine is the datastore engine it implements, such as "clickhouse". Two
	// providers may implement one engine, which is why this is separate from
	// Name.
	Engine() string
	// Open builds the datastore.
	Open(ctx context.Context, cfg DatastoreConfig) (Datastore, error)
}

// RuntimeConfig is what a registered runtime is given.
type RuntimeConfig struct {
	// Root is the repository root.
	Root string
	// Runtime is the manifest's runtime block, as a copy.
	Runtime schema.Runtime
	// TTL is how long an environment this runtime creates may live, already
	// parsed from the manifest. Zero means no expiry.
	TTL time.Duration
	// StateDir is where the runtime may keep local files.
	StateDir string
	// Now is the engine's clock.
	Now func() time.Time
	// Lookup resolves a declared credential, as DatabaseConfig.Lookup does.
	Lookup func(ctx context.Context, name string) (secret.Value, bool, error)
	// ResolveProxyImage produces the egress sidecar image, building it on a
	// local container daemon or reading AF_PROXY_IMAGE.
	//
	// It is here because a runtime that places containers somewhere other than
	// this machine cannot build the sidecar and must not run without it: an
	// environment with no sidecar has no egress policy at all, which is the
	// one thing this product refuses. It is a function rather than a string so
	// that a command which only reads, such as af status, does not pay for a
	// container build to answer a question.
	ResolveProxyImage func(ctx context.Context) (string, error)
}

// RuntimeProvider builds the runtime a manifest names.
type RuntimeProvider interface {
	// Name is the value runtime.provider takes in a manifest.
	Name() string
	// Open builds the runtime.
	Open(ctx context.Context, cfg RuntimeConfig) (provider.Runtime, error)
}

// ObjectStoreConfig is what a registered golden store is opened with.
type ObjectStoreConfig struct {
	// URL is database.golden.storage_url, with any $VARIABLE reference already
	// resolved from the environment. A store never sees the variable name,
	// only the value, which is what keeps a credential out of the manifest.
	URL string
	// Getenv reads the environment for anything else the store needs, such as
	// the credential an S3 URL does not carry.
	Getenv func(string) string
}

// StoredObject is one thing in a store.
type StoredObject struct {
	Name     string
	Size     int64
	Modified time.Time
}

// ObjectStore is where a golden's dump and its attestation live when they live
// somewhere other than the machine that made them.
//
// engine/internal/golden.Store is an alias of this type, so a store registered
// here IS the store the engine uses, with no adapter in between and no second
// definition to drift.
type ObjectStore interface {
	// Name identifies the store in a message.
	Name() string
	// Put writes an object, replacing one of the same name.
	Put(ctx context.Context, name string, size int64, body io.Reader) error
	// Get opens an object. A name that is not there returns
	// ErrObjectNotFound, so that a caller can tell "no golden published yet"
	// from "the store is broken", which are the same HTTP status on more than
	// one service.
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	// List returns the objects under a prefix.
	List(ctx context.Context, prefix string) ([]StoredObject, error)
	// Delete removes an object. Removing one that is not there succeeds,
	// because a retry after a timeout must not fail on the work it already did.
	Delete(ctx context.Context, name string) error
}

// ErrObjectNotFound is what an ObjectStore returns for an object that is not
// there.
//
// It lives here rather than in the engine's golden package because a store
// written outside this repository cannot import an internal package, and a
// sentinel a store cannot name is a sentinel it cannot return: every "no
// golden published yet" from such a store would read as "the store is broken".
// engine/internal/golden.ErrNotFound is this value.
var ErrObjectNotFound = errors.New("golden: no such object")

// GoldenStore opens the object store a manifest names.
type GoldenStore interface {
	// Name is the value database.golden.storage takes in a manifest.
	Name() string
	// Open builds the store for a storage URL.
	Open(cfg ObjectStoreConfig) (ObjectStore, error)
}

// EmulatorContainer describes the container an emulator runs in.
type EmulatorContainer struct {
	// Image is the container image, PINNED BY DIGEST. A tag is refused,
	// because an emulator is the thing answering for production's API and a
	// tag that moves changes what an environment was tested against without
	// anything in the repository changing.
	Image string
	// Port is the port inside the container the sidecar forwards to.
	Port int
	// Env is what the container is started with.
	Env map[string]string
}

// Emulator is a third party service answered inside the environment.
//
// It is a declaration rather than an implementation on purpose. Antifailure
// does not write emulators: LocalStack, Azurite and the vendors' own emulators
// exist and carry years of fidelity work that a hand written replacement would
// not have. What the engine adds is that the application needs no endpoint
// override to reach one, and that is routing, which lives in the sidecar.
//
// The routing is not built yet, so REGISTERING AN EMULATOR TODAY DOES NOTHING
// BEYOND APPEARING IN af license status AND BEING VALIDATED. Said plainly for
// the same reason it is said on Datastore.
type Emulator interface {
	// Name is the value an egress rule will name this emulator by.
	Name() string
	// Hosts are the hostnames it answers for, such as s3.amazonaws.com.
	Hosts() []string
	// Container describes what the engine starts.
	Container() EmulatorContainer
}

// Registry holds what has been registered.
//
// A value rather than only a package-level singleton, so that a test can build
// one and so that two engines in one process do not share hooks.
type Registry struct {
	mu        sync.RWMutex
	policy    []PolicyHook
	lifecycle []LifecycleHook
	audit     []AuditSink
	secrets   []SecretSource

	databases  []DatabaseProvider
	datastores []DatastoreProvider
	runtimes   []RuntimeProvider
	stores     []GoldenStore
	emulators  []Emulator
}

// NewRegistry returns an empty registry, which is the community behaviour.
func NewRegistry() *Registry { return &Registry{} }

// Default is the registry the engine consults when none is supplied.
var Default = NewRegistry()

// AddPolicy registers a hook that may refuse an environment.
func (r *Registry) AddPolicy(h PolicyHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = append(r.policy, h)
}

// AddLifecycle registers a hook that observes environments.
func (r *Registry) AddLifecycle(h LifecycleHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lifecycle = append(r.lifecycle, h)
}

// AddAuditSink registers a sink that forwards audit entries.
func (r *Registry) AddAuditSink(s AuditSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audit = append(r.audit, s)
}

// AddSecretSource registers somewhere else a declared variable can be found.
//
// Order is registration order, and sources registered here are asked after
// every built-in one. Two enterprise sources registered together are asked in
// the order they were added, so an organization running both Vault and a cloud
// secret manager gets a deterministic answer rather than whichever map the
// engine happened to iterate first.
func (r *Registry) AddSecretSource(s SecretSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets = append(r.secrets, s)
}

// SecretSources returns the registered sources in the order they are asked.
//
// With nothing registered this returns nil and the engine's lookup chain is
// byte for byte the chain it was before this hook existed, which is the
// property the whole package is built around.
func (r *Registry) SecretSources() []SecretSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]SecretSource(nil), r.secrets...)
}

// CheckPolicy runs every policy hook and returns the first refusal.
//
// With nothing registered it returns nil, which is the community behaviour and
// the reason the community suite passes unchanged with these calls in place.
//
// Hooks run in registration order and the first refusal wins, so a refusal is
// reproducible. Stopping at the first also means an environment violating three
// policies names one of them, which is the right amount for somebody who has to
// fix them one at a time.
func (r *Registry) CheckPolicy(ctx context.Context, req EnvironmentRequest) error {
	r.mu.RLock()
	hooks := append([]PolicyHook(nil), r.policy...)
	r.mu.RUnlock()

	for _, h := range hooks {
		if err := h.Check(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// Observe reports a lifecycle event to every hook, collecting failures.
//
// It never stops early and never returns an error a caller is expected to act
// on. A metering hook that is down must not prevent a teardown.
func (r *Registry) Observe(ctx context.Context, event LifecycleEvent) []error {
	r.mu.RLock()
	hooks := append([]LifecycleHook(nil), r.lifecycle...)
	r.mu.RUnlock()

	var problems []error
	for _, h := range hooks {
		if err := h.Observe(ctx, event); err != nil {
			problems = append(problems, err)
		}
	}
	return problems
}

// Audit forwards an entry to every sink, collecting failures.
func (r *Registry) Audit(ctx context.Context, entry AuditEntry) []error {
	r.mu.RLock()
	sinks := append([]AuditSink(nil), r.audit...)
	r.mu.RUnlock()

	var problems []error
	for _, s := range sinks {
		if err := s.Write(ctx, entry); err != nil {
			problems = append(problems, err)
		}
	}
	return problems
}

// AddDatabaseProvider registers a database provider a manifest can name.
//
// Registration order is retained and a name may be registered once. The engine
// consults this after its own built-in providers, so a registration cannot
// take over a name the engine already has; Validate reports that collision
// rather than leaving it to be discovered as a provider that was quietly not
// used.
func (r *Registry) AddDatabaseProvider(p DatabaseProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.databases = append(r.databases, p)
}

// DatabaseProviderNamed returns the provider registered under a name.
func (r *Registry) DatabaseProviderNamed(name string) (DatabaseProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.databases {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// DatabaseProviderNames lists what is registered, for a refusal that says what
// this build does have.
func (r *Registry) DatabaseProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.databaseNames()
}

func (r *Registry) databaseNames() []string {
	out := make([]string, 0, len(r.databases))
	for _, p := range r.databases {
		out = append(out, p.Name())
	}
	return out
}

// AddDatastoreProvider registers a provider for a store other than the
// environment's primary Postgres.
func (r *Registry) AddDatastoreProvider(p DatastoreProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.datastores = append(r.datastores, p)
}

// DatastoreProviderNamed returns the datastore provider registered under a name.
func (r *Registry) DatastoreProviderNamed(name string) (DatastoreProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.datastores {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// DatastoreProviderNames lists what is registered.
func (r *Registry) DatastoreProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.datastoreNames()
}

func (r *Registry) datastoreNames() []string {
	out := make([]string, 0, len(r.datastores))
	for _, p := range r.datastores {
		out = append(out, p.Name())
	}
	return out
}

// AddRuntimeProvider registers a runtime a manifest can name.
func (r *Registry) AddRuntimeProvider(p RuntimeProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes = append(r.runtimes, p)
}

// RuntimeProviderNamed returns the runtime registered under a name.
func (r *Registry) RuntimeProviderNamed(name string) (RuntimeProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.runtimes {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// RuntimeProviderNames lists what is registered.
func (r *Registry) RuntimeProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runtimeNames()
}

func (r *Registry) runtimeNames() []string {
	out := make([]string, 0, len(r.runtimes))
	for _, p := range r.runtimes {
		out = append(out, p.Name())
	}
	return out
}

// AddGoldenStore registers somewhere else a published golden can live.
func (r *Registry) AddGoldenStore(s GoldenStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stores = append(r.stores, s)
}

// GoldenStoreNamed returns the store registered under a name.
func (r *Registry) GoldenStoreNamed(name string) (GoldenStore, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.stores {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// GoldenStoreNames lists what is registered.
func (r *Registry) GoldenStoreNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.storeNames()
}

func (r *Registry) storeNames() []string {
	out := make([]string, 0, len(r.stores))
	for _, s := range r.stores {
		out = append(out, s.Name())
	}
	return out
}

// AddEmulator registers a third party service answered inside the environment.
func (r *Registry) AddEmulator(e Emulator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emulators = append(r.emulators, e)
}

// EmulatorNamed returns the emulator registered under a name.
func (r *Registry) EmulatorNamed(name string) (Emulator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.emulators {
		if e.Name() == name {
			return e, true
		}
	}
	return nil, false
}

// EmulatorNames lists what is registered.
func (r *Registry) EmulatorNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.emulatorNames()
}

func (r *Registry) emulatorNames() []string {
	out := make([]string, 0, len(r.emulators))
	for _, e := range r.emulators {
		out = append(out, e.Name())
	}
	return out
}

// The socket names Validate keys its reserved list by.
//
// Constants rather than the literals, because the caller supplying the
// reserved names is the engine and the two sides agreeing on the spelling is
// the whole of the check. A key typed differently on one side would not fail:
// the reserved list would simply match nothing, and a registration that
// shadows a built in provider would go back to being silently unused, which is
// exactly the failure this refuses. They are the words a refusal is written
// in, so they read as prose rather than as identifiers.
const (
	SocketDatabaseProvider  = "database provider"
	SocketDatastoreProvider = "datastore provider"
	SocketRuntimeProvider   = "runtime"
	SocketGoldenStore       = "golden store"
	SocketEmulator          = "emulator"
)

// Validate reports a registration the engine cannot honor.
//
// It runs before an environment is created, which is the only useful moment: a
// build that registered a provider with no name, two providers under one name,
// or an emulator image pinned by a tag has a configuration defect, and finding
// it at the first af up is finding it before anybody depends on the answer.
//
// Reserved are the names the engine already has, which the caller supplies
// because the engine owns that list and this package must not have to know it.
// A registration under a reserved name is refused rather than ignored: the
// engine consults the registry only after its own switch, so an ignored
// registration is a build somebody believes overrides the Docker provider and
// which silently does not.
func (r *Registry) Validate(reserved map[string][]string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	kinds := []struct {
		socket string
		names  []string
	}{
		{SocketDatabaseProvider, r.databaseNames()},
		{SocketDatastoreProvider, r.datastoreNames()},
		{SocketRuntimeProvider, r.runtimeNames()},
		{SocketGoldenStore, r.storeNames()},
		{SocketEmulator, r.emulatorNames()},
	}
	for _, kind := range kinds {
		seen := make(map[string]bool, len(kind.names))
		for _, name := range kind.names {
			switch {
			case strings.TrimSpace(name) == "":
				return fmt.Errorf(
					"a %s is registered with no name, so nothing in a manifest could ask for it",
					kind.socket)
			case seen[name]:
				return fmt.Errorf(
					"two %ss are registered as %q, and a manifest naming it would get "+
						"whichever was registered first", kind.socket, name)
			}
			seen[name] = true
			for _, taken := range reserved[kind.socket] {
				if name == taken {
					return fmt.Errorf(
						"a %s is registered as %q and this build already has one by that "+
							"name. A registration adds a choice and cannot replace one, so "+
							"this registration would never be used; give it another name",
						kind.socket, name)
				}
			}
		}
	}

	for _, e := range r.emulators {
		c := e.Container()
		if len(e.Hosts()) == 0 {
			return fmt.Errorf(
				"the emulator %q answers for no hosts, so no request could ever reach it",
				e.Name())
		}
		if !strings.Contains(c.Image, "@sha256:") {
			return fmt.Errorf(
				"the emulator %q is pinned by %q rather than by digest. An emulator answers "+
					"for a production API, and a tag that moves changes what an environment "+
					"was tested against with nothing in the repository changing",
				e.Name(), c.Image)
		}
	}
	return nil
}

// Registered names what is plugged in, for af version and af doctor.
//
// Worth printing: an operator debugging why an environment was refused needs to
// know a policy hook exists at all, and "nothing registered" is itself the
// answer to most of those questions.
func (r *Registry) Registered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []string
	for _, h := range r.policy {
		out = append(out, "policy:"+h.Name())
	}
	for _, h := range r.lifecycle {
		out = append(out, "lifecycle:"+h.Name())
	}
	for _, s := range r.audit {
		out = append(out, "audit:"+s.Name())
	}
	for _, s := range r.secrets {
		out = append(out, "secret-source:"+s.Name())
	}
	for _, p := range r.databases {
		out = append(out, "database-provider:"+p.Name())
	}
	for _, p := range r.datastores {
		out = append(out, "datastore-provider:"+p.Name())
	}
	for _, p := range r.runtimes {
		out = append(out, "runtime:"+p.Name())
	}
	for _, st := range r.stores {
		out = append(out, "golden-store:"+st.Name())
	}
	for _, e := range r.emulators {
		out = append(out, "emulator:"+e.Name())
	}
	sort.Strings(out)
	return out
}

// Empty reports whether anything is registered, which is the community case.
func (r *Registry) Empty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.policy) == 0 && len(r.lifecycle) == 0 &&
		len(r.audit) == 0 && len(r.secrets) == 0 &&
		len(r.databases) == 0 && len(r.datastores) == 0 &&
		len(r.runtimes) == 0 && len(r.stores) == 0 && len(r.emulators) == 0
}

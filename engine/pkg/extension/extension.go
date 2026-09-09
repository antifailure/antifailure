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
	// EgressDefault is the mode a host with no rule gets. Empty means block.
	//
	// Separate from EgressModes, and its absence was a hole rather than a
	// simplification. A manifest can reach the whole internet with no rules at
	// all: `egress: {default: allow}` and an empty rule list is valid, the
	// validator only warns about it, and a hook reading only EgressModes sees
	// an empty map and finds nothing to refuse. The organization policy's
	// allowed_modes rule had the same blind spot.
	EgressDefault string
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

// MaskingRequest is what a masking hook is asked about, at the one moment the
// answer is a fact rather than a guess.
//
// It carries columns and EnvironmentRequest deliberately does not, and the
// difference is the whole reason this second request exists. A policy hook is
// asked before anything is created, so that a refusal leaves nothing behind,
// and at that point the engine has read a manifest and has not read a
// database. A manifest does not enumerate columns. Filling a column list there
// would mean expanding the repository's masking rules against the tables the
// manifest happens to name, which is a different set from the tables that
// exist, and a required pattern that matched nothing in that set would refuse a
// repository whose schema satisfies it perfectly.
//
// So the column list is asked for here instead, during a golden refresh, after
// masking.ReadCatalog has read the real schema and after the rules have been
// assigned to it, and before one row has been rewritten. A refusal at this
// point stops the golden being published, and an unpublished golden cannot be
// branched, so no environment can ever hold data that this refused. That is
// later than the policy hook and it is still before the data exists.
type MaskingRequest struct {
	Repository string
	Branch     string
	EnvID      string
	// RulesHash identifies the masking configuration that produced this plan,
	// so a hook can record which rule set it approved.
	RulesHash string
	// MaskedColumns is schema.table.column for every column the plan will
	// rewrite. Read off the plan, so it is the columns that are about to be
	// masked rather than the columns somebody hoped a rule would reach.
	MaskedColumns []string
	// CatalogColumns is schema.table.column for every column the catalogue
	// holds, masked or not.
	//
	// A hook needs both lists and neither on its own is enough. MaskedColumns
	// alone cannot tell "this database has no email column" from "this
	// database has three and none of them is masked", and those deserve
	// opposite answers. With the catalogue a rule about a column that does not
	// exist is answerable as such, and a rule about three columns is not
	// satisfied by masking one of them.
	CatalogColumns []string
}

// MaskingHook may refuse a masking plan before it runs.
//
// It can only refuse, for the same reason PolicyHook can only refuse: there is
// no return value that masks something the rules did not, because a hook that
// could add a transform would be a way to change what a golden contains
// without changing the repository.
type MaskingHook interface {
	// Name identifies the hook in the refusal.
	Name() string
	// CheckMasking returns an error to refuse the plan. The error reaches the
	// user, so it says which policy refused and what would satisfy it.
	CheckMasking(ctx context.Context, req MaskingRequest) error
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
	// OccurredAt is when the action happened, which is not when a sink saw it.
	//
	// A sink forwards over a network that retries, so the instant it stamps is
	// the instant forwarding succeeded and can be minutes later. A security
	// team correlating this entry against anything else needs the first of
	// those, and a stream carrying only the second silently reorders itself
	// whenever one destination is slow. The zero value means the producer did
	// not say, and a sink records that as unknown rather than substituting its
	// own clock, because a guessed timestamp in an audit log is evidence that
	// is wrong rather than evidence that is missing.
	OccurredAt time.Time
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
// It carries the manifest's own entry for the store, in Datastore, and
// Settings beside it for whatever a particular provider needs that the
// manifest does not name. Settings was here first and stays, because removing
// it would break a provider written against the shape that shipped before the
// manifest had a datastores list.
type DatastoreConfig struct {
	// Root is the repository root.
	Root string
	// Name is the datastore's name in the environment, such as "events".
	Name string
	// Datastore is the manifest's entry for this store, as a copy, including
	// the stance somebody declared for it. It is the typed field this struct's
	// comment promised would arrive beside Settings when the manifest gained a
	// datastores list, added rather than replacing Settings so that a provider
	// written against the older shape keeps compiling.
	Datastore schema.Datastore
	// StateDir is where the provider may keep local files.
	StateDir string
	// Settings carries provider specific values.
	Settings map[string]string
	// Now is the engine's clock.
	Now func() time.Time
	// Lookup resolves a declared credential, as DatabaseConfig.Lookup does.
	Lookup func(ctx context.Context, name string) (secret.Value, bool, error)
}

// DatastoreCaps is provider.DatastoreCaps.
//
// The type moved when the manifest gained a datastores list, which is the move
// the comment on the old declaration said would happen. The name stays here so
// that a build registering a datastore against the older engine keeps
// compiling, the way engine/internal/secrets keeps the name secrets.Value for
// a type that lives in engine/pkg/secret.
type DatastoreCaps = provider.DatastoreCaps

// Datastore is provider.Datastore. See DatastoreCaps for why the name is still
// here.
type Datastore = provider.Datastore

// ErrNoGolden is provider.ErrNoGolden. It is the same value rather than a
// second one, so errors.Is answers the same on either name.
var ErrNoGolden = provider.ErrNoGolden

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
	// Egress is the manifest's egress catalogue, as a copy.
	//
	// A runtime that places containers somewhere other than this machine has
	// to build the network the sidecar runs in, and the shape of that network
	// depends on what the environment is allowed to reach: a host declared
	// allow or sandbox is one the sidecar forwards to for real and therefore
	// has to resolve, and a host declared block, mock, capture or synth is
	// answered locally and must NOT resolve, because a name the sidecar
	// answers resolving publicly is a way around the decision the manifest
	// made about it.
	//
	// It is here so a runtime composes with that catalogue rather than
	// carrying a second list of hosts that can disagree with it. The ECS
	// runtime is the first to need it, for the Route 53 Resolver DNS Firewall
	// rule group that is the only thing on AWS able to close the resolver
	// path: security groups and network ACLs cannot filter the Amazon DNS
	// server, so the allow list in that rule group is where the manifest's
	// answer has to end up.
	Egress schema.Egress
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
	// Command replaces the image's own command, and it is empty for an image
	// whose entrypoint is already the emulator.
	//
	// Not a convenience. For Google there is no other way to say which
	// emulator you mean, and the image configs say so rather than a docs page:
	// gcr.io/google.com/cloudsdktool/google-cloud-cli has NO entrypoint and
	// the command bash, and it ships Pub/Sub, Firestore, Datastore and
	// Bigtable, so Image, Port and Env alone describe four identical
	// containers that run a shell and exit.
	//
	// The two Google images that need NO command say the same thing from the
	// other side, and leaving one out is as wrong as leaving one in:
	// fsouza/fake-gcs-server carries the entrypoint /bin/fake-gcs-server
	// -data /data and gcr.io/cloud-spanner-emulator/emulator carries the
	// command ./gateway_main --hostname 0.0.0.0, so a command on either
	// replaces a working entrypoint. All three were read out of the registry
	// rather than taken from documentation.
	//
	// Azurite needs a command for a smaller reason with the same shape: it
	// binds to loopback unless it is told otherwise, and an emulator
	// listening on 127.0.0.1 answers nothing from the sidecar while looking
	// perfectly healthy in its own logs.
	//
	// LocalStack is the reason neither of those was noticed first: it is one
	// image whose entrypoint is the emulator, so it needs none of this. One
	// reference implementation is not a contract.
	//
	// A slice rather than a string, because a string would be run through a
	// shell and an emulator's arguments carry addresses and ports that a
	// shell would be free to reinterpret.
	Command []string
	// Maintainer records who stands behind this image.
	//
	// DECLARED, never inferred from the registry the image happens to sit in.
	// A registry path is a fact about hosting and this is a fact about
	// support, and the two disagree exactly where it matters: fake-gcs-server
	// is the de facto GCS emulator and Google does not publish it, because
	// Google ships no GCS emulator at all. Somebody choosing to trust an
	// environment's answers about object storage should read that from the
	// declaration rather than infer it from a hostname.
	Maintainer EmulatorMaintainer
	// Companions are containers this emulator does not work without.
	//
	// The Azure Service Bus emulator is the case: it refuses to start without
	// an MSSQL container beside it, so an emulator socket that could only
	// describe one container could not describe Service Bus at all. They are
	// started on the INNER network alone, exactly as the emulator is, so they
	// have no route out either and the Reach behaviour covers them without
	// knowing they exist.
	//
	// Each carries its own digest and its own Maintainer, because a companion
	// is a third party image running beside a copy of production data on the
	// same terms as the emulator, and "it came with the emulator" is not a
	// provenance. A companion's own Companions are refused rather than walked:
	// one level is what the known cases need, and a graph here would be a
	// dependency resolver nobody asked for.
	Companions []EmulatorContainer
}

// EmulatorMaintainer says who stands behind an emulator image.
//
// A closed set rather than a free string, for the same reason DatastoreStance
// is: the interesting values are few, the difference between them is what
// somebody is actually deciding, and a string would collect eleven spellings
// of "community" that no report could group.
type EmulatorMaintainer string

const (
	// MaintainerVendor is the cloud provider whose API is being emulated:
	// Microsoft's Azurite and Service Bus emulators, Google's own Pub/Sub and
	// Firestore emulators.
	MaintainerVendor EmulatorMaintainer = "vendor"
	// MaintainerCommercial is a company that is not the vendor and sells or
	// supports the emulator, which is LocalStack.
	MaintainerCommercial EmulatorMaintainer = "commercial"
	// MaintainerCommunity is a project with no company behind it.
	// fsouza/fake-gcs-server is the one that matters, because it is the de
	// facto GCS emulator and Google ships none.
	MaintainerCommunity EmulatorMaintainer = "community"
	// MaintainerFirstParty is an image built in this repository. Nothing here
	// is one today and the value exists so that the day something is, it is
	// not quietly filed as vendor.
	MaintainerFirstParty EmulatorMaintainer = "first_party"
)

// AllEmulatorMaintainers returns every value, in the order a report lists
// them, which runs from the strongest claim of support to the weakest.
func AllEmulatorMaintainers() []EmulatorMaintainer {
	return []EmulatorMaintainer{
		MaintainerFirstParty, MaintainerVendor, MaintainerCommercial, MaintainerCommunity,
	}
}

// Emulator is a third party service answered inside the environment.
//
// It is a declaration rather than an implementation on purpose. Antifailure
// does not write emulators: LocalStack, Azurite and the vendors' own emulators
// exist and carry years of fidelity work that a hand written replacement would
// not have. What the engine adds is that the application needs no endpoint
// override to reach one, and that is routing, which lives in the sidecar.
//
// The routing exists. An egress rule in emulate mode names one of these by
// Name, the engine starts Container() on the environment's inner network,
// which has no route out, and the sidecar answers for every host in Hosts()
// with a certificate the environment already trusts and forwards to the
// container. The application needs no endpoint override, which is the whole
// reason this socket is a declaration rather than an implementation.
//
// What a registration still does NOT get is a covered surface. The sidecar
// forwards every request for a listed host, so an operation the emulator does
// not implement is answered by the emulator's own error rather than by a
// refusal naming the gap. Said plainly for the same reason it was said when
// the routing was missing.
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
	masking   []MaskingHook
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

// AddMasking registers a hook that may refuse a masking plan.
func (r *Registry) AddMasking(h MaskingHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.masking = append(r.masking, h)
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

// CheckMasking runs every masking hook and returns the first refusal.
//
// Registration order and first refusal wins, the same as CheckPolicy, and with
// nothing registered it returns nil after one pass over an empty slice.
func (r *Registry) CheckMasking(ctx context.Context, req MaskingRequest) error {
	r.mu.RLock()
	hooks := append([]MaskingHook(nil), r.masking...)
	r.mu.RUnlock()

	for _, h := range hooks {
		if err := h.CheckMasking(ctx, req); err != nil {
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

func (r *Registry) ReplaceDatabaseProvider(p DatabaseProvider) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.databases {
		if existing.Name() == p.Name() {
			r.databases[i] = p
			return true
		}
	}
	return false
}

// ReplaceDatabaseProvider substitutes the provider registered under a name,
// and reports whether there was one.
//
// It exists for one purpose and this comment names it rather than leaving a
// general mutator on a registry: the enterprise edition puts a licence gate in
// front of every registered cloud provider, and a decorator has to take the
// registration's PLACE rather than sit beside it. Two providers under one name
// are refused by Validate, and if they were not, the engine would use whichever
// was registered first, which is the ungated one.
//
// A name that is not already registered is NOT added, and false says so. Adding
// it would turn a typo inside a decorator into a provider a manifest can name
// and nobody wrote, which is the opposite of what a decorator is for.

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

// ReplaceRuntimeProvider substitutes the runtime registered under a name, and
// reports whether there was one. See ReplaceDatabaseProvider for why this
// exists and why a name that is not registered is not added.
func (r *Registry) ReplaceRuntimeProvider(p RuntimeProvider) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.runtimes {
		if existing.Name() == p.Name() {
			r.runtimes[i] = p
			return true
		}
	}
	return false
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
		if err := validateEmulatorContainer(e.Name(), "", c); err != nil {
			return err
		}
		for _, companion := range c.Companions {
			if len(companion.Companions) > 0 {
				return fmt.Errorf(
					"the emulator %q has a companion %q that declares companions of its own. "+
						"One level is what the known cases need, and a graph here would be a "+
						"dependency resolver nobody asked for",
					e.Name(), companion.Image)
			}
			if err := validateEmulatorContainer(e.Name(), companion.Image, companion); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateEmulatorContainer holds one container to the two rules every image
// running beside a copy of production data has to satisfy.
//
// Companion is the companion's image when this is a companion, and empty for
// the emulator's own container. It is threaded through so a refusal names the
// MSSQL image somebody forgot to pin rather than only the Service Bus emulator
// that pulled it in, which is the difference between a message you can act on
// and one you have to go looking behind.
func validateEmulatorContainer(name, companion string, c EmulatorContainer) error {
	what := fmt.Sprintf("the emulator %q", name)
	if companion != "" {
		what = fmt.Sprintf("the companion %q of the emulator %q", companion, name)
	}
	if !strings.Contains(c.Image, "@sha256:") {
		return fmt.Errorf(
			"%s is pinned by %q rather than by digest. An emulator answers for a production "+
				"API, and a tag that moves changes what an environment was tested against "+
				"with nothing in the repository changing", what, c.Image)
	}
	if !knownMaintainer(c.Maintainer) {
		return fmt.Errorf(
			"%s declares the maintainer %q, and the values are %s. Who stands behind an "+
				"image is declared rather than inferred from the registry it sits in, "+
				"because a registry path is a fact about hosting and this is a fact about "+
				"support: the de facto GCS emulator is community maintained and Google "+
				"ships none at all", what, c.Maintainer, maintainerList())
	}
	return nil
}

func knownMaintainer(m EmulatorMaintainer) bool {
	for _, known := range AllEmulatorMaintainers() {
		if m == known {
			return true
		}
	}
	return false
}

func maintainerList() string {
	all := AllEmulatorMaintainers()
	out := make([]string, 0, len(all))
	for _, m := range all {
		out = append(out, string(m))
	}
	return strings.Join(out, ", ")
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
	for _, h := range r.masking {
		out = append(out, "masking:"+h.Name())
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
	return len(r.policy) == 0 && len(r.masking) == 0 && len(r.lifecycle) == 0 &&
		len(r.audit) == 0 && len(r.secrets) == 0 &&
		len(r.databases) == 0 && len(r.datastores) == 0 &&
		len(r.runtimes) == 0 && len(r.stores) == 0 && len(r.emulators) == 0
}

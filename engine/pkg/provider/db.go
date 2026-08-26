// Package provider declares the interfaces that make Antifailure extensible.
//
// A provider is the main extension point, and it is meant to be written by
// people outside this repository. Each interface ships with a conformance
// suite that any implementation runs, so "conformant" is a thing a test says
// rather than a thing a maintainer judges. The suite's README is generated
// from its subtest names, which means the documentation of what conformance
// requires cannot drift from what is actually checked.
package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// GoldenVersion identifies one masked, verified copy of a database.
//
// Versions are immutable. A refresh produces a new one rather than mutating an
// existing one, because an environment that branched an hour ago must keep
// seeing the data it branched from; otherwise a test that passed becomes a
// test that fails for a reason nobody can reproduce.
type GoldenVersion struct {
	// ID has the form gv_<timestamp>_<hash>, so that sorting by name sorts by
	// age and the hash makes two refreshes in the same second distinct.
	ID string
	// CreatedAt is when the refresh finished.
	CreatedAt time.Time
	// SizeBytes is the version's size where the provider reports one.
	SizeBytes int64
	// RulesHash identifies the masking rules that produced it, so that a rules
	// change can be detected without re-reading the data.
	RulesHash string
	// Verified reports whether the verification scanner passed. Branch refuses
	// an unverified version; that rule is the product's central promise and it
	// is enforced here rather than in a checklist.
	Verified bool
	// Attestation is the signed statement of what was scanned and found.
	Attestation string
	// ProviderRef is the provider's own identifier for the version, opaque to
	// the engine.
	ProviderRef string
}

// Branch is one environment's database.
type Branch struct {
	// EnvID is the environment this branch belongs to.
	EnvID string
	// From is the golden version it was created from.
	From string
	// ProviderRef is the provider's own identifier.
	ProviderRef string
	// CreatedAt is when it was created.
	CreatedAt time.Time
}

// ConnMode selects which connection string to hand out.
type ConnMode string

const (
	// ConnDirect is a direct connection, which is what migrations and
	// pg_restore need because they use session level features a pooler in
	// transaction mode does not support.
	ConnDirect ConnMode = "direct"
	// ConnPooled is a pooled connection, which is what an application should
	// use.
	ConnPooled ConnMode = "pooled"
)

// Caps describes what a provider can do.
//
// Capabilities exist so that the conformance suite can skip a behavior
// explicitly, naming the missing capability, rather than pass silently. A
// silent skip is how a provider ends up claiming conformance it does not have.
type Caps struct {
	// Branching reports whether the provider can create branches at all. A
	// provider without it is not a database provider.
	Branching bool
	// Reset reports whether a branch can be returned to its golden state
	// without being destroyed and recreated.
	Reset bool
	// CopyOnWrite reports whether a branch shares storage with its golden, and
	// therefore whether branch time is independent of database size.
	CopyOnWrite bool
	// ProviderMasking reports whether the provider can apply masking rules
	// itself. The engine still runs its own verification scan afterwards,
	// because a provider's masking is a claim and verification is a check.
	ProviderMasking bool
	// PooledEndpoints reports whether ConnPooled is available.
	PooledEndpoints bool
	// MaxConcurrentBranches is the provider's limit. Zero means unlimited.
	MaxConcurrentBranches int
	// ExpectedBranchLatency is what the provider expects branching to take on
	// the conformance dataset. The suite asserts against it, so a provider
	// that gets slower fails rather than quietly degrading.
	ExpectedBranchLatency time.Duration
	// SupportedVersions lists the Postgres majors the provider handles.
	SupportedVersions []int
}

// Supports reports whether a Postgres major version is supported.
func (c Caps) Supports(major int) bool {
	for _, v := range c.SupportedVersions {
		if v == major {
			return true
		}
	}
	return false
}

// GoldenSpec describes a refresh.
type GoldenSpec struct {
	// SourceURL is the read only connection string of the source database. It
	// is read once, during the refresh, and never stored.
	SourceURL secrets.Value
	// Version is the Postgres major version to create.
	Version int
	// RulesHash identifies the masking rules being applied.
	RulesHash string
	// Subset, when set, narrows what is copied.
	Subset *SubsetSpec
	// Mask applies the masking rules to a candidate, and is called by the
	// provider once the candidate holds data.
	//
	// Inverting the control this way is what lets a provider choose where the
	// masking runs. Neon masks during its own anonymized branch creation;
	// Docker masks a container it just restored into. Both call this, so the
	// rules have exactly one implementation.
	Mask func(ctx context.Context, candidateURL secrets.Value) error
	// Verify scans a candidate and returns a signed attestation. A provider
	// must call it and must not publish a version if it returns an error.
	Verify func(ctx context.Context, candidateURL secrets.Value) (string, error)
}

// SubsetSpec narrows what a refresh copies.
type SubsetSpec struct {
	SeedTable        string
	SeedWhere        string
	MaxRows          int
	FollowDependents int
}

// Resource is one thing a provider created, as the leak detector sees it.
type Resource struct {
	// Kind is the provider's own resource type, for example "branch" or
	// "snapshot".
	Kind string
	// ID is the provider's identifier.
	ID string
	// EnvID is the environment it belongs to, when the provider records one.
	EnvID string
	// CreatedAt is when it was created, where the provider reports it.
	CreatedAt time.Time
	// Labels carry whatever else the provider knows, for diagnosis.
	Labels map[string]string
}

// Health describes whether a branch is usable.
type Health struct {
	Reachable bool
	Detail    string
	Latency   time.Duration
}

// Database creates and destroys the databases environments use.
//
// Every method must be idempotent by its identifying argument. Creating a
// branch for an environment that already has one returns the existing branch;
// destroying one that is already gone succeeds. Both are load bearing: the
// engine retries after timeouts, and a retry that creates a second resource is
// how an orphan is made.
//
// Every method takes a context and must honor cancellation. A cancelled create
// must leave either no resource or one the journal already knows about.
type Database interface {
	// Name identifies the provider, and is what appears in a manifest.
	Name() string

	// Capabilities describes what this provider can do.
	Capabilities() Caps

	// RefreshGolden builds a new masked, verified golden version.
	//
	// It must call spec.Mask and then spec.Verify, and must not return a
	// version whose Verified field is false. A provider that publishes an
	// unverified version has broken the product's central guarantee.
	RefreshGolden(ctx context.Context, spec GoldenSpec) (GoldenVersion, error)

	// ListGoldens returns known versions, newest first.
	ListGoldens(ctx context.Context) ([]GoldenVersion, error)

	// DestroyGolden removes a version. Removing one that does not exist
	// succeeds.
	DestroyGolden(ctx context.Context, version string) error

	// Branch creates a database for an environment from a golden version.
	//
	// It must refuse an unverified version with AF-MSK-001. Calling it twice
	// with the same environment identifier returns the same branch.
	Branch(ctx context.Context, version string, envID string) (Branch, error)

	// Reset returns a branch to its golden state, including sequences.
	// Providers without the Reset capability return ErrUnsupported.
	Reset(ctx context.Context, b Branch) error

	// Destroy removes a branch. Removing one that is already gone succeeds.
	Destroy(ctx context.Context, b Branch) error

	// ConnString returns a connection string for a branch.
	//
	// The result is a secrets.Value, so it renders as [redacted] everywhere
	// text is produced and cannot reach a log by accident.
	ConnString(ctx context.Context, b Branch, mode ConnMode) (secrets.Value, error)

	// Inventory lists everything this provider currently holds, which the leak
	// detector compares against the journal. A provider that cannot enumerate
	// its own resources cannot be checked for leaks, so this is required.
	Inventory(ctx context.Context) ([]Resource, error)

	// Health reports whether a branch is reachable.
	Health(ctx context.Context, b Branch) (Health, error)

	// Close releases the provider's own resources, such as connection pools.
	Close() error
}

// ErrUnsupported is returned by a method whose capability the provider does
// not declare.
var ErrUnsupported = fmt.Errorf("provider: this provider does not support that operation")

// Factory builds a provider from its configuration. Registration uses it so
// that a provider is only constructed when a manifest names it.
type Factory func(ctx context.Context, cfg Config) (Database, error)

// Config carries what a provider needs to start.
type Config struct {
	// Version is the Postgres major version from the manifest.
	Version int
	// StateDir is where a provider may keep local files.
	StateDir string
	// Secret resolves a named credential. Providers never read the process
	// environment directly, so that every credential a provider uses is
	// declared and auditable.
	Secret func(name string) (secrets.Value, bool)
	// Settings carries provider specific values from the manifest.
	Settings map[string]string
}

// NewGoldenVersionID builds an identifier from a timestamp and a hash.
//
// The format sorts by age as a string, which means a directory listing, a
// database index, and a human reading a list all agree on the order without
// parsing anything.
func NewGoldenVersionID(at time.Time, hash string) string {
	if len(hash) > 8 {
		hash = hash[:8]
	}
	for len(hash) < 8 {
		hash += "0"
	}
	return fmt.Sprintf("gv_%s_%s", at.UTC().Format("20060102150405"), hash)
}

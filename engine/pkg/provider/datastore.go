package provider

import (
	"context"
	"errors"

	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// The datastore half of the provider surface.
//
// Database is a single struct and it is Postgres. There was one golden, one
// masking pass, one verification scan and one branch, and everything else a
// manifest declared was an empty container. An analytics product's twin held a
// masked Postgres and zero events, because the events live in ClickHouse and
// ClickHouse came up empty, and every query path that mattered was tested
// against nothing while the run went green.
//
// So this is the second interface, and it is deliberately SMALLER than
// Database rather than a copy of it. A second store has no pooled endpoint, no
// reset, and no golden pool of its own to enumerate. What it has is a copy per
// environment and a way to say how faithful that copy is.

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
// It lives beside Database because the manifest now declares datastores and
// the entry the primary database normalizes into is one of them. It moved here
// from engine/pkg/extension, which is where it waited for that, and the older
// name is kept there as an alias.
//
// WHAT THIS DOES AND DOES NOT DO TODAY, said plainly, because a socket nothing
// consults looks exactly like a working feature from the outside and this
// repository has already shipped an audit sink that forwarded nothing. A
// manifest declares a datastore and its stance, the validator refuses one that
// declares no stance, and the fidelity report names every declared store with
// the stance somebody chose. Nothing yet refreshes a golden for one, branches
// one, or starts one: the implementations are the lanes after this, and the
// conformance suite in engine/conformance is what decides whether one of them
// is finished.
type Datastore interface {
	// Name identifies the datastore in output and in errors.
	Name() string
	// Capabilities declares what this datastore can do.
	Capabilities() DatastoreCaps
	// RefreshGolden builds a new masked, verified copy. A datastore that
	// declares Golden false returns ErrNoGolden.
	RefreshGolden(ctx context.Context, spec GoldenSpec) (GoldenVersion, error)
	// Branch creates this environment's copy. Calling it twice with the same
	// environment identifier returns the same branch, the same idempotency
	// contract Database has and for the same reason: the engine retries after
	// timeouts and a retry that creates a second resource is how an orphan is
	// made.
	Branch(ctx context.Context, version string, envID string) (Branch, error)
	// Destroy removes a branch. Removing one that is already gone succeeds.
	Destroy(ctx context.Context, b Branch) error
	// ConnString returns how to reach a branch, as a value that renders as
	// [redacted] everywhere text is produced.
	ConnString(ctx context.Context, b Branch) (secret.Value, error)
	// Inventory lists everything this datastore holds, which the leak detector
	// compares against the journal.
	Inventory(ctx context.Context) ([]Resource, error)
	// Health reports whether a branch is reachable.
	Health(ctx context.Context, b Branch) (Health, error)
	// Close releases the datastore's own resources.
	Close() error
}

// ErrNoGolden is returned by a datastore that holds no golden, which is a
// declared stance rather than a failure. A cache is rebuilt from the primary
// and a copy of one would be noise.
var ErrNoGolden = errors.New("provider: this datastore holds no golden")

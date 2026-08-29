// Package conformance holds the suites every provider implementation runs.
//
// A provider is the main extension point, and it is meant to be written by
// people outside this repository. That only works if "conformant" is something
// a test decides rather than something a maintainer judges, so each interface
// ships with a suite that any implementation calls:
//
//	func TestMyProvider(t *testing.T) {
//	    conformance.RunDatabase(t, factory, conformance.Options{})
//	}
//
// Two design rules make the suite trustworthy.
//
// A behavior a provider cannot support is skipped explicitly, naming the
// missing capability. A silent skip is how a provider ends up claiming
// conformance it does not have, and the skip line is what a reviewer reads.
//
// The suite is itself tested, against a fake provider that is deliberately
// broken one behavior at a time. A suite nobody has proved can fail is a suite
// that proves nothing.
//
// That second rule is met by the runtime suite, in fakeruntime_test.go and
// runtime_selftest_test.go, and is NOT yet met by the database suite below.
// This paragraph used to claim it for both, which was the more comfortable of
// the two things it could have said and the wrong one: STATUS.md has recorded
// the gap for some time, and a package doc asserting a guarantee the package
// does not make is exactly the kind of thing this suite exists to catch in
// other people's code. The runtime files are the pattern to copy; the cost is
// an afternoon and the thing it buys is knowing that a green run means
// anything at all.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Factory builds a provider for one test. Each subtest gets its own, so that a
// provider holding state cannot let one behavior's leftovers change another's
// result.
type Factory func(t *testing.T) provider.Database

// Options configure a run.
type Options struct {
	// Timeout bounds each behavior. Zero uses two minutes, which is generous
	// for a local provider and tight enough that a hung cloud call fails the
	// test rather than the job.
	Timeout time.Duration
	// SeedSQL runs against a golden candidate during a refresh, so that a
	// suite has known rows to read back. When empty, a small default schema is
	// used.
	SeedSQL string
	// SkipSlow omits the behaviors that create several branches, for a run
	// against a provider that bills per branch.
	SkipSlow bool
}

// DefaultSeedSQL is the schema every conformance run works against.
//
// It is deliberately small and deliberately awkward: a composite primary key,
// a self reference, a sequence, and a unique index, because those are what a
// naive branch or restore implementation gets wrong.
const DefaultSeedSQL = `
CREATE TABLE conformance_users (
    id          bigserial PRIMARY KEY,
    email       text NOT NULL UNIQUE,
    manager_id  bigint REFERENCES conformance_users(id),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE conformance_events (
    user_id  bigint NOT NULL REFERENCES conformance_users(id),
    seq      int NOT NULL,
    payload  jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (user_id, seq)
);
INSERT INTO conformance_users (email) VALUES
    ('first@example.test'), ('second@example.test'), ('third@example.test');
INSERT INTO conformance_events (user_id, seq) VALUES (1, 1), (1, 2), (2, 1);
`

// Behaviors returns the name and one sentence description of every behavior
// the suite checks, sorted.
//
// The provider authoring page and the suite README are generated from this, so
// the documentation of what conformance requires cannot drift from what is
// actually run.
func Behaviors() []Behavior {
	out := append([]Behavior(nil), databaseBehaviors...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Behavior is one checked property.
type Behavior struct {
	// Name is the subtest name.
	Name string
	// Describe says what is checked, in one sentence.
	Describe string
	// Requires names the capability a provider must declare for this behavior
	// to run. Empty means it always runs.
	Requires string
}

var databaseBehaviors = []Behavior{
	{"Capabilities_AreSelfConsistent", "The declared capabilities do not contradict each other.", ""},
	{"Refresh_ProducesAVerifiedGolden", "A refresh masks, verifies, and publishes a version, and never publishes an unverified one.", ""},
	{"Refresh_CallsMaskThenVerify", "A refresh applies the masking rules before it verifies, and does both.", ""},
	{"Refresh_RefusesToPublishWhenVerificationFails", "A refresh whose verification fails publishes nothing.", ""},
	{"List_ReturnsWhatWasCreated", "A published version appears in the listing.", ""},
	{"Branch_ReadsAKnownRow", "A branch holds the golden's data.", ""},
	{"Branch_IsIdempotentByEnvironment", "Branching twice for one environment returns one branch, not two.", ""},
	{"Branch_RefusesAnUnverifiedGolden", "Branching an unverified version fails with AF-MSK-001.", ""},
	{"Branch_RefusesAMissingGolden", "Branching a version that does not exist fails with AF-DB-004.", ""},
	{"Branch_IsIsolatedFromTheGolden", "Writing to a branch leaves the golden unchanged.", ""},
	{"Branch_IsIsolatedFromOtherBranches", "Two branches of one golden cannot see each other's writes.", ""},
	{"Reset_ReturnsToGoldenState", "Reset undoes a branch's writes, including sequences.", "reset"},
	{"Destroy_RemovesTheBranch", "A destroyed branch no longer appears in the inventory.", ""},
	{"Destroy_OfSomethingAlreadyGoneSucceeds", "Destroying twice is not an error, because teardown retries.", ""},
	{"ConnString_IsASecret", "A connection string renders as redacted and carries no plaintext.", ""},
	{"ConnString_PooledWorksWhenDeclared", "A pooled connection string connects when the capability is declared.", "pooled"},
	{"Inventory_ListsLiveResources", "Inventory reports what exists, which is what the leak detector compares against.", ""},
	{"Health_ReportsAReachableBranch", "Health reports a live branch as reachable.", ""},
	{"Health_ReportsADestroyedBranch", "Health reports a destroyed branch as unreachable rather than erroring.", ""},
	{"Concurrency_RespectsTheDeclaredLimit", "Branching past the declared limit fails fast with AF-DB-006 rather than hanging.", "limit"},
	{"Cancellation_LeavesNoUntrackedResource", "A cancelled branch leaves either nothing or something the inventory reports.", ""},
	{"GoldenGC_RefusesAReferencedVersion", "Destroying a golden that a branch came from is refused.", ""},
	{"Refresh_DoesNotDisturbExistingBranches", "A new golden version leaves branches of an older one untouched.", ""},
}

// RunDatabase runs the whole suite against a provider.
func RunDatabase(t *testing.T, factory Factory, opts Options) {
	t.Helper()
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.SeedSQL == "" {
		opts.SeedSQL = DefaultSeedSQL
	}

	// One provider instance answers the capability questions, so that a
	// skipped behavior is decided once and reported the same way everywhere.
	probe := factory(t)
	caps := probe.Capabilities()
	_ = probe.Close()

	// What the daemon already held before the suite ran. The assertion at the
	// end is that the suite left nothing new, not that the machine is empty:
	// a shared daemon carries goldens from other work, and failing on those
	// would make the check something people learn to ignore.
	before := inventorySnapshot(t, factory)
	created := newCreatedSet()

	for _, b := range databaseBehaviors {
		b := b
		t.Run(b.Name, func(t *testing.T) {
			if reason := skipReason(b, caps, opts); reason != "" {
				// Named, never silent. A reviewer reading the output has to be
				// able to see exactly which guarantee this provider does not
				// make.
				t.Skipf("skipped: %s does not declare %s", nameOf(probe), reason)
			}
			ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
			defer cancel()
			runBehavior(ctx, t, b.Name, factory, opts, created)
		})
	}

	// A conformance suite for a product whose whole promise is that nothing
	// outlives its environment must not itself leak. This has caught a
	// provider leaving a golden per refresh, which on an image backed provider
	// is half a gigabyte a run.
	if t.Failed() {
		// A failing behavior legitimately leaves things behind for
		// inspection, and reporting that as a second failure buries the first.
		return
	}
	// Only what this suite created. Go runs test packages in parallel, and
	// another package bringing an environment up mid-run creates a golden that
	// legitimately outlives it: goldens are shared and reused, which is the
	// whole point of them. Reporting those as leaks made this suite fail for
	// something it neither did nor should care about, twice, on CI only,
	// because a laptop runs fewer packages at once.
	for r := range inventorySnapshot(t, factory) {
		if before[r] {
			continue
		}
		id := r
		if i := strings.IndexByte(r, ' '); i >= 0 {
			id = r[i+1:]
		}
		if !created.matches(id) {
			continue
		}
		t.Errorf("the suite left %s behind; every resource a behavior creates must be removed "+
			"when it finishes, whether it passed or not", r)
	}
}

// inventorySnapshot records what the provider owns, as comparable strings.
func inventorySnapshot(t *testing.T, factory Factory) map[string]bool {
	t.Helper()
	p := factory(t)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	items, err := p.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	out := make(map[string]bool, len(items))
	for _, r := range items {
		out[r.Kind+" "+r.ID] = true
	}
	return out
}

func nameOf(p provider.Database) string {
	if p == nil {
		return "this provider"
	}
	return p.Name()
}

func skipReason(b Behavior, caps provider.Caps, opts Options) string {
	switch b.Requires {
	case "reset":
		if !caps.Reset {
			return "the reset capability"
		}
	case "pooled":
		if !caps.PooledEndpoints {
			return "pooled endpoints"
		}
	case "limit":
		if caps.MaxConcurrentBranches <= 0 {
			return "a concurrent branch limit"
		}
		if opts.SkipSlow {
			return "a run configured to skip slow behaviors"
		}
	}
	if opts.SkipSlow {
		switch b.Name {
		case "Branch_IsIsolatedFromOtherBranches", "Refresh_DoesNotDisturbExistingBranches":
			return "a run configured to skip slow behaviors"
		}
	}
	return ""
}

// harness gives one behavior a provider and the helpers it needs.
type harness struct {
	t    *testing.T
	p    provider.Database
	opts Options
	// created records every resource this suite made, so that the leak check
	// at the end can tell the suite's own leftovers from anything another test
	// package happened to create while it was running.
	created *createdSet
	// masked and verified record what the refresh callbacks were asked to do,
	// which is how the suite proves a provider actually called them rather
	// than publishing a version it never checked.
	masked     int
	verified   int
	failVerify bool
}

// createdSet records the resources one run of the suite made.
//
// Behaviors run in parallel and Go runs test packages in parallel, so this is
// written from several goroutines and read once at the end.
type createdSet struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newCreatedSet() *createdSet { return &createdSet{ids: map[string]bool{}} }

func (c *createdSet) add(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids[id] = true
}

// matches reports whether an inventory entry names something this suite made.
//
// Containment rather than equality, because the two forms differ: a golden is
// tracked by its version, gv_20260101000000_abc, and inventoried by whatever
// the provider calls the underlying resource, which for the Docker provider is
// antifailure/golden:gv_20260101000000_abc. Comparing them for equality
// silently matched nothing, which turned this filter into a switch that
// disabled the leak detector entirely. A negative control caught that: with
// cleanup deliberately removed, the suite still passed.
//
// The tracked identifiers are timestamped and suffixed, so containment cannot
// collide with another package's resources by accident.
func (c *createdSet) matches(inventoryID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.ids {
		if id != "" && strings.Contains(inventoryID, id) {
			return true
		}
	}
	return false
}

func runBehavior(ctx context.Context, t *testing.T, name string, factory Factory, opts Options, created *createdSet) {
	h := &harness{t: t, p: factory(t), opts: opts, created: created}
	t.Cleanup(func() { _ = h.p.Close() })

	switch name {
	case "Capabilities_AreSelfConsistent":
		h.capabilitiesAreSelfConsistent()
	case "Refresh_ProducesAVerifiedGolden":
		h.refreshProducesAVerifiedGolden(ctx)
	case "Refresh_CallsMaskThenVerify":
		h.refreshCallsMaskThenVerify(ctx)
	case "Refresh_RefusesToPublishWhenVerificationFails":
		h.refreshRefusesWhenVerificationFails(ctx)
	case "List_ReturnsWhatWasCreated":
		h.listReturnsWhatWasCreated(ctx)
	case "Branch_ReadsAKnownRow":
		h.branchReadsAKnownRow(ctx)
	case "Branch_IsIdempotentByEnvironment":
		h.branchIsIdempotent(ctx)
	case "Branch_RefusesAnUnverifiedGolden":
		h.branchRefusesUnverified(ctx)
	case "Branch_RefusesAMissingGolden":
		h.branchRefusesMissing(ctx)
	case "Branch_IsIsolatedFromTheGolden":
		h.branchIsIsolatedFromGolden(ctx)
	case "Branch_IsIsolatedFromOtherBranches":
		h.branchesAreIsolated(ctx)
	case "Reset_ReturnsToGoldenState":
		h.resetReturnsToGolden(ctx)
	case "Destroy_RemovesTheBranch":
		h.destroyRemovesTheBranch(ctx)
	case "Destroy_OfSomethingAlreadyGoneSucceeds":
		h.destroyTwiceSucceeds(ctx)
	case "ConnString_IsASecret":
		h.connStringIsASecret(ctx)
	case "ConnString_PooledWorksWhenDeclared":
		h.pooledConnStringWorks(ctx)
	case "Inventory_ListsLiveResources":
		h.inventoryListsLiveResources(ctx)
	case "Health_ReportsAReachableBranch":
		h.healthReportsReachable(ctx)
	case "Health_ReportsADestroyedBranch":
		h.healthReportsDestroyed(ctx)
	case "Concurrency_RespectsTheDeclaredLimit":
		h.concurrencyRespectsTheLimit(ctx)
	case "Cancellation_LeavesNoUntrackedResource":
		h.cancellationLeavesNothingUntracked(ctx)
	case "GoldenGC_RefusesAReferencedVersion":
		h.goldenGCRefusesAReferencedVersion(ctx)
	case "Refresh_DoesNotDisturbExistingBranches":
		h.refreshDoesNotDisturbBranches(ctx)
	default:
		t.Fatalf("conformance: no implementation for behavior %q", name)
	}
}

// spec builds a refresh specification whose callbacks record what happened.
func (h *harness) spec() provider.GoldenSpec {
	return provider.GoldenSpec{
		SourceURL: secrets.New("postgres://conformance@source/db"),
		Version:   17,
		RulesHash: "conformance",
		Mask: func(_ context.Context, _ secrets.Value) error {
			h.masked++
			return nil
		},
		Verify: func(_ context.Context, _ secrets.Value) (string, error) {
			h.verified++
			if h.failVerify {
				return "", aferrors.Coded(aferrors.AFMSK002,
					"detector", "email", "table", "conformance_users", "column", "email")
			}
			if h.masked == 0 {
				// Verification running before masking would attest to the
				// unmasked data, which is worse than not verifying at all.
				return "", fmt.Errorf("verify ran before mask")
			}
			return `{"scanner":"conformance","findings":0}`, nil
		},
	}
}

// trackGolden schedules a golden for removal when the behavior finishes.
//
// Without it the suite leaves one golden per refresh behind, and a provider
// whose goldens are images leaks half a gigabyte per run. A conformance suite
// for a product whose whole promise is that nothing outlives its environment
// must not be the thing leaking.
func (h *harness) trackGolden(id string) {
	if id == "" {
		return
	}
	h.created.add(id)
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// Best effort: a behavior that deliberately destroyed it already, or
		// left it referenced by a branch this cleanup has not reached yet,
		// must not turn a passing test into a failing one. The leak check at
		// the end of the run is what proves the cleanup worked.
		_ = h.p.DestroyGolden(ctx, id)
	})
}

func (h *harness) refresh(ctx context.Context) provider.GoldenVersion {
	h.t.Helper()
	gv, err := h.p.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)
	if err != nil {
		h.t.Fatalf("RefreshGolden: %v", err)
	}
	if !gv.Verified {
		h.t.Fatal("RefreshGolden published a version whose Verified flag is false; " +
			"a provider must not publish a version it did not verify")
	}
	if gv.ID == "" {
		h.t.Fatal("RefreshGolden returned a version with no identifier")
	}
	return gv
}

func (h *harness) branch(ctx context.Context, version, env string) provider.Branch {
	h.t.Helper()
	b, err := h.p.Branch(ctx, version, env)
	if err != nil {
		h.t.Fatalf("Branch(%s, %s): %v", version, env, err)
	}
	// Recorded before the cleanup is registered, so that the leak check can
	// tell this suite's branch from one another test package is using.
	h.created.add(b.ProviderRef)
	h.created.add(b.EnvID)
	h.t.Cleanup(func() {
		// Teardown runs even when the behavior failed, so that one failing
		// behavior does not leave resources that make the next one fail too.
		_ = h.p.Destroy(context.Background(), b)
	})
	return b
}

func (h *harness) capabilitiesAreSelfConsistent() {
	caps := h.p.Capabilities()
	if !caps.Branching {
		h.t.Fatal("a database provider must declare the branching capability")
	}
	if len(caps.SupportedVersions) == 0 {
		h.t.Fatal("a provider must declare which Postgres versions it supports")
	}
	for _, v := range caps.SupportedVersions {
		if v < 12 || v > 30 {
			h.t.Fatalf("supported version %d is not a plausible Postgres major", v)
		}
	}
	if caps.ExpectedBranchLatency <= 0 {
		h.t.Fatal("a provider must declare its expected branch latency, so that " +
			"getting slower fails a test rather than degrading quietly")
	}
	if caps.MaxConcurrentBranches < 0 {
		h.t.Fatal("a negative branch limit is not meaningful; use zero for unlimited")
	}
	if h.p.Name() == "" {
		h.t.Fatal("a provider must have a name, which is what a manifest refers to")
	}
}

func (h *harness) refreshProducesAVerifiedGolden(ctx context.Context) {
	gv := h.refresh(ctx)
	if gv.RulesHash == "" {
		h.t.Error("the version records no rules hash, so a rules change cannot be detected " +
			"without re-reading the data")
	}
	if gv.Attestation == "" {
		h.t.Error("the version carries no attestation, so nothing records what was scanned")
	}
	if gv.CreatedAt.IsZero() {
		h.t.Error("the version records no creation time, so retention cannot be applied")
	}
}

func (h *harness) refreshCallsMaskThenVerify(ctx context.Context) {
	h.refresh(ctx)
	if h.masked == 0 {
		h.t.Error("the provider published a version without applying the masking rules")
	}
	if h.verified == 0 {
		h.t.Error("the provider published a version without verifying it")
	}
}

func (h *harness) refreshRefusesWhenVerificationFails(ctx context.Context) {
	h.failVerify = true
	gv, err := h.p.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)
	if err == nil && gv.Verified {
		h.t.Fatal("the provider published a verified version even though verification failed; " +
			"this is the one guarantee the product cannot bend")
	}
	if gv.ID == "" {
		return // nothing was published, which is the ideal outcome
	}
	// The assertion is about this refresh's version, not about every version
	// the provider holds. A provider whose goldens live in a shared daemon
	// legitimately carries versions from earlier runs, and failing on those
	// would be testing the machine rather than the provider.
	goldens, listErr := h.p.ListGoldens(ctx)
	if listErr != nil {
		return // a provider that cannot list after a failure is checked elsewhere
	}
	for _, g := range goldens {
		if g.ID == gv.ID && g.Verified {
			h.t.Fatalf("version %s was published as verified even though verification failed", g.ID)
		}
	}
}

func (h *harness) listReturnsWhatWasCreated(ctx context.Context) {
	gv := h.refresh(ctx)
	goldens, err := h.p.ListGoldens(ctx)
	if err != nil {
		h.t.Fatalf("ListGoldens: %v", err)
	}
	for _, g := range goldens {
		if g.ID == gv.ID {
			return
		}
	}
	h.t.Fatalf("version %s was published and does not appear in the listing of %d", gv.ID, len(goldens))
}

func (h *harness) branchReadsAKnownRow(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00001")
	if b.EnvID != "env_conformance00001" {
		h.t.Errorf("the branch records environment %q rather than the one requested", b.EnvID)
	}
	if b.From != gv.ID {
		h.t.Errorf("the branch records golden %q rather than %q", b.From, gv.ID)
	}
	n := h.countUsers(ctx, b)
	if n != 3 {
		h.t.Fatalf("the branch holds %d seeded rows and should hold 3; the golden's data did not arrive", n)
	}
}

func (h *harness) branchIsIdempotent(ctx context.Context) {
	gv := h.refresh(ctx)
	first := h.branch(ctx, gv.ID, "env_conformance00002")
	second, err := h.p.Branch(ctx, gv.ID, "env_conformance00002")
	if err != nil {
		h.t.Fatalf("the second Branch for one environment failed: %v", err)
	}
	if second.ProviderRef != first.ProviderRef {
		h.t.Fatalf("branching twice for one environment produced two resources, %q and %q; "+
			"a retry after a timeout would orphan one", first.ProviderRef, second.ProviderRef)
	}
}

func (h *harness) branchRefusesUnverified(ctx context.Context) {
	h.failVerify = true
	gv, err := h.p.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)

	// Every path asserts something. It did not: the whole body used to sit
	// inside `if err == nil && gv.ID != ""`, and a provider that correctly
	// refuses to PUBLISH an unverified version returns an error and an empty
	// ID, so the condition was false and nothing ran. The behaviour passed
	// having checked nothing, for exactly the providers that behave correctly,
	// which is every provider shipped today. Found by the self test in
	// db_selftest_test.go, which points the suite at a provider that branches
	// unverified goldens on purpose and requires this to go red.
	if err != nil || gv.ID == "" {
		// The provider refused at the refresh, which is the stronger place to
		// refuse: there is then no unverified version in existence to branch.
		// Assert the refusal was well formed rather than returning silently.
		if err == nil {
			h.t.Fatal("the refresh published nothing and reported no error, so a caller " +
				"cannot tell a refusal from a success")
		}
		if gv.ID != "" {
			h.t.Fatalf("the refresh reported an error and still published %s; a version that "+
				"exists after a failed verification is one something else can branch", gv.ID)
		}
		return
	}

	// It published a version whose verification failed. Branching that version
	// must be refused, and this is the product's central promise.
	_, branchErr := h.p.Branch(ctx, gv.ID, "env_conformance00003")
	if branchErr == nil {
		h.t.Fatal("an unverified golden was branched; this is the product's central promise")
	}
	if !errors.Is(branchErr, aferrors.Coded(aferrors.AFMSK001)) {
		h.t.Errorf("branching an unverified golden failed with %v, and must fail with AF-MSK-001", branchErr)
	}
}

func (h *harness) branchRefusesMissing(ctx context.Context) {
	_, err := h.p.Branch(ctx, "gv_19700101000000_deadbeef", "env_conformance00004")
	if err == nil {
		h.t.Fatal("branching a golden that does not exist succeeded")
	}
	if !errors.Is(err, aferrors.Coded(aferrors.AFDB004)) {
		h.t.Errorf("branching a missing golden failed with %v, and must fail with AF-DB-004", err)
	}
}

func (h *harness) branchIsIsolatedFromGolden(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00005")
	h.exec(ctx, b, "INSERT INTO conformance_users (email) VALUES ('written-to-branch@example.test')")

	// A second branch of the same golden must not see the first's write. If it
	// does, the branch is a shared database and every environment is polluting
	// every other one.
	other := h.branch(ctx, gv.ID, "env_conformance00006")
	if n := h.countUsers(ctx, other); n != 3 {
		h.t.Fatalf("a fresh branch of the same golden holds %d rows and should hold 3; "+
			"writing to one branch changed the golden", n)
	}
}

func (h *harness) branchesAreIsolated(ctx context.Context) {
	gv := h.refresh(ctx)
	a := h.branch(ctx, gv.ID, "env_conformance00007")
	b := h.branch(ctx, gv.ID, "env_conformance00008")

	h.exec(ctx, a, "INSERT INTO conformance_users (email) VALUES ('only-in-a@example.test')")
	h.exec(ctx, b, "INSERT INTO conformance_users (email) VALUES ('only-in-b@example.test')")

	if n := h.countUsers(ctx, a); n != 4 {
		h.t.Errorf("branch a holds %d rows and should hold 4", n)
	}
	if n := h.countUsers(ctx, b); n != 4 {
		h.t.Errorf("branch b holds %d rows and should hold 4", n)
	}
	if h.countMatching(ctx, a, "only-in-b@example.test") != 0 {
		h.t.Fatal("branch a can see branch b's write; the branches share storage")
	}
}

func (h *harness) resetReturnsToGolden(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00009")
	h.exec(ctx, b, "INSERT INTO conformance_users (email) VALUES ('before-reset@example.test')")
	if n := h.countUsers(ctx, b); n != 4 {
		h.t.Fatalf("the write did not land: %d rows", n)
	}
	if err := h.p.Reset(ctx, b); err != nil {
		h.t.Fatalf("Reset: %v", err)
	}
	if n := h.countUsers(ctx, b); n != 3 {
		h.t.Fatalf("after reset the branch holds %d rows and should hold 3", n)
	}
	// Sequences matter as much as rows. A reset that rewinds the data but not
	// the sequence produces a primary key collision on the next insert, which
	// surfaces as an application bug nobody can reproduce.
	h.exec(ctx, b, "INSERT INTO conformance_users (email) VALUES ('after-reset@example.test')")
	if n := h.countUsers(ctx, b); n != 4 {
		h.t.Fatalf("inserting after a reset failed, which usually means the sequence was not rewound")
	}
}

func (h *harness) destroyRemovesTheBranch(ctx context.Context) {
	gv := h.refresh(ctx)
	b, err := h.p.Branch(ctx, gv.ID, "env_conformance00010")
	if err != nil {
		h.t.Fatalf("Branch: %v", err)
	}
	if err := h.p.Destroy(ctx, b); err != nil {
		h.t.Fatalf("Destroy: %v", err)
	}
	inv, err := h.p.Inventory(ctx)
	if err != nil {
		h.t.Fatalf("Inventory: %v", err)
	}
	for _, r := range inv {
		if r.ID == b.ProviderRef {
			h.t.Fatalf("the destroyed branch %s still appears in the inventory, "+
				"so the leak detector would report it forever", r.ID)
		}
	}
}

func (h *harness) destroyTwiceSucceeds(ctx context.Context) {
	gv := h.refresh(ctx)
	b, err := h.p.Branch(ctx, gv.ID, "env_conformance00011")
	if err != nil {
		h.t.Fatalf("Branch: %v", err)
	}
	if err := h.p.Destroy(ctx, b); err != nil {
		h.t.Fatalf("the first Destroy failed: %v", err)
	}
	// Teardown retries after a crash and after a partial failure, so deleting
	// something already gone is the expected case at least as often as
	// deleting something that exists.
	if err := h.p.Destroy(ctx, b); err != nil {
		h.t.Fatalf("the second Destroy failed with %v; deleting something already gone must succeed", err)
	}
}

func (h *harness) connStringIsASecret(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00012")
	conn, err := h.p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		h.t.Fatalf("ConnString: %v", err)
	}
	if conn.IsZero() {
		h.t.Fatal("ConnString returned an empty value")
	}
	// The type is what stops a connection string reaching a log by accident,
	// so the suite checks the rendering rather than trusting the signature.
	if rendered := fmt.Sprintf("%v %s", conn, conn); strings.Contains(rendered, "postgres") {
		h.t.Fatalf("the connection string rendered as %q rather than the redaction marker", rendered)
	}
	if !strings.HasPrefix(conn.Reveal(), "postgres") {
		h.t.Errorf("the revealed connection string does not look like a Postgres URL")
	}
}

func (h *harness) pooledConnStringWorks(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00013")
	direct, err := h.p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		h.t.Fatalf("ConnString direct: %v", err)
	}
	pooled, err := h.p.ConnString(ctx, b, provider.ConnPooled)
	if err != nil {
		h.t.Fatalf("ConnString pooled: %v", err)
	}
	if pooled.Equal(direct) {
		h.t.Error("the pooled and direct connection strings are identical, so the " +
			"pooled endpoints capability is declared but not implemented")
	}
}

func (h *harness) inventoryListsLiveResources(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00014")
	inv, err := h.p.Inventory(ctx)
	if err != nil {
		h.t.Fatalf("Inventory: %v", err)
	}
	for _, r := range inv {
		if r.ID == b.ProviderRef {
			if r.Kind == "" {
				h.t.Error("an inventory entry has no kind, so a leak cannot be described")
			}
			return
		}
	}
	h.t.Fatalf("a live branch %s does not appear in the inventory of %d resources; "+
		"a provider that cannot enumerate its own resources cannot be checked for leaks",
		b.ProviderRef, len(inv))
}

func (h *harness) healthReportsReachable(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_conformance00015")
	got, err := h.p.Health(ctx, b)
	if err != nil {
		h.t.Fatalf("Health: %v", err)
	}
	if !got.Reachable {
		h.t.Fatalf("a live branch reports unreachable: %s", got.Detail)
	}
}

func (h *harness) healthReportsDestroyed(ctx context.Context) {
	gv := h.refresh(ctx)
	b, err := h.p.Branch(ctx, gv.ID, "env_conformance00016")
	if err != nil {
		h.t.Fatalf("Branch: %v", err)
	}
	if err := h.p.Destroy(ctx, b); err != nil {
		h.t.Fatalf("Destroy: %v", err)
	}
	got, err := h.p.Health(ctx, b)
	if err != nil {
		// An error used to be accepted here, on the reasoning that the branch
		// genuinely is gone. That made the suite disagree with its own
		// catalogue, which says "unreachable rather than erroring", and the
		// description is what a provider author reads.
		//
		// The catalogue is right and both shipped providers already do it:
		// teardown asks for health, so a provider that errors on a branch it
		// has just removed makes a successful teardown look like a failure.
		// Gone is an answer, not a fault.
		h.t.Fatalf("Health returned an error for a destroyed branch: %v. "+
			"Report it unreachable instead: teardown asks for health, and an "+
			"error here makes a successful teardown look like a failure.", err)
	}
	if got.Reachable {
		h.t.Fatal("a destroyed branch reports reachable, so an environment would " +
			"be told its database is fine when it does not exist")
	}
}

func (h *harness) concurrencyRespectsTheLimit(ctx context.Context) {
	caps := h.p.Capabilities()
	gv := h.refresh(ctx)
	limit := caps.MaxConcurrentBranches

	for i := 0; i < limit; i++ {
		h.branch(ctx, gv.ID, fmt.Sprintf("env_conformancelimit%02d", i))
	}
	// One past the limit must fail fast and say so. Hanging is the failure
	// mode that turns a branch cap into a mystery, because the user sees an
	// af up that never returns.
	done := make(chan error, 1)
	go func() {
		_, err := h.p.Branch(ctx, gv.ID, "env_conformancelimitXX")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			h.t.Fatalf("branching past the declared limit of %d succeeded", limit)
		}
		if !errors.Is(err, aferrors.Coded(aferrors.AFDB006)) {
			h.t.Errorf("branching past the limit failed with %v, and must fail with AF-DB-006", err)
		}
	case <-time.After(30 * time.Second):
		h.t.Fatal("branching past the declared limit hung rather than failing fast")
	}
}

func (h *harness) cancellationLeavesNothingUntracked(ctx context.Context) {
	gv := h.refresh(ctx)
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	b, err := h.p.Branch(cancelCtx, gv.ID, "env_conformance00017")
	if err == nil {
		// Succeeding despite cancellation is allowed, as long as the resource
		// is reported, because then the journal and the leak detector can see
		// it. Silently creating something nothing knows about is not.
		h.t.Cleanup(func() { _ = h.p.Destroy(context.Background(), b) })
	}
	inv, invErr := h.p.Inventory(ctx)
	if invErr != nil {
		h.t.Fatalf("Inventory after a cancelled branch: %v", invErr)
	}
	if err != nil && b.ProviderRef != "" {
		for _, r := range inv {
			if r.ID == b.ProviderRef {
				h.t.Fatal("a cancelled branch reported failure and left a resource behind " +
					"that the caller has no identifier for")
			}
		}
	}
}

func (h *harness) goldenGCRefusesAReferencedVersion(ctx context.Context) {
	gv := h.refresh(ctx)
	h.branch(ctx, gv.ID, "env_conformance00018")
	err := h.p.DestroyGolden(ctx, gv.ID)
	if err == nil {
		h.t.Fatal("a golden with a live branch was destroyed; the branch's data would " +
			"disappear underneath a running environment")
	}
}

func (h *harness) refreshDoesNotDisturbBranches(ctx context.Context) {
	first := h.refresh(ctx)
	b := h.branch(ctx, first.ID, "env_conformance00019")
	h.exec(ctx, b, "INSERT INTO conformance_users (email) VALUES ('before-refresh@example.test')")

	// A refresh produces a new version. An environment that branched an hour
	// ago must keep seeing the data it branched from, or a test that passed
	// becomes a test that fails for a reason nobody can reproduce.
	second := h.refresh(ctx)
	if second.ID == first.ID {
		h.t.Fatal("a refresh reused the previous version's identifier; versions are immutable")
	}
	if n := h.countUsers(ctx, b); n != 4 {
		h.t.Fatalf("after a refresh the existing branch holds %d rows and should hold 4", n)
	}
}

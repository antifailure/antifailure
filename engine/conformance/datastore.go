package conformance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The datastore suite, which is the second one and is deliberately not a copy
// of the first.
//
// RunDatabase checks twenty four behaviours of a Postgres provider, and most
// of them are about SQL: a known row survives a branch, a sequence comes back
// after a reset, two branches cannot see each other's writes. None of that can
// be asked of a datastore in general, because the interface covers ClickHouse
// and Redis and Kafka and a search index, and the only query language they
// share is none.
//
// So this suite checks the contract instead of the contents: what a capability
// declaration has to mean, that a refresh masks before it verifies and
// publishes nothing when verification fails, that branching twice for one
// environment produces one branch, that destroying twice succeeds, that a
// connection string is a secret, and that a store which holds no golden says
// so with ErrNoGolden rather than with a generic failure. Those are the
// promises the engine relies on when it decides whether an environment can be
// trusted, and they are the same promises whatever the store runs.
//
// A store's own contents are the subject of its own package's tests, where
// there is a client that can read them.
//
// This file ships with a broken fake and a self test in the same commit, which
// is the rule engine/conformance/db.go's own doc comment says exists because
// it was not followed once. datastore_selftest_test.go points this suite at a
// datastore broken one behaviour at a time and requires each break to turn
// exactly one behaviour red.

// DatastoreFactory builds a datastore for one behaviour. Each gets its own, so
// that a datastore holding state cannot let one behaviour's leftovers change
// another's result.
type DatastoreFactory func(t *testing.T) provider.Datastore

// DatastoreOptions configure a run.
type DatastoreOptions struct {
	// Timeout bounds each behaviour. Zero uses two minutes, which is generous
	// for a local store and tight enough that a hung cloud call fails the test
	// rather than the job.
	Timeout time.Duration
}

// The capability names a behaviour can require.
//
// Written down rather than spelled inline because the skip line quotes them,
// and a skip that names a capability the provider author cannot find in the
// interface is worse than no skip at all.
const (
	requiresGolden          = "a golden"
	requiresNoGolden        = "no golden"
	requiresBranching       = "branching"
	requiresGoldenAndBranch = "a golden and branching"
)

var datastoreBehaviors = []Behavior{
	{"Name_IsNotEmpty", "The datastore names itself, because every error about it quotes the name.", ""},
	{"Capabilities_AreSelfConsistent", "The declared capabilities do not contradict each other.", ""},
	{"Refresh_SaysErrNoGoldenWhenItHoldsNone", "A store that declares no golden refuses a refresh with ErrNoGolden rather than a generic failure.", requiresNoGolden},
	{"Refresh_ProducesAVerifiedGolden", "A refresh masks, verifies, and publishes a version, and never publishes an unverified one.", requiresGolden},
	{"Refresh_CallsMaskThenVerify", "A refresh applies the masking rules before it verifies, and does both.", requiresGolden},
	{"Refresh_RefusesToPublishWhenVerificationFails", "A refresh whose verification fails publishes nothing.", requiresGolden},
	{"Branch_IsIdempotentByEnvironment", "Branching twice for one environment returns one branch, not two.", requiresBranching},
	{"Branch_RefusesAnUnverifiedGolden", "Branching an unverified version fails with AF-MSK-001.", requiresGoldenAndBranch},
	{"Destroy_RemovesTheBranch", "A destroyed branch no longer appears in the inventory.", requiresBranching},
	{"Destroy_OfSomethingAlreadyGoneSucceeds", "Destroying twice is not an error, because teardown retries.", requiresBranching},
	{"ConnString_IsASecret", "A connection string renders as redacted and carries no plaintext.", requiresBranching},
	{"Inventory_ListsLiveResources", "Inventory reports what exists, which is what the leak detector compares against.", requiresBranching},
	{"Health_ReportsAReachableBranch", "Health reports a live branch as reachable.", requiresBranching},
	{"Health_ReportsADestroyedBranch", "Health reports a destroyed branch as unreachable rather than erroring.", requiresBranching},
	{"Cancellation_LeavesNoUntrackedResource", "A cancelled branch leaves either nothing or something the inventory reports.", requiresBranching},
}

// DatastoreBehaviors returns the name and one sentence description of every
// behaviour this suite checks, sorted.
//
// The provider authoring page is written from this, so what conformance
// requires cannot drift from what is actually run.
func DatastoreBehaviors() []Behavior {
	out := append([]Behavior(nil), datastoreBehaviors...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunDatastore runs the whole suite against a datastore.
//
//	func TestMyStore(t *testing.T) {
//	    conformance.RunDatastore(t, factory, conformance.DatastoreOptions{})
//	}
func RunDatastore(t *testing.T, factory DatastoreFactory, opts DatastoreOptions) {
	t.Helper()
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}

	// One instance answers the capability questions, so that a skipped
	// behaviour is decided once and reported the same way everywhere.
	probe := factory(t)
	caps := probe.Capabilities()
	name := probe.Name()
	_ = probe.Close()

	// What the store already held before the suite ran. The assertion at the
	// end is that the suite left nothing NEW, not that the store is empty: a
	// shared cluster carries other work's resources, and failing on those
	// makes a check people learn to ignore.
	before := datastoreSnapshot(t, factory)
	created := newCreatedSet()

	for _, b := range datastoreBehaviors {
		b := b
		t.Run(b.Name, func(t *testing.T) {
			if reason := datastoreSkip(b, caps); reason != "" {
				// Named, never silent. A reviewer reading the output has to be
				// able to see which guarantee this store does not make.
				t.Skipf("skipped: %s declares %s", orThisDatastore(name), reason)
			}
			ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
			defer cancel()
			runDatastoreBehavior(ctx, t, b.Name, factory, created)
		})
	}

	if t.Failed() {
		// A failing behaviour legitimately leaves things behind for
		// inspection, and reporting that as a second failure buries the first.
		return
	}
	for r := range datastoreSnapshot(t, factory) {
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
		t.Errorf("the suite left %s behind; every resource a behaviour creates must be "+
			"removed when it finishes, whether it passed or not", r)
	}
}

// datastoreSkip decides whether a behaviour can run against these
// capabilities, and returns the sentence the skip line quotes.
//
// The no golden case is a requirement rather than the absence of one, and that
// is the point of it. A store that holds no golden is making a real promise:
// asking it for one answers ErrNoGolden. Treating that as "nothing to check
// here" is how a cache ends up returning a generic error that the engine
// cannot tell from a broken connection.
func datastoreSkip(b Behavior, caps provider.DatastoreCaps) string {
	switch b.Requires {
	case requiresGolden:
		if !caps.Golden {
			return "no golden"
		}
	case requiresNoGolden:
		if caps.Golden {
			return "a golden"
		}
	case requiresBranching:
		if !caps.Branching {
			return "no branching"
		}
	case requiresGoldenAndBranch:
		if !caps.Golden || !caps.Branching {
			return "no golden or no branching"
		}
	}
	return ""
}

func orThisDatastore(name string) string {
	if name == "" {
		return "this datastore"
	}
	return name
}

// datastoreSnapshot records what the store owns, as comparable strings.
func datastoreSnapshot(t *testing.T, factory DatastoreFactory) map[string]bool {
	t.Helper()
	d := factory(t)
	defer func() { _ = d.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	items, err := d.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	out := make(map[string]bool, len(items))
	for _, r := range items {
		out[r.Kind+" "+r.ID] = true
	}
	return out
}

// dsHarness gives one behaviour a datastore and the helpers it needs.
type dsHarness struct {
	t       *testing.T
	d       provider.Datastore
	created *createdSet
	// masked and verified record what the refresh callbacks were asked to do,
	// which is how the suite proves a store actually called them rather than
	// publishing a version it never checked.
	masked     int
	verified   int
	failVerify bool
}

func runDatastoreBehavior(ctx context.Context, t *testing.T, name string, factory DatastoreFactory, created *createdSet) {
	h := &dsHarness{t: t, d: factory(t), created: created}
	t.Cleanup(func() { _ = h.d.Close() })

	switch name {
	case "Name_IsNotEmpty":
		h.nameIsNotEmpty()
	case "Capabilities_AreSelfConsistent":
		h.capabilitiesAreSelfConsistent()
	case "Refresh_SaysErrNoGoldenWhenItHoldsNone":
		h.refreshSaysErrNoGolden(ctx)
	case "Refresh_ProducesAVerifiedGolden":
		h.refreshProducesAVerifiedGolden(ctx)
	case "Refresh_CallsMaskThenVerify":
		h.refreshCallsMaskThenVerify(ctx)
	case "Refresh_RefusesToPublishWhenVerificationFails":
		h.refreshRefusesWhenVerificationFails(ctx)
	case "Branch_IsIdempotentByEnvironment":
		h.branchIsIdempotent(ctx)
	case "Branch_RefusesAnUnverifiedGolden":
		h.branchRefusesUnverified(ctx)
	case "Destroy_RemovesTheBranch":
		h.destroyRemovesTheBranch(ctx)
	case "Destroy_OfSomethingAlreadyGoneSucceeds":
		h.destroyTwiceSucceeds(ctx)
	case "ConnString_IsASecret":
		h.connStringIsASecret(ctx)
	case "Inventory_ListsLiveResources":
		h.inventoryListsLiveResources(ctx)
	case "Health_ReportsAReachableBranch":
		h.healthReportsReachable(ctx)
	case "Health_ReportsADestroyedBranch":
		h.healthReportsDestroyed(ctx)
	case "Cancellation_LeavesNoUntrackedResource":
		h.cancellationLeavesNothingUntracked(ctx)
	default:
		t.Fatalf("conformance: no implementation for datastore behavior %q", name)
	}
}

func (h *dsHarness) nameIsNotEmpty() {
	if strings.TrimSpace(h.d.Name()) == "" {
		h.t.Fatal("the datastore has no name; every error and every line of the fidelity " +
			"report about this store quotes it, and an unnamed store is one nobody can act on")
	}
}

func (h *dsHarness) capabilitiesAreSelfConsistent() {
	caps := h.d.Capabilities()
	if strings.TrimSpace(caps.Engine) == "" {
		h.t.Error("the datastore declares no engine; the manifest names an engine and the " +
			"engine is how a declaration is matched to an implementation")
	}
	if caps.CopyOnWrite && !caps.Branching {
		h.t.Error("copy on write is declared and branching is not; copy on write is a " +
			"statement about how a branch is made, so it says nothing without one")
	}
	if caps.CopyOnWrite && !caps.Golden {
		h.t.Error("copy on write is declared and no golden is; a branch shares storage " +
			"with the golden it came from, and there is none to share with")
	}
}

// refreshSaysErrNoGolden is the behaviour a cache has to pass.
//
// A store that is CORRECT to start empty still has to answer the question. The
// engine asks every store for a golden and decides what to do from the answer,
// and ErrNoGolden is a declared stance while a generic error is a fault: the
// first is reported as a store that holds nothing on purpose and the second
// stops the refresh.
func (h *dsHarness) refreshSaysErrNoGolden(ctx context.Context) {
	gv, err := h.d.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)
	if err == nil {
		h.t.Fatalf("the datastore declares no golden and a refresh succeeded, publishing %q; "+
			"a caller cannot tell that from a store that does hold one", gv.ID)
	}
	if !errors.Is(err, provider.ErrNoGolden) {
		h.t.Fatalf("the datastore declares no golden and a refresh failed with %v, which "+
			"errors.Is does not match provider.ErrNoGolden; a declared stance and a broken "+
			"connection would then be the same result to the engine", err)
	}
	if gv.ID != "" {
		h.t.Errorf("the refresh reported ErrNoGolden and still published %s", gv.ID)
	}
}

func (h *dsHarness) refreshProducesAVerifiedGolden(ctx context.Context) {
	gv := h.refresh(ctx)
	if !gv.Verified {
		h.t.Fatal("the published version is not verified; an unverified golden is one " +
			"nothing may branch, and publishing it breaks the product's central promise")
	}
	if gv.Attestation == "" {
		h.t.Error("the published version carries no attestation, so nothing records what " +
			"was scanned and found")
	}
}

func (h *dsHarness) refreshCallsMaskThenVerify(ctx context.Context) {
	h.refresh(ctx)
	if h.masked == 0 {
		h.t.Error("the refresh never called Mask, so the version it published holds " +
			"production's own data")
	}
	if h.verified == 0 {
		h.t.Error("the refresh never called Verify, so nothing checked the masking it claims " +
			"to have applied")
	}
}

func (h *dsHarness) refreshRefusesWhenVerificationFails(ctx context.Context) {
	h.failVerify = true
	gv, err := h.d.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)
	if err == nil {
		h.t.Fatalf("verification failed and the refresh reported success, publishing %q", gv.ID)
	}
	if gv.ID != "" {
		h.t.Fatalf("verification failed and the refresh still published %s; a version that "+
			"exists after a failed scan is one something else can branch", gv.ID)
	}
}

func (h *dsHarness) branchIsIdempotent(ctx context.Context) {
	gv := h.refresh(ctx)
	const env = "env_dsconformance0001"
	first := h.branch(ctx, gv.ID, env)
	second := h.branch(ctx, gv.ID, env)
	if first.ProviderRef != second.ProviderRef {
		h.t.Fatalf("branching twice for one environment produced two branches, %q and %q; "+
			"the engine retries after a timeout and a retry that creates a second resource "+
			"is how an orphan is made", first.ProviderRef, second.ProviderRef)
	}
	h.destroy(ctx, second)
}

func (h *dsHarness) branchRefusesUnverified(ctx context.Context) {
	h.failVerify = true
	gv, err := h.d.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)

	// Every path asserts something, which is the mistake the database suite
	// made in this exact behaviour: the whole body sat inside a condition that
	// is false for a correct provider, so it passed having checked nothing.
	if err != nil || gv.ID == "" {
		if err == nil {
			h.t.Fatal("the refresh published nothing and reported no error, so a caller " +
				"cannot tell a refusal from a success")
		}
		if gv.ID != "" {
			h.t.Fatalf("the refresh reported an error and still published %s", gv.ID)
		}
		// Refused at the refresh, which is the stronger place to refuse:
		// there is then no unverified version in existence to branch.
		return
	}

	b, branchErr := h.d.Branch(ctx, gv.ID, "env_dsconformance0002")
	if branchErr == nil {
		h.destroy(ctx, b)
		h.t.Fatal("an unverified golden was branched; that branch holds production's own " +
			"data and nothing scanned it")
	}
	if !errors.Is(branchErr, aferrors.Coded(aferrors.AFMSK001)) {
		h.t.Errorf("branching an unverified golden failed with %v, and must fail with "+
			"AF-MSK-001; the engine reads the code to tell a masking refusal from a fault",
			branchErr)
	}
}

func (h *dsHarness) destroyRemovesTheBranch(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_dsconformance0003")
	h.destroy(ctx, b)

	items, err := h.d.Inventory(ctx)
	if err != nil {
		h.t.Fatalf("Inventory: %v", err)
	}
	for _, r := range items {
		if r.ID == b.ProviderRef {
			h.t.Fatalf("the destroyed branch %s is still in the inventory; teardown would "+
				"report success over a store that is still there", r.ID)
		}
	}
}

func (h *dsHarness) destroyTwiceSucceeds(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_dsconformance0004")
	h.destroy(ctx, b)
	if err := h.d.Destroy(ctx, b); err != nil {
		h.t.Fatalf("destroying an already destroyed branch failed with %v; teardown retries, "+
			"and an error on the second attempt turns a completed teardown into a stuck one", err)
	}
}

func (h *dsHarness) connStringIsASecret(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_dsconformance0005")
	defer h.destroy(ctx, b)

	conn, err := h.d.ConnString(ctx, b)
	if err != nil {
		h.t.Fatalf("ConnString: %v", err)
	}
	if conn.IsZero() {
		h.t.Fatal("ConnString returned an empty value, so nothing can reach the branch")
	}
	// The type is what stops a connection string reaching a log by accident,
	// so the suite checks the rendering rather than trusting the signature. It
	// looks for the revealed value itself rather than for a scheme, because a
	// datastore's address can be a Redis URL, a Kafka broker list or an HTTP
	// endpoint and none of them share a prefix.
	rendered := fmt.Sprintf("%v %s %q", conn, conn, conn)
	if strings.Contains(rendered, conn.Reveal()) {
		h.t.Fatalf("the connection string rendered its own plaintext rather than the " +
			"redaction marker, so it reaches any log that formats it")
	}
}

func (h *dsHarness) inventoryListsLiveResources(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_dsconformance0006")
	defer h.destroy(ctx, b)

	items, err := h.d.Inventory(ctx)
	if err != nil {
		h.t.Fatalf("Inventory: %v", err)
	}
	for _, r := range items {
		if r.ID == b.ProviderRef {
			return
		}
	}
	h.t.Fatalf("the live branch %s is not in the inventory; the leak detector compares the "+
		"inventory against the journal, so a store that under reports cannot be checked "+
		"for leaks at all", b.ProviderRef)
}

func (h *dsHarness) healthReportsReachable(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_dsconformance0007")
	defer h.destroy(ctx, b)

	got, err := h.d.Health(ctx, b)
	if err != nil {
		h.t.Fatalf("Health: %v", err)
	}
	if !got.Reachable {
		h.t.Fatalf("a live branch reports unreachable: %s", got.Detail)
	}
}

func (h *dsHarness) healthReportsDestroyed(ctx context.Context) {
	gv := h.refresh(ctx)
	b := h.branch(ctx, gv.ID, "env_dsconformance0008")
	h.destroy(ctx, b)

	got, err := h.d.Health(ctx, b)
	if err != nil {
		h.t.Fatalf("Health returned an error for a destroyed branch: %v. Report it "+
			"unreachable instead: teardown asks for health, and an error here makes a "+
			"successful teardown look like a failure.", err)
	}
	if got.Reachable {
		h.t.Fatal("a destroyed branch reports reachable, so an environment would be told to " +
			"connect to something that is gone")
	}
}

// cancellationLeavesNothingUntracked is the behaviour orphans come from.
//
// A cancelled create must leave either no resource or one the inventory
// reports. Anything else is a resource nothing knows about, which is what a
// leak is.
func (h *dsHarness) cancellationLeavesNothingUntracked(ctx context.Context) {
	gv := h.refresh(ctx)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	b, err := h.d.Branch(cancelled, gv.ID, "env_dsconformance0009")
	if err == nil {
		// Fast enough to finish before the cancellation was noticed, which is
		// allowed. What is not allowed is a branch nothing can find.
		h.created.add(b.ProviderRef)
		defer h.destroy(ctx, b)
	}
	if b.ProviderRef == "" {
		return
	}

	items, invErr := h.d.Inventory(ctx)
	if invErr != nil {
		h.t.Fatalf("Inventory: %v", invErr)
	}
	for _, r := range items {
		if r.ID == b.ProviderRef {
			return
		}
	}
	h.t.Fatalf("a cancelled branch left %s behind and the inventory does not report it; "+
		"a resource nothing can enumerate is one nothing can clean up", b.ProviderRef)
}

// spec builds a refresh specification whose callbacks record what happened.
//
// It is a separate function from the database suite's spec rather than a
// shared one, because that one carries a Load closure for subsetting and a
// Postgres major version, and neither means anything to a datastore. Sharing
// would put fields in front of a provider author that this suite never sets.
func (h *dsHarness) spec() provider.GoldenSpec {
	hash := randomRulesHash(h.t)
	return provider.GoldenSpec{
		SourceURL: secrets.New("antifailure://conformance/source"),
		RulesHash: hash,
		// Distinct per refresh, for the same reason the database suite's is:
		// this runs against shared clusters, so a fixed value would let one
		// run's assertion pass on another run's golden.
		Provenance: "gp1-dsconformance-" + hash,
		Mask: func(_ context.Context, _ secrets.Value) error {
			h.masked++
			return nil
		},
		Verify: func(_ context.Context, _ secrets.Value) (string, error) {
			h.verified++
			if h.failVerify {
				return "", aferrors.Coded(aferrors.AFMSK002,
					"detector", "email", "table", "events", "column", "email")
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

// refresh publishes a golden and schedules it for removal.
func (h *dsHarness) refresh(ctx context.Context) provider.GoldenVersion {
	h.t.Helper()
	gv, err := h.d.RefreshGolden(ctx, h.spec())
	h.trackGolden(gv.ID)
	if err != nil {
		h.t.Fatalf("RefreshGolden: %v", err)
	}
	if gv.ID == "" {
		h.t.Fatal("RefreshGolden reported success and published no version")
	}
	return gv
}

func (h *dsHarness) branch(ctx context.Context, version, envID string) provider.Branch {
	h.t.Helper()
	b, err := h.d.Branch(ctx, version, envID)
	if err != nil {
		h.t.Fatalf("Branch: %v", err)
	}
	h.created.add(b.ProviderRef)
	return b
}

func (h *dsHarness) destroy(ctx context.Context, b provider.Branch) {
	h.t.Helper()
	if err := h.d.Destroy(ctx, b); err != nil {
		h.t.Errorf("Destroy: %v", err)
	}
}

// trackGolden records a golden so the leak check at the end can tell this
// suite's leftovers from another package's, and removes it when the behaviour
// finishes.
//
// A datastore has no DestroyGolden in its interface, which is the one place
// this suite cannot clean up after itself. It says so rather than pretending:
// the identifier is tracked so a leak is REPORTED, and the report names the
// version rather than leaving somebody to find it.
func (h *dsHarness) trackGolden(id string) {
	if id == "" {
		return
	}
	h.created.add(id)
}

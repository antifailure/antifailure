// Package fakes holds the shared test doubles CONTRIBUTING.md promises: fakes
// for the engine's external dependencies that can inject faults.
//
// The reason a fault injector matters more than a fake matters is worth stating
// plainly, because two lanes arrived at it independently while this was being
// written. A conformance suite nobody has watched fail is not evidence. It is a
// list of assertions that might all be vacuous, and the usual way an assertion
// becomes vacuous is undramatic: a helper starts skipping, a comparison starts
// comparing a value against itself, a behaviour asserts on state an earlier
// behaviour already established. Every one of those still prints ok.
//
// So this package's centrepiece is [Break], which takes a provider that works
// and returns one that violates exactly one guarantee. Point the conformance
// suite at it and the suite must go red, in the named behaviour and for the
// named reason. If it stays green, the suite was not checking that guarantee,
// and you have learned something the green run could never tell you.
//
// There are two providers to break, and the second exists because the first
// could not reach five of the twenty four behaviours. [InMemoryDatabase] needs
// nothing and answers nineteen. [NewPostgresDatabase] keeps real rows in real
// databases on a real server, and answers the five about isolation, reset and
// what a branch actually holds, which are the ones a fake without storage can
// only pretend to have checked.
package fakes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Fault names one guarantee to break.
//
// Each maps to a behaviour in the conformance suite. Adding a fault without a
// behaviour that catches it is the interesting case: it means the suite has a
// hole, and the fault is the proof.
type Fault string

const (
	// CapabilitiesContradictThemselves declares a database provider that
	// cannot branch, with no supported Postgres versions and no expected
	// branch latency. Every one of those is separately meaningless, and a
	// provider whose own description does not hang together is one whose
	// skips cannot be trusted either.
	CapabilitiesContradictThemselves Fault = "capabilities-contradict-themselves"

	// PublishesUnverifiedGolden makes a refresh publish a version whose
	// Verified is false. This is the product's central promise, so if the
	// suite does not catch this it catches nothing.
	PublishesUnverifiedGolden Fault = "publishes-unverified-golden"

	// SkipsMasking makes a refresh verify without masking first. The data is
	// still real; only the claim about it changed.
	SkipsMasking Fault = "skips-masking"

	// PublishesWhenVerificationFails makes a refresh publish even though the
	// verification scan returned an error.
	PublishesWhenVerificationFails Fault = "publishes-when-verification-fails"

	// RefusesWithoutSayingSo makes a refresh whose verification failed return
	// no version AND no error, so a caller cannot tell a refusal from a
	// success and carries on as though a golden exists.
	//
	// It exists to prove that Branch_RefusesAnUnverifiedGolden actually
	// asserts something on the path where the provider refuses to publish.
	// That path used to assert nothing at all, which is why the behaviour
	// passed for every correct provider while checking nothing.
	RefusesWithoutSayingSo Fault = "refuses-without-saying-so"

	// ListOmitsWhatWasPublished drops the newest version from the listing.
	// Selection reads the listing, so a version that exists and does not list
	// is a full refresh on every command with nothing explaining why.
	ListOmitsWhatWasPublished Fault = "list-omits-what-was-published"

	// ListDropsTheProvenance accepts the provenance on a refresh and returns
	// it blank from the listing. The engine refuses to branch a version whose
	// provenance is not this project's, so this turns every golden the
	// provider holds into an unbranchable one.
	ListDropsTheProvenance Fault = "list-drops-the-provenance"

	// BranchLosesTheGoldensRows creates the branch and then empties it, so the
	// environment gets a schema and no data. This is the shape of the bug the
	// whole product exists to prevent: an environment that looks like a twin
	// and holds nothing.
	BranchLosesTheGoldensRows Fault = "branch-loses-the-goldens-rows"

	// BranchIsNotIdempotent makes a second Branch for the same environment
	// create a second database. This is how orphans are made: the engine
	// retries after a timeout and the retry leaves a resource nothing owns.
	BranchIsNotIdempotent Fault = "branch-is-not-idempotent"

	// BranchAcceptsUnverified drops the refusal that keeps unmasked data out
	// of an environment.
	BranchAcceptsUnverified Fault = "branch-accepts-unverified"

	// BranchAcceptsAMissingGolden hands back a branch for a version that does
	// not exist, so an environment starts against a database nothing filled.
	BranchAcceptsAMissingGolden Fault = "branch-accepts-a-missing-golden"

	// BranchSharesTheGoldensStorage points every branch at the golden itself,
	// so the first environment to write corrupts the source every later
	// environment is made from.
	BranchSharesTheGoldensStorage Fault = "branch-shares-the-goldens-storage"

	// BranchesShareOneDatabase points every branch of one golden at the first
	// branch's storage, so two environments running at once see each other's
	// writes.
	BranchesShareOneDatabase Fault = "branches-share-one-database"

	// ResetKeepsTheWrites reports a successful reset and changes nothing, so a
	// suite that resets between cases runs every case against the debris of
	// the last one.
	ResetKeepsTheWrites Fault = "reset-keeps-the-writes"

	// DestroyLeavesItInTheInventory reports a successful destroy and removes
	// nothing. The leak detector compares the inventory against the journal,
	// so this is a resource that outlives its environment for ever.
	DestroyLeavesItInTheInventory Fault = "destroy-leaves-it-in-the-inventory"

	// DestroyTwiceErrors makes destroying an absent branch an error rather
	// than a success. Teardown retries, so this strands resources.
	DestroyTwiceErrors Fault = "destroy-twice-errors"

	// ConnStringIsEmpty hands back a connection string with nothing in it,
	// which every caller then passes to a driver that reports something
	// unrelated.
	ConnStringIsEmpty Fault = "connstring-is-empty"

	// PooledEqualsDirect declares the pooled endpoint capability and returns
	// the direct string for both modes, so an application that asked for a
	// pooler silently gets none and exhausts the connection limit under load.
	PooledEqualsDirect Fault = "pooled-equals-direct"

	// InventoryHidesResources returns an empty inventory. The leak detector
	// compares inventory against the journal, so a provider that under-reports
	// is invisible to it, which is worse than one that over-reports.
	InventoryHidesResources Fault = "inventory-hides-resources"

	// HealthReportsALiveBranchUnreachable reports a working branch as down,
	// which fails an environment that is fine.
	HealthReportsALiveBranchUnreachable Fault = "health-reports-a-live-branch-unreachable"

	// HealthErrorsOnDestroyed makes Health return an error for a branch that
	// is gone rather than reporting it unreachable.
	HealthErrorsOnDestroyed Fault = "health-errors-on-destroyed"

	// IgnoresTheDeclaredBranchLimit declares a limit and branches past it, so
	// the number the provider publishes about itself is not a number anything
	// enforces.
	IgnoresTheDeclaredBranchLimit Fault = "ignores-the-declared-branch-limit"

	// CancellationLeavesAnUntrackedResource creates the branch anyway when the
	// context is already cancelled, reports the failure with no identifier,
	// and leaves the resource in the inventory. Nothing owns it and nothing
	// will ever remove it.
	CancellationLeavesAnUntrackedResource Fault = "cancellation-leaves-an-untracked-resource"

	// GoldenGCDropsAReferencedVersion allows destroying a golden that a live
	// branch was created from.
	GoldenGCDropsAReferencedVersion Fault = "goldengc-drops-a-referenced-version"

	// RefreshRebuildsExistingBranches remakes every live branch from the new
	// version, so an environment that branched an hour ago silently loses
	// everything it has done since.
	RefreshRebuildsExistingBranches Fault = "refresh-rebuilds-existing-branches"

	// RefreshReusesTheVersionIdentifier returns the previous version's
	// identifier from a later refresh. Versions are immutable, so two
	// different sets of data answering to one name is how a cleanup destroys
	// the golden something else is about to branch.
	RefreshReusesTheVersionIdentifier Fault = "refresh-reuses-the-version-identifier"
)

// Faults returns every fault, sorted, so a test can table drive over all of
// them and a new one is covered the moment it is declared rather than when
// somebody remembers to add it to a list.
func Faults() []Fault {
	out := make([]Fault, 0, len(catches))
	for f := range catches {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// catches is the one table. Faults and Catches both read it, so a fault cannot
// exist in one and not the other, which is a disagreement a table driven test
// silently skips rather than reports.
var catches = map[Fault]string{
	CapabilitiesContradictThemselves:      "Capabilities_AreSelfConsistent",
	PublishesUnverifiedGolden:             "Refresh_ProducesAVerifiedGolden",
	SkipsMasking:                          "Refresh_CallsMaskThenVerify",
	PublishesWhenVerificationFails:        "Refresh_RefusesToPublishWhenVerificationFails",
	RefusesWithoutSayingSo:                "Branch_RefusesAnUnverifiedGolden",
	ListOmitsWhatWasPublished:             "List_ReturnsWhatWasCreated",
	ListDropsTheProvenance:                "List_ReturnsTheProvenanceThatWasRecorded",
	BranchLosesTheGoldensRows:             "Branch_ReadsAKnownRow",
	BranchIsNotIdempotent:                 "Branch_IsIdempotentByEnvironment",
	BranchAcceptsUnverified:               "Branch_RefusesAnUnverifiedGolden",
	BranchAcceptsAMissingGolden:           "Branch_RefusesAMissingGolden",
	BranchSharesTheGoldensStorage:         "Branch_IsIsolatedFromTheGolden",
	BranchesShareOneDatabase:              "Branch_IsIsolatedFromOtherBranches",
	ResetKeepsTheWrites:                   "Reset_ReturnsToGoldenState",
	DestroyLeavesItInTheInventory:         "Destroy_RemovesTheBranch",
	DestroyTwiceErrors:                    "Destroy_OfSomethingAlreadyGoneSucceeds",
	ConnStringIsEmpty:                     "ConnString_IsASecret",
	PooledEqualsDirect:                    "ConnString_PooledWorksWhenDeclared",
	InventoryHidesResources:               "Inventory_ListsLiveResources",
	HealthReportsALiveBranchUnreachable:   "Health_ReportsAReachableBranch",
	HealthErrorsOnDestroyed:               "Health_ReportsADestroyedBranch",
	IgnoresTheDeclaredBranchLimit:         "Concurrency_RespectsTheDeclaredLimit",
	CancellationLeavesAnUntrackedResource: "Cancellation_LeavesNoUntrackedResource",
	GoldenGCDropsAReferencedVersion:       "GoldenGC_RefusesAReferencedVersion",
	RefreshRebuildsExistingBranches:       "Refresh_DoesNotDisturbExistingBranches",
	RefreshReusesTheVersionIdentifier:     "Refresh_DoesNotDisturbExistingBranches",
}

// Catches maps each fault to the conformance behaviour that must fail when it
// is injected.
//
// This is the table a self test drives: inject the fault, run that behaviour,
// require red. Keeping it here rather than in the test means the claim "this
// behaviour checks this property" is written down next to the property.
func Catches() map[Fault]string {
	out := make(map[Fault]string, len(catches))
	for f, b := range catches {
		out[f] = b
	}
	return out
}

// NeedsRows names the conformance behaviours that read rows back through a
// connection string, and therefore need a provider with storage behind it.
//
// It is a property of the BEHAVIOUR rather than of the fault. Reset, for
// instance, is broken by a decorator that returns nil and does nothing, but
// the only way to see that it did nothing is to count rows.
//
// Named here rather than derived in a test, because a test that quietly
// skipped the ones it could not do would report a coverage it does not have,
// which is the failure this whole package exists to prevent.
func NeedsRows() map[string]bool {
	return map[string]bool{
		"Branch_ReadsAKnownRow":                  true,
		"Branch_IsIsolatedFromTheGolden":         true,
		"Branch_IsIsolatedFromOtherBranches":     true,
		"Reset_ReturnsToGoldenState":             true,
		"Refresh_DoesNotDisturbExistingBranches": true,
	}
}

// ConnString_IsASecret is falsifiable in part, and the part that is not is
// worth reading before trusting a green run.
//
// [ConnStringIsEmpty] breaks it, so the behaviour is not vacuous. But the
// assertion it is named for, that the string RENDERS as the redaction marker,
// cannot be broken through the interface at all: a provider hands back a
// secret.Value, whose String, GoString and Format all return the marker and
// whose only field is unexported, so there is no value of that type that
// renders its plaintext. The guarantee is enforced by the compiler rather than
// by the suite. That is vacuous in the good way, and it is still a different
// claim from "the suite would catch it breaking", which here it would not.

// Break wraps a provider that works and returns one that violates exactly one
// guarantee.
//
// One at a time is the point. A double that breaks several things at once
// proves only that the suite noticed something, and the useful question is
// which assertion did the noticing.
//
// Most faults are injected by decoration, because a decorator cannot
// accidentally change anything it does not mention. [ProviderFaults] names the
// ones it cannot, which are handed to the provider through injectFault. A
// provider that cannot host one gets [Uninjectable] rather than a quietly
// ordinary provider, because a fault that was silently not injected makes the
// suite look as though it caught nothing.
func Break(inner provider.Database, f Fault) provider.Database {
	if providerFaults[f] {
		inj, ok := inner.(interface{ injectFault(Fault) bool })
		if ok && inj.injectFault(f) {
			return inner
		}
		return Uninjectable{Fault: f, Provider: inner.Name()}
	}
	return &broken{Database: inner, fault: f}
}

// providerFaults are the faults no decorator can inject.
//
// Four of them are about storage, because isolation is a property of where the
// bytes live and a wrapper has no bytes. The fifth is about capabilities,
// which return no error and take no context, so a wrapper that wanted to
// contradict them would have to reimplement the interface rather than decorate
// it, and a reimplementation is a second provider whose differences are no
// longer one fault.
var providerFaults = map[Fault]bool{
	CapabilitiesContradictThemselves: true,
	BranchLosesTheGoldensRows:        true,
	BranchSharesTheGoldensStorage:    true,
	BranchesShareOneDatabase:         true,
	RefreshRebuildsExistingBranches:  true,
}

// ProviderFaults returns the faults a provider must host itself, sorted.
func ProviderFaults() []Fault {
	out := make([]Fault, 0, len(providerFaults))
	for f := range providerFaults {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Uninjectable is what [Break] returns when the provider cannot host the
// fault itself.
//
// Every method fails, loudly and identically. The alternative, returning the
// working provider, would make the conformance suite pass and look like proof
// that the fault was caught by nothing, when in fact the fault was never
// present. A self test must never be able to mistake "not injected" for
// "injected and survived".
type Uninjectable struct {
	Fault    Fault
	Provider string
}

func (u Uninjectable) err() error {
	return fmt.Errorf("fakes: the provider %q cannot host the fault %q, so nothing was injected "+
		"and a green run would prove nothing", u.Provider, u.Fault)
}

func (u Uninjectable) Name() string                { return "uninjectable" }
func (u Uninjectable) Capabilities() provider.Caps { return provider.Caps{} }
func (u Uninjectable) RefreshGolden(context.Context, provider.GoldenSpec) (provider.GoldenVersion, error) {
	return provider.GoldenVersion{}, u.err()
}
func (u Uninjectable) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	return nil, u.err()
}
func (u Uninjectable) DestroyGolden(context.Context, string) error { return u.err() }
func (u Uninjectable) Branch(context.Context, string, string) (provider.Branch, error) {
	return provider.Branch{}, u.err()
}
func (u Uninjectable) Reset(context.Context, provider.Branch) error   { return u.err() }
func (u Uninjectable) Destroy(context.Context, provider.Branch) error { return u.err() }
func (u Uninjectable) ConnString(context.Context, provider.Branch, provider.ConnMode) (secrets.Value, error) {
	return secrets.Value{}, u.err()
}
func (u Uninjectable) Inventory(context.Context) ([]provider.Resource, error) { return nil, u.err() }
func (u Uninjectable) Health(context.Context, provider.Branch) (provider.Health, error) {
	return provider.Health{}, u.err()
}
func (u Uninjectable) Close() error { return nil }

// broken embeds the working provider and overrides only the methods its fault
// touches. Embedding rather than reimplementing keeps every untouched
// behaviour genuinely untouched, so a red run points at the fault rather than
// at the double.
type broken struct {
	provider.Database
	fault Fault

	mu        sync.Mutex
	branched  map[string]int
	firstID   *provider.GoldenVersion
	abandoned []string
}

func (b *broken) is(f Fault) bool { return b.fault == f }

func (b *broken) count(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.branched == nil {
		b.branched = map[string]int{}
	}
	b.branched[key]++
	return b.branched[key]
}

func (b *broken) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	switch {
	case b.is(SkipsMasking):
		// Verification still runs, so the version looks published and checked.
		// Only the masking that verification is supposed to be checking has
		// been skipped.
		spec.Mask = func(context.Context, secrets.Value) error { return nil }

	case b.is(PublishesWhenVerificationFails):
		inner := spec.Verify
		spec.Verify = func(ctx context.Context, u secrets.Value) (string, error) {
			att, err := inner(ctx, u)
			if err != nil {
				// Swallow the failure and hand back an attestation anyway,
				// which is the realistic shape of this bug: somebody logged
				// the error and carried on.
				return "attestation-for-a-scan-that-failed", nil
			}
			return att, nil
		}
	}

	v, err := b.Database.RefreshGolden(ctx, spec)
	if err != nil {
		if b.is(RefusesWithoutSayingSo) {
			// Swallow the refusal. Nothing published, nothing said.
			return provider.GoldenVersion{}, nil
		}
		return v, err
	}
	if b.is(PublishesUnverifiedGolden) {
		v.Verified = false
	}
	if b.is(RefreshReusesTheVersionIdentifier) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.firstID == nil {
			first := v
			b.firstID = &first
		} else {
			// The data is the new version's. Only the name is the old one's,
			// which is what makes this survive a cursory look.
			v.ID = b.firstID.ID
		}
	}
	return v, nil
}

func (b *broken) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	out, err := b.Database.ListGoldens(ctx)
	if err != nil {
		return out, err
	}
	switch {
	case b.is(ListOmitsWhatWasPublished):
		// Newest first, so dropping the head drops what was just published
		// and leaves a listing that still looks populated.
		if len(out) > 0 {
			return out[1:], nil
		}
	case b.is(ListDropsTheProvenance):
		stripped := make([]provider.GoldenVersion, len(out))
		copy(stripped, out)
		for i := range stripped {
			stripped[i].Provenance = ""
		}
		return stripped, nil
	}
	return out, nil
}

func (b *broken) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	if b.is(CancellationLeavesAnUntrackedResource) && ctx.Err() != nil {
		b.mu.Lock()
		b.abandoned = append(b.abandoned, envID)
		b.mu.Unlock()
		// The failure is reported with no identifier, and the resource is
		// there. Nothing the caller holds names it.
		return provider.Branch{}, ctx.Err()
	}
	if b.is(BranchIsNotIdempotent) {
		if n := b.count("branch:" + envID); n > 1 {
			// A second environment identifier means a second database, which
			// is exactly the orphan the idempotence rule prevents.
			return b.Database.Branch(ctx, version, fmt.Sprintf("%s-dup%d", envID, n))
		}
	}

	br, err := b.Database.Branch(ctx, version, envID)
	if err == nil {
		return br, nil
	}
	// The refusals below live inside the working provider, so a decorator
	// cannot remove them by delegating; delegating is what a correct provider
	// does. Reporting success without creating anything is the honest shape of
	// each bug: a provider that decided the check was somebody else's job and
	// returned a handle regardless.
	switch {
	case b.is(BranchAcceptsUnverified) && errors.Is(err, aferrors.Coded(aferrors.AFMSK001)),
		b.is(BranchAcceptsAMissingGolden) && errors.Is(err, aferrors.Coded(aferrors.AFDB004)),
		b.is(IgnoresTheDeclaredBranchLimit) && errors.Is(err, aferrors.Coded(aferrors.AFDB006)):
		return provider.Branch{
			EnvID:       envID,
			From:        version,
			ProviderRef: "branch-that-should-have-been-refused",
			CreatedAt:   time.Now(),
		}, nil
	}
	return br, err
}

func (b *broken) Reset(ctx context.Context, br provider.Branch) error {
	if b.is(ResetKeepsTheWrites) {
		// Reported successful, and nothing happened. The only way to see this
		// is to read the rows back.
		return nil
	}
	return b.Database.Reset(ctx, br)
}

func (b *broken) Destroy(ctx context.Context, br provider.Branch) error {
	if b.is(DestroyLeavesItInTheInventory) {
		return nil
	}
	if b.is(DestroyTwiceErrors) {
		if n := b.count("destroy:" + br.ProviderRef); n > 1 {
			return errors.New("AF-DB-999: that branch is already gone")
		}
	}
	return b.Database.Destroy(ctx, br)
}

func (b *broken) ConnString(ctx context.Context, br provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	if b.is(ConnStringIsEmpty) {
		return secrets.Value{}, nil
	}
	if b.is(PooledEqualsDirect) {
		mode = provider.ConnDirect
	}
	return b.Database.ConnString(ctx, br, mode)
}

func (b *broken) Inventory(ctx context.Context) ([]provider.Resource, error) {
	if b.is(InventoryHidesResources) {
		return nil, nil
	}
	out, err := b.Database.Inventory(ctx)
	if err != nil {
		return out, err
	}
	if b.is(CancellationLeavesAnUntrackedResource) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, env := range b.abandoned {
			// No environment identifier, which is the truthful shape of this
			// leak: the resource exists, the caller was told nothing, and
			// nothing in the inventory attributes it to anything either.
			_ = env
			out = append(out, provider.Resource{
				Kind: "branch",
				ID:   fmt.Sprintf("br-abandoned-by-a-cancelled-call-%d", i),
			})
		}
	}
	return out, nil
}

func (b *broken) Health(ctx context.Context, br provider.Branch) (provider.Health, error) {
	h, err := b.Database.Health(ctx, br)
	if b.is(HealthErrorsOnDestroyed) && !h.Reachable {
		return provider.Health{}, errors.New("AF-DB-999: could not reach that branch")
	}
	if b.is(HealthReportsALiveBranchUnreachable) && h.Reachable {
		return provider.Health{Reachable: false, Detail: "reported down while it was serving"}, nil
	}
	return h, err
}

func (b *broken) DestroyGolden(ctx context.Context, version string) error {
	if b.is(GoldenGCDropsAReferencedVersion) {
		// Drop the reference check by destroying without consulting live
		// branches. The inner provider is asked only after the guard it would
		// have applied is gone, so a referenced version disappears.
		if err := b.Database.DestroyGolden(ctx, version); err != nil {
			return nil
		}
		return nil
	}
	return b.Database.DestroyGolden(ctx, version)
}

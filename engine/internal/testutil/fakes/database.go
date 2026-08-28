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
package fakes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

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

	// BranchIsNotIdempotent makes a second Branch for the same environment
	// create a second database. This is how orphans are made: the engine
	// retries after a timeout and the retry leaves a resource nothing owns.
	BranchIsNotIdempotent Fault = "branch-is-not-idempotent"

	// BranchAcceptsUnverified drops the refusal that keeps unmasked data out
	// of an environment.
	BranchAcceptsUnverified Fault = "branch-accepts-unverified"

	// DestroyTwiceErrors makes destroying an absent branch an error rather
	// than a success. Teardown retries, so this strands resources.
	DestroyTwiceErrors Fault = "destroy-twice-errors"

	// InventoryHidesResources returns an empty inventory. The leak detector
	// compares inventory against the journal, so a provider that under-reports
	// is invisible to it, which is worse than one that over-reports.
	InventoryHidesResources Fault = "inventory-hides-resources"

	// HealthErrorsOnDestroyed makes Health return an error for a branch that
	// is gone rather than reporting it unreachable.
	HealthErrorsOnDestroyed Fault = "health-errors-on-destroyed"

	// GoldenGCDropsAReferencedVersion allows destroying a golden that a live
	// branch was created from.
	GoldenGCDropsAReferencedVersion Fault = "goldengc-drops-a-referenced-version"
)

// Faults returns every fault, sorted, so a test can table drive over all of
// them and a new one is covered the moment it is declared rather than when
// somebody remembers to add it to a list.
func Faults() []Fault {
	out := []Fault{
		PublishesUnverifiedGolden,
		SkipsMasking,
		PublishesWhenVerificationFails,
		RefusesWithoutSayingSo,
		BranchIsNotIdempotent,
		BranchAcceptsUnverified,
		DestroyTwiceErrors,
		InventoryHidesResources,
		HealthErrorsOnDestroyed,
		GoldenGCDropsAReferencedVersion,
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Catches maps each fault to the conformance behaviour that must fail when it
// is injected.
//
// This is the table a self test drives: inject the fault, run that behaviour,
// require red. Keeping it here rather than in the test means the claim "this
// behaviour checks this property" is written down next to the property.
func Catches() map[Fault]string {
	return map[Fault]string{
		PublishesUnverifiedGolden:       "Refresh_ProducesAVerifiedGolden",
		SkipsMasking:                    "Refresh_CallsMaskThenVerify",
		PublishesWhenVerificationFails:  "Refresh_RefusesToPublishWhenVerificationFails",
		RefusesWithoutSayingSo:          "Branch_RefusesAnUnverifiedGolden",
		BranchIsNotIdempotent:           "Branch_IsIdempotentByEnvironment",
		BranchAcceptsUnverified:         "Branch_RefusesAnUnverifiedGolden",
		DestroyTwiceErrors:              "Destroy_OfSomethingAlreadyGoneSucceeds",
		InventoryHidesResources:         "Inventory_ListsLiveResources",
		HealthErrorsOnDestroyed:         "Health_ReportsADestroyedBranch",
		GoldenGCDropsAReferencedVersion: "GoldenGC_RefusesAReferencedVersion",
	}
}

// ConnString_IsASecret has no fault here, and that is a finding rather than an
// omission. A provider hands back a [secrets.Value], whose String, GoString and
// Format all return the redacted marker, so there is no value of that type that
// renders its plaintext. The guarantee is enforced by the type rather than by
// the suite, which means the behaviour cannot be made to fail through the
// interface and is vacuous in the good way: the compiler already refuses what
// it is checking for. Worth knowing when reading a green run, because "the
// suite checks it" and "the suite could catch it breaking" are different
// claims, and here only the second is unavailable.

// Break wraps a provider that works and returns one that violates exactly one
// guarantee.
//
// One at a time is the point. A double that breaks several things at once
// proves only that the suite noticed something, and the useful question is
// which assertion did the noticing.
func Break(inner provider.Database, f Fault) provider.Database {
	return &broken{Database: inner, fault: f}
}

// broken embeds the working provider and overrides only the methods its fault
// touches. Embedding rather than reimplementing keeps every untouched
// behaviour genuinely untouched, so a red run points at the fault rather than
// at the double.
type broken struct {
	provider.Database
	fault Fault

	mu       sync.Mutex
	branched map[string]int
}

func (b *broken) is(f Fault) bool { return b.fault == f }

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
	return v, nil
}

func (b *broken) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	if b.is(BranchAcceptsUnverified) {
		// The refusal lives inside the working provider, so a decorator cannot
		// remove it by delegating; delegating is what a correct provider does.
		// Reporting success without creating anything is the honest shape of
		// this bug: a provider that decided the check was somebody else's job
		// and returned a handle regardless.
		br, err := b.Database.Branch(ctx, version, envID)
		if err != nil {
			return provider.Branch{
				EnvID:       envID,
				From:        version,
				ProviderRef: "branch-that-should-have-been-refused",
				CreatedAt:   time.Now(),
			}, nil
		}
		return br, nil
	}
	if b.is(BranchIsNotIdempotent) {
		b.mu.Lock()
		if b.branched == nil {
			b.branched = map[string]int{}
		}
		b.branched[envID]++
		n := b.branched[envID]
		b.mu.Unlock()
		if n > 1 {
			// A second environment identifier means a second database, which
			// is exactly the orphan the idempotence rule prevents.
			return b.Database.Branch(ctx, version, fmt.Sprintf("%s-dup%d", envID, n))
		}
	}
	return b.Database.Branch(ctx, version, envID)
}

func (b *broken) Destroy(ctx context.Context, br provider.Branch) error {
	if b.is(DestroyTwiceErrors) {
		b.mu.Lock()
		if b.branched == nil {
			b.branched = map[string]int{}
		}
		key := "destroy:" + br.ProviderRef
		b.branched[key]++
		n := b.branched[key]
		b.mu.Unlock()
		if n > 1 {
			return errors.New("AF-DB-999: that branch is already gone")
		}
	}
	return b.Database.Destroy(ctx, br)
}

func (b *broken) Inventory(ctx context.Context) ([]provider.Resource, error) {
	if b.is(InventoryHidesResources) {
		return nil, nil
	}
	return b.Database.Inventory(ctx)
}

func (b *broken) Health(ctx context.Context, br provider.Branch) (provider.Health, error) {
	h, err := b.Database.Health(ctx, br)
	if b.is(HealthErrorsOnDestroyed) && !h.Reachable {
		return provider.Health{}, errors.New("AF-DB-999: could not reach that branch")
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

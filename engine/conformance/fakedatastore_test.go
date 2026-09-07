package conformance

import (
	"context"
	"fmt"
	"sort"
	"sync"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A datastore that works, and can be built with exactly one thing wrong.
//
// In memory on purpose. The datastore suite checks the CONTRACT rather than
// the contents, so nothing here needs a container, and a self test that needs
// one is a self test that stops running on the machines where it matters most.

// The two shapes a real datastore comes in, and both have to pass.
//
// A cache is not a broken golden store. It declares Golden false and Branching
// false, answers ErrNoGolden, and that is a complete and correct
// implementation. Running only the golden shape would leave the behaviour
// written for the cache never executed against anything correct, which is the
// state a suite is in when it has a positive control for one shape and calls
// it a positive control.
const (
	dsShapeGolden = "golden"
	dsShapeCache  = "cache"
)

// The flaws the fake can be built with. Each is one thing a real datastore
// could plausibly get wrong.
const (
	dsFlawNone                     = ""
	dsFlawNoName                   = "no-name"
	dsFlawCopyOnWriteWithoutBranch = "copy-on-write-without-branching"
	dsFlawGenericNoGoldenError     = "generic-no-golden-error"
	dsFlawReturnsUnverified        = "returns-unverified"
	dsFlawSkipsMaskAndVerify       = "skips-mask-and-verify"
	dsFlawPublishesFailedVerify    = "publishes-failed-verify"
	dsFlawBranchNotIdempotent      = "branch-not-idempotent"
	dsFlawBranchesUnverified       = "branches-unverified"
	dsFlawDestroyLeavesTheBranch   = "destroy-leaves-the-branch"
	dsFlawDestroyTwiceErrors       = "destroy-twice-errors"
	dsFlawEmptyConnString          = "empty-conn-string"
	dsFlawRedactedIsThePlaintext   = "redacted-is-the-plaintext"
	dsFlawEmptyInventory           = "empty-inventory"
	dsFlawHealthNeverReachable     = "health-never-reachable"
	dsFlawHealthErrorsWhenGone     = "health-errors-when-gone"
	dsFlawCancelledBranchIsHidden  = "cancelled-branch-is-hidden"
	dsFlawLeaksOnRefresh           = "leaks-on-refresh"
	dsFlawGoldenSurvivesDestroy    = "golden-survives-destroy"
	dsFlawEmptyGoldenListing       = "empty-golden-listing"
	dsFlawListingDropsProvenance   = "listing-drops-provenance"
	dsFlawDestroyGoldenTwiceErrors = "destroy-golden-twice-errors"
)

// dsState is what the fake owns, shared across the instances one run builds.
//
// The suite asks for a fresh datastore per behaviour, the way it does of a
// real provider, and a real provider's resources outlive the handle. Keeping
// the state outside the handle is what makes Destroy_RemovesTheBranch and the
// end of suite leak check mean anything.
type dsState struct {
	mu       sync.Mutex
	seq      int
	goldens  map[string]golden // version id -> what was published
	branches map[string]string // environment id -> provider ref
	live     map[string]string // provider ref -> environment id
	goldenOf map[string]string // provider ref -> the version it was branched from
	orphans  map[string]bool   // resources nothing will ever remove
}

// golden is a published version, as the fake remembers it.
type golden struct {
	verified   bool
	provenance string
	rulesHash  string
}

func newDSState() *dsState {
	return &dsState{
		goldens:  map[string]golden{},
		branches: map[string]string{},
		live:     map[string]string{},
		goldenOf: map[string]string{},
		orphans:  map[string]bool{},
	}
}

type fakeDatastore struct {
	state *dsState
	shape string
	flaw  string
}

func newFakeDatastore(state *dsState, shape, flaw string) *fakeDatastore {
	return &fakeDatastore{state: state, shape: shape, flaw: flaw}
}

func (f *fakeDatastore) Name() string {
	if f.flaw == dsFlawNoName {
		return ""
	}
	if f.shape == dsShapeCache {
		return "fake-redis"
	}
	return "fake-clickhouse"
}

func (f *fakeDatastore) Capabilities() provider.DatastoreCaps {
	caps := provider.DatastoreCaps{Engine: "clickhouse", Branching: true, Golden: true}
	if f.shape == dsShapeCache {
		caps = provider.DatastoreCaps{Engine: "redis"}
	}
	if f.flaw == dsFlawCopyOnWriteWithoutBranch {
		caps.Branching = false
		caps.CopyOnWrite = true
	}
	return caps
}

func (f *fakeDatastore) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if err := ctx.Err(); err != nil {
		return provider.GoldenVersion{}, err
	}
	if !f.Capabilities().Golden {
		if f.flaw == dsFlawGenericNoGoldenError {
			return provider.GoldenVersion{}, fmt.Errorf("this store cannot do that")
		}
		return provider.GoldenVersion{}, provider.ErrNoGolden
	}

	attestation := `{"scanner":"fake","findings":0}`
	if f.flaw != dsFlawSkipsMaskAndVerify {
		if err := spec.Mask(ctx, secrets.New("antifailure://fake/candidate")); err != nil {
			return provider.GoldenVersion{}, err
		}
		got, err := spec.Verify(ctx, secrets.New("antifailure://fake/candidate"))
		if err != nil {
			if f.flaw != dsFlawPublishesFailedVerify && f.flaw != dsFlawBranchesUnverified {
				// The correct behaviour: publish nothing.
				return provider.GoldenVersion{}, err
			}
			return f.publish(spec, "", false), nil
		}
		attestation = got
	}
	return f.publish(spec, attestation, true), nil
}

// publish records a version and returns it.
func (f *fakeDatastore) publish(spec provider.GoldenSpec, attestation string, verified bool) provider.GoldenVersion {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.seq++
	id := fmt.Sprintf("gv_fake_%s_%d", spec.RulesHash, f.state.seq)
	f.state.goldens[id] = golden{
		verified: verified, provenance: spec.Provenance, rulesHash: spec.RulesHash,
	}
	if f.flaw == dsFlawLeaksOnRefresh {
		// A resource that carries the version's identifier and that nothing
		// removes, which is what the end of suite check is for. It is not a
		// behaviour: every behaviour passes and the suite still has to notice.
		f.state.orphans[id+"-scratch"] = true
	}
	return provider.GoldenVersion{
		ID:          id,
		Verified:    verified && f.flaw != dsFlawReturnsUnverified,
		Attestation: attestation,
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		ProviderRef: id,
	}
}

func (f *fakeDatastore) ListGoldens(_ context.Context) ([]provider.GoldenVersion, error) {
	if f.flaw == dsFlawEmptyGoldenListing {
		return nil, nil
	}
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	out := make([]provider.GoldenVersion, 0, len(f.state.goldens))
	for id, g := range f.state.goldens {
		gv := provider.GoldenVersion{
			ID: id, Verified: g.verified, Provenance: g.provenance,
			RulesHash: g.rulesHash, ProviderRef: id,
		}
		if f.flaw == dsFlawListingDropsProvenance {
			// The plausible one, and the reason the behaviour checks the
			// field rather than only the identifier. A listing that reports
			// the version and forgets what it was made for makes every
			// version unselectable, and it does it silently: the engine sees
			// a golden that belongs to nobody and refreshes again.
			gv.Provenance = ""
		}
		out = append(out, gv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (f *fakeDatastore) DestroyGolden(_ context.Context, version string) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	if _, present := f.state.goldens[version]; !present && f.flaw == dsFlawDestroyGoldenTwiceErrors {
		return fmt.Errorf("no such golden %s", version)
	}
	for env, ref := range f.state.branches {
		if f.state.goldenOf[ref] == version {
			// A version something still branches, refused. The fake carries
			// this because the interface promises it, and a fake that was
			// lenient where the contract is strict would let a provider be
			// written against the fake's rules.
			return fmt.Errorf("the golden %s is still branched by %s", version, env)
		}
	}
	if f.flaw == dsFlawGoldenSurvivesDestroy {
		return nil
	}
	delete(f.state.goldens, version)
	return nil
}

func (f *fakeDatastore) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	cancelled := ctx.Err() != nil
	if cancelled && f.flaw != dsFlawCancelledBranchIsHidden {
		return provider.Branch{}, ctx.Err()
	}

	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	g, ok := f.state.goldens[version]
	verified := g.verified
	if !ok {
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	if !verified && f.flaw != dsFlawBranchesUnverified {
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
	}
	if ref, exists := f.state.branches[envID]; exists && f.flaw != dsFlawBranchNotIdempotent {
		return provider.Branch{EnvID: envID, From: version, ProviderRef: ref}, nil
	}

	f.state.seq++
	ref := fmt.Sprintf("br_fake_%s_%d", envID, f.state.seq)
	f.state.branches[envID] = ref
	f.state.goldenOf[ref] = version
	if !cancelled || f.flaw != dsFlawCancelledBranchIsHidden {
		f.state.live[ref] = envID
	}
	return provider.Branch{EnvID: envID, From: version, ProviderRef: ref}, nil
}

func (f *fakeDatastore) Destroy(_ context.Context, b provider.Branch) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	_, present := f.state.live[b.ProviderRef]
	if !present && f.flaw == dsFlawDestroyTwiceErrors {
		return fmt.Errorf("no such branch %s", b.ProviderRef)
	}
	if f.flaw == dsFlawDestroyLeavesTheBranch {
		return nil
	}
	delete(f.state.live, b.ProviderRef)
	delete(f.state.goldenOf, b.ProviderRef)
	if f.state.branches[b.EnvID] == b.ProviderRef {
		delete(f.state.branches, b.EnvID)
	}
	return nil
}

func (f *fakeDatastore) ConnString(_ context.Context, b provider.Branch) (secrets.Value, error) {
	switch f.flaw {
	case dsFlawEmptyConnString:
		// The plausible one. A provider that has not finished ConnString
		// returns the zero Value, which compiles, satisfies the signature, and
		// hands an environment nothing to connect to.
		return secrets.Value{}, nil
	case dsFlawRedactedIsThePlaintext:
		// The contrived one, and it is here because the alternative is an
		// assertion with no control at all. secret.Value cannot be made to
		// print its plaintext, which is the entire point of the type, so the
		// only way to reach the observation the assertion looks for is a value
		// whose plaintext IS the redaction marker. Contrived is not the same
		// as pointless: the assertion exists because the suite checks the
		// rendering rather than trusting the signature, and a signature that
		// stopped returning secret.Value would make it violable for real.
		return secrets.New(secrets.Redacted), nil
	}
	return secrets.New("clickhouse://fake/" + b.ProviderRef), nil
}

func (f *fakeDatastore) Inventory(_ context.Context) ([]provider.Resource, error) {
	if f.flaw == dsFlawEmptyInventory {
		return nil, nil
	}
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	out := make([]provider.Resource, 0, len(f.state.live)+len(f.state.orphans))
	for ref, env := range f.state.live {
		out = append(out, provider.Resource{Kind: "branch", ID: ref, EnvID: env})
	}
	for id := range f.state.orphans {
		out = append(out, provider.Resource{Kind: "scratch", ID: id})
	}
	return out, nil
}

func (f *fakeDatastore) Health(_ context.Context, b provider.Branch) (provider.Health, error) {
	f.state.mu.Lock()
	_, live := f.state.live[b.ProviderRef]
	f.state.mu.Unlock()

	if !live {
		if f.flaw == dsFlawHealthErrorsWhenGone {
			return provider.Health{}, fmt.Errorf("no such branch %s", b.ProviderRef)
		}
		return provider.Health{Reachable: false, Detail: "the branch is gone"}, nil
	}
	if f.flaw == dsFlawHealthNeverReachable {
		return provider.Health{Reachable: false, Detail: "the fake always says this"}, nil
	}
	return provider.Health{Reachable: true, Detail: "the fake is answering"}, nil
}

func (f *fakeDatastore) Close() error { return nil }

package fakes

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// InMemoryDatabase is a provider.Database that keeps every guarantee the
// interface documents, without a database behind it.
//
// It exists so that [Break] has something correct to be measured against, and
// so that the conformance suite can be pointed at a deliberately broken
// provider WITHOUT needing a real service. That second use is the important
// one: a negative control that needs infrastructure gets skipped, and a
// skipped negative control is a false green rather than a proof.
//
// It cannot answer the five behaviours that read rows through a connection
// string, because there are no rows. [NeedsRows] names them, and
// [NewPostgresDatabase] is what answers those.
//
// Its capabilities are declared as widely as it can honestly support, which is
// not a cosmetic decision. A capability a fake declines is a behaviour the
// suite SKIPS, and a self test whose subject skipped is the false green this
// package exists to prevent. Reset is the one exception, because a reset can
// only be observed by reading rows back.
type InMemoryDatabase struct {
	// publishUnverified is an affordance rather than a fault, and the
	// distinction matters enough to state.
	//
	// Branch_RefusesAnUnverifiedGolden carries two rules: refuse to publish a
	// version that failed verification, and refuse to BRANCH one. The second
	// was unreachable for as long as every provider obeyed the first, because
	// a suite that never obtains an unverified version has nothing to hand to
	// Branch. That was recorded as a gap the suite could not close.
	//
	// This is the missing affordance: a provider that flags an unverified
	// version rather than withholding it, which the suite explicitly permits
	// (Refresh_RefusesToPublishWhenVerificationFails asserts on the flag, not
	// on the absence). It breaks nothing on its own, which the positive
	// control proves by running the whole suite green with it set, and it puts
	// an unverified version in front of Branch so that [BranchAcceptsUnverified]
	// finally has something to accept.
	publishUnverified bool

	mu        sync.Mutex
	fault     Fault
	goldens   map[string]provider.GoldenVersion
	branches  map[string]provider.Branch
	from      map[string]string
	destroyed map[string]bool
	seq       int
	refSeq    int
}

// NewInMemoryDatabase returns a provider that keeps every guarantee.
func NewInMemoryDatabase() *InMemoryDatabase {
	return &InMemoryDatabase{
		goldens:   map[string]provider.GoldenVersion{},
		branches:  map[string]provider.Branch{},
		from:      map[string]string{},
		destroyed: map[string]bool{},
	}
}

// PublishUnverified returns a provider that records a version whose
// verification failed rather than withholding it. See the field comment: it is
// the affordance that makes the branch side of
// Branch_RefusesAnUnverifiedGolden reachable, and it is not a fault.
func (d *InMemoryDatabase) PublishUnverified() *InMemoryDatabase {
	d.publishUnverified = true
	return d
}

// inMemoryBranchLimit is small on purpose. Concurrency_RespectsTheDeclaredLimit
// creates exactly this many branches before it tries one more, so a large
// number would buy nothing and cost time on every run.
const inMemoryBranchLimit = 3

func (d *InMemoryDatabase) Name() string { return "in-memory" }

// Capabilities declares everything an in-memory provider can honestly do.
//
// Reset is absent because a reset that cannot be read back is not observable,
// so the suite skips it here and names the missing capability as it goes.
// Everything else is declared, because an undeclared capability is a skipped
// behaviour and a skipped behaviour proves nothing.
func (d *InMemoryDatabase) Capabilities() provider.Caps {
	if d.fault == CapabilitiesContradictThemselves {
		// Branching false on a database provider, which the interface says is
		// not a database provider at all.
		return provider.Caps{}
	}
	return provider.Caps{
		Branching:             true,
		PooledEndpoints:       true,
		MaxConcurrentBranches: inMemoryBranchLimit,
		// A second, for a provider whose branch is a map insert.
		//
		// It was a millisecond, which was true and is no longer wise. Nothing
		// checked the field then; Branch_IsWithinTheDeclaredLatency checks it
		// now, and this fake runs in a subprocess on a machine with fifteen
		// other lanes on it, where a map insert that is descheduled crosses a
		// millisecond without anything being wrong. A declaration is a promise
		// the provider has to keep on a bad day, not a boast about a good one,
		// and the first thing the new assertion did was make this one honest.
		ExpectedBranchLatency: time.Second,
		SupportedVersions:     []int{17},
	}
}

func (d *InMemoryDatabase) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	candidate := secrets.New("postgres://candidate/in-memory")
	if spec.Mask != nil {
		if err := spec.Mask(ctx, candidate); err != nil {
			return provider.GoldenVersion{}, err
		}
	}
	att := ""
	verified := true
	if spec.Verify != nil {
		a, err := spec.Verify(ctx, candidate)
		switch {
		case err != nil && !d.publishUnverified:
			// The guarantee: a failed scan publishes nothing.
			return provider.GoldenVersion{}, err
		case err != nil:
			verified = false
		default:
			att = a
		}
	}

	d.seq++
	v := provider.GoldenVersion{
		ID:          fmt.Sprintf("gv_inmemory_%04d", d.seq),
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, d.seq, 0, time.UTC),
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    verified,
		Attestation: att,
	}
	d.goldens[v.ID] = v
	return v, nil
}

func (d *InMemoryDatabase) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]provider.GoldenVersion, 0, len(d.goldens))
	for _, v := range d.goldens {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (d *InMemoryDatabase) DestroyGolden(_ context.Context, version string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, gid := range d.from {
		if gid == version {
			return aferrors.Coded(aferrors.AFDB010, "version", version)
		}
	}
	delete(d.goldens, version)
	return nil
}

func (d *InMemoryDatabase) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	// A cancelled create must leave either no resource or one the journal
	// knows about, and creating nothing is the easier half to get right.
	if err := ctx.Err(); err != nil {
		return provider.Branch{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	v, ok := d.goldens[version]
	if !ok {
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	if !v.Verified {
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
	}
	if b, ok := d.branches[envID]; ok {
		return b, nil
	}
	if len(d.branches) >= inMemoryBranchLimit {
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
			"limit", fmt.Sprintf("%d", inMemoryBranchLimit))
	}
	// The reference counts up rather than being derived from the environment
	// identifier, and that is not cosmetic. A fake whose ProviderRef is a pure
	// function of the environment cannot express non-idempotence at all: two
	// creations agree by construction, so a control asserting they agree stays
	// green with the idempotence check removed. Found by mutation, in this
	// file, after the assertion had been written and believed.
	d.refSeq++
	b := provider.Branch{
		EnvID: envID, From: version,
		ProviderRef: fmt.Sprintf("br_%s_%03d", envID, d.refSeq),
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	d.branches[envID] = b
	d.from[b.ProviderRef] = version
	delete(d.destroyed, b.ProviderRef)
	return b, nil
}

// Reset is unsupported, and says so with the error the interface documents
// rather than pretending to succeed.
func (d *InMemoryDatabase) Reset(context.Context, provider.Branch) error {
	return provider.ErrUnsupported
}

func (d *InMemoryDatabase) Destroy(_ context.Context, b provider.Branch) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Destroying something already gone succeeds, because teardown retries.
	delete(d.branches, b.EnvID)
	delete(d.from, b.ProviderRef)
	d.destroyed[b.ProviderRef] = true
	return nil
}

// ConnString hands out a different string per mode, because the pooled
// endpoint capability is declared and a capability that returns the direct
// string is declared and not implemented.
func (d *InMemoryDatabase) ConnString(_ context.Context, b provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	if mode == provider.ConnPooled {
		return secrets.New("postgres://in-memory-pooler/" + b.ProviderRef), nil
	}
	return secrets.New("postgres://in-memory/" + b.ProviderRef), nil
}

func (d *InMemoryDatabase) Inventory(context.Context) ([]provider.Resource, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]provider.Resource, 0, len(d.branches)+len(d.goldens))
	for _, b := range d.branches {
		out = append(out, provider.Resource{Kind: "branch", ID: b.ProviderRef, EnvID: b.EnvID})
	}
	for _, v := range d.goldens {
		out = append(out, provider.Resource{Kind: "golden", ID: v.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (d *InMemoryDatabase) Health(_ context.Context, b provider.Branch) (provider.Health, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, live := d.from[b.ProviderRef]; !live {
		// Reported unreachable rather than returned as an error, which is the
		// rule teardown depends on.
		return provider.Health{Reachable: false, Detail: "that branch is gone"}, nil
	}
	return provider.Health{Reachable: true}, nil
}

func (d *InMemoryDatabase) Close() error { return nil }

// injectFault is the half of [Break] a decorator cannot do.
//
// Capabilities returns no error and takes no context, so a wrapper that wanted
// to contradict them would have to reimplement the interface rather than
// decorate it, and a reimplementation is a second provider whose differences
// are no longer one fault.
func (d *InMemoryDatabase) injectFault(f Fault) bool {
	if f != CapabilitiesContradictThemselves {
		return false
	}
	d.fault = f
	return true
}

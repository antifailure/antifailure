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

// InMemoryDatabase is a provider.Database that keeps every guarantee the
// interface documents, without a database behind it.
//
// It exists so that [Break] has something correct to be measured against, and
// so that the conformance suite can be pointed at a deliberately broken
// provider WITHOUT needing a real service. That second use is the important
// one: a negative control that needs infrastructure gets skipped, and a
// skipped negative control is a false green rather than a proof.
//
// It cannot answer the behaviours that read rows, because there are no rows.
// Those need a real database and they say so.
type InMemoryDatabase struct {
	mu        sync.Mutex
	goldens   map[string]provider.GoldenVersion
	branches  map[string]provider.Branch
	from      map[string]string
	destroyed map[string]bool
	seq       int
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

func (d *InMemoryDatabase) Name() string { return "in-memory" }

// Capabilities declares only what an in-memory provider can honestly do.
// Reset and pooled endpoints are absent so the suite skips the behaviours that
// would need real rows, naming the missing capability as it goes rather than
// passing silently.
func (d *InMemoryDatabase) Capabilities() provider.Caps {
	return provider.Caps{Branching: true}
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
	if spec.Verify != nil {
		a, err := spec.Verify(ctx, candidate)
		if err != nil {
			// The guarantee: a failed scan publishes nothing.
			return provider.GoldenVersion{}, err
		}
		att = a
	}

	d.seq++
	v := provider.GoldenVersion{
		ID:          fmt.Sprintf("gv_inmemory_%04d", d.seq),
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, d.seq, 0, time.UTC),
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    true,
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
			return errors.New("AF-DB-010: that version still has a branch")
		}
	}
	delete(d.goldens, version)
	return nil
}

func (d *InMemoryDatabase) Branch(_ context.Context, version, envID string) (provider.Branch, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	v, ok := d.goldens[version]
	if !ok {
		return provider.Branch{}, errors.New("AF-DB-004: no version with that identifier")
	}
	if !v.Verified {
		return provider.Branch{}, errors.New("AF-MSK-001: that version is not verified")
	}
	if b, ok := d.branches[envID]; ok {
		return b, nil
	}
	b := provider.Branch{
		EnvID: envID, From: version,
		ProviderRef: "br_" + envID,
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

func (d *InMemoryDatabase) ConnString(_ context.Context, b provider.Branch, _ provider.ConnMode) (secrets.Value, error) {
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

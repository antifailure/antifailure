package fakes_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// working is a provider.Database that keeps every guarantee the interface
// documents, in memory. It exists so the faults have something correct to be
// measured against: a fault is only meaningful as the difference between this
// and the broken wrapper.
//
// It holds no data and cannot answer the behaviours that read rows, which is
// why this file tests the injector rather than the conformance suite. Proving
// the suite catches these needs a provider with a real database behind it.
type working struct {
	goldens   map[string]provider.GoldenVersion
	branches  map[string]provider.Branch
	from      map[string]string // branch ref -> golden id
	destroyed map[string]bool
}

func newWorking() *working {
	return &working{
		goldens:   map[string]provider.GoldenVersion{},
		branches:  map[string]provider.Branch{},
		from:      map[string]string{},
		destroyed: map[string]bool{},
	}
}

func (w *working) Name() string                { return "working" }
func (w *working) Capabilities() provider.Caps { return provider.Caps{Branching: true, Reset: true} }

func (w *working) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	url := secrets.New("postgres://candidate")
	if spec.Mask != nil {
		if err := spec.Mask(ctx, url); err != nil {
			return provider.GoldenVersion{}, err
		}
	}
	att := ""
	if spec.Verify != nil {
		a, err := spec.Verify(ctx, url)
		if err != nil {
			// The guarantee: a failed scan publishes nothing.
			return provider.GoldenVersion{}, err
		}
		att = a
	}
	v := provider.GoldenVersion{
		ID:          "gv_1",
		CreatedAt:   time.Now(),
		RulesHash:   spec.RulesHash,
		Verified:    true,
		Attestation: att,
	}
	w.goldens[v.ID] = v
	return v, nil
}

func (w *working) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	out := make([]provider.GoldenVersion, 0, len(w.goldens))
	for _, v := range w.goldens {
		out = append(out, v)
	}
	return out, nil
}

func (w *working) DestroyGolden(_ context.Context, version string) error {
	for _, gid := range w.from {
		if gid == version {
			return errors.New("AF-DB-010: that version has a branch")
		}
	}
	delete(w.goldens, version)
	return nil
}

func (w *working) Branch(_ context.Context, version, envID string) (provider.Branch, error) {
	v, ok := w.goldens[version]
	if !ok {
		return provider.Branch{}, errors.New("AF-DB-004: no such version")
	}
	if !v.Verified {
		return provider.Branch{}, errors.New("AF-MSK-001: that version is not verified")
	}
	if b, ok := w.branches[envID]; ok {
		return b, nil
	}
	b := provider.Branch{EnvID: envID, From: version, ProviderRef: "br_" + envID, CreatedAt: time.Now()}
	w.branches[envID] = b
	w.from[b.ProviderRef] = version
	return b, nil
}

func (w *working) Reset(context.Context, provider.Branch) error { return nil }

func (w *working) Destroy(_ context.Context, b provider.Branch) error {
	// Destroying something already gone succeeds, because teardown retries.
	delete(w.branches, b.EnvID)
	delete(w.from, b.ProviderRef)
	w.destroyed[b.ProviderRef] = true
	return nil
}

func (w *working) ConnString(context.Context, provider.Branch, provider.ConnMode) (secrets.Value, error) {
	return secrets.New("postgres://user:pw@host/db"), nil
}

func (w *working) Inventory(context.Context) ([]provider.Resource, error) {
	out := make([]provider.Resource, 0, len(w.branches))
	for _, b := range w.branches {
		out = append(out, provider.Resource{Kind: "branch", ID: b.ProviderRef, EnvID: b.EnvID})
	}
	return out, nil
}

func (w *working) Health(_ context.Context, b provider.Branch) (provider.Health, error) {
	if w.destroyed[b.ProviderRef] {
		return provider.Health{Reachable: false, Detail: "gone"}, nil
	}
	return provider.Health{Reachable: true}, nil
}

func (w *working) Close() error { return nil }

func spec() provider.GoldenSpec {
	return provider.GoldenSpec{
		RulesHash: "abc",
		Mask:      func(context.Context, secrets.Value) error { return nil },
		Verify:    func(context.Context, secrets.Value) (string, error) { return "att", nil },
	}
}

// The control. Without this, a fault test proves nothing: every assertion below
// is "the broken one differs from the working one", and that is only meaningful
// if the working one actually keeps the guarantee.
func TestTheWorkingProviderKeepsEveryGuaranteeUnderTest(t *testing.T) {
	ctx := context.Background()
	w := newWorking()

	v, err := w.RefreshGolden(ctx, spec())
	if err != nil || !v.Verified {
		t.Fatalf("refresh should publish a verified version, got %+v err %v", v, err)
	}

	b1, err := w.Branch(ctx, v.ID, "env")
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	b2, _ := w.Branch(ctx, v.ID, "env")
	if b1.ProviderRef != b2.ProviderRef {
		t.Error("branching twice for one environment should return one branch")
	}
	if err := w.DestroyGolden(ctx, v.ID); err == nil {
		t.Error("destroying a referenced golden should be refused")
	}
	inv, _ := w.Inventory(ctx)
	if len(inv) != 1 {
		t.Errorf("inventory should report the live branch, got %d", len(inv))
	}
	if err := w.Destroy(ctx, b1); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := w.Destroy(ctx, b1); err != nil {
		t.Errorf("destroying twice should succeed, got %v", err)
	}
	h, err := w.Health(ctx, b1)
	if err != nil || h.Reachable {
		t.Errorf("a destroyed branch should report unreachable without erroring, got %+v err %v", h, err)
	}
}

func TestPublishesUnverifiedGolden(t *testing.T) {
	p := fakes.Break(newWorking(), fakes.PublishesUnverifiedGolden)
	v, err := p.RefreshGolden(context.Background(), spec())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if v.Verified {
		t.Fatal("the fault must publish a version that is not verified")
	}
}

func TestSkipsMasking(t *testing.T) {
	// The property is about the engine's own masking closure, not about
	// anything the provider records. A provider cannot tell whether the
	// function it was handed did any masking, which is exactly why the suite
	// has to check that the function it passed in was the one that ran.
	tracked := func() (provider.GoldenSpec, *bool, *bool) {
		masked, verified := false, false
		s := spec()
		s.Mask = func(context.Context, secrets.Value) error { masked = true; return nil }
		s.Verify = func(context.Context, secrets.Value) (string, error) { verified = true; return "att", nil }
		return s, &masked, &verified
	}

	s, masked, verified := tracked()
	if _, err := newWorking().RefreshGolden(context.Background(), s); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !*masked || !*verified {
		t.Fatalf("the working provider must call both, masked=%v verified=%v", *masked, *verified)
	}

	s, masked, verified = tracked()
	p := fakes.Break(newWorking(), fakes.SkipsMasking)
	if _, err := p.RefreshGolden(context.Background(), s); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if *masked {
		t.Fatal("the fault must stop the engine's masking rules from running")
	}
	if !*verified {
		t.Error("verification must still run, or the fault is too obvious to be interesting: " +
			"the dangerous version of this bug is the one that still produces an attestation")
	}
}

func TestPublishesWhenVerificationFails(t *testing.T) {
	s := spec()
	s.Verify = func(context.Context, secrets.Value) (string, error) {
		return "", errors.New("unmasked data found in column email")
	}

	if _, err := newWorking().RefreshGolden(context.Background(), s); err == nil {
		t.Fatal("the working provider must refuse to publish when verification fails")
	}

	p := fakes.Break(newWorking(), fakes.PublishesWhenVerificationFails)
	v, err := p.RefreshGolden(context.Background(), s)
	if err != nil {
		t.Fatalf("the fault must publish anyway, got %v", err)
	}
	if !v.Verified {
		t.Error("and it must look verified, which is what makes the bug dangerous")
	}
}

func TestBranchIsNotIdempotent(t *testing.T) {
	ctx := context.Background()
	w := newWorking()
	v, _ := w.RefreshGolden(ctx, spec())

	p := fakes.Break(w, fakes.BranchIsNotIdempotent)
	b1, err := p.Branch(ctx, v.ID, "env")
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	b2, err := p.Branch(ctx, v.ID, "env")
	if err != nil {
		t.Fatalf("second branch: %v", err)
	}
	if b1.ProviderRef == b2.ProviderRef {
		t.Fatal("the fault must produce a second resource for one environment, which is how an orphan is made")
	}
}

func TestBranchAcceptsUnverified(t *testing.T) {
	ctx := context.Background()
	w := newWorking()
	v, _ := w.RefreshGolden(ctx, spec())
	g := w.goldens[v.ID]
	g.Verified = false
	w.goldens[v.ID] = g

	if _, err := w.Branch(ctx, v.ID, "env"); err == nil {
		t.Fatal("the working provider must refuse an unverified version")
	}
	p := fakes.Break(w, fakes.BranchAcceptsUnverified)
	if _, err := p.Branch(ctx, v.ID, "env"); err != nil {
		t.Fatalf("the fault must hand back a branch anyway, got %v", err)
	}
}

func TestDestroyTwiceErrors(t *testing.T) {
	ctx := context.Background()
	w := newWorking()
	v, _ := w.RefreshGolden(ctx, spec())
	b, _ := w.Branch(ctx, v.ID, "env")

	p := fakes.Break(w, fakes.DestroyTwiceErrors)
	if err := p.Destroy(ctx, b); err != nil {
		t.Fatalf("the first destroy should succeed: %v", err)
	}
	if err := p.Destroy(ctx, b); err == nil {
		t.Fatal("the fault must make the retry an error, which is what strands resources")
	}
}

func TestInventoryHidesResources(t *testing.T) {
	ctx := context.Background()
	w := newWorking()
	v, _ := w.RefreshGolden(ctx, spec())
	if _, err := w.Branch(ctx, v.ID, "env"); err != nil {
		t.Fatal(err)
	}

	p := fakes.Break(w, fakes.InventoryHidesResources)
	inv, err := p.Inventory(ctx)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(inv) != 0 {
		t.Fatalf("the fault must under-report, got %d resources", len(inv))
	}
	real, _ := w.Inventory(ctx)
	if len(real) == 0 {
		t.Fatal("and the resource must really exist, or the fault is measuring nothing")
	}
}

func TestHealthErrorsOnDestroyed(t *testing.T) {
	ctx := context.Background()
	w := newWorking()
	v, _ := w.RefreshGolden(ctx, spec())
	b, _ := w.Branch(ctx, v.ID, "env")
	if err := w.Destroy(ctx, b); err != nil {
		t.Fatal(err)
	}

	p := fakes.Break(w, fakes.HealthErrorsOnDestroyed)
	if _, err := p.Health(ctx, b); err == nil {
		t.Fatal("the fault must error rather than report unreachable")
	}
}

func TestGoldenGCDropsAReferencedVersion(t *testing.T) {
	ctx := context.Background()
	w := newWorking()
	v, _ := w.RefreshGolden(ctx, spec())
	if _, err := w.Branch(ctx, v.ID, "env"); err != nil {
		t.Fatal(err)
	}

	if err := w.DestroyGolden(ctx, v.ID); err == nil {
		t.Fatal("the working provider must refuse a referenced version")
	}
	p := fakes.Break(w, fakes.GoldenGCDropsAReferencedVersion)
	if err := p.DestroyGolden(ctx, v.ID); err != nil {
		t.Fatalf("the fault must report success, got %v", err)
	}
}

// Every fault must name the behaviour that catches it, and every name must be
// a behaviour the suite actually has. A fault pointing at a behaviour that does
// not exist is a fault nothing will ever check.
func TestEveryFaultNamesARealConformanceBehavior(t *testing.T) {
	known := map[string]bool{}
	for _, b := range conformanceBehaviorNames() {
		known[b] = true
	}
	catches := fakes.Catches()
	for _, f := range fakes.Faults() {
		name, ok := catches[f]
		if !ok {
			t.Errorf("fault %q names no conformance behaviour, so nothing would catch it", f)
			continue
		}
		if !known[name] {
			t.Errorf("fault %q names behaviour %q, which the suite does not have", f, name)
		}
	}
	for f := range catches {
		found := false
		for _, known := range fakes.Faults() {
			if known == f {
				found = true
			}
		}
		if !found {
			t.Errorf("Catches names fault %q that Faults does not return, so a table driven test would skip it", f)
		}
	}
}

// A fault whose name does not describe what it does is a trap for the next
// person reading a red run.
func TestFaultNamesAreKebabCase(t *testing.T) {
	for _, f := range fakes.Faults() {
		s := string(f)
		if s != strings.ToLower(s) || strings.Contains(s, " ") || strings.Contains(s, "_") {
			t.Errorf("fault %q should be lower case and hyphenated", f)
		}
	}
}

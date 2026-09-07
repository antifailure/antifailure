package fakes_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The provider that keeps every guarantee is fakes.InMemoryDatabase itself.
//
// This file used to carry a second one, a `working` type that duplicated it
// method for method. Two implementations of the same guarantees drift, and
// this one already had: it returned AF-MSK-001 as a plain string where the
// real fake returns a catalogue error, so a decorator that distinguishes
// refusals by code was tested against a provider no conformance run uses. The
// duplicate is gone and the tests below measure a fault as the difference
// between the shipped fake and the shipped fault.

func spec() provider.GoldenSpec {
	return provider.GoldenSpec{
		RulesHash:  "abc",
		Provenance: "gp1-fakes",
		Mask:       func(context.Context, secrets.Value) error { return nil },
		Verify:     func(context.Context, secrets.Value) (string, error) { return "att", nil },
	}
}

func failingSpec() provider.GoldenSpec {
	s := spec()
	s.Verify = func(context.Context, secrets.Value) (string, error) {
		return "", errors.New("unmasked data found in column email")
	}
	return s
}

// The control. Without this, every fault test below is "the broken one differs
// from the working one", which is only meaningful if the working one actually
// keeps the guarantee.
func TestTheWorkingProviderKeepsEveryGuaranteeUnderTest(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()

	caps := w.Capabilities()
	if !caps.Branching || len(caps.SupportedVersions) == 0 || caps.ExpectedBranchLatency <= 0 {
		t.Errorf("the capabilities must hang together, got %+v", caps)
	}

	v, err := w.RefreshGolden(ctx, spec())
	if err != nil || !v.Verified {
		t.Fatalf("refresh should publish a verified version, got %+v err %v", v, err)
	}
	if v.Provenance != "gp1-fakes" {
		t.Errorf("the version should carry back the provenance it was made for, got %q", v.Provenance)
	}
	if _, err := w.RefreshGolden(ctx, failingSpec()); err == nil {
		t.Error("a refresh whose verification fails must publish nothing")
	}
	if _, err := w.Branch(ctx, "gv_19700101000000_deadbeef", "env"); !errors.Is(err, aferrors.Coded(aferrors.AFDB004)) {
		t.Errorf("branching a version that does not exist must fail with AF-DB-004, got %v", err)
	}

	b1, err := w.Branch(ctx, v.ID, "env")
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	b2, _ := w.Branch(ctx, v.ID, "env")
	if b1.ProviderRef != b2.ProviderRef {
		t.Error("branching twice for one environment should return one branch")
	}
	direct, _ := w.ConnString(ctx, b1, provider.ConnDirect)
	pooled, _ := w.ConnString(ctx, b1, provider.ConnPooled)
	if direct.Equal(pooled) {
		t.Error("the pooled endpoint capability is declared, so the two strings must differ")
	}
	if err := w.DestroyGolden(ctx, v.ID); err == nil {
		t.Error("destroying a referenced golden should be refused")
	}
	inv, _ := w.Inventory(ctx)
	if len(inv) == 0 {
		t.Error("inventory should report the live branch")
	}
	h, err := w.Health(ctx, b1)
	if err != nil || !h.Reachable {
		t.Errorf("a live branch should report reachable, got %+v err %v", h, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if cb, err := w.Branch(cancelled, v.ID, "env-cancelled"); err == nil || cb.ProviderRef != "" {
		t.Errorf("a cancelled create must make nothing, got %+v err %v", cb, err)
	}

	// Past the declared limit, which the capability publishes and this
	// enforces. Two more environments fill it; the third is refused.
	for _, env := range []string{"env-2", "env-3"} {
		if _, err := w.Branch(ctx, v.ID, env); err != nil {
			t.Fatalf("branch %s: %v", env, err)
		}
	}
	if _, err := w.Branch(ctx, v.ID, "env-4"); !errors.Is(err, aferrors.Coded(aferrors.AFDB006)) {
		t.Errorf("branching past the declared limit must fail with AF-DB-006, got %v", err)
	}

	if err := w.Destroy(ctx, b1); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := w.Destroy(ctx, b1); err != nil {
		t.Errorf("destroying twice should succeed, got %v", err)
	}
	h, err = w.Health(ctx, b1)
	if err != nil || h.Reachable {
		t.Errorf("a destroyed branch should report unreachable without erroring, got %+v err %v", h, err)
	}
}

// PublishUnverified is an affordance rather than a fault, and the difference
// is the whole reason the branch side of Branch_RefusesAnUnverifiedGolden is
// now reachable. It must produce a version, flag it honestly, and still refuse
// to branch it.
func TestPublishUnverifiedFlagsTheVersionAndStillRefusesToBranchIt(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase().PublishUnverified()

	v, err := w.RefreshGolden(ctx, failingSpec())
	if err != nil {
		t.Fatalf("the affordance must publish rather than refuse, got %v", err)
	}
	if v.ID == "" {
		t.Fatal("and it must be a version something could try to branch")
	}
	if v.Verified {
		t.Fatal("it must be flagged unverified, or the refusal below is about nothing")
	}
	if _, err := w.Branch(ctx, v.ID, "env"); !errors.Is(err, aferrors.Coded(aferrors.AFMSK001)) {
		t.Fatalf("branching an unverified version must fail with AF-MSK-001, got %v", err)
	}
}

func TestCapabilitiesContradictThemselves(t *testing.T) {
	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.CapabilitiesContradictThemselves)
	caps := p.Capabilities()
	if caps.Branching {
		t.Fatal("the fault must declare a database provider that cannot branch")
	}
	if len(caps.SupportedVersions) != 0 || caps.ExpectedBranchLatency != 0 {
		t.Error("and it must contradict itself in more than one place, or the behaviour " +
			"could pass by checking only the one")
	}
}

func TestPublishesUnverifiedGolden(t *testing.T) {
	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.PublishesUnverifiedGolden)
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
	if _, err := fakes.NewInMemoryDatabase().RefreshGolden(context.Background(), s); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !*masked || !*verified {
		t.Fatalf("the working provider must call both, masked=%v verified=%v", *masked, *verified)
	}

	s, masked, verified = tracked()
	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.SkipsMasking)
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
	if _, err := fakes.NewInMemoryDatabase().RefreshGolden(context.Background(), failingSpec()); err == nil {
		t.Fatal("the working provider must refuse to publish when verification fails")
	}

	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.PublishesWhenVerificationFails)
	v, err := p.RefreshGolden(context.Background(), failingSpec())
	if err != nil {
		t.Fatalf("the fault must publish anyway, got %v", err)
	}
	if !v.Verified {
		t.Error("and it must look verified, which is what makes the bug dangerous")
	}
}

func TestRefusesWithoutSayingSo(t *testing.T) {
	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.RefusesWithoutSayingSo)
	v, err := p.RefreshGolden(context.Background(), failingSpec())
	if err != nil {
		t.Fatalf("the fault must swallow the refusal, got %v", err)
	}
	if v.ID != "" {
		t.Fatal("and publish nothing, so a caller cannot tell a refusal from a success")
	}
}

func TestListOmitsWhatWasPublished(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	p := fakes.Break(w, fakes.ListOmitsWhatWasPublished)
	v, err := p.RefreshGolden(ctx, spec())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	real, _ := w.ListGoldens(ctx)
	if len(real) != 1 {
		t.Fatalf("the version must really exist, or the fault is measuring nothing; got %d", len(real))
	}
	listed, err := p.ListGoldens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, g := range listed {
		if g.ID == v.ID {
			t.Fatal("the fault must drop the version that was just published")
		}
	}
}

func TestListDropsTheProvenance(t *testing.T) {
	ctx := context.Background()
	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.ListDropsTheProvenance)
	v, err := p.RefreshGolden(ctx, spec())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if v.Provenance == "" {
		t.Fatal("the refresh must still return it, or the bug is visible at the call that made it")
	}
	listed, _ := p.ListGoldens(ctx)
	if len(listed) == 0 {
		t.Fatal("the fault must still list the version, only without its provenance")
	}
	for _, g := range listed {
		if g.Provenance != "" {
			t.Fatalf("the fault must blank the provenance in the listing, got %q", g.Provenance)
		}
	}
}

func TestBranchIsNotIdempotent(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
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
	w := fakes.NewInMemoryDatabase().PublishUnverified()
	v, err := w.RefreshGolden(ctx, failingSpec())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if _, err := w.Branch(ctx, v.ID, "env"); err == nil {
		t.Fatal("the working provider must refuse an unverified version")
	}
	p := fakes.Break(w, fakes.BranchAcceptsUnverified)
	if _, err := p.Branch(ctx, v.ID, "env"); err != nil {
		t.Fatalf("the fault must hand back a branch anyway, got %v", err)
	}
}

func TestBranchAcceptsAMissingGolden(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	if _, err := w.Branch(ctx, "gv_19700101000000_deadbeef", "env"); err == nil {
		t.Fatal("the working provider must refuse a version that does not exist")
	}
	p := fakes.Break(w, fakes.BranchAcceptsAMissingGolden)
	b, err := p.Branch(ctx, "gv_19700101000000_deadbeef", "env")
	if err != nil {
		t.Fatalf("the fault must hand back a branch anyway, got %v", err)
	}
	if b.ProviderRef == "" {
		t.Fatal("and it must look like a usable handle, which is what makes it dangerous")
	}
}

func TestIgnoresTheDeclaredBranchLimit(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	v, _ := w.RefreshGolden(ctx, spec())
	limit := w.Capabilities().MaxConcurrentBranches
	if limit <= 0 {
		t.Fatal("the working provider must declare a limit, or there is nothing to ignore")
	}

	p := fakes.Break(w, fakes.IgnoresTheDeclaredBranchLimit)
	for i := 0; i < limit; i++ {
		if _, err := p.Branch(ctx, v.ID, string(rune('a'+i))); err != nil {
			t.Fatalf("branch %d: %v", i, err)
		}
	}
	if _, err := w.Branch(ctx, v.ID, "one-too-many"); !errors.Is(err, aferrors.Coded(aferrors.AFDB006)) {
		t.Fatalf("the working provider must refuse past its limit, got %v", err)
	}
	if _, err := p.Branch(ctx, v.ID, "one-too-many"); err != nil {
		t.Fatalf("the fault must branch past the limit it publishes, got %v", err)
	}
}

func TestResetKeepsTheWrites(t *testing.T) {
	// The in-memory provider has no rows and answers ErrUnsupported, which is
	// itself the observable difference: the fault reports success without
	// doing anything, and the only way to see that it did nothing is to read
	// rows back, which is why Reset_ReturnsToGoldenState is a behaviour that
	// needs Postgres.
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	if err := w.Reset(ctx, provider.Branch{}); err == nil {
		t.Fatal("the working provider must not claim a reset it cannot do")
	}
	p := fakes.Break(w, fakes.ResetKeepsTheWrites)
	if err := p.Reset(ctx, provider.Branch{}); err != nil {
		t.Fatalf("the fault must report success and change nothing, got %v", err)
	}
}

func TestDestroyLeavesItInTheInventory(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	v, _ := w.RefreshGolden(ctx, spec())
	b, _ := w.Branch(ctx, v.ID, "env")

	p := fakes.Break(w, fakes.DestroyLeavesItInTheInventory)
	if err := p.Destroy(ctx, b); err != nil {
		t.Fatalf("the fault must report success, got %v", err)
	}
	inv, _ := p.Inventory(ctx)
	for _, r := range inv {
		if r.ID == b.ProviderRef {
			return
		}
	}
	t.Fatal("the fault must leave the branch in the inventory, which is how a resource outlives its environment")
}

func TestDestroyTwiceErrors(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
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

func TestConnStringIsEmpty(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	v, _ := w.RefreshGolden(ctx, spec())
	b, _ := w.Branch(ctx, v.ID, "env")

	if c, _ := w.ConnString(ctx, b, provider.ConnDirect); c.IsZero() {
		t.Fatal("the working provider must hand back a connection string")
	}
	p := fakes.Break(w, fakes.ConnStringIsEmpty)
	c, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		t.Fatalf("the fault must report success, got %v", err)
	}
	if !c.IsZero() {
		t.Fatal("the fault must hand back nothing at all")
	}
}

func TestPooledEqualsDirect(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	v, _ := w.RefreshGolden(ctx, spec())
	b, _ := w.Branch(ctx, v.ID, "env")

	if !w.Capabilities().PooledEndpoints {
		t.Fatal("the working provider must declare the capability, or there is nothing to fake")
	}
	p := fakes.Break(w, fakes.PooledEqualsDirect)
	direct, _ := p.ConnString(ctx, b, provider.ConnDirect)
	pooled, _ := p.ConnString(ctx, b, provider.ConnPooled)
	if !pooled.Equal(direct) {
		t.Fatal("the fault must declare a pooler and hand back the direct endpoint")
	}
}

func TestInventoryHidesResources(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
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

func TestHealthReportsALiveBranchUnreachable(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
	v, _ := w.RefreshGolden(ctx, spec())
	b, _ := w.Branch(ctx, v.ID, "env")

	if h, _ := w.Health(ctx, b); !h.Reachable {
		t.Fatal("the working provider must report a live branch reachable")
	}
	p := fakes.Break(w, fakes.HealthReportsALiveBranchUnreachable)
	h, err := p.Health(ctx, b)
	if err != nil {
		t.Fatalf("the fault must report rather than error, got %v", err)
	}
	if h.Reachable {
		t.Fatal("the fault must call a working branch down")
	}
}

func TestHealthErrorsOnDestroyed(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
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

func TestTheTwoCancellationFaultsLeaveTheirResourceDifferently(t *testing.T) {
	// Two shapes of one leak, and the difference is the whole reason there are
	// two: the conformance behaviour admits an unexplained resource through
	// two separate clauses, and a clause no fault exercises is a claim nobody
	// has checked.
	for fault, wantEnv := range map[fakes.Fault]string{
		fakes.CancellationLeavesAnUntrackedResource:     "",
		fakes.CancellationLeavesAResourceItDidNotReturn: "env",
	} {
		t.Run(string(fault), func(t *testing.T) {
			ctx := context.Background()
			w := fakes.NewInMemoryDatabase()
			v, _ := w.RefreshGolden(ctx, spec())

			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			p := fakes.Break(w, fault)
			b, err := p.Branch(cancelled, v.ID, "env")
			if err == nil {
				t.Fatal("the fault must report the failure")
			}
			if b.ProviderRef != "" {
				t.Fatal("and name nothing, which is what makes the resource untrackable")
			}
			inv, _ := p.Inventory(ctx)
			for _, r := range inv {
				if !strings.Contains(r.ID, "abandoned-by-a-cancelled-call") {
					continue
				}
				if r.EnvID != wantEnv {
					t.Fatalf("the resource should be attributed to %q and names %q", wantEnv, r.EnvID)
				}
				return
			}
			t.Fatal("and leave a resource in the inventory the caller was never given")
		})
	}
}

func TestGoldenGCDropsAReferencedVersion(t *testing.T) {
	ctx := context.Background()
	w := fakes.NewInMemoryDatabase()
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

func TestRefreshReusesTheVersionIdentifier(t *testing.T) {
	ctx := context.Background()
	p := fakes.Break(fakes.NewInMemoryDatabase(), fakes.RefreshReusesTheVersionIdentifier)
	first, err := p.RefreshGolden(ctx, spec())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	second, err := p.RefreshGolden(ctx, spec())
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.ID != first.ID {
		t.Fatal("the fault must give two different sets of data one name; versions are immutable")
	}
}

// A fault the decorator cannot inject must not be silently not injected.
//
// Returning the working provider would make the conformance suite pass and
// read as the fault surviving undetected, which is the false proof this whole
// package is against.
func TestAFaultAProviderCannotHostIsRefusedRatherThanIgnored(t *testing.T) {
	for _, f := range fakes.ProviderFaults() {
		if f == fakes.CapabilitiesContradictThemselves {
			continue // the in-memory fake hosts this one
		}
		p := fakes.Break(fakes.NewInMemoryDatabase(), f)
		u, ok := p.(fakes.Uninjectable)
		if !ok {
			t.Errorf("Break returned an ordinary provider for %q, which the in-memory fake "+
				"cannot host; a suite pointed at it would prove nothing", f)
			continue
		}
		if u.Fault != f {
			t.Errorf("the refusal names %q rather than %q", u.Fault, f)
		}
		if _, err := p.Branch(context.Background(), "v", "env"); err == nil {
			t.Errorf("every method of the refusal must fail loudly, and Branch did not for %q", f)
		}
	}
}

// The Postgres backed fake, and its four storage faults, at the level of the
// interface rather than of the conformance suite.
//
// The suite proves the behaviours go red. This proves the faults do what their
// names say, which is the other half: a fault that turned a behaviour red for
// some unrelated reason would satisfy the suite and mean nothing.
func TestThePostgresFakeIsCorrectAndItsStorageFaultsAreNot(t *testing.T) {
	ctx := context.Background()
	url := postgresURL()

	build := func(t *testing.T, fault fakes.Fault) provider.Database {
		t.Helper()
		prefix, err := fakes.NewPrefix()
		if err != nil {
			t.Fatalf("prefix: %v", err)
		}
		p, err := fakes.NewPostgresDatabase(ctx, fakes.PostgresOptions{
			AdminURL: url, Prefix: prefix, SeedSQL: seedSQL,
		})
		if err != nil {
			if os.Getenv("AF_REQUIRE_DATABASE") != "" {
				t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres at %s: %v", url, err)
			}
			t.Skipf("skipped: these need a Postgres at %s: %v", url, err)
		}
		t.Cleanup(func() {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := fakes.DropEverything(c, url, prefix); err != nil {
				t.Errorf("left databases named %s_* behind: %v", prefix, err)
			}
		})
		if fault == "" {
			return p
		}
		return fakes.Break(p, fault)
	}

	rows := func(t *testing.T, p provider.Database, b provider.Branch) int {
		t.Helper()
		conn, err := p.ConnString(ctx, b, provider.ConnDirect)
		if err != nil {
			t.Fatalf("conn string: %v", err)
		}
		return countUsers(t, conn.Reveal())
	}

	t.Run("correct", func(t *testing.T) {
		p := build(t, "")
		v, err := p.RefreshGolden(ctx, spec())
		if err != nil {
			t.Fatalf("refresh: %v", err)
		}
		a, err := p.Branch(ctx, v.ID, "env_a")
		if err != nil {
			t.Fatalf("branch: %v", err)
		}
		if n := rows(t, p, a); n != 3 {
			t.Fatalf("a branch of the golden holds %d rows and should hold 3", n)
		}
		writeUser(t, ctx, p, a, "only-in-a@example.test")
		b, err := p.Branch(ctx, v.ID, "env_b")
		if err != nil {
			t.Fatalf("second branch: %v", err)
		}
		if n := rows(t, p, b); n != 3 {
			t.Fatalf("a later branch holds %d rows, so the write reached the golden", n)
		}
		if err := p.Reset(ctx, a); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if n := rows(t, p, a); n != 3 {
			t.Fatalf("after a reset the branch holds %d rows and should hold 3", n)
		}
	})

	t.Run(string(fakes.BranchLosesTheGoldensRows), func(t *testing.T) {
		p := build(t, fakes.BranchLosesTheGoldensRows)
		v, _ := p.RefreshGolden(ctx, spec())
		b, err := p.Branch(ctx, v.ID, "env_a")
		if err != nil {
			t.Fatalf("branch: %v", err)
		}
		if n := rows(t, p, b); n != 0 {
			t.Fatalf("the fault must hand back a branch with no rows, got %d", n)
		}
	})

	t.Run(string(fakes.BranchSharesTheGoldensStorage), func(t *testing.T) {
		p := build(t, fakes.BranchSharesTheGoldensStorage)
		v, _ := p.RefreshGolden(ctx, spec())
		a, err := p.Branch(ctx, v.ID, "env_a")
		if err != nil {
			t.Fatalf("branch: %v", err)
		}
		writeUser(t, ctx, p, a, "only-in-a@example.test")
		b, err := p.Branch(ctx, v.ID, "env_b")
		if err != nil {
			t.Fatalf("second branch: %v", err)
		}
		if n := rows(t, p, b); n != 4 {
			t.Fatalf("the fault must let a write reach the golden, so a later branch holds 4; got %d", n)
		}
	})

	t.Run(string(fakes.BranchesShareOneDatabase), func(t *testing.T) {
		p := build(t, fakes.BranchesShareOneDatabase)
		v, _ := p.RefreshGolden(ctx, spec())
		a, _ := p.Branch(ctx, v.ID, "env_a")
		b, err := p.Branch(ctx, v.ID, "env_b")
		if err != nil {
			t.Fatalf("second branch: %v", err)
		}
		writeUser(t, ctx, p, b, "only-in-b@example.test")
		if n := rows(t, p, a); n != 4 {
			t.Fatalf("the fault must let one branch see another's write, so a holds 4; got %d", n)
		}
	})

	t.Run(string(fakes.RefreshRebuildsExistingBranches), func(t *testing.T) {
		p := build(t, fakes.RefreshRebuildsExistingBranches)
		first, _ := p.RefreshGolden(ctx, spec())
		a, err := p.Branch(ctx, first.ID, "env_a")
		if err != nil {
			t.Fatalf("branch: %v", err)
		}
		writeUser(t, ctx, p, a, "before-refresh@example.test")
		if n := rows(t, p, a); n != 4 {
			t.Fatalf("the write did not land: %d rows", n)
		}
		if _, err := p.RefreshGolden(ctx, spec()); err != nil {
			t.Fatalf("second refresh: %v", err)
		}
		if n := rows(t, p, a); n != 3 {
			t.Fatalf("the fault must remake the existing branch from the new version, "+
				"so it holds 3 again; got %d", n)
		}
	})
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

// Every behaviour the suite declares must have at least one fault that breaks
// it. This is the completeness claim, checked here as well as end to end,
// because a fault added without a behaviour and a behaviour added without a
// fault are different mistakes and only one of them is loud.
func TestEveryConformanceBehaviorHasAFault(t *testing.T) {
	covered := map[string]bool{}
	for _, b := range fakes.Catches() {
		covered[b] = true
	}
	var missing []string
	for _, name := range conformanceBehaviorNames() {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d behaviours have no fault, so nobody has watched them fail: %s",
			len(missing), strings.Join(missing, ", "))
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

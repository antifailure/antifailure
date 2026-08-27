package fakes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/testutil/fakes"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// envSpec builds a two service environment with a journal that records the
// order things were reported in.
func envSpec(journal *[]string) provider.EnvSpec {
	return provider.EnvSpec{
		EnvID:  "env1",
		Branch: "main",
		Services: []provider.ServiceSpec{
			{Name: "web", Kind: "web", Port: 8080},
			{Name: "worker", Kind: "worker"},
		},
		Journal: func(kind, id string) error {
			if journal != nil {
				*journal = append(*journal, kind+":"+id)
			}
			return nil
		},
	}
}

// The control. Every fault below is a difference from this, so this has to be
// right or the differences mean nothing.
func TestTheWorkingRuntimeKeepsEveryGuarantee(t *testing.T) {
	ctx := context.Background()
	r := fakes.NewRuntime()

	var journal []string
	env, err := r.Up(ctx, envSpec(&journal))
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if !env.ProxyReady {
		t.Error("the proxy should be up, or the environment has no route out")
	}
	if env.URL() != "http://127.0.0.1:8080" {
		t.Errorf("URL() should find the web service, got %q", env.URL())
	}
	for _, s := range env.Services {
		if !s.Ready {
			t.Errorf("service %s should be ready when Up returns", s.Name)
		}
	}

	// Every resource was reported, and reported before it existed.
	if len(journal) != 4 {
		t.Errorf("want the network, two containers and the proxy recorded, got %v", journal)
	}
	if got, want := len(r.Journaled()), len(r.Created()); got != want {
		t.Errorf("journaled %d and created %d", got, want)
	}

	inv, err := r.Inventory(ctx)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(inv) != 4 {
		t.Errorf("inventory should list all four resources, got %d", len(inv))
	}

	td, err := r.Down(ctx, "env1")
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if td.Removed != 4 || len(td.Pending) != 0 {
		t.Errorf("teardown should remove all four and leave nothing, got %+v", td)
	}
	if td2, err := r.Down(ctx, "env1"); err != nil || td2.Removed != 0 {
		t.Errorf("tearing down twice should succeed quietly, got %+v err %v", td2, err)
	}
}

// Teardown never stops at the first failure, so one stuck resource must not
// strand the others.
func TestTeardownAttemptsEveryResourceEvenWhenOneRefuses(t *testing.T) {
	ctx := context.Background()
	r := fakes.NewRuntime()
	if _, err := r.Up(ctx, envSpec(nil)); err != nil {
		t.Fatal(err)
	}
	r.RefuseRemoval["net_env1"] = "still in use"

	td, err := r.Down(ctx, "env1")
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(td.Pending) != 1 {
		t.Fatalf("want one pending resource, got %+v", td.Pending)
	}
	if td.Removed != 3 {
		t.Errorf("the other three should still have been removed, got %d", td.Removed)
	}
}

func TestTeardownOfOneEnvironmentLeavesAnotherAlone(t *testing.T) {
	ctx := context.Background()
	r := fakes.NewRuntime()
	if _, err := r.Up(ctx, envSpec(nil)); err != nil {
		t.Fatal(err)
	}
	other := envSpec(nil)
	other.EnvID = "env2"
	if _, err := r.Up(ctx, other); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Down(ctx, "env1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(ctx, "env2"); err != nil {
		t.Fatalf("the other environment should still be running: %v", err)
	}
}

func upWith(t *testing.T, f fakes.RuntimeFault, journal *[]string) (provider.Env, *fakes.Runtime, provider.Runtime) {
	t.Helper()
	inner := fakes.NewRuntime()
	p := fakes.BreakRuntime(inner, f)
	env, err := p.Up(context.Background(), envSpec(journal))
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	return env, inner, p
}

func TestNeverJournals(t *testing.T) {
	var journal []string
	_, _, _ = upWith(t, fakes.NeverJournals, &journal)
	if len(journal) != 0 {
		t.Fatalf("the fault must record nothing, got %v", journal)
	}
}

// The ordering rule, which is the one an end-state assertion cannot see: both
// runtimes end up having journaled the same four resources, and only one of
// them did it before the resources existed.
func TestJournalsAfterCreating(t *testing.T) {
	var journal []string
	_, inner, _ := upWith(t, fakes.JournalsAfterCreating, &journal)

	if len(journal) != 4 {
		t.Fatalf("the fault still records everything, eventually: got %v", journal)
	}
	if len(inner.Created()) != 4 {
		t.Fatalf("and everything really was created, got %v", inner.Created())
	}
	// The tell: with the correct runtime the caller's journal receives each id
	// as it is made, so a caller counting during Up would see them interleaved.
	// Here they all arrive at the end, which is what the deferral models.
	var correct []string
	if _, err := fakes.NewRuntime().Up(context.Background(), envSpec(&correct)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(correct, ",") != strings.Join(journal, ",") {
		t.Errorf("the fault should still produce the same set, so only the timing differs:\n correct %v\n faulty  %v", correct, journal)
	}
}

func TestReportsReadyBeforeHealthy(t *testing.T) {
	env, _, _ := upWith(t, fakes.ReportsReadyBeforeHealthy, nil)
	for _, s := range env.Services {
		if s.Ready {
			t.Fatalf("the fault must hand back a service that is not ready, got %+v", s)
		}
	}
}

func TestStopsAtTheFirstTeardownFailure(t *testing.T) {
	ctx := context.Background()
	inner := fakes.NewRuntime()
	if _, err := inner.Up(ctx, envSpec(nil)); err != nil {
		t.Fatal(err)
	}
	inner.RefuseRemoval["net_env1"] = "still in use"

	p := fakes.BreakRuntime(inner, fakes.StopsAtTheFirstTeardownFailure)
	td, err := p.Down(ctx, "env1")
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if td.Removed != 0 {
		t.Fatalf("the fault must abandon the rest, got %d removed", td.Removed)
	}
}

func TestReportsCleanTeardownWithPendingResources(t *testing.T) {
	ctx := context.Background()
	inner := fakes.NewRuntime()
	if _, err := inner.Up(ctx, envSpec(nil)); err != nil {
		t.Fatal(err)
	}
	inner.RefuseRemoval["net_env1"] = "still in use"

	p := fakes.BreakRuntime(inner, fakes.ReportsCleanTeardownWithPendingResources)
	td, err := p.Down(ctx, "env1")
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if len(td.Pending) != 0 {
		t.Fatalf("the fault must report nothing pending, got %+v", td.Pending)
	}
	// And the resource is genuinely still there, which is what makes the clean
	// report a lie rather than a rounding difference.
	inv, _ := inner.Inventory(ctx)
	if len(inv) == 0 {
		t.Fatal("the resource must really still exist, or the fault measures nothing")
	}
}

// The one that costs somebody else their work rather than leaking a container.
func TestTearsDownEveryEnvironment(t *testing.T) {
	ctx := context.Background()
	inner := fakes.NewRuntime()
	if _, err := inner.Up(ctx, envSpec(nil)); err != nil {
		t.Fatal(err)
	}
	other := envSpec(nil)
	other.EnvID = "env2"
	if _, err := inner.Up(ctx, other); err != nil {
		t.Fatal(err)
	}

	p := fakes.BreakRuntime(inner, fakes.TearsDownEveryEnvironment)
	if _, err := p.Down(ctx, "env1"); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Status(ctx, "env2"); err == nil {
		t.Fatal("the fault must have taken the other environment with it")
	}
}

func TestInventoryHidesEnvironments(t *testing.T) {
	ctx := context.Background()
	inner := fakes.NewRuntime()
	if _, err := inner.Up(ctx, envSpec(nil)); err != nil {
		t.Fatal(err)
	}

	p := fakes.BreakRuntime(inner, fakes.InventoryHidesEnvironments)
	inv, err := p.Inventory(ctx)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(inv) != 0 {
		t.Fatalf("the fault must under-report, got %d", len(inv))
	}
	if real, _ := inner.Inventory(ctx); len(real) == 0 {
		t.Fatal("and the resources must really exist")
	}
}

// A service that refuses to start must not leave the caller without a record
// of what was already made. This is the property the journal exists for.
func TestAFailedUpStillLeavesEverythingItMadeRecorded(t *testing.T) {
	r := fakes.NewRuntime()
	r.RefuseService["worker"] = "the image has no such command"

	var journal []string
	if _, err := r.Up(context.Background(), envSpec(&journal)); err == nil {
		t.Fatal("up should fail when a service refuses to start")
	}
	// The network and both containers were reported before the failure, so a
	// teardown has something to find.
	if len(journal) < 3 {
		t.Errorf("everything created before the failure must be recorded, got %v", journal)
	}
}

func TestEveryRuntimeFaultNameIsKebabCase(t *testing.T) {
	for _, f := range fakes.RuntimeFaults() {
		s := string(f)
		if s != strings.ToLower(s) || strings.ContainsAny(s, " _") {
			t.Errorf("fault %q should be lower case and hyphenated", f)
		}
	}
}

// Compile time proof that both doubles satisfy the interfaces they claim.
var (
	_ provider.Runtime = (*fakes.Runtime)(nil)
	_ provider.Runtime = fakes.BreakRuntime(fakes.NewRuntime(), fakes.NeverJournals)
)

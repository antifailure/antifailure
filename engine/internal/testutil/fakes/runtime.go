package fakes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// RuntimeFault names one guarantee a runtime can break.
//
// Kept separate from [Fault] rather than merged into one list, because the two
// interfaces guarantee different things and a single namespace would invite
// passing a database fault to a runtime and getting silence.
type RuntimeFault string

const (
	// JournalsAfterCreating records a resource after it exists rather than
	// before. The window between the two is exactly where an interrupt strands
	// something nothing can find: the resource is real and the journal has
	// never heard of it.
	JournalsAfterCreating RuntimeFault = "journals-after-creating"

	// NeverJournals skips the journal entirely, which is the same bug with the
	// window widened to forever.
	NeverJournals RuntimeFault = "never-journals"

	// StopsAtTheFirstTeardownFailure gives up on the rest of an environment as
	// soon as one resource refuses to go. One unreachable provider then strands
	// every other resource, and the logs that would explain it go with them.
	StopsAtTheFirstTeardownFailure RuntimeFault = "stops-at-the-first-teardown-failure"

	// ReportsCleanTeardownWithPendingResources returns success while leaving
	// things behind. `af down` exits 0, the developer stops thinking about it,
	// and the bill arrives later.
	ReportsCleanTeardownWithPendingResources RuntimeFault = "reports-clean-teardown-with-pending-resources"

	// TearsDownEveryEnvironment removes resources belonging to environments it
	// was not asked about. The one fault here that destroys a colleague's work
	// rather than leaking a container.
	TearsDownEveryEnvironment RuntimeFault = "tears-down-every-environment"

	// ReportsReadyBeforeHealthy answers Up before a service has passed its
	// readiness check, so the URL is handed over to something not yet
	// listening.
	ReportsReadyBeforeHealthy RuntimeFault = "reports-ready-before-healthy"

	// LosesTheProxy brings the environment up with no egress sidecar while
	// still reporting ProxyReady. The environment then has no route out and no
	// policy on the route it does not have.
	LosesTheProxy RuntimeFault = "loses-the-proxy"

	// InventoryHidesEnvironments under-reports, which blinds the leak detector
	// in the direction it cannot recover from.
	InventoryHidesEnvironments RuntimeFault = "inventory-hides-environments"
)

// RuntimeFaults returns every runtime fault, sorted.
func RuntimeFaults() []RuntimeFault {
	out := []RuntimeFault{
		JournalsAfterCreating,
		NeverJournals,
		StopsAtTheFirstTeardownFailure,
		ReportsCleanTeardownWithPendingResources,
		TearsDownEveryEnvironment,
		ReportsReadyBeforeHealthy,
		LosesTheProxy,
		InventoryHidesEnvironments,
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Runtime is an in-memory provider.Runtime that keeps every guarantee the
// interface documents.
//
// It exists for two audiences. Anything that drives a runtime and is not
// itself testing container mechanics can use this and run in milliseconds
// rather than minutes. And a fault injected with [BreakRuntime] is only
// meaningful as a difference from this, so the correct one has to exist first.
//
// Refusing is modelled explicitly rather than by returning canned errors:
// RefuseService names a service that will not start, and RefuseRemoval names a
// resource that will not go away, which is what an unreachable daemon or a
// volume still in use actually looks like from here.
type Runtime struct {
	// RefuseService names services that fail to start, by service name, with
	// the reason.
	RefuseService map[string]string
	// RefuseRemoval names resource IDs that teardown cannot remove, with the
	// reason.
	RefuseRemoval map[string]string

	mu   sync.Mutex
	envs map[string]*provider.Env
	// journaled records every (kind, id) the runtime reported, in order, so a
	// test can assert the record happened before the resource existed.
	journaled []string
	// created records every resource id in creation order, for the same
	// comparison from the other side.
	created []string
}

// NewRuntime returns a runtime that keeps every guarantee.
func NewRuntime() *Runtime {
	return &Runtime{
		RefuseService: map[string]string{},
		RefuseRemoval: map[string]string{},
		envs:          map[string]*provider.Env{},
	}
}

func (r *Runtime) Name() string { return "fake" }

// Capabilities declares what this fake supports.
//
// Added when provider.Runtime gained the method, so that a behaviour a runtime
// cannot support is skipped by name rather than passing silently. The fake
// claims ingress because it reports a URL, and does not claim log reading or a
// local database attachment, because it implements neither. Claiming either
// would be the exact fault this package exists to inject, in the package
// itself.
func (r *Runtime) Capabilities() provider.RuntimeCaps {
	return provider.RuntimeCaps{Ingress: true}
}

// Journaled returns the resources the runtime reported, in order.
func (r *Runtime) Journaled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.journaled...)
}

// Created returns the resources the runtime actually made, in order. Comparing
// this against Journaled is how a test checks the ordering rule rather than
// just the end state: both lists can hold the same things and still describe a
// runtime that records after the fact.
func (r *Runtime) Created() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.created...)
}

func (r *Runtime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	if err := ctx.Err(); err != nil {
		return provider.Env{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	env := &provider.Env{
		EnvID:     spec.EnvID,
		NetworkID: "net_" + spec.EnvID,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// The network is a resource too, and it is the one most often forgotten by
	// a teardown that only tracks containers.
	if err := r.record(spec, "network", env.NetworkID); err != nil {
		return provider.Env{}, err
	}

	for _, s := range spec.Services {
		id := "ctr_" + spec.EnvID + "_" + s.Name
		if err := r.record(spec, "container", id); err != nil {
			return provider.Env{}, err
		}
		if reason, refused := r.RefuseService[s.Name]; refused {
			return provider.Env{}, fmt.Errorf("AF-RUN-001: the service %s did not start: %s", s.Name, reason)
		}
		rs := provider.RunningService{
			Name: s.Name, Kind: s.Kind, ContainerID: id,
			Ready: true, State: "running",
		}
		if s.Port != 0 {
			rs.URL = fmt.Sprintf("http://127.0.0.1:%d", s.Port)
		}
		env.Services = append(env.Services, rs)
	}

	proxyID := "proxy_" + spec.EnvID
	if err := r.record(spec, "proxy", proxyID); err != nil {
		return provider.Env{}, err
	}
	env.ProxyReady = true

	r.envs[spec.EnvID] = env
	return *env, nil
}

// record journals a resource and then creates it, in that order, which is the
// rule Up documents. A runtime that reverses these two is the fault
// JournalsAfterCreating.
func (r *Runtime) record(spec provider.EnvSpec, kind, id string) error {
	if spec.Journal != nil {
		if err := spec.Journal(kind, id); err != nil {
			return fmt.Errorf("AF-RUN-002: could not record %s %s before creating it: %w", kind, id, err)
		}
	}
	r.journaled = append(r.journaled, id)
	r.created = append(r.created, id)
	return nil
}

func (r *Runtime) Down(_ context.Context, envID string) (provider.Teardown, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	env, ok := r.envs[envID]
	if !ok {
		// Tearing down something already gone succeeds, for the same reason
		// destroying a branch twice does: teardown retries.
		return provider.Teardown{}, nil
	}

	var td provider.Teardown
	// Never stop at the first failure. Each resource is attempted and what
	// could not go is reported.
	for _, res := range r.resourcesOf(env) {
		if reason, refused := r.RefuseRemoval[res.ID]; refused {
			td.Pending = append(td.Pending, provider.PendingResource{
				Kind: res.Kind, ID: res.ID, Reason: reason,
			})
			continue
		}
		td.Removed++
	}
	if len(td.Pending) == 0 {
		delete(r.envs, envID)
	}
	return td, nil
}

func (r *Runtime) Status(_ context.Context, envID string) (provider.Env, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	env, ok := r.envs[envID]
	if !ok {
		return provider.Env{}, errors.New("AF-RUN-003: no environment with that identifier is running")
	}
	return *env, nil
}

func (r *Runtime) Inventory(context.Context) ([]provider.Resource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []provider.Resource
	for _, env := range r.envs {
		out = append(out, r.resourcesOf(env)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// resourcesOf lists everything one environment holds. Callers hold the lock.
func (r *Runtime) resourcesOf(env *provider.Env) []provider.Resource {
	out := []provider.Resource{
		{Kind: "network", ID: env.NetworkID, EnvID: env.EnvID},
		{Kind: "proxy", ID: "proxy_" + env.EnvID, EnvID: env.EnvID},
	}
	for _, s := range env.Services {
		out = append(out, provider.Resource{Kind: "container", ID: s.ContainerID, EnvID: env.EnvID})
	}
	return out
}

func (r *Runtime) Close() error { return nil }

// BreakRuntime wraps a runtime that works and returns one that violates
// exactly one guarantee.
func BreakRuntime(inner provider.Runtime, f RuntimeFault) provider.Runtime {
	return &brokenRuntime{Runtime: inner, fault: f}
}

type brokenRuntime struct {
	provider.Runtime
	fault RuntimeFault
}

func (b *brokenRuntime) is(f RuntimeFault) bool { return b.fault == f }

func (b *brokenRuntime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	switch {
	case b.is(NeverJournals):
		spec.Journal = nil

	case b.is(JournalsAfterCreating):
		// Defer every record to the end. The resources are all real before any
		// of them is written down, which is the window an interrupt falls into.
		var pending [][2]string
		inner := spec.Journal
		spec.Journal = func(kind, id string) error {
			pending = append(pending, [2]string{kind, id})
			return nil
		}
		env, err := b.Runtime.Up(ctx, spec)
		if inner != nil {
			for _, p := range pending {
				if jerr := inner(p[0], p[1]); jerr != nil {
					return env, jerr
				}
			}
		}
		return env, err
	}

	env, err := b.Runtime.Up(ctx, spec)
	if err != nil {
		return env, err
	}
	if b.is(ReportsReadyBeforeHealthy) {
		for i := range env.Services {
			env.Services[i].Ready = false
			env.Services[i].State = "starting"
		}
	}
	if b.is(LosesTheProxy) {
		// Still reported ready, which is the dangerous half: an environment
		// with no sidecar and no policy, that says it has both.
		env.ProxyReady = true
	}
	return env, nil
}

func (b *brokenRuntime) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	if b.is(TearsDownEveryEnvironment) {
		inv, err := b.Runtime.Inventory(ctx)
		if err != nil {
			return provider.Teardown{}, err
		}
		seen := map[string]bool{}
		for _, res := range inv {
			seen[res.EnvID] = true
		}
		var total provider.Teardown
		for id := range seen {
			td, err := b.Runtime.Down(ctx, id)
			if err != nil {
				return total, err
			}
			total.Removed += td.Removed
			total.Pending = append(total.Pending, td.Pending...)
		}
		return total, nil
	}

	td, err := b.Runtime.Down(ctx, envID)
	if err != nil {
		return td, err
	}
	switch {
	case b.is(StopsAtTheFirstTeardownFailure) && len(td.Pending) > 0:
		// Give up after the first refusal: report one pending resource and
		// nothing removed, as though the loop had returned early.
		return provider.Teardown{Pending: td.Pending[:1]}, nil
	case b.is(ReportsCleanTeardownWithPendingResources):
		td.Pending = nil
	}
	return td, nil
}

func (b *brokenRuntime) Inventory(ctx context.Context) ([]provider.Resource, error) {
	if b.is(InventoryHidesEnvironments) {
		return nil, nil
	}
	return b.Runtime.Inventory(ctx)
}

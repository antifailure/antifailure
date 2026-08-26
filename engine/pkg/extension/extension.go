// Package extension is where the community engine lets something else in.
//
// MIT, like the rest of the engine. Nothing here is enterprise code and nothing
// here imports any: these are the sockets, and the enterprise edition is one
// thing that can plug into them. A customer's own build is another.
//
// Every hook has a no-op default, and the no-op is the shipped behaviour. That
// is what makes the community edition complete rather than crippled: with
// nothing registered the engine does exactly what it did before these existed,
// and the community test suite runs unchanged with the hooks present. There is
// a test asserting that.
//
// The hooks are deliberately few and deliberately shaped so that a hook cannot
// weaken anything. A policy hook may refuse an environment and cannot permit
// one the manifest would refuse; an audit sink may observe and cannot alter.
// Anything that could loosen a control from outside the repository would be a
// way to change what an environment masks without review.
package extension

import (
	"context"
	"sort"
	"sync"
)

// EnvironmentRequest is what a policy hook is asked about, before anything is
// created.
type EnvironmentRequest struct {
	Org        string
	Repository string
	Branch     string
	EnvID      string
	// EgressHosts are the hosts the manifest permits, so a hook can refuse one
	// that organization policy forbids.
	EgressHosts []string
	// EgressModes is the mode each host is permitted in, keyed by host.
	EgressModes map[string]string
	// MaskedColumns is table.column for every column the plan will mask, so a
	// hook can require that a column pattern is covered.
	MaskedColumns []string
	// Provider is the database provider the environment will use.
	Provider string
	// Region is where it will run, when the runtime reports one.
	Region string
}

// PolicyHook may refuse an environment before it is created.
//
// It can only refuse. There is no return value that permits something the
// manifest did not, because a hook that could widen an egress policy would be a
// way to change what an environment can reach without changing the repository,
// which is the one thing this system exists to make impossible.
type PolicyHook interface {
	// Name identifies the hook in the refusal.
	Name() string
	// Check returns an error to refuse. The error reaches the user, so it says
	// which policy refused and what would satisfy it.
	Check(ctx context.Context, req EnvironmentRequest) error
}

// LifecycleEvent is something that happened to an environment, for hooks that
// meter or record.
type LifecycleEvent struct {
	Org        string
	Repository string
	EnvID      string
	Kind       string
	// SizeClass is how big the environment is, for metering.
	SizeClass string
	// Seconds is how long it existed, on a teardown event.
	Seconds float64
}

// LifecycleHook observes environments coming and going.
//
// It cannot refuse anything and it cannot change anything. An error from it is
// recorded and does not stop the lifecycle, because a metering pipeline that is
// down must not prevent an environment from being torn down: that turns a
// billing outage into a resource leak.
type LifecycleHook interface {
	Name() string
	Observe(ctx context.Context, event LifecycleEvent) error
}

// AuditEntry is one recorded action.
type AuditEntry struct {
	Org        string
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Origin     string
	Detail     map[string]any
}

// AuditSink receives audit entries for forwarding.
//
// Observation only. The primary audit log is written regardless of what any
// sink does, so a sink that is unreachable loses forwarding and never loses the
// entry.
type AuditSink interface {
	Name() string
	Write(ctx context.Context, entry AuditEntry) error
}

// Registry holds what has been registered.
//
// A value rather than only a package-level singleton, so that a test can build
// one and so that two engines in one process do not share hooks.
type Registry struct {
	mu        sync.RWMutex
	policy    []PolicyHook
	lifecycle []LifecycleHook
	audit     []AuditSink
}

// NewRegistry returns an empty registry, which is the community behaviour.
func NewRegistry() *Registry { return &Registry{} }

// Default is the registry the engine consults when none is supplied.
var Default = NewRegistry()

// AddPolicy registers a hook that may refuse an environment.
func (r *Registry) AddPolicy(h PolicyHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = append(r.policy, h)
}

// AddLifecycle registers a hook that observes environments.
func (r *Registry) AddLifecycle(h LifecycleHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lifecycle = append(r.lifecycle, h)
}

// AddAuditSink registers a sink that forwards audit entries.
func (r *Registry) AddAuditSink(s AuditSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audit = append(r.audit, s)
}

// CheckPolicy runs every policy hook and returns the first refusal.
//
// With nothing registered it returns nil, which is the community behaviour and
// the reason the community suite passes unchanged with these calls in place.
//
// Hooks run in registration order and the first refusal wins, so a refusal is
// reproducible. Stopping at the first also means an environment violating three
// policies names one of them, which is the right amount for somebody who has to
// fix them one at a time.
func (r *Registry) CheckPolicy(ctx context.Context, req EnvironmentRequest) error {
	r.mu.RLock()
	hooks := append([]PolicyHook(nil), r.policy...)
	r.mu.RUnlock()

	for _, h := range hooks {
		if err := h.Check(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// Observe reports a lifecycle event to every hook, collecting failures.
//
// It never stops early and never returns an error a caller is expected to act
// on. A metering hook that is down must not prevent a teardown.
func (r *Registry) Observe(ctx context.Context, event LifecycleEvent) []error {
	r.mu.RLock()
	hooks := append([]LifecycleHook(nil), r.lifecycle...)
	r.mu.RUnlock()

	var problems []error
	for _, h := range hooks {
		if err := h.Observe(ctx, event); err != nil {
			problems = append(problems, err)
		}
	}
	return problems
}

// Audit forwards an entry to every sink, collecting failures.
func (r *Registry) Audit(ctx context.Context, entry AuditEntry) []error {
	r.mu.RLock()
	sinks := append([]AuditSink(nil), r.audit...)
	r.mu.RUnlock()

	var problems []error
	for _, s := range sinks {
		if err := s.Write(ctx, entry); err != nil {
			problems = append(problems, err)
		}
	}
	return problems
}

// Registered names what is plugged in, for af version and af doctor.
//
// Worth printing: an operator debugging why an environment was refused needs to
// know a policy hook exists at all, and "nothing registered" is itself the
// answer to most of those questions.
func (r *Registry) Registered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []string
	for _, h := range r.policy {
		out = append(out, "policy:"+h.Name())
	}
	for _, h := range r.lifecycle {
		out = append(out, "lifecycle:"+h.Name())
	}
	for _, s := range r.audit {
		out = append(out, "audit:"+s.Name())
	}
	sort.Strings(out)
	return out
}

// Empty reports whether anything is registered, which is the community case.
func (r *Registry) Empty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.policy) == 0 && len(r.lifecycle) == 0 && len(r.audit) == 0
}

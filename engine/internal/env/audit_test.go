package env_test

// The audit socket had no production caller, so these tests are about call
// sites rather than about behaviour.
//
// Measured on main before this file existed: `Registry.Audit` had exactly two
// callers in the entire repository and both were in
// engine/pkg/extension/extension_test.go. So the sink interface, the registry
// and the forwarding loop were all written, tested and documented, and no
// action the engine takes ever reached any of them. Every test here fails if
// its call site is removed, which is the only property that distinguishes this
// from the state it replaced.
//
// None of them needs a Docker daemon, deliberately. Two stop at a policy
// refusal or at an unsatisfiable variable, both of which happen before anything
// is created, and the third is a teardown, which reports what it could not
// remove rather than failing. A test that needed a daemon to prove a hook is
// called would be a test nobody runs on the machine where it matters.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// recordingSink keeps every entry it is given.
type recordingSink struct {
	mu      sync.Mutex
	entries []extension.AuditEntry
	err     error
}

func (r *recordingSink) Name() string { return "recording-sink" }

func (r *recordingSink) Write(_ context.Context, entry extension.AuditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return r.err
}

func (r *recordingSink) taken() []extension.AuditEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extension.AuditEntry(nil), r.entries...)
}

// actionOf returns the one entry with an action, or fails naming what arrived.
func actionOf(t *testing.T, entries []extension.AuditEntry, action string) extension.AuditEntry {
	t.Helper()
	var seen []string
	for _, e := range entries {
		if e.Action == action {
			return e
		}
		seen = append(seen, e.Action)
	}
	require.FailNowf(t, "the sink never received "+action,
		"it received %d entries: %s", len(entries), strings.Join(seen, ", "))
	return extension.AuditEntry{}
}

// auditOrchestrator is newOrchestrator with an environment and a progress
// recorder, because two of these assert on what the engine said about a sink.
func auditOrchestrator(
	t *testing.T, registry *extension.Registry, getenv func(string) string, progress func(string),
) *env.Orchestrator {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(minimalManifest)+"\n"), 0o644))

	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "feature/audit", Repository: "acme/shop",
		Clock: clock.New(), Redactor: redact.New(),
		Progress:   progress,
		Getenv:     getenv,
		Extensions: registry,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = o.Down(ctx)
	})
	return o
}

func TestUp_ForwardsAPolicyRefusalToTheAuditSink(t *testing.T) {
	// The entry a security team actually came for. Everything else in the
	// stream says what somebody was allowed to do; this says what a control
	// stopped them doing, and it is the only kind of entry that shows a control
	// holding rather than merely being configured.
	registry := extension.NewRegistry()
	refusal := errors.New("organization policy requires api.stripe.com to be blocked, not sandboxed")
	registry.AddPolicy(&refusingHook{err: refusal})
	sink := &recordingSink{}
	registry.AddAuditSink(sink)

	getenv := func(name string) string {
		switch name {
		case "AF_ORG":
			return "acme"
		case "AF_ACTOR":
			return "dana@acme.example"
		}
		return ""
	}
	o := auditOrchestrator(t, registry, getenv, func(string) {})
	_, err := o.Up(context.Background())
	require.ErrorIs(t, err, refusal)

	entry := actionOf(t, sink.taken(), "environment.refused")
	require.Equal(t, "environment", entry.TargetType)
	require.Equal(t, o.EnvID(), entry.TargetID)
	require.Equal(t, "engine", entry.Origin)
	require.Equal(t, "acme", entry.Org)
	require.Equal(t, "dana@acme.example", entry.Actor)
	require.False(t, entry.OccurredAt.IsZero(),
		"an entry with no timestamp cannot be correlated against anything")

	// The refusal travels with the entry. An audit record that says an
	// environment was refused and not which policy refused it is a record
	// nobody can act on.
	require.Contains(t, entry.Detail["refusal"], "api.stripe.com")
	require.Equal(t, "acme/shop", entry.Detail["repository"])
	require.Equal(t, "feature/audit", entry.Detail["branch"])
}

func TestUp_ForwardsTheCreationAttemptWithItsOutcome(t *testing.T) {
	// A failed creation is recorded, with the outcome, for the same reason the
	// lifecycle meter counts one: a run that got far enough to fail got far
	// enough to be worth asking about. This manifest declares a variable
	// nothing supplies, so Up stops at secret resolution, after the point where
	// the audit entry is registered.
	registry := extension.NewRegistry()
	sink := &recordingSink{}
	registry.AddAuditSink(sink)

	o := auditOrchestrator(t, registry, func(string) string { return "" }, func(string) {})
	_, err := o.Up(context.Background())
	require.Error(t, err, "this manifest declares a variable nothing supplies")

	entry := actionOf(t, sink.taken(), "environment.created")
	require.Equal(t, "environment", entry.TargetType)
	require.Equal(t, o.EnvID(), entry.TargetID)
	require.Equal(t, "failed", entry.Detail["outcome"])
	require.NotEmpty(t, entry.Detail["code"],
		"the error code travels with the entry so a receiver need not parse a sentence")
	require.False(t, entry.OccurredAt.IsZero())
}

func TestDown_ForwardsTheTeardownAndAnUnreachableSinkDoesNotBlockIt(t *testing.T) {
	// The contract the AuditSink interface states, proved rather than
	// described: a sink observes and cannot alter, and an error from one is
	// recorded without stopping the lifecycle. A forwarding outage that stopped
	// an environment being destroyed would turn a logging problem into a
	// resource leak, which is strictly worse than the problem it came from.
	registry := extension.NewRegistry()
	unreachable := &recordingSink{err: errors.New("dial tcp 10.0.0.9:6514: i/o timeout")}
	registry.AddAuditSink(unreachable)
	reachable := &recordingSink{}
	registry.AddAuditSink(reachable)

	var mu sync.Mutex
	var progress []string
	o := auditOrchestrator(t, registry, func(string) string { return "" }, func(line string) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, line)
	})

	td, err := o.Down(context.Background())
	require.NoError(t, err, "an unreachable audit sink stopped a teardown")
	require.NotNil(t, td)

	// The second sink still received it, so one destination being down does not
	// cost the others their copy.
	entry := actionOf(t, reachable.taken(), "environment.torn_down")
	require.Equal(t, o.EnvID(), entry.TargetID)
	require.Contains(t, entry.Detail, "pending")

	// And the failure was said out loud rather than swallowed. An operator
	// whose SIEM has been unreachable all day must be able to find that out
	// from the tool that could not reach it.
	mu.Lock()
	said := strings.Join(progress, "\n")
	mu.Unlock()
	require.Contains(t, said, "audit sink:")
	require.Contains(t, said, "i/o timeout")
}

func TestAudit_TakesTheActorFromGitHubActionsWhenNothingElseSaysWho(t *testing.T) {
	// GITHUB_ACTOR is what a GitHub Actions runner sets for free, and most of
	// these run there. The operating system user is deliberately never
	// consulted: on a runner it is `runner` for everybody, which reads as an
	// attribution and is not one.
	registry := extension.NewRegistry()
	registry.AddPolicy(&refusingHook{err: errors.New("no")})
	sink := &recordingSink{}
	registry.AddAuditSink(sink)

	getenv := func(name string) string {
		if name == "GITHUB_ACTOR" {
			return "octocat"
		}
		return ""
	}
	o := auditOrchestrator(t, registry, getenv, func(string) {})
	_, err := o.Up(context.Background())
	require.Error(t, err)

	entry := actionOf(t, sink.taken(), "environment.refused")
	require.Equal(t, "octocat", entry.Actor)
	require.Empty(t, entry.Org, "AF_ORG was not set and an org must not be invented")
}

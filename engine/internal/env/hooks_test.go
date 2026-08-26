package env_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// The extension points are useless if nothing calls them, and a hook with no
// call site is indistinguishable from a hook that works until somebody relies
// on it. These tests exist because the package that defines the sockets can be
// at a hundred percent coverage while the engine never plugs anything in.
//
// A refusal is proved rather than a success, because a refusal is observable
// without a Docker daemon: the check runs before anything is created, which is
// itself the property worth having.

const minimalManifest = `
version: 1
name: hooks
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
`

type refusingHook struct {
	err  error
	seen *extension.EnvironmentRequest
}

func (h *refusingHook) Name() string { return "test-policy" }
func (h *refusingHook) Check(_ context.Context, req extension.EnvironmentRequest) error {
	copied := req
	h.seen = &copied
	return h.err
}

func newOrchestrator(t *testing.T, registry *extension.Registry) *env.Orchestrator {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(minimalManifest)+"\n"), 0o644))

	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "feature/hooks",
		Clock: clock.New(), Redactor: redact.New(),
		Progress:   func(string) {},
		Extensions: registry,
	})
	require.NoError(t, err)

	// Every test in this file gets a teardown, whether or not it expected to
	// create anything. Two of them call Up with nothing registered to refuse,
	// which on a machine with a daemon really does make a database branch, and
	// the first version of this file left two behind. The conformance suite's
	// leak detector found them, which is the entire reason it exists, and the
	// lesson is that "this test does not create resources" is a belief rather
	// than a property.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = o.Down(ctx)
	})
	return o
}

func TestUp_AsksThePolicyHookBeforeCreatingAnything(t *testing.T) {
	registry := extension.NewRegistry()
	refusal := errors.New("organization policy requires api.stripe.com to be blocked, not sandboxed")
	hook := &refusingHook{err: refusal}
	registry.AddPolicy(hook)

	_, err := newOrchestrator(t, registry).Up(context.Background())

	require.ErrorIs(t, err, refusal,
		"the policy hook did not refuse, so nothing is calling it")
	require.NotNil(t, hook.seen, "the hook was never asked")

	// The refusal reaches the user as the hook wrote it. Wrapping it in
	// "environment creation failed" would bury the only sentence that helps.
	require.Contains(t, err.Error(), "api.stripe.com")
}

func TestUp_TellsThePolicyHookWhatItNeedsToDecide(t *testing.T) {
	registry := extension.NewRegistry()
	hook := &refusingHook{err: errors.New("no")}
	registry.AddPolicy(hook)

	_, _ = newOrchestrator(t, registry).Up(context.Background())
	require.NotNil(t, hook.seen)

	// A policy about egress cannot be enforced without the egress rules, and a
	// hook that receives an empty request is a hook that can only refuse
	// everything or nothing.
	require.Equal(t, "hooks", hook.seen.Repository)
	require.Equal(t, "feature/hooks", hook.seen.Branch)
	require.NotEmpty(t, hook.seen.EnvID)
	require.Contains(t, hook.seen.EgressHosts, "api.stripe.com")
	require.Equal(t, "sandbox", hook.seen.EgressModes["api.stripe.com"])
	require.NotEmpty(t, hook.seen.Provider)
}

func TestUp_RefusesBeforeItCreatesAnything(t *testing.T) {
	// A policy control that leaves a database branch behind every time it
	// refuses is a resource leak wearing a security feature's clothes. The
	// check runs before the daemon is touched, which is why this test needs no
	// Docker at all: if the ordering were wrong, it would fail trying to reach
	// one.
	registry := extension.NewRegistry()
	registry.AddPolicy(&refusingHook{err: errors.New("refused")})

	dir := t.TempDir()
	_, err := newOrchestrator(t, registry).Up(context.Background())
	require.Error(t, err)

	// Nothing was written into the working directory either.
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestUp_WithNothingRegisteredDoesNotChangeBehaviour(t *testing.T) {
	// The community build. An empty registry has to be indistinguishable from
	// the calls not being there, or adding the hooks changed the product for
	// everybody who does not use them.
	registry := extension.NewRegistry()
	require.True(t, registry.Empty())

	o := newOrchestrator(t, registry)
	_, err := o.Up(context.Background())

	// It will fail for want of a Docker daemon in a sandbox, or succeed with
	// one. What it must never do is fail with a policy refusal, because there
	// is no policy.
	if err != nil {
		require.NotContains(t, err.Error(), "policy",
			"an empty registry refused an environment")
	}
}

type recordingLifecycle struct {
	events []extension.LifecycleEvent
	err    error
}

func (r *recordingLifecycle) Name() string { return "test-meter" }
func (r *recordingLifecycle) Observe(_ context.Context, e extension.LifecycleEvent) error {
	r.events = append(r.events, e)
	return r.err
}

func TestUp_ReportsALifecycleEventEvenWhenItFails(t *testing.T) {
	// A failed creation still consumed capacity: images were pulled, a branch
	// may have been made, time was spent. A meter that only counts successes
	// undercounts exactly the runs that cost the most, so the event is reported
	// either way.
	registry := extension.NewRegistry()
	meter := &recordingLifecycle{}
	registry.AddLifecycle(meter)

	_, _ = newOrchestrator(t, registry).Up(context.Background())

	require.NotEmpty(t, meter.events, "no lifecycle event was reported, so nothing is calling the hook")
	require.Equal(t, "environment.created", meter.events[0].Kind)
	require.Equal(t, "hooks", meter.events[0].Repository)
	require.NotEmpty(t, meter.events[0].EnvID)
}

func TestUp_IsNotStoppedByALifecycleHookThatFails(t *testing.T) {
	// A metering pipeline that is down must not prevent an environment from
	// being torn down, or a billing outage becomes a resource leak. The failure
	// is reported through progress and never returned.
	registry := extension.NewRegistry()
	registry.AddLifecycle(&recordingLifecycle{err: errors.New("the meter is unreachable")})

	var progress []string
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(minimalManifest)+"\n"), 0o644))
	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "main",
		Clock: clock.New(), Redactor: redact.New(),
		Progress:   func(line string) { progress = append(progress, line) },
		Extensions: registry,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = o.Down(ctx)
	})

	_, upErr := o.Up(context.Background())
	if upErr != nil {
		require.NotContains(t, upErr.Error(), "meter is unreachable",
			"a failing lifecycle hook was returned as the reason the environment failed")
	}

	var mentioned bool
	for _, line := range progress {
		if strings.Contains(line, "meter is unreachable") {
			mentioned = true
		}
	}
	require.True(t, mentioned, "the hook's failure was swallowed entirely rather than reported")
}

// ---------------------------------------------------------------------------
// Secrets reach the environment, and sandbox credentials do not reach services
// ---------------------------------------------------------------------------

const secretManifest = `
version: 1
name: secrets
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
    env:
      - name: DATABASE_URL
      - name: STRIPE_SECRET_KEY
      - name: FEATURE_FLAG
        value: preview
      - name: OPTIONAL_THING
        required: false
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
`

func orchestratorWithEnv(t *testing.T, dotenv string, shell map[string]string) (*env.Orchestrator, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(secretManifest)+"\n"), 0o644))
	if dotenv != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"),
			[]byte(strings.TrimSpace(dotenv)+"\n"), 0o600))
	}

	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "main",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(string) {},
		Getenv:   func(k string) string { return shell[k] },
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = o.Down(ctx)
	})
	return o, dir
}

func TestUp_RefusesBeforeAnythingExistsWhenAVariableIsMissing(t *testing.T) {
	// A variable that is absent produces a failure ten seconds later inside a
	// container, in a log nobody is watching, and it looks like the application
	// is broken rather than the configuration. So it is checked first, and this
	// test needs no Docker daemon to prove the ordering.
	o, _ := orchestratorWithEnv(t, "", map[string]string{})
	_, err := o.Up(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-SEC-001")
	// Both missing names, not just the first. Somebody with two to set wants to
	// be told both rather than running the command twice.
	require.Contains(t, err.Error(), "DATABASE_URL")
	require.Contains(t, err.Error(), "STRIPE_SECRET_KEY")
	// The optional one is not reported as missing.
	require.NotContains(t, err.Error(), "OPTIONAL_THING")

	// And the next step names where to put them, including the .env that does
	// not exist yet, which is very often the answer.
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Contains(t, coded.NextStep(), ".env")
	require.Contains(t, coded.NextStep(), "shell")
}

func TestUp_ReadsAVariableFromADotEnvFile(t *testing.T) {
	// Most repositories that need secrets already have one, and asking somebody
	// to duplicate it into a keyring before they can try the product is how a
	// first run fails.
	o, _ := orchestratorWithEnv(t,
		"DATABASE_URL=postgres://from-dotenv\nSTRIPE_SECRET_KEY=sk_test_from_dotenv",
		map[string]string{})

	_, err := o.Up(context.Background())
	// It gets past resolution. Whether it then reaches a daemon is not what
	// this is testing, so only the configuration error is excluded.
	if err != nil {
		require.NotContains(t, err.Error(), "AF-SEC-001",
			"a variable in .env was reported as missing")
	}
}

func TestUp_TheShellBeatsTheFile(t *testing.T) {
	// Somebody who typed an export meant it and is usually debugging.
	o, _ := orchestratorWithEnv(t,
		"DATABASE_URL=postgres://from-dotenv\nSTRIPE_SECRET_KEY=sk_test_a",
		map[string]string{"DATABASE_URL": "postgres://from-shell"})

	_, err := o.Up(context.Background())
	if err != nil {
		require.NotContains(t, err.Error(), "AF-SEC-001")
	}
}

func TestUp_RefusesALiveCredentialInASandboxSlot(t *testing.T) {
	// It would be substituted into every request to that provider, which is the
	// opposite of what sandbox mode is for, and it would charge real cards.
	live := "sk_live_" + strings.Repeat("a", 24)
	o, _ := orchestratorWithEnv(t, "", map[string]string{
		"DATABASE_URL":      "postgres://x",
		"STRIPE_SECRET_KEY": live,
	})

	_, err := o.Up(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-SEC-003")
	require.Contains(t, err.Error(), "STRIPE_SECRET_KEY")
	// The refusal must not quote the credential it is refusing.
	require.NotContains(t, err.Error(), live)
}

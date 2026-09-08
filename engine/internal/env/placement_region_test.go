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
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// The field a residency policy reads, which until placement existed had no
// writer anywhere in the repository.
//
// extension.EnvironmentRequest.Region is documented as "where it will run, when
// the runtime reports one", and the enterprise policy hook's data residency
// rule guards on it being non-empty. Nothing ever set it, so an organization
// that wrote allowed_regions into its policy document got a rule that was
// parsed, described back to the operator at startup, and could not fire. The
// rule was not missing and it was not broken; it was starved of its input,
// which is the hardest of the three to see.
//
// A target's region tag is the first thing in the product that knows the
// answer. This asserts the hook receives it.

const placedManifest = `
version: 1
name: residency
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
    env:
      - name: NEVER_SUPPLIED
runtime:
  provider: local
  targets:
    - name: frankfurt
      tags:
        region: eu-central-1
`

type regionHook struct{ seen *extension.EnvironmentRequest }

func (h *regionHook) Name() string { return "residency" }
func (h *regionHook) Check(_ context.Context, req extension.EnvironmentRequest) error {
	copied := req
	h.seen = &copied
	// Refused, so the run stops here rather than going on to want a Docker
	// daemon. What is being proved is what the hook was told, and it was told
	// it before anything was created.
	return errors.New("refused so that nothing is built")
}

func placedOrchestrator(t *testing.T, body string, registry *extension.Registry) *env.Orchestrator {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(body)+"\n"), 0o644))
	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "feature/residency",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(string) {}, Extensions: registry,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, _ = o.Down(ctx)
	})
	return o
}

func TestUp_TellsThePolicyHookWhichRegionTheEnvironmentIsPlacedIn(t *testing.T) {
	registry := extension.NewRegistry()
	hook := &regionHook{}
	registry.AddPolicy(hook)

	_, err := placedOrchestrator(t, placedManifest, registry).Up(context.Background())
	require.Error(t, err)
	require.NotNil(t, hook.seen, "the hook was never asked")
	require.Equal(t, "eu-central-1", hook.seen.Region,
		"the residency rule guards on this being non-empty, so an empty value "+
			"is a policy that cannot refuse anything")
}

func TestUp_ReportsNoRegionWhenTheManifestDeclaresNoPlacement(t *testing.T) {
	// The control, and it is the state every manifest in the world is in. An
	// empty region is the honest answer for an unlabelled runtime: the engine
	// does not know where a kubeconfig context points, and inventing a region
	// would make a residency rule pass against a guess.
	registry := extension.NewRegistry()
	hook := &regionHook{}
	registry.AddPolicy(hook)

	unplaced := strings.Replace(placedManifest, `runtime:
  provider: local
  targets:
    - name: frankfurt
      tags:
        region: eu-central-1
`, "", 1)
	_, err := placedOrchestrator(t, unplaced, registry).Up(context.Background())
	require.Error(t, err)
	require.NotNil(t, hook.seen)
	require.Empty(t, hook.seen.Region)
}

package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// A value scoped to one service, in no other container, on the real runtime.
//
// TestUp_EachServiceReceivesItsOwnValueForOneVariableName stops at the spec the
// runtime is handed, with a fake runtime receiving it. That proves the
// distribution and nothing after it. The promise a scope makes is about what a
// container can read, and the half after the spec is the local runtime building
// each container's environment out of the proxy variables, the peers, the
// certificate and the service's own map. A runtime that copied one service's
// map into another's container, or everyone's into every container, would pass
// the fake and leak storage's credential to supavisor. So this reads the
// environment Docker actually gave each container.
//
// Three services on purpose. Two scoped readers of one name, so a value
// crossing between them is visible in both directions, and a bystander that
// declares nothing, which is the container a runtime leaking every service's
// map into every container would reach first. A bare DATABASE_URL is present in
// the environment as well, so a scoped lookup that fell back to the bare name
// is a value in a container rather than a silent miss. SHARED_TOKEN is the
// environment's own half, read without a scope by both, so the scope is shown
// not to have broken the ordinary case on the way.
const scopedLiveManifest = `
version: 1
name: envscopelive
services:
  - name: storage
    kind: worker
    build:
      strategy: image
      image: alpine:3.20
    command: "sleep 3600"
    env:
      - name: DATABASE_URL
        scope: service
      - name: SHARED_TOKEN
  - name: supavisor
    kind: worker
    build:
      strategy: image
      image: alpine:3.20
    command: "sleep 3600"
    env:
      - name: DATABASE_URL
        scope: service
      - name: SHARED_TOKEN
  - name: bystander
    kind: worker
    build:
      strategy: image
      image: alpine:3.20
    command: "sleep 3600"
egress:
  default: block
`

func TestUpLive_AScopedValueReachesOnlyItsOwnContainer(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and this brings an environment up")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"),
		[]byte(strings.TrimSpace(scopedLiveManifest)+"\n"), 0o644))
	m, err := manifest.Load(filepath.Join(root, "antifailure.yaml"))
	require.NoError(t, err)

	const (
		storageValue   = "fixture-storage-role"
		supavisorValue = "fixture-supavisor-role"
		bareValue      = "fixture-bare-name-nobody-should-read"
		sharedValue    = "fixture-shared-token"
	)
	values := map[string]string{
		"STORAGE__DATABASE_URL":   storageValue,
		"SUPAVISOR__DATABASE_URL": supavisorValue,
		"DATABASE_URL":            bareValue,
		"SHARED_TOKEN":            sharedValue,
	}
	o, err := New(Options{
		Root: root, Manifest: m, Branch: "envscope-live",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(line string) { t.Log(line) },
		Getenv:   func(k string) string { return values[k] },
	})
	require.NoError(t, err)

	// The golden `af up` makes, taken away again. This manifest declares no
	// database, and that does not mean no golden: the Docker provider is the
	// default, and with nothing made for this project yet, Up builds an empty
	// golden before it branches. It outlives Down by design, so without this
	// every run left one image behind. Registered before Down's cleanup so it
	// runs after it, and only a version that was not here before the test is
	// removed, because one that was is an earlier run's and not this one's.
	identity, err := o.GoldenIdentity()
	require.NoError(t, err)
	before := goldensMadeFor(t, ctx, o.Goldens, identity)
	var made string
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if made != "" && !before[made] {
			if err := o.DestroyGolden(c, made); err != nil {
				t.Errorf("the golden %s af up made could not be removed: %v", made, err)
			}
		}
		requireNothingNewMadeFor(t, c, "Postgres", o.Goldens, identity, before)
	})

	t.Cleanup(func() {
		down, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		td, err := o.Down(down)
		if err != nil {
			t.Errorf("the environment could not be taken down: %v", err)
			return
		}
		for _, p := range td.Pending {
			t.Errorf("teardown left %s %s behind: %s", p.Kind, p.ID, p.Reason)
		}
	})

	result, err := o.Up(ctx)
	if result != nil {
		made = result.Golden
	}
	require.NoError(t, err)

	storage := containerEnv(t, ctx, o, "storage")
	supavisor := containerEnv(t, ctx, o, "supavisor")
	bystander := containerEnv(t, ctx, o, "bystander")

	// Each reader has its own.
	require.Equal(t, storageValue, storage["DATABASE_URL"],
		"storage's container does not hold storage's own DATABASE_URL")
	require.Equal(t, supavisorValue, supavisor["DATABASE_URL"],
		"supavisor's container does not hold supavisor's own DATABASE_URL")

	// And nobody else's, under any name. Searched by value across every
	// variable rather than by the one name, because a leak does not have to
	// arrive under the name it left.
	requireNoValue(t, "supavisor", supavisor, storageValue)
	requireNoValue(t, "storage", storage, supavisorValue)
	for _, v := range []string{storageValue, supavisorValue} {
		requireNoValue(t, "bystander", bystander, v)
	}
	for name, env := range map[string]map[string]string{
		"storage": storage, "supavisor": supavisor, "bystander": bystander,
	} {
		requireNoValue(t, name, env, bareValue)
	}
	// The bystander does have a DATABASE_URL, and it is meant to: the runtime
	// gives every service the environment's own database, and a scoped value
	// replaces that only in the container of the service that declared it. So
	// the bystander is checked by value, above, and not by name.

	// The environment's own half is untouched by the scope beside it.
	require.Equal(t, sharedValue, storage["SHARED_TOKEN"])
	require.Equal(t, sharedValue, supavisor["SHARED_TOKEN"])
	requireNoValue(t, "bystander", bystander, sharedValue)
}

// containerEnv is the environment Docker gave one service's container, read
// from the container itself rather than from anything the engine reports.
func containerEnv(t *testing.T, ctx context.Context, o *Orchestrator, service string) map[string]string {
	t.Helper()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	info, err := cli.ContainerInspect(ctx, "af-svc-"+o.envID+"-"+service, client.ContainerInspectOptions{})
	require.NoError(t, err, "no container for %s", service)
	require.NotNil(t, info.Container.Config)
	out := map[string]string{}
	for _, kv := range info.Container.Config.Env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func requireNoValue(t *testing.T, service string, env map[string]string, value string) {
	t.Helper()
	for k, v := range env {
		require.NotContains(t, v, value, "%s's container holds a value that is not its own, in %s", service, k)
	}
}

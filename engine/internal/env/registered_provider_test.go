package env

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// A provider registered from outside this repository has to be a provider the
// engine actually uses, not one it merely holds.
//
// The socket is worthless otherwise, and worse than worthless: a registry you
// can add a provider to, consulted by nothing, looks exactly like a working
// extension point from the outside and is the same shippable gap as a block
// button that hides nothing. So this brings an environment up on a registered
// database provider and a registered runtime, and takes it down again, which
// is the only thing that proves the selection reaches the lifecycle.
//
// It needs no daemon and no database. The fakes answer for both, which is the
// point: what is under test is the engine's selection and not a provider's own
// behaviour, and a provider's own behaviour is what engine/conformance is for.

// fakeDB is a database provider that is correct enough to bring an environment
// up on and does nothing else.
//
// It does NOT call spec.Mask or spec.Verify, which a real provider must do and
// which the conformance suite checks. Both connect to a candidate Postgres,
// and a test that needed one would prove nothing more about the socket than
// this does while skipping on every machine without a server.
type fakeDB struct {
	name     string
	opened   int
	goldens  []provider.GoldenVersion
	branches map[string]provider.Branch
	closed   bool
}

func newFakeDB(name string) *fakeDB {
	return &fakeDB{name: name, branches: map[string]provider.Branch{}}
}

func (f *fakeDB) Name() string { return f.name }

func (f *fakeDB) Capabilities() provider.Caps {
	return provider.Caps{Branching: true, Subsetting: true, SupportedVersions: []int{16, 17}}
}

func (f *fakeDB) RefreshGolden(
	_ context.Context, spec provider.GoldenSpec,
) (provider.GoldenVersion, error) {
	gv := provider.GoldenVersion{
		ID:         fmt.Sprintf("gv_20260101000000_%08d", len(f.goldens)),
		CreatedAt:  time.Unix(0, 0).UTC(),
		RulesHash:  spec.RulesHash,
		Provenance: spec.Provenance,
		Verified:   true,
	}
	f.goldens = append(f.goldens, gv)
	return gv, nil
}

func (f *fakeDB) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	return f.goldens, nil
}

func (f *fakeDB) DestroyGolden(context.Context, string) error { return nil }

func (f *fakeDB) Branch(_ context.Context, version, envID string) (provider.Branch, error) {
	if b, ok := f.branches[envID]; ok {
		return b, nil
	}
	b := provider.Branch{EnvID: envID, From: version, ProviderRef: "branch-" + envID}
	f.branches[envID] = b
	return b, nil
}

func (f *fakeDB) Reset(context.Context, provider.Branch) error { return provider.ErrUnsupported }

func (f *fakeDB) Destroy(_ context.Context, b provider.Branch) error {
	delete(f.branches, b.EnvID)
	return nil
}

func (f *fakeDB) ConnString(
	_ context.Context, b provider.Branch, _ provider.ConnMode,
) (secret.Value, error) {
	return secret.New("postgres://fake/" + b.ProviderRef), nil
}

func (f *fakeDB) Inventory(context.Context) ([]provider.Resource, error) { return nil, nil }

func (f *fakeDB) Health(context.Context, provider.Branch) (provider.Health, error) {
	return provider.Health{Reachable: true}, nil
}

func (f *fakeDB) Close() error {
	f.closed = true
	return nil
}

// fakeDBProvider is the registration.
type fakeDBProvider struct {
	name string
	db   *fakeDB
	seen extension.DatabaseConfig
}

func (p *fakeDBProvider) Name() string { return p.name }

func (p *fakeDBProvider) Open(
	_ context.Context, cfg extension.DatabaseConfig,
) (provider.Database, error) {
	p.seen = cfg
	p.db.opened++
	return p.db, nil
}

// fakeRT is a runtime that records what it was asked to do.
type fakeRT struct {
	name string
	ups  []provider.EnvSpec
	down []string
}

func (f *fakeRT) Name() string { return f.name }

func (f *fakeRT) Capabilities() provider.RuntimeCaps {
	return provider.RuntimeCaps{Ingress: true}
}

func (f *fakeRT) Up(_ context.Context, spec provider.EnvSpec) (provider.Env, error) {
	f.ups = append(f.ups, spec)
	// Journalled before it is created, the same order a real runtime uses, so
	// that an interrupt leaves something Down can remove.
	for _, svc := range spec.Services {
		if spec.Journal != nil {
			if err := spec.Journal("container", spec.EnvID+"-"+svc.Name); err != nil {
				return provider.Env{}, err
			}
		}
	}
	env := provider.Env{EnvID: spec.EnvID, ProxyReady: true}
	for _, svc := range spec.Services {
		env.Services = append(env.Services, provider.RunningService{
			Name: svc.Name, Kind: svc.Kind, ContainerID: "fake-" + svc.Name,
			URL: "http://127.0.0.1:1/" + svc.Name, Ready: true, State: "running",
		})
	}
	return env, nil
}

func (f *fakeRT) Down(_ context.Context, envID string) (provider.Teardown, error) {
	f.down = append(f.down, envID)
	return provider.Teardown{Removed: 1}, nil
}

func (f *fakeRT) Status(_ context.Context, envID string) (provider.Env, error) {
	return provider.Env{EnvID: envID}, nil
}

func (f *fakeRT) Inventory(context.Context) ([]provider.Resource, error) { return nil, nil }

func (f *fakeRT) Close() error { return nil }

// fakeRTProvider is the registration.
type fakeRTProvider struct {
	name string
	rt   *fakeRT
	seen extension.RuntimeConfig
}

func (p *fakeRTProvider) Name() string { return p.name }

func (p *fakeRTProvider) Open(
	_ context.Context, cfg extension.RuntimeConfig,
) (provider.Runtime, error) {
	p.seen = cfg
	return p.rt, nil
}

const registeredManifest = `
version: 1
name: sockets
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
    build:
      strategy: image
      image: registry.invalid/web:1
database:
  provider: acmedb
runtime:
  provider: acmert
  ttl: 30m
`

func registeredOrchestrator(t *testing.T, reg *extension.Registry) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(registeredManifest)+"\n"), 0o644))
	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := New(Options{
		Root: dir, Manifest: m, Branch: "feature/sockets",
		Clock: clock.New(), Redactor: redact.New(),
		Progress:   func(string) {},
		Extensions: reg,
		Getenv:     func(string) string { return "" },
	})
	require.NoError(t, err)
	return o
}

func TestAnEnvironmentComesUpOnRegisteredProvidersAndGoesDownAgain(t *testing.T) {
	db := newFakeDB("acmedb")
	dbp := &fakeDBProvider{name: "acmedb", db: db}
	rt := &fakeRT{name: "acmert"}
	rtp := &fakeRTProvider{name: "acmert", rt: rt}

	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(dbp)
	reg.AddRuntimeProvider(rtp)

	o := registeredOrchestrator(t, reg)
	ctx := context.Background()

	result, err := o.Up(ctx)
	require.NoError(t, err)
	require.Len(t, result.Services, 1)
	require.Equal(t, "web", result.Services[0].Name)
	require.NotEmpty(t, result.Golden, "the registered provider's golden was not used")

	// The registered runtime is the one that ran it, not the local one.
	require.Len(t, rt.ups, 1)
	require.Equal(t, o.EnvID(), rt.ups[0].EnvID)
	require.Equal(t, "registry.invalid/web:1", rt.ups[0].Services[0].Image)

	// The registered database provider is the one that branched it, and the
	// services were handed its connection string rather than a Docker one.
	require.Len(t, db.branches, 1)
	inside, err := url.Parse(rt.ups[0].DatabaseURL.Reveal())
	require.NoError(t, err)
	require.Equal(t, "fake", inside.Hostname())
	require.True(t, strings.HasPrefix(inside.Path, "/branch-"))
	require.Equal(t, "45000", inside.Port())
	require.Equal(t, []provider.DatabaseRoute{{Port: 45000, Upstream: "fake:5432"}}, rt.ups[0].DatabaseRoutes)

	// What the engine told each registration about itself.
	require.Equal(t, o.opts.Root, dbp.seen.Root)
	require.Equal(t, schema.DBProvider("acmedb"), dbp.seen.Database.Provider)
	require.NotZero(t, dbp.seen.Version, "the Postgres major version was not resolved")
	require.NotNil(t, dbp.seen.Lookup, "a registered provider cannot read a credential")
	require.NotNil(t, dbp.seen.Now, "a registered provider was given no clock")
	require.Equal(t, 30*time.Minute, rtp.seen.TTL, "runtime.ttl did not reach the runtime")
	require.NotNil(t, rtp.seen.ResolveProxyImage,
		"a registered runtime cannot obtain the egress sidecar, so it could only run without one")

	down, err := o.Down(ctx)
	require.NoError(t, err)
	require.NotNil(t, down)
	// The environment itself, plus the two a rolling deploy check would have
	// left beside it. Teardown asks the runtime for all three because a
	// runtime that was only asked about the main one would strand the others.
	require.Contains(t, rt.down, o.EnvID())
	require.Empty(t, db.branches, "the registered provider's branch outlived the environment")
	require.True(t, db.closed)
}

package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// Two services, one variable name, two values, all the way to the environment
// the runtime is asked to create.
//
// The resolver's own tests prove the lookup. This proves the half that made the
// defect reach a user: the resolved values were distributed from one flat map
// keyed by the variable alone, so whichever value the resolver had kept was the
// value every service received. What the runtime is handed is what a container
// gets, since the local runtime copies each service's own map into the
// container's environment last, over everything it injects itself.
const scopedManifest = `
version: 1
name: supabase
services:
  - name: storage
    kind: web
    port: 3000
    build:
      strategy: image
      image: registry.invalid/storage:1
    env:
      - name: DATABASE_URL
        scope: service
  - name: supavisor
    kind: worker
    build:
      strategy: image
      image: registry.invalid/supavisor:1
    env:
      - name: DATABASE_URL
        scope: service
database:
  provider: acmedb
runtime:
  provider: acmert
  ttl: 30m
`

func TestUp_EachServiceReceivesItsOwnValueForOneVariableName(t *testing.T) {
	db := newFakeDB("acmedb")
	rt := &fakeRT{name: "acmert"}
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDBProvider{name: "acmedb", db: db})
	reg.AddRuntimeProvider(&fakeRTProvider{name: "acmert", rt: rt})

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(scopedManifest)+"\n"), 0o644))
	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	// Each service's own value, under the name the scope stores it as.
	values := map[string]string{
		"STORAGE__DATABASE_URL":   "fixture-storage-role",
		"SUPAVISOR__DATABASE_URL": "fixture-supavisor-role",
	}
	o, err := New(Options{
		Root: dir, Manifest: m, Branch: "feature/two-roles",
		Clock: clock.New(), Redactor: redact.New(),
		Progress:   func(string) {},
		Extensions: reg,
		Getenv:     func(name string) string { return values[name] },
	})
	require.NoError(t, err)

	_, err = o.Up(context.Background())
	require.NoError(t, err)
	require.Len(t, rt.ups, 1)

	got := map[string]string{}
	for _, svc := range rt.ups[0].Services {
		got[svc.Name] = svc.Env["DATABASE_URL"].Reveal()
	}
	require.Equal(t, "fixture-storage-role", got["storage"])
	require.Equal(t, "fixture-supavisor-role", got["supavisor"],
		"supavisor received storage's value, which is the defect this exists for")
}

package env_test

import (
	"context"
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
)

// database.seed runs, and the command sees the database.
//
// The regression: the field was validated, refused alongside source_url_env,
// printed by `af explain`, and executed by nothing. A project with no
// production database got an empty environment and no indication that the
// command it configured had never run.
func TestDatabaseSeed_RunsWithTheDatabaseInItsEnvironment(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "seeded.txt")
	writeManifest(t, root, `
version: 1
name: seedtest
services:
  - name: web
    kind: web
    port: 3000
    build:
      strategy: image
      image: nginx:alpine
database:
  provider: docker
  seed: 'printf "%s" "$DATABASE_URL" > `+marker+`'
`)
	o := orchestratorFor(t, root)

	err := env.RunSeedForTest(context.Background(), o, "printf '%s' \"$DATABASE_URL\" > "+marker,
		"postgres://someone:secret@127.0.0.1:5432/candidate")
	require.NoError(t, err)

	body, readErr := os.ReadFile(marker)
	require.NoError(t, readErr, "the seed command did not run")
	require.Contains(t, string(body), "127.0.0.1:5432/candidate",
		"the command has to be told which database to fill")
}

// A seed that fails stops the refresh, and says what it was.
//
// A golden published from a failed seed is an empty database that looks like a
// working one, which is the failure mode a preview environment exists to
// prevent.
func TestDatabaseSeed_AFailureStopsTheRefreshAndSaysWhy(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `
version: 1
name: seedtest
services:
  - name: web
    kind: web
    port: 3000
    build:
      strategy: image
      image: nginx:alpine
database:
  provider: docker
  seed: exit 3
`)
	o := orchestratorFor(t, root)

	err := env.RunSeedForTest(context.Background(), o,
		"echo 'relation does not exist' >&2; exit 3", "postgres://x@127.0.0.1/y")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-DB-013")
	require.Contains(t, err.Error(), "relation does not exist",
		"the reason the script gave has to reach the person reading this")
}

// An empty seed is not a command, and running `sh -c ”` for every refresh
// would be a process spawned to do nothing.
func TestDatabaseSeed_NothingConfiguredRunsNothing(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `
version: 1
name: seedtest
services:
  - name: web
    kind: web
    port: 3000
    build:
      strategy: image
      image: nginx:alpine
database:
  provider: docker
`)
	o := orchestratorFor(t, root)
	require.NoError(t, env.RunSeedForTest(context.Background(), o, "   ", "postgres://x@127.0.0.1/y"))
}

// Changing the seed changes which golden a branch may be made from.
//
// Without it, editing the seed leaves every environment branching the golden
// the old one made, which is stale data that looks current.
func TestDatabaseSeed_ChangingItChangesTheGoldenIdentity(t *testing.T) {
	a := env.SeedRulesHashForTest("insert into users values (1)")
	b := env.SeedRulesHashForTest("insert into users values (1), (2)")
	require.NotEqual(t, a, b)
	require.Equal(t, "empty", env.SeedRulesHashForTest(""))
	require.True(t, strings.HasPrefix(a, "seed-"))
}

func writeManifest(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"), []byte(body), 0o644))
}

func orchestratorFor(t *testing.T, root string) *env.Orchestrator {
	t.Helper()
	m, err := manifest.Load(filepath.Join(root, "antifailure.yaml"))
	require.NoError(t, err)
	o, err := env.New(env.Options{
		Root: root, Manifest: m, Branch: "main",
		Clock:    clock.NewFake(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
		Redactor: redact.New(),
	})
	require.NoError(t, err)
	return o
}

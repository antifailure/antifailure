package env_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// The orchestrator is where af ci will reach for this, so the path through it
// is exercised rather than only the package underneath. A wiring nothing
// tests is the defect this repository has shipped three times.
func TestChange_ReadsARealCheckoutThroughTheOrchestrator(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	}

	write("antifailure.yaml", `version: 1
name: shop
services:
  - name: web
    path: api
    port: 3000
invariants:
  - name: no-orphans
    sql: select 1 from orders where false
`)
	write("api/handler.go", "package api\n")
	git("init", "-q", "-b", "main", ".")
	git("config", "user.email", "a@b.c")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-qm", "base")

	git("checkout", "-qb", "feature")
	write("migrations/001.sql", "ALTER TABLE orders ADD COLUMN status text;\n")
	git("add", "-A")
	git("commit", "-qm", "add a column")

	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	var progress []string
	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "feature",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(line string) { progress = append(progress, line) },
	})
	require.NoError(t, err)

	profile, err := o.Change(context.Background(), env.ChangeOptions{
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)

	assert.Equal(t, "main", profile.Base, "with no remote, main is the base")
	assert.Equal(t, 1, profile.Files)
	assert.False(t, profile.Everything)
	assert.True(t, profile.Selects(change.CheckMigration))
	assert.True(t, profile.Selects(change.CheckInvariants))
	assert.False(t, profile.Selects(change.CheckWorkflows),
		"nothing in this diff is application source or a declared service path")
	assert.Contains(t, strings.Join(progress, "\n"), "reading the diff between main and HEAD")
}

// No environment is brought up and no database is touched, which is the whole
// point: this is what the product can say before it spends anything. The
// manifest here names a provider that would need a daemon, and the call still
// returns.
func TestChange_NeedsNoEnvironmentAndNoDatabase(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(`version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: docker
  version: 17
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pr.diff"), []byte(
		"diff --git a/README.md b/README.md\n"+
			"--- a/README.md\n+++ b/README.md\n@@ -1,0 +2 @@ x\n+a line\n"), 0o644))

	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: "feature",
		Clock: clock.New(), Redactor: redact.New(), Progress: func(string) {},
	})
	require.NoError(t, err)

	profile, err := o.Change(context.Background(), env.ChangeOptions{
		DiffPath: filepath.Join(dir, "pr.diff"),
	})
	require.NoError(t, err)
	assert.Empty(t, profile.Selected(), "a change to prose selects nothing")
}

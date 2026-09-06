package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// The workflow af init writes and the workflow the documentation shows have
// to be the same bytes. Two copies of one file drift the way two copies of
// anything drift: somebody fixes the example, the embedded one keeps writing
// the old flag, and the failure lands on the customer who never saw either.
func TestTheEmbeddedWorkflowIsTheExample(t *testing.T) {
	t.Parallel()
	example, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "github-workflow.yml"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(example, cli.WorkflowTemplate()),
		"engine/internal/cli/templates/antifailure.yml differs from examples/github-workflow.yml.\n"+
			"examples/github-workflow.yml is the source of truth. Copy it over the template:\n"+
			"  cp examples/github-workflow.yml engine/internal/cli/templates/antifailure.yml")
}

// gitConfig writes the one file githubRemote reads, so a test can put a
// checkout on GitHub without a git binary.
func gitConfig(t *testing.T, dir, remoteURL string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	body := "[core]\n\trepositoryformatversion = 0\n"
	if remoteURL != "" {
		body += "[remote \"origin\"]\n\turl = " + remoteURL + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(body), 0o600))
}

func nodeProject(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"shop","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))
}

// af init writes the workflow when the checkout is on GitHub, and the manifest
// carries the block the workflow reads.
func TestInit_WritesTheWorkflowWhenTheRemoteIsGitHub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nodeProject(t, dir)
	gitConfig(t, dir, "git@github.com:acme/shop.git")

	got := runCLI(t, dir, nil, "init", "--non-interactive", "-o", "json")
	require.Zero(t, got.code, got.stderr)
	var report cli.InitReport
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))

	wf := filepath.Join(dir, ".github", "workflows", "antifailure.yml")
	body, err := os.ReadFile(wf)
	require.NoError(t, err, "the workflow was not written")
	require.Equal(t, cli.WorkflowTemplate(), body)
	require.Equal(t, "written", report.Workflow)
	require.Contains(t, report.Written, wf)

	manifest, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), "github:")
	require.Contains(t, string(manifest), "mode: actions")
	require.Contains(t, string(manifest), "comment: true")
	require.Contains(t, string(manifest), "fork_policy: label")

	// And the text form lists it under Written, which is where a reader
	// looks for what the command did.
	text := runCLI(t, dir, nil, "init", "--non-interactive", "--force")
	require.Zero(t, text.code, text.stderr)
	written := text.stdout[strings.Index(text.stdout, "Written"):]
	require.Contains(t, written, "antifailure.yaml")
	require.Contains(t, prose(written), "already the workflow af init writes",
		"a second run has to say the file was already there rather than claim to have written it")
}

func TestInit_NoGitHubRemoteWritesNoWorkflowAndSaysHowToGetOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nodeProject(t, dir)
	gitConfig(t, dir, "https://gitlab.com/acme/shop.git")

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Zero(t, got.code, got.stderr)
	require.NoFileExists(t, filepath.Join(dir, ".github", "workflows", "antifailure.yml"))
	require.Contains(t, prose(got.stdout), "af github init")

	manifest, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "github:",
		"a block for a workflow that was not written describes nothing")
}

// A workflow somebody edited is theirs. af init leaves it and says so.
func TestInit_LeavesADifferentWorkflowAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nodeProject(t, dir)
	gitConfig(t, dir, "https://github.com/acme/shop")
	wf := filepath.Join(dir, ".github", "workflows", "antifailure.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(wf), 0o755))
	require.NoError(t, os.WriteFile(wf, []byte("name: mine\non: push\n"), 0o600))

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Zero(t, got.code, got.stderr)
	body, err := os.ReadFile(wf)
	require.NoError(t, err)
	require.Equal(t, "name: mine\non: push\n", string(body), "the edited workflow was replaced")
	require.Contains(t, prose(got.stdout), "differs from the workflow af init writes")
	require.Contains(t, prose(got.stdout), "af github init --force")
}

// af github init is the door for a project that already has a manifest.
func TestGitHubInit_WritesTheWorkflowAndTheBlockAndNamesTheSecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	writeManifest(t, dir, `version: 1
name: shop
services:
  - name: web
    path: api
    port: 3000
database:
  provider: docker
  source_url_env: PRODUCTION_DATABASE_URL
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
personas:
  - name: shopper
    email: shopper@example.com
workflows:
  - name: checkout
    persona: shopper
    description: Put the sample item in the basket and pay with the test card.
`)

	got := runCLI(t, dir, nil, "github", "init")
	require.Zero(t, got.code, got.stderr)

	wf := filepath.Join(dir, ".github", "workflows", "antifailure.yml")
	body, err := os.ReadFile(wf)
	require.NoError(t, err)
	require.Equal(t, cli.WorkflowTemplate(), body)

	manifest, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), "fork_policy: label")
	require.Contains(t, string(manifest), "source_url_env: PRODUCTION_DATABASE_URL",
		"adding the block must not lose what was in the file")
	explained := runCLI(t, dir, nil, "explain")
	require.Zero(t, explained.code, "the manifest with the block added no longer parses: %s", explained.stderr)

	said := prose(got.stdout)
	for _, name := range []string{"ANTHROPIC_API_KEY", "AF_MASKING_KEY", "PRODUCTION_DATABASE_URL",
		"STRIPE_TEST_SECRET_KEY", "AF_CONTROL_PLANE"} {
		require.Contains(t, said, name)
	}
	require.Contains(t, said, "optional")

	// Idempotent: the second run changes nothing and says so.
	again := runCLI(t, dir, nil, "github", "init", "-o", "json")
	require.Zero(t, again.code, again.stderr)
	var rep cli.GitHubInitReport
	require.NoError(t, json.Unmarshal([]byte(again.stdout), &rep))
	require.True(t, rep.Unchanged)
	require.False(t, rep.Written)
	require.False(t, rep.Configured, "the block was added twice")
	twice, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(twice), "\ngithub:"))
}

func TestGitHubInit_ReplacesADifferentWorkflowOnlyWithForce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "version: 1\nname: shop\nservices:\n  - name: web\n    port: 3000\n")
	wf := filepath.Join(dir, ".github", "workflows", "antifailure.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(wf), 0o755))
	require.NoError(t, os.WriteFile(wf, []byte("name: mine\non: push\n"), 0o600))

	kept := runCLI(t, dir, nil, "github", "init")
	require.Zero(t, kept.code, kept.stderr)
	body, err := os.ReadFile(wf)
	require.NoError(t, err)
	require.Equal(t, "name: mine\non: push\n", string(body))
	require.Contains(t, prose(kept.stdout), "--force")

	forced := runCLI(t, dir, nil, "github", "init", "--force")
	require.Zero(t, forced.code, forced.stderr)
	body, err = os.ReadFile(wf)
	require.NoError(t, err)
	require.Equal(t, cli.WorkflowTemplate(), body)
}

func TestGitHubInit_NeedsAManifest(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "github", "init")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "AF-MAN-001")
}

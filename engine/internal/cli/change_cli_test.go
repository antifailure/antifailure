package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changeProject writes the smallest project af change needs, plus a diff to
// read. The diff is a file rather than a git history because this is a test of
// the command, and the git path has its own tests next to the parser.
func changeProject(t *testing.T, diff string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(`
version: 1
name: shop
services:
  - name: web
    path: api
    port: 3000
personas:
  - name: shopper
    email: shopper@example.com
workflows:
  - name: checkout
    persona: shopper
    description: Sign in, put the sample item in the basket, pay with the test card and see the order confirmation.
`), 0o600))
	path := filepath.Join(dir, "pr.diff")
	require.NoError(t, os.WriteFile(path, []byte(diff), 0o600))
	return dir
}

const codeDiff = `diff --git a/api/handler.go b/api/handler.go
index 1111111..2222222 100644
--- a/api/handler.go
+++ b/api/handler.go
@@ -1,0 +2,1 @@ package api
+func Handle() {}
`

const proseDiff = `diff --git a/README.md b/README.md
index 1111111..2222222 100644
--- a/README.md
+++ b/README.md
@@ -1,0 +2,1 @@ # shop
+A sentence.
`

// An infrastructure only pull request: a database engine version, a server
// parameter, a replica count and a security group rule, and not one line of
// application source.
const infraDiff = `diff --git a/infra/rds.tf b/infra/rds.tf
index 1111111..2222222 100644
--- a/infra/rds.tf
+++ b/infra/rds.tf
@@ -6,0 +7,5 @@ resource "aws_db_instance" "primary" {
+  engine_version = "16.1"
+  parameter {
+    name  = "lock_timeout"
+    value = "5000"
+  }
diff --git a/infra/ecs.tf b/infra/ecs.tf
index 3333333..4444444 100644
--- a/infra/ecs.tf
+++ b/infra/ecs.tf
@@ -11,0 +12,1 @@ resource "aws_ecs_service" "api" {
+  desired_count = 6
diff --git a/infra/security.tf b/infra/security.tf
index 5555555..6666666 100644
--- a/infra/security.tf
+++ b/infra/security.tf
@@ -2,0 +3,2 @@ resource "aws_security_group" "api" {
+resource "aws_security_group_rule" "outbound" {
+  cidr_blocks = ["0.0.0.0/0"]
`

// The whole chain, at the boundary where it decides whether anything runs.
//
// The analyser selecting a check is half of a feature. The other half is the
// GITHUB_OUTPUT line, because the published action gates the entire run on
// steps.change.outputs.environment being 'true': while the infrastructure
// surface selected nothing, that line said false and an infrastructure only
// pull request was not rehearsed at all. So this reads the file the workflow
// reads rather than the profile the test could reach more easily.
func TestChange_AnInfrastructureOnlyDiffTellsTheJobToRunTheCheck(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, infraDiff)
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", filepath.Join(dir, "pr.diff"))
	require.Zero(t, res.code, res.stderr)

	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")

	assert.Contains(t, lines, "environment=true",
		"this is the value action.yml gates the run on, so false here means nothing runs at all")
	assert.Contains(t, lines, "workflows=true")
	assert.Contains(t, lines, "migration=true",
		"an added line moves the database engine version and another sets a server parameter")
	assert.Contains(t, lines, "egress=true",
		"an added line declares a security group rule")
	assert.Contains(t, lines, "load=false",
		"the replica count selects load and this manifest declares none, so the conjunction is false")
	assert.Contains(t, lines, "masking=false")
	assert.Contains(t, lines, "invariants=false")
	assert.Contains(t, lines, "selected=egress,environment,migration,workflows")

	// And the reasoning a reviewer reads, so that a true output is not the only
	// thing standing behind the run.
	assert.Contains(t, res.stdout, "an added line sets a database engine version of 16")
	assert.Contains(t, res.stdout, "Nothing in a run applies infrastructure as code")
}

func TestChange_ExplainsTheDiffAndTheReasoning(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)

	res := runCLI(t, dir, nil, "change", "--diff", filepath.Join(dir, "pr.diff"))
	require.Zero(t, res.code, res.stderr)
	assert.Contains(t, res.stdout, "the web service")
	assert.Contains(t, res.stdout, "manifest.service")
	assert.Contains(t, res.stdout, "What this cannot see")
	assert.Contains(t, res.stdout, "run   workflows")
	assert.Contains(t, res.stdout, "skip  migration")
}

func TestChange_WritesTheReportSectionAndTheJobOutputs(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)
	section := filepath.Join(dir, "section.md")
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", filepath.Join(dir, "pr.diff"), "--write", section)
	require.Zero(t, res.code, res.stderr)

	body, err := os.ReadFile(section)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(body), "<!-- antifailure:report -->"),
		"without the marker a workflow leaves a second comment instead of updating the first")
	assert.Contains(t, string(body), "**What this change touches.**")
	assert.Contains(t, string(body), "| `workflows` | selected | yes |")

	// The outputs are what makes this actionable rather than a note on the
	// run: a later step reads them and skips work this change does not need.
	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	assert.Contains(t, lines, "workflows=true")
	assert.Contains(t, lines, "migration=false")
	assert.Contains(t, lines, "invariants=false",
		"the manifest declares no invariants, so a step must not be told to run them")
	assert.Contains(t, lines, "load=false",
		"a code change selects load and this manifest has none, so the value is the conjunction and not the selection")
	assert.Contains(t, lines, "selected=environment,workflows")
}

// The saving the whole command exists to make, proved at the command boundary
// rather than only in the analyser.
func TestChange_AProseOnlyDiffTellsAJobToRunNothing(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, proseDiff)
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", filepath.Join(dir, "pr.diff"))
	require.Zero(t, res.code, res.stderr)

	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	assert.Contains(t, string(written), "environment=false")
	assert.Contains(t, string(written), "selected=\n")
}

func TestChange_RendersJSONForAScript(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)

	res := runCLI(t, dir, nil, "change", "--diff", filepath.Join(dir, "pr.diff"), "-o", "json")
	require.Zero(t, res.code, res.stderr)

	var profile struct {
		Files int `json:"files"`
		Facts []struct {
			Path, Rule, Evidence string
		} `json:"facts"`
		Plan []struct {
			Check     string
			Selected  bool
			Available bool
		} `json:"plan"`
		Blind []string `json:"blind"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &profile))
	assert.Equal(t, 1, profile.Files)
	assert.Len(t, profile.Plan, 7)
	assert.NotEmpty(t, profile.Blind)
	for _, f := range profile.Facts {
		assert.NotEmpty(t, f.Rule, "%s has no rule", f.Path)
		assert.NotEmpty(t, f.Evidence, "%s has no evidence", f.Path)
	}
}

// Every other command needs antifailure.yaml. This one does not, and that is
// deliberate rather than lenient: what a diff touches is a fact about the
// repository, and the checks are then all reported unavailable for the one
// honest reason. It is also the first thing somebody can run here, so it has
// to work before they have written anything.
func TestChange_WorksInARepositoryWithNoManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pr.diff")
	require.NoError(t, os.WriteFile(path, []byte(codeDiff), 0o600))
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", path)
	require.Zero(t, res.code, res.stderr)
	assert.NotContains(t, res.stderr, "AF-MAN-001")

	// The facts about the file are still produced, because they do not come
	// from the manifest.
	assert.Contains(t, res.stdout, "api/handler.go")
	assert.Contains(t, res.stdout, "path.code")

	// And every check says the same true thing rather than claiming to run.
	assert.Contains(t, res.stdout, "no manifest was loaded")
	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	assert.Contains(t, string(written), "environment=false",
		"nothing is configured, so no step may be told to do work")
	assert.Contains(t, string(written), "selected=\n")
}

// A manifest that exists and is broken must not be downgraded to no manifest.
// Reporting "nothing is configured" to somebody who configured it and made a
// typo would turn their mistake into our silence.
func TestChange_ABrokenManifestIsStillAnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte("version: 1\nname: shop\nservices: [[[\n"), 0o600))
	path := filepath.Join(dir, "pr.diff")
	require.NoError(t, os.WriteFile(path, []byte(codeDiff), 0o600))

	res := runCLI(t, dir, nil, "change", "--diff", path)
	require.NotZero(t, res.code, "a manifest that does not parse is an error, not an absence")
	assert.NotContains(t, res.stdout, "no manifest was loaded")
}

// A diff file that is not a diff is a user error with a code, not a panic and
// not an empty profile that looks like a change touching nothing.
func TestChange_ReportsAMissingDiffFile(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)

	res := runCLI(t, dir, nil, "change", "--diff", filepath.Join(dir, "absent.diff"))
	require.NotZero(t, res.code)
	assert.Contains(t, res.stderr, "AF-DET-011")
}

// The two outputs that let a job export exactly the secrets the manifest
// reads. Names, never values: the job looks each one up in the caller's
// secrets, and a name missing here is a secret the engine never sees.
func TestChange_NamesTheVariablesTheManifestReads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(`
version: 1
name: shop
services:
  - name: web
    path: api
    port: 3000
    env:
      - name: RESEND_API_KEY
        sandbox: true
      - name: SENTRY_DSN
        from: SENTRY_DSN_PREVIEW
      - name: NODE_ENV
        value: test
database:
  provider: neon
  project: shop
  api_key_env: NEON_API_KEY
  source_url_env: PRODUCTION_DATABASE_URL
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
auth:
  adapter: clerk
  sandbox: true
  token_env: CLERK_SECRET_KEY
personas:
  - name: shopper
    email: shopper@example.com
workflows:
  - name: checkout
    persona: shopper
    description: Sign in, put the sample item in the basket, pay with the test card and see the order confirmation.
`), 0o600))
	diff := filepath.Join(dir, "pr.diff")
	require.NoError(t, os.WriteFile(diff, []byte(codeDiff), 0o600))
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs}, "change", "--diff", diff)
	require.Zero(t, res.code, res.stderr)

	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	assert.Contains(t, lines, "source_url_env=PRODUCTION_DATABASE_URL")
	assert.Contains(t, lines,
		"secrets=CLERK_SECRET_KEY,NEON_API_KEY,PRODUCTION_DATABASE_URL,RESEND_API_KEY,SENTRY_DSN_PREVIEW,STRIPE_SECRET_KEY",
		"every variable the manifest reads a credential from, sorted, and nothing that holds a literal value")
	assert.NotContains(t, string(written), "NODE_ENV",
		"a variable with a literal value is not a secret the job has to export")
}

// With no source the key is still written, empty, so a workflow expression
// reads an empty string rather than a missing output.
func TestChange_WritesAnEmptySourceKeyWhenTheManifestNamesNone(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", filepath.Join(dir, "pr.diff"))
	require.Zero(t, res.code, res.stderr)
	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	assert.Contains(t, lines, "source_url_env=")
	assert.Contains(t, lines, "secrets=")
}

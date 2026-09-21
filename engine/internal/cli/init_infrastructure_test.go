package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// af init, end to end, on a repository with Terraform in it.
//
// The detect package's own tests stop at the draft in memory. Everything after
// that is where a section goes quiet: the draft is YAML encoded, validated
// against the real directory, written, and summarized, and a section that
// never reaches the file or never reaches the summary is a feature that passed
// every test it had. So this runs the real command in a real directory and
// reads the real file and the real output.

// terraformFixture is a web application with a Terraform stack, one module the
// stack calls, and three variable files none of which is evidence about
// production.
func terraformFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"name":"shopfront","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
		"infra/terraform/main.tf": `
module "network" {
  source = "./modules/network"
}

resource "aws_ecs_service" "web" {}
`,
		"infra/terraform/modules/network/main.tf": `resource "aws_vpc" "this" {}`,
		"infra/terraform/production.tfvars":       `image = "shopfront:1"`,
		"infra/terraform/staging.tfvars":          `image = "shopfront:2"`,
		"infra/terraform/dev.tfvars":              `image = "shopfront:3"`,
	} {
		filename := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
		require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
	}
	return dir
}

func TestInitInfrastructure_TheSectionReachesTheWrittenManifest(t *testing.T) {
	dir := terraformFixture(t)
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	written := string(body)

	require.Contains(t, written, "infrastructure:")
	require.Contains(t, written, "stacks:")
	require.Contains(t, written, "source: terraform")
	require.Contains(t, written, "path: infra/terraform")
	// The module the stack calls is a building block, not a root module, and
	// naming it would point the comparison at a library. Asserted on the file
	// rather than on the draft, because this is the artifact somebody commits.
	require.NotContains(t, written, "infra/terraform/modules/network")
	// Neither guess is written. A name is not a fact about production, and
	// this is the one section nothing downstream can check.
	require.NotContains(t, written, "workspace:")
	require.NotContains(t, written, "var_files:")
}

func TestInitInfrastructure_TheSummarySaysWhatWasLeftBlank(t *testing.T) {
	got := runCLI(t, terraformFixture(t), nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	require.Contains(t, got.stdout, "Infrastructure as code")
	require.Contains(t, got.stdout, "infra/terraform")
	// The half that was left out on purpose. Without this sentence the two
	// empty keys read as a feature nobody finished rather than as a decision,
	// and the reader has no reason to fill them in.
	// Asserted as two short phrases rather than one sentence, because the
	// summary hard wraps to the terminal width and a Contains over a wrapped
	// line fails on a newline the page put there rather than on the sentence
	// being wrong.
	require.Contains(t, got.stdout, "var_files are empty")
	require.Contains(t, got.stdout, "describes production")
}

func TestInitInfrastructure_ARepositoryWithNoTerraformPrintsNoneOfIt(t *testing.T) {
	// The direction that makes the section above worth having. A heading that
	// appears on every run is a heading nobody reads.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"plain","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	require.NotContains(t, got.stdout, "Infrastructure as code")

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(body), "infrastructure:")
}

func TestInitInfrastructure_AModuleLibrarySaysWhyNothingWasDrafted(t *testing.T) {
	// Terraform is here and none of it is applied on its own. A silent absence
	// would read exactly like a repository with no Terraform at all, and the
	// reader would never learn that the section is theirs to write by hand.
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"name":"blocks","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"modules/network/main.tf":         "module \"subnets\" {\n  source = \"./subnets\"\n}\n",
		"modules/network/subnets/main.tf": `resource "aws_subnet" "this" {}`,
	} {
		filename := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
		require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
	}

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 0, got.code, got.stderr)
	require.Contains(t, got.stdout, "Infrastructure as code")
	require.Contains(t, got.stdout, "is called as a module by another")

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(body), "infrastructure:")
}

func TestInitInfrastructure_TheWrittenManifestExplainsTheSection(t *testing.T) {
	// af explain reads the file af init wrote and prints the effective
	// configuration. The section has to survive the round trip, because a
	// manifest key that parses and never appears in the one command whose job
	// is to show what is in force is a key nobody can confirm they set.
	dir := terraformFixture(t)
	require.Equal(t, 0, runCLI(t, dir, nil, "init", "--non-interactive").code)

	got := runCLI(t, dir, nil, "explain")
	require.Equal(t, 0, got.code, got.stderr)
	require.Contains(t, got.stdout, "Infrastructure")
	require.Contains(t, got.stdout, "infra/terraform, declared by terraform")
	require.Contains(t, got.stdout, "none, so the default workspace")
	require.Contains(t, got.stdout, "none, so the stack's own defaults")
}

package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/iac"
	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func infraTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	return dir
}

func oneStack(path string, varFiles ...string) *schema.Infrastructure {
	return &schema.Infrastructure{Stacks: []schema.InfraStack{{
		Source: schema.InfraTerraform, Path: path, VarFiles: varFiles,
	}}}
}

func read(t *testing.T, root string, infra *schema.Infrastructure) ([]provider.CloudResource, []iac.Unmeasured) {
	t.Helper()
	res, unmeasured, err := cloudResources(context.Background(), root, infra)
	require.NoError(t, err)
	return res, unmeasured
}

func byName(res []provider.CloudResource) map[string]provider.CloudResource {
	out := map[string]provider.CloudResource{}
	for _, r := range res {
		out[r.Name] = r
	}
	return out
}

// TestEverySeedableTypeIsClassified is the check that closes the hole this
// whole file could otherwise open.
//
// engine/pkg/emulator's own comment says a declaration silently absent from
// the plan is "a resource missing from the twin that nothing ever mentioned",
// and it is careful to emit a step even for a type it cannot create. This file
// FILTERS, by kind, before the planner ever sees a declaration, which moves
// that failure one layer up: a type the emulator can create and
// engine/internal/iac does not classify as a cloud resource is dropped here,
// silently, and the planner never gets the chance to say anything about it.
//
// So the two tables are compared, by asking the emulator's own registry rather
// than by keeping a second list here, because a second list is how two tables
// stop matching. It caught the drift the first time it ran:
// aws_kinesis_stream and aws_cloudwatch_event_bus are both creatable by the
// emulator and neither was classified, so production's streams and event buses
// would have been missing from every twin with nothing to say so.
func TestEverySeedableTypeIsClassified(t *testing.T) {
	t.Parallel()
	// The LIVE table, not a regular expression over its source. emulator
	// exports exactly the question this test needs to ask, and asking the
	// registry itself means a planner registered in a way no pattern
	// anticipated still counts.
	planned := emulator.SeedTypes()
	require.GreaterOrEqual(t, len(planned), 10,
		"engine/pkg/emulator reports only %d seedable types, which is too few to be the real "+
			"list, so this test could not look", len(planned))

	root := t.TempDir()
	for _, typ := range planned {
		t.Run(typ, func(t *testing.T) {
			dir := filepath.Join(root, strings.ReplaceAll(typ, "/", "_"))
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"),
				[]byte("resource \""+typ+"\" \"x\" {\n  name = \"thing\"\n}\n"), 0o600))

			r, err := iac.Read(context.Background(), dir)
			require.NoError(t, err)
			require.Len(t, r.Components, 1)
			require.Truef(t, seedableKinds[r.Components[0].Kind],
				"engine/pkg/emulator can create a %s and engine/internal/iac classifies it as "+
					"%q, which this file does not pass on, so production's %s would be missing "+
					"from every twin and nothing would say so",
				typ, r.Components[0].Kind, typ)
		})
	}

	// The falsification arm. A type the emulator cannot create must NOT be
	// seedable, or the assertion above passes for anything and says nothing.
	dir := filepath.Join(root, "not-a-cloud-resource")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"),
		[]byte("resource \"azurerm_role_assignment\" \"x\" {\n  name = \"thing\"\n}\n"), 0o600))
	r, err := iac.Read(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, r.Components, 1)
	require.False(t, seedableKinds[r.Components[0].Kind],
		"a role assignment is seedable, so the instrument above would accept anything")
}

// TestACredentialNeverReachesASeedingRequest is the rule in this file with no
// exception.
//
// A seeding request is built into a command and written into a run's journal,
// so a withheld value arriving here would put production's password somewhere
// far more durable than a report.
func TestACredentialNeverReachesASeedingRequest(t *testing.T) {
	t.Parallel()
	const marker = "FIXTURE-VALUE-NOT-A-CREDENTIAL"
	root := infraTree(t, map[string]string{"infra/main.tf": `
resource "aws_secretsmanager_secret" "session" {
  name = "session-key"
}
resource "aws_ssm_parameter" "token" {
  name  = "api-token"
  type  = "SecureString"
  value = "` + marker + `"
}
`})
	res, unmeasured := read(t, root, oneStack("infra"))

	for _, r := range res {
		for k, v := range r.Attributes {
			require.NotContainsf(t, v, marker,
				"a withheld credential reached a seeding request under key %q", k)
		}
	}
	require.NotContains(t, renderUnmeasured(unmeasured), marker,
		"a withheld credential reached the unmeasured report")

	// The control arm: the resources ARE passed on, and their non credential
	// attributes with them, so the clean result above is the withholding
	// working rather than nothing being produced.
	got := byName(res)
	require.Contains(t, got, "session-key")
	require.Contains(t, got, "api-token")
	require.Equal(t, "SecureString", got["api-token"].Attributes["type"],
		"the parameter's type is not a credential and the emulator needs it")

	// And the withheld value is NAMED, so a twin that differs from production
	// in a way nobody was told about is impossible.
	var named bool
	for _, u := range unmeasured {
		if strings.Contains(u.What, "value") && u.Withheld {
			named = true
		}
	}
	require.True(t, named, "the withheld value was dropped without being mentioned")
}

// TestAResourceWithNoResolvableNameIsNotCreated covers the second refusal.
func TestAResourceWithNoResolvableNameIsNotCreated(t *testing.T) {
	t.Parallel()
	root := infraTree(t, map[string]string{"infra/main.tf": `
variable "env" {
  type = string
}
resource "aws_s3_bucket" "assets" {
  bucket = "${var.env}-assets"
}
resource "aws_s3_bucket" "logs" {
  bucket = "production-logs"
}
`})
	res, unmeasured := read(t, root, oneStack("infra"))

	got := byName(res)
	require.NotContains(t, got, "${var.env}-assets",
		"a bucket was created under an unresolved interpolation, which the application will "+
			"never ask for")
	require.NotContains(t, got, "assets",
		"a bucket was created under its Terraform label rather than under its real name")
	require.Len(t, res, 1, "the resolvable bucket was lost as well")
	require.Contains(t, got, "production-logs")

	require.Contains(t, renderUnmeasured(unmeasured), "its name is not resolvable",
		"a bucket that could not be created was dropped without being mentioned")
}

// TestACountOfZeroIsNotAHole covers the third refusal, and its opposite.
func TestACountOfZeroIsNotAHole(t *testing.T) {
	t.Parallel()
	root := infraTree(t, map[string]string{"infra/main.tf": `
variable "portal" {
  type = bool
}
resource "aws_sqs_queue" "never" {
  count = 0
  name  = "never"
}
resource "aws_sqs_queue" "maybe" {
  count = var.portal ? 1 : 0
  name  = "maybe"
}
resource "aws_sqs_queue" "always" {
  name = "always"
}
`})
	res, unmeasured := read(t, root, oneStack("infra"))
	got := byName(res)

	require.NotContains(t, got, "never",
		"a resource the configuration deliberately does not deploy was created in the twin")
	require.Contains(t, got, "always")
	require.Contains(t, got, "maybe",
		"a resource behind an unresolvable count was not created; a missing queue breaks an "+
			"application that reads it, where an unused one costs nothing")

	report := renderUnmeasured(unmeasured)
	require.Contains(t, report, "whether production has the queue maybe",
		"an uncertain resource was created without saying that it is uncertain")
	require.NotContains(t, report, "queue never",
		"a resource the configuration says production does not have was reported as a hole")
}

// TestANestedBlockAlsoPublishesItsFlatName covers the mismatch this file
// found between the two sides of the seam.
//
// engine/pkg/emulator reads flat Terraform attribute names, `versioning`, and
// engine/internal/iac reports a nested block dotted, `versioning.enabled`. A
// bucket declaring versioning the legacy way would have had it silently not
// reproduced, and the run would have reported the bucket as created.
func TestANestedBlockAlsoPublishesItsFlatName(t *testing.T) {
	t.Parallel()
	root := infraTree(t, map[string]string{"infra/main.tf": `
resource "aws_s3_bucket" "assets" {
  bucket = "assets"
  versioning {
    enabled = true
  }
}
resource "aws_s3_bucket" "logs" {
  bucket = "logs"
  versioning {
    enabled     = true
    mfa_delete  = false
  }
}
`})
	res, _ := read(t, root, oneStack("infra"))
	got := byName(res)

	require.Equal(t, "true", got["assets"].Attributes["versioning"],
		"a single attribute block did not publish its flat name, so the emulator reads no "+
			"versioning for a bucket that declares it")
	require.Equal(t, "true", got["assets"].Attributes["versioning.enabled"],
		"the dotted name was lost, which is the faithful one")

	// A block with TWO attributes has no single meaning to collapse to, and
	// inventing one would put a value under a key that means something else.
	require.NotContains(t, got["logs"].Attributes, "versioning",
		"a block with two attributes was collapsed to one value")
	require.Equal(t, "true", got["logs"].Attributes["versioning.enabled"])
}

// TestVariableFilesAreRebasedOntoTheStack covers a path bug that would be
// entirely silent.
func TestVariableFilesAreRebasedOntoTheStack(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"infra/stacks/prod/main.tf": `
variable "bucket_name" {
  type = string
}
resource "aws_s3_bucket" "assets" {
  bucket = var.bucket_name
}
`,
		"infra/stacks/prod/production.tfvars": `bucket_name = "production-assets"`,
	}
	root := infraTree(t, files)

	// Named, repository relative, as the manifest writes it.
	res, _ := read(t, root, oneStack("infra/stacks/prod", "infra/stacks/prod/production.tfvars"))
	require.Len(t, res, 1, "the variable file did not resolve, so the bucket had no name")
	require.Equal(t, "production-assets", res[0].Name,
		"the manifest's repository relative variable file was not rebased onto the stack "+
			"directory, so it silently did not apply")

	// The control arm: without it named, the same tree yields nothing, so the
	// result above is the rebasing working rather than a default.
	none, _ := read(t, root, oneStack("infra/stacks/prod"))
	require.Empty(t, none, "the bucket resolved without its variable file, so naming one "+
		"proves nothing")
}

// TestAnAbsentInfrastructureSectionIsNotAnEmptyProduction.
func TestAnAbsentInfrastructureSectionIsNotAnEmptyProduction(t *testing.T) {
	t.Parallel()
	res, unmeasured, err := cloudResources(context.Background(), t.TempDir(), nil)
	require.NoError(t, err)
	require.Empty(t, res)
	require.Empty(t, unmeasured,
		"a manifest with no infrastructure section produced a complaint; its absence is "+
			"reported by whoever notices the section is missing, not as an unmeasured value here")
}

// TestUnreadableFilesTravelWithTheResources.
//
// A file the reader could not parse may have held three buckets. A run that
// created the two it did read and reported two would be the overstatement this
// whole package exists to prevent.
func TestUnreadableFilesTravelWithTheResources(t *testing.T) {
	t.Parallel()
	root := infraTree(t, map[string]string{
		"infra/good.tf":   "resource \"aws_s3_bucket\" \"a\" {\n  bucket = \"a\"\n}\n",
		"infra/broken.tf": "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\n}\n",
	})
	res, unmeasured := read(t, root, oneStack("infra"))
	require.Len(t, res, 1)
	require.Contains(t, renderUnmeasured(unmeasured), "broken.tf",
		"a file the reader refused was not mentioned beside the resources it did produce")
}

func renderUnmeasured(us []iac.Unmeasured) string {
	var sb strings.Builder
	for _, u := range us {
		sb.WriteString(u.String())
		sb.WriteString("\n")
	}
	return sb.String()
}

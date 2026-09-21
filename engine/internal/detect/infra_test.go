package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/detect"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Locating the infrastructure as code, which is a different job from reading
// it and is deliberately the only one this analyzer does.
//
// THE MISTAKE THESE TESTS ARE AIMED AT. Terraform is not one directory, it is
// a call graph, and a glob for directories holding a .tf file returns the
// library as well as the thing built from it. A repository with a stack and
// four modules under it has five such directories and ONE root module, and a
// manifest naming all five points the comparison at four sets of building
// blocks. Nothing downstream would report that: a module produces resources
// when it is called and none of its own, so the extra entries read as
// production having nothing in them, which is the same answer an empty
// repository gives.

const infraWeb = `{"name":"shopfront","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`

func TestInfra_DraftsTheRootModuleAndNotTheModulesItCalls(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": infraWeb,
		"infra/main.tf": `
module "network" {
  source = "./network"
  cidr   = "10.0.0.0/16"
}

module "database" {
  source = "./database"
}
`,
		// Deliberately NOT under a directory called modules. The convention
		// has its own test, and if these sat under one there would be two
		// reasons to exclude them and no way to tell which one worked.
		"infra/network/main.tf":  `resource "aws_vpc" "this" {}`,
		"infra/database/main.tf": `resource "aws_db_instance" "this" {}`,
	}
	res := run(t, "shopfront", files)

	require.NotNil(t, res.Draft.Infrastructure, "a repository with Terraform in it drafts the section")
	require.Equal(t, schema.InfraTerraform, res.Draft.Infrastructure.Source)
	require.Equal(t, []string{"infra"}, res.Draft.Infrastructure.Paths)
	requireDraftValidates(t, res.Draft, files)
}

func TestInfra_DraftsEveryRootModuleWhenThereAreSeveral(t *testing.T) {
	t.Parallel()
	// The normal shape of a repository that splits infrastructure by concern.
	// All three are applied on their own and all three belong in the section,
	// sorted so two runs over one tree produce the same file.
	files := map[string]string{
		"package.json":            infraWeb,
		"infra/network/main.tf":   `resource "aws_vpc" "this" {}`,
		"infra/data/main.tf":      `resource "aws_db_instance" "this" {}`,
		"infra/app/main.tf":       `resource "aws_ecs_service" "this" {}`,
		"infra/app/variables.tf":  `variable "image" {}`,
		"infra/network/output.tf": `output "vpc_id" { value = "" }`,
	}
	res := run(t, "shopfront", files)
	require.Equal(t,
		[]string{"infra/app", "infra/data", "infra/network"},
		res.Draft.Infrastructure.Paths)
	requireDraftValidates(t, res.Draft, files)
}

func TestInfra_DraftsTheRepositoryRootAsTheCurrentDirectory(t *testing.T) {
	t.Parallel()
	// Terraform kept at the top of the repository. The empty string is not a
	// path a manifest can carry, so it has to be written as "." or the draft
	// is one the validator refuses and af init writes nothing at all.
	files := map[string]string{
		"package.json": infraWeb,
		"main.tf":      `resource "aws_s3_bucket" "this" {}`,
	}
	res := run(t, "shopfront", files)
	require.Equal(t, []string{"."}, res.Draft.Infrastructure.Paths)
	requireDraftValidates(t, res.Draft, files)
}

func TestInfra_LeavesTheSectionOutWhenTheRepositoryHasNoTerraform(t *testing.T) {
	t.Parallel()
	// Nil rather than an empty section, and the difference is load bearing
	// twice: an empty section names no path and the validator refuses it, and
	// the fidelity report reads the absence as "this application declared no
	// infrastructure" rather than as "it declared some and none was found".
	res := run(t, "shopfront", map[string]string{
		"package.json": infraWeb,
		"Dockerfile":   "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	})
	require.Nil(t, res.Draft.Infrastructure)
}

func TestInfra_LeavesOutAModuleLibraryAndSaysWhy(t *testing.T) {
	t.Parallel()
	// A repository that publishes modules and applies nothing. Every Terraform
	// directory is called by another, so there is no root module to name, and
	// a silent absence would read exactly like a repository with no Terraform
	// at all. The note is the difference.
	files := map[string]string{
		"package.json": infraWeb,
		"modules/network/main.tf": `
module "subnets" {
  source = "./subnets"
}
`,
		"modules/network/subnets/main.tf": `resource "aws_subnet" "this" {}`,
	}
	res := run(t, "shopfront", files)
	require.Nil(t, res.Draft.Infrastructure)

	var notes []string
	for _, f := range detect.OfKind(res.Findings, detect.KindNote) {
		if f.Subject == "infrastructure" {
			notes = append(notes, f.Detail)
		}
	}
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "is called as a module by another")
}

func TestInfra_DraftsARootModuleThatCallsPublishedModules(t *testing.T) {
	t.Parallel()
	// Most root modules in the world call something from the registry or from
	// Git. What this holds is that calling a module never excludes the CALLER:
	// a scan that marked the calling directory rather than the callee would
	// empty the section on the most ordinary repository there is.
	//
	// It deliberately does not claim to prove that a registry address is told
	// apart from a local path. It cannot, and saying so here is the point: a
	// callee that does not exist excludes no directory, so the end to end
	// answer is the same either way. That distinction is made inside
	// localModuleTargets and is asserted there, in infra_internal_test.go,
	// which is the only place it is visible.
	//
	// THE LOCAL CALL BELOW IS LOAD BEARING and was not here at first. With
	// only the registry and Git calls, localModuleTargets returns nothing at
	// all, so the line that records a callee never executes and the mutation
	// aimed at it, marking the CALLER rather than the callee, could not reach
	// the behaviour: the cell came back SURVIVED against a test that reads as
	// though it covered exactly that. A fixture whose every module call is
	// non local cannot hold a rule about what calling a module does.
	files := map[string]string{
		"package.json": infraWeb,
		"infra/main.tf": `
module "consul" {
  source  = "hashicorp/consul/aws"
  version = "0.11.0"
}

module "vpc" {
  source = "git::https://example.com/vpc.git?ref=v1"
}

module "network" {
  source = "./network"
}
`,
		"infra/network/main.tf": `resource "aws_vpc" "this" {}`,
	}
	res := run(t, "shopfront", files)
	require.NotNil(t, res.Draft.Infrastructure)
	require.Equal(t, []string{"infra"}, res.Draft.Infrastructure.Paths)
}

func TestInfra_DoesNotReadASourceArgumentOutsideAModuleBlock(t *testing.T) {
	t.Parallel()
	// A source argument that belongs to something else. The required_providers
	// block carries one, and a scan that read every source line in the file
	// would be reading an argument whose meaning it does not know. Here it is
	// written with a relative path on purpose, which is the case that would
	// silently remove a real root module from the draft.
	files := map[string]string{
		"package.json": infraWeb,
		"infra/stack/main.tf": `
terraform {
  required_providers {
    local = {
      source = "./not-a-module"
    }
  }
}
`,
		"infra/not-a-module/main.tf": `resource "null_resource" "this" {}`,
	}
	res := run(t, "shopfront", files)
	require.Contains(t, res.Draft.Infrastructure.Paths, "infra/stack",
		"the stack calls no module, so it is a root module")
}

func TestInfra_LeavesOutADirectoryUnderModulesEvenWhenNothingCallsIt(t *testing.T) {
	t.Parallel()
	// A module published for another repository to consume is called by
	// nothing HERE, so the call graph alone reports it as a root module. The
	// convention is the only thing that can answer it, and a repository that
	// ships modules beside its own stack is the case that needs it.
	files := map[string]string{
		"package.json":            infraWeb,
		"infra/main.tf":           `resource "aws_ecs_service" "this" {}`,
		"modules/logging/main.tf": `resource "aws_cloudwatch_log_group" "this" {}`,
	}
	res := run(t, "shopfront", files)
	require.Equal(t, []string{"infra"}, res.Draft.Infrastructure.Paths)
}

func TestInfra_NeverDraftsAWorkspaceOrAVariableFile(t *testing.T) {
	t.Parallel()
	// The refusal to guess, asserted so that nobody adds the guess later
	// believing it is an improvement. Three tfvars files sit beside the stack
	// and not one of them is evidence about PRODUCTION: a name is not a fact,
	// and this is the one manifest section nothing downstream can check, so a
	// wrong entry here silently compares a copy of production against staging
	// and reports a number about the wrong thing.
	files := map[string]string{
		"package.json":            infraWeb,
		"infra/main.tf":           `resource "aws_ecs_service" "this" {}`,
		"infra/production.tfvars": `image = "shopfront:1"`,
		"infra/staging.tfvars":    `image = "shopfront:2"`,
		"infra/dev.tfvars":        `image = "shopfront:3"`,
	}
	res := run(t, "shopfront", files)
	require.Equal(t, []string{"infra"}, res.Draft.Infrastructure.Paths)
	require.Empty(t, res.Draft.Infrastructure.Workspace)
	require.Empty(t, res.Draft.Infrastructure.VarFiles)
}

func TestInfra_FindingsCarryASourceTheSchemaKnows(t *testing.T) {
	t.Parallel()
	// The detect package writes the word "terraform" without importing the
	// schema's constant, so that locating infrastructure does not depend on
	// the manifest's vocabulary. This is what keeps the two in step: the value
	// the analyzer reports has to be one the manifest accepts, or af init
	// drafts a section its own validator refuses.
	res := run(t, "shopfront", map[string]string{
		"package.json":  infraWeb,
		"infra/main.tf": `resource "aws_s3_bucket" "this" {}`,
	})
	found := detect.OfKind(res.Findings, detect.KindInfra)
	require.NotEmpty(t, found)
	for _, f := range found {
		require.Equal(t, string(schema.InfraTerraform), f.Value)
		require.NotEmpty(t, f.Evidence, "a finding names the file it was read from")
		require.Equal(t, "infrastructure", f.Analyzer)
	}
}

func TestInfra_RefusesToDraftMoreRootModulesThanAManifestMayName(t *testing.T) {
	t.Parallel()
	// Above the bound the section is left out and a note says why. Drafting
	// fifty one paths would not produce a long manifest: af init validates the
	// draft before it writes it, so the fifty first entry makes the command
	// refuse to write anything at all on a repository it had read correctly.
	files := map[string]string{"package.json": infraWeb}
	for i := 0; i <= detect.MaxInfraRoots; i++ {
		files[stackPath(i)] = `resource "null_resource" "this" {}`
	}
	res := run(t, "shopfront", files)
	require.Nil(t, res.Draft.Infrastructure)

	var notes []string
	for _, f := range detect.OfKind(res.Findings, detect.KindNote) {
		if f.Subject == "infrastructure" {
			notes = append(notes, f.Detail)
		}
	}
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "51 Terraform root modules and a manifest may name 50")
}

// stackPath names the nth stack directory, zero padded so the sort order is
// the obvious one.
func stackPath(n int) string {
	return "infra/stack" + pad2(n) + "/main.tf"
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

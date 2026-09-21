package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The infrastructure section, which says where the infrastructure as code
// lives.
//
// WHY THESE REFUSALS EXIST AT ALL, since every other path in a manifest fails
// loudly on its own. A service path that is wrong fails the build. A
// Dockerfile that is wrong fails the build. An infrastructure path that is
// wrong fails NOTHING: no container is started from it and no image is built
// from it, so the only consequence is a comparison against a directory that is
// not there. That reads as "this copy reproduces none of your infrastructure",
// which is the same sentence an honest empty result produces, and the reader
// has no way to tell a wrong path from a real gap. So the mistake is caught
// here, at the line the person wrote, or it is never caught at all.
//
// The accept arms below are not decoration either. A refusal that fires on
// everything is as dead as one that fires on nothing, so every rule is proved
// with the case it must refuse AND the neighbouring case it must allow.

const infraBase = `
version: 1
name: shop
services:
  - name: web
    port: 3000
infrastructure:
`

// parseIn parses a manifest against a real repository root, which is what
// makes the existence checks measurable: manifest.Parse with an empty root
// treats every path as present, deliberately, so a test that used the package
// helpers would prove nothing about them.
func parseIn(t *testing.T, root, body string) (*schema.Manifest, error) {
	t.Helper()
	return manifest.Parse([]byte(body), "antifailure.yaml", root)
}

// repoWith builds a temporary repository holding the entries named, where a
// name ending in a slash is a directory and anything else is a file.
func repoWith(t *testing.T, entries ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, e := range entries {
		full := filepath.Join(root, filepath.FromSlash(e))
		if len(e) > 0 && e[len(e)-1] == '/' {
			require.NoError(t, os.MkdirAll(full, 0o750))
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte("# a file\n"), 0o600))
	}
	return root
}

func infraProblems(t *testing.T, root, body string) string {
	t.Helper()
	_, err := parseIn(t, root, body)
	require.Error(t, err, "expected this manifest to be rejected:\n%s", body)
	return messages(problems(t, err))
}

func TestParse_AcceptsAnInfrastructureSection(t *testing.T) {
	t.Parallel()
	// The liveness arm for everything below. One root module that is there,
	// a workspace, a variable file that is there, and all four fields land on
	// the manifest as written rather than being normalized into something
	// else.
	root := repoWith(t, "infra/terraform/", "infra/terraform/production.tfvars")
	m, err := parseIn(t, root, infraBase+`  source: terraform
  paths:
    - infra/terraform
  workspace: production
  var_files:
    - infra/terraform/production.tfvars
`)
	require.NoError(t, err)
	require.NotNil(t, m.Infrastructure)
	require.Equal(t, schema.InfraTerraform, m.Infrastructure.Source)
	require.Equal(t, []string{"infra/terraform"}, m.Infrastructure.Paths)
	require.Equal(t, "production", m.Infrastructure.Workspace)
	require.Equal(t, []string{"infra/terraform/production.tfvars"}, m.Infrastructure.VarFiles)
}

func TestParse_LeavesTheInfrastructureSectionNilWhenItIsNotWritten(t *testing.T) {
	t.Parallel()
	// A pointer, and nothing fills it in. Which is what lets the fidelity
	// report tell "this application declared no infrastructure" apart from
	// "it declared some and none of it was reproduced", and those are
	// different facts about a copy.
	m := mustParse(t, minimal)
	require.Nil(t, m.Infrastructure)
}

func TestParse_AcceptsSeveralRootModules(t *testing.T) {
	t.Parallel()
	// The normal shape for an application whose infrastructure is split by
	// concern, and the neighbour of the two refusals below: several paths are
	// fine, and it is only a workspace or a variable file BESIDE them that
	// has nowhere to belong.
	root := repoWith(t, "infra/network/", "infra/data/", "infra/app/")
	m, err := parseIn(t, root, infraBase+`  source: terraform
  paths:
    - infra/network
    - infra/data
    - infra/app
`)
	require.NoError(t, err)
	require.Len(t, m.Infrastructure.Paths, 3)
}

func TestParse_RefusesAnInfrastructureSourceItCannotRead(t *testing.T) {
	t.Parallel()
	// The whole reason the vocabulary is closed. A manifest naming a tool
	// nothing here reads would look configured and behave as though the
	// section were absent, and the reader would be told their infrastructure
	// was reproduced by an engine that never opened it.
	root := repoWith(t, "infra/")
	msg := infraProblems(t, root, infraBase+`  source: pulumi
  paths:
    - infra
`)
	require.Contains(t, msg, `infrastructure.source: "pulumi" is not an infrastructure source this engine can read.`)
	require.Contains(t, msg, "it covers OpenTofu")
}

func TestParse_RefusesAnInfrastructureSourceThatIsEmpty(t *testing.T) {
	t.Parallel()
	// Written and blank, which is a different mistake from omitted: the
	// schema's required keyword answers the omission at the section, and this
	// answers the key that is present and says nothing.
	root := repoWith(t, "infra/")
	msg := infraProblems(t, root, infraBase+`  source: ""
  paths:
    - infra
`)
	require.Contains(t, msg,
		"infrastructure.source: The infrastructure section does not say what declares the infrastructure.")
}

func TestParse_RefusesAnInfrastructureSectionNamingNoRootModule(t *testing.T) {
	t.Parallel()
	// An empty list is a section that says where nothing is. Refused here
	// rather than left to the schema's minItems, because this sentence names
	// what the list is FOR and the generic one names a number.
	root := repoWith(t, "infra/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths: []
`)
	require.Contains(t, msg,
		"infrastructure.paths: The infrastructure section names no root module, so it says where nothing is.")
}

func TestParse_RefusesAnAbsoluteRootModulePath(t *testing.T) {
	t.Parallel()
	// The machine that reads this is not the machine it was written on. An
	// absolute path is read relative to the repository root anyway, so it
	// silently becomes a path with an empty first segment and lands nowhere.
	root := repoWith(t, "infra/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - /Users/someone/infra
`)
	require.Contains(t, msg,
		`infrastructure.paths[0]: The root module "/Users/someone/infra" is an absolute path.`)
}

func TestParse_RefusesARootModulePathThatLeavesTheRepository(t *testing.T) {
	t.Parallel()
	// A relative path that climbs out reads a directory nobody reviewing this
	// repository can see, on a machine that may not have it.
	root := repoWith(t, "infra/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - ../shared-infra
`)
	require.Contains(t, msg,
		`infrastructure.paths[0]: The root module "../shared-infra" leaves the repository.`)
}

func TestParse_AcceptsADirectoryWhoseNameBeginsWithTwoDots(t *testing.T) {
	t.Parallel()
	// The falsification arm for the rule above, and the reason it compares
	// SEGMENTS rather than searching for two characters. Kubernetes projects a
	// volume's current revision into a directory literally named "..data", and
	// a substring test would refuse a path that climbs nowhere.
	root := repoWith(t, "config/..data/")
	m, err := parseIn(t, root, infraBase+`  source: terraform
  paths:
    - config/..data
`)
	require.NoError(t, err)
	require.Equal(t, []string{"config/..data"}, m.Infrastructure.Paths)
}

func TestParse_RefusesARootModuleThatIsNotInTheRepository(t *testing.T) {
	t.Parallel()
	// The failure this section's refusals exist for. Nothing downstream can
	// report it: a directory that is not there produces no resources, and no
	// resources is indistinguishable from an application that declares none.
	root := repoWith(t, "infra/terraform/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - infra/terrafrom
`)
	require.Contains(t, msg,
		`infrastructure.paths[0]: The root module "infra/terrafrom" is not in this repository.`)
	require.Contains(t, msg, "read as production having nothing in it")
}

func TestParse_RefusesARootModuleThatIsAFile(t *testing.T) {
	t.Parallel()
	// Pointing at main.tf rather than at the directory holding it. It exists,
	// so an existence check alone says yes, and a reader handed a file where a
	// root module belongs reads one file out of a module that has several.
	root := repoWith(t, "infra/terraform/main.tf")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - infra/terraform/main.tf
`)
	require.Contains(t, msg,
		`infrastructure.paths[0]: The root module "infra/terraform/main.tf" is a file, and a root module is a directory.`)
}

func TestParse_RefusesTheSameRootModuleTwice(t *testing.T) {
	t.Parallel()
	// Reported at the list rather than at the second entry, so that the
	// schema's uniqueItems, which boundsPass enforces at that same path, stays
	// quiet and one mistake produces one sentence.
	root := repoWith(t, "infra/terraform/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - infra/terraform
    - infra/terraform
`)
	require.Contains(t, msg,
		`infrastructure.paths: The root module "infra/terraform" is named twice, at positions 0 and 1.`)
}

func TestParse_RefusesAVariableFileThatIsNotInTheRepository(t *testing.T) {
	t.Parallel()
	// The same argument as a root module that is not there, one field over. A
	// variable file is where production's own numbers live, so a missing one
	// is the difference between comparing against production and comparing
	// against a module's defaults.
	root := repoWith(t, "infra/terraform/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - infra/terraform
  var_files:
    - infra/terraform/prod.tfvars
`)
	require.Contains(t, msg,
		`infrastructure.var_files[0]: The variable file "infra/terraform/prod.tfvars" is not in this repository.`)
}

func TestParse_AcceptsAVariableFileThatIsAFile(t *testing.T) {
	t.Parallel()
	// The falsification arm for the directory rule: it is aimed at root
	// modules only, and a variable file is a file. A rule copied to both
	// lists would refuse every correct manifest that names one.
	root := repoWith(t, "infra/terraform/", "infra/terraform/production.tfvars")
	m, err := parseIn(t, root, infraBase+`  source: terraform
  paths:
    - infra/terraform
  var_files:
    - infra/terraform/production.tfvars
`)
	require.NoError(t, err)
	require.Equal(t, []string{"infra/terraform/production.tfvars"}, m.Infrastructure.VarFiles)
}

func TestParse_RefusesAWorkspaceBesideSeveralRootModules(t *testing.T) {
	t.Parallel()
	// A workspace is selected INSIDE one root module. Beside three there is no
	// way to say which, and an engine that picked the first would be deciding
	// something its user did not.
	root := repoWith(t, "infra/network/", "infra/data/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - infra/network
    - infra/data
  workspace: production
`)
	require.Contains(t, msg,
		`infrastructure.workspace: The workspace "production" is given beside 2 root modules, and a workspace belongs to one.`)
}

func TestParse_RefusesVariableFilesBesideSeveralRootModules(t *testing.T) {
	t.Parallel()
	// The same argument for the same reason: a variable file is an argument to
	// one root module.
	root := repoWith(t, "infra/network/", "infra/data/", "infra/production.tfvars")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths:
    - infra/network
    - infra/data
  var_files:
    - infra/production.tfvars
`)
	require.Contains(t, msg,
		"infrastructure.var_files: Variable files are given beside 2 root modules, and a variable file is passed to one.")
}

func TestParse_SaysNothingAboutAWorkspaceWhenNoPathIsNamed(t *testing.T) {
	t.Parallel()
	// The cross field rule fires above ONE path and not at zero, on purpose.
	// A section with an empty list has one mistake, and it is the list; being
	// told the workspace is also wrong would send the reader to fix a key that
	// is not the problem. It is also what keeps the all fields fixture in
	// schema_constraints_test.go buildable, which is the trap #315 fell into
	// from the other direction.
	root := repoWith(t, "infra/")
	msg := infraProblems(t, root, infraBase+`  source: terraform
  paths: []
  workspace: production
`)
	require.Contains(t, msg, "infrastructure.paths: The infrastructure section names no root module")
	require.NotContains(t, msg, "infrastructure.workspace:")
}

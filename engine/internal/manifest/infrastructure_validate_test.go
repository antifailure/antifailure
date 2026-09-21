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
// lives, one stack at a time.
//
// WHY THESE REFUSALS EXIST AT ALL, since every other path in a manifest fails
// loudly on its own. A service path that is wrong fails the build. A
// Dockerfile that is wrong fails the build. An infrastructure path that is
// wrong fails NOTHING: no container is started from it and no image is built
// from it, so the only consequence is a comparison against a directory that is
// not there, or against one that is there and holds nothing. Both read as
// "this copy reproduces none of your infrastructure", which is the same
// sentence an honest empty result produces, and the reader has no way to tell
// a wrong path from a real gap. So the mistakes are caught here, at the line
// the person wrote, or they are never caught at all.
//
// WHY EVERY KEY IS ON THE STACK. The first version of this section had source,
// workspace and var_files one level up, over a list of paths, and it needed a
// cross field refusal to hold it together: a workspace may only be given
// beside exactly one path, because a workspace is selected inside ONE root
// module. That rule was a symptom of the shape rather than a rule anybody
// could predict, and the first person it refused was the one with the most
// infrastructure. Per stack there is no rule left to write, and a repository
// that declares its cloud in one tool and its workloads in another, which is
// ordinary rather than exotic, can say so.
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

// terraformAt is a repository holding one readable Terraform stack at dir.
func terraformAt(t *testing.T, dir string, extra ...string) string {
	t.Helper()
	return repoWith(t, append([]string{dir + "/main.tf"}, extra...)...)
}

func infraProblems(t *testing.T, root, body string) string {
	t.Helper()
	_, err := parseIn(t, root, body)
	require.Error(t, err, "expected this manifest to be rejected:\n%s", body)
	return messages(problems(t, err))
}

func TestParse_AcceptsAnInfrastructureStack(t *testing.T) {
	t.Parallel()
	// The liveness arm for everything below. One stack that is there and holds
	// Terraform, a workspace, a variable file that is there, and all four keys
	// land on the manifest as written rather than being normalized into
	// something else.
	root := terraformAt(t, "infra/terraform", "infra/terraform/production.tfvars")
	m, err := parseIn(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform
      workspace: production
      var_files:
        - infra/terraform/production.tfvars
`)
	require.NoError(t, err)
	require.NotNil(t, m.Infrastructure)
	require.Len(t, m.Infrastructure.Stacks, 1)
	st := m.Infrastructure.Stacks[0]
	require.Equal(t, schema.InfraTerraform, st.Source)
	require.Equal(t, "infra/terraform", st.Path)
	require.Equal(t, "production", st.Workspace)
	require.Equal(t, []string{"infra/terraform/production.tfvars"}, st.VarFiles)
}

func TestParse_AcceptsAWorkspaceOnEveryOneOfSeveralStacks(t *testing.T) {
	t.Parallel()
	// The case the flat shape could not express and had to refuse. Three
	// stacks, each with its own workspace and its own variable file, every one
	// of them an argument to the directory it sits beside. There is no cross
	// field rule left for this to break, which is the whole point of the
	// shape, so it is asserted rather than left to be inferred from the
	// absence of a refusal.
	root := repoWith(t,
		"infra/network/main.tf", "infra/network/prod.tfvars",
		"infra/data/main.tf", "infra/data/prod.tfvars",
		"infra/app/main.tf", "infra/app/prod.tfvars")
	m, err := parseIn(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/network
      workspace: production
      var_files: [infra/network/prod.tfvars]
    - source: terraform
      path: infra/data
      workspace: production-data
      var_files: [infra/data/prod.tfvars]
    - source: terraform
      path: infra/app
      workspace: production
      var_files: [infra/app/prod.tfvars]
`)
	require.NoError(t, err)
	require.Len(t, m.Infrastructure.Stacks, 3)
	require.Equal(t, "production-data", m.Infrastructure.Stacks[1].Workspace)
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

func TestParse_RefusesAnInfrastructureSourceItCannotRead(t *testing.T) {
	t.Parallel()
	// The whole reason the vocabulary is closed, and the reason it carries
	// only the tools a reader exists for. A manifest naming a tool nothing
	// here reads would look configured and behave as though the stack were
	// absent, and the reader would be told their infrastructure was
	// reproduced by an engine that never opened it.
	root := terraformAt(t, "infra")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: pulumi
      path: infra
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].source: "pulumi" is not an infrastructure source this engine can read.`)
	require.Contains(t, msg, "it covers OpenTofu")
}

func TestParse_RefusesAnInfrastructureSourceThatIsEmpty(t *testing.T) {
	t.Parallel()
	// Written and blank, which is a different mistake from omitted: the
	// schema's required keyword answers the omission at the stack, and this
	// answers the key that is present and says nothing.
	root := terraformAt(t, "infra")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: ""
      path: infra
`)
	require.Contains(t, msg,
		"infrastructure.stacks[0].source: This stack does not say what declares it.")
}

func TestParse_RefusesAnInfrastructureSectionNamingNoStack(t *testing.T) {
	t.Parallel()
	// An empty list is a section that says where nothing is. Refused here
	// rather than left to the schema's minItems, because this sentence names
	// what the list is FOR and the generic one names a number.
	root := terraformAt(t, "infra")
	msg := infraProblems(t, root, infraBase+`  stacks: []
`)
	require.Contains(t, msg,
		"infrastructure.stacks: The infrastructure section names no stack, so it says where nothing is.")
}

func TestParse_RefusesAnAbsoluteStackPath(t *testing.T) {
	t.Parallel()
	// The machine that reads this is not the machine it was written on. An
	// absolute path is joined to the repository root anyway, so it silently
	// becomes a path with an empty first segment and lands nowhere.
	root := terraformAt(t, "infra")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: /Users/someone/infra
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].path: The stack path "/Users/someone/infra" is an absolute path.`)
}

func TestParse_RefusesAStackPathThatLeavesTheRepository(t *testing.T) {
	t.Parallel()
	// A relative path that climbs out reads a directory nobody reviewing this
	// repository can see, on a machine that may not have it.
	root := terraformAt(t, "infra")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: ../shared-infra
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].path: The stack path "../shared-infra" leaves the repository.`)
}

func TestParse_AcceptsADirectoryWhoseNameBeginsWithTwoDots(t *testing.T) {
	t.Parallel()
	// The falsification arm for the rule above, and the reason it compares
	// SEGMENTS rather than searching for two characters. Kubernetes projects a
	// volume's current revision into a directory literally named "..data", and
	// a substring test would refuse a path that climbs nowhere.
	root := terraformAt(t, "config/..data")
	m, err := parseIn(t, root, infraBase+`  stacks:
    - source: terraform
      path: config/..data
`)
	require.NoError(t, err)
	require.Equal(t, "config/..data", m.Infrastructure.Stacks[0].Path)
}

func TestParse_RefusesAStackPathThatIsOnlyWhitespace(t *testing.T) {
	t.Parallel()
	// The schema's minLength catches a path that is empty and cannot catch one
	// that is three spaces: it is three characters long and it names nothing.
	// Left alone it would be joined to the repository root and stat the root
	// itself, so a manifest naming a blank line would report every resource in
	// the tree as one stack's.
	root := terraformAt(t, "infra")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: "   "
`)
	require.Contains(t, msg, "infrastructure.stacks[0].path: This stack's path is empty.")
}

func TestParse_RefusesAStackPathThatIsNotInTheRepository(t *testing.T) {
	t.Parallel()
	// The failure this section's refusals exist for. Nothing downstream can
	// report it: a directory that is not there produces no resources, and no
	// resources is indistinguishable from an application that declares none.
	root := terraformAt(t, "infra/terraform")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terrafrom
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].path: The stack path "infra/terrafrom" is not in this repository.`)
	require.Contains(t, msg, "read as production having nothing in it")
}

func TestParse_RefusesAStackPathThatIsAFile(t *testing.T) {
	t.Parallel()
	// Pointing at main.tf rather than at the directory holding it. It exists,
	// so an existence check alone says yes, and a reader handed a file where a
	// stack belongs reads one file out of a directory that has several.
	root := terraformAt(t, "infra/terraform")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform/main.tf
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].path: The stack path "infra/terraform/main.tf" is a file, and a stack is a directory.`)
}

func TestParse_RefusesAStackDirectoryThatHoldsNoTerraform(t *testing.T) {
	t.Parallel()
	// The refusal a deep typo actually lands on. Mistyping the last segment of
	// infra/terraform/stacks/control-plane usually produces a path that EXISTS,
	// because the parent and its siblings are real directories, so every check
	// above this one says yes. "There is a directory here" and "there is a
	// stack here" are different facts and only this one asks the second.
	root := repoWith(t, "infra/terraform/stacks/control-plane/main.tf", "infra/terraform/stacks/")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform/stacks
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].path: The stack path "infra/terraform/stacks" holds no Terraform file.`)
	require.Contains(t, msg, "what a mistyped path one level out looks like")
}

func TestParse_AcceptsAStackDeclaredInJSON(t *testing.T) {
	t.Parallel()
	// The falsification arm for the rule above. Terraform reads .tf.json as
	// well as .tf, and it is what a generated configuration is written as. A
	// check that only knew .tf would refuse a stack Terraform itself applies
	// happily, which is worse than not checking: it refuses correct work.
	root := repoWith(t, "infra/generated/main.tf.json")
	m, err := parseIn(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/generated
`)
	require.NoError(t, err)
	require.Equal(t, "infra/generated", m.Infrastructure.Stacks[0].Path)
}

func TestParse_LooksOnlyAtTheStackDirectoryItselfForTerraform(t *testing.T) {
	t.Parallel()
	// One level and no deeper, deliberately. A root module's own files are in
	// its own directory; a .tf file three levels down belongs to a module it
	// calls. Accepting a directory because something nested under it has
	// Terraform in it would accept the repository root of every repository
	// that has any, which is the one path a person is most likely to type by
	// mistake.
	root := repoWith(t, "infra/terraform/stacks/control-plane/main.tf")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra
`)
	require.Contains(t, msg, `infrastructure.stacks[0].path: The stack path "infra" holds no Terraform file.`)
}

func TestParse_RefusesTwoStacksNamingTheSamePath(t *testing.T) {
	t.Parallel()
	// The schema's uniqueItems on the list catches two entries identical in
	// every key. It cannot catch two entries that name one directory and
	// differ in their workspace, which is the case worth refusing: reading one
	// place twice measures it twice and says nothing more, and whichever
	// workspace is applied to it would be a coin flip.
	root := terraformAt(t, "infra/terraform")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform
      workspace: production
    - source: terraform
      path: infra/terraform
      workspace: staging
`)
	require.Contains(t, msg,
		`infrastructure.stacks[1].path: The stack path "infra/terraform" is already named at infrastructure.stacks[0].`)
}

func TestParse_RefusesAVariableFileThatIsNotInTheRepository(t *testing.T) {
	t.Parallel()
	// The same argument as a stack that is not there, one field over. A
	// variable file is where production's own numbers live, so a missing one
	// is the difference between reading production's values and reading the
	// module's defaults, and the reader has no way to tell which it got.
	root := terraformAt(t, "infra/terraform")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform
      var_files:
        - infra/terraform/prod.tfvars
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].var_files[0]: The variable file "infra/terraform/prod.tfvars" is not in this repository.`)
}

func TestParse_AcceptsAVariableFileThatIsAFile(t *testing.T) {
	t.Parallel()
	// The falsification arm for the directory rule: it is aimed at stack paths
	// only, and a variable file is a file. A rule copied to both would refuse
	// every correct manifest that names one.
	root := terraformAt(t, "infra/terraform", "infra/terraform/production.tfvars")
	m, err := parseIn(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform
      var_files:
        - infra/terraform/production.tfvars
`)
	require.NoError(t, err)
	require.Equal(t, []string{"infra/terraform/production.tfvars"},
		m.Infrastructure.Stacks[0].VarFiles)
}

func TestParse_RefusesTheSameVariableFileTwiceInOneStack(t *testing.T) {
	t.Parallel()
	// Reported at the list rather than at the second entry, so that the
	// schema's uniqueItems, which boundsPass enforces at that same path, stays
	// quiet and one mistake produces one sentence.
	root := terraformAt(t, "infra/terraform", "infra/terraform/prod.tfvars")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform
      var_files:
        - infra/terraform/prod.tfvars
        - infra/terraform/prod.tfvars
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].var_files: The variable file "infra/terraform/prod.tfvars" is named twice, at positions 0 and 1.`)
}

func TestParse_RefusesAVariableFileThatLeavesTheRepository(t *testing.T) {
	t.Parallel()
	// The same reason a stack path may not climb out, and its own test because
	// the two lists are checked by different code. A variable file above the
	// repository is read from a machine that may not have it, and its absence
	// there is silent.
	root := terraformAt(t, "infra/terraform")
	msg := infraProblems(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/terraform
      var_files:
        - ../secrets/prod.tfvars
`)
	require.Contains(t, msg,
		`infrastructure.stacks[0].var_files[0]: The variable file "../secrets/prod.tfvars" leaves the repository.`)
}

func TestParse_AcceptsTheSameVariableFileInTwoDifferentStacks(t *testing.T) {
	t.Parallel()
	// The falsification arm for the duplicate rule, and the reason the seen
	// set is per stack rather than shared. One shared tfvars passed to two
	// stacks is ordinary, and a rule that counted across the list would refuse
	// it while claiming it was passing the same file twice, which it is not:
	// it is passing it once to each of two invocations.
	root := repoWith(t, "infra/a/main.tf", "infra/b/main.tf", "infra/shared.tfvars")
	m, err := parseIn(t, root, infraBase+`  stacks:
    - source: terraform
      path: infra/a
      var_files: [infra/shared.tfvars]
    - source: terraform
      path: infra/b
      var_files: [infra/shared.tfvars]
`)
	require.NoError(t, err)
	require.Equal(t, []string{"infra/shared.tfvars"}, m.Infrastructure.Stacks[0].VarFiles)
	require.Equal(t, []string{"infra/shared.tfvars"}, m.Infrastructure.Stacks[1].VarFiles)
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a fixture repository from the real files at the repository root,
// so every test starts from something that passes and breaks one thing.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join("..", "..")
	example, err := os.ReadFile(filepath.Join(repo, exampleFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{actionFile, reusableFile, exampleFile} {
		b, err := os.ReadFile(filepath.Join(repo, f))
		if err != nil {
			t.Fatal(err)
		}
		write(t, root, f, b)
	}
	for _, c := range copies {
		write(t, root, c, example)
	}
	return root
}

func write(t *testing.T, root, rel string, b []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func edit(t *testing.T, root, rel, old, new string) {
	t.Helper()
	p := filepath.Join(root, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q, so this test would break nothing", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expect(t *testing.T, root, fragment string) {
	t.Helper()
	problems, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		if strings.Contains(p, fragment) {
			return
		}
	}
	t.Fatalf("expected a problem containing %q, got %q", fragment, problems)
}

func TestTheRealTreePasses(t *testing.T) {
	problems, err := check(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("the repository's own surfaces disagree:\n%s", strings.Join(problems, "\n"))
	}
}

func TestAFixtureBuiltFromTheRealFilesPasses(t *testing.T) {
	problems, err := check(tree(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %q", problems)
	}
}

func TestAWithKeyTheActionDoesNotDeclareIsRefused(t *testing.T) {
	root := tree(t)
	edit(t, root, reusableFile, "dispatch: ${{ inputs.dispatch }}", "dispatched: ${{ inputs.dispatch }}")
	expect(t, root, "passes with.dispatched to the action")
}

func TestAReusableInputThatNeverReachesTheActionIsRefused(t *testing.T) {
	root := tree(t)
	edit(t, root, reusableFile, "version: ${{ inputs.version }}", "version: v1.0.0")
	expect(t, root, `accepts input "version" and never passes it`)
}

func TestAnInputTheActionReferencesButDoesNotDeclareIsRefused(t *testing.T) {
	root := tree(t)
	edit(t, root, actionFile, "AF_REPORT: ${{ inputs.report }}", "AF_REPORT: ${{ inputs.output }}")
	expect(t, root, "references inputs.output, which it does not declare")
}

func TestAStepOutputWithNoSuchStepIsRefused(t *testing.T) {
	root := tree(t)
	edit(t, root, actionFile, "${{ steps.plan.outputs.command }}", "${{ steps.plans.outputs.command }}")
	expect(t, root, "references steps.plans.outputs, and no step has that id")
}

func TestTheExampleMustPassOnlyDeclaredInputs(t *testing.T) {
	root := tree(t)
	edit(t, root, exampleFile, "control-plane: ${{ vars.AF_CONTROL_PLANE }}", "control_plane: ${{ vars.AF_CONTROL_PLANE }}")
	expect(t, root, "passes with.control_plane, which .github/workflows/check.yml does not declare")
}

func TestTheExampleMustInheritSecrets(t *testing.T) {
	root := tree(t)
	edit(t, root, exampleFile, "    secrets: inherit\n", "")
	expect(t, root, "does not set `secrets: inherit`")
}

func TestTheDispatchInputsAreTheOnesTheControlPlaneSends(t *testing.T) {
	root := tree(t)
	edit(t, root, exampleFile, "run_id: {", "run: {")
	expect(t, root, "workflow_dispatch inputs are [")
}

func TestTheTwoRefsMoveTogether(t *testing.T) {
	root := tree(t)
	edit(t, root, exampleFile, "check.yml@v1", "check.yml@v2")
	expect(t, root, "calls the reusable workflow at @v2 while")
}

func TestADriftedCopyIsRefused(t *testing.T) {
	root := tree(t)
	edit(t, root, copies[0], "name: Antifailure", "name: Antifailure check")
	expect(t, root, copies[0]+" differs from "+exampleFile)
}

func TestAMissingCopyIsRefused(t *testing.T) {
	root := tree(t)
	if err := os.Remove(filepath.Join(root, copies[1])); err != nil {
		t.Fatal(err)
	}
	expect(t, root, "every embedded copy of "+exampleFile+" must exist")
}

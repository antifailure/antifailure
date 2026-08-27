package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A check nobody has proved can fail is a check that passes everything the day
// it breaks. These build small pairs of files with a known answer.

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindsTheGatesInAWorkflow(t *testing.T) {
	got := collect(`
      - name: Errors
        run: go run ./tools/errcheck .
      - name: Test
        run: cd engine && go test ./... -race
      - name: Vet
        run: cd engine && go vet ./...
      - name: Format
        run: |
          unformatted=$(gofmt -l engine tools)
`)
	for _, want := range []string{"tool errcheck", "gotest ./...", "govet ./...", "gofmt"} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
}

func TestIgnoresShellPlumbing(t *testing.T) {
	// cd, echo and comments are not gates. Comparing them would make this
	// noisy enough that somebody deletes it, which is worse than not having it.
	got := collect(`
      - run: |
          cd engine
          echo "go test is not being run here"
          # go run ./tools/errcheck .
`)
	if len(got) != 0 {
		t.Fatalf("matched something that is not a gate: %v", keys(got))
	}
}

func TestATargetIsComparedThroughTheDirectoryItRanIn(t *testing.T) {
	// CI does `cd engine && go test ./...`; a recipe does `go test ./...` from
	// a line that already changed directory. Comparing the raw strings would
	// report drift that is not drift.
	ci := collect(`run: cd engine && go test ./... -race -timeout 30m`)
	just := collect("test-engine:\n    cd engine && go test ./... -race -timeout 30m")
	for g := range ci {
		if _, ok := just[g]; !ok {
			t.Errorf("%q was reported as missing when both sides run it", g)
		}
	}
}

func TestANewToolInCIIsReportedAsMissing(t *testing.T) {
	// The failure this exists for: somebody adds a check to the workflow and
	// the justfile never learns about it, so a green local run quietly stops
	// meaning what CONTRIBUTING says it means.
	ci := collect(`run: go run ./tools/newcheck .`)
	just := collect("errcheck:\n    go run ./tools/errcheck .")
	if _, ok := just["tool newcheck"]; ok {
		t.Fatal("the justfile appears to run a tool it does not")
	}
	if _, ok := ci["tool newcheck"]; !ok {
		t.Fatal("the new tool was not recognised in CI")
	}
}

func TestARecipeTheGateNeverCallsIsReported(t *testing.T) {
	// A gate the one command does not run is a gate nobody runs.
	uncalled := uncalledByGate(`
gate:
    run "errors" just errcheck

errcheck:
    go run ./tools/errcheck .

scanrepo:
    go run ./tools/scanrepo .
`)
	if len(uncalled) != 1 || uncalled[0] != "scanrepo" {
		t.Fatalf("expected [scanrepo], got %v", uncalled)
	}
}

func TestAConvenienceRecipeIsNotReported(t *testing.T) {
	// `just fmt` writes files and `just db` starts a container. Neither
	// belongs in a gate, and reporting them would teach people to ignore this.
	uncalled := uncalledByGate(`
gate:
    run "errors" just errcheck

errcheck:
    go run ./tools/errcheck .

fmt:
    gofmt -w engine tools

db:
    docker run -d --name af-cp-test postgres:17-alpine
`)
	if len(uncalled) != 0 {
		t.Fatalf("convenience recipes were reported as uncovered gates: %v", uncalled)
	}
}

func TestNoGateRecipeAtAllIsAFailure(t *testing.T) {
	uncalled := uncalledByGate("errcheck:\n    go run ./tools/errcheck .\n")
	if len(uncalled) == 0 {
		t.Fatal("a justfile with no gate recipe passed")
	}
	if !strings.Contains(uncalled[0], "no `gate` recipe") {
		t.Errorf("the message does not say what is wrong: %v", uncalled)
	}
}

func TestTheRealRepositoryAgrees(t *testing.T) {
	// The one that matters. Reads the actual files rather than a fixture, so
	// this fails the moment the workflow and the justfile drift.
	root := filepath.Join("..", "..")
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Skipf("no workflow to compare: %v", err)
	}
	just, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("CONTRIBUTING.md promises `just gate` and there is no justfile: %v", err)
	}

	ciGates := collect(string(ci))
	if len(ciGates) < 8 {
		t.Fatalf("only %d gates found in CI; the patterns have probably stopped matching", len(ciGates))
	}
	justGates := collect(string(just))
	for g := range ciGates {
		if _, ok := justGates[g]; !ok {
			t.Errorf("CI runs %q and the justfile does not", g)
		}
	}
	if u := uncalledByGate(string(just)); len(u) > 0 {
		t.Errorf("these recipes are gates that `just gate` never calls: %v", u)
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Workflow supply chain. These live here because gatecheck already reads the
// workflows, and because the tools module's tests run in CI.

func TestEveryActionIsPinnedToACommit(t *testing.T) {
	// A tag is mutable. `actions/checkout@v4` is a promise the publisher can
	// change after anybody reviewed it, so the thing that was reviewed and the
	// thing that runs are only the same by the publisher's continued goodwill.
	// A commit cannot be changed.
	uses := regexp.MustCompile(`uses:\s*(\S+)`)
	pinned := regexp.MustCompile(`^[\w.-]+/[\w.-]+(/[\w.-]+)*@[0-9a-f]{40}$`)

	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}

	checked := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range uses.FindAllStringSubmatch(string(body), -1) {
			ref := m[1]
			// A local action, referenced by path, has nothing to pin.
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue
			}
			checked++
			if !pinned.MatchString(ref) {
				t.Errorf("%s: %s is pinned to a tag, not a commit.\n"+
					"    Resolve it: gh api repos/<owner>/<repo>/git/ref/tags/<tag> --jq .object.sha\n"+
					"    Then write it as owner/repo@<sha> # <version>",
					filepath.Base(file), ref)
			}
		}
	}
	if checked < 5 {
		t.Fatalf("only %d actions were checked; the pattern has probably stopped matching", checked)
	}
}

func TestEveryPinnedActionSaysWhichVersionItIs(t *testing.T) {
	// A bare forty character hash is unreviewable and unupgradable: nobody can
	// tell whether it is a year out of date. The trailing comment is what makes
	// the pin readable by a person.
	line := regexp.MustCompile(`uses:\s*[\w.-]+/[\S]*@[0-9a-f]{40}(.*)$`)

	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(body), "\n") {
			m := line.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			if !strings.Contains(m[1], "#") {
				t.Errorf("%s: pinned with no version comment: %s", filepath.Base(file), strings.TrimSpace(l))
			}
		}
	}
}

func TestNoWorkflowGrantsWriteToEveryJob(t *testing.T) {
	// A workflow level `contents: write` gives it to every job in the file,
	// including the ones that only compile something. The release workflow had
	// exactly that: four build jobs holding a token that could create a
	// release.
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		for i, l := range lines {
			// Only the workflow level block, which is unindented.
			if l != "permissions:" {
				continue
			}
			for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], " "); j++ {
				if strings.Contains(lines[j], ": write") {
					t.Errorf("%s: grants %q to every job in the file. Move it to the "+
						"job that needs it.", filepath.Base(file), strings.TrimSpace(lines[j]))
				}
			}
		}
	}
}

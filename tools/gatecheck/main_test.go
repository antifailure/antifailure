package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// ciGates reads a workflow fragment the way main does: as steps of a file.
func ciGates(t *testing.T, yaml string) map[string]*entry {
	t.Helper()
	return scan(workflowBlocks("test.yml", yaml))
}

// gateOf is the gate behind a key, and a readable failure when it is not
// there. Reaching into the map directly makes a regression a nil dereference
// in the test rather than a sentence about what stopped being found.
func gateOf(t *testing.T, m map[string]*entry, key string) gate {
	t.Helper()
	e, ok := m[key]
	if !ok {
		t.Fatalf("%q was not found; the set is %v", key, keys(m))
	}
	return e.gate
}

// justGates reads a justfile fragment as recipes.
func justGates(t *testing.T, just string) map[string]*entry {
	t.Helper()
	return scan(recipeBlocks(justRecipes(just)))
}

func TestFindsTheGatesInAWorkflow(t *testing.T) {
	got := ciGates(t, `
jobs:
  one:
    steps:
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
	for _, want := range []string{
		"tool errcheck", "gotest ./... in engine", "govet ./... in engine", "gofmt in .",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
}

func TestIgnoresShellPlumbing(t *testing.T) {
	// cd, echo and comments are not gates. Comparing them would make this
	// noisy enough that somebody deletes it, which is worse than not having it.
	got := ciGates(t, `
jobs:
  one:
    steps:
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
	// CI does `cd engine && go test ./...`; a recipe does the same from a line
	// that already changed directory. Comparing the raw strings would report
	// drift that is not drift.
	ci := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: cd engine && go test ./... -race -timeout 30m\n")
	just := justGates(t, "test-engine:\n    cd engine && go test ./... -race -timeout 30m\n")
	for g := range ci {
		if _, ok := just[g]; !ok {
			t.Errorf("%q was reported as missing when both sides run it", g)
		}
	}
}

// The change this file exists to prove. Everything from here to
// TestNoGateRecipeAtAllIsAFailure is about the directory being part of a gate.

func TestTheSameCommandInTwoDirectoriesIsTwoGates(t *testing.T) {
	// The whole point. `npm test` in web and `npm test` in ee/web are two
	// different suites over two different trees, and a justfile that runs one
	// of them covers one of them. Keyed on the command alone, covering web
	// covered ee/web too, and the enterprise suite could stop running locally
	// with nothing saying so.
	ci := ciGates(t, `
jobs:
  one:
    steps:
      - name: Community
        run: npm test
        working-directory: web
      - name: Enterprise
        run: npm test
        working-directory: ee/web
`)
	just := justGates(t, `
gate:
    just test-web

test-web:
    npm --prefix web test
`)
	reach := reachableFromGate(justRecipes(`
gate:
    just test-web

test-web:
    npm --prefix web test
`))

	if how, _ := pairedWith(gateOf(t, ci, "npm test in web"), just, reach); how != pairedExactly {
		t.Errorf("the covered directory was reported as missing")
	}
	if how, _ := pairedWith(gateOf(t, ci, "npm test in ee/web"), just, reach); how != notPaired {
		t.Errorf("the uncovered directory paired against a recipe that runs a different tree")
	}
	// And the failure has to say what is wrong, not just that something is.
	g := gapFor(gateOf(t, ci, "npm test in ee/web"), "npm test in ee/web", just, reach)
	if !strings.Contains(g.reason, "only in npm test in web") {
		t.Errorf("the message does not name where the justfile does run it: %q", g.reason)
	}
}

func TestNpmRunIsAGateFamilyAndTheDirectoryTellsThemApart(t *testing.T) {
	// `npm run build` in www, in docs and in console is three gates, and it
	// used to be none: the npm pattern wanted `test` or `tsc`, so every
	// `npm run` in every workflow matched nothing at all. That is how
	// `npm run check:seo` ran on every pull request with nothing in the
	// justfile running it.
	got := ciGates(t, `
jobs:
  www:
    steps:
      - name: Build the marketing site
        run: npm run build
        working-directory: www
      - name: The crawl surfaces the site claims to have
        run: npm run check:seo
        working-directory: www
      - name: Build the documentation site
        run: npm run build
        working-directory: docs
      - name: Build the console
        run: npm run build
        working-directory: console
`)
	for _, want := range []string{
		"npm run build in www", "npm run build in docs", "npm run build in console",
		"npm run check:seo in www",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
	if len(got) != 4 {
		t.Errorf("three builds in three directories and one check should be four gates, got %v", keys(got))
	}
}

func TestNpmTestAndNpmRunTestAreOneGate(t *testing.T) {
	// `npm test` IS `npm run test`. Two keys for one command would report a
	// gap the day CI and the justfile happened to spell it differently, which
	// is drift that is not drift.
	long := ciGates(t, "jobs:\n  a:\n    steps:\n      - run: npm run test\n        working-directory: web\n")
	short := justGates(t, "test-web:\n    npm --prefix web test\n")
	if _, ok := long["npm test in web"]; !ok {
		t.Fatalf("`npm run test` did not read as the same gate as `npm test`: %v", keys(long))
	}
	if _, ok := short["npm test in web"]; !ok {
		t.Fatalf("`npm --prefix web test` changed shape: %v", keys(short))
	}
}

func TestAScriptThatIsNotAGateIsExemptByNameRatherThanByPattern(t *testing.T) {
	// The rule the pattern must NOT have. `npm run seed` writes fixture rows
	// for a dogfood run and asserts nothing about the tree, so it does not
	// belong in `just gate`; a pattern shaped to skip it would also skip the
	// next `npm run` somebody adds to CI, silently. It is seen, and then
	// exempt by name with the reason recorded.
	got := ciGates(t, "jobs:\n  a:\n    steps:\n      - run: npm run seed --workspace @antifailure/db\n        working-directory: web\n")
	if _, ok := got["npm run seed in web"]; !ok {
		t.Fatalf("the pattern stopped seeing it, which is the failure mode: %v", keys(got))
	}
	reason, ok := exemptFromGate["npm run seed in web"]
	if !ok {
		t.Fatal("it is seen and not exempt, so `just gate` is being asked to seed a database")
	}
	// The reason has to say what the command is, not that it fails the check.
	if !strings.Contains(reason, "dogfood") || !strings.Contains(reason, "asserts nothing about the tree") {
		t.Errorf("the exemption does not say what the command is: %q", reason)
	}
}

func TestAStepDirectoryOverridesTheJobDirectory(t *testing.T) {
	got := ciGates(t, `
jobs:
  one:
    defaults:
      run:
        working-directory: engine
    steps:
      - name: Inherits the job
        run: go vet ./...
      - name: Names its own
        run: go vet ./...
        working-directory: tools
`)
	for _, want := range []string{"govet ./... in engine", "govet ./... in tools"} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
}

func TestADirectoryDoesNotLeakIntoTheNextStep(t *testing.T) {
	// A step boundary ends the association. Without that the second step here
	// would read as running in engine, and a gate would be paired against a
	// directory it never ran in, which is worse than not pairing it at all.
	got := ciGates(t, `
jobs:
  one:
    steps:
      - name: In engine
        run: go vet ./...
        working-directory: engine
      - name: At the root
        run: go vet ./...
`)
	if _, ok := got["govet ./... in ."]; !ok {
		t.Errorf("the second step inherited the first step's directory: %v", keys(got))
	}
}

func TestOneDirectoryCoversAWholeRunBlock(t *testing.T) {
	// Several commands under one `working-directory:`, with the key after the
	// `run:` it applies to. Reading forward only would give every one of them
	// the repository root.
	got := ciGates(t, `
jobs:
  one:
    steps:
      - name: Several
        run: |
          go vet ./...
          go test ./internal/cli
        working-directory: engine
`)
	for _, want := range []string{"govet ./... in engine", "gotest ./internal/cli in engine"} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
}

func TestABareCdCarriesAndASubshellDoesNot(t *testing.T) {
	// `cd engine` on a line of its own moves the shell for good. The same cd
	// inside `( ... )` moves nothing beyond its own line, and a `(` on a line
	// of its own opens that scope across several. Both shapes are in ci.yml
	// and in the justfile, and confusing them puts a gate in the wrong place.
	got := ciGates(t, `
jobs:
  one:
    steps:
      - name: Mixed
        run: |
          (cd tools && go vet ./...)
          cd engine
          go test ./internal/cli
          (
            cd docs
            echo building
          )
          go test ./internal/hud
`)
	for _, want := range []string{
		"govet ./... in tools",
		"gotest ./internal/cli in engine",
		"gotest ./internal/hud in engine",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
}

func TestAJustRecipeWithoutAShebangRunsEachLineInItsOwnShell(t *testing.T) {
	// just's own semantics, and getting it wrong is not cosmetic: fuzz-engine
	// starts both of its lines with `cd engine`, and carrying the first
	// forward read the second as running in engine/engine.
	got := justGates(t, `
fuzz-engine:
    cd engine && go test ./internal/manifest -run FuzzParse
    cd engine && go test ./internal/detect -run FuzzAnalyzers
`)
	for _, want := range []string{
		"gotest ./internal/manifest in engine", "gotest ./internal/detect in engine",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("did not find %q in %v", want, keys(got))
		}
	}
}

func TestAShebangRecipeIsOneShell(t *testing.T) {
	got := justGates(t, `
lint:
    #!/usr/bin/env bash
    cd engine
    go vet ./...
`)
	if _, ok := got["govet ./... in engine"]; !ok {
		t.Errorf("a cd in a shebang recipe did not carry to the next line: %v", keys(got))
	}
}

func TestADirectoryComputedAtRunTimePairsLooselyAndSaysSo(t *testing.T) {
	// `just typecheck` finds its tsconfig files in the tree rather than naming
	// them, so the directory it runs npx in does not exist until the recipe
	// runs. Refusing to pair that would report drift that is not there;
	// pretending it names one directory would be a lie. It pairs, and the
	// passing output says the directory was not compared.
	ci := ciGates(t, `
jobs:
  one:
    steps:
      - name: Typecheck
        run: npx tsc --noEmit
        working-directory: runner
`)
	src := "gate:\n    just typecheck\n\ntypecheck:\n    #!/usr/bin/env bash\n    npx --prefix \"$root\" tsc --noEmit -p \"$cfg\"\n"
	just := justGates(t, src)
	reach := reachableFromGate(justRecipes(src))
	how, where := pairedWith(gateOf(t, ci, "npm tsc in runner"), just, reach)
	if how != pairedByRuntimeDir {
		t.Fatalf("expected a loose pair, got %v (justfile has %v)", how, keys(just))
	}
	if !strings.Contains(where, "npm tsc in ?") {
		t.Errorf("the loose pair does not say what it paired against: %q", where)
	}
}

func TestAGateInARecipeTheGateNeverCallsIsNotCoverage(t *testing.T) {
	// The overclaim the old passing sentence made. `coverage-profile` runs the
	// engine suite and `just gate` does not call it, so a CI gate whose only
	// counterpart is in there is not reachable from the one command, however
	// present it looks in the file.
	ci := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: cd engine && go test ./...\n")
	src := `
gate:
    just errcheck

errcheck:
    go run ./tools/errcheck .

coverage-profile:
    cd engine && go test ./... -coverprofile=out
`
	just := justGates(t, src)
	reach := reachableFromGate(justRecipes(src))
	if how, _ := pairedWith(gateOf(t, ci, "gotest ./... in engine"), just, reach); how != notPaired {
		t.Fatal("a gate only a recipe outside `just gate` runs was counted as covered")
	}
	g := gapFor(gateOf(t, ci, "gotest ./... in engine"), "gotest ./... in engine", just, reach)
	if !strings.Contains(g.reason, "never calls") || !strings.Contains(g.reason, "coverage-profile") {
		t.Errorf("the message does not name the recipe holding it: %q", g.reason)
	}
}

func TestANewToolInCIIsReportedAsMissing(t *testing.T) {
	// The failure this exists for: somebody adds a check to the workflow and
	// the justfile never learns about it, so a green local run quietly stops
	// meaning what CONTRIBUTING says it means.
	ci := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: go run ./tools/newcheck .\n")
	src := "gate:\n    just errcheck\n\nerrcheck:\n    go run ./tools/errcheck .\n"
	just := justGates(t, src)
	reach := reachableFromGate(justRecipes(src))
	if _, ok := just["tool newcheck"]; ok {
		t.Fatal("the justfile appears to run a tool it does not")
	}
	if _, ok := ci["tool newcheck"]; !ok {
		t.Fatal("the new tool was not recognised in CI")
	}
	if how, _ := pairedWith(gateOf(t, ci, "tool newcheck"), just, reach); how != notPaired {
		t.Fatal("a tool nothing in the justfile runs was counted as covered")
	}
	g := gapFor(gateOf(t, ci, "tool newcheck"), "tool newcheck", just, reach)
	if !strings.Contains(g.reason, "nothing in the justfile runs this") {
		t.Errorf("the message does not say the justfile has no counterpart: %q", g.reason)
	}
}

func TestARecipeTheGateNeverCallsIsReported(t *testing.T) {
	// A gate the one command does not run is a gate nobody runs.
	uncalled := uncalled(`
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

func TestARecipeReachedThroughAnotherRecipeIsNotReported(t *testing.T) {
	// `gate` names its dependency in the header rather than calling it, and
	// `_generated` carries five gates. Reachability that only read `just X`
	// calls out of the gate body would report the whole chain as uncalled.
	uncalled := uncalled(`
gate: prep
    run "errors" just errcheck

prep: schemas

schemas:
    go run ./tools/schemadoc .

errcheck:
    go run ./tools/errcheck .
`)
	if len(uncalled) != 0 {
		t.Fatalf("recipes reached through another recipe were reported: %v", uncalled)
	}
}

func TestAPrivateRecipeIsRead(t *testing.T) {
	// `_generated` is a recipe, `just gate` calls it, and it carries five
	// gates. A recipe parser anchored on [a-z] would skip it, and those five
	// would be reported as things CI runs and the justfile does not, which is
	// a red build for a gate that is running.
	src := `
gate:
    just _generated

_generated:
    go run ./tools/errgen
`
	got := justGates(t, src)
	if _, ok := got["tool errgen"]; !ok {
		t.Fatalf("a recipe whose name starts with _ was not read: %v", keys(got))
	}
	if !reachableFromGate(justRecipes(src))["_generated"] {
		t.Error("`just gate` calls it and it was not counted as reachable")
	}
}

func TestOperatorInitializationIsNotRunByTheSourceGate(t *testing.T) {
	got := uncalled(`
gate:
    run "errors" just errcheck

errcheck:
    go run ./tools/errcheck .

operator-init environment:
    node deploy/cd/operator-init.mjs production
`)
	if len(got) != 0 {
		t.Fatalf("operator creation was required in the source gate: %v", got)
	}
}

func TestAConvenienceRecipeIsNotReported(t *testing.T) {
	// `just fmt` writes files and `just db` starts a container. Neither
	// belongs in a gate, and reporting them would teach people to ignore this.
	uncalled := uncalled(`
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
	u := uncalled("errcheck:\n    go run ./tools/errcheck .\n")
	if len(u) == 0 {
		t.Fatal("a justfile with no gate recipe passed")
	}
	if !strings.Contains(u[0], "no `gate` recipe") {
		t.Errorf("the message does not say what is wrong: %v", u)
	}
}

// uncalled is uncalledByGate over a justfile's text, which is how every caller
// of it outside main already thinks about it.
func uncalled(just string) []string {
	recipes := justRecipes(just)
	return uncalledByGate(recipes, reachableFromGate(recipes))
}

func TestAWorkflowThisStopsReadingIsNotSilent(t *testing.T) {
	// The failure mode a structured read introduces. Five other files carry
	// the count, so a sixth going quiet leaves a healthy looking number and no
	// gates from it at all. A file with `run:` steps that yields none of them
	// is a parse failure, not an empty workflow.
	const noJobsKey = "on:\n  pull_request:\nsteps:\n  - run: go run ./tools/errcheck .\n"
	if !hasRunStep(noJobsKey) {
		t.Fatal("a workflow with a run: step was not recognised as having one")
	}
	if got := workflowBlocks("broken.yml", noJobsKey); len(got) != 0 {
		t.Fatalf("expected the malformed workflow to yield nothing, got %v", got)
	}

	// And the shape it does read, so the guard is not simply always true.
	const real = "on:\n  pull_request:\njobs:\n  a:\n    steps:\n      - run: go run ./tools/errcheck .\n"
	if got := workflowBlocks("ci.yml", real); len(got) != 1 {
		t.Fatalf("a well formed workflow yielded %d blocks, want 1", len(got))
	}
}

func TestTheRealRepositoryAgrees(t *testing.T) {
	// The one that matters. Reads the actual files rather than a fixture, so
	// this fails the moment the workflow and the justfile drift.
	root := filepath.Join("..", "..")
	workflows, err := pullRequestWorkflows(filepath.Join(root, ".github", "workflows"))
	if err != nil || len(workflows.paths) == 0 {
		t.Skipf("no workflow to compare: %v", err)
	}
	just, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("CONTRIBUTING.md promises `just gate` and there is no justfile: %v", err)
	}

	ci := scan(workflows.blocks())
	if len(ci) < 8 {
		t.Fatalf("only %d gates found in CI; the patterns have probably stopped matching", len(ci))
	}
	recipes := justRecipes(string(just))
	reach := reachableFromGate(recipes)
	jg := scan(recipeBlocks(recipes))
	for key, e := range ci {
		if _, exempt := exemptFromGate[key]; exempt {
			continue
		}
		if how, _ := pairedWith(e.gate, jg, reach); how == notPaired {
			t.Errorf("CI runs %q and `just gate` does not: %s", key,
				gapFor(e.gate, key, jg, reach).reason)
		}
	}
	// Every exemption must still name something a workflow runs.
	for g := range exemptFromGate {
		if _, ok := ci[g]; !ok {
			t.Errorf("%q is exempt from `just gate` but no pull request workflow runs it, "+
				"so the exemption is describing a gate that is not there", g)
		}
	}
	if u := uncalledByGate(recipes, reach); len(u) > 0 {
		t.Errorf("these recipes are gates that `just gate` never calls: %v", u)
	}
}

func keys(m map[string]*entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
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

// Workflow discovery. gatecheck used to read ci.yml by name, which meant a
// second workflow could carry gates nothing compared against the justfile.

func writeWorkflows(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAWorkflowThatRunsOnPullRequestsIsRead(t *testing.T) {
	dir := writeWorkflows(t, map[string]string{
		"ci.yml":       "on:\n  push:\n    branches: [main]\n  pull_request:\njobs:\n  a:\n    steps:\n      - run: go test ./...\n",
		"security.yml": "on:\n  pull_request:\n  schedule:\n    - cron: \"0 7 * * *\"\njobs:\n  b:\n    steps:\n      - run: go run ./tools/vulncheck .\n",
	})

	set, err := pullRequestWorkflows(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.paths) != 2 {
		t.Fatalf("read %v, want both workflows", set.paths)
	}
	gates := scan(set.blocks())
	if _, ok := gates["tool vulncheck"]; !ok {
		t.Errorf("a gate in the second workflow must be seen, got %v", keys(gates))
	}
}

// A workflow that never runs against a branch is out of scope, and out of scope
// for a stated reason rather than because it was left off a list.
func TestAWorkflowThatDoesNotRunOnPullRequestsIsSkipped(t *testing.T) {
	dir := writeWorkflows(t, map[string]string{
		"ci.yml":      "on:\n  pull_request:\njobs:\n  a:\n    steps:\n      - run: go test ./...\n",
		"release.yml": "on:\n  push:\n    tags: ['v*']\njobs:\n  b:\n    steps:\n      - run: go run ./tools/notagate .\n",
	})

	set, err := pullRequestWorkflows(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.paths) != 1 || set.paths[0] != "ci.yml" {
		t.Fatalf("read %v, want only ci.yml", set.paths)
	}
	if _, ok := scan(set.blocks())["tool notagate"]; ok {
		t.Error("a tag-triggered workflow must not contribute gates; it runs long after the gate had its say")
	}
}

// The trigger has to be the workflow's own, not the word appearing anywhere.
func TestTheWordPullRequestInACommentIsNotATrigger(t *testing.T) {
	dir := writeWorkflows(t, map[string]string{
		"release.yml": "# not run on pull_request: on purpose\non:\n  push:\n    tags: ['v*']\njobs: {}\n",
	})

	set, err := pullRequestWorkflows(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.paths) != 0 {
		t.Errorf("read %v, want none", set.paths)
	}
}

// Every exemption has to carry a reason, for the same reason .govulncheck.yaml
// entries do: an exemption with no stated reason is a mute button.
func TestEveryExemptionStatesWhy(t *testing.T) {
	for gate, reason := range exemptFromGate {
		if len(strings.Fields(reason)) < 20 {
			t.Errorf("the exemption for %q is %d words, which is too short to be a reason "+
				"to skip a gate in the one command CONTRIBUTING promises", gate, len(strings.Fields(reason)))
		}
	}
}

// A workflow with a YAML error does not fail loudly. GitHub declines to run it
// and says so only on a page nobody opens, so the symptom is a check that
// quietly stops existing. Everything else in this file reads the workflows as
// text, which would not notice.
func TestEveryWorkflowIsValidYAML(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no workflows: %v", err)
	}

	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var parsed map[string]any
		if err := yaml.Unmarshal(body, &parsed); err != nil {
			t.Errorf("%s is not valid YAML, so GitHub will not run it: %v", name, err)
			continue
		}
		if len(parsed["jobs"].(map[string]any)) == 0 {
			t.Errorf("%s parses but declares no jobs", name)
		}
		seen++
	}
	if seen == 0 {
		t.Error("checked no workflows, which means this test has stopped looking in the right place")
	}
}

// No compiled binary is ever committed.
//
// This has happened twice. `go build ./tools/claimcheck` writes ./claimcheck
// into the current directory, `git add -A` picks it up, and a platform specific
// executable lands in a repository that ships Linux binaries. A 10MB
// engine/af-proxy arrived the same way and sat there for longer.
//
// It matters more than the disk space. Git keeps a blob for ever, so every
// rebuild-and-recommit adds another copy of something nobody can run; and a
// repository that has just spent a night pinning its supply chain has no
// business carrying an unexplained executable at its root, because the honest
// answer to "what is this and who built it" is that nobody knows.
//
// .gitignore is the convenience and this is the backstop, because .gitignore
// only helps for the names somebody thought of first.
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	root := filepath.Join("..", "..")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("no git here: %v", err)
	}

	files := strings.Split(string(out), "\x00")
	if len(files) < 10 {
		t.Fatalf("git listed %d files, so this check has stopped looking", len(files))
	}

	checked := 0
	for _, f := range files {
		if f == "" {
			continue
		}
		// Only files with no extension are plausible Go binaries; everything
		// with a suffix is read as itself and reading every file in the
		// repository to check would make this slow for no gain.
		if filepath.Ext(f) != "" {
			continue
		}
		full := filepath.Join(root, f)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() < 4 {
			continue
		}
		checked++

		fh, err := os.Open(full)
		if err != nil {
			continue
		}
		var magic [4]byte
		n, _ := fh.Read(magic[:])
		fh.Close()
		if n < 4 {
			continue
		}
		if kind := executableKind(magic); kind != "" {
			t.Errorf("%s is a committed %s binary, %d bytes. "+
				"It is almost certainly `go build` output that `git add -A` picked up. "+
				"Remove it with `git rm --cached %s` and add it to .gitignore.",
				f, kind, info.Size(), f)
		}
	}
	if checked == 0 {
		t.Error("inspected no extensionless files, which means this check is looking in the wrong place")
	}
}

// executableKind names the format a magic number belongs to, or empty for
// anything that is not an executable.
func executableKind(m [4]byte) string {
	switch {
	case m[0] == 0x7f && m[1] == 'E' && m[2] == 'L' && m[3] == 'F':
		return "ELF"
	case m[0] == 0xcf && m[1] == 0xfa && m[2] == 0xed && m[3] == 0xfe:
		return "Mach-O 64-bit"
	case m[0] == 0xce && m[1] == 0xfa && m[2] == 0xed && m[3] == 0xfe:
		return "Mach-O 32-bit"
	case m[0] == 0xca && m[1] == 0xfe && m[2] == 0xba && m[3] == 0xbe:
		return "Mach-O universal"
	case m[0] == 'M' && m[1] == 'Z':
		return "Windows PE"
	}
	return ""
}

// Every tool's command name is ignored, or has a reason not to be.
//
// The list in .gitignore named the tools that existed when somebody wrote it,
// eleven tools were added since, and nobody thought about that file while
// adding them. A 4.2MB `dogfood` was then committed by `git add -A` and caught
// by the test above, which is the third time a compiled binary has reached
// this repository the same way.
//
// So the interesting failure is not the binary, it is the list. A list
// maintained by remembering is wrong by default, and the two earlier
// occurrences did not change that because each was fixed by adding one line.
// This checks the list is complete instead, which is the difference between a
// gate and a habit.
//
// The backstop above still matters and this does not replace it: it only knows
// about names under tools/, and a binary can arrive from anywhere.
func TestEveryToolsBinaryNameIsIgnored(t *testing.T) {
	root := filepath.Join("..", "..")

	// `docs` is a tool and also the documentation site. `/docs` in .gitignore
	// would ignore the whole tree, which is the collision the file's own
	// comment warns about, so it is exempt here and the backstop above is what
	// covers it. A name is exempt only with a reason, so that an exemption is
	// a decision somebody reads rather than a hole somebody widens.
	exempt := map[string]string{
		"docs": "also the documentation site at the repository root, so ignoring " +
			"the name would ignore the tree",
	}

	entries, err := os.ReadDir(filepath.Join(root, "tools"))
	if err != nil {
		t.Skipf("no tools directory here: %v", err)
	}

	// Whether git can answer at all, asked once and separately.
	//
	// Without this, a checkout git cannot read reports every tool as
	// unignored, because `check-ignore` exits non-zero both for "this is not
	// ignored" and for "I could not look". That is the same mistake the
	// insights report was fixed for: could not look and looked and found
	// nothing are different answers, and a check that conflates them fails
	// loudly for a reason that has nothing to do with what it checks.
	if err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		t.Skipf("git cannot read this checkout, so it cannot be asked what it ignores: %v", err)
	}

	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if why, ok := exempt[name]; ok {
			t.Logf("%s is exempt: %s", name, why)
			continue
		}
		checked++

		// git itself, rather than a parse of .gitignore, because the question
		// is what git would do with a file of that name and the answer depends
		// on rule order, negations and anchoring that a reimplementation gets
		// subtly wrong.
		cmd := exec.Command("git", "-C", root, "check-ignore", "-q", name)
		if err := cmd.Run(); err != nil {
			t.Errorf("`go build ./tools/%s` writes ./%s and nothing ignores it, so the "+
				"next `git add -A` commits a platform specific executable. Add /%s to "+
				".gitignore, or add it to the exempt map above with the reason it "+
				"cannot be ignored.", name, name, name)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d tools were checked, so this has stopped looking", checked)
	}
}

// Every job says where it runs.
//
// A job with no `runs-on` is not a job GitHub reports as failing. The whole
// workflow file is refused before a single job is created, and what the API
// returns is a run with zero jobs, `created_at` equal to `updated_at`, and the
// file's path where its name should be. Nothing appears in the pull request's
// checks, because the check never existed. A red check argues with you; this
// one leaves.
//
// It happened to dogfood.yml. A commit rewrote
//
//	runs-on: ubuntu-latest
//	timeout-minutes: 45
//
// into a longer comment and `timeout-minutes: 75`, and dropped the `runs-on`
// line with the one it meant to replace. The two pushes after it produced runs
// that started nothing, while the pull request still showed the older run's
// comment, so the branch looked like it had a pipeline and did not.
//
// TestEveryWorkflowIsValidYAML above cannot catch it: the file is valid YAML
// and declares its jobs. It is the Actions schema that is violated, and this is
// the cheapest useful piece of that schema to check.
func TestEveryJobSaysWhereItRuns(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if len(files) == 0 {
		t.Fatal("found no workflows, which means this check is looking in the wrong place")
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobsWithNoRunner(t, body) {
			t.Errorf("%s: job %q declares neither runs-on nor uses, "+
				"so GitHub refuses the whole file and the workflow reports nothing at all",
				filepath.Base(file), job)
		}
	}
}

func TestAJobMissingRunsOnIsReported(t *testing.T) {
	// The positive control is the second job: a file where every job is wrong
	// would also pass a check that had stopped looking.
	got := jobsWithNoRunner(t, []byte(`
name: example
on: push
jobs:
  forgot:
    timeout-minutes: 75
    steps:
      - run: echo hello
  remembered:
    runs-on: ubuntu-latest
    steps:
      - run: echo hello
  delegated:
    uses: ./.github/workflows/reusable.yml
`))
	want := []string{"forgot"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// jobsWithNoRunner names the jobs in a workflow that say nowhere to run.
//
// A job that calls a reusable workflow carries `uses` and must NOT carry
// `runs-on`: the called workflow decides. Treating that as a fault would make
// this gate refuse a correct file, which is the way a gate gets deleted.
func jobsWithNoRunner(t *testing.T, body []byte) []string {
	t.Helper()
	var parsed struct {
		Jobs map[string]struct {
			RunsOn any    `yaml:"runs-on"`
			Uses   string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("not valid YAML: %v", err)
	}
	var missing []string
	for name, job := range parsed.Jobs {
		if job.Uses != "" || job.RunsOn != nil {
			continue
		}
		missing = append(missing, name)
	}
	// Sorted, because a map is walked in a different order every run and an
	// error message that reorders itself reads as a different failure.
	sort.Strings(missing)
	return missing
}

// Nothing under www/lib is imported by nothing.
//
// The marketing site kept a whole generation of content modules after the
// pages moved to components/pages: lib/company-content.tsx (19,879 bytes),
// lib/solutions-content.tsx (10,347) and lib/marketing-content.tsx (40,244).
// Seventy kilobytes with no importer between them, shipped in every deploy.
//
// Dead is the smaller half of the problem. lib/lastmod.ts named two of them as
// the source of a route's date, so the home page and every /solutions page in
// the sitemap took their lastmod from files nothing renders, and a commit
// touching only dead content would have moved dates a reader is told mean the
// page changed. company-content.tsx also carried `related` links to /company,
// /security, /open-source and /design-partners, all four of which now answer
// 404, waiting for somebody to import it again.
//
// Neither the compiler nor Biome reports this: every file type checks, and an
// export with no consumer is legal. So it needs a gate, and it goes here with
// the other checks on the shape of the tree rather than in check-seo.mjs,
// which asserts against a build that has to happen first.
func TestEveryWwwLibModuleIsImported(t *testing.T) {
	dir := filepath.Join("..", "..", "www", "lib")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no www/lib: %v", err)
	}

	// Every source file on the site, read once. The site is small enough that
	// this is cheaper than being clever about which directories can import.
	var corpus []string
	root := filepath.Join("..", "..", "www")
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".next" || d.Name() == "out" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".ts", ".tsx", ".mjs", ".js":
		default:
			return nil
		}
		body, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		corpus = append(corpus, p+"\x00"+string(body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus) == 0 {
		t.Fatal("read no www source files, which means this check is looking in the wrong place")
	}

	// The third spelling, which cost a branch a red build. A module inside
	// lib/ imports a sibling as "./bots", not "@/lib/bots" and not
	// "../lib/bots", so a module used only by another module in lib/ read as
	// dead here while being on every page.
	//
	// Reachability rather than a third needle, because "./x" alone would let
	// two dead modules in lib/ that import each other keep one another alive,
	// which is the loophole a name-matching version of this would have. A lib
	// module is alive when something OUTSIDE lib/ names it, or when a lib
	// module that is itself alive names it. Grown to a fixpoint, so a chain of
	// any length works and a cycle with no way in stays dead.
	stems := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".tsx")) {
			continue
		}
		stems[strings.TrimSuffix(strings.TrimSuffix(name, ".tsx"), ".ts")] = name
	}

	alive := liveLibModules(corpus, dir, stems)

	checked := 0
	for stem, name := range stems {
		checked++
		if alive[stem] {
			continue
		}
		t.Errorf("www/lib/%s has no importer anywhere under www/. "+
			"An export nothing consumes type checks, lints clean and ships in every deploy; "+
			"delete it, or wire it to the thing that was meant to use it.", name)
	}
	if checked == 0 {
		t.Error("inspected no modules in www/lib, which means this check is looking in the wrong place")
	}
}

// importedBy reports whether any file other than the module itself names it in
// an import specifier.
// Which modules in www/lib are reachable.
//
// Seeded from everything outside lib/, then grown through sibling imports to a
// fixpoint. Its own function so the case below can drive it against a corpus
// written by hand: a check that cannot be shown saying no about a case it was
// widened for is a check nobody can trust afterwards.
func liveLibModules(corpus []string, dir string, stems map[string]string) map[string]bool {
	alive := map[string]bool{}
	for stem := range stems {
		if importedByOutside(corpus, dir, filepath.Join(dir, stems[stem]), stem) {
			alive[stem] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for stem := range stems {
			if alive[stem] {
				continue
			}
			for live := range alive {
				if namesModule(fileIn(corpus, filepath.Join(dir, stems[live])), stem) {
					alive[stem] = true
					grew = true
					break
				}
			}
		}
	}
	return alive
}

// Whether anything OUTSIDE www/lib names this module, by either spelling the
// rest of the site uses: the "@/lib/x" alias and a relative "../lib/x".
// Matching on "lib/<stem>" covers each, and the quote after it stops "lib/nav"
// from being answered by "lib/navbar".
func importedByOutside(corpus []string, dir, self, stem string) bool {
	for _, entry := range corpus {
		path, body, _ := strings.Cut(entry, "\x00")
		if path == self || filepath.Dir(path) == dir {
			continue
		}
		if closed(body, "lib/"+stem) {
			return true
		}
	}
	return false
}

// Whether one lib module names another, by the sibling spelling or by the
// alias. A file inside lib/ can write either.
func namesModule(body, stem string) bool {
	return closed(body, "./"+stem) || closed(body, "lib/"+stem)
}

// The needle followed by a closing quote, so a prefix cannot answer for a
// longer name.
func closed(body, needle string) bool {
	for _, closer := range []string{`"`, `'`, "`"} {
		if strings.Contains(body, needle+closer) {
			return true
		}
	}
	return false
}

func fileIn(corpus []string, want string) string {
	for _, entry := range corpus {
		path, body, _ := strings.Cut(entry, "\x00")
		if path == want {
			return body
		}
	}
	return ""
}

// The reachability above, against a corpus small enough to read.
//
// THE CASE THAT WIDENED IT: a module in lib/ imported only by a sibling, as
// "./bots". That is a third spelling, neither "@/lib/x" nor "../lib/x", and it
// read as dead while being on every page.
//
// THE CASE THAT STOPS THE WIDENING GOING TOO FAR: two modules in lib/ that
// import each other and nothing else. A version of this that simply added
// "./x" as a third needle would call both of them alive, which is worse than
// the false positive it fixed, because a check that cannot report the dead
// code it was built for is a check that has quietly stopped running.
func TestLibReachabilityCountsSiblingsButNotDeadCycles(t *testing.T) {
	dir := filepath.Join("www", "lib")
	file := func(p, body string) string { return p + "\x00" + body }

	corpus := []string{
		file(filepath.Join("www", "app", "page.tsx"), `import { x } from "@/lib/entry"`),
		file(filepath.Join(dir, "entry.ts"), `import { s } from "./sibling"`),
		file(filepath.Join(dir, "sibling.ts"), `import { d } from "./deeper"`),
		file(filepath.Join(dir, "deeper.ts"), `export const d = 1`),
		file(filepath.Join(dir, "ring-a.ts"), `import { b } from "./ring-b"`),
		file(filepath.Join(dir, "ring-b.ts"), `import { a } from "./ring-a"`),
		file(filepath.Join(dir, "orphan.ts"), `export const o = 1`),
	}
	stems := map[string]string{
		"entry": "entry.ts", "sibling": "sibling.ts", "deeper": "deeper.ts",
		"ring-a": "ring-a.ts", "ring-b": "ring-b.ts", "orphan": "orphan.ts",
	}

	alive := liveLibModules(corpus, dir, stems)
	for _, stem := range []string{"entry", "sibling", "deeper"} {
		if !alive[stem] {
			t.Errorf("%s is reachable from the page and was reported dead", stem)
		}
	}
	for _, stem := range []string{"ring-a", "ring-b", "orphan"} {
		if alive[stem] {
			t.Errorf("%s has no way in from outside lib/ and was reported alive", stem)
		}
	}
}

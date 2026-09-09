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

	// And the same comparison the other way round, which is the direction the
	// failure that earned it lived in: `just _generated` ran the `cp` that
	// writes engine/internal/manifest/manifest.v1.json and ci.yml did not, so
	// the embedded schema sat stale on main for thirteen commits while every
	// required context passed. Nothing here asked this question at all.
	for key, e := range jg {
		if !anyReachable(e.blocks, reach) {
			continue
		}
		if _, exempt := exemptFromCI[key]; exempt {
			continue
		}
		if how, _ := pairedWith(e.gate, ci, nil); how == notPaired {
			t.Errorf("`just gate` runs %q (in %s) and no pull request workflow does: %s",
				key, strings.Join(e.blocks, ", "), reverseReason(e.gate, ci))
		}
	}
	for g := range exemptFromCI {
		e, ok := jg[g]
		if !ok || !anyReachable(e.blocks, reach) {
			t.Errorf("%q is exempt from CI but `just gate` does not run it either, "+
				"so the exemption is describing a gate that is not there", g)
		}
	}

	if u := uncalledByGate(recipes, reach); len(u) > 0 {
		t.Errorf("these recipes are gates that `just gate` never calls: %v", u)
	}
}

func TestAGateOnlyTheJustfileRunsIsReported(t *testing.T) {
	// The failure that earned the reverse direction. Every comparison in this
	// tool started from a CI gate and asked whether the justfile covered it,
	// so a gate the justfile ran and no workflow ran was not a question
	// anybody asked. `cp schemas/manifest.v1.json
	// engine/internal/manifest/manifest.v1.json` was in exactly that state.
	ci := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: go run ./tools/errcheck .\n")
	src := "gate:\n    just errcheck\n    just onlyhere\n    just nowhere\n\nerrcheck:\n    go run ./tools/errcheck .\n\nonlyhere:\n    go run ./tools/onlyhere .\n\nnowhere:\n    cd elsewhere && go test ./internal/x\n"
	just := justGates(t, src)
	reach := reachableFromGate(justRecipes(src))

	e, ok := just["tool onlyhere"]
	if !ok {
		t.Fatalf("the fixture does not run the gate under test; the set is %v", keys(just))
	}
	if !anyReachable(e.blocks, reach) {
		t.Fatal("the fixture does not reach the gate from `just gate`, so it is not a case this asks about")
	}
	if how, _ := pairedWith(gateOf(t, just, "tool onlyhere"), ci, nil); how != notPaired {
		t.Error("a gate no workflow runs was reported as covered by one")
	}
	// A gate that CARRIES a directory, because the two take different routes
	// through pairedWith and only one of them was asserted here at first. A
	// `go run ./tools/X` has no directory and returns at the top; anything
	// with one walks the whole function and returns at the bottom. Mutating
	// that bottom return to `pairedExactly` left this test green, which is a
	// surviving mutation understood rather than papered over: the assertion
	// above cannot reach the line.
	if how, _ := pairedWith(gateOf(t, just, "gotest ./internal/x in elsewhere"), ci, nil); how != notPaired {
		t.Error("a gate with a directory that no workflow runs was reported as covered")
	}
	// The direction that already worked must keep working, or this test would
	// pass against a tool that reports everything.
	if how, _ := pairedWith(gateOf(t, just, "tool errcheck"), ci, nil); how != pairedExactly {
		t.Error("a gate both sides run was reported as uncovered")
	}
	if r := reverseReason(gateOf(t, just, "tool onlyhere"), ci); !strings.Contains(r, "no workflow") {
		t.Errorf("the message does not say what is wrong: %q", r)
	}
}

func TestAGateInARecipeTheGateNeverCallsIsNotHeldToCI(t *testing.T) {
	// The reverse direction must not report a recipe `just gate` never calls.
	// `just vuln` needs the network and is deliberately outside the one
	// command; requiring CI to run it would be reporting drift that is not
	// there, and worse, it would make the reverse check noisy enough to delete.
	src := "gate:\n    just errcheck\n\nerrcheck:\n    go run ./tools/errcheck .\n\nvuln:\n    go run ./tools/vulncheck .\n"
	just := justGates(t, src)
	reach := reachableFromGate(justRecipes(src))

	e, ok := just["tool vulncheck"]
	if !ok {
		t.Fatalf("the fixture does not define the gate under test; the set is %v", keys(just))
	}
	if anyReachable(e.blocks, reach) {
		t.Error("a recipe `just gate` never calls was treated as one it runs")
	}
}

func TestARelativeToolsPathIsTheSameGate(t *testing.T) {
	// ci.yml runs `cd engine && go run ../tools/scanrepo ..`. Against a
	// pattern anchored on `./tools/` that line matched nothing at all, so the
	// only credential scan this repository runs was invisible to the tool
	// whose whole job is to notice a gate on one side and not the other. It
	// then showed up as a justfile gate no workflow ran, which is a false
	// positive produced by a blind spot rather than by a real gap.
	got := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: cd engine && go run ../tools/scanrepo ..\n")
	if _, ok := got["tool scanrepo"]; !ok {
		t.Fatalf("`go run ../tools/scanrepo` was not read as a gate; the set is %v", keys(got))
	}
}

func TestTheWholeModuleTargetCoversAPackageUnderIt(t *testing.T) {
	// `just docexamples` runs `go test ./internal/cli -run ...` in engine and
	// CI runs `go test ./...` there, which runs that package. Refusing to pair
	// them would report a gap that is not one. Pairing them as EQUAL would be
	// a different lie, because the narrow target does not cover the wide one,
	// so it is its own tier and the passing output says which part was not
	// compared.
	ci := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: cd engine && go test ./... -race\n")
	src := "gate:\n    just docexamples\n\ndocexamples:\n    cd engine && go test ./internal/cli -run TestX -count=1\n"
	just := justGates(t, src)

	how, where := pairedWith(gateOf(t, just, "gotest ./internal/cli in engine"), ci, nil)
	if how != pairedByWholeModule {
		t.Fatalf("expected a whole module pair, got %v (CI has %v)", how, keys(ci))
	}
	if !strings.Contains(where, "gotest ./... in engine") {
		t.Errorf("the pair does not say what it paired against: %q", where)
	}

	// The other way round is not coverage: running one package does not run
	// the module. Without this the tier would be a blanket pass on any two
	// `go test` gates in one directory.
	if how, _ := pairedWith(gateOf(t, ci, "gotest ./... in engine"), just, reachableFromGate(justRecipes(src))); how != notPaired {
		t.Error("a narrow target was counted as covering the whole module")
	}
}

func TestTheWholeModuleTierDoesNotReachAcrossDirectories(t *testing.T) {
	// A gate is the command AND the directory, and the new tier must not be
	// the hole that forgets it: `go test ./...` in tools does not run
	// engine/internal/cli.
	ci := ciGates(t, "jobs:\n  one:\n    steps:\n      - run: cd tools && go test ./...\n")
	src := "gate:\n    just docexamples\n\ndocexamples:\n    cd engine && go test ./internal/cli -run TestX\n"
	just := justGates(t, src)
	if how, _ := pairedWith(gateOf(t, just, "gotest ./internal/cli in engine"), ci, nil); how != notPaired {
		t.Error("a whole module run in another directory was counted as coverage")
	}
}

func TestEveryCIExemptionStatesWhy(t *testing.T) {
	// The mirror of TestEveryExemptionStatesWhy. An exemption with no reason
	// is a gate quietly dropped, and this map drops it from the run that
	// actually blocks a pull request.
	for g, why := range exemptFromCI {
		if len(strings.TrimSpace(why)) < 80 {
			t.Errorf("the exemption for %q does not say why: %q", g, why)
		}
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

// The false positive that regex earned its boundary from. A permissions block
// is not an action reference, and `statuses: read` is the line that reads like
// one, because `statuses:` ends in the five characters `uses:`.
func TestAPermissionIsNotAnActionReference(t *testing.T) {
	uses := regexp.MustCompile(`(?:^|\s)uses:\s*(\S+)`)
	block := "    permissions:\n      contents: read\n      pull-requests: read\n" +
		"      checks: read\n      statuses: read\n"
	// statuses is the one that matters here. The others are in the fixture so
	// that a future permission ending in the same letters is covered too.
	if found := uses.FindAllStringSubmatch(block, -1); len(found) != 0 {
		t.Errorf("a permissions block was read as %d action references: %v", len(found), found)
	}
	// And the boundary must not have cost it the thing it is for.
	step := "      - uses: actions/checkout@v4\n"
	if found := uses.FindAllStringSubmatch(step, -1); len(found) != 1 || found[0][1] != "actions/checkout@v4" {
		t.Errorf("a real action reference stopped being found: %v", found)
	}
}

func TestEveryActionIsPinnedToACommit(t *testing.T) {
	// A tag is mutable. `actions/checkout@v4` is a promise the publisher can
	// change after anybody reviewed it, so the thing that was reviewed and the
	// thing that runs are only the same by the publisher's continued goodwill.
	// A commit cannot be changed.
	// Anchored on a boundary, and that is not a detail. `uses:` is a substring
	// of `statuses:`, so without one this reads the permissions block line
	// `statuses: read` as an action called `read` pinned to nothing, and
	// reports a workflow that references no such action. A check that fires on
	// something that is not the thing is how a check gets ignored and then
	// deleted, and this one guards a real supply chain property: a tag is
	// mutable and a commit is not.
	uses := regexp.MustCompile(`(?:^|\s)uses:\s*(\S+)`)
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
			// This repository's own action and reusable workflow are the one
			// reference that must NOT be a commit. Customers write
			// `antifailure/antifailure@v1`, release.yml moves v1 on every
			// release, and check.yml calls the action at the same moving
			// tag so a customer's file and the action it reaches are always
			// the same release. Pinning it here would pin every customer to
			// whichever commit this file last named. tools/actioncheck holds
			// the two refs equal to each other.
			if strings.HasPrefix(ref, "antifailure/antifailure@") {
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

func TestEveryToolIsFetchedAtAPinnedVersion(t *testing.T) {
	// A GATE FOR THE CLASS, WRITTEN THE DAY THE CLASS COST A REQUIRED CONTEXT.
	//
	// On 2026-09-08 the `www` job died with "No matching version found for
	// cspell-gitignore@10.3.0". The step was `npx --yes cspell`, floating on
	// the dist tag, and it had resolved a version whose own sibling package was
	// not published yet. Twenty minutes later the same command passed on
	// another pull request. Nothing was checked in between and nothing said so.
	//
	// That is the worst shaped false red this repository has: it is
	// indistinguishable from a real spelling failure in the summary view, and
	// then it HEALS ITSELF, so the next person re-runs the red job, sees green,
	// and learns that re-running a red job is how you fix one. Every other rule
	// here exists to teach the opposite.
	//
	// TestEveryActionIsPinnedToACommit above makes the same argument about
	// actions and has held for months. Container images are the third such
	// surface and are covered separately, by the tests that walk every file
	// able to start a container rather than only the workflows, because this
	// repository names an image in four spellings and only one of them is an
	// `image:` key in a `services:` block.
	//
	// THE JUSTFILE AS WELL AS THE WORKFLOW, because `gatecheck` exists to stop
	// those two disagreeing and an unpinned recipe beside a pinned CI step is
	// exactly that disagreement, one where the developer's run and the gate's
	// run resolve different software.
	//
	// WHAT THIS CANNOT SEE, said rather than implied. `apt-get install`
	// resolves against Ubuntu's archive on every run and is not practically
	// pinnable, so `postgresql-client-17`, `zsh` and `libsecret-tools` are
	// outside this and always will be. `npx <tool>` WITHOUT `--yes` is not here
	// either, and does not need to be: it resolves from the workspace's own
	// node_modules, which a lockfile pins, which is why `npx playwright` and
	// `npx tsc` are safe where `npx --yes` was not. And a pin answers WHICH
	// thing, never whether it arrived: `lycheeverse/lychee-action` was pinned to
	// a commit and to v0.24.2 and still failed on 2026-09-08 with exit 22
	// fetching its own tarball. That is a different failure with a different
	// remedy and this cannot help with it.
	npxYes := regexp.MustCompile(`npx\s+(?:--yes|-y)\s+(\S+)`)

	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	files = append(files, filepath.Join("..", "..", "justfile"))

	checked := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range npxYes.FindAllStringSubmatch(string(body), -1) {
			spec := m[1]
			checked++
			// A version is an `@` that is not the scope marker at the front,
			// so `@scope/pkg` alone is unpinned and `@scope/pkg@1.2.3` is not.
			if at := strings.LastIndex(spec, "@"); at <= 0 {
				t.Errorf("%s: `npx --yes %s` resolves a version at run time.\n"+
					"    Write it as %s@<version>. A gate that cannot be installed reports "+
					"a verdict about a change it never read, and then heals itself, which "+
					"teaches the next person that re-running a red job is how you fix one.",
					filepath.Base(file), spec, spec)
			}
		}
	}

	// The floor is low because the tree is: there is one such command and it is
	// written twice, once in the workflow and once in the recipe gatecheck
	// holds equal to it. Zero would mean the pattern has stopped matching,
	// which is this check reporting clean because it could not look.
	if checked < 2 {
		t.Fatalf("only %d `npx --yes` invocations were found, and there are two. "+
			"The pattern has stopped matching, so this check proved nothing", checked)
	}
	t.Logf("%d run time package fetches checked, all pinned", checked)
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
		"internal": "not a command. It holds packages the tools share, so no build " +
			"writes a binary of this name, and /internal in .gitignore would ignore " +
			"any directory of that name anywhere at the root",
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

// Container images. The same supply chain argument as the action pins above,
// one layer down. An action reference decides what CODE runs; a service
// container image decides what the code was tested AGAINST, and this
// repository's suites assert what a real Postgres refuses, which is a property
// of the server rather than of us. `postgres:17-alpine` is a tag, a tag moves,
// and the reasoning in full lives at the most authoritative of the twelve
// sites: .github/workflows/ci.yml, above the af-cp-test container.

// registryTagged matches a registry qualified image reference carrying a tag:
// a host with at least one dot, an optional port, a path, and a tag. It has no
// leading boundary on purpose, because the case it was written for is a shell
// default, `${AF_KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0}`, where every
// plausible boundary character is already taken.
var registryTagged = regexp.MustCompile(
	`[a-z0-9][a-z0-9-]*(?:\.[a-z0-9-]+)+(?::[0-9]+)?/[a-z0-9._/-]+:[A-Za-z0-9_][A-Za-z0-9._-]*`)

// imageKey matches a YAML `image:` field. Anchored on a boundary for the
// reason the `uses:` pattern above is: without one, any word ending in the
// five characters `image:` reads as the field.
var imageKey = regexp.MustCompile(`(?:^|\s)image:\s*(\S+)`)

// matrixRef matches a `${{ matrix.key }}` interpolation, and matrixEntry the
// `key: value` of one matrix row. Together they are the only thing that can
// see an image a matrix CONSUMES rather than builds; see the consumed set in
// unpinnedImages for why that distinction needs two patterns.
var matrixRef = regexp.MustCompile(`\$\{\{\s*matrix\.([A-Za-z0-9_.-]+)\s*\}\}`)
var matrixEntry = regexp.MustCompile(`^-?\s*([A-Za-z0-9_.-]+):\s*(\S+)`)

// taggedImage reports whether tok, as it appears in a shell command, names a
// container image by tag. It is deliberately narrow: the tokens that surround
// a real `docker run` are ports, environment assignments and flags, and a
// check that reads `-p 55432:5432` as an image called 55432 is a check that
// gets ignored and then deleted.
func taggedImage(tok string) bool {
	tok = strings.Trim(tok, `"'`)
	// A digest is the thing being asked for, a variable cannot be read here,
	// and a URL is not an image.
	if strings.ContainsAny(tok, "$@") || strings.Contains(tok, "://") {
		return false
	}
	i := strings.LastIndex(tok, ":")
	if i <= 0 || i == len(tok)-1 {
		return false
	}
	name, tag := tok[:i], tok[i+1:]
	// A colon with a path after it is not a tag separator.
	if strings.Contains(tag, "/") {
		return false
	}
	// `sha256:<hex>` is the digest itself written on its own, which turns up
	// in checksum commands and in the prose of these very comments.
	if name == "sha256" {
		return false
	}
	letters := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			letters = true
		case r >= '0' && r <= '9', r == '.', r == '_', r == '/', r == '-':
		default:
			return false
		}
	}
	// The name half must carry a letter. This is the whole reason a port
	// mapping does not read as an image.
	if !letters {
		return false
	}
	for i, r := range tag {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
		if i > 0 {
			ok = ok || r == '.' || r == '-'
		}
		if !ok {
			return false
		}
	}
	return true
}

// imageFinding is one image reference that names a tag and no digest.
type imageFinding struct {
	line int
	ref  string
	why  string
}

// unpinnedImages reads a file this repository controls and reports every
// container image in it that is named by something a publisher can move.
//
// Three shapes, because there are three ways this repository starts a
// container and no single pattern sees all of them: a workflow `services:`
// block and a Kubernetes Deployment both write a YAML `image:` field, a
// justfile recipe and a shell script both write a `docker run` command, and
// one script carries the reference as a shell default that neither shape
// reaches.
//
// WHAT THIS CANNOT SEE, said rather than implied. An image named with no tag
// at all, `docker run alpine sh`, is one bare word in a shell command and
// indistinguishable from a subcommand, so the third rule below refuses a
// `docker run` that names neither a digest nor a variable rather than trying
// to find the word. And Go source that starts a container is not read here;
// engine/pkg/emulator/aws.go carries its own pin and its own reasoning.
func unpinnedImages(name, body string) []imageFinding {
	var out []imageFinding
	yaml := strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")

	// Shell continuations, so a `docker run` split over four lines is read as
	// the one command it is. The line number stays that of the first line.
	type logical struct {
		line int
		text string
	}
	var joined []logical
	physical := strings.Split(body, "\n")
	for i := 0; i < len(physical); i++ {
		start, text := i+1, physical[i]
		for strings.HasSuffix(text, "\\") && i+1 < len(physical) {
			i++
			text = strings.TrimSuffix(text, "\\") + " " + strings.TrimSpace(physical[i])
		}
		joined = append(joined, logical{start, text})
	}

	// The indentation of the `matrix:` block we are inside, or -1 for none.
	//
	// WHY THIS EXISTS. `image:` under `strategy.matrix` is not an image
	// reference at all, it is a matrix VARIABLE that happens to be called
	// image, and control-plane-image.yml uses it to name the two images that
	// workflow BUILDS AND PUSHES. There is no digest to pin them to, because
	// they do not exist until the job runs. This gate landed in #340 and the
	// second matrix row landed in #339; each was green alone and main went red
	// on the pair, which is why the collision is worth naming here rather than
	// just fixing.
	//
	// NARROWED BY CONTEXT, NOT BY SHAPE, and the difference is the whole of
	// it. The tempting fix is to skip any value with no registry, tag or
	// digest, since `control-plane` has none. That would also skip a bare
	// `image: postgres` in a services block, which resolves to :latest and is
	// exactly what this gate exists to refuse. A hole opened while closing a
	// false positive is worse than the false positive. TestPinningIgnoresA
	// BuildMatrixAndStillRefusesABareName pins both directions.
	//
	// AND THE MIRROR OF IT, which is why `consumed` exists. A matrix row can
	// just as easily name an image the workflow PULLS, with a services block
	// reading it back as `image: ${{ matrix.pgversion }}`. That is the
	// ordinary way to matrix a database version, and it would be invisible
	// from both ends: skipped here for sitting in a matrix, and skipped below
	// for being an expression. So any matrix key an `image:` field
	// interpolates is checked in the matrix rows that carry it, and only those
	// keys are. The tree holds no such reference at all today, which is the
	// only reason this was a hole and never a live gap.
	consumed := map[string]bool{}
	if yaml {
		for _, l := range joined {
			// From the start of the value to the end of the line, not the
			// `\S+` the field pattern captures: `${{ matrix.pgversion }}`
			// carries spaces, so the capture stops at `${{` and the key
			// this is looking for is in the part that got dropped.
			for _, m := range imageKey.FindAllStringSubmatchIndex(l.text, -1) {
				for _, r := range matrixRef.FindAllStringSubmatch(l.text[m[2]:], -1) {
					consumed[r[1]] = true
				}
			}
		}
	}

	matrixIndent := -1

	for _, l := range joined {
		if yaml {
			indent := len(l.text) - len(strings.TrimLeft(l.text, " "))
			trimmed := strings.TrimSpace(l.text)
			if matrixIndent >= 0 && trimmed != "" && indent <= matrixIndent {
				matrixIndent = -1 // The block ended.
			}
			if matrixIndent < 0 && (trimmed == "matrix:" || strings.HasPrefix(trimmed, "matrix:")) {
				matrixIndent = indent
			}
		}

		// 1a. A matrix row naming an image some `image:` field reads back.
		// The row is the only place that reference is written literally, so it
		// is the only place it can be pinned.
		if yaml && matrixIndent >= 0 {
			if m := matrixEntry.FindStringSubmatch(strings.TrimSpace(l.text)); m != nil && consumed[m[1]] {
				if ref := m[2]; !strings.Contains(ref, "@sha256:") && !strings.Contains(ref, "$") {
					out = append(out, imageFinding{l.line, ref, "a matrix row an image: field reads names a tag"})
				}
			}
		}

		// 1. A YAML image field. The workflows write one in a `services:`
		// block and control-plane-image.yml writes one inside a Deployment it
		// pipes to kubectl, and both are the same field.
		if yaml && matrixIndent < 0 {
			for _, m := range imageKey.FindAllStringSubmatch(l.text, -1) {
				ref := m[1]
				if strings.Contains(ref, "$") {
					continue // An expression, resolved at run time.
				}
				if !strings.Contains(ref, "@sha256:") {
					out = append(out, imageFinding{l.line, ref, "a YAML image: field names a tag"})
				}
			}
		}

		// 2. A registry qualified reference anywhere in the line, which is the
		// only rule that reaches a reference held in a shell variable.
		for _, idx := range registryTagged.FindAllStringIndex(l.text, -1) {
			ref := l.text[idx[0]:idx[1]]
			if idx[0] >= 2 && l.text[idx[0]-2:idx[0]] == "//" {
				continue // Part of a URL.
			}
			if idx[1] < len(l.text) && l.text[idx[1]] == '@' {
				continue // Pinned, the digest follows.
			}
			out = append(out, imageFinding{l.line, ref, "a registry reference names a tag"})
		}

		// 3. A docker command. Every argument is checked, and separately the
		// command as a whole has to name its image by digest or by variable,
		// which is what catches an image named with no tag at all.
		if !strings.Contains(l.text, "docker run ") && !strings.Contains(l.text, "docker pull ") {
			continue
		}
		fields := strings.Fields(l.text)
		named := false
		for _, tok := range fields {
			if strings.Contains(tok, "@sha256:") || strings.Contains(tok, "$") {
				named = true
			}
			if taggedImage(tok) {
				out = append(out, imageFinding{l.line, tok, "a docker argument names a tag"})
			}
		}
		if !named {
			out = append(out, imageFinding{l.line, strings.TrimSpace(l.text),
				"a docker command names neither a digest nor a variable"})
		}
	}
	return out
}

// filesThatStartContainers is every file in this repository that can start
// one: the workflows, the justfile, and every shell script.
//
// TRACKED FILES ONLY, and that is the whole of what changed here. This walked
// the WORKING TREE when it landed in #338, which meant it read files git has
// never heard of. The harness that mutation tested this very gate is an
// untracked shell script in a lane's worktree, it carries `postgres:17-alpine`
// in a variable and a deliberately wrong digest in another, and the gate read
// both and refused a tree whose every real declaration was pinned.
//
// CI WAS GREEN THROUGH ALL OF IT, which is the half worth keeping. A clean
// checkout has no scratch file, so the required contexts saw nothing wrong.
// The only person who could ever hit it is somebody running `just gate` with a
// scratch script open, and from there it reads as the gate being broken rather
// than as the gate looking at the wrong thing. That is how a real gate gets
// switched off, and this one is four commits old.
//
// What this repository declares is what git tracks. An untracked file is not a
// declaration, it is somebody's afternoon.
func filesThatStartContainers(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	listed, err := cmd.Output()
	if err != nil {
		// Not a skip. "I could not look" and "there was nothing to find" are
		// different answers and only one of them is a pass, so a listing this
		// gate cannot obtain fails it.
		t.Fatalf("git ls-files could not enumerate the tree, so nothing was checked: %v", err)
	}
	var files []string
	for _, name := range strings.Split(strings.TrimRight(string(listed), "\x00"), "\x00") {
		switch {
		case strings.HasPrefix(name, ".github/workflows/") && strings.HasSuffix(name, ".yml"),
			name == "justfile",
			strings.HasSuffix(name, ".sh"):
			files = append(files, filepath.Join(root, filepath.FromSlash(name)))
		}
	}
	// A listing that quietly stops matching reads exactly like a clean tree,
	// which is the failure this repository keeps finding in its own
	// instruments.
	if len(files) < 25 {
		t.Fatalf("only %d files were found to scan; the listing has probably stopped matching", len(files))
	}
	return files
}

func TestEveryContainerImageIsPinnedToADigest(t *testing.T) {
	// A tag is a name the publisher can repoint. `postgres:17-alpine` resolved
	// to 17.11 when this was written and will resolve to 17.12 with no commit
	// here, so a suite that proved what Postgres refuses proved it against
	// whatever was behind the tag that morning. A digest cannot move.
	//
	// Nothing else enforces this. Dependabot's docker file fetcher accepts a
	// file only when its NAME matches /dockerfile|containerfile/i, or it is a
	// `values*.yaml` Helm file, or its first YAML document carries both
	// `apiVersion` and `kind`; a workflow carries `on` and `jobs` and is
	// refused. The github-actions ecosystem reads `uses:` only. So this test
	// is the whole of the enforcement, and the pin has to be refreshed by a
	// person. .github/workflows/ci.yml says how.
	checked := 0
	for _, file := range filesThatStartContainers(t) {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, f := range unpinnedImages(filepath.Base(file), string(body)) {
			t.Errorf("%s:%d: %s: %s\n"+
				"    Resolve the multi architecture index digest without a daemon:\n"+
				"    TOKEN=$(curl -s 'https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/<image>:pull' | jq -r .token)\n"+
				"    curl -sI -H \"Authorization: Bearer $TOKEN\" -H 'Accept: application/vnd.oci.image.index.v1+json' \\\n"+
				"      https://registry-1.docker.io/v2/library/<image>/manifests/<tag>\n"+
				"    Then write it as <image>:<tag>@<digest>, in every site at once.",
				filepath.ToSlash(file), f.line, f.why, f.ref)
		}
	}
	if checked < 25 {
		t.Fatalf("only %d files were read; the scan has probably stopped matching", checked)
	}
}

// A build matrix row is not an image reference, and a bare name in a services
// block still is.
//
// BOTH DIRECTIONS, because only the second one proves the narrowing did not
// open a hole. main went red when the gate from #340 met the matrix row from
// #339: `image: control-plane` under `strategy.matrix.include` names an image
// that workflow BUILDS, so there is no digest in existence to pin it to. The
// obvious repair, skipping any value with no registry, tag or digest, would
// also have skipped `image: postgres` in a services block, which resolves to
// :latest and is the exact thing this gate exists to refuse. So the matcher
// was narrowed by CONTEXT, and the third case below is what says so.
func TestPinningIgnoresABuildMatrixAndStillRefusesABareName(t *testing.T) {
	for _, c := range []struct {
		name    string
		yaml    string
		refused bool
	}{
		{
			name: "a matrix row naming an image the workflow builds",
			yaml: "jobs:\n  build:\n    strategy:\n      matrix:\n        include:\n" +
				"          - edition: community\n            image: control-plane\n",
			refused: false,
		},
		{
			name: "a services image pinned to a digest",
			yaml: "jobs:\n  test:\n    services:\n      db:\n" +
				"        image: postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73\n",
			refused: false,
		},
		{
			name:    "a services image with a bare name, which means latest",
			yaml:    "jobs:\n  test:\n    services:\n      db:\n        image: postgres\n",
			refused: true,
		},
		{
			name:    "a services image naming a tag",
			yaml:    "jobs:\n  test:\n    services:\n      db:\n        image: postgres:17-alpine\n",
			refused: true,
		},
		{
			name: "an image key after the matrix block has ended",
			yaml: "jobs:\n  build:\n    strategy:\n      matrix:\n        include:\n" +
				"          - image: control-plane\n    services:\n      db:\n        image: postgres:17-alpine\n",
			refused: true,
		},
		{
			// The mirror of the first case, and the reason the exemption is
			// not simply "a matrix row is never an image". Here the row names
			// something the workflow PULLS, and a services block reads it
			// back. Nothing else in this file can see it: the row is exempt
			// for being in a matrix and the services image is exempt for
			// being an expression.
			name: "a matrix row an image field reads back, naming a tag",
			yaml: "jobs:\n  test:\n    strategy:\n      matrix:\n        include:\n" +
				"          - pgversion: postgres:17-alpine\n" +
				"    services:\n      db:\n        image: ${{ matrix.pgversion }}\n",
			refused: true,
		},
		{
			name: "the same row, pinned",
			yaml: "jobs:\n  test:\n    strategy:\n      matrix:\n        include:\n" +
				"          - pgversion: postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73\n" +
				"    services:\n      db:\n        image: ${{ matrix.pgversion }}\n",
			refused: false,
		},
		{
			// Only the keys an image: field actually interpolates are read.
			// A matrix carrying an unrelated key beside the built image must
			// stay exempt, or the first case regresses by another route.
			name: "a matrix key no image field reads",
			yaml: "jobs:\n  build:\n    strategy:\n      matrix:\n        include:\n" +
				"          - edition: community\n            image: control-plane\n" +
				"            runner: ubuntu-24.04\n",
			refused: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			found := unpinnedImages("example.yml", c.yaml)
			if c.refused && len(found) == 0 {
				t.Fatalf("this should have been refused and was not: %q", c.yaml)
			}
			if !c.refused && len(found) != 0 {
				t.Fatalf("this should have been allowed and was refused as %q: %q", found[0].ref, c.yaml)
			}
		})
	}
}

func TestEveryPinnedImageAgreesOnOneDigest(t *testing.T) {
	// Twelve sites name the same Postgres and no mechanism holds them equal.
	// A bump that changes eleven of them leaves one job testing against a
	// different server, and every job still passes, which is the shape of a
	// defect nobody finds. This is the check that a partial bump fails on.
	pinned := regexp.MustCompile(`([a-z0-9][a-z0-9._/:-]*)@(sha256:[0-9a-f]{64})`)
	digests := map[string]map[string][]string{}
	for _, file := range filesThatStartContainers(t) {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pinned.FindAllStringSubmatch(string(body), -1) {
			name := strings.SplitN(m[1], ":", 2)[0]
			if digests[name] == nil {
				digests[name] = map[string][]string{}
			}
			digests[name][m[2]] = append(digests[name][m[2]], filepath.ToSlash(file))
		}
	}
	if len(digests) == 0 {
		t.Fatal("no pinned image was found at all; the pattern has stopped matching")
	}
	for name, byDigest := range digests {
		if len(byDigest) > 1 {
			for digest, files := range byDigest {
				t.Errorf("%s is pinned to %s in %v", name, digest, files)
			}
			t.Errorf("%s is pinned to %d different digests; a bump changed some sites and not others",
				name, len(byDigest))
		}
	}
}

func TestTheImagePinCheckRefusesAMovingTag(t *testing.T) {
	// The exact cases that produced this check, each in the shape it was
	// found in. A check that has never been shown to say no is not a check.
	refused := []struct {
		name, body string
	}{
		{"ci.yml", "    services:\n      postgres:\n        image: postgres:17-alpine\n"},
		{"cp.yml", "                  - name: pg\n                    image: postgres:17-alpine\n"},
		{"ci.yml", "          docker run -d --name af-cp-test -p 55432:5432 \\\n" +
			"            -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure postgres:17-alpine \\\n" +
			"            -c shared_preload_libraries=pg_stat_statements\n"},
		{"keycloak-up.sh", "IMAGE=\"${AF_KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0}\"\n"},
		{"up.sh", "docker run -d --name x alpine sh -c true\n"},
	}
	for _, c := range refused {
		if got := unpinnedImages(c.name, c.body); len(got) == 0 {
			t.Errorf("a moving tag passed: %q", c.body)
		}
	}

	// And the same five, pinned, which is the direction that proves the check
	// is not simply always saying no.
	const pg = "postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73"
	const kc = "quay.io/keycloak/keycloak:26.0@sha256:09a381c715ab0b111835b70f2905955274843a219c6f27efb348e4d9f4086858"
	accepted := []struct {
		name, body string
	}{
		{"ci.yml", "    services:\n      postgres:\n        image: " + pg + "\n"},
		{"cp.yml", "                  - name: pg\n                    image: " + pg + "\n"},
		{"ci.yml", "          docker run -d --name af-cp-test -p 55432:5432 \\\n" +
			"            -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure " + pg + " \\\n" +
			"            -c shared_preload_libraries=pg_stat_statements\n"},
		{"keycloak-up.sh", "IMAGE=\"${AF_KEYCLOAK_IMAGE:-" + kc + "}\"\n"},
		{"up.sh", "docker run -d --name x " + pg + " sh -c true\n"},
	}
	for _, c := range accepted {
		if got := unpinnedImages(c.name, c.body); len(got) != 0 {
			t.Errorf("a pinned image was reported: %q gave %v", c.body, got)
		}
	}
}

func TestAPortMappingIsNotAContainerImage(t *testing.T) {
	// The false positives this narrowed against, and each of them sits inside
	// a real `docker run` in this repository. A port mapping has a colon and
	// digits on both sides, an environment assignment has a colon nowhere but
	// looks like a word, a database URL has a colon three times, and an image
	// held in a variable cannot be read at all. A check that fires on any of
	// these is a check that gets switched off.
	body := "          docker run -d --name af-cp-target -p 55433:5432 \\\n" +
		"            -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure \\\n" +
		"            -e URL=postgres://postgres:test@127.0.0.1:5432/antifailure \\\n" +
		"            \"$IMAGE\" -c pg_stat_statements.track=all\n"
	if got := unpinnedImages("ci.yml", body); len(got) != 0 {
		t.Errorf("a port mapping or an environment assignment was read as an image: %v", got)
	}
	// A URL to the registry, which these very comments contain.
	if got := unpinnedImages("ci.yml", "        # https://registry-1.docker.io/v2/library/postgres/manifests/17-alpine\n"); len(got) != 0 {
		t.Errorf("a registry URL was read as an image: %v", got)
	}
	// A workflow expression, which resolves to an image built in the same run.
	if got := unpinnedImages("cd.yml", "        image: ${{ needs.build.outputs.image }}\n"); len(got) != 0 {
		t.Errorf("a workflow expression was read as a moving tag: %v", got)
	}
	// And the boundary has not cost the pattern the thing it is for.
	if got := unpinnedImages("ci.yml", "        image: postgres:17-alpine\n"); len(got) != 1 {
		t.Errorf("a real moving tag stopped being found: %v", got)
	}
}

func TestAnUntrackedScratchFileIsNotScanned(t *testing.T) {
	// The defect this gate had against itself for four commits, found by the
	// mutation harness that was testing it rather than by anything in the
	// tree. The harness is an untracked shell script naming a moving tag; the
	// walk read it and the gate refused a fully pinned repository, while every
	// required context stayed green because a clean checkout has no such file.
	//
	// The fixture is a real `docker run` rather than an assignment, because an
	// earlier version of this test used `IMAGE=postgres:17-alpine` on its own
	// line, which the checker correctly does not refuse: a variable assignment
	// is not a command and the image may still be pinned where it is used. The
	// second assertion is what caught that, and it is why it is here.
	scratch := filepath.Join("..", "..", ".gatecheck-scratch-for-a-test.sh")
	body := "#!/usr/bin/env bash\ndocker run -d --name x postgres:17-alpine\n"
	if err := os.WriteFile(scratch, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(scratch) })

	for _, f := range filesThatStartContainers(t) {
		if filepath.Base(f) == ".gatecheck-scratch-for-a-test.sh" {
			t.Fatalf("an untracked scratch file was scanned: %s", f)
		}
	}
	// And it really would have been refused had it been tracked, so the
	// assertion above is about tracking and not about a harmless fixture.
	if got := unpinnedImages("scratch.sh", body); len(got) == 0 {
		t.Error("the fixture does not name a moving tag, so this test proves nothing")
	}
}

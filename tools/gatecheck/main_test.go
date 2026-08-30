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
	workflows, err := pullRequestWorkflows(filepath.Join(root, ".github", "workflows"))
	if err != nil || len(workflows.paths) == 0 {
		t.Skipf("no workflow to compare: %v", err)
	}
	just, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("CONTRIBUTING.md promises `just gate` and there is no justfile: %v", err)
	}

	ciGates := collect(workflows.text())
	if len(ciGates) < 8 {
		t.Fatalf("only %d gates found in CI; the patterns have probably stopped matching", len(ciGates))
	}
	justGates := collect(string(just))
	for g := range ciGates {
		if _, exempt := exemptFromGate[g]; exempt {
			continue
		}
		if _, ok := justGates[g]; !ok {
			t.Errorf("CI runs %q and the justfile does not", g)
		}
	}
	// Every exemption must still name something a workflow runs.
	for g := range exemptFromGate {
		if _, ok := ciGates[g]; !ok {
			t.Errorf("%q is exempt from `just gate` but no pull request workflow runs it, "+
				"so the exemption is describing a gate that is not there", g)
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
	gates := collect(set.text())
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
	if _, ok := collect(set.text())["tool notagate"]; ok {
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

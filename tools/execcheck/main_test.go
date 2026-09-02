package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fixture proves the rules; the last two tests in this file prove the real
// repository, because a gate that only ever ran against its own fixtures is the
// shape of the defect this one exists for.

// theIncident builds the tree that caused this tool to be written: a shell
// script committed at 100644, named by a workflow step and by a justfile recipe
// as a bare relative path.
func theIncident(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write(t, dir, "tools/site/check-tls.sh", "#!/usr/bin/env bash\nset -euo pipefail\n")
	write(t, dir, "tools/site/assemble.sh", "#!/usr/bin/env bash\necho assembling\n")
	write(t, dir, ".github/workflows/deploy.yml", `name: deploy
jobs:
  publish:
    steps:
      - name: Assemble
        run: tools/site/assemble.sh
      - name: Every hostname presents a valid certificate
        run: tools/site/check-tls.sh
`)
	write(t, dir, "justfile", `# Every hostname the site answers on presents a valid certificate.
check-tls:
    tools/site/check-tls.sh

site:
    tools/site/assemble.sh
`)

	gitInit(t, dir)
	chmodIndex(t, dir, "tools/site/assemble.sh")
	return dir
}

func TestRefusesAScriptThatIsRunByPathAndIsNotExecutable(t *testing.T) {
	dir := theIncident(t)

	var out strings.Builder
	err := run(dir, &out)
	if err == nil {
		t.Fatal("a 100644 script that two call sites exec by path was accepted")
	}

	report := out.String()
	// Both rules have to fire, and each one is load bearing on its own. The
	// shape rule is what catches a script before anything runs it; the call
	// site rule is what catches a program whose name does not end in .sh.
	for _, want := range []string{
		"tools/site/check-tls.sh is mode 100644",
		"opens with a shebang, so it is a program",
		".github/workflows/deploy.yml:8 runs it as `tools/site/check-tls.sh`",
		"justfile:3 runs it as `tools/site/check-tls.sh`",
		"git update-index --chmod=+x tools/site/check-tls.sh",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q.\n%s", want, report)
		}
	}
}

func TestAcceptsTheSameTreeOnceTheBitIsSet(t *testing.T) {
	dir := theIncident(t)
	chmodIndex(t, dir, "tools/site/check-tls.sh")

	var out strings.Builder
	if err := run(dir, &out); err != nil {
		t.Fatalf("a tree whose scripts are all 100755 was refused: %v\n%s", err, out.String())
	}
}

// The mode this reads is git's, not the disk's.
//
// This is the whole reason the tool shells out to git rather than calling
// os.Stat. A developer who runs `chmod +x` and forgets `git update-index
// --chmod=+x` has a working tree that runs the script and a commit that does
// not, so a stat says yes on the machine where the bug was made and CI, which
// checks the index out into a fresh tree, says no. If this test ever passes by
// accident, the gate has become a check on the developer's own filesystem.
func TestReadsTheIndexAndNotTheFilesystem(t *testing.T) {
	dir := theIncident(t)
	if err := os.Chmod(filepath.Join(dir, "tools/site/check-tls.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := run(dir, &out); err == nil {
		t.Fatal("chmod +x on disk, with the index still at 100644, was accepted")
	}
}

// A script nothing runs, and whose name does not end in .sh, is not this tool's
// business. The rule is about what a command line execs, not about every file
// with a shebang in it.
func TestIgnoresAFileThatNothingRunsAndThatIsNotAShellScript(t *testing.T) {
	dir := theIncident(t)
	chmodIndex(t, dir, "tools/site/check-tls.sh")
	write(t, dir, "docs/snippet.py", "#!/usr/bin/env python3\nprint('an example, not a program')\n")
	gitAdd(t, dir)

	var out strings.Builder
	if err := run(dir, &out); err != nil {
		t.Fatalf("a shebanged file that no call site runs was refused: %v\n%s", err, out.String())
	}
}

// But the same file becomes this tool's business the moment something runs it,
// which is the case the .sh suffix rule cannot see on its own.
func TestRefusesAProgramWithNoShellSuffixOnceSomethingRunsIt(t *testing.T) {
	dir := theIncident(t)
	chmodIndex(t, dir, "tools/site/check-tls.sh")
	write(t, dir, "tools/site/warm-cache", "#!/usr/bin/env python3\nprint('warming')\n")
	write(t, dir, "justfile", `warm:
    tools/site/warm-cache

site:
    tools/site/assemble.sh
`)
	gitAdd(t, dir)

	var out strings.Builder
	err := run(dir, &out)
	if err == nil {
		t.Fatal("a 100644 program with no suffix, run by a recipe, was accepted")
	}
	if !strings.Contains(out.String(), "tools/site/warm-cache is mode 100644") {
		t.Errorf("the report does not name the program.\n%s", out.String())
	}
}

func TestAnExceptionExcusesTheShapeRule(t *testing.T) {
	dir := theIncident(t)
	chmodIndex(t, dir, "tools/site/check-tls.sh")
	write(t, dir, "docs/example.sh", "#!/usr/bin/env bash\necho a snippet a reader copies\n")
	gitAdd(t, dir)

	restore := withExceptions(t, map[string]string{
		"docs/example.sh": "a snippet the documentation shows, run by the reader and by nothing here",
	})
	defer restore()

	var out strings.Builder
	if err := run(dir, &out); err != nil {
		t.Fatalf("an excused script was still refused: %v\n%s", err, out.String())
	}
}

// An exception is a decision about a defect that exists. Once the defect is
// gone the entry has to go too, because otherwise it silently covers whatever
// takes that path next, which is the same failure as a stale vulnerability
// suppression.
func TestAStaleExceptionFails(t *testing.T) {
	dir := theIncident(t)
	chmodIndex(t, dir, "tools/site/check-tls.sh")

	restore := withExceptions(t, map[string]string{
		"tools/site/deleted-long-ago.sh": "a file that is no longer here",
	})
	defer restore()

	var out strings.Builder
	err := run(dir, &out)
	if err == nil {
		t.Fatal("an exception covering nothing was accepted")
	}
	if !strings.Contains(err.Error(), "tools/site/deleted-long-ago.sh") {
		t.Errorf("the refusal does not name the stale entry: %v", err)
	}
}

// The four ways this tool can find nothing, each of which would otherwise
// report green.
func TestRefusesToPassOverNothing(t *testing.T) {
	cases := []struct {
		name  string
		strip func(t *testing.T, dir string)
		want  string
	}{
		{
			name: "no shell script in the tree",
			strip: func(t *testing.T, dir string) {
				gitRm(t, dir, "tools/site/check-tls.sh", "tools/site/assemble.sh")
			},
			want: "found no tracked shell script",
		},
		{
			name: "no workflow",
			strip: func(t *testing.T, dir string) {
				gitRm(t, dir, ".github/workflows/deploy.yml")
				if err := os.RemoveAll(filepath.Join(dir, ".github")); err != nil {
					t.Fatal(err)
				}
			},
			want: "found no workflow",
		},
		{
			name: "no recipe in the justfile",
			strip: func(t *testing.T, dir string) {
				write(t, dir, "justfile", "# every recipe has gone\n")
				gitAdd(t, dir)
			},
			want: "found no recipe body",
		},
		{
			name: "nothing is run by path any more",
			strip: func(t *testing.T, dir string) {
				write(t, dir, "justfile", "check-tls:\n    bash tools/site/check-tls.sh\n")
				write(t, dir, ".github/workflows/deploy.yml", "name: deploy\njobs:\n  publish:\n    steps:\n      - name: Assemble\n        run: bash tools/site/assemble.sh\n")
				gitAdd(t, dir)
			},
			want: "found no script run by path",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := theIncident(t)
			chmodIndex(t, dir, "tools/site/check-tls.sh")
			c.strip(t, dir)

			var out strings.Builder
			err := run(dir, &out)
			if err == nil {
				t.Fatalf("checking nothing was reported as green.\n%s", out.String())
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refusal = %v, want it to say %q", err, c.want)
			}
		})
	}
}

func TestParseLsFiles(t *testing.T) {
	out := "100755 aaaa 0\ttools/site/assemble.sh\x00" +
		"100644 bbbb 0\ttools/site/check-tls.sh\x00" +
		"120000 cccc 0\tdocs/link to a page\x00"

	got := parseLsFiles(out)
	want := map[string]string{
		"tools/site/assemble.sh":  "100755",
		"tools/site/check-tls.sh": "100644",
		"docs/link to a page":     "120000",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d records, want %d: %v", len(got), len(want), got)
	}
	for p, mode := range want {
		if got[p] != mode {
			t.Errorf("%s = %q, want %q", p, got[p], mode)
		}
	}
}

// The real repository passes. This is the end to end proof, and it is what
// makes `just execcheck` and the CI step mean something rather than describe
// something.
func TestThisRepositoryPasses(t *testing.T) {
	root := repoRoot(t)
	var out strings.Builder
	if err := run(root, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	t.Log(out.String())
}

// The extraction still finds the real call sites.
//
// A pattern that has stopped matching is silent, and silence here reads exactly
// like a clean repository. So this asserts a floor on what the extractor sees in
// this tree, across both surfaces, rather than trusting that a green run means
// it looked.
func TestTheExtractorStillFindsTheRealCallSites(t *testing.T) {
	root := repoRoot(t)

	modes, err := trackedModes(root)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := invocations(root, modes)
	if err != nil {
		t.Fatal(err)
	}

	sources := map[string]bool{}
	for _, c := range calls {
		sources[c.source] = true
		if _, ok := modes[c.script]; !ok {
			t.Errorf("%s:%d resolved to %s, which git does not track", c.source, c.line, c.script)
		}
	}
	if len(calls) < 10 {
		t.Fatalf("only %d invocations found in this repository, so the extraction has stopped looking", len(calls))
	}
	if !sources["justfile"] {
		t.Error("no invocation came from the justfile, so the recipe reader has stopped looking")
	}
	if n := len(sources) - 1; n < 3 {
		t.Errorf("invocations came from %d workflows, so the workflow reader has stopped looking", n)
	}
}

// repoRoot returns the repository this test tree lives in, or skips.
func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		t.Skipf("git cannot read this checkout, so it cannot be asked for a mode: %v", err)
	}
	return root
}

// withExceptions swaps the package's exception table for the duration of one
// test and hands back the restore.
func withExceptions(t *testing.T, table map[string]string) func() {
	t.Helper()
	was := exceptions
	exceptions = table
	return func() { exceptions = was }
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q")
	gitAdd(t, dir)
}

func gitAdd(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "add", "-A")
}

func gitRm(t *testing.T, dir string, paths ...string) {
	t.Helper()
	git(t, dir, append([]string{"rm", "-q", "--cached", "-f"}, paths...)...)
}

// chmodIndex sets the executable bit the way a commit carries it, which is not
// the way chmod does.
//
// The disk is set to match, because a later `git add -A` in the same fixture
// restages any file whose mode differs from the index and would quietly undo
// this. The one test that wants the two to disagree makes them disagree after
// the last add.
func chmodIndex(t *testing.T, dir, path string) {
	t.Helper()
	if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(path)), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "update-index", "--chmod=+x", path)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

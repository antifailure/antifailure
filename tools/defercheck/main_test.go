package main

// The gate's own tests, and they are written against a real git repository in a
// temporary directory rather than against the scanner's internals. The file set
// comes from `git ls-files`, so a test that called scan() directly would prove
// nothing about the half of this tool that decides which files exist.
//
// EVERY FIXTURE ASSEMBLES ITS MARKER FROM FRAGMENTS, for the same reason
// markerWords does: this file is tracked, so a fixture written whole would make
// the gate refuse its own test. The alternative is a row in the exemptions
// excusing the checker's own tests, and that is the one row nobody would ever
// revisit. It costs a plus sign per fixture and it means the tool is subject to
// itself.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The marker fragments the fixtures below are built from.
const (
	todo         = "TO" + "DO"
	fixme        = "FIX" + "ME"
	wip          = "WI" + "P"
	xxx          = "XX" + "X"
	soon         = "coming " + "soon"
	notYet       = "not yet " + "implemented"
	tempFix      = "temporary " + "workaround"
	laterRelease = "in a later " + "release"
	goSkip       = ".Ski" + "p()"
)

// repo builds a git repository holding the given files and adds all of them to
// the index, because the index is what this tool reads.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

type result struct {
	err    error
	report string
}

// check runs the gate the way the justfile recipe does, with no exemptions
// file unless the fixture wrote one.
func check(t *testing.T, dir string) result {
	t.Helper()
	var out bytes.Buffer
	err := run(dir, filepath.Join(dir, "tools", "docs", "defer-exemptions.tsv"), &out)
	return result{err: err, report: out.String()}
}

// Every rule catches something, and the report NAMES the file and the line.
// Table driven so that adding a rule without a case is visible, and so that a
// rule that silently stops matching is a red test rather than a green scan.
func TestEveryRuleCatchesItsCase(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		why  string
		line int
	}{
		{
			name: "a note in Go source",
			file: "engine/internal/env/up.go",
			body: "package env\n\n// " + todo + ": drain the queue first.\nfunc Up() {}\n",
			why:  "an unfinished note",
			line: 3,
		},
		{
			name: "a note in a migration",
			file: "web/packages/db/migrations/0099_things.sql",
			body: "-- " + fixme + " this index is wrong.\nCREATE TABLE t (id int);\n",
			why:  "an unfinished note",
			line: 1,
		},
		{
			name: "a note in Terraform",
			file: "infra/terraform/stacks/control-plane/ci.tf",
			body: "# " + wip + "\nresource \"null_resource\" \"x\" {}\n",
			why:  "an unfinished note",
			line: 1,
		},
		{
			name: "a note in a workflow",
			file: ".github/workflows/ci.yml",
			body: "on: push\njobs:\n  # " + todo + " split this job\n  x: {}\n",
			why:  "an unfinished note",
			line: 3,
		},
		{
			name: "a note in the justfile",
			file: "justfile",
			body: "gate:\n    # " + todo + " add the new check\n    echo hi\n",
			why:  "an unfinished note",
			line: 2,
		},
		{
			name: "an unfilled slot in TypeScript",
			file: "web/apps/api/src/server.ts",
			body: "export const key = '" + xxx + "'\n",
			why:  "an unfinished note",
			line: 1,
		},
		{
			name: "a promise in a page",
			file: "docs/src/content/docs/reference/providers.md",
			body: "# Providers\n\nAurora support is " + soon + ".\n",
			why:  "a promise instead of a capability",
			line: 3,
		},
		{
			name: "a capability described as absent",
			file: "engine/internal/runtime/k8s/k8s.go",
			body: "package k8s\n\n// Egress is " + notYet + " here.\n",
			why:  "a promise instead of a capability",
			line: 3,
		},
		{
			name: "a fix that calls itself short lived",
			file: "engine/internal/cli/gate.go",
			body: "package cli\n\n// A " + tempFix + " until the runner reports.\n",
			why:  "work marked as not the real thing",
			line: 3,
		},
		{
			name: "work handed to another change",
			file: "docs/src/content/docs/concepts/verdicts.md",
			body: "Verdicts are final.\n\nThe blocked case lands " + laterRelease + ".\n",
			why:  "work handed to a later change",
			line: 3,
		},
		{
			name: "a Go skip with no reason",
			file: "engine/internal/env/up_test.go",
			body: "package env\n\nimport \"testing\"\n\nfunc TestUp(t *testing.T) {\n\tt" + goSkip + "\n}\n",
			why:  "a test that skips without saying why",
			line: 6,
		},
		{
			name: "a Go skip whose reason is empty",
			file: "engine/internal/env/down_test.go",
			body: "package env\n\nimport \"testing\"\n\nfunc TestDown(t *testing.T) {\n\tt.Skipf(\"\")\n}\n",
			why:  "a test that skips without saying why",
			line: 6,
		},
		{
			name: "a disabled JavaScript test",
			file: "web/apps/api/test/billing.test.ts",
			body: "import test from 'node:test'\n\ntest.skip('charges once', () => {})\n",
			why:  "a test disabled rather than explained",
			line: 3,
		},
		{
			name: "a disabled JavaScript suite",
			file: "runner/test/model.test.ts",
			body: "describe.skip('the model', () => {})\n",
			why:  "a test disabled rather than explained",
			line: 1,
		},
		{
			name: "a test marked as an intention",
			file: "console/test/plan.test.tsx",
			body: "it.todo('renders the plan')\n",
			why:  "a test disabled rather than explained",
			line: 1,
		},
		{
			name: "an x prefixed test",
			file: "www/test/hero.test.js",
			body: "xit('paints the hero', () => {})\n",
			why:  "a test disabled rather than explained",
			line: 1,
		},
		{
			name: "a skip option with no condition",
			file: "ee/web/test/audit.test.ts",
			body: "test('writes the chain', { skip: true }, () => {})\n",
			why:  "a test disabled rather than explained",
			line: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := check(t, repo(t, map[string]string{c.file: c.body}))
			if got.err == nil {
				t.Fatalf("the gate passed a file that says the work is not finished:\n%s", c.body)
			}
			if !strings.Contains(got.err.Error(), "1 problem") {
				t.Errorf("reported something other than one problem: %v", got.err)
			}
			// The file, the line and the claim, because a count that does not
			// say where cannot be acted on and cannot be proved right either.
			want := fmt.Sprintf("%s:%d: %s", c.file, c.line, c.why)
			if !strings.Contains(got.report, want) {
				t.Errorf("the report does not name %q:\n%s", want, got.report)
			}
		})
	}
}

// The other half of the control, and it is the half that decides whether
// anybody keeps this gate. Each of these looks like a hit and is not one, and
// every line is a real shape from this repository rather than an invented one.
func TestAccurateCodeAndProsePass(t *testing.T) {
	files := map[string]string{
		// The conventional explained skip, in both languages. A skip that
		// names a measured condition is the thing this gate wants, so firing
		// on it would make the tree worse.
		"engine/internal/db/docker/docker_test.go": "package docker\n\nimport \"testing\"\n\n" +
			"func TestUp(t *testing.T) {\n" +
			"\tt.Skip(\"skipped: AF_SKIP_DOCKER is set\")\n" +
			"\tt.Skipf(\"skipped: no Docker daemon is reachable: %v\", err)\n}\n",
		"web/apps/api/test/allowlist.test.ts": "describe('the allowlist', {\n" +
			"  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',\n" +
			"}, () => {})\n",
		// A transient state inside a transaction, which is what the "not yet"
		// clause deliberately does not cover.
		"web/apps/api/src/analytics/rollup.ts": "// rows removed and the new ones not yet written, which would render as a hole.\n",
		// Go's defer, and the reaper's own vocabulary. "Deferred" is a domain
		// term here for an environment a lock held back, so no rule may read
		// the word as deferred work.
		"engine/internal/cli/reap.go": "package cli\n\n" +
			"// Outcome is one of removed, deferred, failed, or would-remove.\n" +
			"func reap() { defer done() }\n",
		// The product's own error vocabulary. Every error carries a next step,
		// so the phrase is a field name rather than a promise.
		"engine/internal/cli/errors.go": "package cli\n\n// The next step is to create a token.\n",
		// A hexadecimal digest that contains marker letters, which is why the
		// marker rule needs word boundaries.
		"web/package-lock.json": "{\"integrity\": \"sha512-a" + xxx + "b" + todo + "c\"}\n",
		// A user facing choice about the user's own future, on a button. The
		// console really does offer to skip a step, and a gate that refused
		// the label would be answered by making the product worse.
		"console/app/(app)/start/page.tsx": "export const label = 'Skip for now'\n",
	}
	got := check(t, repo(t, files))
	if got.err != nil {
		t.Fatalf("the gate refused accurate code:\n%v", got.err)
	}
	if !strings.Contains(got.report, "0 deferrals") {
		t.Errorf("the report does not say the tree is clean:\n%s", got.report)
	}
}

// A source file no text tool can read is a hole in every text gate at once, so
// it fails rather than being counted as clean. runner/src/cassette.ts was
// exactly this: a tracked TypeScript file holding a raw NUL inside a string
// literal, invisible to every text instrument here.
func TestASourceFileHoldingANulByteFails(t *testing.T) {
	dir := repo(t, map[string]string{"runner/src/cassette.ts": "const sep = '\x00'\n"})
	got := check(t, dir)
	if got.err == nil {
		t.Fatal("a source file that no text gate can read was reported as clean")
	}
	for _, want := range []string{"runner/src/cassette.ts", "NUL byte"} {
		if !strings.Contains(got.err.Error(), want) {
			t.Errorf("the failure does not mention %q: %v", want, got.err)
		}
	}
}

// An image cannot be scanned either, and the difference is that nobody expected
// it to be. It is named in the report rather than counted as checked, because a
// number on its own reads as coverage.
func TestAnImageIsNamedAsNotChecked(t *testing.T) {
	dir := repo(t, map[string]string{
		"www/public/og.png": "\x89PNG\x00\x00 binary",
		"README.md":         "# Antifailure\n",
	})
	got := check(t, dir)
	if got.err != nil {
		t.Fatalf("an image made the gate fail: %v", got.err)
	}
	if !strings.Contains(got.report, "www/public/og.png") {
		t.Errorf("the report does not name the file it did not check:\n%s", got.report)
	}
	if !strings.Contains(got.report, "not checked") {
		t.Errorf("the report does not say the file was not checked:\n%s", got.report)
	}
}

// The property the whole exemptions design exists for. A row covers the line it
// quotes, and the next marker in the same file is still reported.
func TestAnExemptionCoversOneLineAndNotTheFile(t *testing.T) {
	files := map[string]string{
		".github/workflows/ci.yml": "on: push\n" +
			"# skips docker:// with a " + todo + " in the parser. dependabot-core issue\n" +
			"# " + todo + " split this job in two\n",
		"tools/docs/defer-exemptions.tsv": "# path\trule\tfragment\treason\n" +
			".github/workflows/ci.yml\tan unfinished note\tdependabot-core issue\tQuotes another project's parser.\n",
	}
	got := check(t, repo(t, files))
	if got.err == nil {
		t.Fatal("the exemption covered the whole file, so a new marker in it was not reported")
	}
	if !strings.Contains(got.err.Error(), "1 problem") {
		t.Errorf("wanted exactly the unexcused line reported: %v", got.err)
	}
}

// An exemption that excuses nothing is a claim about the tree that stopped
// being true, and left alone it becomes a licence over whatever takes that line
// next.
func TestAStaleExemptionFails(t *testing.T) {
	files := map[string]string{
		"README.md":                       "# Antifailure\n",
		"tools/docs/defer-exemptions.tsv": "README.md\tan unfinished note\ta line that is gone\tThe line this excused is gone.\n",
	}
	got := check(t, repo(t, files))
	if got.err == nil {
		t.Fatal("a stale exemption passed, so it now covers whatever takes that line next")
	}
	if !strings.Contains(got.err.Error(), "1 problem") {
		t.Errorf("the stale row was not counted: %v", got.err)
	}
}

// A row that says less than it has to is refused on the day it is written,
// rather than after it has covered something nobody meant it to.
func TestAnExemptionRowMissingItsReasonFails(t *testing.T) {
	files := map[string]string{
		"README.md":                       "# " + todo + "\n",
		"tools/docs/defer-exemptions.tsv": "README.md\tan unfinished note\tAntifailure\n",
	}
	got := check(t, repo(t, files))
	if got.err == nil {
		t.Fatal("an exemption with three fields was accepted")
	}
	if !strings.Contains(got.err.Error(), "four") {
		t.Errorf("the failure does not say what the row is missing: %v", got.err)
	}
}

// The file set is the git index and not a walk of the tree. tools/gatecheck was
// rewritten because the version that walked the tree read an untracked scratch
// script and refused a fully pinned tree while CI stayed green.
func TestItReadsTheIndexRatherThanTheWorkingTree(t *testing.T) {
	dir := repo(t, map[string]string{"README.md": "# Antifailure\n"})
	scratch := filepath.Join(dir, "scratch.go")
	if err := os.WriteFile(scratch, []byte("// "+todo+" delete me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := check(t, dir); got.err != nil {
		t.Fatalf("an untracked scratch file failed the gate: %v", got.err)
	}
	git(t, dir, "add", "scratch.go")
	if got := check(t, dir); got.err == nil {
		t.Fatal("the same file was passed once git tracked it")
	}
}

// A tree git knows nothing about is an error rather than a clean bill of
// health. A gate that reports zero problems because it found zero files is the
// defect this repository keeps finding in its own instruments.
func TestAnEmptyIndexIsAnError(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	got := check(t, dir)
	if got.err == nil {
		t.Fatal("a repository with no tracked files reported a clean scan")
	}
	if !strings.Contains(got.err.Error(), "looking in the wrong place") {
		t.Errorf("the failure does not say the check found nothing to check: %v", got.err)
	}
}

// And the gate pointed at the tree it was written for, with its real
// exemptions. This is the half that a fixture cannot prove: that the rules do
// not fire on 2875 real files, and that every row in the exemptions still
// excuses something.
func TestThisRepositoryPasses(t *testing.T) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git cannot say where this repository is, so this test checked nothing: %v", err)
	}
	root := strings.TrimSpace(string(out))
	got := check(t, root)
	if got.err != nil {
		t.Fatalf("the repository does not pass its own gate:\n%v", got.err)
	}
	if !strings.Contains(got.report, "files scanned") {
		t.Errorf("the report does not say how many files it read:\n%s", got.report)
	}
}

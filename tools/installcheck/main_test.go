package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A check nobody has proved can fail is a check that passes everything the day
// it breaks. These build small trees with a known answer, and the last one
// reads the real repository.

// pkg is one entry as it appears in either lockfile.
type pkg struct {
	Version  string `json:"version,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Resolved string `json:"resolved,omitempty"`
	Link     bool   `json:"link,omitempty"`
}

// workspace writes a lockfile and, when installed is not nil, the hidden
// lockfile npm would have written beside it.
func workspace(t *testing.T, root, dir string, pinned, installed map[string]pkg) {
	t.Helper()
	writeLock(t, filepath.Join(root, filepath.FromSlash(dir), "package-lock.json"), pinned)
	if installed != nil {
		writeLock(t, filepath.Join(root, filepath.FromSlash(dir), "node_modules", ".package-lock.json"), installed)
	}
}

func writeLock(t *testing.T, path string, packages map[string]pkg) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": packages})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func check(t *testing.T, root, dir string) finding {
	t.Helper()
	all, err := workspaces(root)
	if err != nil {
		t.Fatal(err)
	}
	f, err := inspect(root, dir, all)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAMatchingTreeIsCurrent(t *testing.T) {
	root := t.TempDir()
	workspace(t, root, "www",
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/next"}},
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/next"}})

	if f := check(t, root, "www"); f.state != current {
		t.Fatalf("a correct install was reported as %v: %v", f.state, f.details)
	}
}

func TestTheHistoricalDefectIsNamedWithBothVersions(t *testing.T) {
	// The one that cost an evening: node_modules holding Next 15.5.23 against a
	// lockfile pinning 16.3.3. A message that says only "out of date" would
	// leave somebody guessing which of the two numbers they have.
	root := t.TempDir()
	workspace(t, root, "www",
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/next"}},
		map[string]pkg{"node_modules/next": {Version: "15.5.23", Resolved: "https://r/next"}})

	f := check(t, root, "www")
	if f.state != drifted {
		t.Fatalf("a stale install was reported as %v", f.state)
	}
	joined := strings.Join(f.details, "\n")
	if !strings.Contains(joined, "next 15.5.23 installed, 16.3.3 in the lockfile") {
		t.Errorf("the message does not name both versions: %q", joined)
	}
}

func TestAnOptionalPackageThatIsNotInstalledIsNotDrift(t *testing.T) {
	// The false alarm that would sink this. package-lock.json carries the
	// optional platform binaries for every operating system and npm
	// materialises only this one's: www pins 113 packages and installs 55, and
	// all 58 it leaves out are optional. A rule that demanded the two sets be
	// equal would report every correct install on every machine.
	root := t.TempDir()
	workspace(t, root, "www",
		map[string]pkg{
			"node_modules/next":                    {Version: "16.3.3", Resolved: "https://r/next"},
			"node_modules/@img/sharp-linux-x64":    {Version: "0.35.4", Optional: true, Resolved: "https://r/a"},
			"node_modules/@img/sharp-darwin-arm64": {Version: "0.35.4", Optional: true, Resolved: "https://r/b"},
		},
		map[string]pkg{
			"node_modules/next":                    {Version: "16.3.3", Resolved: "https://r/next"},
			"node_modules/@img/sharp-darwin-arm64": {Version: "0.35.4", Optional: true, Resolved: "https://r/b"},
		})

	if f := check(t, root, "www"); f.state != current {
		t.Fatalf("an optional binary for another platform was read as drift: %v", f.details)
	}
}

func TestAPackageTheLockfileRequiresAndIsNotInstalledIsReported(t *testing.T) {
	// The `ERR_MODULE_NOT_FOUND: drizzle-orm` case, which reads exactly like
	// somebody else's branch being broken and is a half installed tree.
	root := t.TempDir()
	workspace(t, root, "web",
		map[string]pkg{
			"node_modules/drizzle-orm": {Version: "0.44.7", Resolved: "https://r/d"},
			"node_modules/hono":        {Version: "4.10.6", Resolved: "https://r/h"},
		},
		map[string]pkg{"node_modules/hono": {Version: "4.10.6", Resolved: "https://r/h"}})

	f := check(t, root, "web")
	if f.state != partial {
		t.Fatalf("a half installed tree was reported as %v", f.state)
	}
	if !strings.Contains(strings.Join(f.details, "\n"), "drizzle-orm") {
		t.Errorf("the message does not name what is missing: %v", f.details)
	}
}

func TestSomethingInstalledThatTheLockfileDoesNotHaveIsReported(t *testing.T) {
	root := t.TempDir()
	workspace(t, root, "web",
		map[string]pkg{"node_modules/hono": {Version: "4.10.6", Resolved: "https://r/h"}},
		map[string]pkg{
			"node_modules/hono":     {Version: "4.10.6", Resolved: "https://r/h"},
			"node_modules/leftover": {Version: "1.0.0", Resolved: "https://r/l"},
		})

	f := check(t, root, "web")
	if f.state != drifted {
		t.Fatalf("a leftover package was reported as %v", f.state)
	}
	if !strings.Contains(strings.Join(f.details, "\n"), "leftover 1.0.0 is installed and the lockfile does not have it") {
		t.Errorf("the message does not say what is left over: %v", f.details)
	}
}

func TestNoNodeModulesAtAllIsADifferentAnswerAndADifferentMessage(t *testing.T) {
	// A fresh worktree is not drift and must not read like it. Drift means
	// everything already checked answered about the wrong versions; an absent
	// tree means nothing has run at all, and telling somebody their results
	// were wrong when they have not got any is a message that trains people to
	// ignore this.
	root := t.TempDir()
	workspace(t, root, "docs",
		map[string]pkg{"node_modules/astro": {Version: "5.16.6", Resolved: "https://r/a"}}, nil)
	workspace(t, root, "www",
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/n"}},
		map[string]pkg{"node_modules/next": {Version: "15.5.23", Resolved: "https://r/n"}})

	fresh := check(t, root, "docs")
	if fresh.state != absent {
		t.Fatalf("a workspace with no node_modules was reported as %v", fresh.state)
	}

	var errOut, out bytes.Buffer
	if code := report(&errOut, &out, []finding{fresh, check(t, root, "www")}, false); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	got := errOut.String()
	if !strings.Contains(got, "no node_modules at all") || !strings.Contains(got, "fresh checkout rather than drift") {
		t.Errorf("the absent case does not read as a fresh checkout:\n%s", got)
	}
	if !strings.Contains(got, "not what the lockfile says") ||
		!strings.Contains(got, "answered about the wrong versions") {
		t.Errorf("the drift case lost the consequence:\n%s", got)
	}
	// And the two are not run together into one list.
	if strings.Index(got, "not what the lockfile says") > strings.Index(got, "no node_modules at all") {
		t.Error("drift is reported after absence; the expensive one should lead")
	}
}

func TestAFreshWorktreeFailsTheRecipesAndNotTheGate(t *testing.T) {
	// The two questions. A recipe about to use a tree needs a non-zero answer
	// so that it installs; `just gate` does not, because every recipe installs
	// what it uses and a gate that went red on a fresh worktree for a workspace
	// it was about to install anyway is the false alarm that gets a check
	// deleted. Drift fails both, because drift means something already ran and
	// answered about the wrong versions.
	root := t.TempDir()
	workspace(t, root, "docs",
		map[string]pkg{"node_modules/astro": {Version: "5.16.6", Resolved: "https://r/a"}}, nil)
	fresh := []finding{check(t, root, "docs")}

	var e1, o1 bytes.Buffer
	if code := report(&e1, &o1, fresh, false); code != 1 {
		t.Errorf("a recipe asking about an absent tree got %d, so it would not install", code)
	}
	var e2, o2 bytes.Buffer
	if code := report(&e2, &o2, fresh, true); code != 0 {
		t.Errorf("`just gate` went red on a fresh worktree, exit %d", code)
	}
	if !strings.Contains(e2.String(), "no node_modules at all") {
		t.Error("the gate stopped saying anything about it, which is the other failure")
	}

	workspace(t, root, "www",
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/n"}},
		map[string]pkg{"node_modules/next": {Version: "15.5.23", Resolved: "https://r/n"}})
	var e3, o3 bytes.Buffer
	if code := report(&e3, &o3, []finding{check(t, root, "www")}, true); code != 1 {
		t.Errorf("drift did not fail the gate, exit %d", code)
	}
}

func TestAMatchingTreeSaysSoAndExitsZero(t *testing.T) {
	root := t.TempDir()
	workspace(t, root, "www",
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/n"}},
		map[string]pkg{"node_modules/next": {Version: "16.3.3", Resolved: "https://r/n"}})

	var errOut, out bytes.Buffer
	if code := report(&errOut, &out, []finding{check(t, root, "www")}, false); code != 0 {
		t.Fatalf("exit code %d on a correct tree, want 0", code)
	}
	if !strings.Contains(out.String(), "1 installed tree matches the lockfile") {
		t.Errorf("a passing run does not say what it checked: %q", out.String())
	}
}

func TestAWorkspaceInstalledBeforeTheOneItLinksIntoIsReported(t *testing.T) {
	// The third shape this evening's stale installs took, and the one that
	// looks least like an install problem. `npm ci` inside ee/web SUCCEEDS with
	// web uninstalled, and the tree it leaves does not work: the file: links
	// resolve to source directories whose own dependencies are absent, and
	// `npm run typecheck` then reports five implicit-any errors inside
	// web/packages/db/src/schema.ts. Somebody reading that is looking at a type
	// error in a file nobody touched, on a branch that is fine.
	root := t.TempDir()
	workspace(t, root, "ee/web",
		map[string]pkg{
			"node_modules/@antifailure/db": {Resolved: "../../web/packages/db", Link: true},
			"node_modules/typescript":      {Version: "5.9.0", Resolved: "https://r/ts"},
		},
		map[string]pkg{
			"node_modules/@antifailure/db": {Resolved: "../../web/packages/db", Link: true},
			"node_modules/typescript":      {Version: "5.9.0", Resolved: "https://r/ts"},
		})
	workspace(t, root, "web",
		map[string]pkg{"node_modules/drizzle-orm": {Version: "0.45.2", Resolved: "https://r/d"}}, nil)

	f := check(t, root, "ee/web")
	if f.state != dangling {
		t.Fatalf("ee/web installed before web was reported as %v", f.state)
	}
	if !strings.Contains(strings.Join(f.details, "\n"), "web has no node_modules") {
		t.Errorf("the message does not name the workspace that is missing: %v", f.details)
	}
	// And the fix has to put them in the order that works, not just name them.
	got := f.fixCommand()
	if strings.Index(got, "--prefix web ") > strings.Index(got, "--prefix ee/web ") {
		t.Errorf("the fix installs ee/web before web, which is the state it is in: %q", got)
	}
	if strings.Contains(got, "install") {
		t.Errorf("`npm install` rewrites ee/web/package-lock.json; `ci` is what works: %q", got)
	}
}

func TestOnceTheLinkedWorkspaceIsInstalledTheLinkIsFine(t *testing.T) {
	// The positive control, so a check that reported every linking workspace
	// could not pass this suite.
	root := t.TempDir()
	workspace(t, root, "ee/web",
		map[string]pkg{"node_modules/@antifailure/db": {Resolved: "../../web/packages/db", Link: true}},
		map[string]pkg{"node_modules/@antifailure/db": {Resolved: "../../web/packages/db", Link: true}})
	workspace(t, root, "web",
		map[string]pkg{"node_modules/drizzle-orm": {Version: "0.45.2", Resolved: "https://r/d"}},
		map[string]pkg{"node_modules/drizzle-orm": {Version: "0.45.2", Resolved: "https://r/d"}})

	if f := check(t, root, "ee/web"); f.state != current {
		t.Fatalf("a linking workspace with its target installed was reported as %v: %v", f.state, f.details)
	}
}

func TestEveryWorkspaceIsFoundRatherThanListed(t *testing.T) {
	// The property that makes this survive a new workspace. Two places in the
	// justfile named their workspaces by hand and each named a different subset.
	root := t.TempDir()
	for _, dir := range []string{"www", "docs", "web", "ee/web", "examples/next-app"} {
		workspace(t, root, dir, map[string]pkg{}, nil)
	}
	// A lockfile inside an installed tree is not a workspace of this repository.
	writeLock(t, filepath.Join(root, "www", "node_modules", "something", "package-lock.json"), map[string]pkg{})

	got, err := workspaces(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs", "ee/web", "examples/next-app", "web", "www"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("found %v, want %v", got, want)
	}
}

func TestTheRealRepositoryIsConsistent(t *testing.T) {
	// The one that matters, and it reads the actual tree. It asserts about what
	// is installed rather than that everything is: a fresh worktree has no
	// node_modules and that is not a failure of this repository.
	root := filepath.Join("..", "..")
	dirs, err := workspaces(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) < 4 {
		t.Fatalf("found %d workspaces, so discovery has probably stopped working: %v", len(dirs), dirs)
	}
	installed := 0
	for _, dir := range dirs {
		f, err := inspect(root, dir, dirs)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if f.state == absent {
			continue
		}
		installed++
		if f.state != current {
			t.Errorf("%s is installed and does not match its lockfile, so anything "+
				"checked against it answered about the wrong versions:\n      %s\n    Fix: %s",
				dir, strings.Join(f.details, "\n      "), f.fixCommand())
		}
	}
	t.Logf("%d workspaces, %d installed here", len(dirs), installed)
}

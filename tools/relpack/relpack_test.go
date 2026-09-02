// Package relpack tests what a release archive actually contains, by building
// one with the script that builds the real ones.
//
// The gap this closes: every release up to and including v0.1.1 shipped an
// archive with no runner package-lock.json in it. Established by downloading
// antifailure_0.1.1_darwin_arm64.tar.gz from the published release and listing
// it, not by reading build.sh. Nothing noticed, because nothing looked: the
// release gates all check signing, publication and reproducibility, which are
// properties of the archive as an opaque blob. What is inside it was asserted
// nowhere.
//
// So this asserts the contents against the promise the archive makes, and it
// does it by running tools/release/build.sh rather than by reading the cp line,
// because a test that greps the script for a filename passes on a script that
// has the filename in a comment.
package relpack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// release is one run of build.sh, shared by every test here.
//
// Built once rather than per test, because each run is a full static Go build
// of the engine and `just test-tools` gives the whole tools module five
// minutes. Two tests doing it separately spent a minute of that on the same
// work.
type release struct {
	stage string   // the directory build.sh assembled, before it was archived
	paths []string // what is inside the archive, relative to its top directory
	err   error
}

var (
	buildOnce sync.Once
	built     release
	buildDir  string
	prefix    = "antifailure_9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH
)

// TestMain removes the shared build directory. t.TempDir is not available for
// something built once for the whole package, and a static binary left in /tmp
// is the kind of thing somebody finds a year later and cannot explain.
func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

func build(t *testing.T) release {
	t.Helper()
	root := repoRoot(t)
	buildOnce.Do(func() { built = runBuild(root) })
	if built.err != nil {
		t.Fatalf("building a release: %v", built.err)
	}
	// An empty listing and a listing missing one file are indistinguishable to
	// a contains check, and an empty one is what a broken tar invocation
	// prints. Checked on every use rather than once, so no assertion below can
	// pass by measuring nothing.
	if len(built.paths) < 5 {
		t.Fatalf("the archive lists %d entries, so this is measuring the tar command rather than the release: %v",
			len(built.paths), built.paths)
	}
	return built
}

func runBuild(root string) release {
	var r release
	if _, err := exec.LookPath("go"); err != nil {
		r.err = fmt.Errorf("go is not on PATH, so the release build cannot run: %w", err)
		return r
	}
	out, err := os.MkdirTemp("", "relpack")
	if err != nil {
		r.err = err
		return r
	}
	buildDir = out
	dist := filepath.Join(out, "dist")
	r.stage = filepath.Join(out, "stage", prefix)

	// The commit and its date are fixed rather than read from git, so this
	// measures what the script packages and not what the working tree is.
	cmd := exec.Command(filepath.Join(root, "tools", "release", "build.sh"),
		runtime.GOOS, runtime.GOARCH, "9.9.9", strings.Repeat("0", 40),
		"2026-08-29T22:19:34Z", dist, filepath.Join(out, "stage"))
	cmd.Dir = root
	if blob, err := cmd.CombinedOutput(); err != nil {
		r.err = fmt.Errorf("build.sh: %w\n%s", err, blob)
		return r
	}

	blob, err := exec.Command("tar", "-tzf", filepath.Join(dist, prefix+".tar.gz")).Output()
	if err != nil {
		r.err = fmt.Errorf("listing the archive: %w", err)
		return r
	}
	for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
		rel := strings.TrimSuffix(strings.TrimPrefix(line, prefix+"/"), "/")
		if rel != "" {
			r.paths = append(r.paths, rel)
		}
	}
	return r
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	// Read rather than stat, so go test's cache keys on the script this
	// package executes through sh and never opens itself. `just test-tools`
	// runs without -count=1 and has already served a cached pass over an
	// installer edited to do nothing.
	if _, err := os.ReadFile(filepath.Join(root, "tools", "release", "build.sh")); err != nil {
		t.Fatalf("build.sh not found from %s: %v", wd, err)
	}
	return root
}

// TestTheArchiveShipsEverythingItPromises names each file and why it is there,
// because a list with no reasons attached is one somebody trims when a build
// gets slow.
func TestTheArchiveShipsEverythingItPromises(t *testing.T) {
	paths := build(t).paths
	want := map[string]string{
		"af":                       "the binary, which is the whole point",
		"LICENSE":                  "the terms the binary is offered under",
		"README.md":                "what to do next, for somebody who unpacked the tar by hand",
		"runner/package.json":      "what af runner install reads to know node and the dependencies",
		"runner/package-lock.json": "the exact versions, without which npm resolves the ranges afresh per machine",
		"runner/tsconfig.json":     "what node --experimental-strip-types needs to agree with",
		"runner/README.md":         "the runner's own documentation",
		"runner/src/main.ts":       "the entry point af test executes and af runner check stats",
	}
	have := map[string]bool{}
	for _, p := range paths {
		have[p] = true
	}
	for path, why := range want {
		if !have[path] {
			t.Errorf("the release archive has no %s, which is there for %s\ngot: %v", path, why, paths)
		}
	}
}

// The lockfile has to be the repository's, not an empty file that satisfies a
// presence check. npm ci refuses a lockfile that disagrees with package.json,
// so a wrong one would fail every first run rather than fail this test.
func TestTheShippedLockfileIsTheRepositorysOwn(t *testing.T) {
	r := build(t)
	staged, err := os.ReadFile(filepath.Join(r.stage, "runner", "package-lock.json"))
	if err != nil {
		t.Fatalf("no lockfile was staged: %v", err)
	}
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "runner", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != string(source) {
		t.Error("the staged lockfile is not runner/package-lock.json, so it pins something else")
	}
}

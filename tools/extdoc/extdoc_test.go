package extdoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is this repository, from the tools module.
const repoRoot = "../.."

// TestEveryExtensionPointIsDocumented is the gate, and it prints the number.
//
// Run it directly to produce the measurement:
//
//	cd tools && go test ./extdoc -run TestEveryExtensionPointIsDocumented -v
func TestEveryExtensionPointIsDocumented(t *testing.T) {
	t.Parallel()
	result, err := Measure(repoRoot)
	if err != nil {
		t.Fatalf("the measurement could not be made, which is not a pass: %v", err)
	}
	t.Log(result.Number())
	for _, p := range result.Problems {
		t.Error(p)
	}
	if len(result.Sockets) == 0 {
		t.Fatal("zero extension points found, so this reported nothing rather than everything")
	}
}

// TestTheCheckSaysNo points it at trees where the answer is no.
//
// Every case is one of the ways the gap actually appears, and each is asserted
// to produce a DIFFERENT problem, because a check that fails for one reason
// whatever you break is a check that has one assertion wearing four.
func TestTheCheckSaysNo(t *testing.T) {
	t.Parallel()

	t.Run("a socket with no page", func(t *testing.T) {
		root := copyRepo(t)
		remove(t, filepath.Join(root, "docs/src/content/docs/providers/emulators.md"))

		result, err := Measure(root)
		requireNoError(t, err)
		requireProblemContaining(t, result, "which does not exist")
		if contains(result.Documented, "emulator") {
			t.Error("the emulator socket counted as documented with no page")
		}
	})

	t.Run("a page that is a stub", func(t *testing.T) {
		root := copyRepo(t)
		write(t, filepath.Join(root, "docs/src/content/docs/providers/stores.md"),
			"---\ntitle: Golden stores\n---\n\nComing soon.\n")

		result, err := Measure(root)
		requireNoError(t, err)
		requireProblemContaining(t, result, "which is a stub rather than documentation")
	})

	t.Run("a page the overview does not link to", func(t *testing.T) {
		// The failure this catches is a real one: the sidebar is generated
		// from the directory, so a page can exist, render, and never be
		// reached by somebody following the map of the five points.
		root := copyRepo(t)
		overview := filepath.Join(root, "docs/src/content/docs/providers/overview.md")
		body := read(t, overview)
		write(t, overview, strings.ReplaceAll(body, "/docs/providers/datastores", "/docs/nowhere"))

		result, err := Measure(root)
		requireNoError(t, err)
		requireProblemContaining(t, result, "the overview does not link to")
	})

	t.Run("a provider name the engine has and the docs do not", func(t *testing.T) {
		// The gcs case. A store added to the engine and left out of the page
		// leaves a reader with a list of three while the binary has four.
		root := copyRepo(t)
		dir := filepath.Join(root, "docs/src/content/docs/providers")
		entries, err := os.ReadDir(dir)
		requireNoError(t, err)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			write(t, p, strings.ReplaceAll(read(t, p), "`gcs`", "`a-store-nobody-has`"))
		}

		result, err := Measure(root)
		requireNoError(t, err)
		requireProblemContaining(t, result, `provider name "gcs"`)
	})

	t.Run("a socket that is not in the table", func(t *testing.T) {
		// The property that makes this survive a sixth extension point:
		// adding one to the engine reds this until somebody writes its page.
		root := copyRepo(t)
		p := filepath.Join(root, "engine/pkg/extension/extension.go")
		body := read(t, p)
		body = strings.Replace(body,
			`	SocketEmulator          = "emulator"`,
			"\tSocketEmulator          = \"emulator\"\n\tSocketQuantumTape       = \"quantum tape\"",
			1)
		write(t, p, body)

		result, err := Measure(root)
		requireNoError(t, err)
		requireProblemContaining(t, result, "has no page in extdoc's table")
	})

	t.Run("constants that moved", func(t *testing.T) {
		// A measurement that cannot be made must be an ERROR and not a pass.
		// A file with no Socket constants would otherwise report zero of zero
		// documented, which is the shape of green this repository keeps
		// finding in its own instruments.
		root := copyRepo(t)
		p := filepath.Join(root, "engine/pkg/extension/extension.go")
		write(t, p, strings.ReplaceAll(read(t, p), "\tSocket", "\tRenamedSocket"))

		_, err := Measure(root)
		if err == nil {
			t.Fatal("a file with no Socket constants was reported as measurable")
		}
		if !strings.Contains(err.Error(), "no Socket constants") {
			t.Errorf("the error does not name the cause: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------

// copyRepo makes a temporary tree holding only the files Measure reads, so a
// case can break one of them without touching the repository.
func copyRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	for _, rel := range []string{
		"engine/pkg/extension/extension.go",
		"engine/pkg/schema/manifest.go",
	} {
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, dst, read(t, filepath.Join(repoRoot, rel)))
	}

	src := filepath.Join(repoRoot, "docs/src/content/docs/providers")
	dstDir := filepath.Join(root, "docs/src/content/docs/providers")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		write(t, filepath.Join(dstDir, e.Name()), read(t, filepath.Join(src, e.Name())))
	}

	// The copy has to be measurable before a case breaks it, or a case could
	// pass because the copy was wrong rather than because the check works.
	result, err := Measure(root)
	if err != nil {
		t.Fatalf("the untouched copy could not be measured: %v", err)
	}
	if !result.OK() {
		t.Fatalf("the untouched copy already fails, so no case below proves anything: %v",
			result.Problems)
	}
	return root
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requireProblemContaining(t *testing.T, r Result, want string) {
	t.Helper()
	for _, p := range r.Problems {
		if strings.Contains(p, want) {
			return
		}
	}
	t.Fatalf("no problem mentioned %q; got %v", want, r.Problems)
}

func contains(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}

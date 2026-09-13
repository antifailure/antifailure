package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both directions, because a guard proved only against the import it refuses
// has never been shown to let the right one through, and one that refuses the
// replacement module would be switched off by the first person it blocked.

func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAnImportOfTheDeadDockerModuleFailsAndNamesTheFile(t *testing.T) {
	root := repoWith(t, map[string]string{
		"engine/go.mod":          "module example.com/engine\n\ngo 1.26\n",
		"engine/internal/x/x.go": "package x\n\nimport _ \"github.com/docker/docker/client\"\n",
	})
	err := bannedImports(root, []string{"engine"}, bannedModules)
	if err == nil {
		t.Fatal("a file importing github.com/docker/docker/client passed the guard")
	}
	for _, want := range []string{"engine/internal/x/x.go", "github.com/docker/docker/client", "github.com/moby/moby/client"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, err)
		}
	}
}

func TestTheModulesTheEngineMovedToAreNotRefused(t *testing.T) {
	root := repoWith(t, map[string]string{
		"engine/go.mod": "module example.com/engine\n\ngo 1.26\n",
		"engine/x.go": "package x\n\nimport (\n\t_ \"github.com/moby/moby/client\"\n" +
			"\t_ \"github.com/moby/moby/api/types/container\"\n)\n",
	})
	if err := bannedImports(root, []string{"engine"}, bannedModules); err != nil {
		t.Fatalf("the replacement modules were refused: %v", err)
	}
}

func TestAModuleThatOnlySharesTheLettersIsNotRefused(t *testing.T) {
	root := repoWith(t, map[string]string{
		"engine/go.mod": "module example.com/engine\n\ngo 1.26\n",
		"engine/x.go":   "package x\n\nimport _ \"github.com/docker/docker-credential-helpers/credentials\"\n",
	})
	if err := bannedImports(root, []string{"engine"}, bannedModules); err != nil {
		t.Fatalf("a different module whose name starts with the same letters was refused: %v", err)
	}
}

func TestAnImportInTheEnterpriseModuleIsFound(t *testing.T) {
	root := repoWith(t, map[string]string{
		"engine/go.mod":    "module example.com/engine\n\ngo 1.26\n",
		"engine/x.go":      "package x\n",
		"ee/engine/go.mod": "module example.com/ee\n\ngo 1.26\n",
		"ee/engine/y.go":   "package y\n\nimport _ \"github.com/docker/docker\"\n",
	})
	err := bannedImports(root, []string{"ee/engine", "engine"}, bannedModules)
	if err == nil || !strings.Contains(err.Error(), "ee/engine/y.go") {
		t.Fatalf("an import in the module the lint jobs never read was not found: %v", err)
	}
}

func TestEveryBannedImportIsNamedAndNotOnlyTheFirst(t *testing.T) {
	root := repoWith(t, map[string]string{
		"engine/go.mod": "module example.com/engine\n\ngo 1.26\n",
		"engine/a.go":   "package x\n\nimport _ \"github.com/docker/docker/client\"\n",
		"engine/b.go":   "package x\n\nimport _ \"github.com/docker/docker/api/types/container\"\n",
	})
	err := bannedImports(root, []string{"engine"}, bannedModules)
	if err == nil {
		t.Fatal("two banned imports passed")
	}
	for _, want := range []string{"engine/a.go", "engine/b.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure stops before %s: %v", want, err)
		}
	}
}

func TestAFileWhoseImportsCannotBeReadIsAFailureNotASkip(t *testing.T) {
	root := repoWith(t, map[string]string{
		"engine/go.mod": "module example.com/engine\n\ngo 1.26\n",
		"engine/x.go":   "package x\n\nimport (\n",
	})
	if err := bannedImports(root, []string{"engine"}, bannedModules); err == nil ||
		!strings.Contains(err.Error(), "was not checked") {
		t.Fatalf("a file whose imports could not be parsed was passed over: %v", err)
	}
}

// The committed tree, read with the same module discovery the scan uses. It
// fails on any tree that still imports the dead module, which main did until the
// engine moved, and it asserts the engine module was found so a wrong root
// cannot pass by scanning nothing.
func TestTheRealRepositoryImportsNoBannedModule(t *testing.T) {
	root := filepath.Join("..", "..")
	mods, err := discoverModules(root)
	if err != nil {
		t.Fatalf("discovering modules: %v", err)
	}
	found := false
	for _, m := range mods {
		if m == "engine" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the engine module was not among %v, so this would have checked nothing", mods)
	}
	if err := bannedImports(root, mods, bannedModules); err != nil {
		t.Fatal(err)
	}
}

// The wiring, not only the function. run takes its module list from
// discoverAndGuard, so this is the path the scan actually uses: a tree with a
// banned import yields no module list at all, and a clean tree yields its
// modules.
func TestTheScanRefusesABannedImportBeforeItStarts(t *testing.T) {
	banned := repoWith(t, map[string]string{
		"engine/go.mod": "module example.com/engine\n\ngo 1.26\n",
		"engine/x.go":   "package x\n\nimport _ \"github.com/docker/docker/client\"\n",
	})
	if mods, err := discoverAndGuard(banned); err == nil {
		t.Fatalf("the scan would have started on %v with a banned import in the tree", mods)
	}
	clean := repoWith(t, map[string]string{
		"engine/go.mod": "module example.com/engine\n\ngo 1.26\n",
		"engine/x.go":   "package x\n\nimport _ \"github.com/moby/moby/client\"\n",
	})
	mods, err := discoverAndGuard(clean)
	if err != nil || len(mods) != 1 || mods[0] != "engine" {
		t.Fatalf("a clean tree did not yield its module: %v, %v", mods, err)
	}
}

// A module nested inside another is its own entry in the module list, so its
// files are read once, under that entry. Walked from the parent as well, every
// banned import in it would be reported twice, and a count a reader cannot trust
// is a count they stop reading.
func TestAnImportInANestedModuleIsReportedOnce(t *testing.T) {
	root := repoWith(t, map[string]string{
		"tools/go.mod":       "module example.com/tools\n\ngo 1.26\n",
		"tools/x.go":         "package x\n",
		"tools/inner/go.mod": "module example.com/tools/inner\n\ngo 1.26\n",
		"tools/inner/y.go":   "package y\n\nimport _ \"github.com/docker/docker/client\"\n",
	})
	err := bannedImports(root, []string{"tools", "tools/inner"}, bannedModules)
	if err == nil {
		t.Fatal("a banned import in a nested module was not found")
	}
	if !strings.HasPrefix(err.Error(), "1 import(s) of a banned module path") {
		t.Errorf("a file in a nested module was not reported exactly once: %v", err)
	}
}

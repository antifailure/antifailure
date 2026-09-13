package main

// THE FAILURE THIS IS FOR. The engine moved off github.com/docker/docker
// because that module path ends at v28.5.2 and none of the Moby advisories
// against it will ever have a fixed version there. While that move was open, a
// live test merged on main that still spoke the old client. The file was new
// and the client change lived in go.mod, so git reported no conflict and each
// pull request passed on its own; only a lint job reading the merge ref
// noticed. The path can come back the same way in any change that copies an
// older example, `go mod tidy` would put it back in go.mod without a word, and
// Dependabot would reopen every alert the migration closed.
//
// So an import of a banned module path fails this tool before it scans
// anything, in every Go module discoverModules finds. That includes ee/engine,
// which the lint jobs do not read, which is why this is here rather than a
// depguard rule in .golangci.yml.

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// bannedModules are module paths no Go file here may import, each with the
// reason the failure prints. A path is matched by whole segments, so a module
// whose name only begins with the same letters is not refused.
var bannedModules = map[string]string{
	"github.com/docker/docker": "the Moby module path that stops at v28.5.2 with no fixed version for its advisories; " +
		"import github.com/moby/moby/client and github.com/moby/moby/api instead",
}

// bannedImports returns every import of a banned module path in the given
// modules, all of them rather than the first.
//
// A file that does not parse is a failure, not a skip. An import this cannot
// read is an import it cannot vouch for, and a guard that passes over what it
// could not look at reports clean about a tree it never saw.
func bannedImports(root string, modules []string, banned map[string]string) error {
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"testdata": true, "dist": true, "bin": true,
	}
	fset := token.NewFileSet()
	var hits []string
	for _, mod := range modules {
		base := filepath.Join(root, filepath.FromSlash(mod))
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == base {
					return nil
				}
				if skip[d.Name()] || strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				// A nested module is its own entry in modules, so it is read
				// there rather than twice.
				if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("reading the imports of %s, so whether it imports a banned module "+
					"was not checked: %w", path, err)
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return fmt.Errorf("%s: import %s: %w", rel, imp.Path.Value, err)
				}
				for module, why := range banned {
					if p == module || strings.HasPrefix(p, module+"/") {
						hits = append(hits, fmt.Sprintf("%s imports %s, %s", filepath.ToSlash(rel), p, why))
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		return fmt.Errorf("%d import(s) of a banned module path:\n  %s", len(hits), strings.Join(hits, "\n  "))
	}
	return nil
}

// discoverAndGuard is module discovery with the guard applied, and it is the one
// place run gets its module list from. The guard lives here rather than as a
// separate call in run so that a test can drive exactly what the scan uses: run
// also starts a scan that takes minutes, so a guard wired only into run is a
// guard no test could watch being removed.
func discoverAndGuard(root string) ([]string, error) {
	modules, err := discoverModules(root)
	if err != nil {
		return nil, err
	}
	if err := bannedImports(root, modules, bannedModules); err != nil {
		return nil, err
	}
	return modules, nil
}

// Command errcheck proves the error catalog and the code agree.
//
// The reference page says "a code with no entry here fails the build, and an
// entry that nothing returns fails it too". It said that before anything
// enforced it, which is the kind of claim documentation should never make on
// its own: a promise in prose that nothing checks is a promise that quietly
// stops being true.
//
// Two directions, and they catch different mistakes.
//
// A code returned by the engine with no catalog entry means a user sees a bare
// code with no cause and no next step, which is the worst error message this
// product can produce, because it looks like an internal identifier that leaked.
//
// A catalog entry nothing returns is dead. It appears in the reference page,
// somebody searches for it when something goes wrong, and finds a page
// describing a situation the software cannot actually be in.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	problems, checked, err := run(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "errcheck:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "errcheck:", p)
		}
		os.Exit(1)
	}
	fmt.Printf("errcheck: %d codes, every one documented and every entry reachable\n", checked)
}

// codePattern matches the generated constant names, AFRUN001 and so on. The
// constants are what the code refers to; the catalog holds AF-RUN-001. Matching
// on the constant rather than on the string is deliberate: a code assembled
// from pieces at runtime would not be found by a text search either, and there
// are none, which this proves by scanning identifiers rather than strings.
var codePattern = regexp.MustCompile(`^AF[A-Z]{2,4}\d{3}$`)

func run(root string) ([]string, int, error) {
	catalog, err := catalogCodes(filepath.Join(root, "engine", "internal", "errors", "codes.gen.go"))
	if err != nil {
		return nil, 0, err
	}
	if len(catalog) == 0 {
		return nil, 0, fmt.Errorf("no codes found; is the catalog generated?")
	}

	used, err := usedCodes(root)
	if err != nil {
		return nil, 0, err
	}

	planned, err := plannedCodes(filepath.Join(root, "engine", "internal", "errors", "catalog.yaml"))
	if err != nil {
		return nil, 0, err
	}

	var problems []string

	// A code used and not defined does not compile, so that direction needs no
	// check here. What can happen is a catalog entry nothing reaches.
	var dead, stale []string
	for code, wire := range catalog {
		switch {
		case used[code] && planned[wire]:
			stale = append(stale, code)
		case !used[code] && !planned[wire]:
			dead = append(dead, code)
		}
	}
	sort.Strings(dead)
	sort.Strings(stale)

	for _, code := range dead {
		problems = append(problems, fmt.Sprintf(
			"%s has a catalog entry and nothing returns it. Either return it where it "+
				"belongs, or mark it 'planned: true' if it is reserved for a feature that "+
				"does not exist yet. It is on the reference page otherwise, and somebody "+
				"will search for it and find a description of a situation the software "+
				"cannot be in.", catalog[code]))
	}
	for _, code := range stale {
		problems = append(problems, fmt.Sprintf(
			"%s is marked 'planned: true' and something returns it. Remove the marker, "+
				"or a user who hits this error will not find it on the reference page.",
			catalog[code]))
	}

	return problems, len(catalog), nil
}

// plannedCodes reads which entries are reserved.
//
// Parsed from the catalog directly rather than from the generated constants,
// because the marker exists to keep an entry off the reference page and the
// page is generated from the catalog. Reading it from anywhere else would let
// the two disagree, which is the failure this whole command is about.
func plannedCodes(path string) (map[string]bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	var current string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if code, ok := strings.CutPrefix(trimmed, "- code: "); ok {
			current = strings.TrimSpace(code)
			continue
		}
		if current != "" && trimmed == "planned: true" {
			out[current] = true
		}
	}
	return out, nil
}

// catalogCodes reads the generated constants, which are the catalog's own
// definition of what exists.
func catalogCodes(path string) (map[string]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !codePattern.MatchString(name.Name) {
				continue
			}
			// The value is the wire form, AF-RUN-001, which is what a message
			// should name rather than the Go identifier.
			wire := name.Name
			if i < len(spec.Values) {
				if lit, isLit := spec.Values[i].(*ast.BasicLit); isLit {
					wire = strings.Trim(lit.Value, `"`)
				}
			}
			out[name.Name] = wire
		}
		return true
	})
	return out, nil
}

// usedCodes finds every code the engine actually refers to.
//
// Parsed rather than grepped. A grep for AFRUN001 matches the definition, the
// documentation, and a comment mentioning it, so it would report every code as
// used and prove nothing. This walks the syntax tree and counts a code only
// where it appears as an identifier outside the file that defines it.
func usedCodes(root string) (map[string]bool, error) {
	used := map[string]bool{}
	generated := filepath.Join("errors", "codes.gen.go")

	for _, dir := range []string{"engine", "tools", "ee"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, generated) {
				return nil
			}
			// Tests count. A code that only a test returns is still reachable,
			// and excluding them would mark every code that guards a rare path
			// as dead, since a rare path is exactly what a test exists to reach.
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return fmt.Errorf("%s: %w", path, parseErr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if codePattern.MatchString(ident.Name) {
					used[ident.Name] = true
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return used, nil
}

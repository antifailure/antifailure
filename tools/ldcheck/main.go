// Command ldcheck proves the release workflow's -X flags name variables that
// exist.
//
// It exists because they did not. The workflow set main.version, main.commit
// and main.buildDate; the variables live in internal/cli. The linker accepts a
// -X for a symbol it cannot find and says nothing, so the build was green, the
// release published, and the binary it produced reported "dev", "none" and
// "unknown". Nothing in the pipeline could have caught that, because every
// stage did exactly what it was asked.
//
// This reads the -X flags out of the workflow and checks each one against the
// source: the package has to exist, the variable has to be in it, and it has to
// be a string. Anything else fails, with the symbol named.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// -X importpath.name=value, in whatever quoting a shell line happens to use.
var xFlag = regexp.MustCompile(`-X\s+["']?([^\s"'=]+)=`)

func main() {
	root := flag.String("root", ".", "repository root")
	workflow := flag.String("workflow", ".github/workflows/release.yml", "workflow to read")
	modulePath := flag.String("module", "github.com/antifailure/antifailure/engine", "the module the flags refer to")
	moduleDir := flag.String("module-dir", "engine", "that module's directory")
	flag.Parse()

	// A positional root as well, so this is invoked the same way as errcheck
	// beside it in CI. Accepting an argument and ignoring it is how a check
	// ends up pointed at the wrong tree and passing.
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	source, err := os.ReadFile(filepath.Join(*root, *workflow))
	if err != nil {
		fail("reading %s: %v", *workflow, err)
	}

	symbols := xFlag.FindAllStringSubmatch(string(source), -1)
	if len(symbols) == 0 {
		// A workflow with no -X at all is either a mistake or a deliberate
		// change nobody told this tool about. Either way it is worth a failure
		// rather than a silent pass over an empty list.
		fail("%s sets no -X flags; either the release stopped stamping a version "+
			"or this check is looking at the wrong file", *workflow)
	}

	var problems []string
	for _, match := range symbols {
		symbol := match[1]
		dot := strings.LastIndex(symbol, ".")
		if dot < 0 {
			problems = append(problems, fmt.Sprintf("%s is not importpath.name", symbol))
			continue
		}
		pkg, name := symbol[:dot], symbol[dot+1:]

		dir, err := packageDir(*root, *modulePath, *moduleDir, pkg)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", symbol, err))
			continue
		}
		if err := hasStringVar(dir, name); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", symbol, err))
			continue
		}
		fmt.Printf("ok  %s\n", symbol)
	}

	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "\nldcheck: %d of %d -X flags name something that does not exist.\n",
			len(problems), len(symbols))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintf(os.Stderr, "\nThe linker accepts -X for a symbol it cannot find and says nothing, "+
			"so this would ship a binary that does not know its own version.\n")
		os.Exit(1)
	}
	fmt.Printf("ldcheck: %d symbols, every one present and a string\n", len(symbols))
}

// packageDir resolves an import path to a directory on disk.
//
// Only within the module the flags are expected to refer to. A -X pointing at a
// dependency is not something this release does, and resolving one would mean
// reading the module cache to answer a question nobody asked.
func packageDir(root, modulePath, moduleDir, pkg string) (string, error) {
	if pkg == "main" {
		// What the broken workflow said. Ambiguous by construction: a
		// repository has as many main packages as it has commands, and the
		// linker picks the one being built. Naming it in full is the fix.
		return "", fmt.Errorf(
			"the package is written as bare %q. The linker resolves that against the "+
				"package being built, which is why a wrong one fails silently. "+
				"Write the full import path", pkg)
	}
	if !strings.HasPrefix(pkg, modulePath+"/") && pkg != modulePath {
		return "", fmt.Errorf("outside %s, which this check does not resolve", modulePath)
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(pkg, modulePath), "/")
	dir := filepath.Join(root, moduleDir, filepath.FromSlash(rel))
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("no such package directory %s", dir)
	}
	return dir, nil
}

// hasStringVar reports whether the package declares name as a package level
// string variable.
//
// A constant does not count: the linker cannot write to one, and -X against a
// constant is the same silent no-op as -X against a missing symbol. Neither
// does a variable of another type, for the same reason.
func hasStringVar(dir, name string) error {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", dir, err)
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, ident := range value.Names {
						if ident.Name != name {
							continue
						}
						if gen.Tok == token.CONST {
							return fmt.Errorf("%s is a constant; the linker cannot write to one", name)
						}
						if !isStringVar(value) {
							return fmt.Errorf("%s is not a string; -X only writes strings", name)
						}
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("no package level variable named %s in %s", name, dir)
}

// isStringVar reports whether a var spec is a string, either declared as one or
// initialised from a string literal.
func isStringVar(value *ast.ValueSpec) bool {
	if ident, ok := value.Type.(*ast.Ident); ok {
		return ident.Name == "string"
	}
	if value.Type != nil {
		return false
	}
	for _, v := range value.Values {
		if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return true
		}
	}
	return false
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ldcheck: "+format+"\n", args...)
	os.Exit(1)
}

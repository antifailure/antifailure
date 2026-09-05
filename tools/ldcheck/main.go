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
// This reads the -X flags out of every place that writes them and checks each
// one against the source: the package has to exist, the variable has to be in
// it, and it has to be a string. Anything else fails, with the symbol named.
//
// And then the other direction, which this command did not check for a year and
// which cost a wrong answer in a shipped binary. Validating only the flags it is
// handed means a variable nobody stamps is invisible: it is not in the list, so
// there is nothing to look up, so the check passes. cli.Edition sat in the same
// var group as Version, Commit and BuildDate, no build ever wrote it, and every
// binary reported the compiled default. Four platforms of that is precisely the
// bug this command exists for, and it went straight past.
//
// So a variable declared alongside a stamped one must be stamped too. The group
// is the declaration: writing `var (Version; Commit; BuildDate)` says these are
// the release's variables, and adding a fourth line to it says the same thing
// about the fourth. A variable that is genuinely not a release stamp belongs in
// its own declaration, which is a one line change and says what it means.
//
// Every place, plural, because there is more than one. The workflow builds the
// release and tools/release/build.sh is the script it calls, and `just
// build-release` calls the same script so that the shipping build exists once
// rather than twice. Checking only the workflow would leave the file that
// actually carries the flags unexamined, which is the same shape of gap as the
// original bug: a stage that does exactly what it was asked, about the wrong
// thing.
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
	"sort"
	"strings"
)

// -X importpath.name=value, in whatever quoting a shell line happens to use.
var xFlag = regexp.MustCompile(`-X\s+["']?([^\s"'=]+)=`)

func main() {
	root := flag.String("root", ".", "repository root")
	sources := flag.String("sources", ".github/workflows/release.yml,tools/release/build.sh",
		"comma separated files that write -X flags")
	modulePath := flag.String("module", "github.com/antifailure/antifailure/engine", "the module the flags refer to")
	moduleDir := flag.String("module-dir", "engine", "that module's directory")
	flag.Parse()

	// A positional root as well, so this is invoked the same way as errcheck
	// beside it in CI. Accepting an argument and ignoring it is how a check
	// ends up pointed at the wrong tree and passing.
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	var symbols [][]string
	var carriers []string
	for _, name := range strings.Split(*sources, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		source, err := os.ReadFile(filepath.Join(*root, name))
		if err != nil {
			// A named source that is not there is a failure rather than a skip.
			// Skipping is how this check would quietly start reading nothing
			// after somebody renamed a file.
			fail("reading %s: %v", name, err)
		}
		found := xFlag.FindAllStringSubmatch(string(source), -1)
		if len(found) > 0 {
			carriers = append(carriers, name)
		}
		symbols = append(symbols, found...)
	}
	if len(symbols) == 0 {
		// No -X anywhere is either a mistake or a deliberate change nobody told
		// this tool about. Either way it is worth a failure rather than a
		// silent pass over an empty list.
		fail("none of %s sets any -X flag; either the release stopped stamping a "+
			"version or this check is looking at the wrong files", *sources)
	}

	var problems []string
	// Which names are stamped, per package directory, so the inverse check
	// below can ask what was declared beside them and left alone.
	stamped := map[string]map[string]bool{}
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
		if stamped[dir] == nil {
			stamped[dir] = map[string]bool{}
		}
		stamped[dir][name] = true
		fmt.Printf("ok  %s\n", symbol)
	}

	dirs := make([]string, 0, len(stamped))
	for dir := range stamped {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var gaps []string
	for _, dir := range dirs {
		found, err := unstamped(dir, stamped[dir])
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		for _, name := range found {
			gaps = append(gaps, fmt.Sprintf("%s, declared beside %s in %s",
				name, strings.Join(sorted(stamped[dir]), ", "), dir))
		}
	}

	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "\nldcheck: %d of %d -X flags name something that does not exist.\n",
			len(problems), len(symbols))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintf(os.Stderr, "\nThe linker accepts -X for a symbol it cannot find and says nothing, "+
			"so this would ship a binary that does not know its own version.\n")
	}
	if len(gaps) > 0 {
		// Reported apart from the flags above, because it is the opposite
		// failure and the fix is different. A flag naming nothing is a typo in
		// the build; a variable nothing names is a value the build forgot,
		// which is how af version told an auditor the wrong edition.
		fmt.Fprintln(os.Stderr, "\nldcheck: nothing stamps these release variables.")
		for _, g := range gaps {
			fmt.Fprintf(os.Stderr, "  %s\n", g)
		}
		fmt.Fprintf(os.Stderr, "\nA variable in the group the release stamps and no flag for it ships "+
			"the compiled default on every platform, silently. Either stamp it in %s, or move it "+
			"into its own declaration if it is not a release stamp.\n", strings.Join(carriers, " and "))
	}
	if len(problems) > 0 || len(gaps) > 0 {
		os.Exit(1)
	}
	fmt.Printf("ldcheck: %d symbols in %s, every one present and a string, "+
		"and nothing declared beside them is left unstamped\n",
		len(symbols), strings.Join(carriers, " and "))
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
	files, err := parseNonTestFiles(dir)
	if err != nil {
		return err
	}

	for _, file := range files {
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

// unstamped reports package level string variables declared in the same var
// group as a stamped one and not stamped themselves.
//
// The group rather than the file or the package, because the group is what
// carries the intent. A package may hold any number of unrelated package level
// strings, and demanding a -X for each would be a gate that fires on things
// nobody meant as a release stamp, which is how a gate stops being read.
//
// A constant in the group is not reported. The linker cannot write to one, so a
// constant beside these variables is a deliberate statement that this value is
// fixed at compile time, and demanding a flag for it would ask for the one thing
// that silently does nothing.
func unstamped(dir string, stamped map[string]bool) ([]string, error) {
	files, err := parseNonTestFiles(dir)
	if err != nil {
		return nil, err
	}

	var found []string
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			names, strings := groupNames(gen)
			if !anyStamped(names, stamped) {
				continue
			}
			for _, name := range strings {
				if !stamped[name] {
					found = append(found, name)
				}
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

// groupNames returns every name declared in one var group, and the subset of
// those that are strings.
func groupNames(gen *ast.GenDecl) (all, strs []string) {
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, ident := range value.Names {
			all = append(all, ident.Name)
			if isStringVar(value) {
				strs = append(strs, ident.Name)
			}
		}
	}
	return all, strs
}

func anyStamped(names []string, stamped map[string]bool) bool {
	for _, name := range names {
		if stamped[name] {
			return true
		}
	}
	return false
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ldcheck: "+format+"\n", args...)
	os.Exit(1)
}

// parseNonTestFiles parses every non-test Go file in one directory.
//
// This was parser.ParseDir, which Go 1.25 deprecated. The replacement is
// deliberately the same reading rather than a better one: it parses the files
// on disk, in name order, with no type checking and no build context.
//
// SO IT STILL DOES NOT UNDERSTAND BUILD TAGS, which is the reason ParseDir was
// deprecated, and saying so is the point of this comment. A package level
// string behind a //go:build tag is counted here on every platform. The fix
// for that is golang.org/x/tools/go/packages, which type checks and therefore
// needs the tree to compile; this gate runs over directories to answer whether
// a symbol is declared, and a gate that can only answer about a tree that
// already builds is a gate that goes quiet exactly when a build is broken.
// Nothing in this repository declares a version string behind a build tag, and
// if something ever does, this is where the answer becomes wrong.
func parseNonTestFiles(dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", filepath.Join(dir, name), err)
		}
		out = append(out, file)
	}
	return out, nil
}

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
	"strconv"
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
// constants are what most of the code refers to; the catalog holds AF-RUN-001.
//
// The comment here used to say a code assembled from pieces would not be found
// by a text search either, and that there were none. There was one.
// policyenforce.Refusal.Error builds "AF-EE-010: organization policy ..." with
// Sprintf and never mentions the constant, so the identifier walk went straight
// past a code that ships, is tested, and is written up on the enterprise policy
// page. It sat marked 'planned: true' and errgen therefore left it off the
// reference page. String literals are read as well now, from the syntax tree
// rather than from the file's text, which is what keeps a comment mentioning a
// code from counting as a use.
var codePattern = regexp.MustCompile(`^AF[A-Z]{2,4}\d{3}$`)

// returnedWire matches a string literal that IS a code, which is the form a
// user reads: the code, then a colon, then the sentence.
//
// Anchored at the start rather than matched anywhere, and the difference is the
// whole rule. Codes appear inside ordinary sentences all over this repository,
// in assertion messages and in prose kept as strings, and every one of those is
// somebody writing ABOUT an error rather than returning one. Matching anywhere
// reported five codes as reachable and only one of them was: the other four
// were "so AF-CPL-003's promise is not kept" and its kind, and clearing their
// planned markers would have put four descriptions of impossible situations on
// the reference page, which is the exact harm this command exists to prevent.
var returnedWire = regexp.MustCompile(`^(AF-[A-Z]{2,4}-[0-9]{3})(:|$)`)

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

	// The control plane returns codes too, and it is TypeScript. Merged rather
	// than checked separately, because the question either scan answers is the
	// same one: does a user ever see this code.
	fromTS, err := usedInTypeScript(root)
	if err != nil {
		return nil, 0, err
	}
	for code := range fromTS {
		used[code] = true
	}

	planned, err := plannedCodes(filepath.Join(root, "engine", "internal", "errors", "catalog.yaml"))
	if err != nil {
		return nil, 0, err
	}

	var problems []string

	// A code the Go side uses and the catalog does not define fails to compile,
	// because the Go side reaches a code through a generated constant. Nothing
	// enforced that on the other side: a JavaScript or TypeScript string is just
	// text, so `code: 'AF-CP-042'` shipped, answered a real caller, and had no
	// entry, no message, no resolution and no row in the published catalog. The
	// caller got an identifier that looks internal and resolves to nothing,
	// which is exactly the failure the Go direction exists to prevent.
	//
	// Sorted so the message is stable, and reported before the dead-entry pass
	// because an undefined code is live and a dead entry is only untidy.
	var undefined []string
	for code, where := range fromTS {
		if _, ok := catalog[code]; !ok {
			undefined = append(undefined, fmt.Sprintf("%s (%s)", wireFor(code), where))
		}
	}
	sort.Strings(undefined)
	for _, item := range undefined {
		problems = append(problems, fmt.Sprintf(
			"%s is returned to a caller and the catalog does not define it. Add it to "+
				"engine/internal/errors/catalog.yaml so it has a message, a next step, a "+
				"documentation page and a row in the published catalog at "+
				"https://antifailure.dev/errors.v1.json. A code that resolves to nothing is "+
				"worse than no code: it reads like an internal identifier that leaked.", item))
	}

	// A code used and not defined does not compile on the Go side, so that
	// direction needs no further check here. What can happen is a catalog entry
	// nothing reaches.
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

	// Every error prints "More: https://antifailure.dev/docs/<path>". A path
	// with no page is a user following a link out of a failure into a 404,
	// which is worse than printing no link at all: the link is a promise that
	// there is something at the other end.
	missing, err := missingDocs(root, filepath.Join(root, "engine", "internal", "errors", "catalog.yaml"))
	if err != nil {
		return nil, 0, err
	}
	for _, m := range missing {
		problems = append(problems, fmt.Sprintf(
			"%s points at docs/%s and there is no page there. Every error prints that "+
				"URL, so this one sends whoever hits it to a 404. Write the page, or "+
				"point the entry at one that exists.", m.codes, m.path))
	}

	return problems, len(catalog), nil
}

type missingDoc struct {
	path  string
	codes string
}

// missingDocs reports catalog doc paths with no page under docs/.
//
// Read from the catalog rather than from the generated constants, for the same
// reason plannedCodes is: the URL an error prints is built from the catalog,
// so the catalog is the thing that has to be right.
func missingDocs(root, catalogPath string) ([]missingDoc, error) {
	body, err := os.ReadFile(catalogPath)
	if err != nil {
		return nil, err
	}

	// Which codes cite which path, so the failure names the errors that would
	// break rather than a bare path somebody has to go looking for.
	byPath := map[string][]string{}
	code := ""
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "- code: "); ok {
			code = strings.TrimSpace(after)
			continue
		}
		after, ok := strings.CutPrefix(trimmed, "docs: ")
		if !ok {
			continue
		}
		docPath := strings.Trim(strings.TrimSpace(after), `"'`)
		// An entry may point at a section: reference/cli#af-init. The page is
		// what has to exist; the anchor is the site generator's business.
		docPath, _, _ = strings.Cut(docPath, "#")
		if docPath == "" || code == "" {
			continue
		}
		byPath[docPath] = append(byPath[docPath], code)
	}
	if len(byPath) == 0 {
		return nil, fmt.Errorf("no docs paths found in the catalog; has its shape changed?")
	}

	contentDir := filepath.Join(root, "docs", "src", "content", "docs")
	var out []missingDoc
	for docPath, codes := range byPath {
		if docExists(contentDir, docPath) {
			continue
		}
		sort.Strings(codes)
		shown := codes
		if len(shown) > 4 {
			shown = append(append([]string{}, shown[:4]...),
				fmt.Sprintf("and %d more", len(codes)-4))
		}
		out = append(out, missingDoc{path: docPath, codes: strings.Join(shown, ", ")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// docExists reports whether a page is there, under either name the site
// generator accepts.
func docExists(contentDir, docPath string) bool {
	for _, candidate := range []string{
		filepath.FromSlash(docPath) + ".md",
		filepath.FromSlash(docPath) + ".mdx",
		filepath.Join(filepath.FromSlash(docPath), "index.md"),
		filepath.Join(filepath.FromSlash(docPath), "index.mdx"),
	} {
		if info, err := os.Stat(filepath.Join(contentDir, candidate)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
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

// wireInSource matches a code written out as a string, which is the only form
// the TypeScript side has.
//
// Quoted deliberately. provision.ts explains the seat limit in a comment that
// names AF-EE-004 in prose, and a pattern that matched the bare code would
// count that comment as a use, which is the mistake the Go scan avoids by
// walking identifiers instead of text.
var wireInSource = regexp.MustCompile("['\"`](AF-[A-Z]{2,4}-[0-9]{3})['\"`]")

// stringLiteralCodes reads the codes out of one Go string literal.
//
// The literal's own quotes are part of its value, so it is unquoted first and
// the bare pattern is matched against the text a user would see.
func stringLiteralCode(lit string) string {
	unquoted, err := strconv.Unquote(lit)
	if err != nil {
		// A literal this package cannot unquote is one it cannot read a code
		// out of either, and skipping it is the honest answer. Raw strings and
		// ordinary quoted ones both unquote; nothing else carries a code.
		return ""
	}
	m := returnedWire.FindStringSubmatch(unquoted)
	if m == nil {
		return ""
	}
	return m[1]
}

// constantFor turns AF-EE-004 into AFEE004, which is how the catalog map is
// keyed.
func constantFor(wire string) string { return strings.ReplaceAll(wire, "-", "") }

// usedInTypeScript finds every code the control plane returns to a user.
//
// This exists because AF-EE-004 shipped marked 'planned: true'. It is thrown on
// the live SAML path in ee/web/sso/src/provision.ts and asserted by a passing
// test, and the marker meant errgen left it off the reference page: somebody
// whose single sign-on was refused with that code searched the errors reference
// and found nothing. The scan above walks Go syntax trees and returns early on
// anything that is not a .go file, so it had walked straight past the throw.
//
// Source only, and tests deliberately excluded, which is the opposite of the Go
// rule three lines up and is not an inconsistency. A Go test reaches a code
// through the generated constant, so it is exercising the path that returns it.
// A TypeScript string is just text: metrics-endpoint.test.ts posts a payload
// carrying AF-DB-001 to prove the control plane groups metrics by whatever code
// arrives, and the control plane neither produces that code nor could. Counting
// that as a use would clear a marker on an error only the engine can raise.
func usedInTypeScript(root string) (map[string]string, error) {
	used := map[string]string{}
	for _, dir := range []string{"web", "console", "runner", "ee"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", "testdata", "test", "tests", "__tests__", "dist", ".next":
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".tsx") {
				return nil
			}
			if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") {
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("%s: %w", path, readErr)
			}
			for _, m := range wireInSource.FindAllSubmatch(b, -1) {
				constant := constantFor(string(m[1]))
				if _, seen := used[constant]; !seen {
					used[constant] = path
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return used, nil
}

// wireFor turns AFCP003 back into AF-CP-003, which is the form a user reads and
// the form the catalog is keyed on.
//
// Derived rather than carried alongside, because the only reason this function
// is needed is to report a code the catalog does NOT contain, so there is no
// entry to read the wire form off.
func wireFor(constant string) string {
	digits := len(constant)
	for digits > 0 && constant[digits-1] >= '0' && constant[digits-1] <= '9' {
		digits--
	}
	if digits <= 2 || digits == len(constant) {
		return constant
	}
	return constant[:2] + "-" + constant[2:digits] + "-" + constant[digits:]
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
				// testutil is skipped because its fakes stand in for components
				// that are not there, and they raise codes they have not earned:
				// the in-memory database answers "AF-DB-010: that version still
				// has a branch" where the catalog says AF-DB-010 means the
				// storage pool is full. A fake is not a place a user can reach.
				if d.Name() == "node_modules" || d.Name() == "testdata" || d.Name() == "testutil" {
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
				switch v := n.(type) {
				case *ast.Ident:
					if codePattern.MatchString(v.Name) {
						used[v.Name] = true
					}
				case *ast.BasicLit:
					if v.Kind != token.STRING {
						return true
					}
					if wire := stringLiteralCode(v.Value); wire != "" {
						used[constantFor(wire)] = true
					}
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

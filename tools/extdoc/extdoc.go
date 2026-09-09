// Package extdoc measures how many of the engine's extension points the
// documentation actually documents.
//
// It exists because the answer was one out of five and nothing said so.
// engine/pkg/extension declares five sockets, a build outside this repository
// can implement all five, and docs/src/content/docs/providers described
// database providers and nothing else. The other four were reachable, tested,
// refusable and undocumented, which is the quietest way for an extension point
// to not exist: it works, and nobody outside can find out that it does.
//
// THE CHECK HAS TO BE ABLE TO SAY NO, and the two directions it says it in are
// what makes it worth having rather than a word count.
//
// A socket with no page fails. That is the original defect, and it fails for a
// socket added tomorrow too, because the mapping from socket to page is a table
// here and a socket missing from that table is itself a failure. Adding a sixth
// extension point therefore reds this until somebody writes the page, which is
// the whole point: the failure mode is documentation lagging code silently, and
// silence is what this removes.
//
// A NAME this build answers to and does not document fails as well. Every
// DBProvider, RuntimeProvider and GoldenStorage constant has to appear
// somewhere under providers, so adding gcs to the engine and not to the page
// leaves somebody reading a list of three stores while the binary has four.
// The names are read from the schema's own constants rather than listed here,
// so this direction needs no maintenance at all.
//
// WHAT IT DOES NOT CHECK, said plainly because a gate that overstates its reach
// is worse than none. It does not read the prose. A page that names the socket
// and explains nothing passes, and no scanner can tell those apart. What it can
// say is that the page exists, that it is not a stub, and that the overview
// links to it, which are the three ways this particular gap actually appeared.
package extdoc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// pageFor maps a socket, by the constant's VALUE in engine/pkg/extension, to
// the page under providers that documents it.
//
// A table rather than a heuristic, because "runtime" appears in most of these
// pages and matching on the word would call every socket documented the moment
// any page mentioned it, which is a check that cannot say no.
//
// A socket absent from this table is a FAILURE and not a skip. That is the
// property that makes this survive a sixth extension point.
var pageFor = map[string]string{
	"database provider":  "databases.md",
	"datastore provider": "datastores.md",
	"runtime":            "runtimes.md",
	"golden store":       "stores.md",
	"emulator":           "emulators.md",
}

// minimumWords is the length below which a page is a stub rather than
// documentation. Crude on purpose: it is not judging the prose, only refusing
// a file created to make this check pass.
const minimumWords = 200

// Result is what the measurement found.
type Result struct {
	// Sockets are the extension points declared by the engine, sorted.
	Sockets []string
	// Documented are the ones with a page that exists, is not a stub, and is
	// linked from the overview.
	Documented []string
	// Names are every provider name this build answers to, sorted.
	Names []string
	// NamedInDocs are the ones that appear somewhere under providers.
	NamedInDocs []string
	// Problems is why each missing one is missing.
	Problems []string
}

// Number is the sentence this measurement exists to produce.
func (r Result) Number() string {
	return fmt.Sprintf("%d of %d extension points documented, %d of %d provider names named",
		len(r.Documented), len(r.Sockets), len(r.NamedInDocs), len(r.Names))
}

// OK reports whether everything is documented.
func (r Result) OK() bool { return len(r.Problems) == 0 }

// Measure reads a repository root and reports what is documented.
func Measure(root string) (Result, error) {
	var out Result

	sockets, err := constantValues(
		filepath.Join(root, "engine", "pkg", "extension", "extension.go"), "Socket")
	if err != nil {
		return out, err
	}
	if len(sockets) == 0 {
		// Reading the wrong file, or the constants moved. An empty list would
		// otherwise report five of five documented out of nothing, which is
		// the shape of pass this repository keeps finding in its own gates.
		return out, fmt.Errorf(
			"no Socket constants found in engine/pkg/extension/extension.go, so this is " +
				"reading the wrong file or they were renamed")
	}
	out.Sockets = sockets

	dir := filepath.Join(root, "docs", "src", "content", "docs", "providers")
	overview, err := os.ReadFile(filepath.Join(dir, "overview.md"))
	if err != nil {
		return out, fmt.Errorf("the providers overview could not be read: %w", err)
	}

	for _, socket := range sockets {
		page, mapped := pageFor[socket]
		if !mapped {
			out.Problems = append(out.Problems, fmt.Sprintf(
				"the socket %q has no page in extdoc's table. A new extension point needs a "+
					"page under docs/src/content/docs/providers and an entry here", socket))
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil {
			out.Problems = append(out.Problems, fmt.Sprintf(
				"the socket %q is documented by %s, which does not exist", socket, page))
			continue
		}
		if words := len(strings.Fields(string(body))); words < minimumWords {
			out.Problems = append(out.Problems, fmt.Sprintf(
				"%s is %d words, which is a stub rather than documentation of the %q socket",
				page, words, socket))
			continue
		}
		// Linked from the overview, because a page nothing links to is a page
		// nobody reaches. The sidebar is generated from the directory, so the
		// link is the only thing a reader following the map would use.
		slug := "/docs/providers/" + strings.TrimSuffix(page, ".md")
		if !strings.Contains(string(overview), slug) {
			out.Problems = append(out.Problems, fmt.Sprintf(
				"%s exists and the overview does not link to %s, so the map of the five "+
					"extension points does not reach it", page, slug))
			continue
		}
		out.Documented = append(out.Documented, socket)
	}

	names, err := builtInNames(filepath.Join(root, "engine", "pkg", "schema", "manifest.go"))
	if err != nil {
		return out, err
	}
	if len(names) == 0 {
		return out, fmt.Errorf(
			"no provider name constants found in engine/pkg/schema/manifest.go, so this is " +
				"reading the wrong file or the types were renamed")
	}
	out.Names = names

	corpus, err := readProviderDocs(dir)
	if err != nil {
		return out, err
	}
	for _, name := range names {
		// In backticks, which is how every one of these is written in the
		// documentation, so that "local" inside the phrase "the local runtime"
		// does not count as documenting the local golden store.
		if strings.Contains(corpus, "`"+name+"`") {
			out.NamedInDocs = append(out.NamedInDocs, name)
			continue
		}
		out.Problems = append(out.Problems, fmt.Sprintf(
			"this build answers to the provider name %q and no page under providers names it",
			name))
	}
	return out, nil
}

// readProviderDocs concatenates every Markdown file under the providers
// directory.
func readProviderDocs(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("the providers directory could not be read: %w", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", err
		}
		b.Write(body)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// builtInNames returns every database provider, runtime and golden storage
// name the schema declares.
func builtInNames(path string) ([]string, error) {
	var out []string
	for _, typeName := range []string{"DBProvider", "RuntimeProvider", "GoldenStorage"} {
		values, err := constantValuesOfType(path, typeName)
		if err != nil {
			return nil, err
		}
		out = append(out, values...)
	}
	sort.Strings(out)
	return out, nil
}

// constantValues returns the string values of constants whose NAME starts with
// a prefix, which is how the socket constants are grouped.
func constantValues(path, prefix string) ([]string, error) {
	return constants(path, func(name, typeName string) bool {
		return strings.HasPrefix(name, prefix)
	})
}

// constantValuesOfType returns the string values of constants declared with a
// named type.
func constantValuesOfType(path, typeName string) ([]string, error) {
	return constants(path, func(_, declared string) bool { return declared == typeName })
}

// constants parses a file and returns the string values of the constants a
// predicate accepts.
//
// Parsed rather than grepped, so a constant inside a comment or a string
// cannot be mistaken for a declaration.
func constants(path string, want func(name, typeName string) bool) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Clean(path), nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// The type is written once on a const block's first spec and carried
		// by the rest, so it is remembered the way the compiler does.
		carried := ""
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := value.Type.(*ast.Ident); ok {
				carried = ident.Name
			} else if value.Type != nil {
				carried = ""
			}
			for i, expr := range value.Values {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || i >= len(value.Names) {
					continue
				}
				if !want(value.Names[i].Name, carried) {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					return nil, err
				}
				out = append(out, unquoted)
			}
		}
	}
	sort.Strings(out)
	return dedupe(out), nil
}

func dedupe(in []string) []string {
	var out []string
	for i, v := range in {
		if i > 0 && v == in[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}

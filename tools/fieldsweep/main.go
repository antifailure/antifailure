// Command fieldsweep proves that every field a manifest may declare either
// does something or is refused.
//
// THE FAILURE IT WAS WRITTEN FOR. `schemas/manifest.v1.json` documented
// `replicas`, `resources.cpu` and `resources.memory`. The reference rendered
// all three. A manifest carrying them parsed without a word, the normalizer
// filled in defaults for them, and then nothing read any of the three: the
// local runtime never mentions replicas, both Kubernetes Deployments hardcode
// one, and neither runtime emits a resource requirement at all. A manifest
// asking for three instances of a worker ran one instance, and the run went
// green having proved nothing about the case its author was worried about.
//
// That is not a missing feature, it is a silent one. The author's evidence
// that the bug is absent is a run that never reproduced the condition. The
// repository already knew the answer and had applied it once, to
// `load.thresholds.query_count_increase`, which is refused at validation under
// a test named for the reason. The three above skipped that pattern and nobody
// noticed, because nothing was counting.
//
// SO THIS COUNTS. For every exported field of every struct in
// `engine/pkg/schema/manifest.go`, which is the Go mirror of the manifest
// schema, one of three things has to be true and a row in
// tools/docs/manifest-field-exemptions.tsv has to say which:
//
//   - READ. Something outside the declaration reads the field. No row needed.
//   - REFUSED. Validation says no, and the row names the path the refusal
//     uses, which is then checked to appear in
//     `engine/internal/manifest/validate.go`.
//   - LABEL. Nothing reads it and that is the decision, because the value is
//     for whoever reads the manifest rather than for the engine. The row names
//     its place in `schemas/manifest.v1.json`, which is then checked to carry
//     a description, because an undocumented label and a broken promise look
//     exactly alike from the outside and the description is the only thing
//     that tells them apart.
//
// THE THIRD KIND WAS ADDED BY BEING WRONG ABOUT ONE. `workflows[].tags` was
// refused here first, on the grounds that nothing reads it. The examples and
// this repository's own antifailure.yaml then failed to parse: eleven
// deliberate groupings, smoke and auth and rbac and billing, written by people
// who were labelling their workflows and never expected the engine to do
// anything with them. Refusing that deletes somebody's annotation to fix a
// promise nobody made. Its schema entry carried no description at all, which
// is the defect that was actually there, and is what the label rule now
// requires.
//
// The exemption file is the half that matters, and it is the mechanism
// tools/docs/wiring-exemptions.tsv already uses for the same shape of problem.
// A field with no reader is not automatically wrong; a field with no reader
// and no stated decision is. The row is where somebody writes the decision
// down, and the check on the tree is what stops the row from being a claim
// nobody kept.
//
// WHAT IT DOES NOT PROVE, said here rather than left to be discovered. A
// reader is a selector expression `.Field` in a package that imports
// `engine/pkg/schema`, resolved by name rather than by type, because type
// checking the whole engine would mean building it and this has to be able to
// run in seconds. The import narrows it a long way, since a manifest field is
// reached through a schema type and naming a type means importing its package,
// but inside those packages a field whose name is also a field name on an
// unrelated struct counts as read the moment anything reads the other one.
//
// That direction is a false PASS, never a false failure, and it is the
// direction this can afford: a field this reports as unread has genuinely no
// selector of that name in any package that could hold one, which is a fact
// worth acting on. `-ambiguous` lists the fields whose name is shared, so the
// size of the hole is a number rather than a caveat.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// schemaFile is the Go mirror of the manifest schema, and the population this
// counts.
const schemaFile = "engine/pkg/schema/manifest.go"

// validateFile is where a refusal lives. An exemption row names the path its
// refusal uses and this file has to carry it, so that deleting the refusal
// fails the sweep rather than leaving a row asserting something untrue.
const validateFile = "engine/internal/manifest/validate.go"

// exemptionsFile holds one row per field that nothing reads, with the reason.
const exemptionsFile = "tools/docs/manifest-field-exemptions.tsv"

// schemaJSON is the contract a label has to be described in.
const schemaJSON = "schemas/manifest.v1.json"

// The two kinds of row.
const (
	kindRefused = "refused"
	kindLabel   = "label"
)

// declaredIn are the files that declare, normalize or refuse a field. None of
// them is a reader: writing a default into a field is not consuming it, and
// refusing one is the outcome this counts separately.
var declaredIn = []string{
	"engine/pkg/schema/",
	"engine/internal/manifest/normalize.go",
	"engine/internal/manifest/validate.go",
}

type field struct {
	owner string
	name  string
}

func (f field) String() string { return f.owner + "." + f.name }

type exemption struct {
	kind   string
	where  string
	reason string
	line   int
}

func main() {
	root := flag.String("root", ".", "repository root")
	ambiguous := flag.Bool("ambiguous", false, "list field names shared with a struct declared elsewhere")
	flag.Parse()

	code, err := run(*root, *ambiguous, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fieldsweep:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(root string, ambiguous bool, out io.Writer) (int, error) {
	fields, err := schemaFields(filepath.Join(root, schemaFile))
	if err != nil {
		return 0, err
	}
	if len(fields) == 0 {
		return 0, fmt.Errorf("no exported fields found in %s, which cannot be right", schemaFile)
	}

	readers, elsewhere, err := selectors(root)
	if err != nil {
		return 0, err
	}

	exempt, err := readExemptions(filepath.Join(root, exemptionsFile))
	if err != nil {
		return 0, err
	}

	validate, err := os.ReadFile(filepath.Join(root, validateFile))
	if err != nil {
		return 0, err
	}

	contract, err := readContract(filepath.Join(root, schemaJSON))
	if err != nil {
		return 0, err
	}

	var honored, refused, labels []field
	var unaccounted []field
	var broken []string
	claimed := map[field]bool{}

	for _, f := range fields {
		if readers[f.name] {
			honored = append(honored, f)
			continue
		}
		e, ok := exempt[f]
		if !ok {
			unaccounted = append(unaccounted, f)
			continue
		}
		claimed[f] = true
		switch e.kind {
		case kindRefused:
			if !strings.Contains(string(validate), e.where) {
				broken = append(broken, fmt.Sprintf(
					"%s:%d: %s is exempt as refused at %q, and %s does not mention that path",
					exemptionsFile, e.line, f, e.where, validateFile))
				continue
			}
			refused = append(refused, f)
		case kindLabel:
			if why := describedInSchema(contract, e.where); why != "" {
				broken = append(broken, fmt.Sprintf(
					"%s:%d: %s is exempt as a label at %q, and %s",
					exemptionsFile, e.line, f, e.where, why))
				continue
			}
			labels = append(labels, f)
		default:
			broken = append(broken, fmt.Sprintf(
				"%s:%d: %s has kind %q, and the kinds are %s and %s",
				exemptionsFile, e.line, f, e.kind, kindRefused, kindLabel))
		}
	}

	// A row for a field that something reads, or for a field that no longer
	// exists, is a decision that has outlived its reason. Reported in both
	// directions for the reason wirecheck reports in both directions: a stale
	// exemption is how a check goes quiet.
	for f, e := range exempt {
		if claimed[f] {
			continue
		}
		switch {
		case !hasField(fields, f):
			broken = append(broken, fmt.Sprintf(
				"%s:%d: %s is exempt and no longer exists in %s", exemptionsFile, e.line, f, schemaFile))
		default:
			broken = append(broken, fmt.Sprintf(
				"%s:%d: %s is exempt as unread and something reads it now, so delete the row",
				exemptionsFile, e.line, f))
		}
	}

	total := len(fields)
	accounted := len(honored) + len(refused) + len(labels)
	printf(out, "%d of %d manifest fields are accounted for.\n", accounted, total)
	printf(out, "  read     %d, something outside the declaration reads the field\n", len(honored))
	printf(out, "  refused  %d, validation says no and %s carries the path\n", len(refused), validateFile)
	for _, f := range refused {
		printf(out, "             %s: %s\n", f, exempt[f].reason)
	}
	printf(out, "  label    %d, nothing reads it on purpose and %s describes it\n", len(labels), schemaJSON)
	for _, f := range labels {
		printf(out, "             %s: %s\n", f, exempt[f].reason)
	}

	if ambiguous {
		reportAmbiguous(out, fields, elsewhere)
	}

	if len(unaccounted) == 0 && len(broken) == 0 {
		return 0, nil
	}
	line(out, "")
	for _, f := range unaccounted {
		printf(out, "%s is declared and nothing reads it.\n", f)
		printf(out, "  Give it a reader, or refuse it in %s and add a row to %s.\n",
			validateFile, exemptionsFile)
	}
	for _, b := range broken {
		line(out, b)
	}
	return 1, nil
}

func hasField(fields []field, f field) bool {
	for _, have := range fields {
		if have == f {
			return true
		}
	}
	return false
}

// schemaFields lists every exported field of every struct declared in the Go
// mirror of the manifest schema.
func schemaFields(path string) ([]field, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []field
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if name.IsExported() {
					out = append(out, field{owner: ts.Name.Name, name: name.Name})
				}
			}
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// schemaImport is the package a consumer of a manifest field has to name.
const schemaImport = "github.com/antifailure/antifailure/engine/pkg/schema"

// selectors reads every Go file that could consume a manifest field and
// returns the set of names selected somewhere, plus the field names declared
// on structs in the same files, which is what makes a name ambiguous.
//
// ONLY PACKAGES THAT IMPORT THE SCHEMA COUNT, and that is the difference
// between a number worth quoting and a name match. A manifest field is reached
// through a schema type, and naming a type means importing the package that
// declares it, so a package with no such import cannot be reading one. Without
// this rule every field called Name, Path, Mode or Enabled was reported wired
// the moment anything anywhere read a field of that name on something else,
// which is most of two trees.
//
// The import is checked per PACKAGE rather than per file, because Go infers a
// type across a package's files: one file can call manifest.Parse and assign
// the result with :=, and a second file in the same package can read a field
// off it with no import of its own. Per file would report that second file's
// field as unread, which is a false failure, and a check that cries wolf is a
// check somebody turns off.
func selectors(root string) (read map[string]bool, elsewhere map[string]bool, err error) {
	read = map[string]bool{}
	elsewhere = map[string]bool{}

	// Two passes over the same files: the first decides which packages name
	// the schema at all, the second reads only those.
	byPackage := map[string][]string{}
	imports := map[string]bool{}
	for _, tree := range []string{"engine", "ee"} {
		dir := filepath.Join(root, tree)
		if _, statErr := os.Stat(dir); statErr != nil {
			continue
		}
		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if !strings.HasSuffix(path, ".go") || skip(rel) {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return fmt.Errorf("%s: %w", rel, parseErr)
			}
			pkg := filepath.Dir(rel)
			byPackage[pkg] = append(byPackage[pkg], path)
			for _, imp := range file.Imports {
				if strings.Trim(imp.Path.Value, `"`) == schemaImport {
					imports[pkg] = true
				}
			}
			return nil
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
	}

	for pkg, paths := range byPackage {
		if !imports[pkg] {
			continue
		}
		for _, path := range paths {
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, parseErr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					read[v.Sel.Name] = true
				case *ast.StructType:
					if v.Fields == nil {
						return true
					}
					for _, f := range v.Fields.List {
						for _, name := range f.Names {
							elsewhere[name.Name] = true
						}
					}
				}
				return true
			})
		}
	}
	return read, elsewhere, nil
}

// skip reports whether a file is a declaration of the schema rather than a
// consumer of it, a test, or a generated copy of source that would make every
// field look read.
func skip(rel string) bool {
	if strings.HasSuffix(rel, "_test.go") || strings.HasSuffix(rel, ".gen.go") {
		return true
	}
	// The proxy image packages the engine's own sources as string literals, so
	// every line of the schema appears there twice over. It reads nothing.
	if strings.HasPrefix(rel, "engine/internal/proxyimage/") {
		return true
	}
	for _, d := range declaredIn {
		if strings.HasPrefix(rel, d) || rel == d {
			return true
		}
	}
	return false
}

// readExemptions parses the ledger. Tab separated, comments on lines starting
// with a hash, four columns: the field, the kind of decision, where in the tree
// that decision is written down, and why.
func readExemptions(path string) (map[field]exemption, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[field]exemption{}, nil
		}
		return nil, err
	}
	out := map[field]exemption{}
	for i, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("%s:%d: expected four tab separated columns, got %d",
				exemptionsFile, i+1, len(parts))
		}
		name := strings.TrimSpace(parts[0])
		dot := strings.LastIndexByte(name, '.')
		if dot <= 0 {
			return nil, fmt.Errorf("%s:%d: %q is not Struct.Field", exemptionsFile, i+1, name)
		}
		f := field{owner: name[:dot], name: name[dot+1:]}
		if _, dup := out[f]; dup {
			return nil, fmt.Errorf("%s:%d: %s appears twice", exemptionsFile, i+1, f)
		}
		out[f] = exemption{
			kind:   strings.TrimSpace(parts[1]),
			where:  strings.TrimSpace(parts[2]),
			reason: strings.TrimSpace(parts[3]),
			line:   i + 1,
		}
	}
	return out, nil
}

// reportAmbiguous names the fields whose reader could belong to something
// else, so that the size of this instrument's blind spot is a number rather
// than a caveat.
func reportAmbiguous(out io.Writer, fields []field, elsewhere map[string]bool) {
	var shared []string
	for _, f := range fields {
		if elsewhere[f.name] {
			shared = append(shared, f.String())
		}
	}
	sort.Strings(shared)
	printf(out, "\n%d of %d field names are also declared on a struct outside the schema,\n",
		len(shared), len(fields))
	line(out, "so a reader found for one of these could belong to the other one:")
	for _, s := range shared {
		printf(out, "  %s\n", s)
	}
}

// readContract loads schemas/manifest.v1.json, which is the contract a label
// has to be described in.
func readContract(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", schemaJSON, err)
	}
	return doc, nil
}

// describedInSchema reports what is wrong with a label's schema entry, and the
// empty string when nothing is.
//
// The `where` column of a label row is a definition and a property, such as
// `workflow.tags`, which resolves to `$defs.workflow.properties.tags`. The
// entry has to exist and it has to carry a description, and that second half is
// the whole rule. `workflows[].tags` had no description at all, which is what
// let a label sit in the schema for as long as it did looking exactly like a
// field somebody forgot to wire: a reader could not tell whether the engine was
// meant to do something with it. A label the schema does not explain is not a
// decision, it is the absence of one.
func describedInSchema(contract map[string]any, where string) string {
	def, prop, ok := strings.Cut(where, ".")
	if !ok {
		return fmt.Sprintf("%q is not <definition>.<property>", where)
	}
	defs, _ := contract["$defs"].(map[string]any)
	object, _ := defs[def].(map[string]any)
	if object == nil {
		return fmt.Sprintf("%s has no $defs.%s", schemaJSON, def)
	}
	properties, _ := object["properties"].(map[string]any)
	entry, _ := properties[prop].(map[string]any)
	if entry == nil {
		return fmt.Sprintf("%s has no $defs.%s.properties.%s", schemaJSON, def, prop)
	}
	if text, _ := entry["description"].(string); strings.TrimSpace(text) == "" {
		return fmt.Sprintf("its entry at $defs.%s.properties.%s carries no description, "+
			"so nothing tells a reader it is a label rather than a field nobody wired", def, prop)
	}
	return ""
}

// printf and line write the report.
//
// The error is discarded, deliberately and in two places rather than at
// fourteen call sites. The destination is a terminal or a test's buffer, a
// write to either fails only when the process is already losing its output,
// and a blank assignment on every line of a report buries the report.
func printf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

func line(out io.Writer, text string) {
	_, _ = fmt.Fprintln(out, text)
}

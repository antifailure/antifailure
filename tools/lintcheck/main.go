// Command lintcheck proves the lint catalogue, the register and the code agree.
//
// The stability page promises that a lint finding's identifier does not change
// between releases while the rule name is free to. A promise like that is worth
// exactly as much as the thing enforcing it, and prose enforces nothing.
//
// Four directions, and they catch different mistakes.
//
// A rule the linter can report with no catalogue entry emits a finding with an
// empty identifier, which is the worst outcome available here: a consumer
// filtering on the identifier silently stops seeing that finding, and nothing
// looks broken from either side.
//
// A catalogue entry naming no rule is dead. It is on the reference page and in
// the published catalogue, somebody writes a filter against it, and the filter
// matches nothing forever.
//
// An identifier in the register and not in the catalogue has been withdrawn.
// That is the promise itself breaking: whatever was matching on it now matches
// nothing, or worse, matches a different finding if the number is handed out
// again.
//
// A generated map that disagrees with the catalogue means the identifier a user
// receives is not the one the documentation shows, which makes both of them
// useless.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	problems, checked, err := run(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lintcheck:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "lintcheck:", p)
		}
		os.Exit(1)
	}
	fmt.Printf("lintcheck: %d findings, every one identified and every identifier still assigned\n",
		checked)
}

type entry struct {
	ID      string `yaml:"id"`
	Rule    string `yaml:"rule"`
	Title   string `yaml:"title"`
	Retired string `yaml:"retired"`
}

type catalog struct {
	Findings []entry `yaml:"findings"`
}

type register struct {
	Assigned []struct {
		ID string `json:"id"`
	} `json:"assigned"`
}

const (
	catalogPath   = "engine/internal/insights/lintcatalog.yaml"
	registerPath  = "engine/internal/insights/findings.register.json"
	generatedPath = "engine/internal/insights/findings.gen.go"
	rulesDir      = "engine/internal/insights"
)

func run(root string) ([]string, int, error) {
	entries, err := readCatalog(filepath.Join(root, catalogPath))
	if err != nil {
		return nil, 0, err
	}
	if len(entries) == 0 {
		return nil, 0, fmt.Errorf("no findings in %s; has its shape changed?", catalogPath)
	}

	declared, err := declaredRules(filepath.Join(root, rulesDir))
	if err != nil {
		return nil, 0, err
	}
	if len(declared) == 0 {
		return nil, 0, fmt.Errorf("no Rule constants found in %s, so this check is measuring nothing",
			rulesDir)
	}

	generated, err := generatedIDs(filepath.Join(root, generatedPath))
	if err != nil {
		return nil, 0, err
	}

	assigned, err := readRegister(filepath.Join(root, registerPath))
	if err != nil {
		return nil, 0, err
	}

	var problems []string

	live := map[string]entry{}
	byID := map[string]string{}
	for _, e := range entries {
		if first, dup := byID[e.ID]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s is assigned twice, to %q and to %q. An identifier means one finding for as "+
					"long as this project exists, so the second one needs the next unused number.",
				e.ID, first, e.Rule))
			continue
		}
		byID[e.ID] = e.Rule
		if e.Retired == "" {
			live[e.Rule] = e
		}
	}

	// A rule the linter can report and the catalogue has never heard of.
	var unidentified []string
	for name := range declared {
		if _, ok := live[name]; !ok {
			unidentified = append(unidentified, fmt.Sprintf("%s (%s)", name, declared[name]))
		}
	}
	sort.Strings(unidentified)
	for _, name := range unidentified {
		problems = append(problems, fmt.Sprintf(
			"%s can be reported and has no identifier. Add it to %s with the next unused "+
				"LINT number. A finding with an empty identifier is worse than one with a "+
				"changing name: anything filtering on the identifier stops seeing it and "+
				"nothing looks broken.", name, catalogPath))
	}

	// A catalogue entry for a rule that does not exist.
	var dead, retiredButLive []string
	for _, e := range entries {
		_, exists := declared[e.Rule]
		switch {
		case e.Retired == "" && !exists:
			dead = append(dead, fmt.Sprintf("%s (%s)", e.ID, e.Rule))
		case e.Retired != "" && exists:
			retiredButLive = append(retiredButLive, fmt.Sprintf("%s (%s)", e.ID, e.Rule))
		}
	}
	sort.Strings(dead)
	sort.Strings(retiredButLive)
	for _, item := range dead {
		problems = append(problems, fmt.Sprintf(
			"%s is in the catalogue and no rule in %s reports it. Either report it where it "+
				"belongs, or give the entry a 'retired:' reason. It is on the reference page "+
				"otherwise, and a filter written against it matches nothing forever.",
			item, rulesDir))
	}
	for _, item := range retiredButLive {
		problems = append(problems, fmt.Sprintf(
			"%s is marked retired and the rule still exists. Remove the marker, or whoever "+
				"reads the reference page will think the finding is gone.", item))
	}

	// An identifier that was handed out and is no longer spoken for.
	var withdrawn, unregistered []string
	for _, id := range assigned {
		if _, ok := byID[id]; !ok {
			withdrawn = append(withdrawn, id)
		}
	}
	registered := map[string]bool{}
	for _, id := range assigned {
		registered[id] = true
	}
	for _, e := range entries {
		if !registered[e.ID] {
			unregistered = append(unregistered, fmt.Sprintf("%s (%s)", e.ID, e.Rule))
		}
	}
	sort.Strings(withdrawn)
	sort.Strings(unregistered)
	for _, id := range withdrawn {
		problems = append(problems, fmt.Sprintf(
			"%s was assigned and is gone from the catalogue. This is the promise breaking: "+
				"whatever matches on it now matches nothing, and the number is free to be "+
				"handed to a different finding. A rule that is gone keeps its entry with a "+
				"'retired:' reason. Put %s back.", id, id))
	}
	for _, item := range unregistered {
		problems = append(problems, fmt.Sprintf(
			"%s is in the catalogue and not in %s. Run 'just generate': the register is what "+
				"proves the identifier was never withdrawn later.", item, registerPath))
	}

	// The generated map is what a user actually receives.
	var wrong []string
	for _, e := range entries {
		if e.Retired != "" {
			continue
		}
		got, ok := generated[e.Rule]
		switch {
		case !ok:
			wrong = append(wrong, fmt.Sprintf("%s carries no identifier in %s", e.Rule, generatedPath))
		case got != e.ID:
			wrong = append(wrong, fmt.Sprintf("%s carries %s in %s and %s in the catalogue",
				e.Rule, got, generatedPath, e.ID))
		}
	}
	for rule, id := range generated {
		if _, ok := live[rule]; !ok {
			wrong = append(wrong, fmt.Sprintf("%s carries %s in %s and is not a live catalogue entry",
				rule, id, generatedPath))
		}
	}
	sort.Strings(wrong)
	for _, item := range wrong {
		problems = append(problems, item+
			". The identifier a user receives is not the one the documentation shows. "+
			"Run 'just generate'.")
	}

	return problems, len(live), nil
}

func readCatalog(path string) ([]entry, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c catalog
	if err := yaml.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return c.Findings, nil
}

func readRegister(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r register
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make([]string, 0, len(r.Assigned))
	for _, a := range r.Assigned {
		out = append(out, a.ID)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no identifiers, so this check is measuring nothing", path)
	}
	return out, nil
}

// declaredRules returns every rule the linter can report, mapped to the Go
// identifier that declares it.
//
// Parsed rather than grepped, for the same reason errcheck parses: a grep for
// a rule name matches the catalogue, the documentation and every test fixture
// that mentions it, so it would report everything as declared and prove
// nothing. A constant is a Rule when its declared type says so.
//
// The whole package rather than the one file the rules happen to live in
// today. Reading lint.go alone would report every rule declared in a new file
// beside it as a catalogue entry for a rule that does not exist, which is a
// wrong answer in the direction that gets a check deleted for being noisy.
func declaredRules(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// The generated file holds the rule names as map keys rather than as
		// Rule constants, so it declares nothing and reading it would only
		// check the generator against itself.
		if name == "findings.gen.go" {
			continue
		}
		if err := rulesInFile(filepath.Join(dir, name), out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func rulesInFile(path string, out map[string]string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Rule" {
			return true
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				continue
			}
			out[value] = name.Name
		}
		return true
	})
	return nil
}

// generatedIDs reads the rule to identifier map out of the generated file.
//
// The generated file is the only one of the three that a running binary
// consults, so it is the one a user's finding actually comes from. Checking the
// catalogue against the source and never against the generated map would leave
// the gap where it matters: a stale generated file ships identifiers the
// documentation does not list.
func generatedIDs(path string) (map[string]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) == 0 || spec.Names[0].Name != "findingIDs" {
			return true
		}
		for _, value := range spec.Values {
			lit, ok := value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, kerr := literal(kv.Key)
				id, verr := literal(kv.Value)
				if kerr != nil || verr != nil {
					continue
				}
				out[key] = id
			}
		}
		return false
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("no findingIDs entries in %s; has the generator changed shape?", path)
	}
	return out, nil
}

// literal unquotes a string literal, with or without a conversion around it.
func literal(e ast.Expr) (string, error) {
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		e = call.Args[0]
	}
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("not a string literal")
	}
	return strconv.Unquote(lit.Value)
}

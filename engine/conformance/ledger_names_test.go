package conformance_test

// The ledger is keyed by provider name, and nothing checked the name.
//
// TestEveryCopyOnWriteDeclarationHasARecordedVerdict matches an entry to a
// declaration through the entry's evidence DIRECTORY, so the map key never
// takes part in it. A mutation that renamed the rds entry's key survived that
// test, and so would a typo: an entry keyed "rsd" still covers ee/engine/db/rds
// by directory, every sweep stays green, and PublishableClaim("rds") then finds
// no entry at all and publishes the default instead of the recorded verdict.
// A ledger entry naming a provider that does not exist is the same defect as a
// dead link, and it is invisible to every other check here.
//
// So this binds the key to the provider it describes, both ways:
//
//   - FROM THE LEDGER. Each key's evidence directory is named after the key,
//     the provider in that directory declares exactly that name, and the name
//     is one the product registers: a DBProvider constant in engine/pkg/schema
//     for a built in provider, or a package ee/engine/db/managed.Register calls
//     for an enterprise one.
//   - FROM THE PRODUCT. Every registered provider whose package declares a
//     copy on write value has an entry under its own name, not merely an entry
//     somewhere that happens to point at its directory.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/conformance"
)

// builtinProviderDir and managedProviderDir are where a registered provider's
// package lives, by name.
func builtinProviderDir(name string) string { return filepath.Join("engine", "internal", "db", name) }
func managedProviderDir(name string) string { return filepath.Join("ee", "engine", "db", name) }

// registeredProviders returns every provider name the product registers, each
// mapped to the directory its package lives in.
//
// Read from the source rather than from a list here, because a list here is
// the failure this file exists to catch, one level further in.
func registeredProviders(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}

	// Built in: the DBProvider constants the engine's own switch uses.
	schema := filepath.Join(root, "engine", "pkg", "schema", "manifest.go")
	file, err := parser.ParseFile(token.NewFileSet(), schema, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed, so NO built in provider was read: %v", schema, err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "DBProvider" {
				continue
			}
			if lit, ok := vs.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				name, _ := strconv.Unquote(lit.Value)
				out[name] = builtinProviderDir(name)
			}
		}
	}

	// Enterprise: every package managed.Register calls Register on. The
	// directory is the import path's last element, and the name is whatever
	// that package declares, which providerName reads.
	managed := filepath.Join(root, "ee", "engine", "db", "managed", "managed.go")
	file, err = parser.ParseFile(token.NewFileSet(), managed, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed, so NO enterprise provider was read: %v", managed, err)
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		imports[filepath.Base(path)] = path
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Register" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path, ok := imports[pkg.Name]
		if !ok || !strings.Contains(path, "/ee/engine/db/") {
			return true
		}
		dir := managedProviderDir(filepath.Base(path))
		if name := providerName(t, filepath.Join(root, dir)); name != "" {
			out[name] = dir
		}
		return true
	})
	return out
}

// providerName reads the name a provider package declares: the string its
// Name method returns, either as a literal or through a package constant.
// It answers the empty string when no such method exists.
func providerName(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s could not be read, so its provider name was NOT checked: %v", dir, err)
	}
	consts := map[string]string{}
	var returned []ast.Expr
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed, so its provider name was NOT checked: %v", path, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, id := range vs.Names {
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								consts[id.Name], _ = strconv.Unquote(lit.Value)
							}
						}
					}
				}
			case *ast.FuncDecl:
				// The provider's own Name method: a receiver of type Provider,
				// which is what every database provider here is called, and
				// not the registration's socket type, which has a Name too.
				if d.Name.Name != "Name" || d.Recv == nil || d.Body == nil || len(d.Body.List) != 1 {
					continue
				}
				if !receiverIs(d.Recv, "Provider") {
					continue
				}
				if ret, ok := d.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
					returned = append(returned, ret.Results[0])
				}
			}
		}
	}
	for _, r := range returned {
		switch v := r.(type) {
		case *ast.BasicLit:
			name, _ := strconv.Unquote(v.Value)
			return name
		case *ast.Ident:
			return consts[v.Name]
		}
	}
	return ""
}

func receiverIs(recv *ast.FieldList, typeName string) bool {
	if len(recv.List) != 1 {
		return false
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == typeName
}

// ledgerNameProblems is the check, separated from the test so the controls
// below can point it at a ledger that is wrong on purpose.
func ledgerNameProblems(t *testing.T, root string, ledger map[string]conformance.LedgerEntry,
	registered map[string]string, declaring []string) []string {
	t.Helper()
	var problems []string
	for key, e := range ledger {
		dir := filepath.Dir(filepath.FromSlash(e.Evidence))
		if filepath.Base(dir) != key {
			problems = append(problems, "the ledger entry "+strconv.Quote(key)+" names its evidence in "+
				filepath.ToSlash(dir)+", which is another provider's directory")
		}
		regDir, ok := registered[key]
		if !ok {
			problems = append(problems, "the ledger entry "+strconv.Quote(key)+" names no provider the "+
				"product registers, so PublishableClaim for the real provider finds nothing and "+
				"publishes the default instead of this verdict")
			continue
		}
		if filepath.ToSlash(regDir) != filepath.ToSlash(dir) {
			problems = append(problems, "the ledger entry "+strconv.Quote(key)+" points at "+
				filepath.ToSlash(dir)+" and the registered provider of that name lives in "+
				filepath.ToSlash(regDir))
		}
		if name := providerName(t, filepath.Join(root, regDir)); name != key {
			problems = append(problems, "the ledger entry "+strconv.Quote(key)+" describes the package in "+
				filepath.ToSlash(regDir)+", whose provider declares the name "+strconv.Quote(name))
		}
	}
	byDir := map[string]string{}
	for name, dir := range registered {
		byDir[filepath.ToSlash(dir)] = name
	}
	for _, dir := range declaring {
		name, ok := byDir[filepath.ToSlash(dir)]
		if !ok {
			continue // not a registered provider; the declaration sweep owns that case
		}
		if _, ok := ledger[name]; !ok {
			problems = append(problems, filepath.ToSlash(dir)+" is the registered provider "+
				strconv.Quote(name)+", it declares a copy on write value, and the ledger has no "+
				"entry under that name")
		}
	}
	sort.Strings(problems)
	return problems
}

func TestEveryLedgerKeyIsTheProviderItDescribes(t *testing.T) {
	root := repoRoot(t)
	registered := registeredProviders(t, root)
	// A control before it is a source of cases: an enumeration that read
	// nothing would make every comparison below pass.
	for _, known := range []string{"docker", "neon", "supabase", "dblab", "pgurl", "aurora", "rds"} {
		if _, ok := registered[known]; !ok {
			t.Fatalf("the registered providers read as %v, which is missing %q, so the enumeration is "+
				"broken and this test would check less than it says", registered, known)
		}
	}
	declaring := declaringDirs(t, root)
	if len(declaring) == 0 {
		t.Fatal("no provider declares a copy on write value, so the reverse direction checked nothing")
	}
	for _, p := range ledgerNameProblems(t, root, conformance.CopyOnWriteLedger, registered, declaring) {
		t.Error(p)
	}
	t.Logf("%d ledger entries each name the registered provider their evidence belongs to", len(conformance.CopyOnWriteLedger))
}

// The check has to be able to say no, in both directions and for both ways a
// key can be wrong.
func TestTheLedgerNameCheckRefusesAMistypedKeyAndAMisplacedEvidence(t *testing.T) {
	root := repoRoot(t)
	registered := registeredProviders(t, root)
	declaring := declaringDirs(t, root)

	copyOf := func() map[string]conformance.LedgerEntry {
		out := map[string]conformance.LedgerEntry{}
		for k, v := range conformance.CopyOnWriteLedger {
			out[k] = v
		}
		return out
	}

	mistyped := copyOf()
	entry, ok := mistyped["rds"]
	if !ok {
		t.Fatal("the ledger has no rds entry to mistype, so this control would test nothing")
	}
	delete(mistyped, "rds")
	mistyped["rsd"] = entry
	problems := strings.Join(ledgerNameProblems(t, root, mistyped, registered, declaring), "\n")
	for _, want := range []string{`"rsd" names no provider`, `registered provider "rds"`} {
		if !strings.Contains(problems, want) {
			t.Errorf("a ledger keyed rsd for the rds provider was not refused with %q:\n%s", want, problems)
		}
	}

	swapped := copyOf()
	rds, aurora := swapped["rds"], swapped["aurora"]
	rds.Evidence, aurora.Evidence = aurora.Evidence, rds.Evidence
	swapped["rds"], swapped["aurora"] = rds, aurora
	problems = strings.Join(ledgerNameProblems(t, root, swapped, registered, declaring), "\n")
	for _, want := range []string{`"rds" names its evidence in ee/engine/db/aurora`, `"aurora" names its evidence in ee/engine/db/rds`} {
		if !strings.Contains(problems, want) {
			t.Errorf("swapped evidence paths were not refused with %q:\n%s", want, problems)
		}
	}
}

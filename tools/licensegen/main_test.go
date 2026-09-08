package main

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
)

// verifierSource is the file that declares what a license may permit.
const verifierSource = "../../ee/engine/license/license.go"

// TestKnownFeaturesMatchTheVerifier holds the copy to the original.
//
// knownFeatures is a copy, because this MIT tool must not import the
// enterprise-licensed package that owns the real list. A copy with no gate is a
// copy that drifts, and drift here is silent in the direction that costs money:
// a feature added to the verifier and not to this list makes licensegen refuse
// a legitimate request, which somebody notices in a minute, while the reverse
// signs a license naming something no engine acts on.
func TestKnownFeaturesMatchTheVerifier(t *testing.T) {
	want := featuresDeclaredBy(t, verifierSource)
	if len(want) == 0 {
		t.Fatalf("%s declared no Feature constants, so this test is reading the wrong file "+
			"or the constants moved. Point it at the file that declares them", verifierSource)
	}

	got := append([]string(nil), knownFeatures...)
	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("knownFeatures and the verifier's Feature constants differ.\n"+
			"licensegen has: %s\n%s has: %s\n"+
			"Update knownFeatures in main.go. Signing a feature the engine does not carry "+
			"produces a license that verifies and permits nothing.",
			strings.Join(got, ", "), verifierSource, strings.Join(want, ", "))
	}
}

// featuresDeclaredBy reads the string values of the Feature constants.
//
// Parsed rather than grepped, so that a constant inside a comment or a string
// cannot be mistaken for a declaration, and so that renaming the type is a
// compile-shaped failure here rather than an empty list that silently passes.
func featuresDeclaredBy(t *testing.T, path string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatalf("the verifier's source is not where this test expects it: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := value.Type.(*ast.Ident)
			if !ok || ident.Name != "Feature" {
				continue
			}
			for _, v := range value.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("a Feature constant in %s is not a plain string: %s", path, lit.Value)
				}
				out = append(out, unquoted)
			}
		}
	}
	return out
}

// TestNotShippedMatchesTheVerifier holds this tool's second copy to its
// original, the same way TestKnownFeaturesMatchTheVerifier holds the first.
//
// Drift here is silent in the expensive direction. A feature marked unshipped
// in license.go and not here signs cleanly and produces the license this whole
// mechanism exists to prevent; the reverse refuses a request for something that
// does ship, which somebody notices within a minute of trying it.
func TestNotShippedMatchesTheVerifier(t *testing.T) {
	want := notShippedDeclaredBy(t, verifierSource)
	if len(want) == 0 {
		t.Fatalf("%s marks nothing unshipped, so this test and the refusal it guards "+
			"are both proving nothing. If every feature now ships, delete the refusal "+
			"in checkFeatures rather than leaving a check that cannot say no", verifierSource)
	}

	got := append([]string(nil), notShipped...)
	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("notShipped and the verifier's unshipped set differ.\n"+
			"licensegen has: %s\n%s has: %s\n"+
			"Update notShipped in main.go. A feature the verifier refuses to permit and "+
			"this tool will still sign is a license sold for a capability that does nothing.",
			strings.Join(got, ", "), verifierSource, strings.Join(want, ", "))
	}

	// Every unshipped name is also a known one. A name in only the unshipped
	// list would be refused by the check above it anyway, so the entry would be
	// dead and would read as a feature being withheld rather than absent.
	known := map[string]bool{}
	for _, f := range knownFeatures {
		known[f] = true
	}
	for _, f := range got {
		if !known[f] {
			t.Errorf("%s is marked unshipped and is not a feature at all", f)
		}
	}
}

// TestIssueRefusesAFeatureNothingEnforces points the refusal at the exact case
// that produced it.
//
// Both directions in one test would let the first require hide the second, so
// the shipped case and the unshipped case are separate assertions on separate
// inputs and each is checked for the sentence it should produce.
func TestIssueRefusesAFeatureNothingEnforces(t *testing.T) {
	if err := checkFeatures([]string{"sso", "scim"}); err != nil {
		t.Fatalf("a request naming only features that ship was refused: %v", err)
	}

	for _, absent := range notShipped {
		err := checkFeatures([]string{"sso", absent})
		if err == nil {
			t.Fatalf("a request naming %s was accepted, and nothing in the product enforces it", absent)
		}
		if !strings.Contains(err.Error(), absent) {
			t.Errorf("the refusal for %s does not name it: %v", absent, err)
		}
		if !strings.Contains(err.Error(), "nothing in this product enforces") {
			t.Errorf("the refusal for %s reads as an unknown name rather than an absent "+
				"capability, and those are different conversations: %v", absent, err)
		}
	}
}

// notShippedDeclaredBy reads the keys of the notShipped map out of license.go.
//
// The keys are constant identifiers rather than strings, so the constants are
// read first and the identifiers resolved through them. Parsed rather than
// grepped for the same reason featuresDeclaredBy is: the names appear in prose
// in that file, and a grep would find the paragraph explaining the map as
// readily as the map.
func notShippedDeclaredBy(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	// identifier -> string value, for every Feature constant.
	values := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := value.Type.(*ast.Ident)
			if !ok || ident.Name != "Feature" || len(value.Names) != len(value.Values) {
				continue
			}
			for i, name := range value.Names {
				lit, ok := value.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("a Feature constant in %s is not a plain string: %s", path, lit.Value)
				}
				values[name.Name] = unquoted
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("no Feature constants were read out of %s, so the map below cannot be resolved", path)
	}

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "notShipped" {
				continue
			}
			for _, v := range value.Values {
				composite, ok := v.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, elt := range composite.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					name, ok := values[key.Name]
					if !ok {
						t.Fatalf("notShipped names %s, which is not a Feature constant", key.Name)
					}
					out = append(out, name)
				}
			}
		}
	}
	return out
}

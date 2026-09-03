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

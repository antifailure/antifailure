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
	want := featureMapDeclaredBy(t, verifierSource, "notShipped")
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

// TestUnenforcedMatchesTheVerifier holds this tool's third copy to its
// original, the same way the two tests above hold the first and the second.
//
// Drift here is silent in the direction that costs a conversation rather than a
// refund, which is why the guard is a warning and the test is not. A feature
// recorded as unenforced in license.go and missing here signs with no warning
// at all, so the person issuing the key learns what they sold from the customer
// instead of from the receipt.
func TestUnenforcedMatchesTheVerifier(t *testing.T) {
	want := featureMapDeclaredBy(t, verifierSource, "unenforced")
	if len(want) == 0 {
		t.Fatalf("%s records nothing as unenforced, so this test and the warning it guards "+
			"are both proving nothing. If every shipped feature is now gated somewhere, "+
			"delete warnUnenforced rather than leaving a warning that cannot fire",
			verifierSource)
	}

	got := append([]string(nil), unenforced...)
	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("unenforced and the verifier's unenforced set differ.\n"+
			"licensegen has: %s\n%s has: %s\n"+
			"Update unenforced in main.go. A feature the verifier records as gated "+
			"nowhere and this tool signs in silence is a capability sold as though the "+
			"licence granted it.",
			strings.Join(got, ", "), verifierSource, strings.Join(want, ", "))
	}

	// Every unenforced name is also a known one, for the reason the unshipped
	// check next door gives: a name in only this list would be refused as
	// unknown before the warning could ever print, so the entry would be dead.
	known := map[string]bool{}
	for _, f := range knownFeatures {
		known[f] = true
	}
	for _, f := range got {
		if !known[f] {
			t.Errorf("%s is recorded as unenforced and is not a feature at all", f)
		}
	}

	// And the two lists are disjoint. A name in both would be refused outright
	// by checkFeatures, so its warning could never print, and the entry would
	// say the product ships something it also says it does not.
	absent := map[string]bool{}
	for _, f := range notShipped {
		absent[f] = true
	}
	for _, f := range got {
		if absent[f] {
			t.Errorf("%s is recorded as both unshipped and unenforced, which are "+
				"different answers and cannot both be true", f)
		}
	}
}

// TestTheReceiptWarnsAboutAFeatureNothingGates points the warning at the exact
// case that produced it, and at a case that must stay silent.
//
// Separate inputs and separate assertions, so that the silent case cannot be
// hidden by the loud one passing.
func TestTheReceiptWarnsAboutAFeatureNothingGates(t *testing.T) {
	// Named, so the reason reaches whoever reads the receipt.
	//
	// rbac rather than air_gapped. This fixture named air_gapped until the air
	// gapped mode was built and the feature left the unenforced list, at which
	// point the test failed for the best possible reason: the thing it was
	// asserting nobody gated had been gated. The fixture has to be a feature
	// that is genuinely still unenforced or the test asserts nothing, so it
	// follows the list rather than naming a feature of its own.
	warning := warnUnenforced([]string{"sso", "rbac"})
	if !strings.Contains(warning, "rbac") {
		t.Errorf("a licence naming rbac produced no warning naming it: %q", warning)
	}
	if !strings.Contains(warning, "gates nowhere") {
		t.Errorf("the warning does not say what is wrong: %q", warning)
	}
	if strings.Contains(warning, "sso") {
		t.Errorf("the warning names sso, which is gated at a real site: %q", warning)
	}

	// And silent when there is nothing to say. A warning on every licence is a
	// warning nobody reads, which would make the case above invisible.
	if quiet := warnUnenforced([]string{"sso", "scim"}); quiet != "" {
		t.Errorf("a licence naming only gated features produced a warning: %q", quiet)
	}
	if none := warnUnenforced(nil); none != "" {
		t.Errorf("a licence naming no feature produced a warning: %q", none)
	}
}

// featureMapDeclaredBy reads the keys of a named Feature-keyed map out of
// license.go. Two maps are held to this tool's copies by it, notShipped and
// unenforced, and one parser for both is what stops the second from being held
// by a weaker check than the first.
//
// The keys are constant identifiers rather than strings, so the constants are
// read first and the identifiers resolved through them. Parsed rather than
// grepped for the same reason featuresDeclaredBy is: the names appear in prose
// in that file, and a grep would find the paragraph explaining the map as
// readily as the map.
//
// A map name that is not in the file returns nothing rather than failing, which
// would be a check that cannot say no. Every caller therefore fails on an empty
// result and says what an empty result would mean, because "the map is gone"
// and "the map is empty" have to be distinguishable here.
func featureMapDeclaredBy(t *testing.T, path, mapName string) []string {
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
			if !ok || len(value.Names) != 1 || value.Names[0].Name != mapName {
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
						t.Fatalf("%s names %s, which is not a Feature constant", mapName, key.Name)
					}
					out = append(out, name)
				}
			}
		}
	}
	return out
}

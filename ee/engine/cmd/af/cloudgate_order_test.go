// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package main_test

// The one thing that can say no about the order of the registrations in
// main.go.
//
// cloudgate.Wrap puts the managed cloud providers behind the cloud_database
// and cloud_runtime features by wrapping what is registered AT THE MOMENT IT
// RUNS. A provider registered after it is not wrapped, so it is not gated, so
// an unlicensed build serves it.
//
// Every other instrument in this repository is blind to that. The wrong order
// compiles. go vet is clean. Each registration still appears exactly once with
// its import, which is the rule a keep both merge resolution is checked
// against, so the resolution reviews as correct. The provider is really
// registered, so nothing that asserts registration fails. The defect is
// entirely in the ORDER of two statements, and order is the thing a diff, a
// compiler and a symbol count all agree to ignore.
//
// So this reads the syntax tree rather than the behaviour, deliberately: the
// behaviour it is protecting is "a licence is required", and a test that
// exercised that would be asserting the gate works rather than that the gate
// is reached. What goes wrong here is reachability, and position is what
// decides it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// mentionsTheRegistry reports whether a call hands extension.Default to
// something, either as `extension.Default.Add...(x)` or as a function taking
// the registry, which are the two shapes every registration in main.go uses.
func mentionsTheRegistry(call *ast.CallExpr) bool {
	isDefault := func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Default" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "extension"
	}
	for _, a := range call.Args {
		if isDefault(a) {
			return true
		}
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && isDefault(sel.X) {
		return true
	}
	return false
}

func TestTheCloudGateWrapsLast(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var wrap token.Pos
	var lastRegistration token.Pos
	var lastName string

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "cloudgate" && sel.Sel.Name == "Wrap" {
			wrap = call.Lparen
			// The gate takes the registry too, so it must not count as one of
			// the registrations it is being compared against.
			return true
		}
		if mentionsTheRegistry(call) && call.Lparen > lastRegistration {
			lastRegistration = call.Lparen
			lastName = types(fset, call)
		}
		return true
	})

	require.NotEqualf(t, token.NoPos, wrap,
		"main.go does not call cloudgate.Wrap at all, so nothing puts the managed cloud "+
			"providers behind their features and this test would otherwise pass by finding nothing")
	require.NotEqualf(t, token.NoPos, lastRegistration,
		"main.go registers nothing against extension.Default, which cannot be true while "+
			"providers ship, so this test is reading the wrong thing rather than passing")

	require.Lessf(t, int(lastRegistration), int(wrap),
		"%s is at line %d and cloudgate.Wrap is at line %d, so that registration happens AFTER "+
			"the licence gate and is never wrapped by it. The provider still works, still "+
			"appears once, still compiles and still vets, and it is served by a build with no "+
			"licence for it. Move the registration above the cloudgate.Wrap call.",
		lastName, fset.Position(lastRegistration).Line, fset.Position(wrap).Line)
}

// types renders the called function for the failure message, so the refusal
// names the registration that moved rather than only a line number.
func types(fset *token.FileSet, call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "a registration"
	}
	if x, ok := sel.X.(*ast.Ident); ok {
		return x.Name + "." + sel.Sel.Name
	}
	if inner, ok := sel.X.(*ast.SelectorExpr); ok {
		if id, ok := inner.X.(*ast.Ident); ok {
			return id.Name + "." + inner.Sel.Name + "." + sel.Sel.Name
		}
	}
	return sel.Sel.Name
}

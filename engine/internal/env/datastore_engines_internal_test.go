package env

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The switch in newDatastoreProvider and provider.BuiltInDatastoreEngines are
// two records of one fact, and they used to be three: the refusal message
// carried the sentence "The engines it can provide are: clickhouse" as a
// string literal, so a second provider would have landed with the refusal
// still naming one engine and detection still declining to propose the other.
//
// This reads the switch's own case labels out of the source and requires them
// to be the list. It is a structural check on purpose, because the drift it
// catches is structural: two lists that disagree. The behavioural half is
// datastore_up_live_test.go, which brings a real ClickHouse up, and the
// refusal case below, which is what a manifest naming an engine nothing serves
// actually produces.
func TestBuiltInDatastoreEnginesAreTheOnesTheSwitchAnswers(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "datastores.go", nil, 0)
	require.NoError(t, err)

	var cases []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "newDatastoreProvider" {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			sw, ok := inner.(*ast.SwitchStmt)
			if !ok || sw.Tag == nil {
				return true
			}
			sel, ok := sw.Tag.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Engine" {
				return true
			}
			for _, stmt := range sw.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					lit, ok := expr.(*ast.BasicLit)
					if ok && lit.Kind == token.STRING {
						cases = append(cases, lit.Value[1:len(lit.Value)-1])
					}
				}
			}
			return false
		})
		return false
	})

	require.NotEmpty(t, cases,
		"the switch on ds.Engine was not found, so this test measured nothing")

	want := append([]string(nil), provider.BuiltInDatastoreEngines()...)
	sort.Strings(want)
	sort.Strings(cases)
	require.Equal(t, want, cases,
		"the engines this build answers to and the engines it says it answers to disagree, "+
			"so af init either declines to propose a store it could provide or writes one af up refuses")
}

// An empty list is the failure this cannot be allowed to pass through. It
// would make the refusal say "The engines it can provide are: " and make
// detection propose nothing at all, and both would look like a working
// build with no datastore support rather than a broken list.
func TestBuiltInDatastoreEnginesIsNotEmpty(t *testing.T) {
	engines := provider.BuiltInDatastoreEngines()
	require.NotEmpty(t, engines)
	for _, e := range engines {
		require.True(t, provider.ProvidesDatastoreEngine(e),
			"%s is on the list and the predicate reading that list says no", e)
	}
	require.False(t, provider.ProvidesDatastoreEngine("redis"),
		"an engine nothing here brings up must not be claimed")
}

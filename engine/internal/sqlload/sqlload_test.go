package sqlload_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// The tests that need no server. Everything here is a decision made before a
// connection is opened: what a document means, what a name has to be unique
// about, and which refusals are refusals rather than silent drops.

func TestParseScript_ReadsTransactionsWeightsAndParameters(t *testing.T) {
	mix, description, err := sqlload.ParseScript([]byte(`
sql_workload: storefront
description: the read path a storefront runs
transactions:
  - transaction: read one order
    weight: 8
    statements:
      - label: order by id
        sql: SELECT id FROM orders WHERE id = $1
        params:
          - query: SELECT id FROM orders
  - transaction: search
    statements:
      - sql: SELECT id FROM orders WHERE status = $1 AND total > $2
        params:
          - text: {values: [paid, pending]}
          - int: {min: 1, max: 500}
`))
	require.NoError(t, err)
	require.Equal(t, "the read path a storefront runs", description)
	require.Equal(t, sqlload.SourceDeclared, mix.Source)
	require.Len(t, mix.Transactions, 2)

	first := mix.Transactions[0]
	require.Equal(t, "read one order", first.Name)
	require.Equal(t, 8.0, first.Weight)
	require.Equal(t, "order by id", first.Statements[0].Label)
	require.Equal(t, sqlload.ParamQuery, first.Statements[0].Params[0].Kind)
	require.False(t, first.Statements[0].Write)

	second := mix.Transactions[1]
	require.Equal(t, 1.0, second.Weight, "a transaction with no weight runs, at weight one")
	require.Equal(t, "SELECT id FROM orders WHERE status = $1 AND total > $2",
		second.Statements[0].Label, "a statement with no label is named by its own sql")
	require.Equal(t, sqlload.ParamText, second.Statements[0].Params[0].Kind)
	require.Equal(t, []string{"paid", "pending"}, second.Statements[0].Params[0].Values)
	require.Equal(t, sqlload.ParamInt, second.Statements[0].Params[1].Kind)
	require.Equal(t, int64(500), second.Statements[0].Params[1].Max)
}

func TestParseScript_AWriteIsRecordedAsOne(t *testing.T) {
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: checkout
transactions:
  - transaction: place an order
    statements:
      - {sql: "INSERT INTO orders (merchant_id) VALUES ($1)", params: [{int: {min: 1, max: 9}}]}
      - {sql: "SELECT currval('orders_id_seq')"}
`))
	require.NoError(t, err)
	require.True(t, mix.Transactions[0].Statements[0].Write)
	require.False(t, mix.Transactions[0].Statements[1].Write)
}

func TestParseScript_RefusesWhatWouldSilentlyChangeTheRun(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			// The whole reason the decoder is strict. A misspelled weight that
			// silently became one is a run whose report describes a mix nobody
			// declared.
			"an unknown key", `
sql_workload: x
transactions:
  - transaction: a
    wieght: 3
    statements: [{sql: "SELECT 1"}]`, "field wieght not found",
		},
		{"no name", `
transactions:
  - transaction: a
    statements: [{sql: "SELECT 1"}]`, "does not name itself"},
		{"no transactions", "sql_workload: x", "declares no transactions"},
		{"a transaction with no name", `
sql_workload: x
transactions:
  - statements: [{sql: "SELECT 1"}]`, "transaction 1 does not name itself"},
		{"a transaction with no statements", `
sql_workload: x
transactions:
  - transaction: a
    statements: []`, "declares no statements"},
		{"an empty statement", `
sql_workload: x
transactions:
  - transaction: a
    statements: [{sql: "  "}]`, "carries no sql"},
		{"two transactions with one name", `
sql_workload: x
transactions:
  - {transaction: a, statements: [{sql: "SELECT 1"}]}
  - {transaction: a, statements: [{sql: "SELECT 2"}]}`, "both called"},
		{"two statements with one label", `
sql_workload: x
transactions:
  - transaction: a
    statements:
      - {label: same, sql: "SELECT 1"}
      - {label: same, sql: "SELECT 2"}`, "both labelled"},
		{"a negative weight", `
sql_workload: x
transactions:
  - {transaction: a, weight: -1, statements: [{sql: "SELECT 1"}]}`, "negative weight"},
		{"a parameter that sets nothing", `
sql_workload: x
transactions:
  - transaction: a
    statements: [{sql: "SELECT $1", params: [{}]}]`, "exactly one of int, text or query"},
		{"a parameter that sets two", `
sql_workload: x
transactions:
  - transaction: a
    statements: [{sql: "SELECT $1", params: [{int: {min: 1, max: 2}, query: "SELECT 1"}]}]`,
			"exactly one of int, text or query"},
		{"a range that is inside out", `
sql_workload: x
transactions:
  - transaction: a
    statements: [{sql: "SELECT $1", params: [{int: {min: 9, max: 1}}]}]`, "max below its min"},
		{"a text parameter with no values", `
sql_workload: x
transactions:
  - transaction: a
    statements: [{sql: "SELECT $1", params: [{text: {values: []}}]}]`, "no values"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := sqlload.ParseScript([]byte(tc.doc))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSelect_RefusesANameThatMatchesNothing(t *testing.T) {
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: x
transactions:
  - {transaction: read, statements: [{sql: "SELECT 1"}]}
  - {transaction: write, statements: [{sql: "SELECT 2"}]}`))
	require.NoError(t, err)

	only, err := mix.Select([]string{"read"})
	require.NoError(t, err)
	require.Equal(t, []string{"read"}, only.Names())

	// A selection that matched nothing would send nothing and report a run
	// with no problems in it, which is the silent green the whole product
	// exists to stop.
	_, err = mix.Select([]string{"raed"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"raed"`)

	all, err := mix.Select(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"read", "write"}, all.Names())
}

func TestValidate_CatchesAMixAssembledInCode(t *testing.T) {
	cases := []struct {
		name string
		mix  sqlload.Mix
		want string
	}{
		{"nothing at all", sqlload.Mix{}, "holds no transactions"},
		{"a transaction with no name", sqlload.Mix{Transactions: []sqlload.Transaction{
			{Statements: []sqlload.Statement{{SQL: "SELECT 1"}}}}}, "has no name"},
		{"a transaction with no statements", sqlload.Mix{Transactions: []sqlload.Transaction{
			{Name: "a"}}}, "holds no statements"},
		{"a generated parameter naming no type", sqlload.Mix{Transactions: []sqlload.Transaction{
			{Name: "a", Statements: []sqlload.Statement{{SQL: "SELECT $1", Params: []sqlload.Param{
				{Kind: sqlload.ParamGenerated}}}}}}}, "names no type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mix.ValidateForTest()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestGeneratedTypes_AreTheOnesTheDocumentationPublishes reads the PAGE.
//
// Not a list written into this test, which would be a third copy agreeing with
// itself. A reference page naming a type this package refuses is how somebody
// spends an afternoon on a workload that was never going to run, and a page
// missing a type it supports is a feature nobody finds. The only thing that
// catches either is comparing the code against the published sentence.
//
// The sentence is one paragraph of prose rather than a table, deliberately,
// because that is how it reads best on the page. So this pulls the backticked
// type names out of the paragraph that names them, and refuses to run at all
// if it cannot find the paragraph, because an instrument that quietly compares
// against an empty set is the failure this repository names most often.
func TestGeneratedTypes_AreTheOnesTheDocumentationPublishes(t *testing.T) {
	types := sqlload.GeneratedTypesForTest()
	require.NotEmpty(t, types)

	const page = "../../../docs/src/content/docs/concepts/sql-workloads.md"
	body, err := os.ReadFile(page)
	require.NoErrorf(t, err, "the published page is not where this test looks for it: %s", page)

	// The paragraph, anchored on its opening words rather than on a line
	// number. It ends at the blank line after it.
	const opening = "Values can be generated for"
	start := strings.Index(string(body), opening)
	require.GreaterOrEqualf(t, start, 0,
		"%s no longer carries a paragraph beginning %q, so this test is reading nothing",
		page, opening)
	rest := string(body)[start:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}

	published := backticked.FindAllStringSubmatch(rest, -1)
	require.NotEmpty(t, published, "the paragraph names no types in backticks")
	names := make([]string, 0, len(published))
	for _, m := range published {
		names = append(names, m[1])
	}
	// The paragraph says "the two timestamp types" rather than spelling both,
	// because spelling them is unreadable. Both are asserted separately below.
	names = append(names, "timestamp with time zone", "timestamp without time zone")
	sort.Strings(names)

	require.Equal(t, types, names,
		"the published page and this package disagree about which Postgres types a derived "+
			"parameter can be filled with. Either the page promises a type that is refused, or "+
			"it is missing one that works and nobody will find it.")

	// The types deliberately absent. Each is one where a generated value is
	// legal and meaningless, so the statement is refused by name instead, and
	// the page says so in the sentence after the list.
	for _, absent := range []string{"jsonb", "bytea", "inet", "tsvector"} {
		require.NotContains(t, types, absent,
			"a generated %s would be a legal value that means nothing, so it has to be refused by name", absent)
	}
	require.Contains(t, string(body), "refused by name",
		"the page no longer says what happens to a type that cannot be generated")
}

// backticked pulls `a type name` out of a sentence.
var backticked = regexp.MustCompile("`([a-z ]+)`")

// TestALabelIsShortEnoughToReadAndLongEnoughToTellTwoStatementsApart.
func TestALabelIsShortEnoughToReadAndLongEnoughToTellTwoStatementsApart(t *testing.T) {
	long := "SELECT " + strings.Repeat("column_with_a_long_name, ", 20) + "id FROM orders WHERE id = $1"
	mix, _, err := sqlload.ParseScript([]byte("sql_workload: x\ntransactions:\n" +
		"  - {transaction: a, statements: [{sql: \"" + long + "\"}]}"))
	require.NoError(t, err)
	label := mix.Transactions[0].Statements[0].Label
	require.LessOrEqual(t, len(label), 80)
	require.True(t, strings.HasSuffix(label, "..."))
	require.True(t, strings.HasPrefix(label, "SELECT column_with_a_long_name"))
}

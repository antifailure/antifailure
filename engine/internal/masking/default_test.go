package masking_test

// What happens to a column nobody wrote a rule for.
//
// This is the safety-critical default in the product and it was documented one
// way and implemented the other: `transforms.md` says nullify "is the default
// for unclassified free text, because a column nobody has confirmed is safe is
// not safe", the shipped example's README says the same in its own words, and
// the planner left such a column with no transform at all. `BuildPlan` skips a
// column with no transform, so a `notes` column nobody had classified was
// copied verbatim into every environment branched from that golden.
//
// It was survivable because the plan reported the column and the verification
// scan reads every column back looking for anything that still parses as an
// address or a card. Both of those are backstops. A default runs every time
// and a report is read once, which is why this is a default now.
//
// No Docker here on purpose. The rule is about classification, and running it
// against a real database would make the check about a fixture.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

// find returns the assignment for one column, failing if it is missing.
func find(t *testing.T, assignments []masking.Assignment, table, column string) masking.Assignment {
	t.Helper()
	for _, a := range assignments {
		if a.Table.Name == table && a.Column.Name == column {
			return a
		}
	}
	t.Fatalf("no assignment for %s.%s", table, column)
	return masking.Assignment{}
}

func assign(t *testing.T, columns ...masking.ColumnInfo) []masking.Assignment {
	t.Helper()
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	return rules.Assign([]masking.Table{
		{Schema: "public", Name: "orders", Columns: columns, PrimaryKey: []string{"id"}},
	})
}

func TestAssign_EmptiesAFreeTextColumnNobodyClassified(t *testing.T) {
	assignments := assign(t,
		masking.ColumnInfo{Name: "id", Type: "uuid"},
		masking.ColumnInfo{Name: "internal_notes", Type: "text", Nullable: true},
	)

	notes := find(t, assignments, "orders", "internal_notes")
	require.Equal(t, "nullify", notes.Transform,
		"a text column no rule names was left unmasked, so whatever somebody typed into it "+
			"is copied into every preview environment")
	require.True(t, notes.Masked(), "the plan would skip it")
	require.True(t, notes.Unmatched, "it must still be reported as a question")
	require.Contains(t, notes.Why, "nobody has classified")
}

func TestAssign_LeavesStructuralColumnsAlone(t *testing.T) {
	// The other half of the same decision. Emptying a bigint called quantity
	// would break every environment for nothing, and a fail-closed default
	// that breaks everything is a default somebody turns off.
	assignments := assign(t,
		masking.ColumnInfo{Name: "id", Type: "uuid", Nullable: true},
		masking.ColumnInfo{Name: "quantity", Type: "bigint", Nullable: true},
		masking.ColumnInfo{Name: "total_cents", Type: "integer", Nullable: true},
		masking.ColumnInfo{Name: "placed_at", Type: "timestamp with time zone", Nullable: true},
		masking.ColumnInfo{Name: "shipped", Type: "boolean", Nullable: true},
	)

	for _, column := range []string{"id", "quantity", "total_cents", "placed_at", "shipped"} {
		a := find(t, assignments, "orders", column)
		require.Empty(t, a.Transform, "%s was masked and holds nothing about a person", column)
		require.False(t, a.Unmatched, "%s is not a question anybody has to answer", column)
	}
}

func TestAssign_DoesNotEmptyAColumnTheDatabaseComputes(t *testing.T) {
	// A generated column cannot be written to at all, so assigning it the
	// default turns one unclassified column into a plan that refuses to run:
	// a fail-closed default that closes the whole environment rather than the
	// one column. It is reported instead, with the reason.
	assignments := assign(t,
		masking.ColumnInfo{Name: "id", Type: "uuid"},
		masking.ColumnInfo{Name: "display", Type: "text", Nullable: true, Generated: true},
	)

	display := find(t, assignments, "orders", "display")
	require.Empty(t, display.Transform, "a generated column was given a transform it cannot take")
	require.Empty(t, display.Problem, "an unclassified generated column made the plan unrunnable")
	require.True(t, display.Unmatched, "it must still be listed as a question")
	require.Contains(t, display.Why, "computes it")
}

func TestAssign_SaysSoWhenItCannotEmptyANotNullColumn(t *testing.T) {
	// The one case where an unclassified column is still copied as it is. It
	// cannot hold null, so the default cannot apply, and the plan says that in
	// words rather than leaving the column silently absent from it.
	assignments := assign(t,
		masking.ColumnInfo{Name: "id", Type: "uuid"},
		masking.ColumnInfo{Name: "reference", Type: "text", Nullable: false},
	)

	reference := find(t, assignments, "orders", "reference")
	require.Empty(t, reference.Transform)
	require.Empty(t, reference.Problem, "an unclassified not-null column made the plan unrunnable")
	require.True(t, reference.Unmatched)
	require.Contains(t, reference.Why, "cannot hold null")
	require.Contains(t, reference.Why, "verification scan")
}

func TestAssign_AWrittenRuleStillBeatsTheDefault(t *testing.T) {
	// The default must not overwrite a decision somebody made, including the
	// decision that a column is fine as it is. `preserve` and "nobody looked"
	// are different facts and the whole design rests on them staying different.
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "orders", Column: "internal_notes", Transform: "preserve",
			Why: "reviewed: it holds a currency code"},
	})
	require.NoError(t, err)

	assignments := rules.Assign([]masking.Table{{
		Schema: "public", Name: "orders", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "uuid"},
			{Name: "internal_notes", Type: "text", Nullable: true},
		},
	}})

	notes := find(t, assignments, "orders", "internal_notes")
	require.Equal(t, "preserve", notes.Transform)
	require.False(t, notes.Unmatched, "a column somebody classified was reported as unclassified")
	require.Contains(t, notes.Why, "currency code")
}

func TestUnclassified_ListsWhatWasEmptiedByDefaultAsWellAsWhatCouldNotBe(t *testing.T) {
	// Being emptied is not the same as being understood. A column that should
	// have been free_text so a layout still gets three paragraphs, or preserve
	// because it holds a currency code, is a rule somebody has to write, and
	// this list is where they find out.
	assignments := assign(t,
		masking.ColumnInfo{Name: "id", Type: "uuid"},
		masking.ColumnInfo{Name: "internal_notes", Type: "text", Nullable: true},
		masking.ColumnInfo{Name: "reference", Type: "text", Nullable: false},
		masking.ColumnInfo{Name: "quantity", Type: "bigint", Nullable: true},
	)

	var named []string
	for _, a := range masking.Unclassified(assignments) {
		named = append(named, a.Column.Name)
	}
	require.ElementsMatch(t, []string{"internal_notes", "reference"}, named,
		"the questions a plan asks are wrong: a masked-by-default column must still be asked "+
			"about, and a structural column must not be")
}

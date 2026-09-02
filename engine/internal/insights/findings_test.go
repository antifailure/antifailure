package insights

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A finding carries an identifier that does not move between releases, and the
// rule name beside it is free to. That is only true while every rule has one,
// so this walks the generated catalogue against the rules rather than trusting
// either.
//
// tools/lintcheck is the gate that holds the other half: it reads the Rule
// constants out of the source, so a rule declared and left out of AllRules
// cannot hide from both checks at once.

func TestEveryRuleHasAnIdentifierAndATitle(t *testing.T) {
	t.Parallel()
	for _, r := range AllRules() {
		require.NotEmpty(t, r.ID(),
			"%s has no identifier, so a filter matching on one silently stops seeing it", r)
		require.NotEqual(t, string(r), r.Title(),
			"%s falls back to its own name for a title", r)
	}
}

// An identifier means one finding. Two rules sharing one makes it useless for
// the single thing it exists for.
func TestIdentifiersAreUnique(t *testing.T) {
	t.Parallel()
	seen := map[FindingID]Rule{}
	for _, r := range AllRules() {
		first, dup := seen[r.ID()]
		require.False(t, dup, "%s and %s both carry %s", first, r, r.ID())
		seen[r.ID()] = r
	}
}

// AllRules is what the documentation order comes from and what the tests above
// walk. A rule in the catalogue and missing from it would be identified,
// reportable, and invisible to every check keyed on this list.
func TestEveryRuleIsInAllRules(t *testing.T) {
	t.Parallel()
	listed := map[Rule]bool{}
	for _, r := range AllRules() {
		listed[r] = true
	}
	for r := range findingIDs {
		require.True(t, listed[r], "%s is in the catalogue and not in AllRules", r)
	}
	require.Len(t, AllRules(), len(findingIDs))
}

// Every identifier the catalogue has ever assigned is registered, retired ones
// included, so that nothing can hand the same number out twice.
func TestAssignedIdentifiersCoverTheLiveOnes(t *testing.T) {
	t.Parallel()
	assigned := map[FindingID]bool{}
	for _, id := range AssignedFindingIDs() {
		assigned[id] = true
	}
	require.NotEmpty(t, assigned)
	for _, r := range AllRules() {
		require.True(t, assigned[r.ID()], "%s carries %s and it was never assigned", r, r.ID())
	}
}

// The identifier is stamped by Lint rather than by each rule, so this proves
// the stamping happens rather than proving the map is full.
func TestALintedFindingCarriesItsIdentifier(t *testing.T) {
	t.Parallel()
	schema := Schema{
		Rows:        map[string]int64{"orders": 40_000_000},
		Columns:     map[string]string{"orders.total": "int4"},
		LockTimeout: "0",
	}
	findings := Lint(Split("001_change.sql",
		"ALTER TABLE orders ALTER COLUMN total TYPE bigint;"), schema, 1_000_000)
	require.NotEmpty(t, findings)
	for _, f := range findings {
		require.NotEmpty(t, f.ID, "%s reached a caller with no identifier", f.Rule)
		require.Equal(t, f.Rule.ID(), f.ID)
	}
}

// The identifier is only useful to a person if a person sees it. The report is
// where somebody decides to suppress a finding, and a report that shows the
// title alone teaches them to match on the title.
func TestTheReportShowsTheIdentifier(t *testing.T) {
	t.Parallel()
	r := Rehearsal{
		Pending: []Migration{{Name: "001_change.sql"}},
		Lint: []LintFinding{{
			ID: RuleDropTable.ID(), Rule: RuleDropTable,
			Migration: "001_change.sql", Statement: "DROP TABLE orders;",
			Detail: "The rows are gone at commit.", Fix: "Rename it out of the way first.",
		}},
	}
	out := r.Explain()
	require.Contains(t, out, string(RuleDropTable.ID()))
	require.Contains(t, out, RuleDropTable.Title())
}

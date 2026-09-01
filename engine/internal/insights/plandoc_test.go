package insights_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// The manifest schema's description of plan_regression has to name every way a
// plan can get worse, not some of them.
//
// It named two of three. cost_increase appeared nowhere a user could read: not
// in the schema, not in the generated reference page, not in the verdicts
// guide. A promise phrase followed by a gloss of part of a set is worse than
// no gloss at all, because a reader who sees a colon and two items reasonably
// concludes the two are the list, and then cannot work out why their build
// failed on the third.
//
// Held here rather than by a prose scan, because a scan for phrases in English
// finds things that are not defects and gets deleted. This is keyed on a
// closed Go set and a phrase per member that the reports already use, so it
// can only fire on the thing it is about.
//
// This is half of the guard and tools/constcheck is the other, and the two
// were chosen because their blind spots are opposite. constcheck parses the
// const block through go/ast and holds the stated count, so it sees a fourth
// constant that nobody added to PlanChanges, which nothing here can: Go cannot
// enumerate the constants of a string type at run time. What it cannot see is
// a rewrite that drops the count from the sentence, because then there is no
// counted sentence left to hold. This test sees exactly that, and misses what
// constcheck catches.
//
// The cost is that the prose has to use the same phrases as planTitle, which
// is coupling, and it is deliberate and one directional: these are the same
// sentence shown in a schema and in a report, and improving the wording in one
// place should improve it in the other rather than let them drift. The failure
// message says which file to change.
//
// When to delete this file rather than argue with it: if that coupling ever
// blocks an edit somebody actually wanted to make, the count in constcheck is
// doing the load bearing work and this can go. Not before. "constcheck landed"
// is not the trigger, because the two cover different holes; "the wording had
// to stay wrong to keep a test green" is.
func TestSchemaDescribesEveryPlanChange(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "manifest.v1.json"))
	require.NoError(t, err)

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc))
	desc := findDescription(doc, "plan_regression")
	require.NotEmpty(t, desc, "the schema has no description for plan_regression")

	require.NotEmpty(t, insights.PlanChanges)
	for _, kind := range insights.PlanChanges {
		title := insights.PlanTitle(kind)
		require.NotEmptyf(t, title,
			"%s has no title, so a report would describe it as nothing at all", kind)
		require.Containsf(t, desc, title,
			"the schema's description of plan_regression does not mention %s (%q).\n"+
				"Add it to schemas/manifest.v1.json and run 'go run ./tools/schemadoc .',\n"+
				"and check docs/src/content/docs/concepts/verdicts.md, which is written by hand.\n"+
				"Description is:\n%s", kind, title, desc)
	}
}

// findDescription returns the description of the first property with this name.
func findDescription(node any, property string) string {
	switch n := node.(type) {
	case map[string]any:
		if props, ok := n["properties"].(map[string]any); ok {
			if target, ok := props[property].(map[string]any); ok {
				if d, ok := target["description"].(string); ok {
					return d
				}
			}
		}
		for _, v := range n {
			if got := findDescription(v, property); got != "" {
				return got
			}
		}
	case []any:
		for _, v := range n {
			if got := findDescription(v, property); got != "" {
				return got
			}
		}
	}
	return ""
}

// The same set, and the same reasoning, for the page a person reads rather
// than the schema a generator reads. verdicts.md is written by hand, so
// nothing regenerates it when the schema changes.
func TestVerdictsPageDescribesEveryPlanChange(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "docs", "src", "content", "docs", "concepts", "verdicts.md"))
	require.NoError(t, err)

	var row string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "`plan_regression`") {
			row = line
			break
		}
	}
	require.NotEmpty(t, row, "verdicts.md no longer mentions plan_regression")

	for _, kind := range insights.PlanChanges {
		require.Containsf(t, row, insights.PlanTitle(kind),
			"the verdicts page does not mention %s (%q). The row is:\n%s",
			kind, insights.PlanTitle(kind), row)
	}
}

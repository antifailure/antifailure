package oracle

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The path language is the escape hatch for everything this package refuses to
// guess at, so a pattern that silently matches nothing is the worst defect it
// can have: somebody writes an ignore rule, the report keeps showing the field,
// and there is no error to tell them why.

func TestPatternMatchesWhatItSays(t *testing.T) {
	cases := []struct {
		pattern string
		path    []segment
		want    bool
	}{
		{"$.token", []segment{keySegment("token")}, true},
		{"$.token", []segment{keySegment("tokens")}, false},
		{".token", []segment{keySegment("token")}, true},
		{"$.a.b", []segment{keySegment("a"), keySegment("b")}, true},
		{"$.a.b", []segment{keySegment("a")}, false},
		{"$.a.b", []segment{keySegment("a"), keySegment("b"), keySegment("c")}, false},

		{"$.orders[0].total", []segment{keySegment("orders"), indexSegment(0), keySegment("total")}, true},
		{"$.orders[0].total", []segment{keySegment("orders"), indexSegment(1), keySegment("total")}, false},
		{"$.orders[*].total", []segment{keySegment("orders"), indexSegment(7), keySegment("total")}, true},
		{"$.orders[*]", []segment{keySegment("orders"), indexSegment(7)}, true},

		// A wildcard consumes exactly one segment, so it must not reach a
		// grandchild. This is the difference between "*" and "..", and getting
		// it wrong makes every ignore rule far broader than it reads.
		{"$.meta.*", []segment{keySegment("meta"), keySegment("build")}, true},
		{"$.meta.*", []segment{keySegment("meta"), keySegment("build"), keySegment("sha")}, false},

		// The descend matches at the root as well as deep, because somebody
		// writing $..created_at means every created_at including the top one.
		{"$..created_at", []segment{keySegment("created_at")}, true},
		{"$..created_at", []segment{keySegment("a"), keySegment("created_at")}, true},
		{"$..created_at", []segment{keySegment("a"), indexSegment(2), keySegment("created_at")}, true},
		{"$..created_at", []segment{keySegment("created_at"), keySegment("inner")}, false},

		// A field can be named like an index, and the two must not be confused.
		{"$.0", []segment{keySegment("0")}, true},
		{"$.0", []segment{indexSegment(0)}, false},
		{"$[0]", []segment{keySegment("0")}, false},
	}
	for _, c := range cases {
		m := newMatcher([]string{c.pattern})
		require.Equalf(t, c.want, m.matches(c.path),
			"pattern %q against %s", c.pattern, renderPath(c.path))
	}
}

func TestPatternRefusesWhatItCannotParse(t *testing.T) {
	for _, bad := range []string{"", "$", "orders", "$.a[", "$.a[b]", "$.a[-1]", "$..", "$.a..", "$.."} {
		require.Falsef(t, ValidPattern(bad), "%q parsed and should not have", bad)
	}
	for _, good := range []string{"$.a", ".a", "$.a.b", "$.a[0]", "$.a[*]", "$..a", "$.a.*"} {
		require.Truef(t, ValidPattern(good), "%q did not parse and should have", good)
	}
}

// A rendered path has to parse as a pattern that selects it again, because the
// report tells people to copy a path out of it and paste it into the manifest.
// If that round trip does not hold, the advice in the report is wrong.
func TestARenderedPathIsAPatternThatSelectsIt(t *testing.T) {
	paths := [][]segment{
		{keySegment("token")},
		{keySegment("orders"), indexSegment(3), keySegment("placed_at")},
		{keySegment("a"), keySegment("b"), keySegment("c")},
		{indexSegment(0), keySegment("id")},
	}
	for _, p := range paths {
		rendered := renderPath(p)
		require.Truef(t, ValidPattern(rendered), "%s does not parse", rendered)
		require.Truef(t, newMatcher([]string{rendered}).matches(p),
			"%s does not select the path it was rendered from", rendered)
	}
}

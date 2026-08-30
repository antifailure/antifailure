package masking_test

// The transform reference page said it was generated from the registry, and
// nothing generated it. A page that claims to be checked and is not is worse
// than one that admits it is hand written: everybody trusts it, and the day a
// transform is added the table is quietly wrong.
//
// This makes the claim true. The table between the markers comes from the
// registry, and a transform added without regenerating fails here.

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

var updateTransforms = flag.Bool("update-transforms", false,
	"rewrite the transform table in the reference page")

const (
	referencePath = "../../../docs/src/content/docs/reference/transforms.md"
	tableStart    = "<!-- transforms:start -->"
	tableEnd      = "<!-- transforms:end -->"
	uniqueStart   = "<!-- unique:start -->"
	uniqueEnd     = "<!-- unique:end -->"
)

// transformTable renders the registry as the page's table.
//
// Sorted by masking.Names, so the page does not reshuffle itself every time
// somebody regenerates it and a diff shows the change rather than the order.
func transformTable() string {
	var b strings.Builder
	b.WriteString("| Transform | Unique | What it does |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, name := range masking.Names() {
		tr, ok := masking.Lookup(name)
		if !ok {
			continue
		}
		unique := "no"
		if tr.PreservesUniqueness() {
			unique = "yes"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, unique, tr.Describe())
	}
	return b.String()
}

// uniqueSentence is the registry's answer to which transforms keep a unique
// column unique.
//
// Generated for the same reason the table is, and for a while it was not. The
// sentence claimed int_fpe and string_fpe preserve uniqueness and left out
// preserve, while the table two lines above it, built from the same registry,
// said the opposite about all three. It sits directly under the AF-MSK-007
// example, so somebody whose golden refresh had just failed on a unique index
// was being told by the reference page to choose one of the two transforms
// that would fail it again.
//
// The lesson is narrower than "generate the prose". A sentence that restates a
// fact the registry already holds is not prose, whatever it looks like, and
// leaving it by hand next to the generated table meant the page contradicted
// itself with nothing able to notice.
func uniqueSentence() string {
	var names []string
	for _, name := range masking.Names() {
		tr, ok := masking.Lookup(name)
		if ok && tr.PreservesUniqueness() {
			names = append(names, "`"+name+"`")
		}
	}
	return prose(names) + " preserve uniqueness.\n"
}

// prose joins names the way the page reads them, with no serial comma.
func prose(names []string) string {
	switch len(names) {
	case 0:
		return "No transform"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// splice replaces the block between one pair of markers, leaving the prose
// around it alone. The prose is where the page explains why uniqueness matters
// and what a rules author should do about it, and a generator has nothing to
// say about that.
func splice(page, start, end, body string) (string, error) {
	i := strings.Index(page, start)
	j := strings.Index(page, end)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("the page has no %s and %s pair, so there is nowhere to put the block",
			start, end)
	}
	return page[:i] + start + "\n" + body + page[j:], nil
}

func TestTransformReferenceIsCurrent(t *testing.T) {
	raw, err := os.ReadFile(referencePath)
	require.NoError(t, err)

	want, err := splice(string(raw), tableStart, tableEnd, transformTable())
	require.NoError(t, err)
	want, err = splice(want, uniqueStart, uniqueEnd, uniqueSentence())
	require.NoError(t, err)

	if *updateTransforms {
		require.NoError(t, os.WriteFile(referencePath, []byte(want), 0o644))
		return
	}
	require.Equal(t, want, string(raw),
		"the transform reference is out of date with the registry. "+
			"Regenerate with: go test ./internal/masking -update-transforms")
}

// The page's own claim, checked directly rather than through the diff: every
// registered transform appears, and nothing appears that is not registered.
// The diff above would catch both, and this says which one went wrong.
func TestEveryRegisteredTransformIsOnThePage(t *testing.T) {
	raw, err := os.ReadFile(referencePath)
	require.NoError(t, err)
	page := string(raw)

	for _, name := range masking.Names() {
		require.Contains(t, page, "| `"+name+"` |",
			"%s is registered and the reference page does not list it", name)
	}

	registered := map[string]bool{}
	for _, name := range masking.Names() {
		registered[name] = true
	}
	for _, line := range strings.Split(page, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		name := strings.TrimPrefix(strings.SplitN(line, "`", 3)[1], "")
		require.True(t, registered[name],
			"the reference page lists %q and no transform by that name is registered", name)
	}
}

// A page whose markers were removed by an edit would silently stop being
// generated, which is the failure this file exists to prevent.
func TestSpliceRefusesAPageWithNoMarkers(t *testing.T) {
	for _, m := range [][2]string{{tableStart, tableEnd}, {uniqueStart, uniqueEnd}} {
		_, err := splice("no markers here\n", m[0], m[1], "body")
		require.Error(t, err)
		require.Contains(t, err.Error(), m[0])
	}
}

// The uniqueness sentence and the table's Unique column answer one question,
// and the page must not answer it two ways.
//
// The diff above would catch a stale sentence, and this says what is wrong with
// it. It also fails if the sentence stops being a sentence about uniqueness,
// which the diff alone cannot tell from a correct regeneration.
func TestTheUniquenessSentenceAgreesWithTheTable(t *testing.T) {
	raw, err := os.ReadFile(referencePath)
	require.NoError(t, err)
	page := string(raw)

	i := strings.Index(page, uniqueStart)
	j := strings.Index(page, uniqueEnd)
	require.True(t, i >= 0 && j > i,
		"the page no longer marks the uniqueness sentence, so nothing checks it against the registry")
	sentence := page[i+len(uniqueStart) : j]

	for _, name := range masking.Names() {
		tr, ok := masking.Lookup(name)
		require.True(t, ok)
		named := strings.Contains(sentence, "`"+name+"`")
		if tr.PreservesUniqueness() {
			require.True(t, named,
				"%s preserves uniqueness and the sentence under AF-MSK-007 does not name it, "+
					"so a reader choosing a transform for a unique column will not consider it", name)
			continue
		}
		require.False(t, named,
			"the sentence under AF-MSK-007 names %s as preserving uniqueness and it does not. "+
				"That sentence is what somebody reads after a golden refresh failed on a unique "+
				"index, so it would send them straight back into the same failure", name)
	}
}

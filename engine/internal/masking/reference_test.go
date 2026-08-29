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
	startMarker   = "<!-- transforms:start -->"
	endMarker     = "<!-- transforms:end -->"
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

// splice replaces the block between the markers, leaving the prose around it
// alone. The prose is where the page explains why uniqueness matters and what
// a rules author should do about it, and a generator has nothing to say about
// that.
func splice(page, table string) (string, error) {
	i := strings.Index(page, startMarker)
	j := strings.Index(page, endMarker)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("the page has no %s and %s pair, so there is nowhere to put the table",
			startMarker, endMarker)
	}
	return page[:i] + startMarker + "\n" + table + page[j:], nil
}

func TestTransformReferenceIsCurrent(t *testing.T) {
	raw, err := os.ReadFile(referencePath)
	require.NoError(t, err)

	want, err := splice(string(raw), transformTable())
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
	_, err := splice("no markers here\n", "table")
	require.Error(t, err)
	require.Contains(t, err.Error(), startMarker)
}

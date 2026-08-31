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

// The prose, checked against the registry the table above it comes from.
//
// This is the gate for a class the suite was structurally blind to, and the
// blindness was deliberate: `splice` says in its own comment that the prose is
// where the page explains what a rules author should do and that a generator
// has nothing to say about that. True of the advice, false of the facts the
// advice rests on.
//
// So the page contradicted itself. The generated table said `string_fpe` does
// not preserve uniqueness, which is what the registry returns, and the
// paragraph two screens below listed it among the ones that do. Both halves
// were checked, one by a generator and one by a person, and neither check
// could see the other. It was found by reading the page to choose a transform
// for a unique column and getting a candidate the engine would have refused.
//
// The pattern is worth naming because it is not about masking: two artifacts
// each individually valid, jointly wrong, with no gate positioned to compare
// them. That is the shape of most of what running this product against itself
// has turned up.
func TestTheProseAgreesWithTheRegistryAboutUniqueness(t *testing.T) {
	raw, err := os.ReadFile(referencePath)
	require.NoError(t, err)
	prose := afterGeneratedBlock(t, string(raw))

	claimed, denied := uniquenessClaims(t, prose)

	// The guard that stops this test passing because it found nothing. A
	// rewording that breaks the parse has to fail here rather than quietly
	// stop checking, which is the failure mode of every test that scans prose.
	//
	// Only the denied half now, and that is a narrowing rather than a
	// weakening. The sentence naming which transforms DO preserve uniqueness
	// is generated from PreservesUniqueness and sits inside the unique
	// markers, so it cannot contradict the registry and
	// TestTheUniquenessSentenceAgreesWithTheTable holds it there. What is
	// still written by a person, and still able to be wrong, is the half that
	// names the ones that do not.
	require.NotEmpty(t, denied,
		"the paragraph naming which transforms do not preserve uniqueness was not "+
			"found, so this check is not checking anything. If the wording changed, "+
			"teach this about the new wording rather than deleting it")

	for _, name := range claimed {
		tr, ok := masking.Lookup(name)
		require.True(t, ok, "the prose names %q, which is not a registered transform", name)
		require.True(t, tr.PreservesUniqueness(),
			"the prose says %s preserves uniqueness and the registry says it does not. "+
				"The registry is the answer: it is what the plan checks a unique column "+
				"against, so following the page here gets a rules author a refusal", name)
	}
	for _, name := range denied {
		tr, ok := masking.Lookup(name)
		require.True(t, ok, "the prose names %q, which is not a registered transform", name)
		require.False(t, tr.PreservesUniqueness(),
			"the prose says %s does not preserve uniqueness and the registry says it does", name)
	}
}

// afterGeneratedBlock returns the hand written half of the page.
//
// Only the half a generator does not own, because the generated half is
// already checked by the diff above and finding the same names there would
// make this test pass on the strength of the table agreeing with itself.
func afterGeneratedBlock(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, uniqueEnd)
	require.GreaterOrEqual(t, i, 0, "the page has no generated block to be after")
	return withoutFences(page[i+len(uniqueEnd):])
}

// withoutFences drops fenced code blocks.
//
// They are examples rather than claims, and one of them is a sample error
// message that names two transforms, so leaving them in would have this
// checking the page's own illustration of a failure. They also break the
// backtick pairing below outright: a fence is three backticks, and a scanner
// reading them in twos pairs every name on the far side with the wrong
// neighbour and silently finds nothing. That is what happened, and the
// emptiness guard is what said so.
func withoutFences(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "```")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		rest := s[i+3:]
		j := strings.Index(rest, "```")
		if j < 0 {
			return b.String()
		}
		s = rest[j+3:]
	}
}

// uniquenessClaims reads the sentence that partitions the transforms.
//
// It is one sentence of the form "a, b and c preserve uniqueness. d, e and the
// rest do not", so the split is on the phrase itself rather than on a
// structure the prose does not have. Names are read from backticks, which is
// how the page writes every transform it names.
func uniquenessClaims(t *testing.T, prose string) (claimed, denied []string) {
	t.Helper()
	// Wrapped prose, so the phrase this splits on is routinely broken across
	// two lines. The first version of this searched the raw text, found
	// nothing, and its own emptiness guard caught it, which is the only reason
	// this comment exists rather than a silently vacuous test.
	prose = strings.Join(strings.Fields(prose), " ")

	const pivot = "preserve uniqueness"
	i := strings.Index(prose, pivot)
	if i < 0 {
		// No pivot means the claimed half is not here, which is the shape the
		// page has now: that sentence is generated inside the unique markers
		// and this is reading what comes after them. What is left is the
		// denied list on its own, "`name`, `city`, `company` and the rest do
		// not, because ...", and it is still hand written and still able to
		// disagree with the registry.
		k := strings.Index(prose, "do not")
		if k < 0 {
			return nil, nil
		}
		span := prose[:k]
		if from := strings.LastIndex(span, ". "); from >= 0 {
			span = span[from:]
		}
		return nil, backticked(span)
	}
	// Back to the start of the sentence, so a name in the previous one is not
	// swept in. The paragraph begins after a blank line.
	start := strings.LastIndex(prose[:i], ". ")
	if start < 0 {
		start = 0
	}
	after := prose[i+len(pivot):]
	// The second half ends at the end of its sentence, and the "do not" that
	// separates the two lists is the only one before it.
	end := strings.Index(after, "because")
	if end < 0 {
		end = len(after)
	}
	after = after[:end]

	j := strings.Index(after, "do not")
	if j < 0 {
		return backticked(prose[start : i+len(pivot)]), nil
	}
	return backticked(prose[start : i+len(pivot)]), backticked(after[:j])
}

// backticked returns the `names` in a span, which is how the page writes them.
func backticked(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			return out
		}
		j := strings.Index(s[i+1:], "`")
		if j < 0 {
			return out
		}
		name := s[i+1 : i+1+j]
		// "the rest" and prose in backticks are not transform names, and a
		// name is always lower case with underscores.
		if name != "" && !strings.ContainsAny(name, " .,") {
			out = append(out, name)
		}
		s = s[i+2+j:]
	}
}

package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// contentOnDisk is where the site keeps the pages this package packages, from
// this package's own directory.
const contentOnDisk = "../../../docs/src/content/docs"

// TestEmbeddedPagesAreTheDocumentationThatShips is the drift check.
//
// The whole reason this corpus is generated rather than fetched is that the
// answer has to match the build the caller is talking to. A generated file
// that has fallen behind the site is the same defect wearing the opposite
// face: an authoritative looking answer about a version nobody is running.
//
// Every step fails rather than skipping. A directory that cannot be read would
// otherwise mean nothing to compare, and nothing to compare reads exactly like
// nothing wrong.
func TestEmbeddedPagesAreTheDocumentationThatShips(t *testing.T) {
	t.Parallel()
	onDisk := map[string]string{}
	err := filepath.WalkDir(contentOnDisk, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(contentOnDisk, path)
		if relErr != nil {
			return relErr
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		onDisk[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	require.NoError(t, err,
		"the documentation site at %s could not be read, so nothing was compared. Do "+
			"not let this pass by finding nothing.", contentOnDisk)
	require.NotEmpty(t, onDisk, "no page was found on disk, so nothing was compared")

	for path, want := range onDisk {
		got, packaged := Pages[path]
		require.Truef(t, packaged,
			"%s is on the documentation site and is not packaged. Run "+
				"'go run ./tools/docsembed'.", path)
		require.Equalf(t, want, got,
			"%s has changed since pages.gen.go was generated. Run "+
				"'go run ./tools/docsembed'.", path)
	}
	for path := range Pages {
		_, present := onDisk[path]
		require.Truef(t, present,
			"%s is packaged and is no longer on the documentation site. Run "+
				"'go run ./tools/docsembed'.", path)
	}
}

// fixture is a small corpus with the shapes the real one has: frontmatter, a
// heading tree, a fenced block containing a hash, and a word that is in every
// page beside one that is in a single page.
func fixture() *index {
	return build(map[string]string{
		"concepts/stances.md": `---
title: Stances
description: What a datastore stance declares.
---

Every datastore declares a stance and the manifest refuses one that does not.

## The stances

A stance is golden, empty, derived or topics_only.

` + "```" + `sh
# this hash is a shell comment and is not a heading
af up
` + "```" + `

### Empty is a legitimate answer

An invisible empty is not.

## Something else entirely

Nothing about stances here.
`,
		"guides/other.md": `---
title: Another guide
description: A page that mentions a stance once.
---

The word stance appears here once and this page is not about it.

## A heading

Filler.
`,
		"guides/unrelated.md": `---
title: Unrelated
description: No mention at all.
---

This page is about something else.
`,
		// Filler, so that "in fewer than half the pages" is a rule the fixture
		// can actually exercise. A three page corpus makes a word carried by
		// two pages look common, which is an artefact of the fixture and not
		// of the rule.
		"guides/filler-one.md":   "---\ntitle: One\n---\n\nThis page is filler.\n",
		"guides/filler-two.md":   "---\ntitle: Two\n---\n\nThis page is filler.\n",
		"guides/filler-three.md": "---\ntitle: Three\n---\n\nThis page is filler.\n",
	})
}

func TestParse_ReadsTheFrontmatterTheSiteRequires(t *testing.T) {
	t.Parallel()
	page, ok := fixture().lookup("concepts/stances.md")
	require.True(t, ok)
	require.Equal(t, "Stances", page.Title)
	require.Equal(t, "What a datastore stance declares.", page.Description)
	require.NotContains(t, page.Body, "title: Stances",
		"the frontmatter must not be in the body, or every excerpt can quote it")
	require.Equal(t, "concepts", page.Section)
}

func TestParse_AHashInAFencedBlockIsNotAHeading(t *testing.T) {
	t.Parallel()
	page, ok := fixture().lookup("concepts/stances.md")
	require.True(t, ok)
	for _, h := range page.Headings {
		require.NotContains(t, h.Text, "shell comment",
			"a comment inside a fenced block was read as a heading, which builds a "+
				"table of contents out of somebody's example script")
	}
}

func TestParse_HeadingsCarryAnchorsATrailAndTheirOwnBounds(t *testing.T) {
	t.Parallel()
	page, ok := fixture().lookup("concepts/stances.md")
	require.True(t, ok)
	require.Len(t, page.Headings, 3)

	first := page.Headings[0]
	require.Equal(t, "the-stances", first.Anchor)
	require.Equal(t, "Stances > The stances", first.Trail)

	nested := page.Headings[1]
	require.Equal(t, 3, nested.Level)
	require.Equal(t, "Stances > The stances > Empty is a legitimate answer", nested.Trail,
		"a deeper heading is a child of the one above it")

	// A section ends where the next heading at its level or above begins, not
	// at the end of the page. Reading it whole is how a search for one section
	// returns the rest of the document.
	require.Equal(t, page.Headings[2].FirstLine-1, first.LastLine)
	require.Contains(t, page.Text(first.FirstLine, first.LastLine), "topics_only")
	require.NotContains(t, page.Text(first.FirstLine, first.LastLine),
		"Nothing about stances here")
}

func TestSearch_AWordInEveryPageIsNotAMatch(t *testing.T) {
	t.Parallel()
	// "page" is in all three fixtures and "stance" is in two. A question
	// phrased as a question carries several words like the first kind, and
	// counting them as matches is how an answer says 92 pages matched and
	// tells the caller nothing.
	got := fixture().search(Query{Text: "which page has a stance", MaxResults: 5, MaxChars: 4000})
	require.Equal(t, 2, got.PagesMatched,
		"only the pages carrying the selective word may match")
	require.Equal(t, "concepts/stances.md", got.Hits[0].Path,
		"the page that is about the word outranks the one that mentions it once")
}

func TestSearch_AQueryOfOnlyCommonWordsStillAnswers(t *testing.T) {
	t.Parallel()
	// Refusing here would be a tool that answers nothing to a vague question
	// and says why in a way nobody reads. It ranks by the little the words are
	// worth instead.
	got := fixture().search(Query{Text: "this page", MaxResults: 5, MaxChars: 4000})
	require.NotEmpty(t, got.Hits, "a query with no selective word must still answer")
}

func TestSearch_ReturnsAnExcerptAndNeverThePage(t *testing.T) {
	t.Parallel()
	ix := fixture()
	got := ix.search(Query{Text: "topics_only", MaxResults: 5, MaxChars: 4000})
	require.NotEmpty(t, got.Hits)
	page, _ := ix.lookup("concepts/stances.md")
	require.Less(t, len(got.Hits[0].Excerpt), len(page.Body),
		"an excerpt the size of the page is the defect this tool exists to avoid")
	require.Contains(t, got.Hits[0].Excerpt, "topics_only")
	require.Equal(t, "the-stances", got.Hits[0].Anchor,
		"the anchor is what turns a hit into an exact second call")
}

func TestSearch_HonoursTheBudgetAndNamesWhatItDropped(t *testing.T) {
	t.Parallel()
	ix := fixture()
	generous := ix.search(Query{Text: "stance", MaxResults: 5, MaxChars: 4000})
	require.Equal(t, 2, len(generous.Hits))
	require.False(t, generous.Truncated)
	require.Equal(t, 0, generous.NotShownTotal)

	tight := ix.search(Query{Text: "stance", MaxResults: 1, MaxChars: 300})
	require.Equal(t, 1, len(tight.Hits), "a lower max_results returns fewer excerpts")
	require.Equal(t, 1, tight.NotShownTotal, "and says how many it did not return")
	require.Equal(t, []string{"guides/other.md"}, tight.PagesNotShown,
		"and names them, so the caller can ask for one directly")
	require.True(t, tight.Truncated)
	require.LessOrEqual(t, tight.CharsUsed, 300, "the stated budget is enforced, not advertised")
}

func TestSearch_ASectionNarrowsAndSaysNothingElse(t *testing.T) {
	t.Parallel()
	got := fixture().search(Query{
		Text: "stance", Section: "guides", MaxResults: 5, MaxChars: 4000})
	require.Len(t, got.Hits, 1)
	require.Equal(t, "guides/other.md", got.Hits[0].Path)
}

func TestSearch_AQueryThatMatchesNothingDoesNotMatchEverything(t *testing.T) {
	t.Parallel()
	// Two shapes of nothing, and they are decided by different code. A query
	// with no words at all never reaches the scoring; a query whose words are
	// nowhere in the corpus reaches it and has to come back with nothing. A
	// search that answered either with every page would be a search that
	// cannot say no.
	punctuation := fixture().search(Query{Text: "!!! ?", MaxResults: 5, MaxChars: 4000})
	require.Empty(t, punctuation.Hits)
	require.Equal(t, 0, punctuation.PagesMatched)

	absent := fixture().search(Query{
		Text: "zzzqqxnothinglikethis", MaxResults: 5, MaxChars: 4000})
	require.Empty(t, absent.Hits)
	require.Equal(t, 0, absent.PagesMatched)
}

func TestSearch_CountsTheOtherMatchingSectionsOfAPage(t *testing.T) {
	t.Parallel()
	got := fixture().search(Query{Text: "stance", MaxResults: 5, MaxChars: 4000})
	require.Equal(t, "concepts/stances.md", got.Hits[0].Path)
	require.Positive(t, got.Hits[0].OtherSections,
		"a page with more to say about the query must say so, or its other sections "+
			"are invisible")
	require.Greater(t, got.SectionsMatched, got.PagesMatched)
}

func TestRead_AWholeSectionAndNothingAfterIt(t *testing.T) {
	t.Parallel()
	ix := fixture()
	page, ok := ix.lookup("concepts/stances.md")
	require.True(t, ok)
	section, found := page.FindHeading("the-stances")
	require.True(t, found)

	got := Read(page, section, 4000)
	require.False(t, got.Truncated)
	require.Equal(t, 0, got.NotReturnedChars)
	require.Contains(t, got.Text, "topics_only")
	require.NotContains(t, got.Text, "Nothing about stances here")
	require.Equal(t, "Stances > The stances", got.Trail)
}

func TestRead_ATruncatedPageSaysHowMuchAndWhichSectionsAreMissing(t *testing.T) {
	t.Parallel()
	ix := fixture()
	page, ok := ix.lookup("concepts/stances.md")
	require.True(t, ok)

	got := Read(page, nil, 200)
	require.True(t, got.Truncated, "a page longer than the budget must say it was cut")
	require.Contains(t, got.Text, CutMarker,
		"the text itself must carry the mark, or a reader believes it is complete")
	require.Positive(t, got.NotReturnedChars)
	require.Equal(t, got.TotalChars-got.NotReturnedChars,
		len(strings.TrimSuffix(got.Text, "\n"+CutMarker)),
		"the withheld count must be the arithmetic complement of what was returned")
	require.Contains(t, got.SectionsNotReturned, "something-else-entirely",
		"a section past the cut has to be named, or the truncation is a dead end")
}

func TestRead_AWholePageThatFitsWithholdsNothing(t *testing.T) {
	t.Parallel()
	page, ok := fixture().lookup("guides/unrelated.md")
	require.True(t, ok)
	got := Read(page, nil, 4000)
	require.False(t, got.Truncated)
	require.Equal(t, got.TotalChars, got.ReturnedChars)
	require.Empty(t, got.SectionsNotReturned)
}

func TestLookup_AcceptsThePathFormsACallerActuallyHas(t *testing.T) {
	t.Parallel()
	ix := fixture()
	for _, form := range []string{
		"concepts/stances.md", "concepts/stances", "/docs/concepts/stances", "docs/concepts/stances.md",
	} {
		page, ok := ix.lookup(form)
		require.Truef(t, ok, "%q must resolve", form)
		require.Equal(t, "concepts/stances.md", page.Path)
	}
	_, ok := ix.lookup("concepts/invented")
	require.False(t, ok, "a path nobody ships must not resolve to something near it")
}

func TestNear_NamesWhatTheCallerProbablyMeant(t *testing.T) {
	t.Parallel()
	got := fixture().near("concepts/stance", 5)
	require.Contains(t, got, "concepts/stances.md")
}

func TestList_TheWholeCorpusAndOneSectionOfIt(t *testing.T) {
	t.Parallel()
	ix := fixture()
	require.Len(t, ix.list(""), 6)
	require.Len(t, ix.list("guides"), 5)
	require.Empty(t, ix.list("nosuchsection"))
	require.Equal(t, "Stances", ix.list("concepts")[0].Title)
}

func TestSlug_MatchesTheAnchorTheSiteServes(t *testing.T) {
	t.Parallel()
	require.Equal(t, "versions-are-immutable", slug("Versions are immutable"))
	require.Equal(t, "rehearse_migration_safety", slug("`rehearse_migration_safety`"))
	require.Equal(t, "one-two", slug("  One, two!  "))
}

func TestHeadings_ARepeatedHeadingGetsADistinctAnchor(t *testing.T) {
	t.Parallel()
	ix := build(map[string]string{"a/b.md": "---\ntitle: T\n---\n\n## Same\n\nx\n\n## Same\n\ny\n"})
	page, ok := ix.lookup("a/b.md")
	require.True(t, ok)
	require.Equal(t, []string{"same", "same-1"}, page.Anchors(),
		"two sections sharing an anchor means one of them can never be read")
}

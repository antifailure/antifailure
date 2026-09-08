package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/docs"
)

// These run against the documentation this build actually ships, not against a
// fixture. A tool that answers questions about the product has to be tested on
// the product's own pages: the ranking, the budget and the anchors are only
// meaningful against a corpus of the real size, and a fixture would prove the
// plumbing while saying nothing about whether the answer arrives.

// callDocsTool puts a call through the same validation a real one goes
// through, rather than handing the handler a Go map.
//
// It matters here more than it looks. The protocol decodes numbers as
// json.Number and the handlers read them as such, so a test passing a Go int
// exercises a path no caller can reach: max_chars silently falls back to its
// default, and every budget assertion in this file would then be asserting
// about the default rather than about the budget. That is exactly the shape of
// test that passes while the feature does nothing.
func callDocsTool(t *testing.T, tool *Tool, args map[string]any) (any, *Fault) {
	t.Helper()
	p := evidenceProject(nil)
	if _, present := args["project_id"]; !present {
		args["project_id"] = p.ID
	}
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	parsed, fault := validateArguments(tool.Input, raw)
	if fault != nil {
		return nil, fault
	}
	return tool.Handler(context.Background(), &Call{}, parsed)
}

func searchDocs(t *testing.T, args map[string]any) docSearchDoc {
	t.Helper()
	out, fault := callDocsTool(t, newSearchDocsTool(evidenceProject(nil)), args)
	require.Nil(t, fault)
	doc, ok := out.(docSearchDoc)
	require.True(t, ok, "the tool returned %T", out)
	return doc
}

func readDocsPage(t *testing.T, args map[string]any) (docReadDoc, *Fault) {
	t.Helper()
	out, fault := callDocsTool(t, newReadDocsPageTool(evidenceProject(nil)), args)
	if fault != nil {
		return docReadDoc{}, fault
	}
	doc, ok := out.(docReadDoc)
	require.True(t, ok, "the tool returned %T", out)
	return doc, nil
}

func listDocs(t *testing.T, args map[string]any) (docListDoc, *Fault) {
	t.Helper()
	out, fault := callDocsTool(t, newListDocsTool(evidenceProject(nil)), args)
	if fault != nil {
		return docListDoc{}, fault
	}
	doc, ok := out.(docListDoc)
	require.True(t, ok, "the tool returned %T", out)
	return doc, nil
}

// -----------------------------------------------------------------------
// search_documentation
// -----------------------------------------------------------------------

func TestSearchDocs_AnswersTheQuestionAndDoesNotReturnThePage(t *testing.T) {
	t.Parallel()
	got := searchDocs(t, map[string]any{"query": "what does stance mean"})

	require.NotEmpty(t, got.Results, "the question has an answer in these pages")
	first := got.Results[0]
	require.Contains(t, strings.ToLower(first.Excerpt), "stance",
		"the excerpt returned first must be about the word that was asked about")

	// The page it came from is 40 kilobytes. Returning it would answer the
	// question and cost the caller the room it needed to act on the answer,
	// which is the failure this tool exists to avoid.
	require.Less(t, len(first.Excerpt), 1500,
		"an excerpt this size is a page, and a tool that returns a page when asked a "+
			"narrow question is worse than no tool")
	require.NotEmpty(t, first.Anchor,
		"a hit has to carry the anchor that reads its section, or the caller's only "+
			"way forward is the whole page")
	require.LessOrEqual(t, got.Budget.CharsReturned, got.Budget.MaxChars)
}

func TestSearchDocs_ASmallBudgetReturnsLessAndSaysWhatWasDropped(t *testing.T) {
	t.Parallel()
	query := "what does stance mean"
	generous := searchDocs(t, map[string]any{"query": query, "max_chars": 4000})
	tight := searchDocs(t, map[string]any{"query": query, "max_chars": 400, "max_results": 1})

	require.Less(t, tight.Budget.CharsReturned, generous.Budget.CharsReturned,
		"a lower budget has to return less content, or the budget is decoration")
	require.LessOrEqual(t, tight.Budget.CharsReturned, 400,
		"the budget the caller stated is the budget the tool keeps")
	require.Less(t, len(tight.Results), len(generous.Results))
	require.Positive(t, tight.Omitted.PagesNotShown,
		"what the budget cost the caller has to be a fact in the answer")
	require.NotEmpty(t, tight.Omitted.Paths,
		"the pages that were dropped have to be named, or the caller cannot ask for one")
	// A budget too small for the excerpt whole is the case the clipping
	// exists for, and it has to answer rather than drop the best hit: a tight
	// budget buys a shorter answer, never no answer. Without the clip this
	// excerpt is 330 characters, does not fit in 300, and the only remaining
	// move is to drop it.
	clipped := searchDocs(t, map[string]any{
		"query": "golden branch verification", "max_chars": 300, "max_results": 1})
	require.NotEmpty(t, clipped.Results,
		"a budget smaller than the best excerpt must still answer, shortened")
	require.True(t, clipped.Results[0].ExcerptClipped,
		"an excerpt cut to fit has to say it was cut, or a reader believes it is whole")
	require.LessOrEqual(t, clipped.Budget.CharsReturned, 300)

	require.Contains(t, tight.Omitted.Note, "not shown")
	require.Contains(t, tight.Omitted.Note, "max_results is 1",
		"the note has to report the limit the caller passed, not one recomputed from "+
			"the answer, or it gives a wrong reason for a real omission")
	require.True(t, tight.Omitted.Truncated)
}

func TestSearchDocs_ManyMatchesSayHowManyWereNotShown(t *testing.T) {
	t.Parallel()
	got := searchDocs(t, map[string]any{"query": "golden branch verification", "max_results": 3})

	require.Greater(t, got.Omitted.PagesMatched, len(got.Results),
		"this query matches more pages than any answer should carry")
	require.Equal(t, got.Omitted.PagesMatched, got.Omitted.PagesShown+got.Omitted.PagesNotShown,
		"the arithmetic has to close, or the count is a claim rather than a fact")
	require.Contains(t, got.Summary, "not shown",
		"a caller reading only the summary still has to learn that it is not seeing "+
			"everything")
	require.NotEmpty(t, got.Omitted.Note)
}

func TestSearchDocs_NothingWithheldIsAlsoStated(t *testing.T) {
	t.Parallel()
	// The opposite case, which is the one a silent implementation gets right
	// by accident. A caller has to be able to tell a complete answer from a
	// truncated one without inferring it from an absence.
	got := searchDocs(t, map[string]any{
		"query": "topology", "max_results": 5, "max_chars": 20000})
	require.Equal(t, 0, got.Omitted.PagesNotShown)
	require.Contains(t, got.Omitted.Note, "Nothing was withheld")
}

func TestSearchDocs_AQueryThatMatchesNothingSaysSoAndSuggestsTheNextCall(t *testing.T) {
	t.Parallel()
	got := searchDocs(t, map[string]any{"query": "zzzqqxnothinglikethis"})
	require.Empty(t, got.Results)
	require.Equal(t, 0, got.Omitted.PagesMatched)
	require.Contains(t, got.Summary, "No page")
	require.Contains(t, got.NextStep, "list_documentation")
}

func TestSearchDocs_ASectionNarrowsTheSearch(t *testing.T) {
	t.Parallel()
	got := searchDocs(t, map[string]any{"query": "golden", "section": "concepts"})
	require.NotEmpty(t, got.Results)
	for _, hit := range got.Results {
		require.True(t, strings.HasPrefix(hit.Path, "concepts/"),
			"%s is not in the section that was asked for", hit.Path)
	}
	require.Contains(t, got.Summary, "concepts section only")
}

// -----------------------------------------------------------------------
// read_documentation_page
// -----------------------------------------------------------------------

func TestReadDocsPage_APageThatDoesNotExistIsRefusedAndTheNearestAreNamed(t *testing.T) {
	t.Parallel()
	_, fault := readDocsPage(t, map[string]any{"path": "concepts/golden.md"})

	require.NotNil(t, fault,
		"a page nobody ships must be refused; an empty result reads exactly like a "+
			"page with nothing in it, and an agent told that invents the content")
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Equal(t, "path", fault.Field)
	require.Contains(t, fault.Detail, "concepts/goldens.md",
		"the refusal has to name what the caller probably meant")
	require.Contains(t, fault.Detail, "list_documentation")
}

func TestReadDocsPage_ASectionIsReadOnItsOwn(t *testing.T) {
	t.Parallel()
	whole, fault := readDocsPage(t, map[string]any{
		"path": "concepts/goldens.md", "max_chars": 40000})
	require.Nil(t, fault)
	require.False(t, whole.Withheld.Truncated)

	section, fault := readDocsPage(t, map[string]any{
		"path": "concepts/goldens.md", "section": "versions-are-immutable"})
	require.Nil(t, fault)
	require.Less(t, section.Withheld.TotalChars, whole.Withheld.TotalChars,
		"a section has to be smaller than the page, or the anchor bought nothing")
	require.Contains(t, section.Content, "immutable")
	require.Equal(t, "versions-are-immutable", section.Anchor)
	require.Contains(t, section.Withheld.Note, "Nothing was withheld")
}

func TestReadDocsPage_ASectionThatDoesNotExistIsADifferentRefusal(t *testing.T) {
	t.Parallel()
	// A path nobody ships and a heading a page does not have are two different
	// mistakes. Reporting either as the other sends an agent to fix the wrong
	// half of its call.
	_, fault := readDocsPage(t, map[string]any{
		"path": "concepts/goldens.md", "section": "no-such-heading"})
	require.NotNil(t, fault)
	require.Equal(t, "section", fault.Field)
	require.Contains(t, fault.Detail, "concepts/goldens.md")
	require.Contains(t, fault.Detail, "has no section")
}

func TestReadDocsPage_ATruncatedReadNamesWhatIsPastTheCut(t *testing.T) {
	t.Parallel()
	got, fault := readDocsPage(t, map[string]any{
		"path": "reference/cli.md", "max_chars": 1000})
	require.Nil(t, fault)

	require.True(t, got.Withheld.Truncated)
	require.LessOrEqual(t, len(got.Content), 1000+len(docs.CutMarker)+1,
		"the budget is what bounds the content, and the mark that says it was cut is "+
			"the only thing allowed past it")
	require.Contains(t, got.Content, docs.CutMarker,
		"the text itself carries the mark, or a reader believes it is complete")
	require.Positive(t, got.Withheld.WithheldChars)
	require.NotEmpty(t, got.Withheld.Sections,
		"the sections past the cut have to be named, or the truncation is a dead end")
	require.NotEmpty(t, got.NextStep)
}

func TestReadDocsPage_TheWholePageOfSomethingSmallIsComplete(t *testing.T) {
	t.Parallel()
	got, fault := readDocsPage(t, map[string]any{"path": "concepts/verdicts", "max_chars": 40000})
	require.Nil(t, fault, "a path without its .md must resolve")
	require.False(t, got.Withheld.Truncated)
	require.Equal(t, got.Withheld.TotalChars, got.Withheld.ReturnedChars)
	require.Empty(t, got.Withheld.Sections)
	require.NotEmpty(t, got.Anchors, "a page's anchors are what an exact second call needs")
}

// -----------------------------------------------------------------------
// list_documentation
// -----------------------------------------------------------------------

func TestListDocs_ListsEveryPageAndStaysCheapEnoughToCallFirst(t *testing.T) {
	t.Parallel()
	got, fault := listDocs(t, map[string]any{})
	require.Nil(t, fault)

	listed := 0
	for _, pages := range got.PagesBySection {
		listed += len(pages)
	}
	require.Equal(t, got.PagesTotal, listed,
		"the cheap listing is complete: every page this build ships is named")
	require.NotEmpty(t, got.Sections)
	require.Empty(t, got.Pages, "the cheap listing does not also carry the expensive one")

	// The point of this tool is that it can be called before you know what you
	// want. A listing that costs as much as reading a page would not be.
	body, err := json.Marshal(got)
	require.NoError(t, err)
	require.Less(t, len(body), 6000,
		"the orientation call has to stay cheap, or nobody can afford to orient")
	require.NotContains(t, string(body), "\"description\"",
		"descriptions are what make this listing expensive; they belong to the "+
			"per section call")
}

func TestListDocs_OneSectionCarriesADescriptionEach(t *testing.T) {
	t.Parallel()
	got, fault := listDocs(t, map[string]any{"section": "concepts"})
	require.Nil(t, fault)
	require.NotEmpty(t, got.Pages)
	for _, page := range got.Pages {
		require.True(t, strings.HasPrefix(page.Path, "concepts/"), page.Path)
		require.NotEmpty(t, page.Description,
			"%s has no description, so the more expensive listing bought nothing", page.Path)
	}
}

func TestListDocs_OnePagesHeadingsCarryTheirAnchors(t *testing.T) {
	t.Parallel()
	got, fault := listDocs(t, map[string]any{"path": "concepts/goldens.md"})
	require.Nil(t, fault)
	require.NotNil(t, got.Page)
	require.Empty(t, got.Pages, "a page outline is not also a listing of pages")
	require.Empty(t, got.PagesBySection, "nor a listing of every page")

	anchors := map[string]bool{}
	for _, h := range got.Page.Headings {
		require.NotEmpty(t, h.Anchor, "%q has no anchor and so cannot be read", h.Text)
		require.False(t, anchors[h.Anchor],
			"%q repeats an anchor, so one of the two sections can never be read", h.Anchor)
		anchors[h.Anchor] = true
		require.Positive(t, h.Chars, "%q reports no size, so the caller cannot budget", h.Text)
	}
	require.True(t, anchors["versions-are-immutable"])
}

func TestListDocs_SectionAndPathTogetherAreRefused(t *testing.T) {
	t.Parallel()
	// Resolved by precedence, one of the two would be silently ignored, and a
	// caller would believe it had narrowed something it had not.
	_, fault := listDocs(t, map[string]any{"section": "concepts", "path": "guides/synth.md"})
	require.NotNil(t, fault)
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Contains(t, fault.Detail, "not both")
}

// -----------------------------------------------------------------------
// the contract all three keep
// -----------------------------------------------------------------------

func documentationTools() []*Tool {
	p := evidenceProject(nil)
	return []*Tool{newSearchDocsTool(p), newListDocsTool(p), newReadDocsPageTool(p)}
}

func TestDocsTools_AreRegisteredByTheServer(t *testing.T) {
	t.Parallel()
	// A tool that exists and is never registered is a dead, shippable gap that
	// reads exactly like a working feature. The registrations are read out of
	// Serve rather than asserted about this list.
	registered := localToolNames(t)
	for _, tool := range documentationTools() {
		_, found := registered[tool.Name]
		require.Truef(t, found, "%s is built and Serve never registers it", tool.Name)
	}
}

func TestDocsTools_RequireTheProjectAssertionLikeEveryOtherTool(t *testing.T) {
	t.Parallel()
	// Uniformity is the point. A caller that has learned to pass project_id on
	// every tool of this server would have the call refused by any tool that
	// did not declare it, because the schema refuses unknown members.
	for _, tool := range documentationTools() {
		require.Contains(t, tool.Input.Required, "project_id", tool.Name)
		_, fault := tool.Handler(context.Background(), &Call{}, map[string]any{})
		require.NotNilf(t, fault, "%s answered a call that named no project", tool.Name)
		require.Equal(t, "project_id", fault.Field, tool.Name)
	}
}

func TestDocsTools_AreReadOnlyAndTouchNothing(t *testing.T) {
	t.Parallel()
	for _, tool := range documentationTools() {
		require.Truef(t, tool.ReadOnly, "%s reads a fixed corpus and changes nothing", tool.Name)
		require.Falsef(t, tool.Destructive, "%s destroys nothing", tool.Name)
	}
}

func TestDocsTools_EveryPropertyIsDescribedAndBounded(t *testing.T) {
	t.Parallel()
	for _, tool := range documentationTools() {
		walkSchema(t, tool.Input, tool.Name, func(path string, s *Schema) {
			if s.Type == "string" && path != tool.Name {
				require.NotZerof(t, s.MaxLength, "%s is an unbounded string", path)
			}
			if s.Type == "integer" {
				require.Truef(t, s.HasMax, "%s is an unbounded number", path)
			}
		})
		for name, prop := range tool.Input.Properties {
			require.NotEmptyf(t, prop.Description, "%s.%s has no description", tool.Name, name)
		}
	}
}

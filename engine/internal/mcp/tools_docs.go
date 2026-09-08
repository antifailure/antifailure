package mcp

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/antifailure/antifailure/engine/internal/docs"
)

// The three documentation tools answer the question every other tool in this
// server assumes has already been answered: what is this product.
//
// Antifailure is new and no model carries it. An agent handed this server can
// submit a rehearsal and read a verdict while having no way to find out what a
// stance is, what topology measures, or why a golden that fails verification
// cannot be branched. It guesses, and it guesses confidently, because nothing
// tells it otherwise.
//
// THE CONSTRAINT THAT SHAPES ALL THREE. The documentation is 92 pages and
// 1.1 MB. A tool that answers a narrow question with a whole page is worse
// than no tool: it costs the caller the room it needed to act on the answer,
// and it does so invisibly. So search returns excerpts and never pages, a
// listing is cheap enough to call before you know what you want, a page is
// read by section, and every one of the three states what it did NOT return.
// A silent truncation is a caller believing it has seen everything, which is
// the defect this repository exists to find.
//
// ON project_id, WHICH THESE TOOLS REQUIRE LIKE EVERY OTHER TOOL HERE.
// It was worth asking whether they should. The field exists to refuse a call
// meant for another repository, and the documentation is the same in every
// server built from the same engine, so a documentation call answered by the
// wrong server returns a right answer.
//
// It is required anyway, for two reasons. The first is that the schema refuses
// unknown members: a caller that has learned to pass project_id on every tool
// of this server would have that call refused by the three that did not
// declare it, and "the field you always pass is rejected here" is a worse
// contract than one field on every tool. The second is that a right answer
// from the wrong server is still the wrong server: an agent whose docs call
// succeeded against a server it did not mean to reach has been taught that its
// routing works, and the next call is a rehearsal. Refusing early is cheaper
// than a confident verdict about the wrong repository.

// The output bounds for the documentation tools. Each is a real bound rather
// than a suggestion: the tools cut to them and say so.
const (
	// maxSearchResults is the ceiling on max_results, whatever budget is
	// asked for. Beyond this a caller is browsing rather than asking, and
	// browsing is what the listing is for.
	maxSearchResults = 20
	// defaultSearchResults and defaultSearchChars are what a caller that
	// states no budget gets. Four thousand characters is roughly a thousand
	// tokens, which is a fraction of a percent of the corpus and enough for
	// five excerpts to answer a real question.
	defaultSearchResults = 5
	defaultSearchChars   = 4000
	// maxSearchChars bounds the budget itself.
	maxSearchChars = 20000
	// minBudgetChars is the smallest budget accepted. Below it no excerpt
	// survives and the answer is a list of things not returned.
	minBudgetChars = 200
	// defaultReadChars and maxReadChars bound reading one page. A page here
	// averages twelve thousand characters and the longest is seventy six
	// thousand, so the default returns most sections whole and the ceiling
	// still refuses to hand over the largest page in one call.
	defaultReadChars = 8000
	maxReadChars     = 40000
	// maxAnchorsListed bounds the anchors reported with a page.
	maxAnchorsListed = 60
	// maxNearMisses is how many close paths a refusal names.
	maxNearMisses = 5
)

// docText bounds a piece of the shipped documentation for a result.
//
// It is NOT neutralize. That collapses every run of whitespace into one space,
// which is right for a migration's file name and wrong for a documentation
// excerpt: a collapsed code fence, table or list is harder to read than the
// page it came from, and an agent given mangled YAML will write mangled YAML.
// So newlines and tabs survive and every other control character does not.
//
// The pages are this repository's own prose, compiled into the binary, so they
// are trusted in the way the error catalog is trusted. They are bounded anyway,
// because a result that trusts one field is a result with one unbounded field
// in it.
func docText(s string, max int) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == unicode.ReplacementChar:
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			// Control and format characters, which includes the
			// bidirectional overrides that let text render in an order other
			// than the one it is stored in.
		default:
			b.WriteRune(r)
		}
	}
	return clip(b.String(), max)
}

// docPathSchema is the shared declaration of a page path.
func docPathSchema(required string) *Schema {
	return &Schema{
		Type: "string", MaxLength: 256, MinLength: 1,
		Pattern: `/?[A-Za-z0-9][A-Za-z0-9._/-]{0,254}`,
		Description: required + " A page's path under docs/src/content/docs, as " +
			"search_documentation and list_documentation report it, such as " +
			"concepts/goldens.md. The .md is optional and a leading docs/ is ignored. " +
			"A path this build does not ship is refused and the nearest paths are named.",
	}
}

// docSectionSchema declares the top level groups, with the real names in the
// published enum so a caller never has to guess one.
func docSectionSchema(what string) *Schema {
	return &Schema{
		Type: "string", MaxLength: 64, MinLength: 1, Enum: docs.Sections(),
		Description: what,
	}
}

// -----------------------------------------------------------------------
// search_documentation
// -----------------------------------------------------------------------

type docSearchDoc struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Terms are the words actually searched for, so a caller can see that a
	// misspelling was searched for literally rather than corrected.
	Terms   []string         `json:"terms_searched"`
	Results []docSearchHit   `json:"results"`
	Omitted docSearchOmitted `json:"not_returned"`
	Budget  docBudget        `json:"budget"`
	// NextStep is the one call to make with what this answer found.
	NextStep string `json:"next_step"`
}

type docSearchHit struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	// Section is the heading path the excerpt came from.
	Section string `json:"section"`
	// Anchor is what to pass to read_documentation_page to get this section
	// and nothing else. Empty when the match was above the first heading.
	Anchor string `json:"anchor,omitempty"`
	Lines  string `json:"lines"`
	// Excerpt is the lines around the match. It is never a whole page.
	Excerpt string `json:"excerpt"`
	// ExcerptClipped says this excerpt itself was cut to fit the budget.
	ExcerptClipped bool `json:"excerpt_clipped"`
	// OtherSectionsOnThisPage is how many further sections of the same page
	// matched and were not returned.
	OtherSectionsOnThisPage int `json:"other_matching_sections_on_this_page"`
}

// docSearchOmitted is the account of what the answer left out.
//
// It is a field of every response rather than one that appears on truncation,
// because a field that appears only sometimes is a field a caller forgets to
// check. Zero and an empty list are an answer.
type docSearchOmitted struct {
	PagesMatched    int `json:"pages_matched"`
	PagesShown      int `json:"pages_shown"`
	PagesNotShown   int `json:"pages_not_shown"`
	SectionsMatched int `json:"sections_matched"`
	// Paths names the pages that matched and were not shown, itself bounded,
	// with PathsListed saying how many of PagesNotShown are named here.
	Paths       []string `json:"pages_not_shown_paths"`
	PathsListed int      `json:"pages_not_shown_paths_listed"`
	Truncated   bool     `json:"truncated"`
	Note        string   `json:"note,omitempty"`
}

// docBudget is what was asked for and what was spent.
type docBudget struct {
	MaxChars      int `json:"max_chars"`
	CharsReturned int `json:"chars_returned"`
	MaxResults    int `json:"max_results"`
}

// newSearchDocsTool builds search_documentation.
func newSearchDocsTool(p *Project) *Tool {
	return &Tool{
		Name:     "search_documentation",
		Title:    "Search the Antifailure documentation",
		ReadOnly: true,
		Description: "Search this build's own documentation and get back the few lines that " +
			"answer the question, never a whole page. Reach for this before assuming " +
			"anything about how Antifailure works: it is a new product, so what you " +
			"believe about a manifest field, a stance, a verdict or a golden is a guess " +
			"unless you read it here. Each hit is a page path, the heading it came from, " +
			"the anchor to read that section on its own, and the lines around the match. " +
			"You state the budget with max_chars and max_results and this tool honours it " +
			"by shortening and then dropping excerpts, and every response says how many " +
			"pages matched, how many were shown and which were not, so a short answer is " +
			"never mistaken for a complete one. The pages are compiled into this binary, " +
			"so they describe the version you are talking to and no network is used.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id", "query"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"query": {
					Type: "string", MaxLength: 300, MinLength: 2,
					Description: "What you want to know, in your own words or as keywords. " +
						"A rare word decides the ranking and a common one costs almost " +
						"nothing, so a whole question works as well as a keyword.",
				},
				"section": docSectionSchema("Optional. Search only one part of the " +
					"documentation. Leave it out to search all of it."),
				"max_results": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true,
					Maximum: maxSearchResults,
					Description: "Optional. How many pages to return an excerpt from, " +
						"one per page, best first. The default is 5.",
				},
				"max_chars": {
					Type: "integer", HasMin: true, Minimum: minBudgetChars,
					HasMax: true, Maximum: maxSearchChars,
					Description: "Optional. The total characters of excerpt you are " +
						"willing to spend, roughly four characters per token. The default " +
						"is 4000. It is enforced rather than advertised: excerpts are " +
						"shortened and then dropped to stay inside it, and everything " +
						"dropped is named in the response.",
				},
			},
		},
		Handler: func(_ context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return searchDocumentation(args)
		},
	}
}

func searchDocumentation(args map[string]any) (any, *Fault) {
	query, _ := args["query"].(string)
	section, _ := args["section"].(string)
	results := optionalInt(args, "max_results", defaultSearchResults)
	budget := optionalInt(args, "max_chars", defaultSearchChars)

	found := docs.Search(docs.Query{
		Text: query, Section: section, MaxResults: results, MaxChars: budget,
	})

	out := docSearchDoc{
		Kind: "documentation_search", Terms: found.Terms,
		Results: []docSearchHit{},
		Budget:  docBudget{MaxChars: budget, CharsReturned: found.CharsUsed, MaxResults: results},
	}
	for _, hit := range found.Hits {
		out.Results = append(out.Results, docSearchHit{
			Path: hit.Path, Title: docText(hit.Title, 200),
			Section: docText(hit.Trail, 300), Anchor: hit.Anchor,
			Lines:   fmt.Sprintf("%d-%d", hit.FirstLine, hit.LastLine),
			Excerpt: docText(hit.Excerpt, maxSearchChars), ExcerptClipped: hit.Clipped,
			OtherSectionsOnThisPage: hit.OtherSections,
		})
	}
	out.Omitted = docSearchOmitted{
		PagesMatched: found.PagesMatched, PagesShown: len(found.Hits),
		PagesNotShown: found.NotShownTotal, SectionsMatched: found.SectionsMatched,
		Paths: found.PagesNotShown, PathsListed: len(found.PagesNotShown),
		Truncated: found.Truncated,
	}
	out.Omitted.Note = searchOmissionNote(found, results)
	out.Summary = searchSummary(query, section, found)
	out.NextStep = searchNextStep(found)
	return out, nil
}

// searchSummary is the sentence a model reads when it reads nothing else.
func searchSummary(query, section string, found docs.Results) string {
	var b strings.Builder
	where := "the documentation"
	if section != "" {
		fmt.Fprintf(&b, "Searched the %s section only. ", section)
		where = "it"
	}
	if len(found.Terms) == 0 {
		return strings.TrimSpace(b.String() +
			"The query carried no searchable word, so nothing was looked up.")
	}
	if found.PagesMatched == 0 {
		fmt.Fprintf(&b, "No page of %s matches %q. The words searched for were %s. "+
			"Try one word rather than a sentence, or call list_documentation to see "+
			"what is here.", where, clip(query, 100), strings.Join(found.Terms, ", "))
		return strings.TrimSpace(b.String())
	}
	fmt.Fprintf(&b, "%d %s matched and %d %s shown, best first, as excerpts rather than "+
		"whole pages. ",
		found.PagesMatched, plural(found.PagesMatched, "page", "pages"),
		len(found.Hits), plural(len(found.Hits), "is", "are"))
	if found.NotShownTotal > 0 {
		fmt.Fprintf(&b, "%d %s not shown. ",
			found.NotShownTotal, plural(found.NotShownTotal, "page is", "pages are"))
	}
	fmt.Fprintf(&b, "%d of %d characters of budget were spent.",
		found.CharsUsed, found.CharsBudget)
	return strings.TrimSpace(b.String())
}

// searchOmissionNote says what was left out and how to ask for it.
//
// maxResults is the value the CALLER passed rather than anything recomputed
// from the answer. A note that reports the limit as the number of pages that
// matched tells the caller its budget was something it never asked for, which
// is worse than saying nothing: it is a wrong reason for a real omission.
func searchOmissionNote(found docs.Results, maxResults int) string {
	switch {
	case found.PagesMatched == 0:
		return ""
	case found.NotShownTotal == 0 && !found.Truncated:
		return fmt.Sprintf(
			"Nothing was withheld: all %d matching %s shown in full.",
			found.PagesMatched, plural(found.PagesMatched, "page is", "pages are"))
	case found.NotShownTotal == 0:
		return "Every matching page is shown, and at least one excerpt was cut to fit " +
			"the character budget. Raise max_chars, or read the section with " +
			"read_documentation_page and the anchor beside it."
	}
	note := fmt.Sprintf(
		"%d of the %d matching pages were not shown, because max_results is %d and the "+
			"character budget is %d. ",
		found.NotShownTotal, found.PagesMatched, maxResults, found.CharsBudget)
	if len(found.PagesNotShown) < found.NotShownTotal {
		note += fmt.Sprintf("The first %d of them are named above and %d are not. ",
			len(found.PagesNotShown), found.NotShownTotal-len(found.PagesNotShown))
	} else {
		note += "Every one of them is named above. "
	}
	return note + "Ask again with a narrower query, with section set, or read one of " +
		"those paths directly with read_documentation_page."
}

func searchNextStep(found docs.Results) string {
	if len(found.Hits) == 0 {
		return "Call list_documentation with no section to see every page this build ships."
	}
	first := found.Hits[0]
	if first.Anchor == "" {
		return fmt.Sprintf(
			"If the excerpt is not enough, read the page with read_documentation_page "+
				"path=%q.", first.Path)
	}
	return fmt.Sprintf(
		"If the excerpt is not enough, read that section and nothing else with "+
			"read_documentation_page path=%q section=%q.", first.Path, first.Anchor)
}

// -----------------------------------------------------------------------
// list_documentation
// -----------------------------------------------------------------------

type docListDoc struct {
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	PagesTotal int    `json:"pages_total"`
	// Sections is the top level shape of the documentation, present when no
	// single page was asked about.
	Sections []docSectionCount `json:"sections,omitempty"`
	// PagesBySection is the whole table of contents, one line per page, as
	// "<path> <title>". A list of strings rather than a list of objects, and
	// the reason is measured rather than aesthetic: as objects with a path
	// field and a title field, this listing costs 2,545 tokens, and as lines
	// it costs about half that for the same information. The orientation call
	// has to be cheap enough that an agent can afford to make it before it
	// knows what it wants.
	PagesBySection map[string][]string `json:"pages_by_section,omitempty"`
	// Pages is the listing for one section, which carries a description each.
	// Listing 92 descriptions is the expensive answer this tool exists to
	// avoid, so it is only ever one section's worth.
	Pages []docPageListing `json:"pages,omitempty"`
	// Page is the one page's own table of contents, when path was given.
	Page     *docPageOutline `json:"page,omitempty"`
	NextStep string          `json:"next_step"`
	Note     string          `json:"note,omitempty"`
}

type docSectionCount struct {
	Section string `json:"section"`
	Pages   int    `json:"pages"`
}

type docPageListing struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Sections    int    `json:"sections,omitempty"`
	Chars       int    `json:"chars,omitempty"`
}

type docPageOutline struct {
	Path        string          `json:"path"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Chars       int             `json:"chars"`
	Headings    []docHeadingRow `json:"headings"`
	// HeadingsTotal is the count before any bound, so a page with more
	// headings than are listed says so.
	HeadingsTotal int `json:"headings_total"`
}

type docHeadingRow struct {
	Level  int    `json:"level"`
	Text   string `json:"text"`
	Anchor string `json:"anchor"`
	Lines  string `json:"lines"`
	Chars  int    `json:"chars"`
}

// newListDocsTool builds list_documentation.
func newListDocsTool(p *Project) *Tool {
	return &Tool{
		Name:     "list_documentation",
		Title:    "What the Antifailure documentation covers",
		ReadOnly: true,
		Description: "See what documentation this build ships without reading any of it. " +
			"With no arguments it returns every page path and title grouped by section, " +
			"which is a few hundred tokens and is the cheapest way to orient before " +
			"asking anything. Pass section to get that section's pages with a one line " +
			"description each. Pass path to get one page's table of contents: its " +
			"headings, the anchor for each, and how large each section is, so the next " +
			"call can fetch exactly the part you need. This is the call to make when you " +
			"do not yet know what to search for.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"section": docSectionSchema("Optional. List one section's pages with a " +
					"description each, rather than every page's path and title."),
				"path": docPathSchema("Optional. List one page's headings and anchors " +
					"rather than listing pages."),
			},
		},
		Handler: func(_ context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return listDocumentation(args)
		},
	}
}

func listDocumentation(args map[string]any) (any, *Fault) {
	section, _ := args["section"].(string)
	path, _ := args["path"].(string)
	if section != "" && path != "" {
		// Refused rather than resolved by precedence. A silent winner between
		// two arguments is a caller believing it narrowed something it did
		// not.
		return nil, fieldFault(FaultInvalidArgument, "path",
			"Pass section or path, not both. section lists the pages of one part of the "+
				"documentation; path lists the headings of one page.")
	}

	out := docListDoc{Kind: "documentation_index", PagesTotal: docs.PageCount()}
	switch {
	case path != "":
		page, found := docs.Lookup(path)
		if !found {
			return nil, unknownDocPathFault(path)
		}
		out.Page = outlineOf(page)
		out.Summary = fmt.Sprintf(
			"%s at %s has %d %s and %d characters. Read one of them with "+
				"read_documentation_page and its anchor.",
			docText(page.Title, 200), page.Path, out.Page.HeadingsTotal,
			plural(out.Page.HeadingsTotal, "section", "sections"), out.Page.Chars)
		if out.Page.HeadingsTotal > len(out.Page.Headings) {
			out.Note = fmt.Sprintf(
				"%d of this page's %d headings are listed and %d are not. The page is "+
					"unusually deep; read it in sections rather than whole.",
				len(out.Page.Headings), out.Page.HeadingsTotal,
				out.Page.HeadingsTotal-len(out.Page.Headings))
		}
		out.NextStep = fmt.Sprintf(
			"read_documentation_page path=%q section=<one of the anchors above>.", page.Path)
	case section != "":
		listing := docs.List(section)
		out.Pages = make([]docPageListing, 0, len(listing))
		for _, l := range listing {
			out.Pages = append(out.Pages, docPageListing{
				Path: l.Path, Title: docText(l.Title, 200),
				Description: docText(l.Description, 300), Sections: l.Headings, Chars: l.Chars,
			})
		}
		out.Summary = fmt.Sprintf(
			"%d %s in the %s section, with a description each. Nothing else is withheld: "+
				"this is the whole section.",
			len(out.Pages), plural(len(out.Pages), "page", "pages"), section)
		out.NextStep = "search_documentation with a query, or list_documentation with " +
			"path set to one of these paths for its headings."
	default:
		listing := docs.List("")
		counts := map[string]int{}
		out.PagesBySection = map[string][]string{}
		for _, l := range listing {
			counts[l.Section]++
			out.PagesBySection[l.Section] = append(out.PagesBySection[l.Section],
				l.Path+" "+docText(l.Title, 200))
		}
		listed := 0
		for _, name := range docs.Sections() {
			out.Sections = append(out.Sections, docSectionCount{Section: name, Pages: counts[name]})
			listed += counts[name]
		}
		out.Summary = fmt.Sprintf(
			"%d pages in %d sections, all %d listed here as \"path title\" and none "+
				"withheld. No page content is returned.",
			len(listing), len(out.Sections), listed)
		out.Note = "Descriptions, headings and content are not in this listing. Pass " +
			"section for one section's descriptions, path for one page's headings, or " +
			"call search_documentation to get the lines that answer a question."
		out.NextStep = "search_documentation with what you actually want to know."
	}
	return out, nil
}

func outlineOf(page *docs.Page) *docPageOutline {
	out := &docPageOutline{
		Path: page.Path, Title: docText(page.Title, 200),
		Description: docText(page.Description, 300), Chars: len(page.Body),
		Headings: []docHeadingRow{}, HeadingsTotal: len(page.Headings),
	}
	for _, h := range page.Headings {
		if len(out.Headings) >= maxAnchorsListed {
			break
		}
		out.Headings = append(out.Headings, docHeadingRow{
			Level: h.Level, Text: docText(h.Text, 200), Anchor: h.Anchor,
			Lines: fmt.Sprintf("%d-%d", h.FirstLine, h.LastLine),
			Chars: len(page.Text(h.FirstLine, h.LastLine)),
		})
	}
	return out
}

// -----------------------------------------------------------------------
// read_documentation_page
// -----------------------------------------------------------------------

type docReadDoc struct {
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Section is the heading path of what was returned, or the page title
	// when the whole page was asked for.
	Section string `json:"section"`
	Anchor  string `json:"anchor,omitempty"`
	Lines   string `json:"lines"`
	Content string `json:"content"`
	// Withheld is the account of what was not returned.
	Withheld docReadWithheld `json:"not_returned"`
	// Anchors are every section of this page, bounded, so a truncated read is
	// followed by an exact one.
	Anchors      []string `json:"anchors"`
	AnchorsTotal int      `json:"anchors_total"`
	NextStep     string   `json:"next_step,omitempty"`
}

type docReadWithheld struct {
	Truncated     bool     `json:"truncated"`
	TotalChars    int      `json:"total_chars"`
	ReturnedChars int      `json:"returned_chars"`
	WithheldChars int      `json:"withheld_chars"`
	Sections      []string `json:"sections_not_returned"`
	Note          string   `json:"note,omitempty"`
}

// newReadDocsPageTool builds read_documentation_page.
func newReadDocsPageTool(p *Project) *Tool {
	return &Tool{
		Name:     "read_documentation_page",
		Title:    "Read one documentation page",
		ReadOnly: true,
		Description: "Read one page of this build's documentation, or one section of it. " +
			"Pass section with an anchor from search_documentation or list_documentation " +
			"to get that heading and nothing else, which is almost always what you want: " +
			"a whole page here averages twelve thousand characters and the largest is " +
			"seventy six thousand. The read is bounded by max_chars and a page longer " +
			"than the budget is cut at a line boundary, marked as cut, and reported with " +
			"the exact characters withheld and the anchors of every section past the " +
			"cut, so nothing is dropped silently. A path this build does not ship is " +
			"refused with the nearest paths named, rather than answered with nothing.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id", "path"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"path":       docPathSchema("Required."),
				"section": {
					Type: "string", MaxLength: 200, MinLength: 1,
					Description: "Optional. One heading anchor from this page, such as " +
						"versions-are-immutable. The heading's own text works too. Leave " +
						"it out to read from the top of the page.",
				},
				"max_chars": {
					Type: "integer", HasMin: true, Minimum: minBudgetChars,
					HasMax: true, Maximum: maxReadChars,
					Description: "Optional. The most characters to return, roughly four " +
						"characters per token. The default is 8000.",
				},
			},
		},
		Handler: func(_ context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return readDocumentationPage(args)
		},
	}
}

func readDocumentationPage(args map[string]any) (any, *Fault) {
	path, _ := args["path"].(string)
	anchor, _ := args["section"].(string)
	budget := optionalInt(args, "max_chars", defaultReadChars)

	page, found := docs.Lookup(path)
	if !found {
		return nil, unknownDocPathFault(path)
	}
	var heading *docs.Heading
	if anchor != "" {
		h, ok := page.FindHeading(anchor)
		if !ok {
			// A page that exists and a section it does not have are two
			// different mistakes, and reporting either as the other sends a
			// caller to fix the wrong half of its call.
			return nil, fieldFault(FaultInvalidArgument, "section",
				"The page %s has no section %q. Its sections are: %s.",
				page.Path, clip(anchor, 100), strings.Join(firstN(page.Anchors(), 12), ", "))
		}
		heading = h
	}

	reading := docs.Read(page, heading, budget)
	out := docReadDoc{
		Kind: "documentation_page", Path: reading.Path,
		Title: docText(reading.Title, 200), Description: docText(reading.Description, 300),
		Section: docText(reading.Trail, 300), Anchor: reading.Anchor,
		Lines:   fmt.Sprintf("%d-%d", reading.FirstLine, reading.LastLine),
		Content: docText(reading.Text, maxReadChars+len(docs.CutMarker)+1),
		Withheld: docReadWithheld{
			Truncated: reading.Truncated, TotalChars: reading.TotalChars,
			ReturnedChars: reading.ReturnedChars, WithheldChars: reading.NotReturnedChars,
			Sections: reading.SectionsNotReturned,
		},
		Anchors: firstN(reading.Anchors, maxAnchorsListed), AnchorsTotal: len(reading.Anchors),
	}
	if out.Withheld.Sections == nil {
		out.Withheld.Sections = []string{}
	}

	what := "the whole page"
	if heading != nil {
		what = "the section " + heading.Anchor
	}
	if reading.Truncated {
		out.Withheld.Note = fmt.Sprintf(
			"%d of %d characters were returned and %d were not. The text stops at line "+
				"%d and is marked where it was cut. %s. Raise max_chars, or read one of "+
				"the sections named here on its own.",
			reading.ReturnedChars, reading.TotalChars, reading.NotReturnedChars,
			reading.LastLine, sectionsNotReturnedPhrase(reading.SectionsNotReturned))
		out.Summary = fmt.Sprintf(
			"%s of %s, cut to the %d character budget: %d of %d characters returned.",
			strings.ToUpper(what[:1])+what[1:], reading.Path, budget,
			reading.ReturnedChars, reading.TotalChars)
		out.NextStep = fmt.Sprintf(
			"read_documentation_page path=%q section=<one of sections_not_returned> for "+
				"the rest.", reading.Path)
	} else {
		out.Withheld.Note = fmt.Sprintf(
			"Nothing was withheld: %s is %d characters and all of it is here.",
			what, reading.TotalChars)
		out.Summary = fmt.Sprintf("%s of %s, %d characters, complete.",
			strings.ToUpper(what[:1])+what[1:], reading.Path, reading.TotalChars)
	}
	return out, nil
}

func sectionsNotReturnedPhrase(anchors []string) string {
	if len(anchors) == 0 {
		return "No further heading starts after the cut"
	}
	return fmt.Sprintf("%d %s start after the cut: %s",
		len(anchors), plural(len(anchors), "section", "sections"),
		strings.Join(firstN(anchors, 12), ", "))
}

// unknownDocPathFault refuses a path this build does not ship, and names what
// the caller probably meant.
//
// A refusal rather than an empty result. An empty result for a path that does
// not exist reads exactly like a page with nothing in it, and an agent told
// that goes and invents the content.
func unknownDocPathFault(path string) *Fault {
	near := docs.Near(path, maxNearMisses)
	if len(near) == 0 {
		return fieldFault(FaultInvalidArgument, "path",
			"This build ships no documentation page at %q, and nothing close to it. "+
				"Call list_documentation with no arguments for every page it does ship.",
			clip(path, 120))
	}
	return fieldFault(FaultInvalidArgument, "path",
		"This build ships no documentation page at %q. The closest paths it does ship "+
			"are: %s. Call list_documentation with no arguments for all %d of them.",
		clip(path, 120), strings.Join(near, ", "), docs.PageCount())
}

// firstN bounds a list of short strings for a message, keeping document
// order, which for anchors is the order they appear on the page.
func firstN(in []string, n int) []string {
	if len(in) > n {
		in = in[:n]
	}
	return append([]string{}, in...)
}

// optionalInt reads a bounded integer argument, defaulting when absent.
//
// The schema has already refused anything out of range or not whole, so this
// cannot narrow or widen a bound; it only turns a present value into an int.
func optionalInt(args map[string]any, name string, fallback int) int {
	raw, present := args[name]
	if !present {
		return fallback
	}
	n, err := toInt(raw)
	if err != nil {
		return fallback
	}
	return n
}

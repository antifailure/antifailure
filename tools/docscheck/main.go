// Command docscheck is the first thing in this repository that looks at what
// the documentation site actually renders.
//
// It exists because nothing did. www/scripts/check-seo.mjs asserts the SEO and
// GEO surfaces against www/out and never opens docs/dist, so the documentation,
// which is 76 of the site's roughly 90 pages, had no gate with an opinion about
// its output at all. PR #47 ported the marketing half of the social card and
// entity graph work and left the docs half behind, and all 76 pages shipped
// with two of the eight tags for as long as it took somebody to notice by
// reading the config. Every stage was green the whole time, because no stage
// was looking.
//
// Four things are checked, and they are the four ways this surface goes wrong.
//
// First, the head of every built page. A missing og:image:width is invisible
// until somebody pastes a link into Slack, and by then it has been wrong for
// months.
//
// Second, the cross-site references. The documentation's JSON-LD claims to be
// part of the marketing site's entity graph by naming three @id values that
// www/lib/jsonld.tsx declares. Those are two descriptions of one thing living
// in two languages in two directories, which is the shape this repository keeps
// finding defects in. A reference to an @id nothing declares is not a small
// error, it is the whole point of the tag silently doing nothing, so the ids
// are read out of the TypeScript and matched rather than assumed.
//
// Third, that the article says which page it is. The first version of this
// markup named the three anchors and nothing else: no @id of its own, no url,
// no headline. It passed the check above while telling an engine that an
// article exists here without identifying it, which is the one thing the tag
// was added to do. So the article's own identity is checked against the
// canonical on the same page, and against the title the same head advertises.
// Two claims about which address this is means the engine picks one.
//
// Fourth, that a page asking not to be indexed does not also claim to be an
// indexable technical article. Starlight's head merge deduplicates a meta tag
// by its name or property, which is how the 404's noindex correctly beat the
// config's index directive, but a script tag is not a meta tag and was not
// deduplicated. The 404 shipped saying both things in the same head. That page
// is not an index.html, so the loop above never opened it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The tags every documentation page has to carry, and why each one is here.
//
// og:image alone is not enough: a scraper that has the image still has to
// decide how to lay the card out, and one that cannot fetch it has only
// og:image:alt to describe it with. Starlight emits twitter:card but never
// twitter:image, and Slack, Discord and LinkedIn each read a different subset,
// so the fallback to og:image is not something to rely on.
var required = []struct{ label, needle string }{
	{"og:image", `property="og:image"`},
	{"og:image:width", `property="og:image:width"`},
	{"og:image:height", `property="og:image:height"`},
	{"og:image:alt", `property="og:image:alt"`},
	{"twitter:card", `name="twitter:card"`},
	{"twitter:image", `name="twitter:image"`},
	{"robots", `name="robots"`},
	{"JSON-LD", `application/ld+json`},
}

var ldBlock = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

var (
	canonicalTag = regexp.MustCompile(`<link rel="canonical" href="([^"]*)"`)
	robotsTag    = regexp.MustCompile(`<meta name="robots" content="([^"]*)"`)
	ogTitleTag   = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
)

// ORG_ID, SITE_ID and SOFTWARE_ID in www/lib/jsonld.tsx. They are template
// literals over SITE_URL rather than plain strings, which is why this reads the
// suffix and rebuilds the URL rather than grepping for the whole thing.
var tsID = regexp.MustCompile(`(?m)^const\s+(\w+)\s*=\s*` + "`" + `\$\{SITE_URL\}(/#\w+)` + "`")

var siteURL = regexp.MustCompile(`(?s)export const SITE_URL = \(\s*process\.env\.NEXT_PUBLIC_SITE_URL \?\? "([^"]+)"`)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if flag.NArg() > 0 {
		*root = flag.Arg(0)
	}

	dist := filepath.Join(*root, "docs", "dist")
	if _, err := os.Stat(dist); err != nil {
		// Not a skip. A silent pass when the output is absent is the same gap
		// this tool exists to close: a stage that is green about nothing.
		fmt.Fprintf(os.Stderr, "docscheck: no docs/dist. Build it first:\n\n    (cd docs && npm run build)\n\n")
		os.Exit(1)
	}

	pages, err := collect(dist)
	if err != nil {
		fmt.Fprintln(os.Stderr, "docscheck:", err)
		os.Exit(1)
	}
	if len(pages) == 0 {
		fmt.Fprintln(os.Stderr, "docscheck: docs/dist has no pages, which means the build produced nothing")
		os.Exit(1)
	}

	declared, err := declaredIDs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "docscheck:", err)
		os.Exit(1)
	}

	var problems []string
	referenced := map[string]bool{}

	for _, page := range pages {
		body, err := os.ReadFile(page)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel(*root, page), err))
			continue
		}
		html := string(body)

		for _, tag := range required {
			if !strings.Contains(html, tag.needle) {
				problems = append(problems, fmt.Sprintf("%s: no %s", rel(*root, page), tag.label))
			}
		}

		m := ldBlock.FindStringSubmatch(html)
		if m == nil {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
			problems = append(problems, fmt.Sprintf("%s: JSON-LD does not parse: %v", rel(*root, page), err))
			continue
		}
		for _, key := range []string{"isPartOf", "publisher", "about"} {
			ref, ok := doc[key].(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: JSON-LD has no %s", rel(*root, page), key))
				continue
			}
			id, _ := ref["@id"].(string)
			if id == "" {
				problems = append(problems, fmt.Sprintf("%s: JSON-LD %s has no @id", rel(*root, page), key))
				continue
			}
			referenced[id] = true
			if !declared[id] {
				problems = append(problems, fmt.Sprintf(
					"%s: JSON-LD %s names %s, which www/lib/jsonld.tsx does not declare",
					rel(*root, page), key, id))
			}
		}

		// Which page is this article about? The canonical on the same page is
		// the answer, and the article has to give the same one.
		canonical := first(canonicalTag, html)
		if canonical == "" {
			problems = append(problems, fmt.Sprintf("%s: no canonical", rel(*root, page)))
		} else {
			for _, claim := range []struct{ key, want string }{
				{"@id", canonical + "#techarticle"},
				{"url", canonical},
				{"mainEntityOfPage", canonical},
			} {
				got, _ := doc[claim.key].(string)
				if got != claim.want {
					problems = append(problems, fmt.Sprintf(
						"%s: JSON-LD %s is %q, and the canonical on this page says it should be %q",
						rel(*root, page), claim.key, got, claim.want))
				}
			}
		}

		// And a headline, which is the difference between naming the article
		// and asserting that one exists. og:title is the same head's own answer.
		headline, _ := doc["headline"].(string)
		if title := first(ogTitleTag, html); title != "" && headline != title {
			problems = append(problems, fmt.Sprintf(
				"%s: JSON-LD headline is %q and og:title on the same page is %q",
				rel(*root, page), headline, title))
		}
	}

	// Every built page, not only the index.html files above, because the one
	// this catches is the 404.
	problems = append(problems, noindexArticles(*root, dist)...)

	if len(problems) > 0 {
		sort.Strings(problems)
		// One line per page per tag is unreadable across 76 pages when the
		// cause is one missing entry in one config, so identical findings are
		// counted rather than listed.
		fmt.Fprintf(os.Stderr, "docscheck: %d problems across %d pages\n\n", len(problems), len(pages))
		for _, line := range dedupe(problems) {
			fmt.Fprintln(os.Stderr, "  "+line)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	ids := make([]string, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Printf("docscheck: %d pages, %d head tags each, each article naming its own canonical, "+
		"%d entity references all declared in www\n", len(pages), len(required), len(ids))
}

// first returns the single capture of re in html, or "".
func first(re *regexp.Regexp, html string) string {
	if m := re.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}

// noindexArticles finds pages that ask not to be indexed and claim to be an
// indexable technical article in the same head. A crawler resolves the
// contradiction whichever way it likes, and we do not get to know which.
func noindexArticles(root, dist string) []string {
	var problems []string
	_ = filepath.WalkDir(dist, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".html") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel(root, path), err))
			return nil
		}
		html := string(body)
		if !strings.Contains(first(robotsTag, html), "noindex") {
			return nil
		}
		if ldBlock.MatchString(html) {
			problems = append(problems, fmt.Sprintf(
				"%s: asks not to be indexed and carries JSON-LD claiming an article", rel(root, path)))
		}
		return nil
	})
	return problems
}

// dedupe folds "page: no og:image:width" across many pages into one line with a
// count, keeping the first page as an example.
func dedupe(problems []string) []string {
	type entry struct {
		example string
		n       int
	}
	order := []string{}
	seen := map[string]*entry{}
	for _, p := range problems {
		key := p
		if i := strings.Index(p, ": "); i >= 0 {
			key = p[i+2:]
		}
		if e, ok := seen[key]; ok {
			e.n++
			continue
		}
		seen[key] = &entry{example: p, n: 1}
		order = append(order, key)
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		e := seen[key]
		if e.n == 1 {
			out = append(out, e.example)
			continue
		}
		out = append(out, fmt.Sprintf("%s (on %d pages, for example %s)", key, e.n, strings.SplitN(e.example, ": ", 2)[0]))
	}
	return out
}

func collect(dist string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dist, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "index.html" {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// declaredIDs reads the entity ids the marketing site actually declares.
func declaredIDs(root string) (map[string]bool, error) {
	site, err := os.ReadFile(filepath.Join(root, "www", "lib", "site.ts"))
	if err != nil {
		return nil, fmt.Errorf("reading www/lib/site.ts: %w", err)
	}
	m := siteURL.FindSubmatch(site)
	if m == nil {
		return nil, fmt.Errorf("www/lib/site.ts: could not find the SITE_URL fallback, so the @id values cannot be rebuilt")
	}
	base := strings.TrimSuffix(string(m[1]), "/")

	ld, err := os.ReadFile(filepath.Join(root, "www", "lib", "jsonld.tsx"))
	if err != nil {
		return nil, fmt.Errorf("reading www/lib/jsonld.tsx: %w", err)
	}
	found := tsID.FindAllStringSubmatch(string(ld), -1)
	if len(found) == 0 {
		return nil, fmt.Errorf("www/lib/jsonld.tsx: no `${SITE_URL}/#...` constants, so the graph has moved and this check is stale")
	}
	out := map[string]bool{}
	for _, f := range found {
		out[base+f[2]] = true
	}
	return out, nil
}

func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

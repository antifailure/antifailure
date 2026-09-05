# SEO and GEO catalog: 200 techniques, and where Antifailure stands on each

Research date: 2026-08-29. Two lists, 100 each, no item repeated across them.
SEO covers classic crawl/index/rank/authority. GEO covers being retrieved and
cited by generative engines: ChatGPT, Claude, Perplexity, Gemini, AI Overviews,
AI Mode, Copilot.

Status column: `done` shipped in this repo, `n/a` genuinely does not apply,
`owner` needs an account or an outward-facing action only Vir can take.

Sources for the research are listed at the bottom. The single most important
empirical input is the Princeton/IIT-Delhi GEO paper (KDD '24), which measured
nine content strategies over 10,000 queries; its numbers appear in GEO 41-49
and are the only citation-lift figures here that come from a controlled
experiment rather than vendor blogging.

---

## Part 1: 100 SEO techniques

### A. Crawl and index foundations

1. Serve `robots.txt` from the origin root with an explicit `Sitemap:` line.
2. Generate the XML sitemap from the route manifest, never a hand-kept list.
3. Drive sitemap `lastmod` from real content change time, not build time.
4. Split sitemaps by section and reference them from one index when a site
   spans separate apps (marketing plus docs on one origin).
5. Point `robots.txt` at every sitemap the origin serves, including the docs
   app's, so one file is enough for a crawler to find everything.
6. Self-referencing `<link rel="canonical">` on every indexable page.
7. Set an absolute `metadataBase` so canonical, OG and image URLs resolve to
   absolute URLs instead of paths.
8. Pick one trailing-slash policy and enforce it; mixed policies split signals.
9. Consolidate apex and `www` with a single permanent redirect, one hop.
10. Force HTTPS and send HSTS; HTTPS is a ranking signal and a trust floor.
11. Return a real 404 status on unknown routes, never a soft 200.
12. Give the 404 page real navigation so a crawler that lands there continues.
13. Use 301 for retired URLs; 302 tells the engine to keep the old one.
14. Collapse redirect chains: every legacy URL reaches its destination in one hop.

### B. Indexation control

15. `noindex` utility routes (sign-in, sign-up, error) so index budget goes to content.
16. Send `X-Robots-Tag` for non-HTML responses that should not be indexed.
17. Set `max-image-preview:large` and `max-snippet:-1` to allow rich previews.
18. Keep preview and staging deployments out of the index.
19. Verify the property in Google Search Console.
20. Verify the property in Bing Webmaster Tools.
21. Publish an IndexNow key and ping on deploy.
22. Submit the sitemap in both consoles, not just Google's.

### C. On-page

23. Unique title per route, primary term front-loaded, under about 60 characters.
24. A title template in the root layout plus an absolute default, so no route
    can ship untitled.
25. Unique meta description per route under about 155 characters, stating the
    differentiator rather than restating the title.
26. Exactly one `<h1>` per page, matching the query intent of the route.
27. Heading hierarchy with no skipped levels; headings chosen for structure, not size.
28. Lowercase hyphenated slugs that read as the topic and never change.
29. One page per query cluster, so pages do not cannibalise each other.
30. Semantic landmarks: `main`, `nav`, `article`, `section`, `header`, `footer`.
31. Descriptive anchor text; never "learn more" or "click here".
32. Alt text on every meaningful image; `aria-hidden` on decorative ones.
33. `lang` on `<html>`.
34. Complete Open Graph set: type, title, description, url, site_name, locale,
    image, image:alt, image:width, image:height.
35. Twitter card `summary_large_image` with site and creator handles.
36. Full icon set: SVG icon, apple-touch-icon, PNG fallbacks.
37. A web app manifest with name, short_name, theme_color and icons.
38. `theme-color` and `color-scheme` meta so the browser chrome matches.

### D. Structured data

39. `Organization` JSON-LD with name, url, logo and `sameAs`.
40. `WebSite` JSON-LD with a `SearchAction` for the sitelinks search box.
41. `SoftwareApplication` JSON-LD for the product itself.
42. `SoftwareSourceCode` with `codeRepository` and `programmingLanguage`.
43. `BreadcrumbList` JSON-LD on nested routes, with visible breadcrumbs to match.
44. `FAQPage` JSON-LD wherever real question and answer pairs exist.
45. `TechArticle` or `HowTo` on documentation guides.
46. `Offer` and `PriceSpecification` on the pricing page.
47. One `@id`-linked entity graph so Organization, WebSite and WebPage resolve
    to each other instead of floating as three unrelated blobs.
48. A `sameAs` array covering every owned profile.
49. `datePublished` and `dateModified` on documentation, from git history.
50. Validate every JSON-LD block in CI so a refactor cannot silently break it.

### E. Performance and Core Web Vitals

51. Identify the LCP element per template and preload it.
52. `fetchpriority="high"` on the LCP image; lazy-load everything below the fold.
53. Explicit width and height on every image, video and iframe to hold layout.
54. AVIF or WebP with `srcset` and `sizes`.
55. Fonts with `display: swap`, preloaded with `crossorigin`.
56. Subset fonts to the scripts actually used and drop unused weights.
57. Keep the family count low; each extra family is another critical download.
58. Inline critical CSS and defer the rest.
59. Code-split heavy client components; dynamically import below-fold WebGL.
60. Break up long main-thread tasks so INP stays under 200 ms.
61. Preserve bfcache eligibility: no `unload` handlers, no `no-store` on HTML.
62. Speculation Rules to prefetch or prerender the likely next navigation.
63. `preconnect` only to origins actually used on the critical path.
64. Immutable long-lived cache headers on content-hashed assets.
65. Brotli on HTML, CSS and JS.
66. Honour `prefers-reduced-motion`, which also removes animation work from INP.

### F. Architecture and internal linking

67. Every page within three clicks of the homepage.
68. Zero orphan pages; every route is linked from somewhere crawlable.
69. Pillar pages that link to every spoke in their cluster.
70. Bidirectional hub and spoke linking, not just hub to spoke.
71. Contextual in-body cross-links between sibling pages.
72. A footer link block that covers the long tail the nav cannot.
73. A related-pages module at the end of each cluster page.
74. Visible breadcrumbs that match the BreadcrumbList markup.
75. Deep links from marketing into specific docs pages, not just the docs root.
76. Links from docs back to the relevant marketing pages.
77. Vary anchor text across exact, partial and semantic match.
78. Assert crawl depth in CI so a new route cannot land orphaned.

### G. Content and E-E-A-T

79. A comparison page per real alternative.
80. "Alternatives to X" pages for the incumbents in the category.
81. Solution pages per vertical and per stack.
82. Integration pages per provider the product supports.
83. Glossary pages defining the category's vocabulary.
84. A changelog, which is both a freshness signal and long-tail surface.
85. `Person` schema with real credentials on anything with a byline.
86. A company page that says who is behind the product.
87. Original research or benchmarks that nobody else can publish.
88. A security page and other trust surfaces.
89. Real, specific, sourced numbers instead of round vanity stats.
90. A refresh cadence that bumps `dateModified` only on substantive edits.

### H. Off-page and measurement

91. Up to 20 GitHub topics chosen for the category terms people search.
92. A GitHub About description and homepage URL pointing at the site.
93. A 1280x640 GitHub social preview image.
94. Submissions to the relevant awesome-lists.
95. One brand name and one boilerplate description used identically everywhere.
96. Report on search queries from the Search Console API, not pageviews.
97. Monitor Core Web Vitals from field data, not only lab Lighthouse runs.
98. Check links in CI, internal and external.
99. Regression-test metadata and structured data in CI.
100. Keep a committed audit checklist and re-run it quarterly.

---

## Part 2: 100 GEO techniques

### A. AI crawler access

1. Allow `GPTBot` (OpenAI training and grounding).
2. Allow `OAI-SearchBot` (the retrieval crawler behind ChatGPT search).
3. Allow `ChatGPT-User` (user-triggered fetch during a conversation).
4. Allow `ClaudeBot`.
5. Allow `Claude-SearchBot` and `Claude-User`.
6. Allow `PerplexityBot` and `Perplexity-User`.
7. Allow `Google-Extended`, which gates Gemini grounding independently of Googlebot.
8. Take an explicit documented position on `Applebot-Extended`, `CCBot`,
   `Meta-ExternalAgent`, `Bytespider`, `Amazonbot`, `cohere-ai`, `Diffbot`,
   `TimpiBot` and `YouBot` rather than leaving them to a default.
9. Confirm the CDN or WAF is not blocking those agents underneath robots.txt.
   Cloudflare began blocking Training and Agent categories by default for new
   domains on 2026-09-15, so an allow in robots.txt can be silently overridden.
10. Do not set a `Crawl-delay` so high that retrieval crawlers give up.
11. Serve complete HTML without client-side rendering; most AI crawlers do not
    execute JavaScript, so a client-rendered fact is an invisible fact.
12. No cookie wall or interstitial in front of content a crawler must read.

### B. Machine-readable surfaces

13. `/llms.txt`: a markdown index of the pages that matter, one line each.
14. `/llms-full.txt`: the full corpus concatenated for single-fetch ingestion.
15. A `.md` twin for every page, serving the raw markdown source.
16. Content negotiation returning `text/markdown` when the Accept header asks.
17. `<link rel="alternate" type="text/markdown">` pointing at the twin.
18. A machine-readable API description (OpenAPI) linked from `llms.txt`.
19. Public JSON for machine-consumable catalogs such as the error catalog.
20. An MCP server or agent skill so assistants can drive the product directly.
21. An RSS or Atom feed for the changelog.
22. A stable, documented plain-text install URL.
23. Emit JSON-LD in the server HTML, never inject it from client JavaScript.
24. Reference the machine-readable surfaces from the sitemap and robots.txt.

### C. Passage-level structure

25. Open each section with a 40 to 60 word direct answer, then elaborate.
26. Aim for self-contained passages of roughly 134 to 167 words, the measured
    extraction window for AI Overviews.
27. Write H2 and H3 headings as the question a person would actually type.
28. Make each section stand alone; a chunk that needs the previous chunk loses.
29. Keep paragraphs to two or three lines. Engines lift blocks, not narrative.
30. Put comparisons in real `<table>` markup with header cells.
31. Put enumerable facts in `<ul>` or `<ol>`, not comma runs inside a sentence.
32. Use `<dl>` for term and definition pairs.
33. Bold the load-bearing claim in a block so extraction selects the right span.
34. One idea per heading, so a chunk boundary cannot cut a claim in half.
35. Inverted pyramid: conclusion first. Early text is weighted more heavily, and
    44% of citations come from the first 30% of a response.
36. Repeat the entity name instead of using pronouns, so a chunk read in
    isolation still knows what it is about.
37. Never put a load-bearing fact only inside an image or an unlabelled SVG.
38. Never hide required content behind tabs or accordions that are absent from
    the server HTML.
39. A TL;DR block at the top of any long page.
40. A "what this page answers" list mapping the page to real questions.

### D. Citation-worthiness, from the Princeton GEO experiment

41. Quotation Addition: quote authoritative sources verbatim. +41% position-adjusted word count, the largest measured lift of the nine.
42. Statistics Addition: add concrete quantitative data. +33%.
43. Cite Sources: inline citations with links. +28%.
44. Fluency Optimization: clean, readable prose. +29%.
45. Technical Terms: use the domain's real vocabulary. +19%.
46. Authoritative tone: declarative claims. +12% PAWC and +17% subjective impression.
47. Easy-to-Understand: plain explanation alongside the technical depth. +14%.
48. Do not keyword stuff. It measured **−9%**, the only negative result in the study.
49. Deprioritise the unique-words tactic at +6%; it distorts copy for nothing.
50. Publish original benchmark data that others have to cite back to you.
51. Name and define your own concepts; a coined term becomes an entity anchor.
52. Attribute every number to a method so it survives an engine's fact check.

### E. Entity and knowledge graph

53. A Wikidata item with a QID for the product or company.
54. `sameAs` linking every owned profile from the Organization entity.
55. One boilerplate sentence, reused verbatim on every surface.
56. One spelling and casing of the product name, everywhere.
57. A single entity-home page that every other profile points back to.
58. Complete, cross-linked profiles on Crunchbase, LinkedIn, X and GitHub.
59. Explicit disambiguation copy when the name collides with anything.
60. State the category outright rather than implying it.
61. Founder and author `Person` entities linked to the Organization.
62. Fill `knowsAbout` and `applicationCategory`.
63. Identical description in the GitHub About, `package.json`, docs and site.
64. A logo at a stable URL that the schema references.

### F. Presence in the corpora AI engines actually cite

65. Reddit. The most-cited domain across ChatGPT, Gemini, Perplexity, AI Mode
    and AI Overviews, and roughly 46.7% of Perplexity's top-10 source share.
66. Stack Overflow and Stack Exchange, heavily weighted for developer queries.
67. Hacker News, high weight for technical queries.
68. G2 and Capterra; Perplexity favours review aggregators for commercial intent.
69. YouTube, a top-five cited domain, with real transcripts.
70. LinkedIn, also top five.
71. Syndicated technical posts with a canonical pointing home.
72. Awesome-list inclusion, since GitHub is crawled heavily.
73. Legitimately earned third-party comparison coverage.
74. Podcast and interview appearances, whose transcripts get indexed.
75. A Wikipedia mention only where genuinely notable, never self-edited.
76. Real answers in GitHub Discussions and Issues, which are indexed.

### G. Prompt-space targeting

77. Research prompts, not keywords: the questions people ask an assistant.
78. Cover the fan-out sub-questions, not just the head query. Pages ranking for
    fan-out queries are 161% more likely to be cited than pages ranking only
    for the visible query.
79. "Best X for Y" pages, which is what recommendation sets are drawn from.
80. Head-to-head "X vs Y" pages with honest trade-offs.
81. "Alternatives to X" pages.
82. Task pages: "how to do X with Y".
83. Honest negative framing: when not to use this. Trust citations follow.
84. A transparent pricing page, which is where budget answers come from.
85. Explicit "who this is for, and who it is not for" blocks.
86. Product specification and feature-matrix pages. Product content earns about
    70% of B2B AI citations; blog content earns under 6%.
87. Objection-handling FAQ phrased the way objections are actually voiced.
88. Migration guides from each named incumbent.

### H. Freshness and measurement

89. `dateModified` in schema on every content page.
90. `lastmod` in the sitemap, driven by real change.
91. A correct `Last-Modified` response header.
92. A visible "last updated" date in the interface.
93. Substantive updates only. Engines discount date bumps and synonym swaps.
94. A dated changelog as a recurring freshness signal.
95. Ping IndexNow on publish. Bing's index is the retrieval layer for ChatGPT
    search and Copilot, and a page Bing has not indexed cannot be cited there.
96. Verify presence in Bing's index specifically, separately from Google's.
97. Run a fixed prompt set weekly across the major assistants and log citations.
98. Track citation share of voice against named competitors.
99. Log AI crawler hits server-side to prove the bots actually fetch pages.
100. Segment referral traffic from assistant domains separately in analytics.

---

## Sources

Princeton/IIT-Delhi GEO paper: https://arxiv.org/abs/2311.09735 and
https://dl.acm.org/doi/10.1145/3637528.3671900
AI crawler user agents: https://www.honeyb.ai/blog/ai-crawler-user-agents-reference-2026
and https://www.cite.sh/blog/ai-crawler-guide/
Cloudflare AI crawl control: https://blog.cloudflare.com/content-independence-day-ai-options/
llms.txt adoption data: https://codersera.com/blog/llms-txt-complete-guide-2026/
Most-cited domains: https://www.semrush.com/blog/most-cited-domains-ai/ and
https://searchengineland.com/ai-search-engines-cite-reddit-youtube-and-linkedin-most-study-473138
Query fan-out: https://ipullrank.com/how-ai-mode-works and
https://www.semrush.com/blog/query-fan-out/
Bing as ChatGPT's retrieval index: https://www.stackmatix.com/blog/bing-webmaster-tools-chatgpt
Content freshness: https://takeagander.ai/resources/gander-blog/how-content-freshness-drives-visibility-in-ai-search/
B2B product-content citation share: https://guptadeepak.com/winning-the-ai-shortlist-geos-70-product-content-advantage/
Core Web Vitals 2026: https://www.debugbear.com/blog/technical-seo-checklist
Entity SEO: https://www.digitalapplied.com/blog/entity-seo-knowledge-graph-optimization-guide-2026
Next.js metadata: https://nextjs.org/docs/app/api-reference/functions/generate-metadata
GitHub repo discoverability: https://nakora.ai/blog/github-seo

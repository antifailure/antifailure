# What was applied, and what is left

Companion to [seo-geo-catalog.md](seo-geo-catalog.md), which lists the 200
techniques. This is the ledger: what shipped in code, what needs an account
somebody has to log into, and what was deliberately not done.

## Where production stood before

Measured against the live site on 2026-08-29, not inferred from the source.

| Surface | Before |
| --- | --- |
| OpenGraph tags | 0 |
| Twitter card tags | 0 |
| `rel="canonical"` | 0 |
| JSON-LD | 0 |
| `/robots.txt` | 404 |
| `/sitemap.xml` | 404 |
| `/llms.txt` | 404 |
| `/og.png` | 404, and every one of the 41 docs pages referenced it |
| `/manifest.webmanifest` | 404 |
| Home page HTML | 310 KB |
| Hero art | 9 PNGs, 15.4 MB, one of them preloaded at 1.9 MB |

The live `<head>` contained a title, a description, and an icon. Nothing else.
A link to antifailure.dev pasted anywhere rendered as a bare URL.

## Shipped in this change

**Crawl and index.** `app/robots.ts`, `app/sitemap.ts`, `app/manifest.ts`.
The sitemap is generated from `lib/routes.ts`, so a route that exists in the
app and not in the registry fails `npm run check:seo` rather than quietly
going missing. `lastmod` comes from the git commit that last touched the files
behind each route, not the build clock.

**Per-page metadata.** `lib/seo.ts` builds canonical, OpenGraph, Twitter card
and robots directives for all 30 indexable routes from one registry. Includes
`max-image-preview:large` and `max-snippet:-1`, which are opt-ins.

**Structured data.** `lib/jsonld.tsx` emits one linked `@graph`:
Organization, WebSite and a combined SoftwareApplication/SoftwareSourceCode
node, each with a stable `@id` the others reference. Per page: WebPage plus
BreadcrumbList. FAQPage on `/product`, generated from the same array that
renders the visible answers.

**Breadcrumbs.** `components/layout/Breadcrumbs.tsx`, rendered by `PageHero`
on all 29 page instances, so the BreadcrumbList markup describes something a
reader can actually see.

**AI crawler access.** `robots.txt` names 26 agents in three groups: search
and retrieval, training and dataset, and the wildcard. All allowed, with the
reasoning written into the file rather than left implicit.

**Machine-readable surfaces.** `/llms.txt` (site index), `/llms-full.txt`,
`/docs/llms-full.txt` (all 41 docs pages, 219 KB, generated from the markdown
they already are), and a `.md` twin of every page linked by
`<link rel="alternate" type="text/markdown">`. The twins are extracted from
`<main>` in the built HTML, so they cannot drift from what shipped. 4.5 MB of
HTML becomes 72 KB of markdown.

**Images.** `scripts/optimize-images.mjs` encodes AVIF and WebP at two widths
from sources that now live outside `public/`. 15.4 MB becomes 936 KB total,
a 97.9% reduction. The preloaded hero goes from 1.9 MB to 27 KB.
`components/Picture.tsx` serves them with a `<picture>` element. Compression
was checked by diffing 1:1 crops of the one image containing text, not by
trusting the ratio.

**The social card.** `scripts/make-og.mjs` generates `public/og.png` at
1200x630 and `assets/github-social-preview.png` at 1280x640.

**Host configuration.** `public/staticwebapp.config.json`: immutable cache
headers on hashed assets, correct content types for `.md` and the text
endpoints, the `/product/crowdi` 301, a real 404 status, and security headers.

**Build integrity.** `scripts/check-seo.mjs`
runs 28 assertions against the built output and is wired into `deploy.yml`.
`fetch-depth: 0` added to the checkout so sitemap dates are real.

`output: "export"` was missing when this work started, so `next build` wrote
`.next` while `deploy.yml` asserts `test -d www/out`, and nothing could
deploy. It was restored independently on `hosted-loop` while this branch was
in progress, and that version is the one kept here; only a pointer to
`public/staticwebapp.config.json` was added to it.

**Repository.** Homepage URL set (it was empty), description aligned to the
one canonical sentence, topics expanded from 13 to the full 20, Discussions
enabled, unused wiki disabled. README gained badges, a category-positioning
table, a "when not to use this" section and eight questions with answers, all
of which restate claims this repository already makes elsewhere.

**Documentation site.** `og:image` now resolves. Added `og:image:width`,
`og:image:height`, `og:image:alt`, `twitter:image`, robots preview directives,
and a TechArticle node tied to the same Organization `@id` as the marketing
site.

## Needs an account. Cannot be done from here.

These are the items in the catalog that require logging in as somebody. Each
one is a real technique from the list, not a gap in the work.

1. **Upload the GitHub social preview.** `assets/github-social-preview.png` is
   generated and correct at 1280x640. There is no REST endpoint for this.
   Settings > General > Social preview > Edit > Upload an image. Until then
   GitHub serves an auto-generated grey card. (SEO 93)
2. **Google Search Console.** Verify the property, submit the sitemap. (SEO 19, 22)
3. **Bing Webmaster Tools.** Verify and submit. This one matters more than it
   looks: Bing's index is the retrieval layer behind ChatGPT search and
   Copilot, so a page Bing has not indexed cannot be cited there. (SEO 20, GEO 96)
4. **IndexNow key.** Generate a key, serve it at the root, ping on deploy.
   (SEO 21, GEO 95)
5. **Wikidata item.** Create it, then add the QID to `SAME_AS` in
   `lib/site.ts`. (GEO 53)
6. **Owned profiles.** LinkedIn, X, Crunchbase. Create them with the identical
   boilerplate from `lib/site.ts`, then add each URL to `SAME_AS`. The array
   currently holds only the GitHub URL, deliberately: a `sameAs` pointing at a
   404 is worse than a short list. (GEO 54, 58)
7. **Presence in the corpora that get cited.** Reddit is the most-cited domain
   across every major engine and roughly 46.7% of Perplexity's top-10 source
   share; Stack Overflow, Hacker News, YouTube and LinkedIn follow. None of
   this can or should be automated, and posting as somebody else would be
   worse than not posting. (GEO 65-76)
8. **Awesome-list submissions.** (SEO 94, GEO 72)

## Deliberately not done

- **Named competitor comparison pages.** Product content earns roughly 70% of
  B2B AI citations against under 6% for blogs, so these are the highest-value
  content type in the catalog (GEO 79-82, 88; SEO 79-80). The README now
  carries a category-level positioning table, which is honest because it
  compares against categories rather than making claims about specific
  vendors' behavior. Per-vendor pages need every claim verified against that
  vendor's own current documentation, and asserting something outdated about a
  named competitor is worse than publishing nothing. This is the single
  largest remaining item and it wants a dedicated pass.
- **hreflang and internationalisation** (SEO catalog, section C). One locale,
  no translations. Genuinely not applicable.
- **`Accept: text/markdown` content negotiation** (GEO 16). Needs a server.
  The site is a static export, so the `.md` twins are served as files instead,
  which reaches the same consumers.
- **`Last-Modified` response headers** (GEO 91). Set by the host, not by the
  build.

## Known issues found along the way, outside this scope

- `www/auth.ts` was deleted. It imported `next-auth`, which was not in
  `package.json`, so the build did not compile. It had zero call sites, and
  the comment at the top of `components/AuthScreen.tsx` explains that the
  OAuth sign-in was deliberately removed for promising an account that does
  not exist. Wiring it up would have rebuilt exactly that.
- `components/layout/MarketingPage.tsx` and `InnerPage.tsx` have no importers.
  Left in place; they are dead but harmless, and deleting them was not this
  change's business.
- `cta-atmosphere.png` and `sage-noise.png` are referenced nowhere. 3.5 MB
  that was being deployed for nothing. They are kept in `www/assets/hero/` and
  simply not encoded.
- The site's mega-menu renders in plain divs with no `<nav>` or `<header>`
  ancestor. That is a semantic-landmark gap and an accessibility issue
  (SEO 30). Worked around here rather than fixed: the markdown twins scope to
  `<main>` instead of trying to exclude chrome.
- The live deployment is stale. It serves `/home/avatar-maya.jpg`, which no
  current source file references. Consistent with `deploy.yml`'s own note that
  the publish step has never run.
- `www/.next.broken.1787887261` (212 MB) and `www/node_modules.broken.*`
  (14 MB) are salvage directories from an interrupted install. Now gitignored;
  safe to delete.

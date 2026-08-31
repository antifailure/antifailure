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
| `/og.png` | 404, and every one of the 57 docs pages referenced it |
| `/manifest.webmanifest` | 404 |
| Hero art | 10 PNGs, 15.55 MB, one of them preloaded at 1.9 MB |

The live `<head>` contained a title, a description, and an icon. Nothing else.
A link to antifailure.dev pasted anywhere rendered as a bare URL.

## Shipped in this change

**Crawl and index.** `app/robots.ts`, `app/sitemap.ts`, `app/manifest.ts`.
The sitemap is generated from `lib/routes.ts`, so a route that exists in the
app and not in the registry fails `npm run check:seo` rather than quietly
going missing. `lastmod` comes from the git commit that last touched the files
behind each route, not the build clock.

**Per-page metadata.** `lib/seo.ts` builds canonical, OpenGraph, Twitter card
and robots directives for all 25 indexable routes from one registry. Includes
`max-image-preview:large` and `max-snippet:-1`, which are opt-ins.

**Structured data.** `lib/jsonld.tsx` emits one linked `@graph`:
Organization, WebSite and a combined SoftwareApplication/SoftwareSourceCode
node, each with a stable `@id` the others reference. Per page: WebPage plus
BreadcrumbList. FAQPage on `/product`, generated from the same array that
renders the visible answers. BlogPosting per post, referencing the same
Organization and the same WebPage rather than declaring its own: an inline
node is a new entity to a consumer, and three posts spelling out their own
author would have put three more Antifailures on a domain whose whole purpose
here is to resolve to one. `check:seo` asserts a BreadcrumbList on every
indexable page below the root, which is how the posts were caught rendering a
visible trail and describing none of it.

**Breadcrumbs.** `components/layout/Breadcrumbs.tsx`, rendered by `PageHero`
from a single `path` prop on all 21 page instances, so the BreadcrumbList
markup describes something a reader can actually see.

**AI crawler access.** `robots.txt` names 26 agents in three groups: search
and retrieval, training and dataset, and the wildcard. All allowed, with the
reasoning written into the file rather than left implicit.

**Machine-readable surfaces.** `/llms.txt` (site index), `/llms-full.txt`,
`/docs/llms-full.txt` (all 57 docs pages, 393 KB, generated from the markdown
they already are), and a `.md` twin of every page linked by
`<link rel="alternate" type="text/markdown">`. The twins are extracted from
`<main>` in the built HTML, so they cannot drift from what shipped. 3.5 MB of
HTML becomes 72 KB of markdown across 25 pages.

The chrome filter dropped `<header>` and `<footer>` unconditionally, which is
right for site chrome and wrong inside `<main>`, where they are an article's
own furniture. The three blog posts put their h1, date, tags and standfirst in
`<article><header>` and lost all four. The twins still looked correct, because
a missing heading was silently replaced with the `<title>`, site-name suffix
and all. Scoping to `<main>` already excludes the site header and footer, so
only `nav` is dropped inside it now, and `check:seo` asserts every twin opens
with the page's own h1: a twin missing a section is worse than no twin,
because nothing about it looks wrong.

**Images.** `scripts/optimize-images.mjs` encodes AVIF and WebP at two widths
from sources that now live outside `public/`. 15.55 MB of PNG in `public/`
becomes 1.14 MB of AVIF and WebP across both widths: the eight referenced
sources are 12.1 MB and the two nothing references are 3.46 MB that simply
stop being deployed. The preloaded hero goes from 1.91 MB to 35 KB, or 9 KB
on a phone. `components/Picture.tsx` serves them with a `<picture>` element.
Compression was checked by diffing 1:1 crops of the one image containing
text, not by trusting the ratio.

`assets/hero/art.json` is the one place each source's pixel size is written
down, because both the encoder and the component need it and they must not
disagree: `footer-aurora.png` is 1024x768 while everything else is 1536x1024,
and a srcSet candidate advertised as 1536w that is really 1024w makes a
browser download the wrong one. The encoder checks every entry against the
file on disk and fails the build when they drift.

`HeroFilm` renders the same aurora twice, once for the desktop frame and once
for phones, and only one is ever visible. `hidden` is a paint instruction and
not a fetch one, so the two share a single `sizes` string: agreeing means both
resolve to the same candidate and a phone makes one request instead of two.

**The social card.** `scripts/make-og.mjs` generates `public/og.png` at
1200x630 and `assets/github-social-preview.png` at 1280x640.

**Host configuration.** `public/staticwebapp.config.json`: immutable cache
headers on hashed assets, correct content types for `.md` and the text
endpoints, the `/product/crowdi` 301, a real 404 status, and security headers.

**Build integrity.** `scripts/check-seo.mjs` runs 32 assertions against the
built output and is wired into `deploy.yml`. `fetch-depth: 0` added to the
checkout so sitemap dates are real.

The `lastmod` check took two wrong forms before this one, and both are worth
recording because they are the same mistake mirrored. Requiring several
distinct dates fails a repository where every page legitimately changed in one
commit, which is exactly what happened the first time these files were
committed together. Rejecting dates close to now fails the opposite way: on a
push, the commit being deployed is minutes old, so a real date and the build
clock are indistinguishable. What actually separates them is that a date read
out of git can never be newer than the commit being built, so HEAD's own date
is the ceiling.

`lib/lastmod.ts` no longer falls back to the build clock in CI at all. It
throws, with the `fetch-depth` fix in the message. A silent fallback is
indistinguishable downstream from a real date and produces exactly the
everything-changed-today lie the module exists to remove; locally it stays a
warning, because a page being written has legitimately never been committed.

The case that needed naming is the shallow checkout, and it is the one every
version of this missed. `actions/checkout` defaults to a depth of 1, and git
then answers perfectly well: there is one commit, every file looks like it was
added in it, and `git log -1` returns that commit's date for all of them.
Nothing throws, nothing is empty, and no downstream check can tell the result
from the truth, because it IS a real timestamp. So the module asks
`git rev-parse --is-shallow-repository` outright. `fetch-depth: 0` is now on
the `www` job in `ci.yml` as well as on `deploy.yml`, and it is load bearing
rather than a convenience.

Both refusals were proven by running the build against a `git` that lies:
one that cannot answer at all, and one that reports a shallow repository. In
both the export stops on `/sitemap.xml` and prints what to fix.

**Repository.** Homepage URL set (it was empty), description aligned to the
one canonical sentence, topics expanded from 13 to the full 20, Discussions
enabled, unused wiki disabled. README gained badges, a category-positioning
table, a "when not to use this" section and eight questions with answers, all
of which restate claims this repository already makes elsewhere.

**Documentation site.** `og:image` now resolves. Added `og:image:width`,
`og:image:height`, `og:image:alt`, `twitter:image`, robots preview directives,
and a TechArticle node tied to the same Organization `@id` as the marketing
site.

## The blog, and blog.antifailure.dev

The writing lives at `antifailure.dev/blog`. `blog.antifailure.dev` resolves and
sends one 301 there.

That split is deliberate. A subdomain is commonly treated as a separate site,
so a blog on one starts from nothing and earns authority that never reaches the
product pages. A subfolder shares the domain outright, which for a domain
registered this year is most of the available upside. Keeping the subdomain
working costs nothing and means a link somebody has already shared does not
break.

What was built:

- `/blog` and `/blog/<slug>`, static, with three posts whose every factual
  claim about the product restates something this repository already says.
- Posts are registered in `lib/blog.ts` and appended to the route registry, so
  the sitemap, `llms.txt`, `llms-full.txt`, the breadcrumb trail, the markdown
  twins and `check:seo` all cover them without any of those being taught what
  a blog is. Adding a post to one file reaches all of them.
- `BlogPosting` JSON-LD per post, linked to the same Organization and WebSite
  `@id` as the rest of the site, with `datePublished` and `dateModified`.
- `og:type: article` with `publishedTime`, which is what puts a date into a
  link preview.
- An RSS feed at `/blog/rss.xml`. Unfashionable and cheap, and the things that
  still read one, aggregators and crawlers learning a site has published, are
  worth reaching.

Infrastructure, in Azure rather than GoDaddy: the `antifailure.dev` zone is
Azure DNS in the `af-web` resource group, and GoDaddy is at most the registrar.

- `af-blog`, a second Free Static Web App, serving only a 301. `af-site` is on
  the Free tier, which allows two custom domains, and the apex and `www` use
  both.
- A `blog` CNAME to that app, and `blog.antifailure.dev` bound to it with a
  managed certificate.
- `deploy/blog-redirect/` holds the two files that app serves, so the redirect
  is version controlled rather than living only in a portal.

## IndexNow

Shipped. `www/public/<key>.txt` is the ownership proof, `scripts/indexnow.mjs`
submits, and `deploy.yml` runs it after a successful publish and never before.

Only URLs whose sitemap `lastmod` falls inside the last seven days are
submitted. Re-sending the whole sitemap on every deploy is what the protocol
asks people not to do, and `lastmod` here comes from real git history, so it is
a truthful basis for deciding what is new.

The key is deliberately committed. IndexNow proves ownership by fetching it
back from the site root, so publishing it is the mechanism rather than a leak.

Submitting is opt in: `scripts/indexnow.mjs` prints the batch and stops unless
given `--submit`, which is what `deploy.yml` passes after a successful publish.
It was the other way around at first, and reading the script to see what it
would do sent 25 URLs to Bing. Nothing came of it, because the key file is not
live yet and an unverifiable batch is discarded, but a script that POSTs to
somebody else's service when run with no arguments is the wrong default.

Google does not participate. Bing, Yandex, Seznam and Naver do, and
participants share submissions.

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
   Copilot, so a page Bing has not indexed cannot be cited there. IndexNow now
   pushes changes automatically, but the console is still where you confirm
   they were accepted and see what was indexed. (SEO 20, GEO 96)
4. **Wikidata item.** Create it, then add the QID to `SAME_AS` in
   `lib/site.ts`. (GEO 53)
5. **Owned profiles.** LinkedIn, X, Crunchbase. Create them with the identical
   boilerplate from `lib/site.ts`, then add each URL to `SAME_AS`. The array
   currently holds only the GitHub URL, deliberately: a `sameAs` pointing at a
   404 is worse than a short list. (GEO 54, 58)
6. **Presence in the corpora that get cited.** Reddit is the most-cited domain
   across every major engine and roughly 46.7% of Perplexity's top-10 source
   share; Stack Overflow, Hacker News, YouTube and LinkedIn follow. None of
   this can or should be automated, and posting as somebody else would be
   worse than not posting. (GEO 65-76)
7. **Awesome-list submissions.** (SEO 94, GEO 72)

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

## What changed when this was re-applied onto main

This work was written against `hosted-loop`, which had branched from `main` 276
commits earlier. Cherry-picking would have produced a merge that quietly
deleted somebody else's work, so it was re-applied file by file. #47 landed
most of it. What follows is what that port left out or got wrong, found by
building the result and reading it rather than by reading the diff.

- **The images never came across.** `public/home` still held 15.55 MB of PNG
  and the 1.91 MB preload was still on the home page. `Picture.tsx`,
  `optimize-images.mjs` and `assets/hero/` are the missing half.
- **Four routes were not in the registry.** `/dpa`, `/subprocessors`,
  `/data-retention` and `/sla` shipped with a hand-written title and
  description and nothing else, four days after the registry was added to stop
  exactly that.
- **Seventeen of twenty-one pages never passed `path`.** The prop existed, was
  documented, and only three callers used it, so most of the site had no
  WebPage node and no breadcrumb trail at all.
- **`FaqJsonLd` had zero call sites.** A complete FAQPage emitter, and no
  FAQPage anywhere on the site.
- **The blog was an island.** Every link resolved and nothing outside `/blog`
  linked into it, so the only route in was the sitemap. `check:seo` now walks
  out from the home page and requires every sitemap route to be reachable.
- **`actions={null}` rendered the default buttons**, because `actions ??
  default` cannot tell null from undefined, so the blog index asked for no
  action row and got two buttons.

Four routes are also gone from the registry because the pages are gone from the
app: `/company`, `/security`, `/open-source` and `/design-partners`, deleted by
#42 along with five solution verticals.

## Known issues found along the way, outside this scope

- `www/lib/company-content.tsx` has no importers. It still holds the content
  for the four pages #42 deleted, including `related` links to `/company`,
  `/security`, `/open-source` and `/design-partners`, all of which now 404.
  Nothing renders it, so nothing ships those links, and deleting somebody
  else's content file was not this change's business. It is called out because
  a dead file full of dead links is the shape of thing that gets re-imported
  later. `lib/lastmod.ts` no longer reads it.
- `components/layout/MarketingPage.tsx` and `InnerPage.tsx` have no importers.
  Left in place, for the same reason.
- `cta-atmosphere.png` and `sage-noise.png` are referenced nowhere. 3.5 MB
  that was being deployed for nothing. They are kept in `www/assets/hero/` and
  simply not encoded.
- The site's mega-menu renders in plain divs with no `<nav>` or `<header>`
  ancestor. That is a semantic-landmark gap and an accessibility issue
  (SEO 30). Worked around here rather than fixed: the markdown twins scope to
  `<main>` instead of trying to exclude chrome.
- The live deployment is stale, consistent with `deploy.yml`'s own note that
  the publish step has never run. Everything in the "before" table above is
  still what production serves until this reaches `main`.

# fixed

The documentation shipped two of the eight head tags it was written to carry,
on all 76 pages, and nothing in the repository was in a position to notice.

PR #47 ported the marketing half of the social card and entity graph work and
left the docs half behind. `og:image:width`, `og:image:height`, `og:image:alt`,
`twitter:image`, `robots` and the `TechArticle` JSON-LD were all missing. The
JSON-LD is the one that mattered most: without it the documentation declared no
relationship to the marketing site's entity graph, so 76 of the site's roughly
90 pages resolved as a separate thing that happened to share a domain. All six
are now emitted, verified present on all 76 built pages rather than on the one
page somebody opened.

The reason it went unnoticed for that long is the more useful half of this
change. `www/scripts/check-seo.mjs` asserts the SEO surfaces against `www/out`
and never opens `docs/dist`, so the largest part of the site had no gate with an
opinion about its output at all. `just docscheck` is that gate. It reads every
built documentation page, requires the eight head tags on each, and resolves the
three `@id` values the JSON-LD references against the constants
`www/lib/jsonld.tsx` actually declares, rebuilding them from `SITE_URL` the way
the TypeScript does. A reference to an entity nothing declares is not a small
error, it is the tag silently doing nothing, and it now fails loudly and names
the page. A missing `docs/dist` fails rather than skipping, because a gate that
is green about nothing is the gap this closes.

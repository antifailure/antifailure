# fixed

Every documentation address the product prints or publishes now names the URL
the site actually serves, rather than a spelling it answers with a 301.

The host serves antifailure.dev with no trailing slash. The marketing site
agreed; the documentation build did not, so
`https://antifailure.dev/docs/reference/cli/` redirected to
`https://antifailure.dev/docs/reference/cli` on the way to every page. That
affected the `More` link under all 131 error codes, which is the address the
engine prints when something has already gone wrong for you, the 82
documentation pages' own canonical tags, the 81 URLs in the documentation
sitemap, and the links between the pages themselves.

Nothing about which page an address resolves to has changed, and the old
spelling still works, because the redirect that was always there is still
there. It is simply no longer on the path a reader or a search engine takes to
reach the page.

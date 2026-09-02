# fixed

The sign-in and sign-up pages advertised a markdown file that 404'd.

Every page on the marketing site carried a `<link rel="alternate"
type="text/markdown">` pointing at its own address with `.md` on the end, so an
assistant could read 800 words of prose instead of parsing 300KB of HTML. The
generator that writes those files does not write one for every page: it skips
anything carrying `noindex`, because a page crawlers were asked to ignore
should not be republished in a machine-readable form.

`/signin` and `/signup` are the only two pages the site marks `noindex`, so
they were the only two advertising a file the build never produced. Both
addresses answered 404. The tag is now emitted only for indexable pages, which
is the rule the generator already followed, and the SEO check asserts that
every twin a page advertises is a file the build actually wrote.

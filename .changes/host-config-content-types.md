# fixed

Five route entries in `staticwebapp.config.json` declared a `content-type` that
Static Web Apps ignores. The platform sets content type from the file extension
and a route header gets no say, so `/robots.txt`, `/llms.txt` and
`/llms-full.txt` went out as `text/plain` with no charset and `/sitemap.xml` as
`text/xml`, while the `cache-control` sitting in the same `headers` object was
applied correctly every time. The `/*.md` rule was dead in the same way; it only
looked like it worked because `mimeTypes` already carried `.md` and quietly did
the job the route rule was being credited for.

Nothing was broken for a reader. `llms.txt` is pure ASCII today, so a missing
charset changes nothing, and no crawler distinguishes `text/xml` from
`application/xml`. What was wrong is that a file said one thing and the server
did another, which is a trap set for whoever first puts a curly quote into
`llms.txt` and then goes looking four steps away from the change that broke it.

`.txt` and `.xml` move into `mimeTypes`, which is the mechanism that
demonstrably works, and the five dead `content-type` keys are gone while their
`cache-control` stays. Verified by HEAD against a real deployment rather than by
reading the file back: all four now carry `charset=utf-8`, `/index.md` is still
`text/markdown` after losing its route header, and every `cache-control` is
unchanged. This also covers the several hundred other `.txt` files the static
export writes, which are the markdown twins and the prefetch payloads, and
`/blog/rss.xml`.

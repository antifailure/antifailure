# fixed

The API reference named eight routes fewer than the server serves.

`reference/api.md` is the page a reader takes as the API surface, and three
whole families were absent from it: `/webhooks/github` and `/webhooks/stripe`,
the two `/byok` model proxy endpoints, and the four `/console/api/providers`
routes. The model proxy is the mechanism two guides describe in full, so the
page that lists the surface omitted the thing those pages tell you to point your
client library at.

It also said "Everything below is authenticated, apart from the four
unauthenticated routes at the top", which was a counted claim that the webhooks
break in a way counting cannot fix. They accept a signature rather than a
credential, which is a third thing: nobody signs in to them, anybody can reach
them, and what protects them is that the body must have been produced by
somebody holding the shared secret. The page says that now instead of a number.

Each row was written from the source rather than from the route table: the
console routes take a session cookie plus a CSRF header from `GET /auth/session`
and exist because `/v1/providers` authenticates a Bearer token for
`af provider`, the proxy takes an engine token in whichever header that
provider's own client already sends, and both webhooks verify over the exact
bytes received before anything is parsed.

The gate's backward direction now runs unguarded, because its register emptied
itself: the assertion that fails when a known gap stops being a gap forced these
three families out of the list in the same change that documented them.

And the scanner behind it had a blind spot worth naming, found by going looking
for one rather than by it failing. It matched `app.get` and friends, and the
tRPC router is mounted with `app.use('/trpc/*', ...)`, so `/trpc` was never in
the route set at all. The backward check was passing over a family it could not
see. It reads mounted families now, and removing the `/trpc` row from the page
fails as loudly as removing any other.

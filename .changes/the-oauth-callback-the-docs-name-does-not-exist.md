# fixed

Four documents told a self-hoster to point GitHub at a URL this product does
not serve.

`AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/callback` is in the third
page of the getting started path, the self-hosting control plane page, the
Azure page and the README. There is no `/auth/callback`. The route is
`/auth/github/callback`, registered at `server.ts:411`, and it is what
`production.tfvars` and `staging.tfvars` both configure, so the product's own
deployment disagreed with its own instructions.

That value goes straight to GitHub as `redirect_uri`, and nothing validates its
path at start up. So the failure lands at the END of the first sign in, after
the operator has registered an OAuth App with the same wrong URL. They then
compare the variable against the App, find them identical, and go looking
somewhere else entirely.

A new test reads the routes the server registers and the pages the console
serves, and refuses any URL in the documentation that hangs off one of this
product's own hosts and matches neither.

The first version of that test matched a bare path under `/auth/`, `/v1/`,
`/trpc/`, `/byok/` or `/console/api/`, which sounds specific and is not. It
produced ten findings and nine were false: `/v1/` is what Stripe, Anthropic and
OpenAI all use, so `/v1/charges`, `/v1/messages` and `/v1/chat/completions` came
back as missing routes on this server, and it pulled `/auth/github.ts` out of
the middle of a source file path. One real finding under nine false ones is a
gate somebody deletes, and the real one dies with them. Requiring a host of ours
drops every false one and keeps all four real occurrences, because each was
written as a complete URL an operator pastes.

Separately, both pages that tell a maintainer to add a label to run a fork's
pull request now say which label. It is `antifailure:allow`, and it was named
only in the generated schema reference, so a reader following the getting
started path was told to add a label nobody had told them the name of.

And `just docexamples`, the gate that keeps documented commands honest, was
serving cached passes over the documentation. It reads
`docs/src/content/docs`, which is outside the engine module, so nothing it
depends on is anything Go's test cache watches. Measured rather than reasoned
about: a page was edited to read `af init --wat`, a flag that does not exist,
and the recipe answered `ok (cached)`. The same test with `-count=1` failed on
it at once. CI already passes `-count=1` through `go test ./...`, so this was
local only, which is the worst place for it: CONTRIBUTING promises that a green
`just gate` means a green CI.

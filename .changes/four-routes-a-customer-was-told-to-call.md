# added

The four `/v1/oidc/bindings` routes are in the published OpenAPI document.

They were served and undocumented. `docs/guides/github.md` hands a customer a
`curl` line for them, and there is no `af` command in front of them, so the
HTTP call is the surface. A surface a customer is told to call and cannot look
up is undocumented rather than internal.

Four rather than three, which is the count everybody including me kept getting
wrong. Revoking has two paths and not one: a repository is `owner/name` and a
slash is a path separator, so a single `{binding}` segment cannot match one.

The document also gains a third security scheme. These take a CLI token from
`af login`, which is neither the browser session nor an engine token, and
declaring one bearer scheme for both would have told a reader they are
interchangeable.

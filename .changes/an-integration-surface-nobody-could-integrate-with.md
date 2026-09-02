# fixed

A provider could not be written outside this repository, which is the one thing
providers are for.

`provider.Database.ConnString` returned `secrets.Value` from
`engine/internal/secrets`. An implementation has to name the return type, and
there is no spelling of that type an outside package is allowed to use: the Go
toolchain refuses an import of an internal path from outside the subtree rooted
at its parent, so naming it fails one way and importing it fails the other. The
interface the release notes call an integration surface compiled here, reviewed
as correct, and would have failed on the first line of the first real provider
anybody wrote. The 1.0.0 tag would have held it there for the whole of version 1.

The type is now `engine/pkg/secret.Value`, which is public for exactly this
reason. `engine/internal/secrets.Value` is an alias for it, so nothing inside
the module changed, and CI compiles a provider from outside the engine module
to prove the promise rather than restate it.

Two carve-outs in the 1.0.0 notes now have mechanisms behind them instead of
sentences. `tools/surfacecheck` refuses a Go package that becomes importable
and is classified nowhere, a change to a stable package that version 1 does not
allow, and a stable signature naming a type from a package that is not stable.
`web/apps/api/src/boundary.ts` classifies every route the control plane's
router serves as published contract or deliberately excluded, and
`route-boundary.test.ts` holds the router's own route table against the
published document in both directions. Before that, a route missing from the
document could mean nobody outside could call it or that somebody forgot, and
four live routes under `/v1/oidc/bindings` were the second.

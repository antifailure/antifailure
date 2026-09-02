# Enterprise edition

**This directory is not MIT licensed.** Everything in `ee/` is covered by the
[Antifailure Enterprise License](./LICENSE.md): the source is public so that
you can read, audit, and modify it, but running it in production requires a
valid Antifailure enterprise license key or subscription, and it may not be
resold or offered to third parties as a hosted service.

Everything outside this directory is MIT licensed with no such restriction.

## Why this directory exists

Antifailure is a real open source product, not a demo with the useful parts
removed. The MIT-licensed engine masks your data, seals your network, runs
your agents, and tears everything down, and it does that completely, forever,
for free, self hosted. What lives here is the set of things a large company
asks for before a rollout, which is also the set of things a large company is
willing to pay for: single sign on, SCIM provisioning, custom roles and
approvals, SIEM streaming with a tamper evident hash chain, organization wide
policy enforcement, customer owned runtime clusters, enterprise secret
managers, billing and metering, and support tooling.

## How the boundary is enforced

It is not a convention. It is checked four ways on every pull request, and
each of them names the file that does the checking so you can read it rather
than take this paragraph's word for it.

1. **Separate modules, so a stray import does not compile.** `ee/engine` is
   its own Go module and is deliberately kept out of the root `go.work`, so
   there is no import path from the community engine that resolves; the
   comment at the top of `ee/engine/go.mod` says why. `ee/web` is its own npm
   workspace root under the scope `@antifailure-ee`, separate from `web/`, so
   the community workspace cannot resolve an enterprise package either. This
   is stronger than a lint rule: a mistaken import is a compile error on the
   machine that made it.
2. **A grep, for the case a module boundary cannot catch.** The `edition
   boundary` job in `.github/workflows/ci.yml` fails if any Go file under
   `engine` or `tools` names `antifailure/antifailure/ee`, and `just edition`
   fails if anything under `web/apps` or `web/packages` names
   `antifailure-ee` or `ee/web`.
3. **Deleting `ee` and building what is left.** The same job runs `rm -rf ee`,
   then `go build ./...` and the engine's unit suites from the tree that
   remains. That is the community build passing green with the enterprise code
   gone, which is the claim the paragraph below used to make about a mirror.
4. **Symbol inspection of the artifact that ships.** The job builds
   `cmd/af` and fails if `strings` finds any `antifailure/.../ee/` package
   path in the binary. `.dockerignore` excludes `ee` from the image build
   context, so the published control plane image is built from a context that
   does not contain it.

WHAT THIS SECTION USED TO SAY, and why it is written down rather than quietly
replaced. It claimed `depguard` in Go and an import restriction in Biome; this
repository has neither, and `.golangci.yml` enables ten linters, none of them
`depguard`. It claimed enterprise Go packages compile only with an `ee` build
tag; there is no `//go:build ee` anywhere in the tree, and `ee/engine/go.mod`
opens by saying it is deliberately *not* a build tag. It claimed CI publishes a
generated mirror repository named antifailure-foss; no workflow generates,
pushes to, or builds from any mirror.

Three of the four stated mechanisms were fiction. The boundary itself was
real the whole time, which is exactly what makes this worth recording: an
accurate claim resting on an invented mechanism reads identically to a true
one until somebody goes looking, and the person who goes looking is usually
the customer.

Enterprise features attach through extension points that the community code
declares as interfaces with no-op defaults: authentication providers, route
and page registration, policy hooks, runtime placement, billing hooks, audit
sinks, and the secrets adapter registry. A new enterprise feature that needs a
new hook adds the interface and its no-op default to the community code, and
its implementation here.

## Running without a license

The enterprise binary runs without a license key. Enterprise features are
simply off, and `af license status` says so and explains how to get one. When
a license expires, features enter a grace period with daily warnings and then
degrade to community behavior. Settings are preserved on disk throughout, so
renewing restores them exactly. Nothing is deleted and no environment stops
working because a license lapsed.

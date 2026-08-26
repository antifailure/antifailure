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

It is not a convention. It is checked four ways on every pull request:

1. **Import direction.** Code under `ee/` may import anything. Nothing outside
   `ee/` may import `ee/`. Enforced by `depguard` in Go and an import
   restriction in Biome for TypeScript.
2. **Build tags.** Enterprise Go packages compile only with the `ee` build
   tag. The community binary is built without it.
3. **Symbol inspection.** The release pipeline inspects the shipped community
   binary and the shipped bundles and fails if a single symbol or module from
   `ee/` appears.
4. **The FOSS mirror.** CI publishes `antifailure/antifailure-foss`, a
   generated copy of this repository with `ee/` deleted. If the community
   build does not compile and pass its full test suite from that mirror, the
   boundary is broken and the gate fails.

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

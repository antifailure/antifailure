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
approvals, SIEM streaming of the engine's privileged actions, organization wide
policy enforcement, customer owned runtime clusters, enterprise secret
managers, billing and metering, and support tooling.

### What "SIEM streaming with a tamper evident hash chain" means, exactly

It is two mechanisms and they are now joined to each other. They were not, for
as long as both existed, and the conjunction was false in the way that is
hardest to notice, because each half could be pointed at when questioned.

**The engine's half.** `ee/engine/auditsink`, registered in
`ee/engine/cmd/af/main.go`, asks the licence per call and forwards the five
actions `docs/enterprise/audit-stream.md` lists to syslog, a webhook or an
object store. Its webhook carries an HMAC over the exact bytes posted, so one
delivery is tamper evident. Nothing in it chains entries to each other, because
the engine runs on a machine with no database.

**The control plane's half.** `audit_entries` carries `prev_hash` and
`entry_hash`, records organization actions including sign on, provisioning and
administration, and is MIT. Global operator actions in `admin_audit_entries`
are forwarded only when they also produce an organization entry. `ee/web/audit`
carries it to a sink: a bounded queue, four destinations, and a batch manifest
signed over the chain head so an archived batch can be checked without asking
this control plane anything. `ee/web/server/src/register.ts` starts the poll
loop, which is what was missing.

**What was wrong and how long it lasted.** `ee/web/audit` was complete, tested
and imported by nothing. It was not a declared dependency of any package in this
workspace, so it could not be imported without a package.json change, while
`npm --prefix ee/web test` ran its suite green on every pull request because
that command runs every workspace. Tested, green and unreachable is the most
convincing disguise dead code can wear, and the summary line above it sold the
conjunction the whole time.

Two things the wiring found that the sentence above could not have:
`web/apps/api/src/entitlements.ts` had no `audit_stream` entry at all, so
`licensed(pool, orgId, 'audit_stream', now)` answered false for every
organization on every plan and the gate could never have been passed; and the
audit log is a cross tenant read by nature, which needed a policy and a pool
scope of its own rather than a reuse of the sweeper's. Migration 0043 carries
that argument.

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
   path in the binary. The repository root `.dockerignore` excludes `ee` from
   the build context, so the published **community** control plane image is
   built from a context that does not contain it, and the community leg of
   `control-plane-image.yml` asserts that the image it produced holds no `ee`
   tree at all.

   That sentence used to have no qualifier, because there was one image. There
   are now two, and the enterprise one obviously must see `ee`. It does not get
   there by weakening the line above: `deploy/docker/control-plane-enterprise.Dockerfile`
   carries its own ignore file beside it, which BuildKit reads in place of the
   root one for that Dockerfile alone. The qualifier is written down rather
   than left implied for the reason the paragraph further down gives: a claim
   that was true when it was written and is no longer true in a new context
   reads exactly like one that still holds.

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

## Running the enterprise control plane

`ee/web/server` is the enterprise entry point: the process that mounts single
sign-on and directory provisioning onto the control plane. Everything else,
every environment variable, every refusal, the pool, the sweeps and the server
itself, is the community `boot.ts`, unchanged. The enterprise edition adds
registrations and nothing else, which is why there is no second copy of the
configuration to drift.

It is published as `ghcr.io/antifailure/control-plane-enterprise`, built by the
same workflow and under the same tag as `ghcr.io/antifailure/control-plane`. So
the two are the same commit, the same console build, the same migrations and
the same bootstrap entrypoint, and `node bootstrap.mjs` and `node
maintenance.mjs` mean exactly what they mean in the community image. A Helm
release or a container app pointed at the enterprise repository instead of the
community one needs no other change.

Five variables configure the edition itself. Two of them are required and the
process says so and exits rather than starting half configured:

- `AF_EE_SSO_KEY`, **required**, 32 bytes of base64. Single sign-on encrypts the
  client secret and the service provider key it stores under it. Generate one
  with `openssl rand -base64 32`. A key that is a single repeated byte is
  refused, because that is the shape of a placeholder somebody meant to replace.
- `AF_ENTERPRISE_BASE_URL`, **required unless `AF_APP_BASE_URL` is set**, which
  it defaults to. Where these routes publish themselves. The process refuses to
  start when neither is set: an identity provider configured with an assertion
  consumer URL pointing nowhere is a failure that appears in somebody else's
  admin console.
- `AF_LICENSE_PUBLIC_KEYS`, as `kid=base64`, the keys this installation trusts.
  The same name and the same form the engine reads, so one key configures both.
- `AF_LICENSE_KEY`, the licence itself.
- `AF_ORG`, the organization slug the licence was issued to.

A licence that does not parse is refused at startup rather than degraded. That
is a deployment mistake and not a commercial state, and starting anyway would
mean an enterprise deployment quietly serving 402 to its own identity provider
because somebody pasted a truncated key.

### Streaming the control plane's audit log

Off unless a destination is named, and the process says which state it is in on
every start, because an installation that forwards and one that does not
produced identical logs until that line existed.

- `AF_AUDIT_STREAM_SINK`, one of `splunk`, `event_hubs` or `webhook`. Unset
  means the audit log is written and not forwarded, which is said out loud.
- `AF_AUDIT_STREAM_KEY`, **required when a sink is named**. Batch manifests are
  signed under it, and a manifest signed under a key nobody chose is decoration
  rather than evidence. Generate one with `openssl rand -base64 32`.
- `AF_AUDIT_STREAM_SPLUNK_URL` and `AF_AUDIT_STREAM_SPLUNK_TOKEN` for the HTTP
  Event Collector, with `AF_AUDIT_STREAM_SPLUNK_INDEX` and
  `AF_AUDIT_STREAM_SPLUNK_SOURCETYPE` optional so entries land where the
  customer's existing searches already look.
- `AF_AUDIT_STREAM_EVENT_HUBS_URL` and `AF_AUDIT_STREAM_EVENT_HUBS_AUTHORIZATION`,
  the second being a shared access signature the operator generates, so no key
  reaches this process and managed identity stays possible.
- `AF_AUDIT_STREAM_WEBHOOK_URL` and `AF_AUDIT_STREAM_WEBHOOK_SECRET`, which
  signs the body and its first entry's event timestamp. Receivers must
  deduplicate by organization and sequence; signature verification alone does
  not reject replay.
- `AF_AUDIT_STREAM_INTERVAL_MS`, how often a pass runs, ten seconds by default.
- `AF_AUDIT_STREAM_BATCH`, how many entries one pass reads, 500 by default, and
  `AF_AUDIT_STREAM_DELIVERY_BATCH`, how many one request carries, defaulting to
  the pass size. Two knobs rather than one because a pass size is how fast the
  forwarder catches up and a delivery size is what somebody else's collector
  will accept in one request.

A sink named with its variables missing **refuses to start**, naming the
variable. That is the same rule the engine's sink follows and for the same
reason: somebody who set the variable has said every privileged action must
reach their SIEM, and starting anyway forwards nothing and says nothing, which
is indistinguishable from a quiet week.

The object store sink in `ee/web/audit/src/sinks.ts` **cannot be configured from
the environment**, because it takes a `put` callback and this side of the
product carries no S3 or Blob signer to supply one. It is reachable by an
embedder that passes a `Sink` directly. Naming it in the variable is refused
rather than accepted and then silently writing nowhere.

**With no licence the enterprise routes are mounted and answer 402**, naming
the feature and the variable to set. They are not left unmounted. A 404 says
the feature does not exist, and is indistinguishable from a build that never
had it, a renamed route and a proxy that dropped the path.

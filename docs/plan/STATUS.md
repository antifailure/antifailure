# Status

The honest answer to "does it do X yet". Every component carries one of three
states and nothing else:

| State | Means |
| --- | --- |
| **proven** | The code exists, its tests pass, and the behavior has been exercised end to end against the real thing. |
| **written** | The code exists and passes its tests against a fake that enforces the real service's validation rules. It has never talked to the real service. |
| **planned** | Specified, not built. |

The distinction between proven and written is load bearing. A provider that has
only ever spoken to a fake is written, no matter how good the fake is.

This file is updated in the same pull request as the code it describes.

## What works today

```
af up          builds every service, branches a masked Postgres golden,
               places the egress sidecar, and prints where the app is
af down        removes every container, network, and database branch it made
af status      what is running for this branch, and where
af logs        service output, redacted

af golden refresh   copy, mask, verify, publish; a golden that fails
                    verification is never published and cannot be branched
af golden list      what exists, and which are verified
af golden gc        remove old ones, never one still branched from

af mask plan     a decision per column, with the reason for it
af mask apply    rewrite this environment's data
af mask verify   read it back with the detectors that would find a leak

af net policy    the effective egress policy, in the order that decides
af net explain   what happens to one request, and which rule decides it
af net log       every outbound request, allowed ones included

af inbox list/get/wait   mail and SMS captured instead of sent, with the
                         magic link and one time code extracted
af webhook list          providers and events that can be sent
af webhook trigger       one signed callback into the environment

af test        agents drive the workflows and return verdicts with a
               trace, a video, and steps to reproduce a failure

af load        traffic shaped like production's, with the worst
               regression first
af load smoke  a short burst, to check it answers under any load at all

af mask preview  a few rows before and after, written nowhere
af golden verify re-check a published golden on a throwaway branch
af support bundle logs, decisions, manifest, and doctor output, redacted,
                 with a listing of exactly what it included

af ci          the whole pull request check in one command: up, test,
               report, tear down, whatever happens

af runner install / check   put the runner where af test finds it

af env list / prune   what this machine is holding, and a cutoff to clear it

af insights    what the database noticed: the N+1, the index that stopped
               being used, the scan on a table that grew

af env pull    what the control plane recorded for one environment

af license     what this installation is licensed for, in both editions

af doctor      ten checks, each with a remediation
af init        reads a repository, writes a manifest, explains what it assumed
af explain     the effective configuration with every default resolved
af version     version, commit, edition, platform
```

Every command in the tree does something. The list of placeholders in the CLI
test is empty, and it is kept rather than deleted with its last entry, because
an empty list is the assertion.

Proved end to end against a real Docker daemon and a real Postgres:

- A Node repository with no Dockerfile gets a generated build, a Postgres
  branch reachable at `db:5432`, and an app serving on localhost.
- A whole Stripe billing flow (customer, checkout, subscribe, read, cancel)
  runs against the built in mock pack with the network unplugged.
- Signed webhooks are accepted by the application's own HMAC verification,
  with no secret configured anywhere.
- Mail sent through the Resend API is captured, never delivered, and the
  verification link and code come back out of it.
- A branch holding real looking data fails `af mask verify`, passes after
  `af mask apply`, and the card number in a free text column is gone.
- An agent finds the email and password fields by their labels, presses
  Create account by its name, and passes with a trace, inside the sealed
  environment. Given an expectation the page neither confirms nor
  contradicts, it reports unverified with every step it took rather than
  guessing a pass or a failure.
- A second tenant is refused a read, an unqualified read, an update, a delete,
  and an insert on every table in the control plane, by Postgres rather than
  by the application. Turning one policy off makes the suite say which table
  leaked and how many rows.
- An engine sends four events over HTTP in the order 3, 1, 3, 2. Three are
  stored, the repeat is dropped, and the environment ends at sequence 3 in the
  right state, with the late event changing nothing.

### What the containment is, exactly

Every environment sits on a network with no route to the internet. The only
thing on both networks is the egress sidecar, so the policy is an enforcement
rather than a request: a client that ignores its proxy variables has nowhere
to send the packet.

Interception is by DNS. Every name a service looks up that is not inside the
environment resolves to the sidecar, which recovers the destination from the
Host header or the TLS server name and decides. That is what makes it work for
Node, which has no proxy support at all, and for every SDK that bundles its own
client. Connecting to a raw address does not get around it, because the network
still has no route out, and there is a test for that.

Where a rule names a path, or the mode is capture, mock, sandbox, or synth, the
sidecar terminates TLS with a certificate signed by an authority generated for
the environment. Everything else is tunnelled untouched, so a client that pins
its own certificate keeps working. A rule naming paths on an HTTPS host that is
only tunnelled is recorded in the decision log as host_only rather than assumed
away.

The first containment design was wrong and a test caught it: disabling IP
masquerading looks like it removes a container's route out and does not, because
Docker Desktop translates the traffic again at the virtual machine's gateway.

## Phase 1. Foundation

| Sub-phase | State | Notes |
| --- | --- | --- |
| 1.1 Repository and governance | proven | Governance files, templates, CODEOWNERS, ADRs 0001 and 0002. |
| 1.2 Toolchain pinning and task runner | planned | |
| 1.3 Schemas and code generation | proven | `schemas/manifest.v1.json` is the source of truth; Go types mirror it. TypeScript generation lands with the runner. |
| 1.4 Continuous integration and gates | planned | |
| 1.5 Release pipeline | planned | |
| 1.6 Security baseline | planned | |
| 1.7 Documentation site skeleton | planned | |
| 1.8 Azure foundation (Terraform) | planned | Isolation boundary documented in `infra/ISOLATION.md`. Blocked on Q4. |
| 1.9 Test infrastructure and fakes | planned | |
| 1.10 Events, logging, and redaction | proven | 100 percent coverage on redaction, 454 ns/op with no allocations. |
| 1.11 Local state store and journal | proven | Crash injection at every step, plus a property test over random interleavings. |

## Phase 2. Engine core and CLI

| Sub-phase | State | Notes |
| --- | --- | --- |
| 2.1 CLI framework | proven | Whole tree present; unimplemented commands return AF-RUN-001 and exit 2. |
| 2.2 Manifest loader and validator | proven | Fuzzed. Unknown keys are errors with a line and a suggestion. |
| 2.3 Detection engine | proven | Twelve analyzers. Deterministic, bounded, fuzzed, never executes repository code. |
| 2.4 `af init` | proven | Validates its own output before writing. |
| 2.5 Secrets subsystem | planned | `secrets.Value` exists and is proven. Sources and adapters are next. |
| 2.6 `af doctor` | proven | |
| 2.7 HUD | planned | |
| 2.8 Event sinks | proven | NDJSON with rotation, JSON, memory, and a replay reader. |

## Supporting packages

| Package | State | Coverage |
| --- | --- | --- |
| `internal/clock` | proven | 88 percent |
| `internal/secrets` | proven | 100 percent |
| `internal/redact` | proven | 100 percent |
| `internal/errors` | proven | 100 percent |
| `internal/events` | proven | 94 percent |
| `internal/journal` | proven | 95 percent |
| `internal/state` | proven | 83 percent |
| `internal/lock` | proven | 78 percent |
| `internal/manifest` | proven | 87 percent |
| `internal/detect` | proven | 81 percent |
| `internal/cli` | proven | 73 percent |

## Phase 3. Postgres data layer

| Sub-phase | State | Notes |
| --- | --- | --- |
| 3.1 Provider interface and conformance suite | proven | 23 behaviors in `engine/conformance`. A behavior a provider cannot support is skipped explicitly, naming the capability. |
| 3.2 Docker provider | proven | 21 behaviors pass against a real daemon, 2 skip with named reasons, zero resources left behind across repeated runs. |
| 3.3 Masking engine | partial | The 22 transforms and the key hierarchy are proven at 95 percent. The rules model, classifier, SQL compiler, and resumable executor are next. |
| 3.4 Verification scanner | partial | The 9 detectors are proven at 94 percent. The streaming table scan and the signed attestation are next. |
| 3.5 Subsetting | planned | |
| 3.6 Authentication adapters | planned | |
| 3.7 to 3.9 Neon, Supabase, DBLab | planned | Blocked on Q5: no accounts provisioned. |
| 3.10 Golden lifecycle | planned | |
| 3.11 Postgres Insights | planned | |

## Supporting packages, phase 3

| Package | State | Coverage |
| --- | --- | --- |
| `internal/masking` | proven | 95 percent |
| `internal/verify` | proven | 94 percent |
| `pkg/provider` | proven | interface only |
| `engine/conformance` | proven | 23 behaviors |
| `internal/db/docker` | proven | full suite green against a real daemon |

## Phase 3. Data

| Component | State | Notes |
| --- | --- | --- |
| `pkg/provider` database interface | proven | |
| `engine/conformance` | proven | 23 behaviors; now asserts it left nothing behind |
| `internal/db/docker` | proven | full suite green against a real daemon |
| `internal/masking` transforms | proven | 22 transforms |
| `internal/masking` catalog | proven | reads the live schema, keys, unique constraints, generated columns |
| `internal/masking` rules | proven | specificity decides, not order; defaults link by transform |
| `internal/masking` executor | proven | chunked, resumable, ctid for unkeyed tables |
| `internal/verify` detectors | proven | 9 detectors |
| `internal/verify` scan | proven | catches unmasked data, not only passes masked data |
| `internal/verify` attestation | proven | Ed25519; rejects a deleted finding |
| `internal/subset` | proven | dependency order, cycles named where they break, unreachable tables reported |
| Neon, Supabase, DBLab providers | planned | blocked on accounts |

## Phase 4. Build and runtime

| Component | State | Notes |
| --- | --- | --- |
| `internal/dockerutil` | proven | one label scheme; filters match on presence, not value |
| `internal/build` context | proven | deterministic tar, digest as cache key |
| `internal/build` ignore | proven | `.dockerignore` implemented here rather than added as a dependency |
| `internal/build` buildpacks | proven | Go, Node, Python, Ruby; three are built for real in the suite |
| `internal/build` docker | proven | output redacted at the writer |
| `internal/runtime/local` | proven | 20 behaviors against a real daemon |
| `internal/env` | proven | lock, state, journal, database, build, runtime, in the one order that works |

## Phase 5. Egress control

| Component | State | Notes |
| --- | --- | --- |
| `internal/policy` | proven | 100 percent, 48ns per decision, zero allocations |
| `cmd/af-proxy` | proven | DNS interception, transparent HTTP and TLS, explicit proxy |
| `internal/proxyimage` | proven | sidecar built from source carried in the binary, no registry |
| `internal/envcert` | proven | per environment authority, one level, thirty days |
| `internal/livekey` | proven | refuses a live credential anywhere, distinguishes live from test |
| Capture and the inbox | proven | Resend, SendGrid, Postmark, Mailgun, Twilio |
| `internal/mockpack` | proven | stateful packs; built in Stripe pack runs a billing flow offline |
| `internal/webhook` | proven | Stripe, GitHub, Svix signing, verified independently |
| Synth mode | proven | invents a response, marks it synthesized, refuses readably with no key |
| Rate limiting | proven | per rule, shapes rather than refuses, burst lets startup through |

## Phase 6. Agents

| Component | State | Notes |
| --- | --- | --- |
| `runner` verdict model | proven | five verdicts; a runner failure never counts against the application |
| `runner` inbox client | proven | checks what already arrived before waiting |
| `runner` login strategies | proven | password, magic link, email code, SMS code |
| `runner` planner | proven | deterministic, no model key needed; three way expectation check |
| `runner` browser | proven | Playwright, accessibility tree, three tests against a real browser |
| `internal/env` test | proven | `af test` end to end |
| Model driven planning | proven | Anthropic and OpenAI; refuses a control the page does not have; falls back when the model is unreachable |
| Invariants and insights | planned | |

## Phase 7. Load

| Component | State | Notes |
| --- | --- | --- |
| `internal/load` shape | proven | weighted mix, Poisson arrivals, deterministic per seed |
| `internal/load` safety | proven | every route unsafe until named; a method pattern does not cover another method |
| `internal/load` run | proven | measured against a real server; achieved rate reported, not the target |
| `internal/load` access log | proven | paths normalised, or the mix collapses into a list |
| `af load` and `af load smoke` | proven | run against a live environment; a route with no baseline is never a breach |

## Continuous integration

| Component | State | Notes |
| --- | --- | --- |
| `.github/workflows/ci.yml` | proven | engine with the race detector against a real daemon, runner with a real browser, edition boundary, credential scan |
| `tools/scanrepo` | proven | uses the engine's own detector, so CI and the proxy cannot disagree |

It found two real bugs on its first two runs: stale packaged sidecar
sources, and a database provider that inventoried every managed container
and so blamed itself for the runtime's.

## Phase 9. GitHub

| Component | State | Notes |
| --- | --- | --- |
| `internal/report` | proven | written for somebody who did not ask for it and has thirty seconds |
| `af ci` | proven | run end to end; tears down on success, on failure, and on a missing runner |
| `examples/github-workflow.yml` | written | a template to copy; the comment is updated rather than added |
| GitHub App mode | planned | belongs to the control plane, phase 8 |

## Phase 10. Release

| Component | State | Notes |
| --- | --- | --- |
| `.github/workflows/release.yml` | written | four platforms, static, the runner travels with the binary |
| `install.sh` | proven | POSIX sh, checksum verified, fails readably; the failure path is tested |
| `tools/notices` | proven | generated from what is actually linked, so it cannot go stale |
| `af runner install` | proven | af ci now needs no flags at all |

Written rather than proven for the workflow itself, because no tag has been
pushed. The first release is the test.

## Phase 8. Control plane

| Sub-phase | State | Notes |
| --- | --- | --- |
| 8.1 API and data model | proven | Tenancy is row-level security, not a WHERE clause. The cross-tenant suite asks the database which tables exist rather than carrying a list, and attacks every one from a second tenant: read, unqualified read, update, delete, insert. |
| 8.2 Authentication and organizations | proven | Permission matrix green for every route by every role, with the route list read out of the router. GitHub OAuth against a fake that enforces what GitHub enforces: single-use codes, expiry, verified addresses only. |
| 8.3 Environment matrix | written | The API is proven; the view is the web application. |
| 8.4 Run history and artifact viewer | written | Same: the endpoints exist and the timeline is the web application. |
| 8.5 Masking policy center | partial | The rules and attestation endpoints are proven. Writing rules back as a pull request needs the GitHub App. |
| 8.6 Network policy center | proven | The TypeScript engine reproduces the Go engine's decisions across the shared corpus, including the wording of every reason. |
| 8.7 Audit log | proven | Insert and select granted, nothing else, asserted against the grant table. Hash chain detects every field of every entry being altered in a 25-entry chain. |
| 8.8 Agent live view | planned | Needs the streaming endpoint and the web application. |
| 8.9 Design system | planned | The web application. |
| 8.10 Deployment | planned | Terraform and Helm. Needs an Azure subscription with quota. |

| Component | State | Coverage or notes |
| --- | --- | --- |
| `web/packages/db` schema and migrations | proven | 25 tests against a real Postgres |
| `web/packages/db` row-level security | proven | disabling one policy makes the suite name the table and the row count |
| `web/packages/db` audit chain | proven | every field of every entry altered in turn, each detected |
| `web/packages/policy` | proven | 43 vectors; a one-bit change to a specificity weight breaks six |
| `web/apps/api` | proven | 127 tests: matrix, sign-in, sessions, ingestion, membership sync |
| `engine/internal/controlplane` | proven | 79 percent; sends, buffers, drops the oldest, obeys a throttle |
| `af env pull` | proven | against the real server: four events sent out of order with a repeat, three stored, the late one changing nothing |

The whole loop is proven end to end rather than against a fake: the real
control plane on real Postgres, the Go engine sending over HTTP, the
environment ending at the right sequence, and `af env pull` reading it back.

## Phase 13. Enterprise edition

| Sub-phase | State | Notes |
| --- | --- | --- |
| 13.1 Edition foundation and licensing | proven | Licensing at 98.9 percent, parser fuzzed over 2.4 million executions, extension points at 100 percent, community binary proven free of enterprise symbols. |
| 13.2 to 13.14 | planned | Single sign on, SCIM, roles, SIEM streaming, policy enforcement, multi-cluster, secrets, billing, dashboard, support tooling, compliance, deployment. |

The boundary is a separate Go module rather than a build tag. The community
build cannot resolve an enterprise import path at all, so a mistaken import is
a compile error rather than something a linter has to notice. CI deletes the
directory and proves the community engine builds and passes without it, then
scans the shipped binary for enterprise package paths.

## Phase 14. Scaling

| Sub-phase | State | Notes |
| --- | --- | --- |
| 14.2 Environment scheduler | proven | 98.6 percent. Ten thousand runs across fifty organizations plan in 12 ms against a one-second budget, with no limit exceeded and every organization served. |
| 14.1, 14.3 to 14.10 | planned | Horizontal scaling, multi-cluster pools, incremental goldens, runner pools, observability, quotas, chaos, archival, disaster recovery. |

## Phase 11. Documentation site

| Component | State | Notes |
| --- | --- | --- |
| 11.1 to 11.6 | planned | Assigned elsewhere. The generated references have their sources: the cobra tree, the schemas, `catalog.yaml`, and the transform registry. |

## What is not built, and why

Everything remaining needs infrastructure that does not exist yet rather than
code that has not been written:

- **8.10, 14.1, 14.3, 14.10** need an Azure subscription with approved quota.
- **3.7 to 3.9** need Neon, Supabase, and DBLab accounts.
- **13.2, 13.3** need identity provider test tenants.
- **13.9** needs a Stripe account.
- **8.3, 8.4, 8.8, 8.9, and Phase 11** are the web application and the docs
  site, which are somebody else's work. The API they consume is proven.

Where a phase could be built against a fake, it was, and it says `written`
rather than `proven`. The distinction is the point of this file.

## Where to pick up

1. The remaining enterprise sub-phases, 13.2 onwards. Each one is independent
   and each one lives entirely under `ee/`.
2. Phase 14.7, rate limits and quotas, which is provable locally against the
   control plane that now exists.
3. Anything blocked above, as soon as the account or the quota exists.

Notes for whoever picks this up. The conformance suite is not yet tested
against a deliberately buggy provider, so it is not yet proved that every
subtest can fail; that is worth doing before a second provider is written
against it. And the Docker provider reports every committed image as verified,
which is true by construction today because the commit is the last step of a
refresh, but will stop being true once goldens can be imported. `af up`
creates an empty golden when no source database is configured, so the schema
arrives with the migrations rather than from production; masking and
verification run and trivially pass on no rows, which is the honest answer
rather than a skipped step. The masking key is generated once per machine and
kept in local state unless AF_MASKING_KEY is set, so two machines produce
different mappings until CI sets one.

The control plane's row-level security has a pattern worth understanding
before adding a table to it. A lookup that determines the tenant cannot itself
be tenant-scoped, and the tempting fix is a policy that opens up when nothing
is set. The request with nothing set is the unauthenticated one, so that is a
hole rather than a fix. Sessions, engine tokens, installations, and user
upserts each declare the single value they already hold, and the policy
returns that row and nothing else. Four separate bugs, one shape.

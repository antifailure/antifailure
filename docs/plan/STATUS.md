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
| 1.2 Toolchain pinning and task runner | proven | `just gate` runs the 17 gates CI runs, in CI's order, and has been run end to end. `tools/gatecheck` fails the build when the justfile and the workflows disagree, and it reads every workflow that triggers on a pull request rather than `ci.yml` by name. Go is pinned by a `toolchain` directive in all three modules. |
| 1.3 Schemas and code generation | proven | `schemas/manifest.v1.json` is the source of truth; Go types mirror it. TypeScript generation lands with the runner. |
| 1.4 Continuous integration and gates | proven | Six jobs. The generated files, the policy vectors, the command reference, and the error catalog all fail the build when they drift. |
| 1.5 Release pipeline | proven | v0.1.1 released and verified end to end: downloaded, checksum matched, `af version` correct, and `af init`, `af up`, `af down` run on a scratch repository. `tools/ldcheck` refuses a release that stamps a symbol which does not exist, which is the bug that made v0.1.0 report itself as `dev`. The SPDX bill of materials and the cosign keyless signing added under 1.6 have NEVER RUN: no release has been cut since, so those two steps are written and not proven. |
| 1.6 Security baseline | proven | `tools/vulncheck` runs govulncheck over all three modules against the real Go vulnerability database, on every pull request and daily, and CI reports 2 reachable, 2 accepted, 0 unaccepted, 0 stale. It found 20 reachable vulnerabilities: 16 in the standard library (CI was installing Go 1.25.0) and GO-2026-5004, a SQL injection in pgx. Every action is pinned to a commit; `contents: write` is scoped to the publish job. SECURITY.md made ten claims to security researchers and seven were false; it now names only what a reader can go and look at, and states the two remaining gaps, no reproducibility verification and no adversarial suite. |
| 1.7 Documentation site skeleton | proven | Every deliverable the plan names now exists and runs. The Astro Starlight site has 43 pages. `tools/prosecheck` enforces the em dash rule in CI, with code and fenced blocks exempt because `--flag` is how an option is spelled. `tools/docs/forbidden.sh` is the G8 token scan: unfinished notes, filler, unfilled slots, work marked as not the real thing, addresses that resolve only on a private network, and bare GUIDs that name a cloud tenant, with names in `tools/docs/dictionary.txt`'s sibling `forbidden-extra.txt` because no pattern finds a person's name. Seventeen subprocess driven tests prove each rule catches its token and that clean prose passes; the scan refuses an exemption with no reason and one that matches nothing. Two of the plan's tokens are scoped to their marker sense rather than matched as bare words, and that is a deliberate deviation recorded in the script: the sandbox really does hand a container a placeholder credential and a migration really does run against a temporary server, so a bare word rule would be answered by rewording accurate documentation. `cspell.json` plus `tools/docs/dictionary.txt` covers spelling: 44 files, 0 issues. `.vale.ini` runs the Google developer documentation style plus a project rule for em dashes, at error level: 0 errors across 45 files. Warnings are not enforced and the number is worth recording rather than hiding: 106 warnings and 725 suggestions, of which 631 are contractions and passive voice, both of which this documentation's voice declines on purpose. Vale's own spell checker is off because cspell owns spelling and two word lists would disagree. `lychee.toml` checks the assembled site rather than the markdown, because the address a reader follows is `/docs/reference/cli/#af-init`, which is a heading on a built page and not a file in the tree: 7,112 links, 555 unique, 0 errors, fragments included. It found real defects on the first run. `tools/errgen` built `/docs/reference/cli#af-init/`, appending the trailing slash after the fragment, so two error codes linked to an anchor that does not exist and silently landed at the top of a 900 line page; the marketing site linked to `/docs/architecture`, `/docs/firewall`, `/docs/workload`, `/docs/migration-safety` and `/docs/open-source`, none of which are pages. All seven are fixed and pinned by a test. `tools/schemadoc` renders every JSON Schema into a reference page and deletes a page whose schema is gone. It refuses a definition with no description, which is what surfaced nine definitions in the manifest schema that had none, so nine rows in the reference table were blank. `schemas/events.v1.json` did not exist, though `internal/events` claimed it fixed the envelope; it is now generated from the Go type and the catalog, with a test that walks the struct so a field added with no schema entry fails rather than shipping. Pull request previews are the assembled site uploaded as an artifact, deliberately not a deployment, because a pull request from a fork must not publish to the address people trust. External links run on a daily schedule in `links.yml` rather than on every build, because a vendor's site being down is not a reason to refuse somebody's change. Looking at the rendered pages on a phone found the last one: a reference table needed 571px inside a 358px column and its Notes column was cut off mid sentence, on every table in the documentation. Fixed in the site's stylesheet, verified at 390px with no page level horizontal scroll. Outstanding and small: `just links` and `just vale` need `vale` and `lychee` installed, which `just setup` now reports, and gatecheck cannot compare a step that runs a pinned action against a justfile recipe, so the parity for those two is by inspection rather than by tool. |
| 1.8 Azure foundation (Terraform) | written | `infra/terraform` with a foundation module (resource group, budget on forecast as well as actual, Log Analytics) and a control plane stack. `terraform plan` against the real subscription is clean at 30 to add, 0 to change, 0 to destroy. Nothing has been applied, so this is not `proven`. `tools/azguard` and `tools/cost` are `proven`: the guard refuses all five foreign resource groups in this subscription and fails closed, and the estimator reports 32.49 USD a month from prices read out of the Azure retail API. Q4 is narrower now: what remains is the Entra app registration and federated credential, without which the CI plan job skips rather than passes. |
| 1.9 Test infrastructure and fakes | proven | The database conformance suite is now PROVED ABLE TO FAIL. `engine/conformance/db_selftest_test.go` points the suite at a provider that violates exactly one guarantee and requires it to go red in the named behaviour, in a subprocess, with no database: the property being false is arranged in the fake, and a negative control that needs infrastructure gets skipped, which is a false green rather than a proof (that correction is lane 4's). A positive control asserts the same nine behaviours PASS against a correct provider and did not SKIP, because a suite can otherwise pass by skipping itself. **Running it for the first time found two holes in the suite, both recorded in `knownGaps` with a guard that fails if they stop being real.** `Branch_RefusesAnUnverifiedGolden` puts its whole assertion inside `if err == nil && gv.ID != ""`, which a provider that correctly refuses to PUBLISH an unverified version never satisfies, so the body never runs and the product's central promise is unchecked on the branch side. `Health_ReportsADestroyedBranch` is declared as "unreachable rather than erroring" and its implementation explicitly accepts an error, so the suite and its own catalogue description disagree. Five faults still need Postgres because their behaviours read rows, and the test names which. `fakes` also carries `InMemoryDatabase`, `NewRuntime`/`BreakRuntime` and a clock. |
| 1.10 Events, logging, and redaction | proven | 100 percent coverage on redaction, 454 ns/op with no allocations. |
| 1.11 Local state store and journal | proven | Crash injection at every step, plus a property test over random interleavings. The journal is now also read: `af down` replays it after the label sweep, which nothing did until phase 14, so the records had gone in and never come out. |

## Phase 2. Engine core and CLI

| Sub-phase | State | Notes |
| --- | --- | --- |
| 2.1 CLI framework | proven | Whole tree present; unimplemented commands return AF-RUN-001 and exit 2. |
| 2.2 Manifest loader and validator | proven | Fuzzed. Unknown keys are errors with a line and a suggestion. |
| 2.3 Detection engine | proven | Twelve analyzers. Deterministic, bounded, fuzzed, never executes repository code. |
| 2.4 `af init` | proven | Validates its own output before writing. |
| 2.5 Secrets subsystem | proven | Sources with precedence, a dotenv reader, an encrypted local store, and a resolution layer. A sandbox credential reaches the sidecar as a file and the service as a marker; proven against a running container. The OS keyring is an interface with a fake: no real credential store is wired yet. |
| 2.6 `af doctor` | proven | |
| 2.7 HUD | proven | Model, rendering, non-TTY fallback, the Bubble Tea program, and `af up --hud`, which is the caller it did not have. `engine/internal/hud`: reorder window that abandons a gap rather than stalling, three layouts (stacked at 80, two column at 120, three column at 160) with seven golden frames committed, keyboard navigation, resize handling, and a queue that drops and counts rather than ever applying backpressure to the bus. Wiring it end to end meant making the engine emit what the dashboard draws: `internal/env` now publishes env.creating, golden.ready, db.branching, db.branched, build.started, build.log, build.finished, build.failed, service.starting, service.ready, service.exited, env.ready, env.failed, env.destroying and env.destroyed, and `Orchestrator.AddSink` attaches a subscriber to the session bus each command opens. Before this the bus had no subscribers at all and every event the journal published was delivered to nobody. Four tests in `internal/env` prove the attachment against a real Up, with no Docker daemon, by stopping the run at the policy hook; a negative control that removes the AddSink loop turns all four red. Looking at a frame built from the real stream found three more things reading the code did not: the golden fixtures were scripted with `service.started`, which is not an event type, so six committed frames were pictures of a stream the engine cannot produce; every log line repeated `env=` for the environment named in the header; and a verified golden showed as unverified on every ordinary run, because the pane only believed mask.verified and an ordinary run branches from a golden verified days earlier. goleak found a goroutine leaked per dashboard, from a cancellation watcher receiving on a nil Done channel, and a leaked drainer in the package's own concurrency test. `Program.Close` now signals on a second channel rather than closing the one producers write to, so Send after Close is a counted drop instead of a panic, and Close is idempotent because both the bus and the command legitimately call it. Outstanding: the vhs recordings for the docs page. `docs/guides/dashboard.md` documents the panes, the keys and the fallback without them. |
| 2.8 Event sinks | proven | NDJSON with rotation, JSON, memory, and a replay reader. Attached to a bus by `internal/telemetry` since phase 14; before that every constructor here had zero callers outside a test, so the log this row describes was never written on any machine. |

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
| 3.7 Neon | proven | The full database conformance suite, all 23 behaviours, against the real Neon API. Found three bugs a fake would not have: `pooled` omitted means pooled, so `ConnDirect` was returning a pooled connection; a 200 with an empty body broke destroy-twice; and Neon's own branch ceiling arrived as an unexplained 422. |
| 3.8, 3.9 Supabase, DBLab | planned | Blocked on Q5: no accounts provisioned. |
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
| Neon provider | proven | against the real service |
| Supabase, DBLab providers | planned | blocked on accounts |

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
| 8.10 Deployment | written | The control plane image exists (`deploy/docker/control-plane.Dockerfile`), is built and published by a workflow, and has been `proven` locally: built, run, migrated a fresh database, served 200. The Helm chart is `proven`: it installs on a real kind cluster in CI, and the assertion that matters passes there, `SELECT pg_has_role('af_app','antifailure_app','MEMBER')` returning true, followed by the application role reading a table and seeing no other tenant's rows. Uninstall is asserted to leave nothing behind. Its value guards are `proven` able to refuse. Terraform targets Container Apps rather than AKS: 32.49 USD a month against about 75 for an idle AKS control plane before a node runs. Quota is confirmed at 65 cores in southcentralus as well as eastus, and the control plane needs none of it. |

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
| 13.5 Audit export and SIEM streaming | proven | The hash chain is in the control plane's audit log. Forwarding is a bounded queue that cannot fail or slow the action it audits, with signed batch manifests carrying the chain head so a batch in an object store verifies on its own. Splunk, Event Hubs, an object store, and a signed webhook, at 31 tests. |
| 13.6 Organization policy enforcement | proven | 100 percent. A property test over five hundred random policies proves a stricter policy never permits more. |
| 13.4 Advanced access control and approvals | proven | 42 tests. The role model and scopes, approval policies that one person cannot complete alone, and the model as a reviewable file with a dry run that refuses a file leaving a required approval unreachable. |
| 13.2, 13.3, 13.7 to 13.14 | planned | Single sign on, SCIM, multi-cluster, secrets adapters, billing, dashboard, support tooling, compliance, deployment. |

The boundary is a separate Go module rather than a build tag. The community
build cannot resolve an enterprise import path at all, so a mistaken import is
a compile error rather than something a linter has to notice. CI deletes the
directory and proves the community engine builds and passes without it, then
scans the shipped binary for enterprise package paths.

The extension points the enterprise edition plugs into are MIT and live in the
community engine, and `af up` calls them. That matters: they shipped once with
no call site anywhere, which is a socket nothing is plugged into and is
indistinguishable from one that works until somebody relies on it.

## Phase 14. Scaling

| Sub-phase | State | Notes |
| --- | --- | --- |
| 14.2 Environment scheduler | proven | 98.6 percent. Ten thousand runs across fifty organizations plan in 12 ms against a one-second budget, with no limit exceeded and every organization served. |
| 14.9 Retention and archival | proven | Events partitioned by month on `occurred_at`, kept ahead by a daily pass, archived to newline delimited JSON before a drop. Proven against a real Postgres, including the ordering where the job stopped and the writes went to the default partition. The negative control, partitioning on `received_at` instead, makes the retry test fail. See `docs/plan/14.9-partitioning.md`. |
| 14.7 Rate limiting, quotas, kill switch | proven | Every public endpoint has a declared limit, checked against the server's own route table. An endpoint with none is refused rather than served unbounded. |
| 14.6 Observability | proven | The engine's event stream reaches a local NDJSON log and a control plane, which it never had before: nothing in the engine had ever called `Bus.AddSink`, no sink constructor had a caller outside a test, and `controlplane.NewSink` had none at all. The control plane serves `/metrics` in the Prometheus text format from counters it keeps itself and no query, because an aggregate across tenants would need a role that can read every tenant's rows. Alert rules and a Grafana dashboard live in `observability/` and a test fails on any metric they name that nothing exports. A sixth defect, found last by auditing the writers rather than the callers: the control plane sink sent event payloads unredacted, so a connection string in an event field reached the network in the clear while the local log, the spool, span attributes and the OTLP bytes were all scrubbed. The one writer of the five that leaves the machine was the one with no redactor. Fixed at `Client.Send`, the single point both the live path and the spool drain pass through, and a client with no redactor is now refused at construction the way the spool and the attachment already were. Two tests, each proved able to fail by removing the fix. |
| 14.8 Platform chaos testing | proven | Four claims the repository made in prose. Two were false. See `engine/chaos`, whose package doc carries the table. Two later corrections, both to the suite rather than to the product, and both the same shape as what the suite exists to find. `ok` for this package meant less than it looked like: `go test` without -v discards a passing package's output, so the tally printed nothing on a green run, and the only count assertion was that at least one scenario ran. A roster of the ten names compared as a set now makes `ok` mean these ten, by name. And the three attempt retry around a busy Docker daemon could not retry, because all three attempts shared one deadline that the first one spent; each has its own budget now, and a blown budget is reported as a fact about the machine rather than as a provider fault. |
| 14.10 Disaster recovery | proven | Backup, restore, and a drill that runs both against a real Postgres and reports the recovery time it measured: under two seconds to restore a small database on a continuous integration runner, and 20 to 160 seconds on a laptop with a dozen other containers on it. Two consecutive runs on the same runner reported 1.8 seconds and 0.6, which is why the operations page tells an operator to quote their own measurement rather than this one. The restore is checked against a manifest taken at backup time and then asked, through the unprivileged role, to refuse a cross-tenant read. The verification states its own scope, which a recovery plan needs and a row saying only `proven` does not: every check reads the `public` schema, where all 21 of the control plane's tables live today. Anything outside it is recorded in the manifest as unverified and reported by both the restore and the drill as a table the check cannot speak for, so the day this database grows a schema the drill says so instead of quietly verifying a subset and reporting a clean restore. |
| 14.4 Incremental goldens | planned | Not attempted rather than unfinished. It lands entirely in `internal/env/golden.go` and `internal/golden`, which another lane rebuilt in the same session; writing it unwired would be another instance of the bug this phase spent its time finding. The fingerprint design is recorded in the pull request. |
| 14.5 Runner pools | planned | Needs the Kubernetes runtime and the runner's own model and artifact paths. Worth recording while it is fresh: `artifact.stored` is an event type the control plane accepts and nothing produces, because artifact upload does not exist yet. |
| 14.1, 14.3 | planned | Horizontal scaling and multi-cluster pools. |

Three bugs on that path were each invisible for the same reason, and are
worth stating because the shape recurs. `typeMap` translated nine engine event
names to the control plane's and not one of the nine was a name the engine can
emit, so every event would have arrived as a type the control plane stores and
acts on not at all, leaving every environment displayed at whatever state it
was first reported in. The test that covered the translation passed, because
its helper built events with the same invented names. The per-environment
sequence restarted at zero in every process, and the control plane advances a
row only where the sequence is ahead of it, so every event after the first
command would have been refused. And `Journal.Replay`, `journal.NewRegistry`
and `Journal.Commit` each had zero callers, so the compensating half of
"everything that is created has a recorded, compensating deletion" had never
run.

Two smaller ones fell out of the chaos tests. `af down` segfaulted whenever
the Docker daemon was unreachable, because `newDatabaseProvider` returned a
typed nil inside a non-nil interface; it now reports AF-RUN-002. And the api
test harness decided whether a database existed with a three second connect
timeout, which on a loaded machine meant whole suites skipped and reported
green.

What is `written` rather than `proven` here, and why. OTLP trace export is
posted as JSON to a real HTTP endpoint in the tests and decoded back, so the
wire format, the identifier encoding, the integers-as-strings rule and the
redaction of the actual bytes are all proven; what is not is a real
OpenTelemetry Collector accepting them, which needs one running.
Chaos scenario 2 needs a real golden and fails on a saturated laptop inside the
provider's readiness window; it is proven on continuous integration and not
here. `events.EgressDecision` and `events.EgressTripwire` are declared,
described, and emitted by nothing, so the control plane has no record of a
single egress decision: the proxy is a separate process in a sidecar and
nothing bridges its decisions back to the bus. That is a real gap rather than
an oversight in this lane, and it is named here so it is not found again.

## Phase 11. Documentation site

| Component | State | Notes |
| --- | --- | --- |
| 11.2 Generated references | proven | The command reference is generated from the cobra tree and the error reference from the catalog. Both fail the build when they drift. |
| 11.4 Error catalog completeness | proven | Every entry is either returned somewhere or marked reserved, and a reserved entry that something returns fails too. It found 38 entries documenting errors this version cannot produce. |
| 11.1, 11.3, 11.5, 11.6 | planned | The site, the guides, and the examples. Assigned elsewhere. |

## What is not built, and why

Everything remaining needs infrastructure that does not exist yet rather than
code that has not been written:

- **8.10** is no longer blocked on an AKS decision. The control plane runs on Container Apps, where the whole stack costs 32.49 USD a month against roughly 75 for an idle AKS control plane before a node runs. The Terraform is written and plans clean; what is left is the decision to spend, and an Entra app registration so CI can plan with a federated credential instead of skipping.
- **14.1, 14.3** still want a cluster, which costs money for as long as it exists. 14.10 does not, and is not on this list: backup, restore and the drill are proven against a real Postgres and run on every CI build. What the deployed stack would add is a rehearsal against the database an outage would actually happen to, which is a different and better test than the one that exists, not a missing one.
- **3.8 and 3.9** need Supabase and DBLab accounts. 3.7 no longer does.
- **13.2, 13.3** need identity provider test tenants.
- **13.9** needs a Stripe account.
- **8.3, 8.4, 8.8, 8.9, and Phase 11** are the web application and the docs
  site, which are somebody else's work. The API they consume is proven.

Where a phase could be built against a fake, it was, and it says `written`
rather than `proven`. The distinction is the point of this file.

## Where to pick up

1. 13.2 and 13.3 need identity provider test tenants.
2. Anything blocked above, as soon as the account or the quota exists.

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

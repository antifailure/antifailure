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
| 1.8 Azure foundation (Terraform) | proven | `terraform apply` has run against the real subscription and the resource groups exist: `af-tfstate-eastus` holds the Terraform state and `af-cp-centralus` holds the control plane, both prefixed `af-` and tagged `project=antifailure`, with no subscription-scoped role assignment anywhere. **THE PLAN THAT SAID 27 TO ADD, 0 TO DESTROY WAS CLEAN AND STILL FAILED**, and that is the entry worth reading: applying it created twenty six resources and then refused the database with `ParameterOutOfRange: The value of the 'Version' should be in: []`. The empty list is literal. `az postgres flexible-server list-skus -l eastus` returns `supportedServerVersions: []` and the reason "Provisioning is restricted in this region", so PostgreSQL flexible server cannot be created in eastus on this subscription at any version in any SKU, while everything else in the stack creates there quite happily. A region therefore has THREE independent gates checked by three systems at three times: quota (which was never the constraint, 65 cores in both), Azure Policy (evaluated at write time, invisible to a plan), and regional service availability (invisible to both). The stack is in `centralus`, the only region that passes all three, at 30.47 USD a month from prices re-read for that region. `tools/azguard region` is the guard for the third gate, proved able to fail against the real API in both directions: it refuses eastus quoting Azure's own reason and allows centralus. Its unit tests cover four separate ways of not knowing, each of which must refuse rather than pass, plus a positive control so a guard that refuses everything cannot pass its own suite. Also fixed here and each found by running: `shared_access_key_enabled = false` breaks the azurerm provider's own post-create data-plane poll (`storage_use_azuread = true` is the fix); Owner on a subscription grants nothing on the storage data plane; `prevent_destroy` plus a tainted resource deadlock with an error naming the wrong one; the Key Vault name had no region in it, so a region move would have burned a global name for seven days that purge protection makes impossible to release early. Q4 is closed: the Entra app `af-infra-ci` exists with FOUR federated credentials and NO client secret or certificate at all. Four rather than two because GitHub issues OIDC subjects carrying immutable numeric organisation and repository ids, not the names every example shows, and both spellings are registered so a change in either direction does not break the job. It holds Reader on the control plane group, and BOTH Storage Blob Data Reader and Reader on the state account, which look redundant and are not: Azure splits storage into a control plane and a data plane and a role on one grants nothing on the other, in both directions. |
| 1.9 Test infrastructure and fakes | proven | The database conformance suite is now PROVED ABLE TO FAIL. `engine/conformance/db_selftest_test.go` points the suite at a provider that violates exactly one guarantee and requires it to go red in the named behaviour, in a subprocess, with no database: the property being false is arranged in the fake, and a negative control that needs infrastructure gets skipped, which is a false green rather than a proof (that correction is lane 4's). A positive control asserts the same nine behaviours PASS against a correct provider and did not SKIP, because a suite can otherwise pass by skipping itself. **Running it for the first time found two holes in the suite, both recorded in `knownGaps` with a guard that fails if they stop being real.** `Branch_RefusesAnUnverifiedGolden` puts its whole assertion inside `if err == nil && gv.ID != ""`, which a provider that correctly refuses to PUBLISH an unverified version never satisfies, so the body never runs and the product's central promise is unchecked on the branch side. `Health_ReportsADestroyedBranch` was declared as "unreachable rather than erroring" while its implementation accepted an error, so the suite and its own catalogue disagreed. **That one is closed.** The catalogue was right and both shipped providers already answer that way: teardown asks for health, so a provider that errors on a branch it has just removed makes a successful teardown look like a failure. The suite enforces the description now, and reverting the fix turns the self-test red in the named fault, which is how I know it is enforced rather than merely written. Five faults still need Postgres because their behaviours read rows, and the test names which. `fakes` also carries `InMemoryDatabase`, `NewRuntime`/`BreakRuntime` and a clock. |
| 1.10 Events, logging, and redaction | proven | 100 percent coverage on redaction, 454 ns/op with no allocations. |
| 1.11 Local state store and journal | proven | Crash injection at every step, plus a property test over random interleavings. |

## Phase 2. Engine core and CLI

| Sub-phase | State | Notes |
| --- | --- | --- |
| 2.1 CLI framework | proven | Whole tree present; unimplemented commands return AF-RUN-001 and exit 2. |
| 2.2 Manifest loader and validator | proven | Fuzzed. Unknown keys are errors with a line and a suggestion. |
| 2.3 Detection engine | proven | Twelve analyzers. Deterministic, bounded, fuzzed, never executes repository code. |
| 2.4 `af init` | proven | Validates its own output before writing. |
| 2.5 Secrets subsystem | proven | Sources with precedence, a dotenv reader, an encrypted local store, and a resolution layer. A sandbox credential reaches the sidecar as a file and the service as a marker; proven against a running container. The OS keyring is an interface with a fake: no real credential store is wired yet. |
| 2.6 `af doctor` | proven | |
| 2.7 HUD | proven | Model, rendering, non-TTY fallback, the Bubble Tea program, and `af up --hud`, which is the caller it did not have. `engine/internal/hud`: reorder window that abandons a gap rather than stalling, three layouts (stacked at 80, two column at 120, three column at 160) with seven golden frames committed, keyboard navigation, resize handling, and a queue that drops and counts rather than ever applying backpressure to the bus. Wiring it end to end meant making the engine emit what the dashboard draws: `internal/env` now publishes env.creating, golden.ready, db.branching, db.branched, build.started, build.log, build.finished, build.failed, service.starting, service.ready, service.exited, env.ready, env.failed, env.destroying and env.destroyed, and `Orchestrator.AddSink` attaches a subscriber to the session bus each command opens. Before this the bus had no subscribers at all and every event the journal published was delivered to nobody. Four tests in `internal/env` prove the attachment against a real Up, with no Docker daemon, by stopping the run at the policy hook; a negative control that removes the AddSink loop turns all four red. Looking at a frame built from the real stream found three more things reading the code did not: the golden fixtures were scripted with `service.started`, which is not an event type, so six committed frames were pictures of a stream the engine cannot produce; every log line repeated `env=` for the environment named in the header; and a verified golden showed as unverified on every ordinary run, because the pane only believed mask.verified and an ordinary run branches from a golden verified days earlier. goleak found a goroutine leaked per dashboard, from a cancellation watcher receiving on a nil Done channel, and a leaked drainer in the package's own concurrency test. `Program.Close` now signals on a second channel rather than closing the one producers write to, so Send after Close is a counted drop instead of a panic, and Close is idempotent because both the bus and the command legitimately call it. Outstanding: the vhs recordings for the docs page. `docs/guides/dashboard.md` documents the panes, the keys and the fallback without them. Driven by a real `af up` on 2026-08-27, not only by fixtures: the non-TTY fallback rendered env.creating, golden.ready with verified=true, db.branching, ninety build.log lines, build.finished with its duration, service.starting, and a failure at error level with its reason. That run also showed what the display does not say: the runtime's progress lines went to the terminal and not to the stream, and dashboard mode silences the terminal, so the readiness wait, which is the longest part of a run, drew nothing at all. They are on the stream now, tagged with the service they name when they name one that exists. Run end to end three times on 2026-08-27 against a real daemon, and each run found something the run before it hid. The first proved the wiring: env.creating through env.ready, 105 seconds, service answering 200. The second showed `service.ready web is running detail=` on every line, because an empty field was attached whatever its value, and `env=` repeated on a line already scoped to that environment. The third showed the readiness wait still silent, because the runtime's progress lines were emitted as service.log, which the fallback correctly folds away as noise: a service writes thousands of lines and a build log is not the place for them. `engine.progress` is a new type for exactly that, a step in a long running operation with no more specific event, and the run after it reads `engine.progress egress proxy ready with 0 rules` and `engine.progress web: ready at http://127.0.0.1:46000`, where thirty seconds of nothing used to be. |
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
| `golangci-lint` | proven | zero findings across the engine, and a gate in CI and in `just gate` rather than advice |
| `tools/gatecheck` | proven | 22 gates in CI, every one reachable from `just gate`; one exemption left, `vuln`, which security.yml owns |

It found two real bugs on its first two runs: stale packaged sidecar
sources, and a database provider that inventoried every managed container
and so blamed itself for the runtime's.

Getting the linter to zero was not a formatting exercise. It found five
symbols that were defined and never called, which is the shape a half
finished feature takes: a duplicate path existence check beside the one that
runs, a command tree walker whose comment named two callers it did not have,
a helper for commands that do not exist, a scheduler field holding a number
already in the field beside it, and `regionCodes`, a lexicon with no
transform to use it. The last one was a missing feature rather than a
leftover, so `region` is now a masking transform.

It also found a property test that built a record of what happened to every
resource and asserted nothing about it, three places where an error was
formatted into a string instead of wrapped, so `errors.Is` stopped working
across them, and seven writes to the output stream whose failure was
discarded. That last one is why `af` now exits non zero when it could not
write what it was asked to write: reporting success tells a script that the
output it never received is complete.

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
| 8.10 Deployment | partial | The control plane image exists (`deploy/docker/control-plane.Dockerfile`), is built and published by a workflow, and is `proven` locally: built, run, migrated a fresh database, served 200. The Helm chart is `proven`: it installs on a real kind cluster in CI, and the assertion that matters passes there, `SELECT pg_has_role('af_app','antifailure_app','MEMBER')` returning true, followed by the application role reading a table and seeing no other tenant's rows. Uninstall is asserted to leave nothing behind. Its value guards are `proven` able to refuse. **ON AZURE IT IS 26 OF 29 RESOURCES AND THE THREE THAT ARE MISSING ARE THE ONES THAT MATTER**: the Container App and its two jobs cannot be created because the GHCR package is PRIVATE. Azure validates an image reference at CREATE time and refuses with `UNAUTHORIZED: authentication required`, which is better than creating something that would never start. Package visibility has no REST endpoint, so no workflow and no token can change it; it is a click in the GitHub UI and until it happens the two-role model has never been exercised against the hosted database, because the bootstrap job is what exercises it. Container Apps rather than AKS: 30.47 USD a month in centralus against about 75 for an idle AKS control plane before a node runs. The region is decided by Azure Policy and by PostgreSQL regional availability rather than by quota, which is recorded under 1.8. |

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
| 14.1, 14.3 to 14.6, 14.8 to 14.10 | planned | Horizontal scaling, multi-cluster pools, incremental goldens, runner pools, observability, chaos, archival, disaster recovery. |

## Phase 11. Documentation site

| Component | State | Notes |
| --- | --- | --- |
| 11.2 Generated references | proven | Five references are generated and every one fails the build when it drifts: the command reference from the cobra tree, the error reference from the catalog, the transform reference from the masking registry, and a page per JSON Schema from `tools/schemadoc`. The event envelope schema itself is generated from the Go type, with a test that walks the struct so a field added without a schema entry fails rather than ships. |
| 11.4 Error catalog completeness | proven | Every entry is either returned somewhere or marked reserved, and a reserved entry that something returns fails too. It found 38 entries documenting errors this version cannot produce. |
| 11.5 Contributing and provider authoring | proven | `CONTRIBUTING.md`, `docs/contributing/provider-authoring.md` including a walkthrough of the worked example the plan asks for, and both document types with templates: RFCs in `docs/rfc/` for a change somebody outside the repository will notice, argued before the code exists, and ADRs in `docs/adr/` for what was decided and why. The worked example is `engine/internal/testutil/fakes/inmemory.go`, a complete provider in 180 lines with no database behind it, and the guide names the four things in it worth copying rather than inventing. |
| 11.6 Documentation quality assurance | written | Five of the plan's six deliverables. The four gates it names are at zero findings and run in CI: vale with the Google style at error level, cspell with a project dictionary, lychee over the assembled site including fragments, and the G8 forbidden token scan. `tools/readability` is the per page report, in the gate with a limit of 28 words a sentence, which is five words above the hardest page today, so it fires on drift rather than on style: 41 pages, mean 16.3, hardest 23.0. Screenshot freshness is enforced for the one screenshot this product has, the dashboard frame in `guides/dashboard`, which is rendered by `internal/hud` and regenerated by `just generate` rather than typed. All four gates plus the readability report now cover `examples/` as well as the site, because an example's README is the first prose most people meet. `tools/walkthrough` is the scripted new user walkthrough: `af doctor`, `af explain`, `af up`, open the URL it printed, `af status`, `af down`, each timed, with `--budget` to judge the total and a teardown that runs whether or not a step failed. It runs on a daily schedule in `walkthrough.yml` rather than on every push, for the same reason as the external link check. **It found three defects in `examples/go-api` on its first run and none of them were visible in the files:** a reachable `golang.org/x/text` advisory, a manifest declaring `DATABASE_URL` as required while the database block already injects it, and a sandbox rule whose credential cannot resolve on a machine that has never been given a test key. A fourth is fixed and unproven here: the image had no `psql` while the manifest's migrate command used it, and the full path has not yet completed on this machine, where `af up` on this example exceeded fifteen minutes against a plain `docker build` of the same image taking one minute forty eight. The daily run on a clean machine is what will settle that. |
| 11.1 Information architecture and content | written | 43 pages across the fixed architecture, and the site is live. Getting started has one page rather than the three the plan asks for, and that is a placement gap rather than a coverage one, which is a correction to what this row said first: the GitHub Actions path is documented in `guides/github` and the hosted path in `self-hosting/control-plane`, and neither is reachable from Getting started where somebody arriving would look. Genuinely missing: the framework guides the plan names by name, Next.js with Supabase, Next.js with Neon, Django, Rails, and monorepos. |
| 11.3 Examples and tutorials | written | One of the three the plan names. `examples/go-api` is a complete application against the whole configuration surface: two tables with a foreign key, three endpoints, masking rules that link the join, a sandboxed egress rule, a persona, a workflow and two invariants. It is gated rather than committed and forgotten: `just examples` and CI build every example outside the workspace and run `af explain` over every manifest, because an example that does not compile is worse than none, being the first thing a user copies. Its manifest has not been run through a full `af up`; the throwaway repository used to prove `af up --hud` was. Missing: the Next.js and Django examples, and the recorded tutorials. |

## What is not built, and why

Everything remaining needs infrastructure that does not exist yet rather than
code that has not been written:

- **8.10** is no longer blocked on an AKS decision. The control plane runs on Container Apps, where the whole stack costs 32.49 USD a month against roughly 75 for an idle AKS control plane before a node runs. The Terraform is written and plans clean; what is left is the decision to spend, and an Entra app registration so CI can plan with a federated credential instead of skipping.
- **14.1, 14.3, 14.10** still want a cluster, which costs money for as long as it exists.
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

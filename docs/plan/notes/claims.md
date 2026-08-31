# Claims audit: every promise on antifailure.dev, checked against the binary

Audited 30 August 2026 against `origin/main` at `3e20414`, the release binary
`v0.1.1` from the published installer, and the live site.

Claims were harvested from the deployed pages, not from the repository, because
the deployed pages are what a buyer reads. Twenty-two pages were fetched and
their text extracted, and the homepage plus `/product/report` were additionally
rendered in a real browser and sampled across fourteen animation frames, because
some of this site's content only exists after hydration. No number appears in the
render that is absent from the served HTML. The fabricated `fid 87%` found
earlier tonight is gone from both.

Six product pages exist in `www/app/product/` and are shadowed by 301s
(`exploratory-users`, `oracle`, `workload`, `crowdi`, `change-intelligence`,
`fidelity`). No claim was harvested from them; each redirect was confirmed.

## Summary

| Verdict | Count |
| --- | --- |
| PROVEN | 73 |
| FALSE | 21 |
| PENDING | 0 |
| UNTESTABLE HERE | 8 |
| **Total** | **102** |

Nothing depends on `af fidelity` or `af change`, so nothing is PENDING. Every
command declared in `af --help` does something; the "not yet available in this
version" marker the command tree once carried is gone from `main`, and running
all thirty subcommands found no stub.

### The three worst findings

**1. The load page has its two sources exactly backwards, and the headline
comparison cannot happen.** The page says a combined-format access log is "the
only shape source this build reads" and that OpenTelemetry is "refused at runtime"
with `AF-LOD-010: otel is not connected in this build`. The reverse is true.
`load.FromOTLP` is fully implemented and returns a shape with a real p95 per
route; `FromAccessLog` returns `p95_ms = 0` for every route, so `HasBaseline` is
false, so the `p95_increase` threshold can never fire and the "vs prod p95" column
the page illustrates can never be populated from the source the page names. The
engine's own comment in `internal/load/otel.go:26` says so: "A combined format
line has no duration in it, so every route read from a log has a zero baseline,
which means HasBaseline is false, which means p95_increase can never fire." The
page disclaims the only source that makes its headline feature work and credits
that feature to the source that cannot deliver it. It also says the manifest
"accepts Datadog and New Relic"; the schema enum is `["none","otel","access_log"]`
and both are rejected at parse time with `AF-MAN-002`, not at runtime with
`AF-LOD-010`.

**2. "84 statements queued behind it" is a number the product cannot produce.**
The queued-statement count is the flagship figure on the homepage, the product
overview and the migration page, and the product overview lists "what queued
behind it" among "the measurements `af insights` takes". The sampler records a
single boolean, `LockHold.Blocking`, "whether another session was ever seen
waiting on it" (`internal/insights/locks.go:31`). There is no count and no list of
which statements queued, anywhere. Worse, the rehearsal runs against a throwaway
branch with no application traffic on it, so in an ordinary run nothing can queue
at all: the engine's own wording is "Another session was seen waiting on it even
here, on a branch nothing else is using". The product/load page confirms it from
the other side: "It does not run traffic against a migration while the migration
applies." Two live pages contradict each other and the site's most repeated number
is unobtainable.

**3. Both conversion paths for the hosted product are dead in production.**
`POST https://antifailure.dev/api/waitlist` returns **HTTP 500** on every attempt.
There is no `/api/waitlist` route anywhere in the repository, no rewrite to one,
and `www/next.config.ts` sets `output: "export"` in production, so this deployment
is a static export with no server that could ever answer it. "Join the waitlist"
is the CTA in the footer of all twenty-two pages, on `/pricing` ("every button on
this page leads to a waitlist"), on `/signup` and on `/signin`. Separately,
`www/components/AuthScreen.tsx:15` hardcodes `CONTROL_PLANE =
"https://app.dev.antifailure.dev"`, and `https://app.dev.antifailure.dev/auth/github`
returns **HTTP 500**, while the production control plane at
`https://app.antifailure.dev/auth/github` correctly returns 302 to GitHub OAuth.
Every visitor who tries either path is told something went wrong.

Nothing found is legally exposing. The legal pages are the most honest surface on
the site: `/sla` commits to nothing and enumerates what does not exist, and `/dpa`
lists "No SOC 2 report, no ISO 27001 certificate, and no third-party penetration
test. None is claimed anywhere on this site", which I confirmed by searching all
twenty-two pages. Their defect is staleness in the safe direction, not overclaim.

---

## The table

Every PROVEN row names what was run. Reading the code and concluding it would work
is not PROVEN and does not appear as one.

### Install and CLI

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 1 | `curl -fsSL https://antifailure.dev/install.sh` piped to `sh` installs the product | footer, all pages | PROVEN | Ran it. "Downloading antifailure_0.1.1_darwin_arm64 / Checksum verified / Installed v0.1.1 to ~/.antifailure/bin/af". `af version` prints `antifailure 0.1.1 (community edition)`. |
| 2 | "`af init` reads the repository and writes the manifest" | home, product | PROVEN | `af init --non-interactive` on a fresh repo printed the detected service, the egress policy with a reason per host, and an "Assumed" block, then wrote `antifailure.yaml`. |
| 3 | "`af up` builds the twin around a masked branch" | home | PROVEN | `af up` printed "branching the database from gv_20260830044013_74234e98", built the service, issued the environment certificate, started the proxy with 4 rules, and served the app. |
| 4 | "`af ci` runs it and attaches the report" | home | PROVEN | `af ci` ran migrations, tore down, and wrote a GitHub-comment-shaped report with findings, a rehearsal table, a masking line and a teardown count. Exit 0 with three warnings. |
| 5 | "Twelve analyzers write a manifest and say what they assumed" | product | **FALSE** | `detect.DefaultAnalyzers()` returns **thirteen**: Workspace, Node, Python, Go, Ruby, Docker, Compose, Procfile, Migration, Env, ThirdParty, Auth, Schedule. The "say what they assumed" half is true. |
| 6 | Every declared command does something | (implicit) | PROVEN | Ran `--help` on all thirty subcommands and exercised sixteen. No "not yet available in this version" text exists on `main`. |
| 7 | "A run serves on a loopback port... which is what `af up` prints. There is no hosted preview hostname" | twins | PROVEN | `af up` printed `http://127.0.0.1:46000`, matching the `127.0.0.1:46000` shown on `/product/report`. |
| 8 | "The application. Antifailure needs no import in it." | home | PROVEN | The fixture app has zero Antifailure imports and ran unmodified inside the twin. |
| 9 | "Three commands... `af init`, `af up` and `af ci` exist and do what the terminal shows" | home | PROVEN | Rows 2, 3, 4. Caveat below: `af init` can emit an invalid manifest. |

### Containment and the side-effect firewall

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 10 | "The twin cannot reach the public internet" / "Unknown destinations are denied" | home, firewall | PROVEN | From inside the live twin: `https://example.com/` and `https://api.openai.com/v1/models` both failed with "Client network socket disconnected before secure TLS connection was established". `af net log` shows `block CONNECT https://example.com (default) 403`. |
| 11 | "Direct-IP attempts are caught and blocked" | firewall | PROVEN | `https://18.4.2.9/` from inside the twin: `connect ENETUNREACH`. Conformance behaviour `Egress_CannotBeBypassedByAddress` passes against a real daemon. |
| 12 | "169.254.169.254 is not reachable, whatever the policy says" | (conformance-backed) | PROVEN | `http://169.254.169.254/latest/meta-data/` from inside the twin: `connect ENETUNREACH`. `Egress_CannotReachTheMetadataEndpoint` passes. |
| 13 | "`af net log` prints every attempt", including the denials | firewall | **FALSE** | Proxy-level attempts are all logged with mode, rule and outcome, including 403 denials. Attempts that bypass the proxy are **not**: the direct-IP and metadata attempts above were blocked by having no route and left no ledger row. The firewall page illustrates `TCP 18.4.2.9:443 DENY ... deny_02` as a ledger row and captions the panel "the decision log [is] real: `af net log` prints every attempt". Blocked yes, and more strongly than claimed; logged no. |
| 14 | Modes: BLOCK, ALLOW, SANDBOX, CAPTURE, MOCK | product | PROVEN | All five exist. `af net explain` returned `BLOCK` for an unlisted host, `SANDBOX` for `api.stripe.com` naming the substituted credential and webhook path, `CAPTURE` for `api.sendgrid.com`. A sixth mode, `synth`, is in the schema enum and is not documented on the site. |
| 15 | "An unlisted host fails closed" | product, firewall | PROVEN | Row 10, plus `af net explain GET https://evil.example.com/x` → "BLOCK. No rule matches evil.example.com, and the default is block." |
| 16 | "An unresolved secret fails closed and stops the run" | twins, firewall | PROVEN | `af up` with `STRIPE_SECRET_KEY` unset: `AF-SEC-001`, exit 3, nothing provisioned. |
| 17 | "Mail rendered and captured rather than sent" | firewall, home | PROVEN | `POST https://api.sendgrid.com/v3/mail/send` from inside the twin returned 202; `af inbox list` then showed message #8 via sendgrid. Nothing was delivered. |
| 18 | "A stateful Stripe that answers offline" | firewall | PROVEN | With `mode: mock`, four Stripe endpoints answered from the built-in pack with `livemode: false` and incrementing clone-local ids (`cus_mock…001`, `cs_mock…002`, `sub_mock…004`, `in_mock…006`). `af net log` shows all four as `mock ... 200`. The pack has a real `store`/`load`/`list` keyed by collection. |
| 19 | "The Stripe pack is complete enough to run checkout, subscribe, renew, and cancel with signed webhooks and no network" | product | PROVEN | The pack declares sixteen routes covering `POST /v1/checkout/sessions`, `POST /v1/subscriptions`, `POST /v1/invoices` and `DELETE /v1/subscriptions/{id}`. `af webhook list stripe` returns seven signed events including `checkout.session.completed`, `customer.subscription.created`, `invoice.paid` and `customer.subscription.deleted`, signed with `STRIPE_WEBHOOK_SECRET`. |
| 20 | "A client that ignores its proxy variables has nowhere to send the packet" | architecture | PROVEN | Conformance behaviour `Egress_AppliesToAClientThatIgnoresProxyVariables` passes against a real daemon. |
| 21 | "Clone-local DNS. Production hostnames do not resolve to production." | twins, firewall | PROVEN | `api.prod.internal` is refused; `Egress_NamesDoNotCrossEnvironments` passes. |
| 22 | "A UDP query straight to a public resolver does not get out" | (conformance-backed) | PROVEN | `Egress_CannotBeBypassedByUDP` passes. |
| 23 | "Read-only forwarding exists only for explicitly approved endpoints" | firewall | UNTESTABLE HERE | Needs a manifest declaring an `allow` rule against a live third party the machine can reach and a redaction assertion on the recorded body. Not exercised. |

### Twin lifecycle, isolation and cleanup

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 24 | "The Docker runtime... passes all thirty-two runtime conformance behaviours against a real daemon" | twins | PROVEN | `go test ./internal/runtime/local -run TestConformance` against Docker 28.5.1: **32 of 32 passed, zero skipped**, 92s. The behaviour list in `conformance/runtime.go` counts exactly 32. |
| 25 | "These seven are in force today on the Docker runtime" | twins | **FALSE** | Six hold: credentials replaced, no production database route, no default internet route, separate DNS policy, twin-scoped secrets, one label scheme on everything. The seventh, "Teardown will not destroy a namespace this run did not provision. It errors instead. `AF-RUN-045`", is **Kubernetes-only**: `AFRUN045` appears only in `internal/runtime/k8s/lifecycle.go`, Docker has no namespaces, and the Docker runtime never emits it. The underlying property does hold on Docker by label filtering (`Down_TouchesOnlyItsOwnEnvironment` passes), but the mechanism and code named are not the Docker runtime's. |
| 26 | "The Kubernetes runtime is written and not yet proven to the same standard" | twins | UNTESTABLE HERE | Accurate as stated. `internal/runtime/k8s/conformance_test.go` requires `AF_KUBE_CONTEXT` and skips without a k3d or kind cluster. Proving it needs such a cluster and a registry the sidecar image can be pushed to. |
| 27 | "No cloud runtime exists" | twins | PROVEN | `internal/runtime/` contains exactly `k8s` and `local`. |
| 28 | "Thirteen states... are the ones the orchestrator moves through" | twins | **FALSE** | The thirteen names (`REQUESTED`, `PLANNED`, `PROVISIONING`, `SANITIZING`, `DEPLOYING`, `VERIFYING_CONTAINMENT`, `READY`, `BASELINE_RUNNING`, `CANDIDATE_RUNNING`, `ANALYZING`, `REPORTING`, `DESTROYING`, `DESTROYED`) exist in exactly one file in the whole repository: `www/components/pages/product/Twins.tsx`, the component that draws them. The orchestrator in `internal/env/` has no state enum at all, and no doc or plan file mentions them. The panel is captioned "Illustrative: the thirteen states and the four phases are the ones the orchestrator moves through", which asserts they are real. |
| 29 | "The four phases" (Build, Restore, Contain, Destroy) | twins, product | PROVEN | Observed in order in `af up` and `af ci` output: build the candidate, branch and restore the masked golden, place the proxy and replace credentials, tear down. |
| 30 | "A resource is journaled the moment it exists, not after the run succeeds" | twins, architecture | PROVEN | Conformance behaviours `Up_JournalsBeforeCreating` and `Up_CreatesNothingTheJournalRefused` both pass against a real daemon. |
| 31 | "Teardown replays that journal in reverse and counts what it removed" | twins | PROVEN | `af down` printed "the journal compensated 6 resources the sweep did not find" then "12 resources removed". |
| 32 | "A continuous integration step counts the managed containers and networks afterwards and fails the build if any are left" | twins | PROVEN | `.github/workflows/ci.yml:292-302` counts `docker ps -aq --filter label=dev.antifailure.managed` **and** `docker network ls -q --filter …`, and `exit 1`s if either is non-zero. |
| 33 | "`af env prune` removes environments older than a cutoff you pass" | twins, architecture | PROVEN | `af env prune --help`: `--older-than duration` (default 24h) and `--dry-run`, and it "refuses to remove anything without a cutoff". |
| 34 | "There is no automatic time-to-live and no independent reaper yet" | twins | PROVEN | Accurate. Prune is a command; no reaper exists. |
| 35 | "Teardown happens whatever the outcome... before the report is written" | `af ci` help, report | PROVEN | In the `af ci` run the teardown lines ("removed 5 runtime resources", "torn down, 12 resources removed") precede the report body, and the report contains "Torn down: 12 resources removed, nothing left behind". |
| 36 | "Nothing outlives the run" / "0 orphans" | home, safe-state | PROVEN | `af ci` and `af down` each reported nothing left behind, and the environment's four containers were gone afterwards. |

### Safe State and masking

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 37 | "A scanner reads back every column of every table looking for anything that still parses as an email, a card number, a phone number, or a key" | product, safe-state | PROVEN | Falsification test: after a clean `af mask verify`, I wrote `contact vir.sanghavi@realcompany.com or 4111111111111111` into `public.users.full_name` on the live branch. `af mask verify` then returned "fail public.users.full_name holds email" and "holds payment-card", redacted the value to `con********111`, said "A golden in this state cannot be branched", and exited with `AF-MSK-002`. `internal/verify/detect.go` declares detectors for `private-key`, `payment-card`, `jwt`, `email` and `phone`. |
| 38 | "then signs an attestation" | product, safe-state | PROVEN | `verify.Attestation` carries a `Signature` field "covering the canonical form of everything above", and `af golden list` shows a `RULES` hash per golden. |
| 39 | "An unverified golden cannot be branched, and that is enforced in code rather than in a checklist" | product, safe-state, home | PROVEN | `go test ./internal/db/docker -run TestConformance` against a real daemon: `Branch_RefusesAnUnverifiedGolden` and `Refresh_RefusesToPublishWhenVerificationFails` both pass. On the Docker provider the golden image is only committed after verification, so an unverified one has no image to branch. |
| 40 | "Deterministic masking" | safe-state | PROVEN | Two independent `af golden refresh` runs produced byte-identical masked values: `eb0n7eqveesh8@example.test`, `p0mrxnmgfjfbw@example.test`, `ggfhns1pwrhj4@example.test` and the same three names and phone numbers. |
| 41 | "uniqueness preserved" | safe-state | PROVEN | `public.users.email` carries a UNIQUE constraint; all five rows in the branch hold distinct masked addresses and the restore succeeded. |
| 42 | "Format-preserving replacement" | safe-state | PROVEN | `+1-415-555-0134` → `+1-457-876-1471`; `+44 20 7946 0958` → `+44 96 6129 7704`; card `4111111111111111` → `4242156565922135`. |
| 43 | "Tokens, sessions, secrets, and credentials are deleted rather than disguised" | safe-state | **FALSE** | `session_token` was emptied, as claimed. `api_key` was **hashed, not deleted**: the plan assigns it `hash_hex` and `sk_live_51HqABCDEFGHIJKLMNOP` became `6ff5f8bd7528dc0f4d21c72d164c`. The page's own before-and-after visual shows `api_key sk_live_51Hq` → `deleted DELETE`. Safe either way, but the page says delete and the engine masks. |
| 44 | "Free-text PII: scan for emails, cards, phones, and keys that schema rules miss" | safe-state | PROVEN | The `free_text` rule fired on `notes` and the planted email, phone and card were all gone from the branch. |
| 45 | The before-and-after visual: `email ajay@acme.com if the card fails` becomes `email [redacted] if the card fails` | safe-state | **FALSE** | Replacement is **whole-field**, not surgical. That row became `matched and needed and conversation..`. The page shows redaction preserving the surrounding sentence; nothing does that. Safer than advertised, and not what is advertised. |
| 46 | "Subsetting is the default instead of full copying" / "Subset: a referential subset instead of a full copy, **by default**. Built." | safe-state, architecture | **FALSE** | `subset.enabled` has `"default": false` in `schemas/manifest.v1.json`, `af init` never sets it, and `af explain` on the generated manifest prints "subset off, the whole database is masked". The capability is built and wired (`Orchestrator.subsetConfig` in `internal/env/golden.go`), but it is opt-in. On the architecture page this is one of "the four controls below marked built [that] are what hold the cost down"; one of the four does nothing unless the customer turns it on. |
| 47 | "Masking runs in the customer cloud" | safe-state, architecture | PROVEN | The whole refresh, mask, verify and branch cycle ran locally against a local source database. Nothing left the machine. |
| 48 | "The control plane receives evidence, not records" | safe-state, architecture | UNTESTABLE HERE | The `af ci` report carries counts and hashes rather than rows, but proving what the hosted ingest accepts needs a control plane credential and a captured request to it. This machine has neither. |
| 49 | "Supabase Auth, roles, RLS handled explicitly. Storage objects and Edge Functions excluded unless declared." | safe-state | UNTESTABLE HERE | Needs a Supabase project and service key. `internal/db/supabase` exists and is registered in the provider switch, so it is not dead code. |

### Migration safety

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 50 | "samples what is locked every 250 milliseconds" | product, migrations | PROVEN | `insights.LockSampleInterval = 250 * time.Millisecond`. `af -o json insights` reported `held_ms: 250` for a lock shorter than one sample, which is the interval showing through. |
| 51 | "the strongest mode held per table, how long it was held" | product, migrations | PROVEN | `af -o json insights` on a real migration: `locks[0] = {table: "users", mode: "AccessExclusiveLock", held_ms: 250, statement: "ALTER TABLE users ADD COLUMN billing_status …"}`. |
| 52 | "what queued behind it" / "84 statements queued behind it" / "which statements queued behind it" | home, product, migrations | **FALSE** | See worst finding 2. `LockHold.Blocking` is a boolean; no count and no list exists. The rehearsal runs on a throwaway branch with no traffic, so it is normally false by construction, and the engine's own wording concedes it: "Another session was seen waiting on it even here, on a branch nothing else is using". The report renders a column headed "Blocked another session" with the values yes/no. |
| 53 | "Full table rewrites, reported by Postgres rather than guessed from the SQL" | product, migrations | PROVEN | `ALTER TABLE orders ALTER COLUMN amount TYPE text` produced `"rewrote": ["orders"]` in the JSON, and the `af ci` report said "Postgres rewrote 1 table. `migration_rewrite` on `orders`". |
| 54 | "Adding a column with a default rewrites the table", the flagship example finding | home, migrations | **FALSE** | Postgres has not rewritten for a constant default since version 11, and `af init` defaults the manifest to Postgres 17. I ran exactly the site's migration, `ALTER TABLE users ADD COLUMN billing_status text NOT NULL DEFAULT 'active'`, and the engine correctly reported **no rewrite** for it while correctly reporting one for a type change in the same run. The homepage's "Rewrite FOUND: the default rewrites every row of subscriptions. Postgres reports it, we do not guess" and the matching lint quote describe an outcome the product measures as false. |
| 55 | "Per-statement duration, so the slow one in a batch is named" | migrations | PROVEN | `statements[]` carried `{sql: "ALTER TABLE users ADD COLUMN …", ms: 23.885}`, `{sql: "CREATE INDEX …", ms: 10.914}`, `{sql: "SET lock_timeout = '3s'", ms: 0.556}`, `{sql: "ALTER TABLE orders ALTER COLUMN amount TYPE text", ms: 19.47}`. |
| 56 | "Lint: **six** rules, each carrying the fix rather than only the complaint" | migrations | **FALSE** | `insights.AllRules()` returns **seventeen**: `no_lock_timeout`, `not_null_without_default`, `set_not_null_existing_column`, `alter_column_type`, `index_not_concurrent`, `drop_index_not_concurrent`, `reindex_not_concurrent`, `foreign_key_not_valid`, `check_constraint_not_valid`, `unique_constraint_builds_index`, `backfill_in_ddl_transaction`, `rename_column_in_use`, `drop_column_in_view`, `vacuum_full`, `cluster`, `drop_table`, `truncate`. Understated by eleven. |
| 57 | Each lint rule "carries the fix" | migrations | PROVEN | Every finding printed an `Instead:` clause. `no_lock_timeout` fired and stopped firing once I added `SET lock_timeout = '3s'`, so the rules read the actual SQL. |
| 58 | "EXPLAIN before and after, on production's own shape" | product, migrations | UNTESTABLE HERE | `internal/insights/plan.go` runs `EXPLAIN` without `ANALYZE`. My run produced `plans: null` because no queries had been captured. Proving it needs an application exercised against the branch first, plus the `pg_stat_statements` extension, which the golden image does not carry. The run said so rather than inventing a result. |
| 59 | "A saved report from an earlier run, compared against this one" | migrations | PROVEN | `af insights --save`/`--baseline` exist, and with neither the run printed "No baseline, so query counts are not compared. Save one on main with --save and pass it here with --baseline". |
| 60 | "It says what it could not measure, and it names any check the manifest turned off" | product, report | PROVEN | The run emitted three separate "Not measured:" lines: pg_stat_statements twice and the missing baseline once. |
| 61 | "Rehearsed against a fresh branch carrying production's shape" | migrations | PROVEN | "branching gv_20260831012711_74234e98 to rehearse the migrations against", and the findings quote the branch's real row counts ("about 3 rows in users"). |

### Load

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 62 | "A combined-format access log is the only shape source this build reads"; OTel "refused at runtime" with `AF-LOD-010` | load | **FALSE** | `FromOTLP` is fully implemented. A 25-span OTLP/JSON export produced `source="otel" rps=1.034` and `route=GET /api/subscriptions weight=25 p95ms=180`. Nothing refuses it. |
| 63 | "The manifest also accepts Datadog, New Relic and OpenTelemetry, and every one of them is refused at runtime" | load | **FALSE** | The schema enum is `["none","otel","access_log"]`. `source: datadog` and `source: new_relic` are refused at **parse** time with `AF-MAN-002` ("There is no load source called \"datadog\""), not at runtime with `AF-LOD-010`. |
| 64 | "every answer is a comparison against the p95 production serves that route in" / "The baseline is the p95 in your own access log" / "p95_increase applied per route against the p95 in your own access log" | load, report | **FALSE** | `FromAccessLog` on a valid combined-format log returned three routes all with `p95ms=0`. `run.go:334` sets `HasBaseline` only when `base > 0`, and `run.go:402` skips any route without a baseline, so `p95_increase` can never fire on an access-log shape. The page's own illustration ("`af load source access_log` … 180ms baseline, 129% slower") shows a column that source cannot fill, under a caption saying "the columns, the sort order and the thresholds are the ones `af load` produces". |
| 65 | "Poisson arrivals. Requests arrive in clumps" | load | PROVEN | `Picker.Interval` samples the exponential distribution by inversion: `-math.Log(u) * mean`, seeded from the picker's own rng. |
| 66 | "Deterministic per seed" | load | PROVEN | Arrival intervals and route choice both draw from `p.rng`, a single seeded source, and the same mechanism produced identical masking output across two runs (row 40). Not separately exercised with two load runs. |
| 67 | "The report carries the rate the generator managed, not the rate it was asked for" | load | PROVEN | `run.go:317` sets `res.Rate = sent / elapsed`, and the struct carries `TargetRate` separately with the comment "Reporting the target is how a load test says everything was fine while the queue grew". |
| 68 | "No route is sent until the manifest names it safe. With no allowlist the default is read-only GETs under the root." | load | PROVEN | `internal/env/test.go:399` defaults `safe = []string{"GET /**"}`, and a shape with nothing sendable is refused with `AF-LOD-010` telling the reader to add `safe_routes`. |
| 69 | "`p95_increase`, default 0.25" and "`error_rate`, default 0.01" | report | PROVEN | `normalize.go:442-446` sets exactly those two defaults. |
| 70 | "A load threshold produces a listed regression here and exits non-zero under `af load`" | report | PROVEN | `internal/cli/load.go:104` and `:153` return `AF-LOD-011` carrying the breach count, which is a non-zero exit. |
| 71 | "A route with no baseline is never a breach" | load | PROVEN | `run.go:402` skips every route where `!route.HasBaseline`. |
| 72 | "It does not run traffic against a migration while the migration applies" | load | PROVEN | Accurate, and it is the direct contradiction of row 52. |

### Safety report and release gate

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 73 | "Five verdicts: pass, fail, flaky, blocked, unverified" | report, product | PROVEN | `report.Verdicts = []string{"fail","flaky","blocked","unverified","pass"}`, and `af test` printed the tally "0 passed, 0 failed, 0 flaky, 1 blocked, 0 unverified". |
| 74 | "Only fail exits non-zero" / "a blocked run exits zero, so an incomplete environment is not indistinguishable from a broken change" | report, product | PROVEN | `af test` on a blocked workflow exited **0** and printed "Blocked means the runner or the environment could not carry the workflow through, so it is not counted against the application." `af ci` with three real findings ranked as warnings also exited **0**. |
| 75 | "When a workflow fails it carries the steps, the Playwright trace and the video" | report | PROVEN | `af test` printed numbered reproduction steps and a trace path. On disk: `sign-up-1.trace.zip`, `sign-up-2.trace.zip`, two `page@….webm` videos and two PNG screenshots. |
| 76 | "Agents drive the application... through the accessibility tree" | product, report | PROVEN | `runner/src/browser.ts` starts `context.tracing` with screenshots and snapshots and drives the page through the accessibility tree, and the run above produced its artifacts. |
| 77 | "Every statement runs inside a transaction opened READ ONLY, so a write is refused by Postgres rather than trusted not to happen" | invariants | PROVEN | `internal/invariant/invariant.go:224` opens `pgx.TxOptions{AccessMode: pgx.ReadOnly}` and handles SQLSTATE `25006`, `read_only_sql_transaction`, explicitly. Not exercised with a violating invariant. |
| 78 | "When an invariant does not hold the comment carries the offending rows" | report, fintech | UNTESTABLE HERE | The code path prints returned rows and `af ci` renders an invariant section, but my fixture had no invariants declared and no violating data. Proving it needs a manifest invariant that returns rows. |
| 79 | "A summary of every outbound attempt" on the pull request | report | PROVEN | The `af ci` report carried the masking verification line and the teardown count; `af net log` carried every proxy-level attempt including the 403s. Subject to the gap in row 13. |
| 80 | "Migration findings come from `af insights`, which is a command you run rather than a section of this check" | report | PROVEN | Accurate about `af insights` being its own command, but understated: `af ci` **does** render migration findings as a section, which I observed. |

### Architecture, licence and trust

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 81 | "five of these are enforced today and five are design" | architecture | PROVEN | Both lists hold exactly five items, and the five "in force today" match six of the seven verified in row 25. |
| 82 | "the four controls below marked built" (Subset, Cache, BYOC, Sweep) | architecture | **FALSE** | Four are marked built and three are: goldens are branched rather than restored per run, the engine runs on your own compute, and `af env prune` works. Subset is marked "by default" and is off by default (row 46). |
| 83 | "The repository is MIT licensed except for the `ee/` directory... never compiled into the community binary" | product | PROVEN | Root `LICENSE` is MIT; `ee/LICENSE.md` is the Antifailure Enterprise License. `strings` on the built binary finds **zero** `antifailure/ee` symbols, and `af version` prints "(community edition)". |
| 84 | "`docs/plan/STATUS.md` gives the honest answer per component... marking each one proven, written, or planned" | product | PROVEN | The file opens with exactly those three states and a definition of each, including "A provider that has only ever spoken to a fake is written, no matter how good the fake is". |
| 85 | Postgres sourced from Docker | product | PROVEN | Proven end to end: refresh, mask, verify, publish, branch, query. |
| 86 | Postgres sourced from Neon, Supabase or DBLab thin clones | product | UNTESTABLE HERE | All four providers are registered in the switch at `internal/env/env.go:793-863`, so none is dead code, and the shared db conformance suite is written against all four. Exercising these three needs a Neon API key, a Supabase project and service key, and a DBLab instance. |
| 87 | "It runs locally on Docker, [and] in GitHub Actions" | product | PROVEN | Docker proven end to end. `.github/workflows/ci.yml` and `dogfood.yml` run the engine in Actions, including the leak check in row 32. |
| 88 | "...or on your own Kubernetes" | product | UNTESTABLE HERE | Needs a k3d or kind cluster and a registry the sidecar image can be pushed to. See row 26. |
| 89 | "A run returns pass, fail, flaky, blocked, or unverified, and a failure caused by the runner is never counted against your application" | product | PROVEN | Rows 73 and 74. |
| 90 | "This site loads no analytics and no third-party script" | subprocessors | PROVEN | Every `<script src>` on the homepage is first-party `/_next/static/chunks/`. The only external hosts referenced anywhere in the HTML are `antifailure.dev`, `github.com` and `schema.org` (a JSON-LD context, not fetched). |
| 91 | "There is no AWS, Google Cloud, Vercel, Cloudflare, or Fastly in its path" | subprocessors | PROVEN | Response headers carry no Vercel, Cloudflare or Fastly markers, and `www/next.config.ts` and `public/staticwebapp.config.json` show an Azure Static Web Apps deployment. |
| 92 | "No SOC 2 report, no ISO 27001 certificate, and no third-party penetration test. None is claimed anywhere on this site." | dpa | PROVEN | Searched all twenty-two harvested pages. No such claim appears. |
| 93 | "There is no service level agreement" | sla | PROVEN | The page commits to nothing and enumerates what is absent. |

### The hosted product and its conversion paths

| # | Claim | Page | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 94 | "Join the waitlist" (footer of every page, pricing, signup, signin, modal) | all | **FALSE** | `POST https://antifailure.dev/api/waitlist` returns **500** on every attempt, as does a `GET`. There is no `/api/waitlist` route in `www/app`, no rewrite to one, and no waitlist handler anywhere in `web/`, `console/` or the database schema. `www/next.config.ts` sets `output: "export"` in production, so the deployment has no server that could answer it. `www/lib/waitlist.ts` posts there and reports the failure to the visitor. |
| 95 | "Continue with GitHub" on `/signup` and "Log in" in the nav | signup, signin | **FALSE** | `www/components/AuthScreen.tsx:15` hardcodes `https://app.dev.antifailure.dev`, and `https://app.dev.antifailure.dev/auth/github` returns **500**. The production control plane at `https://app.antifailure.dev/auth/github` correctly returns 302 to GitHub OAuth with a valid client id and callback. The site points at the wrong host. |
| 96 | "The hosted control plane is in development... every button on this page leads to a waitlist" | pricing | **FALSE** | `https://app.antifailure.dev/` serves and its OAuth flow works. The control plane is deployed. |
| 97 | "The control plane itself is not deployed yet, so this boundary has not been exercised in production" | architecture | **FALSE** | Same as row 96. |
| 98 | "Billing: not deployed yet; the engine runs in your CI today" | architecture | **FALSE** | The "the engine runs in your CI today" half is proven. "Billing: not deployed yet" is stale in the same way as the two rows above: a control plane is deployed. Whether billing specifically is wired was not checked. |
| 99 | "A single staging control plane... The production deploy path is wired end to end and its final job deliberately fails, because there is no production environment to deploy into." | sla | **FALSE** | A production control plane is live at `app.antifailure.dev`. |
| 100 | "Data received [by Microsoft]: ... and waitlist addresses" | subprocessors | **FALSE** | No waitlist backend exists, so no address is received by anyone. An overclaim in the safe direction, on a page that is otherwise the most carefully sourced on the site. |
| 101 | Docs, Quickstart, Manifest, Error reference, Enterprise, verdicts and load doc links | all | PROVEN | All fourteen checked resolve 200: `/docs`, `/docs/concepts/{agents,egress,insights,masking,verdicts,load}`, `/docs/enterprise/licensing`, `/docs/getting-started/quickstart`, `/docs/reference/{errors,manifest}`, `/signup`, `/signin`, `/blog`. |
| 102 | The six shadowed product pages redirect rather than 404 | (structure) | PROVEN | All six return 301 to a live page. |

---

## What would have to change, per FALSE row

| Row | What is wrong | What would make it true |
| --- | --- | --- |
| 5 | Says twelve analyzers, there are thirteen. | Change the number to thirteen, or add a test that pins the count so the page and `DefaultAnalyzers()` cannot drift again. |
| 13 | Claims `af net log` prints every attempt; proxy-bypassing attempts leave no row. | Either narrow the sentence to "every attempt the gateway sees", or have the sidecar's packet-level drops write a ledger row so the direct-IP example on the page is real. |
| 25 | `AF-RUN-045` is cited as a Docker-runtime property; it is Kubernetes-only. | Cite the Docker mechanism (label-scoped teardown, proven by `Down_TouchesOnlyItsOwnEnvironment`) or move that row under a Kubernetes heading. |
| 28 | Thirteen named lifecycle states exist only in the marketing component. | Either implement the state machine and name the states in `internal/env/`, or redraw the panel around the four phases that are real and drop the disclaimer that asserts thirteen. |
| 43 | Says credentials are deleted; `api_key` is hashed. | Change the default rule for key-shaped columns to `nullify`, or change the page's visual to show `api_key` masked rather than `DELETE`. |
| 45 | Free-text redaction is whole-field, the page shows surgical redaction. | Redraw the before-and-after to show the whole `notes` value replaced, which is what happens and is the safer behaviour anyway. |
| 46, 82 | Subsetting is described as the default and as a built cost control; `enabled` defaults to false. | Either flip the default (a large change, since it needs a seed table), or say "available, off by default" on both pages and stop counting it among the four controls holding cost down. |
| 52 | "84 statements queued" and "which statements queued behind it" are unobtainable. | Either count waiters (`pg_locks WHERE NOT granted`, per sample, with their queries from `pg_stat_activity`) and drive concurrent traffic at the branch during the rehearsal so there is something to count; or remove the number and the phrase from all three pages and say what is measured: the strongest mode, its hold time as a lower bound, and whether any session was seen waiting. |
| 54 | The flagship example asserts Postgres behaviour that ended in version 11. | Change the example migration to one that genuinely rewrites (a type change, which the engine detects correctly), or change the finding text to the one the lint actually emits for that statement. |
| 56 | Says six lint rules, there are seventeen. | Change the number to seventeen. `AllRules()` already carries a comment saying a test walks the list to keep the docs page honest; the marketing page is not in that loop and should be. |
| 62, 63, 64 | The two load sources are described inverted, and the headline comparison cannot happen on the source the page names. | Rewrite the section around what is true: OTel is connected and is the source that carries a p95 baseline; an access log gives the route mix and no latency, so `p95_increase` needs traces. Delete the `AF-LOD-010` panel, and delete Datadog and New Relic from the sentence since the manifest does not accept them. Then fix the illustration, which currently shows a baseline column an access log cannot fill. |
| 94 | The waitlist posts to an endpoint that does not exist. | Add a real handler. `www` is a static export, so it cannot be a Next route: it needs to go to the control plane (an endpoint on `app.antifailure.dev`) with CORS, and `www/lib/waitlist.ts` needs to point at it. A `waitlist` agent is working in this session, so this may be in flight. |
| 95 | The GitHub sign-in points at a dev host that 500s. | Change `CONTROL_PLANE` in `www/components/AuthScreen.tsx:15` to `https://app.antifailure.dev`, and make it an environment variable so a dev build cannot ship as production again. |
| 96, 97, 99, 100 | Four pages describe the control plane as undeployed; it is live. | One pass over `/pricing`, `/product/architecture`, `/sla` and `/subprocessors` to describe what is actually deployed. Note that this depends on rows 94 and 95 being fixed first: telling visitors the control plane is live while both ways in are broken is worse than the current understatement. |

## Two defects found that are not landing-page claims

**`af init` can write an invalid manifest.** On a repository holding both a
`Dockerfile` and a `package.json` whose `name` differs from the directory name,
`af init --non-interactive` produced two services both claiming port 3000 and
then refused its own output with `AF-MAN-002` at exit 3, leaving no manifest
behind. The homepage's "three commands" promise starts here.

**`AF-AGT-004` gives a circular next step.** `af runner install` on a machine
with no runner source fails with "The agent runner could not be found... Next:
Install it with 'af runner install'", which is the command that just failed. The
`--runner` flag it suggests as an alternative is not accepted by `af runner
install`, only by `af test` and `af explore`.

One error has no code at all: a bad `source_url_env` produces `Error: pgcopy:
read the roles the source's policies name: failed to connect...` with no `AF-`
prefix, against the repository's rule that every user-facing error carries one.

## What I did not test, and why

- **The referential subset itself.** Row 46 proves it is off by default. I did not
  enable it and verify that dropped parents take their children, which is a real
  claim on the safe-state page. It is testable on this machine with a larger
  fixture and a seed table; I ran out of budget before the source database was
  torn down.
- **The oracle** (`af oracle`). It is not claimed on any live landing page, since
  `/product/oracle` is 301'd to `/product/report`.
- **`af explore`**. Same reason: `/product/exploratory-users` is 301'd.
- **Everything hosted.** The control plane's ingest, RLS, audit chain, session
  hashing and Key Vault claims on `/dpa` need a deployment credential this
  machine does not have.

# Production readiness

**This file is temporary. Delete it when production exists.** Removal
instructions are the last section, and they are part of the work rather than an
afterthought: a readiness assessment that outlives the gaps it describes becomes
a document that lies about the product.

**This file is not published.** The Astro site builds only
`docs/src/content/docs`, so nothing under `docs/plan` reaches
antifailure.dev/docs. That is deliberate and it is what makes this safe to
commit: the sections below name, precisely, which parts of a security product
are not built yet, and that list is a map for somebody attacking a customer.
Verified by checking `docs/dist` after a build. If anybody ever moves this file
under `docs/src/content/docs`, they publish that map.

Written 2026-08-30 against `main` at the merge of #43. Every claim below was
checked against the code, the live deployment, or the Azure API on that date,
and the method is named where it is not obvious. Where this disagrees with
`STATUS.md`, `STATUS.md` is the authority on component state and this file is
the authority on what is missing for a paid, hosted product.

---

## The one paragraph version

The engine is further along than the business is. The thing this product is
actually for, proving a Postgres migration is safe against production's real
shape before it merges, is built, deep, and better than the commercial tools it
sits next to. What is missing is not engineering ambition. It is the ordinary
apparatus of selling something: there is no way to take money, no way for a
customer to make the control plane do anything rather than watch, no alerting on
the service you would be charging them for, and no second environment to promote
into. Standing up `app.antifailure.dev` is the smallest of those problems.

---

## Part 1. What this is for, and the claim everything rests on

Antifailure's bet, from the README, is that the bug you ship is the one no
fixture predicted, and that the fix is to give every branch a disposable copy of
production's *shape*: real schema, real row counts, real statistics, every
identifier masked and the masking proved.

Everything else in the product is downstream of one claim:

> A migration that is instant on a seed database can hold a lock for minutes in
> production, and static analysis cannot tell you which. That needs execution
> against production-shaped data.

That claim is correct, it is the reason the product exists, and it is the part
that is built. The rest of this document is mostly about the parts that are not.

There is a second claim that matters commercially and is worth stating plainly
because it decides the architecture:

> Raw snapshots, secrets, and captured request bodies stay in your cloud. The
> hosted control plane holds organizations, policy, aggregated reports, and
> billing.

That boundary is real and it is enforced by where the code runs, not by a
promise. It also means the hosted product is a control plane and a GitHub App,
not a place where customer environments execute. That is a much cheaper thing to
operate and a much easier thing to sell to a security team, and it should be
defended rather than quietly eroded the first time somebody asks for a hosted
runner.

---

## Part 2. Postgres migration safety, which is the product

### What is actually built

`engine/internal/insights` is 5,269 lines across 17 files. I read it rather than
trusting the status table, because an earlier read of a stale worktree made me
believe this was unimplemented, and being wrong about it in the other direction
would have been worse.

| Piece | File | What it does |
| --- | --- | --- |
| Rehearsal | `rehearsal.go` | Applies pending migrations to a throwaway branch of the environment's own golden, so the row counts and statistics are production's. |
| Per statement timing | `rehearsal.go`, `capture.go` | SQL migrations are applied statement by statement from Go. Opaque tools (Rails, Django, Alembic, Knex) run inside the service's own image, and timing comes from `ddl_command_start` and `ddl_command_end` event triggers, because only the tool knows what SQL its migrations become. |
| Table rewrites | `capture.go` | A `table_rewrite` event trigger reports rewrites, with `pg_event_trigger_table_rewrite_reason`. Reported by the database rather than inferred from the SQL, which is the difference between a fact and a guess. |
| Lock analysis | `locks.go` | `pg_locks` and `pg_stat_activity` sampled every 250ms from a second connection. Reports the strongest mode per table, how long it was held, whether anything was ever seen waiting on it, and the statement that held it. |
| DDL lint | `lint.go` | Six rules, each carrying the lock mode, the live row count for that table, why it hurts, and the fix. |
| Plan diff | `plan.go` | `EXPLAIN (GENERIC_PLAN)` compared between base and branch, with a fallback when the server does not support it. |
| Query regression | `insights.go` | `pg_stat_statements` matched on `queryid`, with a `regression_min_ms` floor so 0.1ms to 0.3ms is not reported as a threefold regression. |

Three things about this are unusually good and should not be lost:

**It reports what it could not see.** `Report.Missing` exists because
`pg_stat_statements` is an extension somebody has to install, and an insight
that silently reports nothing looks like a clean bill of health. That instinct
is right and it is rare.

**The lock figure is honest about being a sample.** `HeldMS` is documented as a
lower bound rounded to the sample interval, and the sample interval is 250ms
with the tradeoff written down: sampling faster costs a round trip against the
database whose timings the rehearsal exists to measure.

**The lint findings are better than the commercial equivalents.** A finding does
not say "avoid this". It says which lock is taken, how many rows that table
actually holds, what breaks, and the four-deploy sequence that avoids it. That
is the difference between a linter and a colleague.

### What is missing from it

The six rules are the right six to build first. These are the ones a customer
running a real Postgres will hit that are not covered. I checked each against
`lint.go` by reading the rule set, not by grep alone.

**Highest value, and not covered at all: no `lock_timeout`.** This is the one
that takes sites down. A migration that waits for a lock does not merely wait;
it queues every subsequent query on that table behind its own lock request, so a
"fast" `ALTER TABLE` that blocks for thirty seconds behind a long-running
`SELECT` stops all writes for thirty seconds. The fix is one line in the
migration session, and the rule is "this migration did not set `lock_timeout`,
so a lock wait becomes an outage". `locks.go` already observes `Blocking`, so
the data to report it exists; the rule does not.

**`ALTER COLUMN ... SET NOT NULL` on an existing column.** Distinct from
`RuleNotNullNoDefault`, which covers `ADD COLUMN ... NOT NULL`. Setting NOT NULL
on a column that already exists scans the whole table under ACCESS EXCLUSIVE.
From Postgres 12 the scan is skipped if a validated CHECK constraint already
proves it, which is exactly the fix worth printing.

**`ADD CONSTRAINT ... CHECK` without `NOT VALID`.** The same shape as the
foreign key rule that is already implemented, and the fix is the same two-step.

**`ADD CONSTRAINT ... UNIQUE`.** Builds an index non-concurrently under ACCESS
EXCLUSIVE. The fix is `CREATE UNIQUE INDEX CONCURRENTLY` then
`ADD CONSTRAINT ... USING INDEX`.

**Backfill in the same transaction as DDL.** An `UPDATE` over a whole table
inside the migration holds its locks for the length of the update.

**`DROP INDEX` without `CONCURRENTLY`, `VACUUM FULL`, `CLUSTER`, `REINDEX`
without `CONCURRENTLY`, `DROP TABLE`, `TRUNCATE`.** Cheap to add, and their
absence is more conspicuous than their presence would be useful, because a
customer who has heard of `strong_migrations` will look for them.

### The one structural gap in the rehearsal

The rehearsal proves what the migration costs. It does not prove the
application survives the window. A rename is flagged by `RuleRenameColumnInUse`
with the right explanation about rolling deploys, but nothing checks that the
*old* code still works against the *new* schema, which is the actual invariant
during a rolling deploy. The environment already runs both the built services
and a real browser, so the missing piece is smaller than it sounds: run the
previous commit's image against the migrated branch and see whether the
workflows still pass. That is a genuinely novel check and no competing product
has it. It is the single highest-leverage thing left in this area.

---

## Part 3. What "production ready" means here, and the honest gap

There are two products in this repository and they are at very different stages.

**The engine**, which customers run in their own CI, is close. Phases 1 through
7 are almost entirely `proven`, including the containment, the masking, the
verification attestation, the agents, and the load shaping. `af ci` runs the
whole check in one command.

**The hosted control plane**, which is the thing you would charge for, is a
reporting surface that cannot yet be sold or operated.

The gap is sharpest in one number. Six declared permissions guard no route at
all:

```
environments.create   network.approve   agents.run
load.run              billing.manage    runtimes.manage
```

I checked this by loading the router and diffing the declared permissions
against the catalog, not by reading. It means the hosted console can observe
environments, runs, verdicts, audit, members, policy and keys, and it cannot
*create an environment*, *approve a network change*, *start a run*, *manage
runtimes*, or *take money*. The permission matrix test proves every route
declares a permission; nothing proves every permission has a route, which is
why this was invisible.

That is the same failure shape as the `syncMembership` defect fixed in #43 and
the same shape the STATUS file describes for the enterprise hooks: a socket
nothing is plugged into is indistinguishable from one that works, until somebody
relies on it.

**Add the inverse assertion to `permissions.test.ts` before anything else.** It
is four lines, it turns this whole class of gap into a failing test, and every
one of the six above becomes a tracked item instead of a discovery.

---

## Part 4. Blocking gaps, in the order they block money

### 1. There is no way to take money

Verified against the live schema: there is no `subscriptions`, `invoices`,
`usage`, `payment_methods`, or `metering` table. `organizations.plan` exists and
`PLAN_QUOTAS` defines `free`, `team`, and `enterprise` with environment, golden
and artifact limits that are enforced by `checkQuota`. Nothing can change the
plan, because `billing.manage` guards no route.

So the quota system works and the plan is permanently whatever it was seeded as.

What is needed, smallest first:

- A `billing.manage` route that reads and sets the plan, so the enforcement that
  already works can be pointed at something.
- Stripe: customer, subscription, checkout session, customer portal, and the
  webhook that moves `organizations.plan`. Note that the engine already contains
  a complete Stripe mock pack that runs checkout, subscribe, renew and cancel
  offline with signed webhooks. **Build the billing integration against your own
  mock pack.** That is a genuine advantage and it is sitting there unused.
- Metering, if pricing is usage based. Nothing counts environment-hours or
  golden refreshes today. Decide this before writing the schema, because
  retrofitting metering is much worse than including it.
- Dunning, proration, tax. Stripe Tax handles the last one; the first two need
  decisions, not code.

### 2. The control plane cannot act, only report

`environments.create`, `agents.run`, `load.run` and `runtimes.manage` are the
product's verbs. Until they exist, the hosted offering is a dashboard over work
the customer triggers from their own CI, and the honest framing of that is
"reporting and policy", not "run your environments here". Either build the verbs
or say the smaller thing on the pricing page. Both are defensible; the current
state is that the console implies the larger one.

`network.approve` is a special case and worth doing first: the masking and
network policy centres exist, a member can propose a change, and nobody can
approve one, so the proposal queue is a dead end. That is a two-hour fix and it
completes a feature that is otherwise finished.

### 3. Nothing tells you the service is down

Verified: there is no `azurerm_monitor_metric_alert` and no
`azurerm_monitor_action_group` anywhere in `infra/`. Diagnostics flow to Log
Analytics and a consumption budget exists in the foundation module. Nothing
pages anybody.

For a paid service, minimum viable alerting:

- Availability probe against `/healthz` from outside Azure, alerting on two
  consecutive failures.
- HTTP 5xx rate over a five minute window.
- Container app revision unhealthy or restart-looping.
- Postgres storage above 80 percent, connections above 80 percent of max, CPU
  sustained.
- Failed migration job execution, which today fails the deploy loudly in CI and
  silently at 3am if anything else runs it.
- Certificate expiry on the custom domain.
- The daily `Security` workflow failing, because a vulnerability scan nobody
  reads is not a control.

An action group with an email and a phone number is enough to start. The gap is
not sophistication, it is existence.

### 4. Backups are configured but the drill does not run

`web/apps/api/src/backup.ts` is one of the better things in this repository. Its
header enumerates the ways a restore appears to succeed and leaves the control
plane broken: roles live in the cluster and not in the dump, so
`antifailure_app` does not exist in a fresh region and every GRANT fails; row
level security can survive as text and not as behaviour, so a restore can
produce a database that answers every query and isolates nothing. It records
every policy, every table with RLS enabled and forced, every grant, and it
finishes with a behavioural check: the restored database is asked, through the
unprivileged role, to read another tenant's rows, and it has to refuse.

That is exactly right, and it runs when somebody runs it. There is no scheduled
execution, so there is no evidence that today's backup restores.

Also: `geo_redundant_backup` defaults to `false` and `high_availability`
defaults to `false`. Both are correct defaults for staging and both are wrong
for production.

- Schedule the drill weekly in CI against a scratch database, and fail loudly.
- Record the restore time. That number is your RTO and right now you do not know
  it.
- Decide and write down an RPO. Azure point-in-time recovery is 14 days by
  default here; the `backup_retention_days` variable allows 7 to 35.

### 5. Sign-in has no path back in

The allowlist is closed (`virsanghavi,maksymrajszewski`), membership follows a
GitHub App installation, and roles now follow GitHub after #43. There is no
break-glass for the hosted product: if the GitHub App is deleted, or the OAuth
App's secret is rotated wrongly, nobody can sign in and nobody can grant
themselves the ability to fix it. The enterprise SSO code has
`sso_break_glass_codes`; the GitHub path has nothing equivalent.

The cheapest fix is a documented, audited, one-off `members.setRole` run through
the migration role, with the command written down somewhere findable. The next
cheapest is to make the first member of an organization its owner.

---

## Part 5. Standing up app.antifailure.dev

You have credits, so this is now a small piece of work rather than a spending
decision. What follows is what the repository already knows, in order.

### What exists

The production deploy path is wired end to end and refuses on purpose.
`cd.yml` has a `production` job pointing at `https://app.antifailure.dev`, gated
on a `v*` tag or an explicit dispatch, behind the `production` GitHub
environment's approval rule, promoting the same image *digest* staging tested.
Its final step prints an error saying production infrastructure does not exist.
That is the correct shape and the only missing part is the infrastructure.

### What to create

The control plane module is already parameterised. There is one tfvars file,
`staging.tfvars`. Production needs a sibling.

- A second resource group and a second stack state. Do not share a resource
  group with staging; a blast radius that includes your customers' control plane
  is not worth the saving.
- Postgres: `high_availability = true`, which the module correctly refuses on a
  burstable SKU with a readable error. That forces `GP_Standard_D2ds_v4`, the
  only General Purpose SKU this subscription's policy permits.
  **`infra/pricing.yaml` has no price for it**, so `tools/cost estimate` will
  report UNKNOWN for the single largest line in the production bill. Add the
  price from the retail API before sizing, and update `checked`.
- `geo_redundant_backup = true` and a considered `backup_retention_days`.
- `min_replicas` at least 2. Staging runs 1 with a written reason (a health gate
  measuring a cold start is meaningless); production needs 2 so a revision
  restart is not an outage.
- Storage: goldens already have versioning and soft delete. Consider an
  immutability policy for the attestations specifically, since those are the
  artifacts that prove masking happened and are the ones a customer's auditor
  will ask about.
- Alerting, per Part 4.3.

### GitHub identity

Production needs its own GitHub App and its own OAuth App, not shared with
staging. Reasons, in order of how much they will hurt:

- The webhook secret and the App private key are the credentials that let a
  delivery write rows. Sharing them means a staging compromise writes into
  production's tenants.
- Installation ids differ per App, and `github_installations` keys on them.
- The OAuth App's callback is a single registered URL today. The form field is
  indexed (`application_callback_urls_attributes][0]`), which suggests several
  are supported, but verify that before relying on it rather than assuming.

While you are in there: **"Allow wildcard matching" is ticked on the staging
OAuth App and nothing needs it.** The registered callback is exact. Untick it on
staging and do not tick it on the production one. This is the item I flagged and
did not change, because it is an account setting.

### The trap that will bite during this

Documented in `infra/terraform/modules/control-plane/app.tf` and in the
self-hosting guide as of #43, and repeated here because it will happen again:
the container app runs in `Multiple` revision mode, Terraform owns the template,
and CD owns traffic. **A Terraform change to the template creates a revision at
zero percent traffic and reports a successful apply.** Production keeps serving
the old one. That is how `AF_GITHUB_APP_ID` sat applied and green while the
running revision logged "no GitHub App" and answered 503 to real webhook
deliveries. After any apply that touches the template, check what is actually
serving.

---

## Part 6. Everything else, by category

### Security

The baseline is genuinely strong: `govulncheck` over all three modules daily
against the real database, every action pinned to a commit, tenant isolation
enforced by Postgres row level security with a test that turns a policy off and
names which table leaked, an audit log that is a hash chain, and a
`SECURITY.md` that was rewritten after seven of its ten claims turned out to be
false.

Its own stated gaps, which I am repeating rather than discovering:

- The release build is reproducible by construction and has never been rebuilt
  and compared.
- **There is no adversarial test suite attempting sandbox escape and proxy
  bypass.** For a product whose central promise is containment, this is the
  most important missing test in the repository. The containment design was
  already wrong once in a way a test caught: disabling IP masquerading looks
  like it removes a container's route out and does not, because Docker Desktop
  translates again at the VM gateway. That is evidence the failure mode is real,
  not theoretical.
- SPDX bill of materials and cosign signing are written and have never run,
  because no release has been cut since they were added.

Before charging money, add: a penetration test of the control plane, a
documented vulnerability disclosure response time, and secret rotation runbooks
for the six Key Vault secrets. `keyring.yml` exists; check what it covers.

### Legal and commercial

`www/app/terms` and `www/app/privacy` exist. For a paid, hosted, multi-tenant
product handling a customer's schema metadata you also need:

- A Data Processing Agreement, with subprocessors listed. Azure at minimum, plus
  Anthropic and OpenAI if model-driven planning is enabled, which is a
  subprocessor most security reviews will ask about by name.
- An SLA, or an explicit statement that there is none. The terms already say the
  product produces evidence rather than certainty, which is the right posture
  and should be carried into the SLA rather than contradicted by it.
- Data retention and deletion commitments, which the partitioned events table
  and the retention work in 14.9 can actually honour.
- SOC 2 readiness, if you intend to sell to anybody with a security review.
  Nothing here blocks it; the audit log, the RLS isolation, and the access
  control model are the hard parts and they exist.

### Documentation integrity

Two concrete defects found while reading:

**41 percent of the error catalog is unbuilt.** 37 of 90 codes carry
`planned: true`. That is honest inside the repository and invisible to a reader
of the published error reference, who cannot tell which codes can occur.

**`STATUS.md` contradicts itself across sections.** There are two Phase 3
tables. The first says `3.3 Masking engine: partial`, `3.4 Verification scanner:
partial`, `3.10 Golden lifecycle: planned`, `3.11 Postgres Insights: planned`.
A later section says the executor, the scan, the attestation and insights are
all `proven`, and the 3.11 row elsewhere in the same file says `proven` with a
long and clearly accurate description. The later text is right. **This cost me
an hour and a wrong conclusion**: I read the stale table, grepped for readers of
`MigrationRehearsal`, found only `normalize.go` and `explain.go`, and concluded
the product's headline feature was dead code. It is not, it is 5,269 lines and
excellent. Reconcile the two tables before somebody makes that mistake in front
of a customer.

### Operations

Missing and needed:

- A status page. Customers of an availability product ask for one first.
- A runbook per alert. An alert with no runbook wakes somebody who then reads
  code at 3am.
- On-call, even if it is one person with a phone.
- Load testing of the control plane itself. The product ships a load generator
  and has never been pointed at its own API.
- A documented upgrade and rollback procedure for the control plane. `deploy.sh`
  does roll back on a failed health gate, which is most of it; write down the
  manual path for the case where the automatic one does not fire.

---

## Part 7. The order I would do this in

Ordered by what unblocks the next thing, not by size.

1. **The inverse permission test.** Four lines. Turns Part 3 into a tracked list.
2. **Reconcile `STATUS.md`.** One sitting. Stops the next person repeating my
   hour.
3. **`network.approve` and `masking.approve` routes.** Completes two features
   that are otherwise finished and currently dead-end.
4. **Alerting.** Before production carries anybody's traffic, not after.
5. **Production infrastructure**, per Part 5. Its own App, its own OAuth App,
   HA, geo-redundant backups, two replicas.
6. **The scheduled backup drill**, so you learn your RTO before you need it.
7. **Billing**, built against the repository's own Stripe mock pack.
8. **`lock_timeout` and the four missing lint rules.** Highest customer-visible
   value per line of code in the whole list.
9. **The adversarial containment suite.** The central promise deserves a test
   that attacks it.
10. **The rolling-deploy check**: previous image against migrated schema. The
    thing nobody else has.

Items 1 through 3 are a day. Items 4 through 6 are the week that makes it
operable. Items 7 onward are the product.

---

## Part 8. Deleting this file

This document is scaffolding and it should not outlive the work.

**Delete it when** production is serving on `app.antifailure.dev` and items 1
through 6 above are done. Not when they are planned, and not when the tickets
exist.

**How to remove it cleanly:**

```sh
git rm docs/plan/prod_guide.md
grep -rn "prod_guide" . --exclude-dir=node_modules --exclude-dir=.git
```

The grep must come back empty. If anything links here, that link becomes a dead
end in a repository whose `tools/claimcheck` gate exists specifically to stop
documents pointing at paths that do not exist, so a stale reference fails CI
rather than rotting quietly. That is the intended behaviour and it is why no
other file should link to this one in the first place.

**What to carry forward before deleting**, so the reasoning is not lost with the
file:

- The missing lint rules in Part 2 belong in `STATUS.md` under 3.11, or as
  issues. They are product work and they outlive this document.
- The alerting list in Part 4.3 belongs in the Terraform module as real
  resources, at which point it is code and needs no prose.
- The production tfvars notes in Part 5 belong as comments in
  `production.tfvars` itself, in the same voice `staging.tfvars` already uses.
  That file already explains why the region is `centralus` and why the SKU is
  what it is, and it is the right home for the rest.
- The `STATUS.md` contradiction in Part 6 should be fixed rather than recorded.

**Then clean the repository around it.** These are the loose threads I saw while
reading, none of which are urgent and all of which are cheaper now than later:

- `docs/dist` is a committed build output. Check whether it needs to be in the
  tree at all.
- `docs-desktop.png` and `hosted-mobile.png` sit at the repository root with no
  obvious owner.
- The `hosted-loop` branch in the primary working directory carries 108 modified
  and untracked files, including engine work, a dogfood workflow, and a
  BuildKit builder, none of which is on `main`. Either land it or record why it
  is parked. It is also the tree whose stale `STATUS.md` produced the wrong
  conclusion described in Part 6, which is a second reason not to leave two
  divergent copies of the plan lying around.
- Four git worktrees are registered, two under `/private/tmp`. Prune the ones
  that are finished.
- `permissions.test.ts` gained a `PRECONDITION_FAILED` allowance in #43. If the
  six unrouted permissions get routes, revisit whether that allowance is still
  the narrowest thing that passes.

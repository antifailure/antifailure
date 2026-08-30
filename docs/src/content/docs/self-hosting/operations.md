---
title: Operations
description: What to look at, what to do, and what not to do, when something is wrong at three in the morning.
sidebar:
  order: 4
---

This page is written for the person who has just been woken up. It assumes you
know nothing about the state of the system and have about ninety seconds of
patience. Everything here has been run; nothing is aspirational.

Setting the rotation up rather than firefighting inside it belongs on the
[on-call page](/docs/self-hosting/on-call/): who holds it, what an
acknowledgement means, and what to do first for each class of page. The
[status page](/docs/self-hosting/status-page/) is what a customer reads while
you read this one; it is not the pager and does not substitute for it.

## The first thirty seconds

Three questions, in this order, because the answer to each changes which of the
rest matter.

**Is the control plane answering?** `curl -sf https://your-control-plane/health`
returns `{"ok":true}`. If it does not, go to [The control plane is
down](#the-control-plane-is-down), and note that environments are still working:
nothing about `af up` needs the control plane, and engines are buffering their
events to disk.

**Is it the database?** `curl -s https://your-control-plane/metrics | grep
af_http_requests_total`. A control plane that is up and failing everything
almost always has a database it cannot reach. The process starts fine without
one, because it does not connect until the first request.

**Is it one organization or all of them?** `af_environment_outcomes_total` broken
down by `code` answers this. One error code across many organizations is a
platform fault. Many codes in one organization is that organization's
repository, and is not your problem tonight.

## What the alerts mean

Every rule in `observability/alerts/antifailure.rules.yml` links back here. They
are the only alerts that exist, on purpose: an alert nobody acts on trains
everybody to ignore the ones that matter.

### ControlPlaneAvailabilityBudgetBurningFast

Five percent of the month's error budget has gone in the last hour, and the last
five minutes agree. The objective is 99.9 percent, which is forty-three minutes
a month, so this is spending it fast enough to matter.

Look at `af_http_requests_total` by `route`. One route failing is a bug in that
handler and can usually wait for morning behind a rollback. Every route failing
at once is the database, the pool, or a deploy.

### EnvironmentCreationFailing

More than one environment in two hundred is failing to come up. Break
`af_environment_outcomes_total` down by `code` first, before anything else. The
code is an `AF-` reference and every one of them has a page under
`https://antifailure.dev/docs/`; the page says what the failure is and what to
do about it, which is faster than guessing from the count.

### TimeToPreviewOverObjective

The slowest one in twenty environments is taking more than eight minutes to be
reachable. Almost always one of two things: a golden that is being copied in
full rather than branched, or a build cache that is not being hit. Neither is an
outage and neither should be treated as one at three in the morning.

### IngestionIsLosingEvents

The one to act on immediately. An engine treats a rejection as delivery and does
not send that event again, so every rejected event is permanently lost and the
environment it described may never advance again in the dashboard. The reason is
on the ingestion response and in the API log for that batch.

### IngestionHasStopped

No engine has reported anything for fifteen minutes. On a small installation
this is usually nobody working, which is fine. The failure it is really watching
for is invisible from here: engines that cannot reach ingestion buffer to disk
and keep going, so a total ingestion outage looks exactly like a quiet night.
If you have any reason to think somebody is working, treat this as an outage.

### RateLimitingIsRefusingRealTraffic

`af_rate_limited_total` by `route`. One route is a limit set too low for honest
traffic. Everything at once is one caller, and the per-organization kill switch
is the tool for that.

## The control plane is down

**Environments are not down.** This is the most important sentence on this page
and the easiest to forget while being paged. `af up`, `af down`, `af test` and
everything else work with no control plane at all. It was built that way
deliberately: a preview environment that stops working because a web application
is down would be a worse product than no dashboard.

What is actually happening while it is down:

- Engines buffer their events in memory and, when that fills or the command
  ends, to a spool directory under `.antifailure/spool` in the repository. The
  spool survives the process. The next command that runs against a control plane
  that has come back sends what the earlier ones could not, oldest first.
- Nothing is lost until the spool exceeds its budget, at which point the oldest
  batches are dropped and the count is reported. You will see that reported
  rather than have to infer it.
- `af env pull` fails, and says so with `AF-CPL-003`, which is the only
  user-visible consequence.

So the recovery order is: bring the control plane back, and do nothing to the
engines. They will catch up on their own.

## A deploy went bad and the automatic rollback did not fire

`deploy/cd/deploy.sh` already rolls back on a failed post-promotion health
gate, in the same run, before the gate exits. This section is for the failure
that shows up after that: the gate passed, the run finished green, and the
problem only became visible later, from a graph or a customer.

Full procedure, including the case where a migration already applied and the
revision you are about to restore may or may not still be compatible with it:
[Upgrade and rollback, the manual path](/docs/self-hosting/azure/#upgrade-and-rollback-the-manual-path).
Do not skip that page's step on the migration; assuming compatibility instead
of checking it is how a rollback becomes a second incident.

## Restoring the control plane database

The commands below have been run. The recovery time this installation should
expect is the one your own drill measured, not the one in any document.

### How much data an incident costs: the recovery point objective

**Five minutes.** That is the recovery point objective for the control plane
database, and it is Azure's number rather than one this project chose. Azure
Database for PostgreSQL flexible server archives the write-ahead log
continuously and documents the delay as up to five minutes, so a point in time
recovery inside the primary region lands within five minutes of the failure.
Five minutes of control plane writes is at most a handful of runs, verdicts and
audit entries. Nothing in that window is a customer's data: raw snapshots,
secrets and captured request bodies never leave the customer's cloud, and an
engine that cannot reach the control plane buffers rather than dropping.

**The recovery window is fourteen days**, which is `backup_retention_days` in
`infra/terraform/modules/control-plane/variables.tf`. Azure allows 7 to 35 and
its own default is 7. Fourteen is deliberate in both directions. Seven is not
enough for the failure that actually needs a long window, which is not a lost
server but a logical corruption nobody noticed: a bad migration on a Friday,
found on the Monday after a week away, is already outside seven days and there
is nothing to recover to. Thirty five is billed as backup storage every day for
a window nobody has ever reached back into. Fourteen covers a fortnight, which
is longer than anyone here has taken to notice a broken write.

**A region loss costs up to an hour, and today it costs everything.**
`geo_redundant_backup` defaults to `false`, so backups live only in the primary
region and a region that is gone takes them with it. Turning it on gives a
geo-restore with an RPO of up to an hour, because the copy to the paired region
is asynchronous, and a geo-restore reaches the last backup that arrived rather
than a second you choose: Azure does not offer point in time recovery from
geo-redundant backups. Both of those are worse than the five minutes above, and
both are enormously better than nothing.

That default is correct for staging and wrong for production, and it is the
expensive kind of wrong: backup redundancy can only be set when the server is
created, so switching it on later means creating a new server and moving to it.
Decide before the apply, not after.

**None of this is the dump.** `af-control-plane-backup` is a second line with a
different failure mode: it produces a file you hold, readable by any Postgres,
which is what covers the case where the Azure subscription itself is the
problem. Its recovery point is however long ago somebody last ran it, so it is
worth a schedule of its own if you rely on it.

### Take a backup

```
af-control-plane-backup backup \
  --url postgres://owner@host/antifailure \
  --out /var/backups/antifailure
```

Three files come out: the dump, a roles file, and a manifest. All three matter.

The **roles file** matters most and is the least obvious. `pg_dump` works on one
database; roles live in the cluster. Restore a dump into a fresh cluster in
another region and `antifailure_app` does not exist there, so every `GRANT` in
the dump fails, `pg_restore` exits zero, and the application cannot connect to
the database you just recovered. The roles file is what prevents that, and it is
why a backup taken any other way is not enough.

The **manifest** records what a restore has to reproduce: row counts per table,
every policy, every table with row level security enabled and separately
`FORCE`d, every privilege the application role holds, and the audit chain head.

It records its own scope as well, which matters more than it sounds. All of
those checks read the `public` schema, where every one of the control plane's
tables lives. Anything outside it is listed in the manifest as unverified and
reported by the restore and the drill as a table the check cannot speak for.
That is not a restore failure and should not be read as one. It means the
database grew somewhere this verification does not look, and somebody has to
decide whether that table matters before the next real recovery.

### Restore it

```
af-control-plane-backup restore \
  --url postgres://owner@newhost/postgres \
  --database antifailure \
  --dump /var/backups/antifailure/backup.dump \
  --roles /var/backups/antifailure/backup.roles.sql \
  --manifest /var/backups/antifailure/backup.manifest.json \
  --app-password "$APP_PASSWORD"
```

It refuses a database that already exists. That refusal is deliberate: restoring
over a live database is not a recovery, it is an outage with a different cause.
Restore into a new name and switch the application over.

It exits 3, and says which check failed, if the restored database does not match
the manifest or does not isolate tenants. **Do not point the control plane at a
database that exited 3.**
`pg_restore` exits zero over a `GRANT` that failed because the role was missing,
and over policies restored onto a table whose row level security it could not
enable. Both produce a control plane that starts, answers every request, and
isolates nothing at all. Nothing about it looks wrong from the outside.

### Rehearse it, on a schedule, before you need it

```
af-control-plane-backup drill \
  --url postgres://owner@host/antifailure \
  --out /var/backups/antifailure \
  --database af_drill \
  --app-password "$APP_PASSWORD" \
  --report /var/backups/antifailure/drill.json
```

The drill backs up, restores into a throwaway database, checks it against the
manifest, asks it through the unprivileged role to read another tenant's rows,
drops it, and prints the recovery time it measured. Run it quarterly at least.
A backup nobody has restored is a file.

`--app-password` is not optional in practice. Without it nothing can connect as
`antifailure_app`, so every check becomes a comparison of catalogue text against
catalogue text, and all of that passes over a database that answers every query
and isolates nothing. The drill treats a cross-tenant read it could not attempt
as a failure and says so. The `restore` command says so and leaves the decision
to you, because somebody recovering at three in the morning may not have the
password to hand.

It exits 3 when the restored database does not match or does not isolate, and 4
when the restore was sound and slower than a `--max-restore-seconds` budget you
gave it. Two codes rather than one, because a backup that is not one and a
runner having a slow morning are not the same finding and must not read as the
same finding.

This repository runs the drill against a scratch database every Monday at 04:00
UTC, in `.github/workflows/drill.yml`, which invokes the `drill` recipe in the
`justfile` so that what runs unattended and what you can run by hand are the
same command. Run `just drill` to run exactly that yourself: it starts a
Postgres of its own, applies every migration, seeds two organizations so the
cross-tenant read has another tenant to be refused, and holds the recovery time
against a budget of 300 seconds. That budget is a backstop against a restore
that has stopped working, not the objective below and not a performance target.
What detects a regression is the series: the workflow publishes each
measurement to the run summary and keeps it for ninety days.

The number it prints is the **restore** time, not the whole run, because
recovery starts from a backup that already exists. Counting the time to take one
flatters the number by measuring work that has already happened when it matters.

**Use your own number, not this one.** For scale only, all of it measured rather
than estimated: on a continuous integration runner with nothing else on it, a
control plane database holding a handful of organizations restored in under two
seconds, and two consecutive runs of the same drill on the same runner reported
1.8 seconds and 0.6. On a development machine running a dozen other containers,
the same restore took between 20 and 160 seconds. A factor of three between two
runs an hour apart, and a factor of a hundred between two machines, is the
point. A recovery time is a property of the
hardware, the size of the database and what else is happening, so the only
figure worth putting in an incident plan is the one your own drill measured on
the machine you would actually recover onto. The suite prints its measurement on
every run, so the number in front of you is never older than the last time
anybody checked.

The objective to hold it against is two hours, which is the recovery time this
system is designed for. A drill that comes in well under it is not a reason to
stop running the drill: what the drill really tests is whether the backup is
one, and the timing is the part you get for free.

## What not to do

**Do not restore over the live database.** The tool refuses; do not work around
the refusal. Restore beside it and switch.

**Do not run `af down --all` to clean up during an incident.** It removes every
environment on the machine, including ones somebody is using to debug the
incident.

**Do not delete a golden to reclaim space while environments are running.**
`af golden gc` already refuses to collect a version an environment came from, and
reports `AF-DB-005` when asked to. That refusal is the feature.

**Do not disable a row level security policy to unblock a query.** It is the only
thing separating one customer's data from another's, and the drill above will
tell you it is missing long after somebody has read something they should not
have.

## Collecting evidence before you change anything

```
af support bundle
```

Logs, decisions, manifest, and doctor output, redacted, with a listing of
exactly what it included. Take one before you start changing things, because the
state that explains the incident is usually the first thing a fix destroys.

For one environment specifically:

```
af status
af logs web
af doctor
```

`af doctor` runs ten checks and each one carries a remediation. It is the fastest
way to find out that the thing you are debugging is a Docker daemon that is not
running.

## Load testing the control plane itself

`af load` shapes traffic against an environment `af up` built; nothing before
this pointed it at the control plane's own API, which is the one service in
this product that has never had its own load generator run against it.

`engine/cmd/loadcp` does, using the same `engine/internal/load` package `af
load` does, against a URL instead of an af-managed environment:

```sh
go run ./cmd/loadcp -url https://app.dev.antifailure.dev -duration 1m -scale 1
```

The bundled profile is not measured production traffic; none has been
captured yet, and there is nowhere in this product's own load package to point
at the control plane's access log until there is one. Each route's weight is
instead its own declared ceiling from `web/apps/api/src/limits.ts`, the number
the rate limiter already enforces per caller. The profile says so: its
`source` field reads `declared_limits`, not `production`, the same honesty
`internal/load` itself applies to a shape nobody supplied.

**What a real run found.** Built and run once against a real local instance,
schema migrated, serving from an actual Postgres, not a fake: at half the
combined declared rate (92 requests a second, one caller, `-scale 0.5`), p95
latency climbed from 0.5 seconds to 3.2 seconds over a 31 second run, achieving
37 requests a second against a target of 92, with `/readyz` carrying the worst
tail at up to 4.9 seconds. No request was rejected by the rate limiter at any point in
this run; the connection pool queued first. That run was on a laptop reporting
a load average over 75 from other work sharing the same machine at the time,
which is exactly the caveat this project's own [disaster recovery
timings](#rehearse-it-on-a-schedule-before-you-need-it) already carry: a
latency number is a property of the hardware and what else is running on it,
not a portable fact about the code. What is portable is the finding underneath
it, which is worth checking again on quiet, dedicated hardware before it
informs a real capacity decision: on this run, the database connection pool
(`AF_POOL_MAX`, ten by default) became the limiting factor before the
per-caller rate limits did, for a single caller sending across every route at
once. An operator sizing a real deployment should raise `AF_POOL_MAX` to match
expected concurrent callers rather than assuming the rate limiter is the only
ceiling in the system.

## Where the numbers come from

`GET /metrics` on the control plane, in the Prometheus text format. It reads no
tables: tenancy here is row level security, so an aggregate across every
organization would need a role that can read every organization's rows, and
creating one in order to draw a graph would put the strongest read in the system
on the least important path and leave it there being scraped every fifteen
seconds forever. Everything exposed is a counter the process kept itself, and
several replicas each expose their own for Prometheus to sum.

The dashboard is `observability/dashboards/control-plane.json`, importable as it
is. Its panels and the alert rules are both checked against the exporter by a
test, because an alert on a metric that does not exist fires never and a panel on
one draws an empty graph, and an empty graph reads as a quiet system rather than
as a broken dashboard.

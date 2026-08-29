---
title: Operations
description: What to look at, what to do, and what not to do, when something is wrong at three in the morning.
sidebar:
  order: 4
---

This page is written for the person who has just been woken up. It assumes you
know nothing about the state of the system and have about ninety seconds of
patience. Everything here has been run; nothing is aspirational.

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

## Restoring the control plane database

The commands below have been run. The recovery time this installation should
expect is the one your own drill measured, not the one in any document.

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
the manifest. **Do not point the control plane at a database that exited 3.**
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
  --app-password "$APP_PASSWORD"
```

The drill backs up, restores into a throwaway database, checks it against the
manifest, drops it, and prints the recovery time it measured. Run it quarterly
at least. A backup nobody has restored is a file.

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

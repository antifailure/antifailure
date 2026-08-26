---
title: Control plane configuration
description: Every environment variable the control plane reads, what it does, and what happens when it is missing.
sidebar:
  order: 7
---

The control plane reads its configuration from the environment and refuses to
start without what it needs, naming the variable that is missing. A process
that starts with a missing secret and fails on the first request that needs it
is a process that fails in production rather than at deploy time.

## Required

| Variable | What it is |
| --- | --- |
| `AF_DATABASE_URL` | The connection string the application uses. This is the unprivileged role, not the owner: it cannot run DDL, because a role that can `ALTER TABLE` can drop the policies that isolate tenants. |
| `AF_GITHUB_CLIENT_ID` | The OAuth App's client identifier. |
| `AF_GITHUB_CLIENT_SECRET` | The OAuth App's client secret. |
| `AF_GITHUB_REDIRECT_URI` | Where GitHub returns the browser after sign in. Must match what the App is configured with exactly. |

## Optional

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_PORT` | `8080` | The port to listen on. |
| `AF_POOL_MAX` | `10` | Connections in the application pool. |
| `AF_APP_BASE_URL` | unset | The public origin, used to build absolute links. |
| `AF_INSECURE_COOKIES` | unset | Set to `1` to drop the `Secure` attribute from cookies. For local development over plain HTTP and nothing else. |
| `AF_MIGRATE` | unset | Set to `1` to apply migrations at startup. Requires `AF_MIGRATION_DATABASE_URL`. |
| `AF_MIGRATION_DATABASE_URL` | unset | A connection string for a role that may run DDL. |

## Schema maintenance

The `events` table is partitioned by month. Partitions are created ahead of the
writes, because a range-partitioned table with no partition for an incoming row
does not slow down, it fails.

Keeping ahead is DDL, so it runs as the migration role and not as the
application role. The connection is opened for each pass and closed after it,
rather than held idle between them.

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_MAINTENANCE_DATABASE_URL` | falls back to `AF_MIGRATION_DATABASE_URL` | The role that creates and drops partitions. When neither is set, this process logs a warning at startup and does not keep the partitions ahead. Something else must. |
| `AF_EVENT_RETENTION_MONTHS` | unset | Drop event partitions entirely older than this many whole months. Unset keeps everything forever, which is the default because retention is an operator's decision. A value that is not a whole number of months at least 1 stops the process at startup rather than silently keeping everything. |
| `AF_EVENT_ARCHIVE_DIR` | unset | Write a month out as newline delimited JSON here before dropping it. |

### What a pass does, in order

1. **Creates** the current month and the three after it. This happens
   unconditionally and first. Nothing below is allowed to prevent it.
2. **Archives** each month that retention has condemned, if
   `AF_EVENT_ARCHIVE_DIR` is set. The file is written under a temporary name and
   renamed when it is complete, so a file appearing in the directory always
   means a whole one.
3. **Drops** those months, but only if every archive finished. A failed write
   costs a retention run rather than the events, because a month deleted with no
   copy anywhere cannot be undone.
4. **Prunes** the default partition by age, a bounded number of rows per pass.

A pass runs at startup and then once a day. A pass that throws is logged and
the schedule continues: the failure that matters is running out of partitions,
and giving up after one transient error is how that happens quietly.

### If the job has not run for a while

Nothing needs to be done by hand. Events whose month does not exist land in the
default partition rather than failing, and the next pass moves them into the
month it creates for them. It detaches the default partition, creates the
month, moves the rows through the parent so that Postgres decides where each
one goes, and reattaches, all in one transaction.

### Why the partition key is `occurred_at`

Ingestion depends on a unique constraint to make retries safe:

```sql
INSERT INTO events (...) VALUES (...)
ON CONFLICT (org_id, idempotency_key, occurred_at) DO NOTHING
```

An engine that sent a batch and lost the response cannot know which half
landed, so it sends the batch again and the database drops the copy.

Postgres will not enforce a unique constraint that omits the partition key, so
the partition column is necessarily part of that key. `received_at` is assigned
here, by the clock, and would differ between an attempt and its retry: the
conflict would never fire and every retry would duplicate. `occurred_at` is
assigned by the sender when the event happened and is resent unchanged, so it
does not vary between attempts and costs nothing by being in the key.

The usual objection to partitioning on a value a client supplies is a skewed
clock inventing partitions forever. Ingestion already rejects `occurredAt` more
than a day in the future or more than a year in the past, so the live range is
bounded before a row reaches the table.

The cost, stated plainly: the idempotency key is now
`(org_id, idempotency_key, occurred_at)` rather than
`(org_id, idempotency_key)`. A sender that reuses an identifier under a new
timestamp gets two rows where it used to get one. No sender does that by
accident, since the identifier and the timestamp are minted together and resent
together, but it is a real difference and not a free one.

## Reading an archive

Each line is one event, as JSON, with timestamps as RFC 3339 text rather than
in a driver's own format, because the file is read by something that is not
this process.

```sh
# how many events, and over what span
wc -l events_2026_03.jsonl
head -1 events_2026_03.jsonl | jq -r .occurred_at

# everything one environment did
jq -c 'select(.env_id == "env-1234")' events_2026_03.jsonl
```

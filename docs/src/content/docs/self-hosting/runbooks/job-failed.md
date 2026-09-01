---
title: A job failed
description: The bootstrap job or the maintenance job reported a failed execution.
sidebar:
  order: 16
---

**Alerts:** `bootstrap-job-failed` and `maintenance-job-failed`. **Severity 1.**

One alert per job, so the page says which one. There is no window to wait out:
these jobs run once and either work or do not.

## Why this alert exists at all

A failed migration already fails the deploy, loudly, because continuous
deployment starts the bootstrap job and waits for it. Nothing else does. An
operator running it by hand after an image upgrade, or the maintenance job at
03:17, fails into silence.

## Read the failure first

```sh
az containerapp job execution list -n afcpprod-bootstrap -g af-cp-prod-centralus \
  --query "[0:5].{name:name,status:properties.status,start:properties.startTime}" -o table
az containerapp job logs show -n afcpprod-bootstrap -g af-cp-prod-centralus \
  --container bootstrap --tail 200
```

## The bootstrap job

It applies the schema and grants the application role its membership in
`antifailure_app`. Without it a fresh install migrates cleanly, starts, answers
`/health` with 200, and cannot read a single table, because a role with no
`USAGE` on the schema is told the relation does not exist rather than that it
lacks permission.

It is idempotent. Running it again after fixing the cause is the normal repair:

```sh
az containerapp job start -n afcpprod-bootstrap -g af-cp-prod-centralus
```

**`CREATE EXTENSION` refused** is the failure this stack met first. Azure
refuses any extension absent from the `azure.extensions` server parameter, and
that parameter defaults to empty. Migration 0001 opens with `CREATE EXTENSION IF
NOT EXISTS pgcrypto`, so the first statement of the first migration was refused
and the whole file rolled back. The allow list is `database_extensions` in the
stack's variables.

**`gave up waiting for a lock`** is a deploy that was blocked rather than
broken, and it is the one failure here that is usually safe to simply run again.
The migration asked for a lock on a table the running revision writes to, waited
three seconds, and gave up. Nothing applied: a migration file is one
transaction, so it rolled back whole and was not recorded.

That failure is deliberate and the alternative is worse. A lock request that
cannot be granted queues, and every later request queues behind the request
rather than behind the table, so a migration that waits is a migration that
stops every sign-in for as long as the transaction in its way lives. The server
bounds none of that on its own: `lock_timeout`, `statement_timeout` and
`idle_in_transaction_session_timeout` are all zero on a flexible server.

Start the job again. If it fails the same way twice, find the holder before a
third attempt:

```sql
SELECT pid, state, wait_event_type, xact_start, left(query, 120)
FROM pg_stat_activity
WHERE state <> 'idle' OR state = 'idle in transaction'
ORDER BY xact_start;
```

An `idle in transaction` backend older than the deploy is the usual answer, and
it is a client that opened a transaction and never finished it rather than
anything the migration did.

**`canceling statement due to statement timeout`** is the opposite case: nothing
was blocking the migration, the migration was blocking everybody else. One of
its statements ran past five minutes while holding its locks. Do not raise the
timeout to get the deploy through. Read which statement it was, because a
migration statement that takes five minutes on this data will take longer on
more of it, and the answer is usually an index or a batched backfill rather than
a larger budget.

**A migration that failed part way** leaves the schema between two versions. Do
not write a corrective migration under pressure. Read what applied, decide
whether to roll forward, and remember that point in time recovery reaches back
35 days at five minute granularity.

## The maintenance job

It creates the next months of `events` partitions and drops the ones past
`event_retention_months`, which production sets to 24. It runs at 03:17 daily.

**This is the alert that becomes an outage if it is ignored.** A range
partitioned table with no partition for an incoming row does not slow down, it
refuses the insert. So a maintenance job that has been failing quietly for
weeks presents as ingestion failing on the first day of a month.

The window is generous, which is why severity 1 rather than 0 is right: the job
creates partitions ahead, so several consecutive failures are survivable and one
is not urgent. Do not let that turn into leaving it.

## What not to do

**Do not run the migration role from a laptop to fix it.** The server has no
public endpoint, deliberately. The job runs inside the VNet, which is why it is
a job and not a `postgresql` provider block, and reaching the database from
outside means opening something that should stay shut.

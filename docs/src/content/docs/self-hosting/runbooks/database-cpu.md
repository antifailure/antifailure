---
title: Database CPU
description: The server has averaged more than 80 percent CPU for half an hour.
sidebar:
  order: 15
---

**Alert:** `database-cpu`. **Severity 3.** This is a morning problem.

The window is thirty minutes and not five, on purpose. Postgres pegs a core for
half a minute during a vacuum or a large query and recovers, and a five minute
window turns every one of those into a page. What this rule watches for is CPU
that does not come back down.

## What to look at

```sql
SELECT query, calls, mean_exec_time, total_exec_time
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20;
```

`pg_stat_statements` needs to be in the `azure.extensions` allow list, which is
`database_extensions` in the stack's variables and currently holds `PGCRYPTO`
only. Adding it is a dynamic server parameter change and needs no restart,
which makes it a reasonable thing to add while investigating and a better thing
to have added already.

Without it, the live view is still available:

```sql
SELECT pid, state, wait_event_type, wait_event, now() - query_start AS age, query
FROM pg_stat_activity
WHERE state <> 'idle'
ORDER BY age DESC;
```

Two causes account for almost all of it on this schema. A sequential scan on
`events`, which is large and partitioned, usually means a query that did not
constrain the partition key. Autovacuum working through a table that has
accumulated dead tuples is the other, and that one is doing necessary work and
should be left alone.

## Before making the server bigger

`GP_Standard_D2ds_v4` is the only General Purpose SKU this subscription's
`bonfire-sku-allowlist` permits, so there is no larger size available without a
policy exemption. That is worth knowing before spending an hour planning one.

Scaling compute on a flexible server is a restart, and with high availability
it is a failover. Neither is free and neither fixes a missing index.

## What not to do

**Do not kill a long running autovacuum.** It will start again, having made no
progress, and the table it was working on is now further behind.

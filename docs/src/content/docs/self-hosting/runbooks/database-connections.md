---
title: Database connections
description: Active connections passed 80 percent of what the SKU allows.
sidebar:
  order: 15
---

**Alert:** `database-connections`. **Severity 2.**

Production runs `GP_Standard_D2ds_v4`, whose `max_connections` is 859, so this
fires above 687.

## The number is computed, and it can be wrong

Azure exposes no "percent of connections used" metric, and a metric alert reads
one series so it cannot divide `active_connections` by `max_connections`. The
threshold is therefore computed in
`infra/terraform/modules/alerting/database.tf` from a table of SKU to
connection limit.

The burstable entry in that table was read from the running staging server and
matches the documented series exactly. The General Purpose entry comes from the
same series and has not been read from a live server. **Confirm it once, after
the first production apply**, because if it is wrong this alert is quietly
measuring the wrong fraction and nothing else will ever say so:

```sh
az postgres flexible-server parameter show \
  -g af-cp-prod-centralus -s afcpprod-pg -n max_connections \
  --query "{value:value,default:defaultValue}" -o json
```

## Where the connections come from

The arithmetic is small enough to do in your head, which is what makes an
unexpected number informative. Each replica opens at most `pool_max`, which is
10. Two replicas is 20. The bootstrap and maintenance jobs each open one while
they run. Anything beyond about 25 is not this application.

```sql
SELECT usename, application_name, state, count(*)
FROM pg_stat_activity
GROUP BY 1, 2, 3
ORDER BY 4 DESC;
```

**`af_app` with hundreds of connections** means replicas scaled out under load.
Check `max_replicas`, which is 6, and remember that six replicas at a pool of
ten is 60 connections and still nowhere near the ceiling.

**`af_migrator` with more than one** is a person or a script holding a
privileged session open. That role can drop the policies that isolate tenants,
so an unexplained one is a security question and not a capacity one.

**Anything in `idle in transaction`** is the real problem. Those hold locks and
hold back autovacuum, and they are why a connection count climbs without traffic
climbing.

## What not to do

**Do not raise `max_connections`.** Each connection is a process with its own
memory, and a server that is out of connections is usually about to be out of
memory. The fix is on the client side: fewer replicas, a smaller pool, or a
transaction that stops being held open.

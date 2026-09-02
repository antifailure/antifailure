---
title: Database storage
description: The flexible server is above 80 percent of its provisioned disk.
sidebar:
  order: 14
---

**Alert:** `database-storage`. **Severity 2.** Hours, not minutes.

Production provisions 64 GB, so this fires at roughly 52 GB used. It is a
warning and not an outage, but it becomes an outage: a flexible server that
fills its disk stops accepting writes and Postgres refuses transactions.

## Find out what is using it before adding any

```sql
SELECT relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS total
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
ORDER BY pg_total_relation_size(c.oid) DESC
LIMIT 20;
```

There are only three plausible answers on this schema.

**The `events` table.** It is partitioned by month and production keeps 24
months. The maintenance job drops partitions past that window, so this table
growing past its retention means the maintenance job has not been running. Check
[a job failed](/docs/self-hosting/runbooks/job-failed).

**Write ahead log.** `txlogs_storage_used` is a separate metric. A replication
slot that nobody is reading holds the log forever, and that is the failure that
fills a disk in a day rather than a year.

**Dead tuples.** Autovacuum not keeping up shows as `n_dead_tup_user_tables`
climbing. It is a symptom of a long running transaction holding back the
horizon, not of a full disk.

```sh
az monitor metrics list -g af-cp-prod-centralus --resource afcpprod-pg \
  --resource-type Microsoft.DBforPostgreSQL/flexibleServers \
  --metric storage_percent txlogs_storage_used --interval PT1H -o table
```

## Growing the disk

Storage can be grown and can **never** be shrunk. Growing it also raises the
IOPS ceiling, which is why production starts at 64 GB rather than the 32 GB
floor staging uses.

Change `database_storage_mb` in
`infra/terraform/stacks/control-plane/production.tfvars` and apply. Doing it in
the portal instead means the next plan proposes to put it back.

Remember that high availability bills the standby's disk too, so doubling the
storage adds twice the storage price to the monthly bill. Run the estimate
before applying:

```sh
go run ./tools/cost estimate --plan plan.json --pricing infra/pricing.yaml
```

## What not to do

**Do not delete rows to reclaim space in an emergency.** A `DELETE` grows the
table before it shrinks it, and on a full disk it will simply fail. Dropping an
old partition is instant and reclaims the file; deleting from a live one does
neither.

**Do not disable the maintenance job to stop it writing.** It is the thing
creating next month's partition, and a range partitioned table with no partition
for an incoming row refuses the insert rather than slowing down.

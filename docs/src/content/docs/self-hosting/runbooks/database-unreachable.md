---
title: The database is not answering
description: Azure reports the flexible server as not alive. This is the one unambiguous database signal.
sidebar:
  order: 13
---

**Alert:** `database-unreachable`. **Severity 0.**

Azure's own `is_db_alive` metric went to zero. Every other database alert on
this stack is a number crossing a line somebody chose. This one is the platform
saying the server did not answer.

It is here even though the production assessment did not ask for it, because
without it the first news of a dead database arrives as a wave of 5xx, and
whoever reads that page starts by looking at the application.

## What it means in production, which has high availability

Production runs zone redundant high availability: a second server, in a second
availability zone, kept in synchronous replication. A zone failure is a failover
that takes tens of seconds and needs nobody. So this alert firing and then
clearing on its own within a few minutes is the standby doing exactly what it is
paid for, and the thing to do afterwards is read the failover in the portal
rather than act.

This alert **staying** on is different. Check the server before the application:

```sh
az postgres flexible-server show -g af-cp-prod-centralus -n afcpprod-pg \
  --query "{state:state,ha:highAvailability,zone:availabilityZone}" -o json
```

## What high availability does not protect against

The standby has the same rows. A bad migration, a `DROP TABLE` or a corrupting
defect reaches it instantly. The thing that protects against those is point in
time recovery, which production holds for 35 days at a five minute recovery
point objective, and the restore procedure is on the
[operations page](/docs/self-hosting/operations/).

**Read that page before restoring anything.** A restore that appears to succeed
can leave a control plane that answers every query and isolates nothing,
because roles live in the cluster rather than in the dump and row level security
can survive as text without surviving as behaviour. `af-control-plane-backup
restore` exits 3 when the restored database does not match its manifest, and a
database that exited 3 must not be served from.

## What not to do

**Do not fail over by hand while the alert is firing.** Azure is already doing
it, and a manual failover on top of an automatic one is two.

**Do not restore over the live database.** The tool refuses; do not work around
the refusal. Restore beside it and switch.

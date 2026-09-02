---
title: Database connections
description: Active connections peaked above 80 percent of what the server will hand the application.
sidebar:
  order: 15
---

**Alert:** `database-connections`. **Severity 2.**

Production runs `GP_Standard_D2ds_v4`, whose `max_connections` is 859. Postgres
holds 15 of those back, so the application may open 844 and this fires above
675.

## The denominator is not `max_connections`

Postgres refuses an ordinary role once the free slots fall to
`reserved_connections` plus `superuser_reserved_connections`, which are 5 and 10
on every SKU this project allows. The application is deliberately not a member
of `pg_use_reserved_connections`, so what it actually gets is
`max_connections - 15`.

This matters most on the small SKU, where the gap decides whether the alert can
fire at all. A `B_Standard_B1ms` reports `max_connections = 50` and hands the
application 35. A threshold set at 80 percent of 50 is 40, and 40 is above 35:
the rule would have sat green while the server was already answering

```
remaining connection slots are reserved for roles with privileges of the
"pg_use_reserved_connections" role
```

That is what staging did, and it is why the threshold is computed from
`usable_connections` in `infra/terraform/modules/control-plane/database.tf`
rather than from `max_connections`. The same value bounds the application's own
pool at plan time, so the alert and the ceiling cannot drift apart.

Confirm the SKU's number against the running server if you add one to the
table. If it is wrong, this alert is quietly measuring the wrong fraction and
nothing else will ever say so:

```sh
az postgres flexible-server parameter show \
  -g af-cp-prod-centralus -s afcpprod-pg -n max_connections \
  --query "{value:value,default:defaultValue}" -o json
```

## It reads the peak, not the average

The criterion is `Maximum` over fifteen minutes. Connection exhaustion here is a
burst: every replica runs the same five minute housekeeping sweep, so they all
reach for the pool at once and let go again. Staging's own numbers while it was
refusing connections were 6 to 11 for four minutes out of every five and 33 to
39 in the fifth. An `Average` reads that as 12 and stays green through every one
of the spikes that took the service down.

## Where the connections come from

```sql
SELECT usename, state, host(client_addr), count(*)
FROM pg_stat_activity
WHERE client_addr IS NOT NULL
GROUP BY 1, 2, 3
ORDER BY 4 DESC;
```

**Count the distinct client addresses first.** One address per replica, so this
is the number of control plane processes talking to the database, and it is the
number that is wrong most often. If it is larger than the replicas the app is
supposed to be running, the connections are coming from revisions nobody is
serving traffic from:

```sh
az containerapp revision list -n "$APP" -g "$RG" \
  --query "[?properties.active].{name:name,replicas:properties.replicas,traffic:properties.trafficWeight}" -o table
```

A revision at zero traffic is not idle. In Multiple revision mode it keeps
`min_replicas` running, and each of those is a whole control plane process
holding a pool and sweeping the database every five minutes. Forty six deploys
to staging left forty six of them. `deploy/cd/deploy.sh` now deactivates
superseded revisions after each release and fails the run if what remains does
not fit, but a revision reactivated by hand for a rollback and left there will
do the same thing again.

**`af_app` with hundreds of connections** and the expected number of addresses
means replicas scaled out under load. Six replicas at a pool of ten is 60, which
is nowhere near production's ceiling.

**`af_migrator` with more than one** is a person or a script holding a
privileged session open. That role can drop the policies that isolate tenants,
so an unexplained one is a security question and not a capacity one.

**Anything in `idle in transaction`** holds locks and holds back autovacuum, and
it is why a connection count climbs without traffic climbing.

## What not to do

**Do not raise `max_connections`.** Each connection is a process with its own
memory, and a server that is out of connections is usually about to be out of
memory. The fix is on the client side: fewer active revisions, fewer replicas, a
smaller `pool_max`, or a transaction that stops being held open.

**Do not deactivate the revision that is serving traffic.** Read the
`trafficWeight` column above before deactivating anything.

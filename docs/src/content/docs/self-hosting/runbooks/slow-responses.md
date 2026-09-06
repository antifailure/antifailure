---
title: Slow responses
description: The application answered everything, correctly, and too slowly for fifteen minutes.
sidebar:
  order: 12
---

**Alert:** `slow-responses`. **Severity 1.** Nothing is failing and customers
can see it anyway.

The average response time across every request the ingress handled was above
the threshold, 2000 ms in production, for fifteen minutes. The series is the
Container Apps `ResponseTime` metric, in milliseconds, averaged over every
status code.

## Why this rule exists beside the others

Every other rule on the application watches a failure: a `5xx`, a restart, a
replica that is not there. A saturated replica set, a blocked connection pool
or one slow query on the hot path produces none of those. Every request
completes, every status is 200, and each one takes twelve seconds. The
availability test has a thirty second timeout and stays green through all of
it. On a busy day that is the likeliest degradation and the one a customer
notices first, and before this rule nothing paged for it.

## Read this before tuning the threshold

Measured on production over the two days before the rule was written, with
traffic in every one of 576 five minute buckets: successful requests averaged
216 ms, the busiest hour 334 ms, the worst five minute average 587 ms, and the
slowest single request in any hour 6043 ms. The threshold is more than three
times the worst average the service has produced and about ten times an
ordinary one.

It is an average, not a maximum, on purpose. The statement timeout is fifteen
seconds, so one request that waits on a lock can legitimately take that long,
and a rule on the maximum would fire every time that happened. The average is
what the whole population of customers experienced. It is a fifteen minute
window because Azure allows a static threshold no way to wait for two
consecutive violations and no ten minute window, and fifteen at the same five
minute cadence as the `5xx` rule is the nearest thing to a second look.

## What to look at

**`/readyz` first**, and time it. It takes a connection out of the pool the
application serves with, so a slow answer there is a slow database or an
exhausted pool, and a 503 there names the reason.

```sh
curl -s -o /dev/null -w '%{http_code} %{time_total}s\n' https://app.antifailure.dev/readyz
```

**The application's own histogram**, which has the breakdown by route that
Azure does not have. One route slow is a query; every route slow is the pool,
the database or the replica count.

```sh
curl -s https://app.antifailure.dev/metrics | grep af_http_request_seconds
```

**The same series Azure alerted on, split by status.** A stall that ends in
timeouts shows up as a slow `5xx` category before the `5xx` count crosses its
own threshold, and a slow `2xx` category with nothing else is the application
working hard.

```sh
az monitor metrics list -g af-cp-prod-centralus \
  --resource afcpprod-app --resource-type Microsoft.App/containerApps \
  --metric ResponseTime --aggregation Average \
  --filter "statusCodeCategory eq '*'" --interval PT5M -o table
```

**The replicas.** `CpuPercentage` and `MemoryPercentage` on the app, and
`Replicas` against `max_replicas`. A replica set pinned at its maximum with CPU
above eighty percent is a scaling problem and the fix is
`infra/terraform/stacks/control-plane/production.tfvars`, not a restart.

**The database.** `cpu_percent` and `active_connections` on the flexible
server, and the [database connections](/docs/self-hosting/runbooks/database-connections)
runbook if the second is near its ceiling. A long running query holds a
connection and a lock, and `pg_stat_activity` names it.

## What not to do

**Do not restart the app first.** A restart drops every in-flight request,
destroys the state that explains the slowness and fixes nothing that is not a
leak. Read `/readyz` and the histogram before touching anything.

**Do not raise the threshold to silence it.** If two seconds is ordinary for
this service, that is a fact to record in
`infra/terraform/stacks/control-plane/production.tfvars` with the measurement
that made it true, beside the one that is there now.

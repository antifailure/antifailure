---
title: When one machine is not enough
description: What a control plane adds, why nothing depends on it, and the shortest path to running one.
sidebar:
  order: 3
---

The first two pages need no server. `af up` builds an environment on the
machine it runs on, [`af ci`](/docs/getting-started/pull-requests/) does the
same inside a workflow, and nothing calls home.

A control plane is what a team adds when one person's laptop stops being the
right place for the answer: environments that outlive a CI job, a reviewer who
can open one, scheduling across a queue, quotas, and history.

## Nothing breaks without it

```
AF-CP-001 The control plane at https://cp.example.com could not be reached.
  Next: Antifailure works without it. Unset control_plane.url to run fully
  locally.
```

That is the design rather than a consolation. Events are buffered and delivered
when it returns, environments keep running, and teardown still works, because
teardown reads the local journal and not the control plane. Adding one is a
decision you can reverse by deleting a line.

## Two steps, in that order

The first prepares the database. The second serves requests. They are separate
because they need different credentials, and the serving step deliberately has
no migration credential at all.

```sh
# 1. Apply the schema, create the application role, grant it its membership.
docker run --rm \
  -e AF_MIGRATION_DATABASE_URL=postgres://owner:...@db:5432/antifailure \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  ghcr.io/antifailure/control-plane:v0.1.1 node bootstrap.mjs

# 2. Serve.
docker run \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  -e AF_GITHUB_CLIENT_ID=... \
  -e AF_GITHUB_CLIENT_SECRET=... \
  -e AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/callback \
  -p 8080:8080 ghcr.io/antifailure/control-plane:v0.1.1
```

On Kubernetes, the chart in `deploy/helm/antifailure-control-plane` runs step 1
as a Job before the Deployment rolls.

## Do not skip step 1, and do not trust a 200

This is the one that catches people, so it is worth knowing before it happens
to you rather than after.

Step 1 is what grants the application role its membership. Skip it and the
failure is quiet instead of loud: the server starts, `/health` answers 200, the
container reports healthy, and every query fails with

```
ERROR:  relation "organizations" does not exist
```

which reads like a missing migration and is not one. Postgres does not tell a
role that lacks `USAGE` on a schema that it lacks permission. It tells it the
relation is not there.

So the check that means anything is the membership itself, not the health
endpoint:

```sql
SELECT pg_has_role('af_app', 'antifailure_app', 'MEMBER');
```

## Point a repository at it

```yaml
control_plane:
  url: https://cp.example.com
```

Then set `github.mode` to `app` so environments outlive the job and a reviewer
can open one.

Related: [the full control plane guide](/docs/self-hosting/control-plane/),
[every variable it reads](/docs/reference/control-plane/),
[running it on Azure](/docs/self-hosting/azure/).

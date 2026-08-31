---
title: The control plane
description: What it adds, why everything works without it, and how to run one.
sidebar:
  order: 1
---

Antifailure works without a control plane. `af up` builds an environment on the
machine it runs on, and nothing calls home.

The control plane is what a team adds when one person's laptop stops being the
right place for the answer: environments that outlive a CI job, a reviewer who
wants to open one, scheduling across a queue, quotas, and history.

## Everything degrades to local

```
AF-CP-001 The control plane at https://cp.example.com could not be reached.
  Next: Antifailure works without it. Unset control_plane.url to run fully
  locally.
```

```
AF-CPL-003 The control plane could not be reached: dial tcp: i/o timeout
  Next: Environments keep working without it; events are buffered and sent when
  it returns.
```

That is the design and not a consolation. Events are buffered and delivered
when it comes back, environments keep running, and teardown still works, because
teardown reads the local journal rather than the control plane.

## Running one

Two steps, and the order is not optional. The first prepares the database; the
second serves requests.

```sh
# 1. Prepare the database. Applies the schema, creates the application role,
#    and grants it the membership that makes the schema visible to it.
docker run --rm \
  -e AF_MIGRATION_DATABASE_URL=postgres://owner:...@db:5432/antifailure \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  ghcr.io/antifailure/control-plane:latest node bootstrap.mjs

# 2. Serve. Note what is absent: no migration credential, and no AF_MIGRATE.
docker run \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  -e AF_GITHUB_CLIENT_ID=... \
  -e AF_GITHUB_CLIENT_SECRET=... \
  -e AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/callback \
  -p 8080:8080 ghcr.io/antifailure/control-plane:latest
```

`latest` names the newest released version. It is moved by the push of a `v*`
tag and by a maintainer republishing one, and never by a build off `main`. Pin
a digest for an install you have to be able to reproduce: a tag can be moved
and a digest cannot.

Every variable it reads is in the [configuration
reference](/docs/reference/control-plane/), including retention and the schema
maintenance that keeps the events table partitioned.

On Kubernetes, use the chart in `deploy/helm/antifailure-control-plane`, which
runs step 1 as a Job before the Deployment rolls. It installs on any conformant
cluster and is developed against kind.

## Two database roles, on purpose

The application connects as an unprivileged role that cannot run DDL. A role
that can `ALTER TABLE` can drop the policies that isolate tenants, so the role
serving requests is deliberately not that role.

### `antifailure_app` is not an account

This is the part that catches everyone, so it is worth being exact.

Migration `0001_init.sql` creates `antifailure_app` as `NOLOGIN`. It is a GROUP
role that holds the grants. **Nobody can connect as it.** The application
connects as a *separate* login role that is a member of it and owns nothing:

```sql
-- Run by the bootstrap step above. Shown here for anyone doing it by hand.
CREATE ROLE af_app LOGIN PASSWORD '...' NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
GRANT antifailure_app TO af_app;        -- after the migrations, not before
GRANT CONNECT ON DATABASE antifailure TO af_app;
```

The grant has to come *after* the migrations, because that is what creates
`antifailure_app`.

If you skip it, the failure is quiet and confusing rather than loud. The schema
migrates, the server starts, `/health` returns 200, and every query fails with:

```
ERROR:  relation "organizations" does not exist
```

which reads like a missing migration and is not one. A role with no `USAGE` on
the schema is not told that it lacks permission; it is told the relation is not
there. Check the membership directly:

```sh
psql -c "SELECT pg_has_role('af_app', 'antifailure_app', 'MEMBER')"   # expects t
```

### What the unprivileged role cannot do

Verified against a real Postgres rather than asserted:

| Attempt | Result |
| --- | --- |
| `ALTER TABLE users DISABLE ROW LEVEL SECURITY` | refused, `must be owner of table users` |
| `DROP POLICY self_or_shared_org ON users` | refused, `must be owner of relation users` |
| `CREATE TABLE ...` | refused, `permission denied for schema public` |
| `UPDATE` or `DELETE` on `audit_entries` | refused, `permission denied` |
| `ALTER ROLE af_app BYPASSRLS` | refused, needs `CREATEROLE` |
| `SELECT` with no tenant set | returns nothing, rather than everything |

Tenant isolation is row level security in Postgres rather than a `WHERE` clause
in the application. A missing clause is a bug that returns another
organisation's data; a missing policy is a table that returns nothing. The
suite proves it by running every query as a second tenant and asserting it sees
none of the first's rows, on every table, and it fails if a new table appears
that nobody classified.

## Connecting an engine

An engine token is created in the control plane and read from the environment.

```sh
export AF_CONTROL_PLANE_URL=https://cp.example.com
export AF_CONTROL_PLANE_TOKEN=aft_...
```

```
AF-CPL-001 No control plane token is configured.
  Next: Create an engine token in the control plane, then set
  AF_CONTROL_PLANE_TOKEN. Everything except this command works without one.
```

```
AF-CP-002 The control plane rejected this engine's token.
  Next: Create a new engine token in the control plane and set
  AF_CONTROL_PLANE_TOKEN to it. The old one was revoked, expired, or belongs to
  a different control plane.
```

Tokens are stored as a hash. A control plane database that leaks does not leak
anything that can be used against it, and a revoked token stops working
immediately rather than at the end of a cache window.

## Reading an environment

```
AF-CPL-002 The control plane has no environment called env-pr-41.
  Next: Check the identifier with 'af env list', or confirm the engine that
  created it was sending events to this control plane.
```

The second half is usually the answer: an engine with no token, or one pointed
at a different control plane, creates environments the control plane never hears
about.

## Health checks

`/health` returns `{"ok":true}` and **does not touch the database**. It answers
"is this process running", which makes it a correct liveness probe and a wrong
readiness probe: a replica that has lost its database still returns 200 and
would still be sent traffic.

So the Helm chart and the Terraform both use `/health` for liveness only, and a
TCP check for readiness. Neither claims more than it can check. If you write
your own probes, do the same.

## The audit log

Append only, enforced by the grants rather than by the code: the application
role has `INSERT` and `SELECT` on it and nothing else, and `UPDATE`, `DELETE`
and `TRUNCATE` are explicitly revoked. Entries are hash chained, so removing one
from the middle leaves a break that anybody can detect.

Related: [configuration](/docs/reference/control-plane/), [GitHub](/docs/guides/github/).

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

```sh
docker run \
  -e AF_DATABASE_URL=postgres://antifailure_app:...@db/antifailure \
  -e AF_MIGRATION_DATABASE_URL=postgres://owner:...@db/antifailure \
  -e AF_MIGRATE=1 \
  -e AF_GITHUB_CLIENT_ID=... \
  -e AF_GITHUB_CLIENT_SECRET=... \
  -e AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/callback \
  -p 8080:8080 ghcr.io/antifailure/control-plane:v0.1.1
```

Every variable it reads is in the [configuration
reference](/reference/control-plane/), including retention and the schema
maintenance that keeps the events table partitioned.

## Two database roles, on purpose

The application connects as an unprivileged role that cannot run DDL. A role
that can `ALTER TABLE` can drop the policies that isolate tenants, so the role
serving requests is deliberately not that role.

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

## The audit log

Append only, enforced by the grants rather than by the code: the application
role has `INSERT` and `SELECT` on it and nothing else, and `UPDATE`, `DELETE`
and `TRUNCATE` are explicitly revoked. Entries are hash chained, so removing one
from the middle leaves a break that anybody can detect.

Related: [configuration](/reference/control-plane/), [GitHub](/guides/github/).

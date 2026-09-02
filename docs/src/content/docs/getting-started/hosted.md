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
  Next: Antifailure works without it. Run af logout, or unset
  AF_CONTROL_PLANE_URL, to work fully locally.
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
  ghcr.io/antifailure/control-plane:main-b53906a node bootstrap.mjs

# 2. Serve.
docker run \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  -e AF_GITHUB_CLIENT_ID=... \
  -e AF_GITHUB_CLIENT_SECRET=... \
  -e AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/callback \
  -p 8080:8080 ghcr.io/antifailure/control-plane:main-b53906a
```

On Kubernetes, the chart in `deploy/helm/antifailure-control-plane` runs step 1
as a Job before the Deployment rolls.

The tag names the commit the image was built from, and every push to `main`
publishes one. Do not run `:latest` or `:v0.1.1`: they are the same image and it
predates this page. [Which tag to
run](/docs/self-hosting/control-plane/#which-tag-to-run) has the details and the
command that lists what is published.

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

## Point this machine at it

The control plane is not a manifest key. A manifest describes an application,
and which control plane you happen to be signed in to is a fact about your
machine rather than about the code, so it lives with the credential:

```sh
af login --control-plane https://cp.example.com
```

The token goes straight into the operating system's credential store. It is
never shown, never copied through a clipboard, and never written to a shell
history file. `AF_CONTROL_PLANE_URL` sets the same thing for a runner that
cannot open a browser, and `af logout` removes it and revokes it everywhere.

Then set `github.mode` to `app` in the manifest, which is a fact about the
repository, so environments outlive the job and a reviewer can open one:

```yaml
github:
  mode: app
```

## What the control plane has to be told

An environment appears in the console because the engine reported it, not
because anything here created it. Every `environment.*` event carries the
repository as `owner/name`, the branch, the pull request number when there is
one, and the lifetime `runtime.ttl` declares, and the control plane creates the
environment from whichever of those events reaches it first.

Each of those events also carries the instant the environment began existing,
which is not the instant the event fired: an environment is reported ready
after its build, and the build is the expensive part of a cold run. Usage and
the expiry are both measured from the earlier instant.

The repository name comes from `GITHUB_REPOSITORY` when the run is in GitHub
Actions, and otherwise from the `origin` remote of the checkout. A checkout
with neither, which is a directory somebody is trying the tool in, reports no
repository: the environment runs, and it does not appear in the console. The
response says so on the event rather than accepting it silently, and the
control plane counts it as `af_ingest_events_total{outcome="unprojected"}`.

A repository the GitHub App has never mentioned is created from the name the
engine reports rather than refused, so an engine running against a repository
nobody has connected still shows up.

Related: [the full control plane guide](/docs/self-hosting/control-plane/),
[every variable it reads](/docs/reference/control-plane/),
[running it on Azure](/docs/self-hosting/azure/).

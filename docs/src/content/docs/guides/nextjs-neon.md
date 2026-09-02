---
title: Next.js with Neon
description: An environment per pull request whose database is a Neon branch, and the three things that differ from the local provider.
sidebar:
  order: 14
---

[The Next.js guide](/docs/guides/nextjs) covers what the service needs. This
covers what changes when the database is a Neon branch instead of a container
on the machine running `af`.

Nothing in the application changes. It still reads `DATABASE_URL`, and the
engine still injects it. What changes is where that database comes from, and
three consequences worth knowing before the first busy day.

## The manifest

```yaml
database:
  provider: neon
  version: 17
  project: dawn-river-12345678
  api_key_env: NEON_API_KEY
  max_branches: 10
```

`project` is the Neon project branches are created in. It is not a secret,
which is why it lives in the manifest and the key that reaches it does not:
`api_key_env` names the variable, and `NEON_API_KEY` is the default.

`af explain` will not tell you if `project` is missing. That check happens when
the provider is built, so the refusal arrives at `af up`, naming what is
absent. Worth knowing, because `af explain` is otherwise the command that
catches a bad manifest.

## Your service gets the pooled string, your migration does not

Both connection strings are used, and nothing has to be configured for it. A
service receives the pooled one. A `migrate` command receives the direct one,
and so do golden refreshes and restores, because a transaction pooler does not
support the session level features migrations and `pg_restore` use.

This matters more with Next.js than with most things, because a migration run
by Drizzle, Prisma or `psql` against a pooled host fails in ways that look like
the migration is wrong rather than the connection. The engine asks for a pooled
string whenever the provider says it has one and uses the direct string for
both when it does not, so the correct thing happens without a second variable.

Keep the pool small in the application. A pooled endpoint is already a pool,
and every server instance holding twenty connections to it is twenty
connections spent for nothing:

```ts
pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 5 });
```

## The branch limit is the thing that bites a busy repository

One environment per pull request means one Neon branch per pull request, and a
plan has a ceiling. Neon's API does not report that ceiling on a path the
provider can rely on, so `max_branches` states it:

```yaml
  max_branches: 10
```

Reaching it fails with `AF-DB-006`, naming the limit, rather than hanging or
returning an unexplained 422. Set it to what your plan actually allows.

Nothing else is needed to stay under it. A branch is given back when the
environment is torn down, and teardown is not a setting: `af ci` tears down
whatever the outcome, including on a failed job and including on a cancelled
one. This page used to tell you to set `github.teardown_on` here, which was
wrong twice over, since that key is
[read by nothing](/docs/reference/manifest#github) and the values it named
were not ones the schema accepts.

## What a free tier will and will not hold

A free tier branch is capped at 512 MB with six hours of history. That is fine
for previews of a small application and it is not enough for a copy of a real
production database. If your golden is larger than that, either subset it or
use the local `docker` provider, which is bounded by the disk on the machine
rather than by a plan.

Related: [the Neon provider in full](/docs/providers/neon),
[Next.js](/docs/guides/nextjs), [an environment per pull
request](/docs/getting-started/pull-requests).

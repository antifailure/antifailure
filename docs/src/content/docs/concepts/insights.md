---
title: Insights
description: What Postgres itself can tell you about a change, before anybody clicks anything.
sidebar:
  order: 10
---

A branch is a real database with production's shape in it, which makes some
questions answerable without running the application at all.

```yaml
insights:
  enabled: true
  migration_rehearsal: true
  query_regression: true
  plan_diff: true
  regression_factor: 2.0
  regression_min_ms: 10
  large_table_rows: 1000000
```

## Migration rehearsal

The migrations run against the branch, and how long each took is reported.

```
AF-DB-030 Migrations failed on the branch: relation "users_email_key" already
exists
```

A migration that fails here is a migration that would have failed in
production, found before merge instead of during a deploy window.

The timing matters as much as the outcome. A migration that takes four seconds
on an empty test database and ninety on a branch with production's row counts
is a migration that will lock a table in production, and the branch is where
that becomes visible.

An `ALTER TABLE` that rewrites a table over `large_table_rows` is called out
specifically, because that is the shape of change that turns into an outage.

## Query regression

Queries are timed against the branch and against the base. A query more than
`regression_factor` slower is reported.

`regression_min_ms` exists because a query going from 0.1ms to 0.3ms is three
times slower and means nothing. Without a floor the report is all noise and
people stop reading it.

## Plan diff

The `EXPLAIN` plan on the branch against the plan on the base. A sequential
scan where there was an index scan is the classic finding, and it is usually a
`WHERE` clause that changed shape rather than anything anybody thought was a
performance change.

Plans are compared on a database with production's statistics. Comparing plans
on an empty database is comparing two plans that will not be used, since
Postgres chooses differently when a table has a thousand rows and when it has
ten million.

## Turning things off

Every check is a `*bool`, so `false` is distinguishable from unset. Setting
`plan_diff: false` turns off that check and nothing else.

Related: [goldens](/docs/concepts/goldens/), [load](/docs/concepts/load/),
[invariants](/docs/guides/invariants/).

---
title: Invariants
description: Statements about your data that must stay true while agents use the application.
sidebar:
  order: 9
---

An invariant is a read only query that must return no rows. It runs against the
branch while the workflows run, so a flow that appears to succeed while
corrupting data is caught by the data rather than by the screen.

```yaml
invariants:
  - name: no-orphan-orders
    description: Every order belongs to a user that exists.
    sql: |
      SELECT o.id FROM orders o
      LEFT JOIN users u ON u.id = o.user_id
      WHERE u.id IS NULL

  - name: no-negative-balances
    description: A balance can be zero and never less.
    sql: SELECT id FROM accounts WHERE balance < 0
```

Rows returned means the invariant is violated, and the rows are the evidence.

## Why "returns no rows"

Because the answer carries the diagnosis. A boolean tells you something is
wrong; a set of rows tells you which ones, and the query that found them is a
query somebody can run again by hand.

## They must be read only

```
AF-AGT-011 Invariant no-orphan-orders is not read only.
  Next: Use a SELECT; an invariant observes the database and never changes it.
```

Enforced rather than trusted. A check that modified the data would change the
thing the next check is about, and a run whose result depends on check order is
not a result.

## Timeouts

```
AF-AGT-010 Invariant no-orphan-orders did not finish within 30s.
```

Usually a sequential scan on a table that is large even after subsetting. Add
the index the query wants, or narrow it. An invariant that takes a minute runs
on every environment, and the cost lands on every pull request.

## What makes a good one

The things your application assumes and never checks. Foreign keys that are not
enforced by a constraint, totals that should agree with their line items,
states that should be unreachable together.

```sql
-- a subscription that is active with no payment method
SELECT s.id FROM subscriptions s
LEFT JOIN payment_methods p ON p.user_id = s.user_id
WHERE s.status = 'active' AND p.id IS NULL
```

Write one the first time a bug of that shape reaches production. It is the
cheapest possible regression test and it runs against every branch afterwards.

Related: [agents](/concepts/agents/), [insights](/concepts/insights/).

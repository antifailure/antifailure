---
title: Invariants
description: Statements about your data that must stay true while agents use the application.
sidebar:
  order: 9
---

An invariant is a read only query that must return no rows, asked of the branch
while the workflows run, so that a flow which appears to succeed while
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

## When they run

After the workflows, in `af test` and in `af ci`, against the environment's own
branch. The workflows are the part that changes the data, so asking before them
would be asking about the golden.

`af invariants` asks them on their own, which is what you want while writing
one, after a migration, or when a run failed and you want to know whether the
data is the reason.

```
$ af invariants

  invariants
  fail  no-orphaned-orders           does not hold
      Every order belongs to a customer that exists.
      order_id  customer_id
      5  999999
      6  999998

  0 held, 1 violated, 0 could not be checked
```

A violated invariant fails the run, and the pull request comment says so on its
first line rather than reporting that every workflow passed.

## Return the rows, not a count

```
The invariant "no-orphan-orders" counts the violations instead of returning
them, so it can never hold.
```

`SELECT count(*) FROM orders WHERE ...` returns one row saying zero. One row is
a violation, so an invariant written that way is red forever. Select the
offending rows themselves and the empty result is the pass.

The manifest refuses a bare count for that reason. A grouped aggregate is fine,
because `GROUP BY ... HAVING` returns no rows when nothing matches:

```sql
SELECT email, count(*) FROM users GROUP BY email HAVING count(*) > 1
```

## Why "returns no rows"

Because the answer carries the diagnosis. A boolean tells you something is
wrong; a set of rows tells you which ones, and the query that found them is a
query somebody can run again by hand.

## They must be read only

```
AF-AGT-011 Invariant no-orphan-orders is not read only.
  Next: Use a SELECT; an invariant observes the database and never changes it.
```

Enforced rather than trusted. Every statement runs inside a transaction opened
`READ ONLY`, so the refusal comes from Postgres and not from us reading your
SQL and hoping. The manifest also refuses a statement that names a writing
keyword, which catches the common mistake early with a better message, but that
check cannot be complete: `SELECT do_the_thing()` names no keyword and writes,
because the writing is inside the function. The transaction is what makes the
promise true, and the transaction is rolled back either way.

A check that modified the data would change the thing the next check is about,
and a run whose result depends on check order is not a result.

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
cheapest possible regression test, and the intent is that it runs against every
branch afterwards.

Related: [agents](/docs/concepts/agents), [insights](/docs/concepts/insights).

---
title: Subsetting
description: Taking a production shaped slice of a database instead of all of it, and keeping every foreign key resolvable.
sidebar:
  order: 12
---

A golden the size of production is a golden nobody refreshes, and a golden
nobody refreshes drifts until it is testing last quarter's schema.

Subsetting takes a slice instead. You name a seed, and the closure over the
foreign keys decides the rest.

```yaml
database:
  provider: docker
  source_url_env: PRODUCTION_DATABASE_URL
  subset:
    enabled: true
    seed_table: tenants
    seed_where: "created_at > now() - interval '90 days'"
    max_rows: 100000
    follow_dependents: 2
```

That takes the tenants created in the last ninety days, everything those rows
reference, and two levels of what references them.

## What it copies, and in which direction

The direction is the thing people get wrong, and the two directions are not
symmetrical.

**Upward, from a row to what it references, is mandatory.** An order whose
customer is missing is a row that violates its own constraint, and a database
that will not load. Nothing configures this and nothing turns it off.

**Downward, from a row to what references it, is optional and bounded.** One
level from a customer is every order they ever placed, which is most of the
database again. `follow_dependents` is how many levels to take, and it defaults
to one.

The two interleave rather than run once each. A table pulled in downward brings
its own upward requirements with it: taking an order's line items means taking
the product each one names, even though no product was anywhere near the seed.

## Referential integrity is the guarantee

Every foreign key in the result resolves. That is checked rather than claimed:
after the copy, one query per key asks whether any row points at something that
is not there, and a run that cannot answer no fails and publishes nothing.

The constraints are not simply revalidated instead, because enforcement is
suspended during the load so that a cycle can be loaded at all, and a constraint
Postgres was not watching reports nothing when it is switched back on.

### Composite keys are one condition, not two

A key over `(region, tenant_no)` referencing `(region, tenant_no)` is a single
condition. Treated as two independent ones it takes rows whose region matches
one parent and whose number matches a different one: each half passes, the pair
does not exist, and the result looks correct until a join returns nothing.

### A null reference is kept

A foreign key column that is null satisfies its constraint, so those rows belong
in the subset. Postgres reads `NULL IN (...)` as unknown rather than true, so
the obvious form of the condition drops every row whose optional reference is
not set. Every generated condition allows nulls explicitly.

A reference that **is** set and points outside the slice excludes the row. That
is a deliberate choice and the tradeoff runs the other way: keeping the row and
clearing the link would lose less data, but the same rule has to hold for the
key that pulled a table into the subset in the first place, and a rule that
stopped narrowing on optional keys would copy a whole table and then clear most
of it.

### Cycles and self references are repaired, and the repair is reported

Some keys cannot be satisfied by copy order at all. A row that points at its own
table needs rows that are still being copied; a cycle between two tables has no
order that loads both.

Those keys are deferred, and put right after the load:

- Where the column is **optional**, the reference is cleared.
- Where it is **required**, the row is removed, because a row that cannot be
  loaded is worse than a row that is not there.

Both are counted and reported. Nothing is repaired quietly.

The repair runs to a fixed point over **every** key, not only the deferred ones,
because one repair can create work for another: removing a project whose lead
was not in the subset leaves any employee whose primary project was that project
pointing at nothing.

## Relationships the schema does not declare

A join that lives in application code is invisible to a subsetter, and those are
exactly the joins a naive subset breaks silently: the table arrives empty and
somebody finds out three days later, from a test that returns nothing.

Two things happen about it. Tables nothing connects to the seed are **reported**,
by name, rather than quietly emptied. And an undeclared relationship can be
declared:

```yaml
    virtual_relationships:
      - from: public.events.employee_id
        to: public.employees.id
```

Declared relationships are followed exactly like real ones and reported
separately, because a wrong one produces a broken subset and the schema cannot
catch it.

## The row budget

`max_rows` caps what is taken from any one narrowed table.

Truncation is deterministic: rows are ordered by primary key before the limit,
so two runs of one plan take the same rows and two goldens can be compared. A
budget with no order would take a different thousand rows every time.

Two consequences follow from that, and both are stated rather than hidden:

- A table nothing narrows is taken **whole**, with no budget. Cutting off a
  small reference table would leave dangling references in everything that
  points at it.
- A table with **no primary key** has no order to truncate by, so the budget
  does not apply to it. If nothing narrows such a table and it is larger than
  the budget, the plan is refused and names it, because there is no honest way
  to take part of it.

## Sequences

After the copy, every sequence is moved past the largest value that arrived,
plus a margin.

Past rather than to: the rows above the largest one copied still exist in
production and will exist in the next refresh. A golden whose sequence sits
exactly on its own maximum hands the application identifiers a later refresh
collides with, and the margin also makes the environment's own rows
distinguishable from production's, which is worth something the first time
somebody is reading a bug report and wondering which is which.

## What it does to your production database

Nothing. Every read happens inside one read-only, repeatable-read transaction.

- **Read only**, because `seed_where` is SQL out of a manifest, and a manifest
  is a file somebody can open a pull request against. A predicate that tries to
  write fails rather than writing.
- **Repeatable read**, because a parent selected from one snapshot and a child
  from a later one is a subset whose references do not resolve through no fault
  of the plan. One snapshot for the whole run.
- **Nothing is created on the source**, not even a temporary table. The
  selection is a chain of materialized common table expressions inside the
  `COPY` itself, which also means it works against a read-only replica, which is
  where you should be pointing it.

Rows move by `COPY` in both directions, in the database's binary format, so a
timestamp, a float and a numeric survive exactly rather than going through a
formatter and a parser.

## Which providers can do it

Subsetting needs an empty database to load the slice into.

| Provider | Subsetting | Why |
| --- | --- | --- |
| `docker` | yes | A candidate is an empty Postgres container the provider fills. |
| `neon` | no | A candidate is a branch of production, so it holds everything the moment it exists. |

On a copy on write provider the branch already shares storage with its parent,
so branching was free and a subset would save nothing. A manifest asking for one
on a provider that cannot is **refused**, naming the provider, rather than
accepted and quietly ignored.

## Masking still runs

Subsetting happens first, masking second, verification third, and publication
only after all three. A subset is not a substitute for masking: it is fewer
rows of the same real data.

Masking's `link` groups still map consistently across the reduced set, because
they are computed from the values, not from the row count.

## Seeing the plan before running it

```
af explain
```

shows the effective subset block with every default resolved.

A refresh prints what it did as it goes: the tables in dependency order with
their row counts, anything repaired, anything that arrived empty, and any table
nothing connected to the seed.

## When it refuses

`AF-DB-011` covers the whole family: a seed table that is not in the database,
a seed table named ambiguously in two schemas, a predicate the database will not
run, a plan that cannot be run, a provider that cannot subset, and a copy that
finished with a key that does not resolve.

The message carries which of those it was.

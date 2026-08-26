---
title: Masking
description: How production data becomes data that is safe to branch, and what stays true about it.
sidebar:
  order: 2
---

Masking replaces every value that identifies a person with a synthetic one,
while keeping everything a test depends on: shapes, lengths, formats, joins,
distributions, and uniqueness.

That second half is the whole difficulty. Nulling every string is easy and
gives you an environment where nothing renders, no form validates, and no join
returns a row. A masked database has to still behave like the one it came from.

## Where it runs

On a golden candidate, never on a source.

```
AF-MSK-005 Masking is only permitted on a golden candidate, and
postgres://prod/app is a source database.
  Next: Run masking against a golden candidate; the engine never rewrites a
  source.
```

The source is read once, with `pg_dump`, and never written to. There is no flag
that changes this.

## The rules

```yaml
# masking.yaml
rules:
  - table: users
    column: email
    transform: email
    why: "customer addresses"

  - table: "*"
    column: "*_id"
    type: uuid
    transform: uuid_remap
    link: entity
    why: "keeps foreign keys joinable after remapping"
```

`table` and `column` accept `*`. `type` matches the Postgres type. `why` is one
sentence, printed by `af mask plan` beside the column it applies to, so a
decision made months ago is readable when somebody questions it.

### `link` is the one that catches people

Two columns joined by a foreign key must mask to the same value, or the join
returns nothing. `link` groups them:

```yaml
  - table: users
    column: id
    transform: uuid_remap
    link: user
  - table: orders
    column: user_id
    transform: uuid_remap
    link: user
```

Without the link, `users.id` and `orders.user_id` get different new UUIDs, every
order becomes an orphan, and the environment looks like a customer base with no
orders. Nothing errors. That is why it is worth stating explicitly.

## Planning before applying

```sh
af mask plan      # every column, the rule that matched, and why
af mask preview   # before and after, on a sample, values redacted
af mask apply     # run it against a candidate
af mask verify    # scan the result
```

`af mask plan` is the one to read. It lists every column in the schema, which
rule matched it, and what will happen. A column with no rule is shown as such,
which is how you find the `notes` field nobody thought about.

## Columns with no rule

```
AF-MSK-008 The columns orders.notes, tickets.body hold free text and have no
masking rule.
  Next: Give each column a rule, or allowlist it explicitly if it is known to
  hold no personal data.
```

Unclassified free text defaults to `nullify`, because a column nobody has
confirmed is safe is a column that might hold anything a customer typed. That
default is deliberately inconvenient: it makes the page render wrong, which
makes somebody look.

To keep the shape, give it `free_text`. To state it was reviewed and is safe,
give it `preserve` and a `why`.

## A rule that names nothing

```
AF-MSK-003 The masking rule for users.emial names a column that does not exist
in the schema.
  Next: Remove the rule or correct the name; 'af mask plan' lists the columns
  it found.
```

A typo in a rule is a column with no masking and no warning, so a rule that
matches nothing is an error rather than a shrug. Wildcards are exempt: `column:
"*_id"` matching nothing in a small schema is normal.

## What masking does not decide

Whether the result is safe. That is [verification](/concepts/verification/),
which runs afterwards, scans for anything that still looks like a person, and
refuses to publish if it finds something. The rules are a claim; the scan is
the check.

Related: [transforms](/reference/transforms/), [goldens](/concepts/goldens/).

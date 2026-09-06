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

## Writing the rules from the schema

```sh
af mask init      # reads the source database, writes masking.yaml
```

`af mask init` connects to the source the manifest names, reads the catalog,
and runs the classifier over it. It writes one rule per column that carries
personal data, restating the default that matched with its `why`, and one
explicit rule per column the classifier could not place, so the file records a
decision for every column rather than leaving the unplaced ones to the
inconvenient default. `af mask plan` on the result reports zero problems and
zero unmatched columns, which is the point: the first plan you read is one
where every row is a choice to confirm rather than a gap to fill.

It refuses to overwrite a `masking.yaml` that exists. Pass `--force` to
replace one, and read the diff, because a rule you edited by hand is what the
rewrite would lose. `af init` runs the same code when the source resolves at
init time, and says either that the rules were written from N tables or that
they were not written because there is no source yet.

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

Its first lines are the two counts that matter:

```
Masking plan
  Read from the source named by AF_SOURCE_DATABASE_URL.
  231 columns across 55 tables, about 6111 rows.
  128 columns have no rule, and 128 of those are copied unchanged.
```

Two different things share "no rule". Most such columns are emptied by the
fail closed default, which is a question with a safe answer already in place.
The rest are copied unchanged: a `NOT NULL` text column, a `bytea`, an enum, an
array, anything the default has no way to empty. Those hold exactly what
production holds, and that count is the one to read first. It is printed at the
top because the list it summarises is printed at the bottom, after every
assignment, and on a real schema that is several hundred lines down.

The same count travels. `af mask apply` and `af golden refresh` print
"N columns copied unchanged with no rule" beside their own success line,
`af mask verify` prints it beside its verdict, the golden's attestation records
the count and the names, and `af golden list` shows it in a column called
`NO RULE`, so a golden made from a rules file with a gap in it says so wherever
the golden is looked at.

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

## Third party identifiers

A Stripe customer id, a subscription id, an invoice id: none of these is a
secret, and every one of them is a live pointer into a real account. Stripe
issues it once and it never changes, so anybody who has seen it in an invoice
email or the Stripe dashboard can say which real customer a masked row belongs
to, and it works the same in every environment because the value is the same
in every environment.

They are `NOT NULL` text in almost every schema, so the default cannot empty
them, and they are copied unchanged until a rule names them. `prefixed_id`
keeps the prefix and replaces the body with a keyed hash of the same length:

```yaml
  - table: subscriptions
    column: stripe_customer_id
    transform: prefixed_id
    link: stripe
    why: "a live pointer into a real Stripe account"
```

The billing code still recognises `cus_` as a customer, the unique constraint
holds, and with one `link` across every table that carries the id the same
customer maps to the same fake customer in all of them. The masked body is
lowercase hex, which the verification scan's provider identifier detector does
not report, so the scan can still catch a real one.

## Columns the scan cannot read

A `bytea` column holds whatever was written into it, and the verification scan
cannot pattern match a sealed blob. It decodes each value as UTF-8 where it
decodes and lists the column as not readable where it does not. A column the
scan cannot read is masked by its rule or by nothing, so give every one a rule:
`nullify` where the column allows it, `hash_hex` where it does not.

```yaml
  - table: provider_keys
    column: ciphertext
    transform: hash_hex
    why: "the customer's sealed API key; the api refuses a body its tag does not authenticate"
```

Read how the application treats an unreadable value before choosing. A
decryption that fails closed with a typed error is what you want on the copy; a
crash is not.

## What masking does not decide

Whether the result is safe. That is [verification](/docs/concepts/verification),
which runs afterwards, scans for anything that still looks like a person, and
refuses to publish if it finds something. The rules are a claim; the scan is
the check.

Related: [transforms](/docs/reference/transforms), [goldens](/docs/concepts/goldens).

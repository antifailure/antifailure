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

`table` and `column` accept `*`. `type` matches the type name, written in the
Postgres vocabulary whichever store the column is in: see [more than one
store](#more-than-one-store). `why` is one sentence, printed by `af mask plan`
beside the column it applies to, so a decision made months ago is readable when
somebody questions it.

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

## More than one store

An environment can hold more than one datastore, and the same person is usually
in several of them: a Postgres holding accounts and a ClickHouse holding the
events about them, joined on an identifier that is in both.

**One identity has to mask to one person across every store.** If it does not,
a join across the two returns nothing or returns somebody else, every report
built on it is plausible, and nothing anywhere says so. That is worse than a
store nobody copied at all, because an empty store is visible within a minute of
opening a chart.

It is one `masking.yaml` for every store, and the `type` in a rule is written in
the Postgres vocabulary whatever the store is. Each engine's own type names are
mapped onto the Postgres ones before a rule is matched, so `type: text` means
Postgres `text` and ClickHouse `String` and nobody writes the rule twice. A
rules file per engine would be a rules file that goes stale for one engine and
not the other, and the failure mode of that is a column masked in one store and
real in the next.

Two refusals follow from the same principle:

- A store whose engine this build has no dialect for is refused when the plan is
  made. Guessing at Postgres would mean matching a rule against a vocabulary the
  store does not have, so nothing would match, so every column would fall
  through to the branch that says nobody decided. A column that looks classified
  and was not is the failure this whole page is about.
- A ClickHouse table with no sorting key is refused for the same reason a half
  masked table is never started. ClickHouse has no physical row identifier, so
  there is no statement that means one row. Postgres always has `ctid`, so the
  refusal is the engine's rather than a new rule about keys.

The transforms themselves never needed a store. Every one is a pure function of
the project key, the column identity, and the input value, computed on the
machine running the refresh and never in the database, so what a value masks to
has never depended on which store it came out of. What did depend on the store
was the classifier deciding what to do with a column, and that is what the
dialect settles.

### The check that says the two stores agree

The guarantee is checked rather than argued, and you run the check on your own
stores rather than reading about ours. Give each datastore the name of the
variable holding a read only connection string:

```yaml
database:
  source_url_env: PRODUCTION_DATABASE_URL

datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: CLICKHOUSE_URL
```

Then:

```
af mask crossstore
```

It takes both stores' plans, finds every identifier that appears in both, masks
probe values through each side, and reports the share that come out identical:

```
join keys verified identical across primary and events: 4 of 4 (100.0%)
```

**It reads catalogs and no rows.** The probe values are its own, so what it
needs from a store is the schema, which is why it is safe to point at
production. The report says how many tables and columns it read and that it read
no rows, as a field rather than as a promise on this page.

**That is enforced by a test rather than by intent.** A live test runs the
check against a real ClickHouse, then reads the server's OWN `system.query_log`
back and fails if any statement the check sent selected from a data table. A
sentence saying no rows are read is something anybody can write; a query log
the server keeps is something that can contradict it, and if it ever does, the
`rows_read` of zero in the report is a lie and the build says so.

A table the reader deliberately left out is named with the reason, because a
share of the join keys it could see is a true answer to a smaller question when
half a schema was dropped in silence. A ClickHouse view has no rows of its own
and a `Distributed` engine is a pointer at another server, so neither is a store
whose masking can be compared. Both are left out of the comparison and named in
the report, rather than dropped in silence.

Every store it could not read is named with the reason, and a store that names
no `source_url_env` is named as never read at all. A run that reached one store
says it proved nothing rather than reporting a hundred percent of one, and that
answer carries a different exit code from a real disagreement: one is a
statement about your data and the other is a statement about what could be
reached.

The same question is the `cross_store` question of the `inspect_data_masking`
tool, where it answers PASS, FAIL or INCONCLUSIVE. Where two stores are present
it is also a line in the [component inventory](/docs/concepts/inventory), and
until something compares them that line reads unmeasured rather than passed.

Anything below 100 percent is a bug, and there are three ways to get there:

- one side is masked and the other is copied unchanged, which is a leak as well
  as a broken join
- the two sides are masked with different transforms
- the two sides are masked under different links, so each derives its own subkey
  and one input produces two different outputs

There is deliberately no way to mark a pair exempt. Two columns with one name in
two stores that genuinely mean different things is a real thing to look at, and
the cost of looking at it is a rule; the cost of silencing it is a twin that is
wrong in a way nobody can see. A report that found nothing to compare is not a
pass either.

The commonest thing it finds is a blob that is `jsonb` on one side and a
`String` holding JSON on the other. Nothing leaks, and the two stores still hold
different values for one field. A rule settles it:

```yaml
  - table: "*"
    column: properties
    type: text
    transform: empty_json
    why: "the analytics store keeps this JSON in a String"
```

### What is not built yet

The dialect boundary is the classification, the statements and the verification
scan. Nothing yet refreshes a golden for a second store or branches one: there
is no ClickHouse provider, and `datastores` entries other than `primary` are
reported with their declared stance rather than measured. `af mask crossstore`
opens a connection to a second store to READ ITS CATALOG and nothing else; a
ClickHouse is read over its HTTP interface, and a URL naming the native port is
refused with the HTTP one in the message rather than attempted. Said here rather
than left to be discovered, because a boundary that looks complete from outside
is how somebody ends up trusting one.

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

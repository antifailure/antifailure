---
title: Extensions and custom storage
description: How a golden carries PostGIS, pgvector, TimescaleDB or pg_cron, and what happens to a table stored in an access method that is not the heap.
sidebar:
  order: 18
---

A Postgres schema is rarely only Postgres. It has PostGIS geometry, or pgvector
embeddings, or a TimescaleDB hypertable, or a table stored in an access method
that came out of an extension. A golden that cannot carry those is a golden of
somebody else's database.

The `docker` provider builds a golden inside a container, so what that container
carries is a decision the manifest makes:

```yaml
database:
  provider: docker
  version: 17
  image: pgvector/pgvector:pg17
  extensions:
    - vector
    - pg_trgm
```

Three keys, because the answer has three parts and skipping any one of them
produces a server that starts perfectly and is missing something.

## The image is where an extension lives

An extension is files on the server's disk before it is anything in a database.
No SQL adds one the image does not have, which is why a missing extension fails
at `CREATE EXTENSION` with "is not available" rather than at install time.

Without `database.image` the provider runs `postgres:<version>-alpine`, which
carries the contrib modules and nothing else. That is the right default and it
is the reason [AF-DB-007](/docs/reference/errors) exists: a copy of a schema
using PostGIS stops on the first object that needs it.

Name an image that already carries what the schema needs. `pgvector/pgvector`,
`postgis/postgis`, `timescale/timescaledb` and `citusdata/citus` all publish
one, and an image you build yourself works the same way. Pin it by digest where
the golden has to be reproducible.

Two things the image has to be true about, and both are checked rather than
trusted:

- **It runs the official entrypoint and honours `PGDATA`.** A golden is the
  container's filesystem committed, so the data directory is moved to
  `/var/lib/antifailure/pgdata` to keep it out of the volume the stock image
  declares. An image declaring a volume of its own over that path is refused,
  because the alternative is a golden that publishes successfully and holds no
  rows at all.
- **It is the major version the manifest declares.** `database.version` is
  compared against what the server reports, not against the tag. An image on
  16 beside `version: 17` is refused, because everything downstream works and
  every environment runs a Postgres your application does not.

## The extension still has to be created

An extension installed in the image and never created carries no types, no
operators, no functions and no table access methods. `database.extensions` is
the list to create, in the order given, one `CREATE EXTENSION IF NOT EXISTS`
each, before the source is copied in.

Before, because the copy is what needs them. `IF NOT EXISTS`, because an image
such as `citusdata/citus` creates some of its own and a manifest naming one of
those is right rather than wrong.

An extension the image does not carry is refused by name, with the image named,
so that the answer is about the image rather than about your SQL.

## Some extensions are loaded, not created

`timescaledb`, `citus` and `pg_cron` are loaded by the postmaster before any
database is opened. Creating one in a server that did not load it fails with a
message about `shared_preload_libraries`, and a server holding such an
extension's catalog entries without its library refuses to start at all.

```yaml
database:
  provider: docker
  version: 17
  image: timescale/timescaledb:2.17.2-pg17
  preload_libraries:
    - timescaledb
  extensions:
    - timescaledb
```

`preload_libraries` is ADDED to `shared_preload_libraries` rather than
replacing it. Dropping `pg_stat_statements` is not an option the manifest has:
without it the insights read a permanently empty table and report that
statement timing is unavailable on every environment.

The libraries you declare come first, in the order you write them, and
`pg_stat_statements` follows them. That order is measured rather than chosen:
citus refuses to load from anywhere but the front, and a server started with
the statistics module ahead of it exits during initialisation with "Citus has
to be loaded first" and never accepts a connection. Nothing has the opposite
requirement, so the statistics module is the one that moves. A plain library
name only, never a path.

The list is recorded on the golden image and read back when a branch starts, so
a branch carries what its golden was built with even if the manifest has since
stopped asking. Removing a line changes the next golden, never the branches of
the ones that already exist.

## Tables in a custom access method

A table created `USING <am>` from an extension is carried end to end: through
the golden, through every branch of it, through `pg_dump` and `pg_restore`, and
through subsetting, whose loads go in as binary `COPY`.

The access method travels with the table rather than being flattened. Read it
back on the far side and it is the one you created the table with:

```sql
SELECT am.amname
FROM pg_class c JOIN pg_am am ON am.oid = c.relam
WHERE c.relname = 'measurements';
```

The extension providing the access method has to be in the image and in
`database.extensions`, for the ordinary reason: the restore reaches a
`CREATE TABLE ... USING columnar` and the access method has to exist before it.

### What masking will not do, and why it says so

Masking rewrites a row at a time, addressed by the table's primary key or, when
there is none, by `ctid`. Both of those are guarantees of the heap rather than
of Postgres. An access method is free to implement neither, and the catalog
records the handler without recording what the handler implements, so there is
nothing to ask.

Measured against `columnar` from citus on Postgres 17.2, both are refused:
`SELECT ctid FROM t` and `UPDATE t SET ... WHERE id = 2` each answer "UPDATE
and CTID scans not supported for ColumnarScan", and the table accepts a primary
key regardless, so nothing about its shape warns you first.

So masking refuses at planning time, before anything is written, naming the
table and the access method. A run that discovered this partway through a table
would leave data neither real nor safe.

The refusal is narrow. It applies only to a column masking would actually
rewrite, so a table on a custom access method whose columns are preserved, or
that holds nothing any rule matches, goes through untouched. Give such a column
a rule that preserves it, and the golden carries the table:

```yaml
# masking.yaml
rules:
  - table: archived_people
    column: email
    transform: preserve
    why: columnar storage cannot be rewritten a row at a time, and this archive is already scrubbed at source
```

Preserving a column is a decision somebody has to be able to defend, which is
why it is written down with a reason rather than inferred from the storage.

## What is not covered

- These three keys are the `docker` provider's. A hosted provider furnishes its
  own Postgres, so the extensions available in it are that service's to enable,
  and a manifest naming any of the three beside another provider is refused
  rather than ignored.
- Row counts and table sizes for a custom access method are whatever that
  access method reports through `pg_class.reltuples` and `pg_table_size`. An
  access method that does not maintain them reports zero, and the fidelity and
  volume numbers will say zero rather than guessing.

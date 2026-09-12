---
title: Managed Postgres vendors
description: What each of thirteen managed Postgres products can do, what serves it here, and what nobody measured.
sidebar:
  order: 7
---

Thirteen managed Postgres products were looked at to decide one thing per
vendor: whether Antifailure has to build anything for it, or whether the
[`pgurl`](/docs/providers/pgurl) provider already serves it.

The answer for eleven of them is that `pgurl` serves it and nothing needed
building. One, Xata, has real copy on write branching, and it got a provider of
its own: [`xata`](/docs/providers/xata). One, Tembo Cloud, no longer sells
managed Postgres at all.

Eleven and one and one is the finding rather than an apology for one. Thirteen
thin providers that each call `pgurl` and add a name to a list would compile,
would look like thirteen integrations, and would do nothing the one provider
underneath was not already doing.

## What was proved, and what was not

Every verdict on this page was read from the vendor's own published
documentation, on the date recorded beside it. **No account was created on any
of these thirteen services, no request was sent to any of their control planes,
and no database was branched on any of them.** So this page records what each
vendor says its product does. It does not record what any of them did, and no
number on this page was measured against a vendor.

The refusal is deliberate. A seconds figure for a service nobody connected to
is an upper bound wearing the clothes of an answer, and it would be quoted as a
measurement by the first person who read it.

## The two questions that are not the same question

The `pgurl` provider needs two connection strings and they are two different
servers.

The **source** is production, read once per refresh by `pg_dump`, which needs
read access and nothing else. Every one of the thirteen can be a source.

The **host server** is where the goldens and the branches are made, and it
needs a role that may `CREATE DATABASE`. It is not the source and the provider's
own documentation says it should not be the production server. So a vendor that
refuses `CREATE DATABASE` is not a vendor Antifailure cannot serve. It is a
vendor that cannot also be the host server, which is a smaller and truer claim
than a row of ticks would have made.

## The thirteen

`CoW` is copy on write: whether a branch shares storage with its parent, so that
branch time does not grow with the database. It is the field
`engine/conformance/cow.go` can falsify on a provider, and it is recorded here
for vendors no provider was written for so the table is not the word "fork"
thirteen times.

| Vendor | Its own mechanism | CoW | Can host goldens | Read on |
| --- | --- | --- | --- | --- |
| Aiven for PostgreSQL | fork restored from a backup | no | yes, additional databases are supported | [aiven.io](https://aiven.io/docs/platform/concepts/service-forking) |
| Crunchy Bridge | fork restored from a backup, point in time | no | yes, the `postgres` role is a superuser | [docs.crunchybridge.com](https://docs.crunchybridge.com/api/cluster) |
| DigitalOcean Managed Databases for PostgreSQL | fork restored from a backup | no | yes, a cluster holds many databases | [docs.digitalocean.com](https://docs.digitalocean.com/products/databases/postgresql/how-to/fork-clusters/) |
| Fly Managed Postgres | fork, mechanism not published | not stated | unverified | [docs.machines.dev](https://docs.machines.dev/postgres-clusters/Postgres_fork) |
| Heroku Postgres | fork restored from a snapshot | no | **no**, one database per add on and no superuser | [help.heroku.com](https://help.heroku.com/IV1DHMS2/can-i-get-superuser-privileges-or-create-a-superuser-in-heroku-postgres) |
| Nile | none documented | no | unverified | [thenile.dev](https://thenile.dev/docs/support/backup_restore) |
| PlanetScale Postgres | branch created empty, or restored from a backup | no | yes, the default role carries `CREATEDB` | [planetscale.com](https://planetscale.com/docs/postgres/branching) |
| Prisma Postgres | none documented | no | unverified | [prisma.io](https://www.prisma.io/docs/postgres/database/backups) |
| Railway Postgres | none documented | no | yes, the official Postgres image and its superuser | [docs.railway.com](https://docs.railway.com/databases/postgresql) |
| Render Postgres | point in time recovery into a new instance | no | yes, `CREATE DATABASE` in psql is documented | [render.com](https://render.com/docs/postgresql-backups) |
| Tembo Cloud | **the product was withdrawn** | no | no, there is no service | [tembo.io](https://www.tembo.io/) |
| Tiger Cloud, formerly Timescale Cloud | fork restored from a backup on paid tiers, copy on write on free | no | **no**, a service holds exactly one database | [tigerdata.com](https://www.tigerdata.com/docs/use-timescale/latest/fork-services) |
| Xata | **copy on write branch** | **yes** | unverified | [xata.io](https://xata.io/docs/core-concepts/branching) |

Every quote behind those verdicts is in
`engine/internal/db/managed/vendors.go`, with the page and the date it was read.

### Unverified is an answer

Four vendors carry `unverified` above, and it is not a polite no. It means the
vendor's published documentation did not answer the question at the date it was
read. Fly Managed Postgres documents creating additional databases through its
dashboard and `flyctl` and says nothing about whether a SQL role carries
`CREATEDB`. Guessing in either direction would put a number of ticks in this
table that nobody could check.

The engine follows the same rule. A host recognised as a vendor whose
documentation says the grant is unavailable is refused before anything reads
production. A host recognised as a vendor this repository could not verify is
named and not refused.

### Three vendors worth reading twice

**Tembo Cloud no longer sells managed Postgres.** The company pivoted and the
site now sells agent orchestration. There is nothing to point anything at, and
the row is kept rather than deleted because a vendor missing from a list of
thirteen reads as a vendor nobody looked at.

**Xata is the only genuine copy on write branch on this list**, and the only one
of the thirteen whose mechanism earned a provider of its own. It is documented as
a storage level copy on write snapshot that completes in seconds at terabyte
scale, on CloudNativePG and OpenEBS, and the platform is Apache 2.0 and self
hostable. The provider is [`xata`](/docs/providers/xata), it declares
`CopyOnWrite: true`, and what that declaration is worth today is
[below](#what-xatas-copy-on-write-declaration-is-worth-today).

**Prisma Postgres issues a connection string that is not one.** The Console's
default is a `prisma+postgres://accelerate.prisma-data.net/?api_key=...` URL,
which is an HTTP protocol address `pg_dump` cannot speak. Prisma also issues a
direct TCP string on `db.prisma.io`, and its own documentation says to use that
one with `psql`, `pg_dump` and `pg_restore`. Pasting the first into
`database.source_url_env` gets `AF-DB-024`, which correctly says the scheme is
wrong.

## Heroku cannot be recognised, and that is a property of Heroku

A Heroku Postgres host is an EC2 name of the form
`ec2-ADDRESS.compute-1.amazonaws.com`, which is the name every other machine on
EC2 also has. There is no suffix that identifies one without also claiming every
self hosted Postgres running on an EC2 instance, and a wrong recognition is
worse than none: it would attach Heroku's refusal to somebody whose own server
does grant `CREATEDB`.

So Heroku, the vendor with the strongest documented refusal on this list, is the
one the engine cannot warn about from a hostname. Somebody who points
`PGURL_ADMIN_URL` at a Heroku database gets the general `AF-DB-035`, which tells
them to run `ALTER ROLE ... CREATEDB`, and on Heroku there is no role that can.
That limit is stated here rather than left to be discovered.

## Branch time and first golden time

The number this row owes the comparison table, and the cells that are refusals.

Twelve of the thirteen still sell a Postgres, and every one of those twelve can
be a `pgurl` source today, so the branch time that applies to them is `pgurl`'s
on whatever host server is chosen. That is a real number and it was
measured, on a local Postgres 17, by `just benchmark`, which is
`engine/internal/db/pgurl/benchmark_test.go` in this repository. It is **not**
any vendor's number: nothing in this row was timed against a vendor's service.

| Provider or vendor | Time to first golden | Time to branch | Where the number is from |
| --- | --- | --- | --- |
| `pgurl`, small database, 357 MB | 71 seconds, 205 seconds per GB | 52 seconds, 149 seconds per GB | measured, `benchmarks/2026-09-07-1002-pgurl.md` |
| `pgurl`, large database, 1.43 GB | 242 seconds, 169 seconds per GB | 110 seconds, 77 seconds per GB | measured, same report |
| `pgurl`, 100 GB | not measured, and the rate above extrapolates to about four hours forty minutes | not measured, and the rate above extrapolates to about two hours eight minutes | arithmetic on a measured rate, not a measurement |
| Xata, copy on write branch | refused, no account | refused, no account. Documented as seconds at terabyte scale | not measured |
| Tiger Cloud, free tier fork | refused, no account | refused, no account. Documented as 30 to 90 seconds | not measured |
| Tiger Cloud, paid tier fork | refused, no account | refused, no account. Documented as 5 to 20 or more minutes | not measured |
| Heroku fork | refused, no account | refused, no account. Documented as several minutes to several hours, with the dataset | not measured |
| Aiven, Crunchy Bridge, DigitalOcean, Fly, Render forks | refused, no account | refused, no account, and none of the five publishes a figure | not measured |
| Nile, Prisma, Railway | no mechanism to time | no mechanism to time | not applicable |
| Tembo Cloud | the product was withdrawn | the product was withdrawn | not applicable |

Read the per gigabyte rate from the LARGER row. A small database is mostly fixed
cost, so dividing a few seconds by a few megabytes produces a rate that is real
for nothing.

The 100 GB row is arithmetic and it is labelled as arithmetic. Extrapolating a
rate measured at 1.43 GB out by seventy times is a projection, and a projection
printed in the same column as a measurement, with nothing to tell them apart, is
how a figure nobody took gets quoted as one somebody did.

## What Xata's copy on write declaration is worth today

The [`xata`](/docs/providers/xata) provider declares `CopyOnWrite: true`, which
is what Xata's own reference says its branches are. Nothing in this repository
has confirmed it, and the reason is worth writing down rather than leaving as a
gap somebody discovers.

`engine/conformance/cow.go` can falsify a copy on write claim. It builds a small
golden and a large one, times several branches of each, and refuses the
declaration when the larger one costs more than the machine's own noise can
account for. It is two sided, so exactly one of true and false fails on any
measurement whatsoever, which is the property a check needs before anybody
should believe a green one.

That behaviour has not been run against this provider, and it cannot be run
against the harness the provider is tested with. The test is a fake control
plane over a real local Postgres, which proves the provider's logic, its request
shapes and its error mapping, and cannot exhibit copy on write: the only way one
local Postgres can produce a second database holding the first one's data is to
copy the files, and a copy is what the behaviour refuses.

So there were three ways to ship and two of them were refused. Declaring
`CopyOnWrite: false` is green and false about the product. Switching the
behaviour off turns the instrument off for the one capability it exists to
check. What shipped is the truthful declaration with the measurement not made,
said here and in the provider's own source, and a lane is building a third
verdict for the suite so that a harness which structurally cannot exhibit copy
on write can answer UNPROVEN instead of pass or fail.

Running the whole suite against the fake is one command and it is expected to
fail on exactly that behaviour:

```
AF_XATA_FAKE_SUITE=1 go test ./internal/db/xata -run TestConformanceAgainstTheFake -v
```

The thing that settles it is an account. `TestConformance` in the same package
runs the suite against the real service and skips by name without credentials.

## What to do on each of them today

Point `database.source_url_env` at the vendor. It is read once per refresh and
needs read access only, and every one of the twelve that still exists can supply
it.

Point `PGURL_ADMIN_URL` at a Postgres that grants `CREATE DATABASE`. On Aiven,
Crunchy Bridge, DigitalOcean, PlanetScale, Railway and Render that can be the
same service. On Heroku and Tiger Cloud it cannot, and it has to be a server you
administer: a container, a small instance, or the `docker` provider instead.

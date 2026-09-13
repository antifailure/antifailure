---
title: Managed Postgres vendors
description: Which of thirteen managed Postgres products can hold the goldens for pgurl, which cannot, and where each answer was read.
sidebar:
  order: 15
---

The [`pgurl`](/docs/providers/pgurl) provider copies any Postgres it can reach,
and that includes the managed ones. It needs two connection strings, and on a
managed Postgres the question that decides whether a setup works is which of the
two the vendor can be.

This page answers that for thirteen vendors, one by one, from each vendor's own
published documentation.

## What was proved, and what was not

Every verdict here was read from the vendor's own documentation on the date
recorded beside it in `engine/internal/db/managed/vendors.go`. **No account was
created on any of these thirteen services, no request was sent to any of their
control planes, and no database was branched on any of them.** So this page
records what each vendor says its product does. It does not record what any of
them did.

## The two questions that are not the same question

The **source** is production, read once per refresh by `pg_dump`, which needs
read access and nothing else.

The **host server** is where the goldens and the branches are made, and it needs
a role that may `CREATE DATABASE`. The provider's own documentation already says
it should not be the production server.

So a vendor that refuses `CREATE DATABASE` is not a vendor Antifailure cannot
serve. It is a vendor that cannot also be the host server. Keep it as the
source, and make the host server a Postgres you administer: a container, a small
instance, or the `docker` provider instead.

## The thirteen

`CoW` is copy on write: whether the vendor's own copy shares storage with its
parent, so that making one does not take longer as the database grows.

| Vendor | Its own mechanism | CoW | Can host goldens | Read on |
| --- | --- | --- | --- | --- |
| Aiven for PostgreSQL | fork restored from a backup | no | yes, additional databases are supported | [its page](https://aiven.io/docs/products/postgresql/howto/create-database) |
| Crunchy Bridge | fork restored from a backup, point in time | no | yes, the `postgres` role is a superuser | [its page](https://docs.crunchybridge.com/concepts/users) |
| DigitalOcean Managed Databases for PostgreSQL | fork restored from a backup | no | yes, a cluster holds many databases | [its page](https://docs.digitalocean.com/products/databases/postgresql/how-to/manage-users-and-databases/) |
| Fly Managed Postgres | fork, mechanism not published | no | unverified | [its page](https://fly.io/docs/mpg/cluster-configuration/) |
| Heroku Postgres | fork restored from a snapshot | no | **no**, the assigned user may not create or drop databases | [its page](https://devcenter.heroku.com/articles/managing-heroku-postgres-using-cli) |
| Nile | none documented | no | unverified | [its page](https://thenile.dev/docs/api-reference/databases/create-a-database) |
| PlanetScale Postgres | branch created empty, or restored from a backup | no | yes, the default role carries `CREATEDB` | [its page](https://planetscale.com/docs/postgres/connecting/roles) |
| Prisma Postgres | none documented | no | unverified | [its page](https://www.prisma.io/docs/postgres/database) |
| Railway Postgres | none documented | no | yes, the official Postgres image and its superuser | [its page](https://docs.railway.com/databases/build-a-database-service) |
| Render Postgres | point in time recovery into a new instance | no | yes, `CREATE DATABASE` in psql is documented | [its page](https://render.com/docs/postgresql-creating-connecting) |
| Tembo Cloud | the product was withdrawn | no | **no**, there is no service | [its page](https://www.tembo.io/) |
| Tiger Cloud, formerly Timescale Cloud | fork restored from a backup on paid tiers, copy on write on free | no | **no**, a service holds exactly one database | [its page](https://www.tigerdata.com/docs/use-timescale/latest/services/troubleshooting) |
| Xata | copy on write branch | yes | unverified | [its page](https://github.com/xataio/xata) |

The link in the last column is the page the host server answer was read from.
Every quote behind every verdict, and the page for each mechanism, is in
`engine/internal/db/managed/vendors.go`.

### Unverified is an answer

Four vendors carry `unverified`, and it is not a polite no. It means the
vendor's documentation did not answer the question on the date it was read. Fly
Managed Postgres documents creating additional databases through its dashboard
and `flyctl` and says nothing about whether a SQL role carries `CREATEDB`.
Guessing in either direction would put an answer in this table that nobody
could check.

## What the engine does with this

**On a host it recognises as a vendor whose documentation says no**, a role
without `CREATEDB` is refused with `AF-DB-037`. The message names the vendor,
gives the reason, and quotes the page and the date the verdict was read, so a
reader can check whether it has gone stale. It does not tell them to run
`ALTER ROLE`, because on that vendor there is nobody who can.

`af start` gives the same answer without connecting to anything. Its database
rung names the vendor and blocks there, so the answer reaches somebody who has
not finished configuring yet.

**On any other host**, the refusal is `AF-DB-035`. It gives `ALTER ROLE ...
CREATEDB` first, because that is the right answer on a server somebody
administers, which is most of them. It also says what to do when there is no
role that may grant it, because a managed Postgres the engine cannot recognise
still reaches this message.

**Neither refusal is decided by the table.** The provider asks the server
whether its role may create databases, and only a server that says no is
refused. The table decides which sentence describes that refusal. A vendor that
starts granting `CREATEDB` is never refused at all.

## Heroku cannot be recognised from its hostname

A Heroku Postgres host is an EC2 name such as
`ec2-ADDRESS.eu-west-1.compute.amazonaws.com`, which is the name every other
machine on EC2 also carries. No suffix identifies one without also claiming
every self hosted Postgres on an EC2 instance, and a wrong recognition is worse
than none: it would refuse, in Heroku's name, somebody whose own server does
grant `CREATEDB`.

So a Heroku user whose role lacks `CREATEDB` gets `AF-DB-035`, and that is why
`AF-DB-035` carries the second remedy. On Heroku, the second remedy is the one
that works.

## Tiger Cloud is recognised by its service hostname

A Tiger Cloud service is addressed as `SERVICE.PROJECT.tsdb.cloud.timescale.com`.
That suffix is matched on a label boundary, so a host that merely ends in the
same letters is not taken for Tiger Cloud.

## Prisma Postgres issues two connection strings

The Prisma Console's default is a `prisma+postgres://accelerate.prisma-data.net`
URL, an HTTP protocol address that `pg_dump` cannot speak. Prisma also issues a
direct TCP string on `db.prisma.io`, and its own documentation says to use that
one with `psql`, `pg_dump` and `pg_restore`. Pasting the first into
`database.source_url_env` gets `AF-DB-024`, which says the scheme is wrong.

## What to do on each of them

Point `database.source_url_env` at the vendor. It is read once per refresh and
needs read access only.

Point `PGURL_ADMIN_URL` at a Postgres that grants `CREATE DATABASE`. On Aiven,
Crunchy Bridge, DigitalOcean, PlanetScale, Railway and Render that can be a
service at the same vendor. On Heroku and Tiger Cloud it cannot. On Fly, Nile,
Prisma and Xata the documentation did not say, and the server's own answer when
the provider starts is the one that counts.

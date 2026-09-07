---
title: Any Postgres
description: Using any reachable Postgres as the database provider, what it creates on your server, and what branching costs there.
sidebar:
  order: 5
---

Every other provider here is a provider for one product. `pgurl` is the one for
everything else: a self hosted cluster, a machine at Hetzner or Scaleway or
OVH, an internal server behind a bastion, a managed Postgres whose vendor has
no provider in this repository. If `psql` can reach it, this can copy it.

It knows nothing about any vendor. It needs two connection strings and a role
that may create databases.

```yaml
database:
  provider: pgurl
  version: 17
  source_url_env: PRODUCTION_DATABASE_URL   # what is copied
  api_key_env: PGURL_ADMIN_URL              # where the copies live
```

`source_url_env` is the database being copied, which is the same field every
other provider uses. It is read once, during a refresh, and never stored.

`api_key_env` names the variable holding the connection string of the server
the goldens and the branches are kept on. It defaults to `PGURL_ADMIN_URL`. It
is called `api_key_env` because that is the field the manifest schema has for
"the credential this provider needs", and for this provider the whole
connection string is the credential, which is exactly why it is named here and
not written into a file that gets committed.

**That server is not your production server.** This provider creates one
database per golden and one database per environment on it. A spare box, a
second instance beside production, or a container on the machine running `af`
are all fine. Production is not.

There is no `project`. A manifest that sets one for `pgurl` is refused at
validation rather than ignored, because a field that is accepted and never read
is a field somebody writes and believes.

## What it creates on your server

| Name | What it is |
| --- | --- |
| `af_c_<nanoseconds>` | A candidate: the empty database a refresh fills, masks and verifies. Removed whether the refresh succeeds or fails, and any left by a killed run are swept by the next one. |
| `af_g_<version>` | A golden. Marked `IS_TEMPLATE`, with connections refused. |
| `af_b_<environment>` | One environment's branch, made with `CREATE DATABASE ... TEMPLATE`. |

Every one of them carries a JSON marker in its database comment, and that
marker is what the provider reads before it drops anything. A database whose
name matches the scheme and whose comment does not is refused, not adopted and
not deleted: names collide, and a provider that trusted the prefix would
eventually destroy data it never created.

The golden is sealed once it is published, and both halves are load bearing. A
template database cannot be dropped until something unmarks it on purpose, so
a golden cannot go while an environment is still using its copy.
`ALLOW_CONNECTIONS false` stops it drifting from what was verified, and stops
one forgotten `psql` session breaking every branch made after it, because
Postgres refuses to copy a template while a session is connected to it.

## Branch time is not flat, and that is the trade

`CREATE DATABASE ... TEMPLATE` copies files. It is fast, it happens entirely on
the server with nothing crossing the network, and it is proportional to the
size of the database. This provider declares `CopyOnWrite: false` and the
conformance suite holds it to a declared branch latency, so a provider that
gets slower fails rather than degrading quietly.

If flat branch time matters more than running on your own hardware, that is
what [`neon`](/docs/providers/neon) and [`dblab`](/docs/providers/dblab) are
for: both hand out copy on write clones, and a clone of a terabyte costs about
what a clone of a megabyte does.

What that costs, measured rather than described: on an eight core laptop
against a Postgres in a container, a 1.43 GB database took between 55 and 169
seconds per gigabyte for the first golden and between 18 and 77 seconds per
gigabyte to branch. The range is not hedging. It is two runs of the same commit
against the same server twenty one minutes apart, at load averages of 11.8 and
20.1, and the second was three times slower than the first.

Which is the reason the harness ships rather than the figure. The measured
numbers, the machine, the load average and the client tools are all in
`benchmarks/` beside the code that produced them, and `just benchmark` against
your own server gives you the only number that can decide anything.

## The version is the server's

`database.version` is checked against the version the server actually reports,
read at startup rather than taken from the manifest. A golden here is a
database on that server, so there is no other version it could be. A manifest
asking for Postgres 18 against a Postgres 16 server is refused with AF-DB-003
rather than quietly building the golden on 16, because an environment whose
Postgres differs from production is an environment that agrees with production
until the day it does not.

## What it needs, and what it refuses

The role in `PGURL_ADMIN_URL` needs `CREATEDB`. That is checked when the
provider starts, not when the first `CREATE DATABASE` runs, so the refusal
arrives before a refresh has read production rather than after.

| Refusal | When |
| --- | --- |
| AF-DB-034 | The server named by the variable could not be reached. |
| AF-DB-035 | Its role may not create databases. |
| AF-DB-036 | A database with the name it needs exists and this provider did not create it. |
| AF-DB-024 | The variable does not hold a `postgres://` URL. |
| AF-DB-003 | The manifest asks for a Postgres major the server does not run. |

## Boundaries, stated rather than discovered

- **The server must be reachable from wherever your services run**, not only
  from the machine running `af`. A Postgres on your own loopback is reachable
  from `af` and not from inside a service container; give the containers an
  address they can resolve.
- **No pooled endpoint.** A pooler in front of this server is yours to run and
  this provider would be guessing at its address, so it declares
  `PooledEndpoints: false` and services and migrations receive the same
  connection string.
- **Encoding and collation come from the server's own `template1`**, because
  that is what a plain `CREATE DATABASE` inherits. A source database in a
  different encoding is not a case this provider has been shown to handle.
- **One server holds one project's goldens comfortably and several projects'
  uncomfortably.** `max_branches` counts every branch this provider holds on
  that server, not per project.

## Running the conformance suite against your own server

The suite that every provider here runs is the same one, and for this provider
it needs no account and no cloud:

```
AF_PGURL_ADMIN_URL=postgres://... \
  go test ./internal/db/pgurl -run TestConformance -v
```

Twenty three behaviours run and one skips by name, the pooled connection
string, because this provider does not declare pooled endpoints. A skip is
always named: a silent one is how a provider ends up claiming conformance it
does not have.

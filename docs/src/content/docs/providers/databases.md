---
title: Database providers
description: What a database provider is, which ones ship, how to choose, and what every one of them guarantees.
sidebar:
  order: 2
---

A database provider is what creates the copy of production each environment
gets. It is the extension point most repositories care about first, and it is
meant to be written by people outside this repository.

```yaml
database:
  provider: docker   # or neon, supabase, dblab, pgurl, xata, aurora, cloudsql, azurepg, or rds
  version: 17
```

## What ships

| Provider | Where the data lives | Branch time | Needs |
| --- | --- | --- | --- |
| `docker` | A container on the machine running `af` | Flat, because the daemon's storage driver shares layers | A Docker daemon |
| [`neon`](/docs/providers/neon) | A Neon project | Flat, because branches share storage | A Neon project and an API key |
| [`dblab`](/docs/providers/dblab) | A Database Lab Engine you run | Flat, because clones are copy on write | A Database Lab Engine, ZFS, and its verification token |
| [`supabase`](/docs/providers/supabase) | A Supabase branch, which is a whole separate project | Grows with the database, because a Supabase branch is created empty | A Supabase project on a paid plan and an access token |
| [`pgurl`](/docs/providers/pgurl) | A database on any Postgres server you name | Grows with the database, because a branch is a server side file copy | A reachable Postgres and a role that may create databases |
| [`xata`](/docs/providers/xata) | A branch of a Xata project | Expected to be flat, because Xata documents its branches as copy on write snapshots. Never timed on Xata | A Xata project and an API key |
| [`aurora`](/docs/providers/aurora) | A clone of an Amazon Aurora PostgreSQL cluster | Expected to be flat, because a clone shares the source's storage volume. Never timed on AWS | An Aurora PostgreSQL cluster, an IAM role, and the enterprise edition |
| [`cloudsql`](/docs/providers/cloudsql) | A fast clone of a Google Cloud SQL for PostgreSQL instance | Expected to be flat, because a fast clone is created from an Instant Snapshot. Cloud SQL's other clone workflow is not flat, and the provider is built so it cannot ask for that one. Never timed on Google Cloud | A Cloud SQL instance, a service account, and the enterprise edition |
| [`azurepg`](/docs/providers/azurepg) | A point in time restore of an Azure Database for PostgreSQL Flexible Server | Expected to grow with the database. The snapshot half is flat and the log replay half is not, so this provider does not claim copy on write. Never timed on Azure | A flexible server, a service principal, and the enterprise edition |
| [`rds`](/docs/providers/rds) | An instance restored from a snapshot of an Amazon RDS for PostgreSQL instance | Grows with the database, because a restore hydrates a new volume with every byte. One live restore took 5 minutes 4 seconds at 20 GB | An RDS for PostgreSQL instance, an IAM role, and the enterprise edition |

A schema is rarely only Postgres. What the golden's server carries, meaning
PostGIS, pgvector, TimescaleDB, pg_cron, or a table stored in an access method
that came out of an extension, is configured on the `docker` provider and
described in [Extensions and custom storage](/docs/providers/extensions).

`docker` is the default and needs nothing. Its branch time is flat, measured
rather than assumed: the conformance suite branches an 8 MiB golden and a 512 MiB
one and the daemon's storage driver shares the layers, so the two cost the same.
What is not flat is building the golden, because that commits an image. This row
said "Grows with the database" until somebody ran the measurement, which is the
whole argument for having one.

`neon` is the right choice when it is. Neon branches are copy on write, so
creating one takes about as long for a hundred gigabytes as for a hundred rows.

`dblab` is the same property without the account. A Database Lab Engine holds
one full size copy of production on ZFS and hands out thin clones of it, on
your hardware, with nothing leaving your network. The cost is that you run it:
it needs ZFS, a machine large enough to hold production once, and its own data
retrieval configured against your source.

`pgurl` is the one for every Postgres nobody wrote a provider for: a self
hosted cluster, a machine at a host with no API, a managed Postgres whose
vendor is not in this list. It needs no account and no vendor at all, only a
server it may create databases on. Branch time is not flat there, and the
measured seconds per gigabyte are published in `benchmarks/` rather than
described.

`xata` is the managed Postgres whose branching is really branching. Xata
documents a branch as a copy on write storage snapshot that completes in seconds
at terabyte scale, and of thirteen managed vendors it is the only one that does
not restore a backup to make one. That is Xata's claim rather than a
measurement made here, and [the provider page](/docs/providers/xata) says
exactly which half the suite proves.

`supabase` is the right choice when your application already lives there.
Branch time is not flat, because Supabase creates a branch with no data in it
and the golden has to be copied in, but what you get back is a real Supabase
project with the Auth, Storage and Realtime services your application is
calling, which neither of the others can offer. A branch is billed by the hour.

`aurora` is the one for a production that already runs on Aurora PostgreSQL,
and it is in the enterprise edition, because it needs an IAM role somebody in
an organization has to grant. A branch is an Aurora clone. What has been
measured is the provider's half of that: the requests a branch makes are
identical at a one gigabyte volume and at a one terabyte one, and the provider
reads and writes no database content while making them. That is what flat
branch time needs from the code. What it needs from AWS is a clone that is
fast whatever the size, and a writer instance for the preview environment,
because a clone has none. Neither has been timed. Nobody who wrote this
provider has an Aurora account, and its benchmark prints every wall clock cell
as unmeasured rather than guessing one, so the table's "flat" is an
expectation, and the [provider page](/docs/providers/aurora) says the same.

### What is proved, and what is not

The table mixes providers that have answered their real service with one that
has not, so here is the split, in the terms the
[golden stores](/docs/providers/stores) page uses:

- **`docker` and `pgurl` are proved on every pull request**, by the shared
  conformance suite against a real Docker daemon and a real Postgres server.
  For `pgurl` the real server is the whole of the provider's service, so there
  is nothing a fake would be standing in for.
- **`neon`, `supabase` and `dblab` are proved against the real service, by
  hand.** Each needs an account or a Database Lab Engine that CI does not have,
  so the runs that passed were made by a person rather than by a pull request.
- **`aurora` is proved against a fake, and not against AWS.** The same suite
  runs every line of the provider on every pull request, with a fake RDS
  control plane in front of a real Postgres, so the claims about bytes are
  checked against bytes. What it cannot show is that AWS accepts those
  requests, or how long a clone and its writer take, because no test in this
  repository may need a cloud account.
- **`cloudsql` and `azurepg` are proved against fakes, and not against Google
  or Azure.** The same arrangement as `aurora`: every line of each provider
  runs on every pull request, against a fake Cloud SQL Admin API and a fake
  Azure Resource Manager, each with a real Postgres behind it. `cloudsql` has
  never met Google Cloud, because the only Google billing account available is
  closed. `azurepg` has completed one private run against a real flexible
  server on 2026-09-13: a golden restored, masked and verified over `verify-full`, a
  branch written to without the source changing, the goldens listed, and
  everything torn down. One run at one row is a demonstration rather than proof.
- **`rds` is proved against a fake, and once against AWS.** A fake RDS control
  plane with a real Postgres behind it runs every line. One live run on AWS on
  2026-09-14 published a golden over `verify-full` against RDS's own
  certificate, branched it, found the branch held the golden's masked rows and
  nothing written to either side crossed to the other, and tore everything
  down. Four defects no fake could show were found by live runs and each is
  fixed and covered by a test. One run at one size decides no timing, so copy
  on write is recorded as unproven.

`cloudsql` is the one for a production on Google Cloud, and it is in the
enterprise edition for the same reason `aurora` is. A branch is a Cloud SQL
FAST clone, created from an Instant Snapshot, which Google documents as moving
no data whatever the size. That is Google's claim rather than a measurement:
nobody who wrote this provider has run a clone on Google Cloud, so the table's
"flat" is an expectation. The thing to know before choosing it is that Cloud
SQL also has a slower clone whose duration scales with the database, it picks
between the two from the shape of the request rather than from anything you ask
for, and it tells you nothing about which you got. The provider is built so it
cannot ask for the slow one, and its page explains the three conditions that
would have selected it.

`azurepg` is the one for a production on Azure, and it is the only provider here
that does NOT claim flat branch time. A branch is a point in time restore, whose
snapshot half is flat in the size of the data and whose log replay half is not,
so the honest number is one that grows. Microsoft gives the overall recovery as
a few minutes up to a few hours. Its page says why claiming otherwise would be
quoting the fast half of that. One complete run has been timed on Azure, in
`centralus` on a `Standard_B1ms` server with one synthetic row: the golden took
420.3 seconds and the branch 518.3 seconds, the branch including the wait for the
golden's first backup. That is fixed cost at one size, recorded in
[the benchmarks](https://github.com/antifailure/antifailure/tree/main/benchmarks),
so the growth with the size of the database is still Microsoft's description
rather than a number anybody here measured.

`rds` is the one for a production on plain RDS for PostgreSQL, which is where
most Postgres on AWS lives, and it is the slow row of this table on purpose.
RDS has no clone, so a branch is a snapshot restore: RDS provisions an instance
and hydrates a new volume from the snapshot, and the volume is every byte of
the database. It does not claim copy on write and it will not branch from an
Aurora cluster, where `aurora` is the faster answer. What has been measured is
the provider's own half: a branch makes the same control plane calls at twenty
gibibytes and at a tebibyte. One live run timed the first half on AWS, a
snapshot in 1 minute 11 seconds and a restore in 5 minutes 4 seconds at 20 GB,
and the [provider page](/docs/providers/rds) says what that does and does not
show.

A provider named in the manifest and neither built into this binary nor
registered with it is refused at startup rather than substituted. Falling back
to `docker` would hand somebody an empty preview with no reason for it. The
refusal names every provider the build does have, registered ones included, so
a misspelling is answered rather than merely rejected.

A build outside this repository can add its own without forking the engine.
[Writing a provider](/docs/contributing/provider-authoring) has the
registration, which is four lines around `engine/pkg/afcli`.

## What every provider guarantees

These are not documentation. They are a conformance suite that any
implementation runs, so that "conformant" is something a test decides rather
than something a maintainer judges.

- A refresh masks, then verifies, and publishes nothing if verification fails.
- An unverified golden cannot be branched. This is the product's central
  promise and it is enforced in the provider, not in a checklist.
- Branching twice for one environment returns one branch. The engine retries
  after timeouts, and a retry that creates a second resource is how an orphan
  is made.
- Destroying something already destroyed succeeds, because teardown retries.
- A connection string is a secret: it renders as `[redacted]` everywhere text
  is produced.
- Every resource the provider holds can be enumerated, so the leak detector has
  something to compare the journal against.
- A capability a provider does not have is skipped by name in the suite output,
  never silently.

## Direct and pooled connections

A provider may offer a pooled endpoint. Where it does, services receive the
pooled connection string and migrations receive the direct one, because a
transaction pooler does not support the session level features migrations use.
Where it does not, both receive the same string.

Nothing has to be configured for this. The engine asks based on what the
provider declares.

## Writing one

Implement `provider.Database` and run the suite:

```go
func TestMyProvider(t *testing.T) {
    conformance.RunDatabase(t, factory, conformance.Options{})
}
```

Declare only the capabilities you actually have. Declaring one you do not makes
the suite run a behaviour it should have skipped, which fails, which is the
intended outcome: a capability is a promise the suite checks.

Register it under a name this build does not already have. `docker`, `neon`,
`supabase`, `dblab`, `pgurl` and `xata` are reserved, and a registration under one of
them is refused at validation rather than accepted and then never consulted.

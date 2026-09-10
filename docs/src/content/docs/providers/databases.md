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
  provider: docker   # or neon, supabase, dblab, pgurl, or aurora
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
| [`aurora`](/docs/providers/aurora) | A clone of an Amazon Aurora PostgreSQL cluster | Flat, because a clone shares the source's storage volume | An Aurora PostgreSQL cluster, an IAM role, and the enterprise edition |

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

`supabase` is the right choice when your application already lives there.
Branch time is not flat, because Supabase creates a branch with no data in it
and the golden has to be copied in, but what you get back is a real Supabase
project with the Auth, Storage and Realtime services your application is
calling, which neither of the others can offer. A branch is billed by the hour.

`aurora` is the flat one for a production that already runs on Aurora
PostgreSQL, and it is in the enterprise edition, because it needs an IAM role
somebody in an organization has to grant. A branch is an Aurora clone, so
branching moves no data whatever the size. What it does not give you is
seconds: a clone has no instances, a preview environment needs one, and
provisioning a writer takes minutes. That number is flat in the size too, and
the provider page says so before you buy rather than after.

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
`supabase`, `dblab` and `pgurl` are reserved, and a registration under one of
them is refused at validation rather than accepted and then never consulted.

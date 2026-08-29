---
title: Database providers
description: What a provider is, which ones ship, and how to choose.
sidebar:
  order: 1
---

A database provider is what creates the copy of production each environment
gets. It is the main extension point, and it is meant to be written by people
outside this repository.

```yaml
database:
  provider: docker   # or neon
  version: 17
```

## What ships

| Provider | Where the data lives | Branch time | Needs |
| --- | --- | --- | --- |
| `docker` | A container on the machine running `af` | Grows with the database | A Docker daemon |
| [`neon`](/docs/providers/neon/) | A Neon project | Flat, because branches share storage | A Neon project and an API key |
| [`dblab`](/docs/providers/dblab/) | A Database Lab Engine you run | Flat, because clones are copy on write | A Database Lab Engine, ZFS, and its verification token |

`docker` is the default and needs nothing. It is the right choice for a
repository whose database is small enough that copying it is not the slow part.

`neon` is the right choice when it is. Neon branches are copy on write, so
creating one takes about as long for a hundred gigabytes as for a hundred rows.

`dblab` is the same property without the account. A Database Lab Engine holds
one full size copy of production on ZFS and hands out thin clones of it, on
your hardware, with nothing leaving your network. The cost is that you run it:
it needs ZFS, a machine large enough to hold production once, and its own data
retrieval configured against your source.

A provider named in the manifest and not built into this binary is refused at
startup rather than substituted. Falling back to `docker` would hand somebody
an empty preview with no reason for it.

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

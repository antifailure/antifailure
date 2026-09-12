---
title: Xata
description: Copy on write branches of a masked, verified golden, and what has not been measured about them.
sidebar:
  order: 8
---

Xata is a Postgres platform whose branches are copy on write snapshots at the
storage layer. Its own documentation says a child branch copies the parent's
schema and data using a copy on write storage snapshot and completes in seconds
even for terabyte scale databases, on OpenEBS volumes under CloudNativePG.

Of the thirteen managed Postgres vendors on the
[comparison page](/docs/providers/managed-postgres), it is the only one whose
branching is really branching. Every other one calls the operation a fork and
restores a backup, where the clock grows with the data.

```yaml
database:
  provider: xata
  version: 17
  project: my-organization/my-project
  api_key_env: XATA_API_KEY
  source_url_env: PRODUCTION_DATABASE_URL
```

`database.project` is `<organization>/<project>`, both as they appear in the
Xata console. Both are path segments of every call the provider makes and
neither can be discovered from the other, so a manifest with one of them is
refused rather than left to fail at the first refresh with a message about a
path nobody wrote.

`database.api_key_env` names the variable holding an API key with the
`branch:read` and `branch:write` scopes. It is named rather than carried,
because a manifest is committed and a key is not. It defaults to
`XATA_API_KEY`.

## The model

A Xata project holds production on its root branch. A golden is a copy on write
branch of that root, masked and verified in place and then published by a
rename. An environment's database is a copy on write branch of the golden.
Nothing is copied by this provider at any point, which is the reason to choose a
vendor whose branches share storage.

Publishing is the rename and nothing else. The attestation does not exist until
the candidate has been masked and scanned, which is after the branch was
created, so there is no way to create a branch that is already published. A
refresh that fails at any earlier step deletes the candidate rather than leaving
a branchable copy of unmasked production behind.

The attestation, the rules hash and the provenance are written into a
`_antifailure.golden` table inside the golden itself. In the database rather
than beside it, because a verification statement is about that data and should
travel with it: a copy on write branch inherits the row for free, so whoever
holds an environment can read what was scanned and what was found without asking
the engine. It is also the only place it can go. Xata's branch object has one
free text field, `description`, capped at fifty characters, which is enough for
a golden version identifier and nothing else.

## What is declared, and what is not

| Capability | Value | Why |
| --- | --- | --- |
| Branching | yes | copy on write branches are the product |
| Copy on write | yes | Xata's own reference, and see the section below |
| Reset | **no** | Xata publishes no endpoint returning a branch to another branch's state |
| Subsetting | **no** | a candidate already holds the whole database, so a subset could only mean deleting down |
| Pooled endpoints | **no** | the credentials endpoint returns one connection string and documents no pooled variant |
| Provider masking | no | the engine's rules are the single implementation, and masking is a claim where verification is a check |

Reset is refused rather than faked. Implementing it as a delete and a recreate
would hand back a different branch on a different connection string while the
caller's environment still holds the old one, so the capability is declared
false and the conformance suite skips that behaviour naming what is missing
instead of passing it silently.

## What has not been measured

The provider declares copy on write. Nothing in this repository has confirmed
it.

`engine/conformance/cow.go` can falsify the claim: it builds a small golden and
a large one, times several branches of each, and refuses the declaration when
the larger one costs more than the machine's own noise can account for. It is
two sided, so exactly one of true and false fails on any measurement whatsoever.

That behaviour has not been run against this provider. The test in the tree is a
fake control plane over a real local Postgres, which proves the provider's
logic, its request shapes and its error mapping, and cannot exhibit copy on
write: the only way one local Postgres can produce a second database holding the
first one's data is to copy the files, and a copy is what the behaviour refuses.

So this page states the boundary rather than leaving it to be assumed. **The
harness proves the provider's logic, its request shapes and its error mapping.
It does not prove that Xata accepts those requests, and it cannot produce a wall
clock number.** No account was created and no branch was made on Xata.

Running the whole suite against the fake is one command, and it is expected to
fail on exactly that behaviour:

```
AF_XATA_FAKE_SUITE=1 go test ./internal/db/xata -run TestConformanceAgainstTheFake -v
```

What settles it is an account. `TestConformance` in the same package runs the
suite against the real service and skips by name without credentials:

```
AF_XATA_API_KEY=... AF_XATA_ORG=... AF_XATA_PROJECT=... \
  go test ./internal/db/xata -run TestConformance -v
```

That run costs one branch per golden and one per environment, each sharing
storage with its parent, all removed by the suite's own cleanup and checked by
its leak assertion at the end.

## Cleaning up after a killed run

A failing behaviour leaves its branches behind on purpose, so they can be looked
at. Removing them is a separate command rather than something the suite does
for you, because a sweep that ran automatically would destroy the evidence:

```
AF_XATA_SWEEP=1 AF_XATA_API_KEY=... AF_XATA_ORG=... AF_XATA_PROJECT=... \
  go test ./internal/db/xata -run TestSweepLeftovers -v
```

It removes environment branches first and goldens last, because a golden
something came from is refused.

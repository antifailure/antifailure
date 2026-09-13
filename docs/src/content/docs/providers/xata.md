---
title: Xata
description: Copy on write branches of a masked, verified golden on Xata, and what has not been measured about them.
sidebar:
  order: 14
---

Xata is a Postgres platform whose branches are copy on write snapshots at the
storage layer. Its [branching page](https://xata.io/docs/core-concepts/branching)
says a child branch "copies the parent's schema and data using a Copy-on-Write
storage snapshot, so it completes in seconds even for terabyte-scale
databases". Its platform is built on CloudNativePG and is
[open source](https://github.com/xataio/xata) under Apache 2.0.

Of the thirteen vendors on [Managed Postgres vendors](/docs/providers/managed-postgres),
it is the only one whose branching is really branching. Every other one calls
the operation a fork and restores a backup, where the clock grows with the data.

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
refused when it is validated rather than left to fail at the first refresh.

`database.api_key_env` names the variable holding an API key with the
`branch:read`, `branch:write` and `credentials:read` scopes. The third is the
one that returns a branch's connection string. It defaults to `XATA_API_KEY`.

`database.version` has to be the major your project's root branch runs. A
candidate inherits its parent's image, so a refresh asks the candidate's server
which major it is and refuses a mismatch with `AF-DB-003` before anything is
loaded.

## The model

A Xata project holds production on its root branch, the one with no parent. A
golden is a copy on write branch of that root, masked and verified in place and
then published by a rename. An environment's database is a copy on write branch
of the golden. The provider copies nothing itself.

Publishing is the rename and nothing else. The attestation does not exist until
the candidate has been masked and scanned, which is after the branch was
created. A refresh that fails at any earlier step deletes the candidate rather
than leaving a branchable copy of unmasked production behind.

The attestation, the rules hash and the provenance are written into a
`_antifailure.golden` table inside the golden itself. A branch inherits that row,
so whoever holds an environment can read what was scanned and what was found.
Xata's branch object has no annotation map, and its one free text field holds a
golden version identifier and cannot hold an attestation.

## What is declared, and why

- **Branching: yes.** Copy on write branches are the product.
- **Copy on write: yes.** From Xata's branching page. What that declaration is
  worth is the next section.
- **Reset: no.** Xata's API has no call that returns a branch to another
  branch's state. The one restore call it documents creates a new branch from a
  backup. A reset built as a delete and a recreate would hand back a different
  branch on a different connection string.
- **Subsetting: no.** A candidate holds the whole database the moment it
  exists, so a subset could only mean deleting down.
- **Pooled endpoints: no.** Xata does have a pooled endpoint type, selected by a
  hostname suffix. Its credentials call takes no endpoint type and returns one
  connection string, and the provider does not build addresses from a naming
  convention.
- **Provider masking: no.** The engine's rules are the single implementation of
  masking.

A refusal from Xata reaches you with Xata's own code and message. The API
documents a precondition failure on creating a branch without saying which
precondition, so the provider does not guess that it means a branch limit.
`database.max_branches` is the ceiling it enforces itself, with `AF-DB-006`.

## What has not been measured

**No account was used to build this provider, and no branch was made on Xata.**

`engine/internal/db/xata/conformance_test.go` runs the whole conformance suite
on every run against a fake Xata control plane over a real local Postgres. The
fake speaks the paths, fields and status codes of Xata's
[API document](https://api.xata.tech/openapi.json), refuses what that document
refuses, and invents no rule the document does not state. That proves the
provider's logic, its request shapes and its error mapping. It does not prove
that Xata accepts those requests, and it cannot produce a wall clock number.

It also cannot exhibit copy on write. The only way one local Postgres can hand
back a second database holding the first one's data is to copy the files. So
that run asserts no real service, and the copy on write behaviour answers
**unproven** rather than timing a copy. The copy on write ledger records the
same word, and so does the `benchmarks/README.md` table.

The run that settles it is the same suite against the real service:

```
AF_XATA_API_KEY=... AF_XATA_ORG=... AF_XATA_PROJECT=... \
  go test ./engine/internal/db/xata -run TestConformanceAgainstXata -v
```

That run costs one branch per golden and one per environment, each sharing
storage with its parent, all removed by the suite's own cleanup and checked by
its leak assertion at the end.

## Cleaning up after a killed run

A failing behaviour leaves its branches behind on purpose, so they can be looked
at. Removing them is a separate command:

```
AF_XATA_SWEEP=1 AF_XATA_API_KEY=... AF_XATA_ORG=... AF_XATA_PROJECT=... \
  go test ./engine/internal/db/xata -run TestSweepLeftovers -v
```

It removes environment branches first and goldens last, because a golden
something came from is refused.

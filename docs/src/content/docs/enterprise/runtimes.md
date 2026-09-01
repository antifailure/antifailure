---
title: Multiple runtimes
description: Placing an environment on the right pool when there is more than one.
sidebar:
  order: 5
---

*Requires an enterprise license with the `multi_runtime` feature.*

With one runtime there is nothing to decide. With several, an environment has to
go somewhere, and where is a policy question: a region for data residency, a
pool with more memory for a heavy repository, an isolated pool for repositories
that handle regulated data.

```
AF-SCH-001 No runtime satisfies the placement requirement region=eu-west.
  Next: Register a runtime that meets it, or relax the requirement in the
  placement rules.
```

## Why it refuses rather than falls back

Placing an EU repository's environment in a US pool because the EU pool was full
is the kind of helpfulness that ends a compliance audit badly. A requirement
that can be silently ignored is not a requirement.

A run that cannot be placed is queued and reported, with its position, the same
as any other run waiting for capacity.

## Requirements

Attributes, matched against what each registered runtime declares: region,
instance class, isolation level, whatever your organisation decides matters.

The scheduler treats an unsatisfiable requirement as different from a full
queue, because the fixes are different. A full queue resolves itself. An
unsatisfiable requirement never will, and saying "queued" would be a lie that
lasts until somebody investigates.

## The community edition

One runtime, the local one. `runtime.provider` in the manifest names `local`
and `kubernetes`, and only `local` is built; asking for the other is refused
with a message rather than quietly substituted.

Related: [scheduling](/docs/concepts/scheduling/), [licensing](/docs/enterprise/licensing/).

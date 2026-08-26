---
title: Provider limits
description: What happens when a provider runs out of branches, and what to do about it.
sidebar:
  order: 3
---

Every hosted provider has a ceiling on how many databases exist at once, and it
is usually a property of the plan rather than of the software. Reaching it
fails with `AF-DB-006`, naming the limit.

## Why it is configuration

A provider declares its limit through `max_branches`:

```yaml
database:
  provider: neon
  project: dawn-river-12345678
  max_branches: 10
```

It is stated rather than discovered because the API does not report it on a
path worth relying on, and because a limit the engine knows about can be
enforced before a branch is attempted. Failing fast with a number somebody can
act on beats a 422 from a service, and it beats hanging.

The service's own refusal is still translated. If your plan's real ceiling is
lower than what the manifest says, you get `AF-DB-006` either way rather than
an unexplained error from the provider.

## When you hit it

```
AF-DB-006: The provider's concurrent branch limit (10) is reached.
```

Three things to try, in order:

1. **`af env list`**, then **`af down`** on the ones nobody is looking at. A
   pull request that merged last week usually still has an environment. This is
   almost always the answer.
2. **`af env prune --older-than 24h`** to do that in bulk. It prints what it
   would remove before removing it, and refuses to run without a cutoff.
3. **`af golden gc`** if the goldens have accumulated. Every refresh publishes
   a new version and the old ones stay until something collects them. A version
   an environment came from is refused rather than collected, so this cannot
   pull the floor out from under a running environment.
4. **Raise the limit**, in your provider's plan and then in `max_branches`.
   Raising it in the manifest alone moves where the refusal comes from without
   changing when it happens.

## Automatic cleanup

Nothing is deleted on a schedule by default. Environments outlive their pull
requests on purpose: an environment that vanished while somebody was reading it
is worse than one that lingered.

What is cleaned up automatically is the thing nobody can be reading. A golden
candidate is a branch that exists for the minutes between starting a refresh
and publishing it, and nothing ever branches from one. A candidate older than
two hours can only be the remains of a process that died, so the next refresh
removes it.

## Other limits worth knowing

A branch size cap and a history retention window are both common on free tiers
and both bite later than the branch count does. They are the provider's, not
this tool's, and the provider's documentation is where the current numbers are.

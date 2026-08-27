---
title: Scheduling
description: How runs are ordered when there is more work than capacity.
sidebar:
  order: 11
---

A busy repository asks for more environments than there is capacity for. The
scheduler decides what runs now and what waits.

```
AF-SCH-002 The organization is at its concurrent environment limit (10); this
run is queued at position 3.
  Next: Wait, or tear down an environment nobody is using.
```

Position 3 is the useful part. A queue with no position is indistinguishable
from a hang.

## Fair sharing

Capacity is shared between repositories in rounds rather than first come first
served. A repository that opens twenty pull requests in a minute does not take
the whole pool: each repository gets a turn, and one busy project cannot starve
a quiet one.

## Ageing

Fair sharing alone can leave a run waiting indefinitely if new higher priority
work keeps arriving. Every run gains priority with time, and once it has waited
long enough it is promoted ahead of newer work.

The promotion is deliberately one lane at a time rather than to the front. A
run that jumped straight to the top after a delay would make the queue lurch,
and a starvation fix that causes its own unfairness is not a fix.

There is a test for this that runs with ageing disabled as a negative control,
because a starvation test that passes with the mechanism switched off is a test
that was never about the mechanism.

## Priority

A pull request marked ready for review is worth more than a draft, and a
re-run of a branch that already has an environment is worth less than a branch
with none. The scheduler knows both.

## Capacity

`af env list` shows what is held. Tearing down environments for merged pull
requests is the fastest way to shorten a queue, and
`af env prune --older-than 24h` does it in bulk after printing what it would
remove.

Related: [provider limits](/docs/providers/limits/), [the journal](/docs/concepts/journal/).

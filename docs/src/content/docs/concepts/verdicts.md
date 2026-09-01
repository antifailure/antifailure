---
title: Verdicts
description: The six answers a run can give, which of them fail the check, and how to change that.
sidebar:
  order: 16
---

Every run ends in one word. Six are possible, and only one of them fails the
check.

| Verdict | Means | Exit code |
| --- | --- | --- |
| `pass` | Everything asked, nothing found. | 0 |
| `warn` | A real finding about this change that does not stop the merge. | 0 |
| `flaky` | A workflow passed only sometimes. | 0 |
| `blocked` | The runner or the environment could not evaluate something. | 0 |
| `unverified` | A workflow ran and proved nothing either way. | 0 |
| `fail` | A workflow failed, an invariant did not hold, or a finding your policy puts at `fail`. | non zero |

When more than one applies, the run reports the worst: `fail`, then `flaky`,
then `warn`, then `blocked`, then `unverified`. Whichever word wins, the
comment lists every finding worst first, so nothing is hidden by the order.

## Blocked is not a failure

`blocked` is the one worth reading twice. It means a browser did not start, an
environment did not come up, or an invariant could not be asked. That is a fact
about our tooling and not about your change, so it exits zero and the comment
says so in as many words.

This is deliberate. A check that failed a build because our runner could not
start is a check people route around, and a check people route around is a
check that stops finding anything.

## Warn is a real finding

`warn` is the middle level: something true about this change that is not worth
blocking a merge over. A migration that rewrites a table of four hundred rows
is worth a line in the comment and is not worth stopping a release for.

Which findings warn and which fail is yours to set. Nothing about the split is
hardcoded.

## The policy block

```yaml
policy:
  migration_lock:
    warn_ms: 500
    fail_ms: 2000
  migration_failed: fail
  migration_rewrite: warn
  migration_lint: warn
  plan_regression: warn
  query_regression: warn
  load_regression: warn
  egress_surprise: fail
  masking: fail
  cleanup: fail
```

That block is the default written out, so a project that says nothing about
policy gets exactly this. Every key takes `ignore`, `warn` or `fail`.
`ignore` drops the finding entirely: it is not reported and it does not reach
the verdict.

| Key | The finding |
| --- | --- |
| `migration_lock` | How long a migration held a lock on one table. Both figures are milliseconds, compared against a sampled lower bound, so a run that breaches one really did hold the lock at least that long. `fail_ms` must not be below `warn_ms`. |
| `migration_failed` | The migrations did not apply to a branch with production's shape in it. |
| `migration_rewrite` | Postgres reported rewriting a table, which copies every row under a lock nothing can read through. |
| `migration_lint` | Any of the seventeen migration lint rules. The finding names the rule it broke. |
| `plan_regression` | A query plan got worse in one of three plan regressions: a table is now read end to end, an index is no longer used, or the planner's estimate grew. |
| `query_regression` | A statement runs more often, or slower, than the saved baseline did. |
| `load_regression` | A threshold from the `load` block was exceeded. |
| `egress_surprise` | The environment tried to reach a host the manifest does not mention. The request was refused either way; this decides whether the attempt stops the merge. |
| `masking` | The environment's own branch read back with something in it that still parses as real data. |
| `cleanup` | Teardown left a resource behind. |

A level this file does not list is refused when the manifest is read, rather
than quietly treated as the weakest one. A manifest that said `block` and
warned instead would only be found out by a merge that should not have
happened.

Run `af explain` to see the thresholds and the failing classes your manifest
resolves to.

## Exit codes

`af ci` exits zero for every verdict except `fail`. When it does fail, the code
names what failed:

| Code | What failed |
| --- | --- |
| `6` | An unknown destination, with `egress_surprise` at `fail`. |
| `7` | The branch read back with data that still parses as real. |
| `8` | A workflow, an invariant, a migration finding, or a load threshold. |
| `10` | Teardown left resources behind. The journal remembers them; `af down` finishes the job. |

The full list of exit codes is in the [error reference](/docs/reference/errors/).

## Verdicts on one workflow

The six words above are the answer for a whole run. One workflow has five of
its own, from the runner: `pass`, `fail`, `flaky`, `blocked` and `unverified`.
There is no per workflow `warn`, because an agent either carried the workflow
through or it did not.

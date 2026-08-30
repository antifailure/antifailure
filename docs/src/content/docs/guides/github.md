---
title: GitHub
description: An environment per pull request, and the two ways to run it.
sidebar:
  order: 11
---

```yaml
github:
  mode: actions        # or app, or off
  comment: true
  fork_policy: label
  teardown_on: [closed, merged]
```

## Two modes

**`actions`** runs everything inside a workflow. No server, no control plane,
nothing to host. The environment lives for the length of the job, which suits a
repository that wants preview checks and not preview URLs somebody can open
later.

**`app`** uses the GitHub App and the control plane. Environments outlive the
job, so a reviewer can open one, and the control plane holds the scheduling,
quotas, and history. This is what a team wants once more than one person is
reading the results.

**`off`** disables the integration. `af up` still works locally.

## Comments

`comment: true` posts one comment per pull request and edits it in place rather
than adding a new one per push. A bot that adds a comment on every push is a
bot people mute, and a muted bot reports nothing.

The comment carries a headline saying what the run amounted to, the
environment URL, a row per workflow with its verdict and the detail behind it,
steps for reproducing anything that did not pass, and a footer naming the
branch, the commit, the duration and the golden it branched from.

It also carries what the data said. Every invariant the manifest declares is
asked after the workflows, and a violated one puts the violating rows in the
comment and the failure in the headline, so a run where every workflow passed
and the data is broken does not read as a pass. The insights summary is meant
to join it and does not appear yet.

## Forks

```yaml
  fork_policy: label     # none, label, or all
```

A pull request from a fork runs code somebody outside your organisation wrote,
against an environment holding a masked copy of your data with real sandbox
credentials in the proxy.

`label` is the default and the right one: nothing runs until a maintainer adds
the label, which is a person deciding. `none` refuses forks. `all` runs
everything, and is only reasonable for a repository where every contributor
already has write access.

## Teardown

```yaml
  teardown_on: [closed, merged]
```

An environment that outlives its pull request is the leak this product exists
to prevent. Both events are listed because a merged pull request is closed and
a closed one may never be merged.

## Signature verification

```
AF-GH-001 The webhook signature did not verify.
```

Every delivery is verified against the App's secret before anything is read. An
unverified webhook is an unauthenticated request asking for an environment to be
created, so this fails closed and says nothing more: telling a caller why their
forgery failed helps them forge better.

## API failures

```
AF-GH-002 The GitHub API rejected the request: 403 Resource not accessible by
integration.
```

Almost always a permission the App was not granted, or a token from a workflow
with a narrower `permissions:` block than the job needs. The message carries
GitHub's own words, which name the missing scope.

Related: [scheduling](/docs/concepts/scheduling/), [the control plane](/docs/self-hosting/control-plane/).

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
  teardown_on: [close, merge, ttl]
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

## What the App must be granted

[Standing up production](/docs/self-hosting/production/#8-create-the-production-github-app)
carries the permission and event lists, with what each one is for and why the
rest are refused. It is one list rather than two so that they cannot drift.

The one worth knowing here: the console's controls need **Actions: write**, and
declaring it on the App is not the same as holding it. Widening an existing
App's permissions asks every installation to accept the new grant and changes
nothing until somebody does, so the App's settings page can read Actions: write
while every installation of it still refuses a dispatch.

GitHub does not name the state it refuses in, so the console works it out and
says which of these it is:

| What GitHub answers | What it can mean |
| --- | --- |
| `403 Resource not accessible by integration` | The installation holds no Actions write, **or** the App was never given that repository. |
| `404 Not Found` | There is no workflow file of that name on the default branch, **or** no repository of that name this App can see. |
| `422` | The branch does not exist, the workflow declares no `workflow_dispatch` trigger, or it does not declare the inputs the console sends. |

A missing permission is checked before the workflow file is looked for, so a
403 hides whether the file is even there: granting the permission can reveal a
second thing to fix.

## Starting a run from the console

The console's **Create environment**, **Run agents** and **Run load** controls
do not run anything on the control plane. They dispatch a run of your own
workflow, in your own repository, on the branch the environment is on. Your
database, your secrets and your captured traffic stay where they already are.

That needs two things. The App has Actions write, above. And the workflow
accepts a dispatch:

```yaml
on:
  pull_request:
  workflow_dispatch:
    inputs:
      command:     { type: choice, options: [up, agents, load], default: up }
      workflows:   { required: false, default: '' }
      duration:    { required: false, default: '' }
      scale:       { required: false, default: '' }
```

`examples/github-workflow.yml` carries the whole file, including the step that
turns each input into the flag it belongs to. One thing about this costs an
afternoon if you meet it the hard way: GitHub reads the trigger list from the
**default branch**, so adding `workflow_dispatch` on a feature branch alone
changes nothing.

The console checks all of that when you choose a repository, not when you press
the button, and says what is missing in the form. It does not disable the
button: the check can be a few seconds out of date by the width of whatever you
just did on GitHub, and a form that refuses to submit because of a stale read
is worse than one that tries and tells you.

The control plane records nothing about the environment when it dispatches.
The run appears in your Actions tab, and the environment appears in the console
when the engine reports it, the same way it does for a run you started
yourself.

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
and the data is broken does not read as a pass.

And it carries what this change does to the database and the network: the
migrations rehearsed against a branch of the golden, the locks they held, what
Postgres rewrote, the lint findings, the plans that changed, the hosts the
environment reached for, whether the branch read back masked, and what teardown
removed. Each of those is ranked by the manifest's
[policy block](/docs/concepts/verdicts/), worst first, and the ones set to
`fail` are what stop the merge.

## Forks

```yaml
  fork_policy: label     # never, label, or always
```

A pull request from a fork runs code somebody outside your organisation wrote,
against an environment holding a masked copy of your data with real sandbox
credentials in the proxy.

`label` is the default and the right one: nothing runs until a maintainer adds
the label, which is a person deciding. `never` refuses forks. `always` runs
everything, and is only reasonable for a repository where every contributor
already has write access.

## Teardown

```yaml
  teardown_on: [close, merge, ttl]
```

An environment that outlives its pull request is the leak this product exists
to prevent. Close and merge are both listed because a merged pull request is
closed and a closed one may never be merged, and `ttl` bounds the case where
neither happens.

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

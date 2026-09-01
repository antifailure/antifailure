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
GitHub answers a dispatch with 404 whether the App lacks that permission, the
workflow file is missing, or the App was never installed on that repository. A
missing permission looks exactly like a missing file.

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
turns each input into the flag it belongs to. Two things about this cost an
afternoon if you meet them the hard way: GitHub reads the trigger list from the
**default branch**, so adding `workflow_dispatch` on a feature branch alone
changes nothing, and a dispatch to a workflow without it answers 404 rather
than saying what is wrong.

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
the `antifailure:allow` label, which is a person deciding. `never` refuses
forks whatever anybody labels. `always` runs everything, and is only reasonable
for a repository where every contributor already has write access.

### Where it is enforced

`af ci`, `af up`, `af test` and `af load run` all refuse, before an environment
is named and before the Docker daemon is touched. The refusal is `AF-GH-003`,
and `af ci` writes a report saying the check did not run rather than exiting
non zero, because a fork waiting on a maintainer is not a finding about the
change and `never` would otherwise leave every fork pull request permanently
red.

It applies to `pull_request` and to `pull_request_target`. The second one
matters most: it hands the base repository's secrets to a job checking out a
stranger's code, on purpose, which is exactly the configuration this exists
for.

### The policy is read from the base branch

Your manifest is in your repository, so on a fork pull request the checked out
`antifailure.yaml` is the fork's copy. Reading the policy from there would let
anybody add `fork_policy: always` to their own pull request and walk through
the gate, so the policy is read from the base branch instead, which is the only
copy a contributor cannot edit.

Two consequences worth knowing before they surprise you. Changing the policy
takes effect when the change lands on the base branch, not when it is proposed.
And a checkout that does not carry the base branch cannot be read, so the gate
falls back to `label` and says so in the report; the workflow template checks
out with `fetch-depth: 0`, which is also what `af change` needs.

### The workflow has to be woken by the label

Adding a label is an event, and a workflow that does not subscribe to it will
not run again when a maintainer approves. The template lists it:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, labeled, unlabeled]
```

Without `labeled`, the approval is real and nothing acts on it until the next
push.

### What GitHub does on its own, and what it does not

On a GitHub-hosted runner, a `pull_request` job from a fork gets a read-only
token and no secrets. That is real protection and it is not this. It does
nothing on a self-hosted runner, where the Docker daemon, the registry login
and the network are already on the machine, and self-hosted is the ordinary
shape here because an environment needs a daemon and a golden.

With [the control plane](/docs/getting-started/hosted/) there is a second gate
in front of this one, and it is not configurable: a fork's commit is always
held until a maintainer applies the same label, and the approval covers that
commit alone, so the next push withdraws it. The control plane never reads your
manifest, which is why it cannot honour `never` or `always` and applies `label`
behaviour to every repository.

## Teardown

```yaml
  teardown_on: [close, merge, ttl]
```

An environment that outlives its pull request is the leak this product exists
to prevent, which is why this is not actually a choice, and saying so plainly
beats leaving you to find out.

**`teardown_on` is accepted and read by nothing.** Teardown happens whatever
you put here, and there is no combination of these three that turns it off. In
`actions` mode `af ci` tears down before it writes the report, including on a
failed job and including on a cancelled one, and the runner goes away at the
end of the job regardless. In `app` mode the control plane asks for teardown
when the pull request closes or merges, when a newer commit supersedes the run,
and when the check times out, and it cannot honour a setting in your manifest
because it never reads your manifest: see
[the manifest reference](/docs/reference/manifest/#github).

The `ttl` outcome is real and it is configured somewhere else. The ceiling on
how long an environment may live is
[`runtime.max_ttl`](/docs/reference/manifest/), and that one is read.

`af explain` says all of this against the setting, so the manifest and the
command agree.

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

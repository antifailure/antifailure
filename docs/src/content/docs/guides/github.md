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

These four are printed by `af explain` and read by nothing. That is not an
oversight and it is worth knowing before you set one: see
[the manifest reference](/docs/reference/manifest/#github) for what happens
instead, and why the hosted control plane cannot read your manifest.

## Two ways to run it

**Without a control plane.** Everything happens inside the workflow. No server,
nothing to host. `af ci` brings the environment up, runs the agents, writes the
report and tears down, and the workflow's last step posts that report as one
comment which it edits in place. The environment lives for the length of the
job.

**With one.** The workflow does exactly the same work, and then tells the
control plane what happened. The control plane publishes a **check run** the
repository can require, maintains the comment itself, and owns the parts a
workflow cannot do: stopping a run when the pull request closes, noticing a run
that never reported, and keeping the history.

Which one you get is decided by one repository variable, `AF_CONTROL_PLANE`.
Set it to the control plane's address and the two extra steps in the example
workflow run; leave it unset and they are skipped and the workflow comments for
itself. There is no mode to configure and nothing to keep in step.

It is a **variable** on your repository rather than a secret, because it is an
address and not a credential, and it is read by your workflow rather than by the
control plane. Do not confuse it with `AF_CONTROL_PLANE_TOKEN`, which is one
word longer and a different thing entirely: an engine token, for `af` talking to
a control plane from a terminal. Nothing here needs one.

## The check

One check run per commit, named **Antifailure**, so a branch protection rule can
require it. The name is stable on purpose: changing it would silently
un-require the check on every repository that named it.

| The check says | GitHub's conclusion | Merges behind a required check? |
| --- | --- | --- |
| Every check passed | `success` | yes |
| A check failed | `failure` | no |
| Blocked before anything could be checked | `action_required` | no |
| Nothing was verified | `action_required` | no |
| Nothing was verified: the run never reported back | `timed_out` | no |
| Superseded by a newer commit | `cancelled` | no |
| Waiting for a runner / Building the environment | not concluded | not yet |

**Blocked and nothing-was-verified are not passes.** The temptation is GitHub's
`neutral`, which reads as "nothing to say", and `neutral` PASSES a required
check. A pull request whose agents never ran would then merge behind a green
tick, which is the failure this product exists to make impossible: `af test`
exits zero on `unverified`, so a green job means the job exited, not that
anything was checked.

GitHub's conclusion vocabulary is smaller than ours, so two of ours share
`action_required`. They stay apart in the check's title, which is the first line
anybody reads, and in the comment.

## One comment, about one commit

The comment's first line carries the commit it is about. That is not decoration:
somebody pushes while a check is running, the first run is cancelled, the
cancellation finishes after the second run started, and without the fence the
comment ends up reporting a commit that is no longer the head with nothing to
say so. A result that is stale in a way the reader cannot detect is worse than
no result.

So a run whose commit is no longer the head updates its own check, which is
correct because that check belongs to that commit, and does not touch the
comment.

## A fork never reaches a secret

A pull request from a fork runs code somebody outside your organisation wrote.
Two independent things keep it away from your credentials, and neither is
sufficient alone.

GitHub withholds your repository's secrets and the workflow identity token from
a pull request job running on a fork. That is GitHub's rule and it needs nothing
from you.

And the control plane issues no callback credential for a fork's commit until a
maintainer adds the `antifailure:allow` label. **The approval is for that exact
commit.** The next push withdraws it, because a maintainer approved code they
read and the next push is code nobody read. The check on an unapproved fork
commit says so, with the label to add.

**What the approval does and does not buy, said plainly.** GitHub's own rule is
that a `pull_request` job on a fork gets a read-only token, no secrets, and
therefore no workflow identity to exchange, so a fork's own job cannot report a
result to a control plane whatever anybody grants it. The label is what makes
the control plane willing to ACCEPT a result for that commit; the result still
has to come from a run that can prove itself, which means a maintainer starting
one from the console or from the Actions tab against the base repository.

Without a control plane there is nothing for the job to report to, so none of
this arises: the workflow runs on the fork's pull request, `af ci` does its
work, and the comment step posts the report with the `pull-requests: write` the
job already has. The fork still gets no secrets, which is GitHub's doing and not
this product's.

That GitHub rule is documented rather than observed here. Establishing it would
mean opening a fork pull request against this repository, which is a public
action nobody has approved, so it is stated as GitHub's documented behaviour and
not as something this project has watched happen.

## Teardown, and what "torn down" means

An environment that outlives its pull request is the leak this product exists to
prevent, so teardown is asked for when the pull request closes or merges, when a
newer commit supersedes the run, and when a check times out.

**The only route this control plane has into the machine holding your
environment is asking GitHub to cancel the run.** It holds no cluster
credential, no kubeconfig and no address, by design, and `af ci` tears the
environment down on every exit including a cancelled one. So teardown is:
cancel, then come back and check, and it is not finished until GitHub says the
run reached a terminal state.

The console reports the state it is actually in, and none of them is a guess:

| Teardown | What it means |
| --- | --- |
| nothing to remove | no environment was ever reported for this commit |
| asked for | recorded, not confirmed |
| in progress | a cancel has been sent and the run has not stopped yet |
| done | the runtime confirmed it. The environment is gone |
| gave up | there was no route to it. Says so, and names `af down` |

That last row is the honest one. An environment with no live workflow run behind
it is one nothing here can reach, and reporting it torn down would be the same
lie the console used to tell: the button set a column and nothing anywhere read
it, so the page said the environment was gone while the containers kept running.

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

## What the comment carries

One comment per pull request, edited in place rather than added to. A bot that
adds a comment on every push is a bot people mute, and a muted bot reports
nothing.

The comment carries a headline saying what the run amounted to, a link to the
environment in the console, a row per workflow with its verdict and the detail
behind it, steps for reproducing anything that did not pass, and a footer naming
the branch, the commit, the duration and the golden it branched from.

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

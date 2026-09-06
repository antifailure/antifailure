---
title: An environment per pull request
description: The shortest path from a working local environment to one that opens on every pull request.
sidebar:
  order: 2
---

The [quickstart](/docs/getting-started/quickstart) gets an environment running
on your machine. This gets one running on every pull request, reported back on
the pull request itself. Each push builds your services, branches a masked copy
of your production database, runs the agents through your workflows, rehearses
the migrations, and leaves one comment that it edits in place. It needs a
repository on GitHub and nothing else: no account, no control plane, no server
to host, and no secret to create before the first check runs.

## Three ways in, pick one

**Install the GitHub App.** When the App is installed on a repository that has
no workflow, it opens a pull request titled "Check every pull request with
Antifailure" on a branch called `antifailure/setup`. The pull request adds one
file. Merge it, and the next pull request gets a check. Nothing runs until it
is merged, and the console lists the repositories it is still getting
connected. [The pull request the App opens](/docs/guides/github#the-pull-request-the-app-opens)
says what happens when the App cannot write to the repository.

**Run `af init`.** When the checkout has a `github.com` remote, `af init`
writes the same file to `.github/workflows/antifailure.yml` and lists it under
"Written", beside the manifest. It also adds the `github` block to the draft.
A project that already has a manifest gets the file from `af github init`,
which is idempotent and refuses to replace a file that differs unless you pass
`--force`. Both print the secrets that are optional and the one variable the
hosted control plane needs.

**Copy it by hand.** The file is
[`examples/github-workflow.yml`](https://github.com/antifailure/antifailure/blob/main/examples/github-workflow.yml)
in the repository. Copy it to `.github/workflows/antifailure.yml` and commit.

## The file

Whichever door you came through, this is the whole of what lands in your
repository:

```yaml
# Antifailure checks every pull request on a disposable copy of production.
#
# `af init` writes this file for you, and so does installing the GitHub App.
# Copying it to .github/workflows/antifailure.yml by hand works too. The work
# happens in the reusable workflow it calls, so this file rarely needs to change.
#
# https://antifailure.dev/docs/getting-started/pull-requests
name: Antifailure

on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, labeled, unlabeled]
  # Only the hosted control plane uses this. Its buttons run this workflow on
  # the branch an environment is on. Delete it if you do not use one.
  workflow_dispatch:
    inputs:
      command: { type: choice, default: up, options: [up, down, agents, load, scenario, explore], description: "Which part to run" }
      workflows: { description: "Comma separated names out of the manifest. Empty means all of them." }
      duration: { description: "How long to send load for, as a Go duration such as 60s" }
      scale: { description: "Multiplier on production's rate" }
      seed: { description: "Makes two runs do the same thing" }
      concurrency: { description: "Ceiling on requests in flight" }
      run_id: { description: "Leave it empty. The engine asks." }

permissions:
  contents: read
  pull-requests: write
  id-token: write

jobs:
  check:
    uses: antifailure/antifailure/.github/workflows/check.yml@v1
    secrets: inherit
    with:
      dispatch: ${{ toJSON(inputs) }}
      control-plane: ${{ vars.AF_CONTROL_PLANE }}
```

It is short because the work is somewhere else, and where it is matters.

The job calls a **reusable workflow** in the Antifailure repository. That
workflow checks out your branch with full history, because `af change` diffs
against the merge base and a one commit clone has none. It applies the fork
label gate, sets the concurrency group so a push cancels the check it
supersedes, and then calls the action.

The **action**, `antifailure/antifailure@v1`, installs `af`, installs the
agent runner when the command needs a browser, works out what the change
touches, runs the check, and leaves the comment. Its inputs and outputs are on
[the action reference](/docs/reference/action).

`secrets: inherit` is the line that makes the file short. A composite action
cannot read a caller's secrets, so without it every secret would have to be
named in your file, including the production database secret whose name only
your manifest knows. With it the reusable workflow can see your secrets, and
it reads only the ones the manifest names. `af change` reports which those are
before the check starts, and each is looked up by that name and passed to the
action under it. A secret the manifest never mentions is never read.

The `workflow_dispatch` block is for the hosted control plane, whose buttons
run this workflow on the branch an environment is on. Delete it if you do not
use one. The `permissions` block is what the job needs: `pull-requests: write`
for the comment, and `id-token: write` so the job can prove who it is to a
control plane without a stored credential.

## Nothing else is required

No secrets and no account. Open a pull request and the workflow runs, `af
change` reads the diff, and `af ci` brings the environment up, runs the
workflows, asks the invariants, rehearses the migrations, writes the report and
tears down. Teardown happens whatever the outcome, including on a failed job
and on a cancelled one, because an environment that outlives its pull request
is the leak this product exists to prevent.

[`af change`](/docs/concepts/change-analysis) is what keeps the check off a
change to a README. It reads the diff, says which checks exercise what it
touched, and writes that as the comment when nothing else runs. A path it does
not recognise selects every check rather than none, so the mistake it can make
costs a run rather than hiding one.

## What is optional, by name

Each of these is a repository secret, except the last, which is a repository
variable. Each is read only when the manifest asks for it, and each has a real
consequence when it is missing.

`ANTHROPIC_API_KEY` lets the agents read a page. Without one they still run,
and a workflow that needed a page read comes back unverified rather than
guessed at. On a workstation, `af model set anthropic` keeps the key out of
your shell profile; see [your own model key](/docs/guides/model-keys).

`AF_MASKING_KEY` makes masking deterministic across machines, so two goldens
can be compared. Left unset, every runner generates its own.

**The production database secret** has whatever name the manifest's
`database.source_url_env` chooses, such as `PRODUCTION_DATABASE_URL`. Add a
secret of that name and the workflow passes it automatically, because the
action reads the manifest and exports the variable it names. Nothing in the
workflow file changes when the name does. Without it the check runs on an
empty database, and the report says so at the top.

`STRIPE_TEST_SECRET_KEY` is needed only when the manifest sets a host to
`sandbox` mode. The action exports it as `STRIPE_SECRET_KEY`, which is the name
the engine reads. It has to be a test key. A live one is refused before
anything starts.

`AF_CONTROL_PLANE` is a repository **variable**, not a secret, because it is an
address. Set it to a hosted control plane's address and the run reports there,
the control plane publishes a check run and maintains the comment. Leave it
unset and the job comments for itself. [The control plane](/docs/getting-started/hosted)
is what that adds.

## No manifest yet

The check does not wait for one. When the repository has no `antifailure.yaml`,
`af ci` drafts a manifest from the repository, in memory, the same way `af init`
would, and uses that. The comment says so in its first lines: this run used a
manifest Antifailure drafted from the repository, and `af init` committed is
what makes it yours. A repository the draft cannot describe gets a skipped run
and a comment naming the reason, with `af init` as the next command, rather
than a red check for a file that was never there.

## An empty database

When `database.source_url_env` is unset, the report opens with this sentence:

> This ran on an empty database. database.source_url_env names nothing, so the
> migrations built the schema and no production data was masked or branched.
> Set `database.source_url_env: PRODUCTION_DATABASE_URL` and add that secret
> to the repository.

It is rendered before the workflow table on purpose. A check that passed on an
empty schema is a weaker claim than one that passed on a masked copy of
production, and the difference has to be the first thing a reader sees rather
than a footnote. `af up` prints the same sentence on a workstation.

## Turn the integration on

`af init` adds this to the manifest when it writes the workflow. Add it by hand
if you copied the file:

```yaml
github:
  mode: actions
  comment: true
  fork_policy: label
```

Three keys rather than four. There is a `teardown_on` as well, and it is
[read by nothing](/docs/reference/manifest#github): teardown happens whatever
you put there, so setting it would only teach you to trust a line that does not
work.

`mode: actions` runs everything inside the workflow. The environment lives for
the length of the job, which suits a repository that wants preview checks
rather than preview URLs somebody opens later. When you want the second thing,
[the control plane](/docs/getting-started/hosted) is what adds it, and the
mode becomes `app`.

## Open a pull request

Push the branch and open one. The workflow runs and leaves a single comment.
It carries a headline saying what the run amounted to, the environment URL,
and a row per workflow with its verdict and the detail behind it. Below that
sit a collapsible set of steps for reproducing any workflow that did not pass,
and a footer naming the branch, the commit, how long it took and which golden
it branched from.

It also carries what the data said: every
[invariant](/docs/guides/invariants) the manifest declares is asked after the
workflows, and a violated one puts the offending rows in the comment.

And it carries what this change does to the database. The pending migrations
are rehearsed against a throwaway branch of the golden, and the comment names
what they locked and for how long, what Postgres rewrote, and what the
[lint](/docs/concepts/insights) objected to. A lock held past two seconds
fails the check by default; a rewrite warns. The
[policy block](/docs/concepts/verdicts) is where you change that.

It edits that comment in place on the next push rather than adding another. A
bot that comments on every push is a bot people mute, and a muted bot reports
nothing.

## Pull requests from forks

`fork_policy: label` is the default and the right starting point. A pull
request from a fork runs code somebody outside your organisation wrote, against
an environment holding a masked copy of your data. Nothing runs until a
maintainer adds the `antifailure:allow` label, which is a person deciding.
The file subscribes to `labeled` and `unlabeled` so that the approval, and a
withdrawn approval, reach the check without waiting for the next push.

The policy is read from the base branch rather than from the pull request,
because the pull request's copy of the manifest belongs to the contributor.
[Forks](/docs/guides/github#forks) has the full picture, including what GitHub
itself withholds from a fork and what it does not.

Related: [the full GitHub configuration](/docs/guides/github),
[the action reference](/docs/reference/action),
[scheduling](/docs/concepts/scheduling).

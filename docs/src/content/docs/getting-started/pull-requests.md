---
title: An environment per pull request
description: The shortest path from a working local environment to one that opens on every pull request.
sidebar:
  order: 2
---

The [quickstart](/docs/getting-started/quickstart/) gets an environment running
on your machine. This gets one running on every pull request, reported back on
the pull request itself. It needs a repository on GitHub and nothing else: no
account, no control plane, and no server to host.

## Copy the workflow

There is a template in the repository at
[`examples/github-workflow.yml`](https://github.com/antifailure/antifailure/blob/main/examples/github-workflow.yml).
Copy it to `.github/workflows/antifailure.yml`. The whole of it is two commands
and a step that leaves the report:

```yaml
- name: Install Antifailure
  run: curl -fsSL https://antifailure.dev/install.sh | sh

- name: Work out what this change touches
  id: change
  run: af change --write report.md

- name: Run the check
  if: steps.change.outputs.environment == 'true'
  run: af ci --output report.md
```

`af ci` brings the environment up, reads the branch back, runs the agents, asks
the invariants, rehearses the migrations, writes the report, and tears down
afterwards. Teardown happens whatever the outcome, including on a
failed job and including on a cancelled one, because an environment that
outlives its pull request is the leak this product exists to prevent.

[`af change`](/docs/concepts/change-analysis/) is what keeps the second command
off a change to a README. It reads the diff, says which checks exercise what it
touched and which do not, and writes that as the comment when nothing else
runs. A path it does not recognise selects every check rather than none, so the
mistake it can make costs a run rather than hiding one.

The workflow checks out with `fetch-depth: 0`, because the default one commit
deep clone shares no history with the base branch and there is no merge base to
diff against.

Two commands rather than seven is deliberate. A workflow that threads seven
commands together is a file every user edits slightly and gets subtly wrong.

## Turn the integration on

In `antifailure.yaml`:

```yaml
github:
  mode: actions
  comment: true
  fork_policy: label
  teardown_on: [closed, merged]
```

`mode: actions` runs everything inside the workflow. The environment lives for
the length of the job, which suits a repository that wants preview checks
rather than preview URLs somebody opens later. When you want the second thing,
[the control plane](/docs/getting-started/hosted/) is what adds it, and the
mode becomes `app`.

## Open a pull request

Push the branch and open one. The workflow runs and leaves a single comment.
Taken from a real report rather than from the plan, it carries a headline
saying what the run amounted to, the environment URL, a row per workflow with
its verdict and the detail behind it, a collapsible set of steps for
reproducing any workflow that did not pass, and a footer naming the branch, the
commit, how long it took and which golden it branched from.

It also carries what the data said: every
[invariant](/docs/guides/invariants/) the manifest declares is asked after the
workflows, and a violated one puts the offending rows in the comment.

And it carries what this change does to the database. The pending migrations
are rehearsed against a throwaway branch of the golden, and the comment names
what they locked and for how long, what Postgres rewrote, and what the
[lint](/docs/concepts/insights/) objected to. A lock held past two seconds
fails the check by default; a rewrite warns. The
[policy block](/docs/concepts/verdicts/) is where you change that.

It edits that comment in place on the next push rather than adding another. A
bot that comments on every push is a bot people mute, and a muted bot reports
nothing.

## What to set before you need it

Three secrets, all optional, each with a real consequence when it is missing:

`ANTHROPIC_API_KEY` lets the agents read a page. Without one they still run,
and a workflow that needed a page read comes back unverified rather than
guessed at.

`AF_MASKING_KEY` makes masking deterministic across machines, so two goldens
can be compared. Left unset, every runner generates its own.

A test credential, such as `STRIPE_SECRET_KEY`, is needed only when the
manifest sets a host to `sandbox` mode. It has to be a test key. A live one is
refused before anything starts.

## Pull requests from forks

`fork_policy: label` is the default and the right starting point. A pull
request from a fork runs code somebody outside your organisation wrote, against
an environment holding a masked copy of your data. Nothing runs until a
maintainer adds the label, which is a person deciding.

Related: [the full GitHub configuration](/docs/guides/github/),
[scheduling](/docs/concepts/scheduling/).

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
Copy it to `.github/workflows/antifailure.yml`. The whole of it is one command
and a step that leaves the report:

```yaml
- name: Install Antifailure
  run: curl -fsSL https://antifailure.dev/install.sh | sh

- name: Run the check
  run: af ci --output report.md
```

`af ci` brings the environment up, runs the agents, writes the report, and
tears down afterwards. Teardown happens whatever the outcome, including on a
failed job and including on a cancelled one, because an environment that
outlives its pull request is the leak this product exists to prevent.

One command rather than five is deliberate. A workflow that threads five
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

Push the branch and open one. The workflow runs and leaves a single comment
carrying the environment URL where there is one, each workflow's verdict,
anything the invariants found, and the insights summary.

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

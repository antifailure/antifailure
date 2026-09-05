---
title: The GitHub Action
description: Every input and output of antifailure/antifailure@v1, and every input of the reusable workflow that calls it.
sidebar:
  order: 11
---

Two published surfaces run Antifailure inside GitHub Actions. The **action**,
`antifailure/antifailure@v1`, is `action.yml` at the root of the repository. It
installs `af`, works out what the change touches, runs the check, and leaves
the comment. The **reusable workflow**, `.github/workflows/check.yml`, is what
a customer's file calls: it checks out with full history, applies the fork
label gate and the concurrency group, and calls the action with the caller's
secrets. [An environment per pull request](/docs/getting-started/pull-requests)
is the page that gets you a check. This page is what the two files accept.

`v1` is a moving tag that the release workflow points at every final release.
Until the first release after these files landed, `@main` is the reference
that works.

## Inputs of the action

| Input | Default | What it does |
| --- | --- | --- |
| `version` | empty | The release of `af` to install, such as `v1.2.1`. Empty installs the latest release. |
| `command` | `ci` | What to run. `ci` on a pull request. The hosted control plane sends `up`, `down`, `agents`, `load`, `scenario` or `explore` through `dispatch` instead, and that wins when both are set. |
| `dispatch` | `{}` | The caller's `workflow_dispatch` inputs as JSON, which is what `toJSON(inputs)` produces. Empty or `{}` means this is a pull request and the command is `ci`. |
| `control-plane` | empty | Address of a hosted control plane. Empty skips both calls to it, and the job comments for itself. |
| `secrets` | empty | The caller's secrets as JSON, `toJSON(secrets)`, from a reusable workflow. Only the variables the manifest names are exported, by name, after `af change` reports which those are. Leave it empty and pass secrets through `env:` on the step instead. |
| `report` | `report.md` | Where to write the report that becomes the comment. |
| `runner` | `auto` | Whether to install the agent runner, which drives a real browser and needs node. `auto` installs it for `ci`, `agents` and `explore`. `always` and `never` do what they say. |

Every input reaches a script through `env:` rather than through an expression
inside a `run:` block, so an input carrying a quote cannot become a command.

The `secrets` input is how the production database reaches the check without
its name appearing in any workflow file. `af change` writes the variables the
manifest reads, `database.source_url_env` among them, to its step outputs, and
the action exports exactly those out of the JSON. A variable the caller already
set through `env:` is left alone. The JSON is dropped before the engine starts.
One mapping is fixed: a `STRIPE_TEST_SECRET_KEY` in the environment is exported
as `STRIPE_SECRET_KEY` when the latter is unset, because a sandbox rule reads
the second name and the first is the one people create.

## Outputs of the action

| Output | What it carries |
| --- | --- |
| `command` | The command that ran. |
| `environment` | Whether `af change` selected an environment for this change. `true` or `false`. |
| `selected` | The checks `af change` selected, comma separated. |
| `handled` | Whether a control plane took the report. When it is `true` the action leaves no comment, because the control plane maintains one. |

## Inputs of the reusable workflow

The customer's file calls `.github/workflows/check.yml` and passes these. The
workflow forwards each to the action of the same name, and adds the caller's
secrets as `toJSON(secrets)`.

| Input | Default | What it does |
| --- | --- | --- |
| `dispatch` | `{}` | The caller's `workflow_dispatch` inputs as JSON, `toJSON(inputs)`. Empty or `{}` on a pull request, and then the command is `ci`. |
| `control-plane` | empty | Address of a hosted control plane, usually `vars.AF_CONTROL_PLANE`. Empty skips the two calls to it and the job comments for itself. |
| `version` | empty | The Antifailure release to install, such as `v1.2.1`. Empty installs the latest release. |

The workflow has no `secrets` input of its own. `secrets: inherit` in the
caller is what lets it see them, and it is the reason the workflow exists as a
workflow rather than only as an action: a composite action cannot read a
caller's secrets, so every customer would otherwise name each one in their own
file.

## What the reusable workflow decides for you

The job is named `Antifailure`, runs on `ubuntu-latest` with a thirty minute
timeout, and checks out with `fetch-depth: 0`. A `labeled` or `unlabeled` event
for any label other than `antifailure:allow` skips the job. Everything else
runs, including `unlabeled` of the approval label, so a withdrawn approval
reaches `af ci` and is refused there rather than leaving the last result
standing. The concurrency group is one per branch and event, and a push
cancels the check it supersedes on a pull request, but never a dispatch from
the control plane, because "Run agents" must not kill the environment "Create
environment" is building.

## Calling the action directly

Most repositories never write the `uses:` line themselves. Call the action
directly when the job needs something of its own: a service container, a
runner with a particular label, or a step before the check. You then own the
checkout, the permissions and the secrets. This job seeds a Postgres service
container as a stand-in for production, and points the manifest's
`database.source_url_env` at it through `env:`, under the name the manifest
chooses, so the golden is built from data the job controls:

```yaml
jobs:
  antifailure:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
      id-token: write
    services:
      postgres:
        image: postgres:17
        env:
          POSTGRES_PASSWORD: postgres
        ports: ['5432:5432']
        options: >-
          --health-cmd "pg_isready -U postgres"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 10
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0
      - name: Seed the stand-in
        run: psql postgres://postgres:postgres@localhost:5432/postgres -f fixtures/production-sample.sql
      - uses: antifailure/antifailure@v1
        with:
          version: v1.2.1
        env:
          PRODUCTION_DATABASE_URL: postgres://postgres:postgres@localhost:5432/postgres
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

Three things are on you in this shape that the reusable workflow otherwise
carries. The checkout must be `fetch-depth: 0`, or `af change` has no merge
base. The `secrets` input is empty, so each secret is named under `env:` and
only those are visible. And the fork label gate in the reusable workflow's
`if:` is absent, though the engine's own gate still refuses an unapproved fork
before it names an environment, which [Forks](/docs/guides/github#forks)
describes.

Related: [An environment per pull request](/docs/getting-started/pull-requests),
[GitHub](/docs/guides/github#the-reusable-workflow-and-the-action),
[the CLI reference](/docs/reference/cli).

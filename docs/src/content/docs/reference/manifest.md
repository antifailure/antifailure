---
title: Manifest reference
description: Every block in antifailure.yaml, what it does, and what happens when it is wrong.
sidebar:
  order: 1
---

`antifailure.yaml` sits at the repository root. `af init` writes one from what
is already in the repository; nothing regenerates it afterwards, so an edit you
make survives.

The rule worth knowing before reading anything else: an environment can reach
nothing on the network except the hosts listed under `egress`, each in the mode
named. Everything else is refused with a decision you can read.

## Top level

| Key | Type | What it is |
| --- | --- | --- |
| `version` | int | Schema version. `1` today. |
| `name` | string | The project. Used in environment identifiers. |
| `services` | list | What runs. |
| `database` | block | Where the Postgres comes from. |
| `egress` | block | What the environment may reach. |
| `personas` | list | Users the agents sign in as. |
| `workflows` | list | What the agents do. |
| `invariants` | list | Statements about the data that must stay true. |
| `insights` | block | The Postgres native checks. |
| `change` | block | Path rules for [change analysis](/docs/concepts/change-analysis/), for a layout the built in rules do not predict. |
| `fidelity` | block | The component inventory of what this environment reproduces. |
| `load` | block | Production shaped traffic. |
| `policy` | block | What each class of finding does to the check. |
| `runtime` | block | Where and how long environments run. |
| `github` | block | The pull request integration. |

## `services`

| Key | Type | Notes |
| --- | --- | --- |
| `name` | string | Required. |
| `kind` | string | `web`, `worker`, or `cron`. A `web` service gets a URL. |
| `path` | string | Directory, for a monorepo. |
| `command` | string | How to start it. |
| `port` | int | What it listens on. `PORT` is set for you. |
| `health_path` | string | Readiness check, default `/`. |
| `health_timeout` | duration | Default `180s`. |
| `migrate` | string | Runs to completion before the service starts, with an elevated connection. See below. |
| `schedule` | cron | For `kind: cron`. |
| `replicas` | int | Default 1. |
| `depends_on` | list | Other services that must start first. |
| `env` | list | Variables this service needs, by name. |
| `resources` | block | `cpu` and `memory`. |
| `build` | block | See below. |

### What a service is given

Every container the engine starts receives these, whether or not the manifest
mentions them.

| Variable | In the service | In its `migrate` command |
| --- | --- | --- |
| `DATABASE_URL` | The unprivileged connection the application uses. Pooled where the provider has a pool. |  The elevated connection, which may run DDL and is never pooled, because a transaction pooler does not support what a migration needs. |
| `PORT`, `HOST` | The port from the manifest, bound to `0.0.0.0`. | Not set. |
| `AF_ENV_ID` | The environment's identifier. | The same. |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | The egress sidecar, and the addresses inside the environment that must not go through it. | The same. |

The row that surprises people is the first one. `DATABASE_URL` is one name for
two different connections, and which one a container gets depends on whether it
is the service or the service's migration. That is deliberate: a migration
needs privileges the application must not have, and giving the application a
second variable it should never read is a worse answer than giving each
container exactly the connection it is allowed to use.

The consequence for an image author: a migration entry point should read
`DATABASE_URL` and expect to be able to run DDL with it. An image built for a
deployment that names two connections explicitly needs to accept
`DATABASE_URL` as well, or it cannot run inside a preview at all.

`AF_ENV_ID` is set only for a container the engine created. A script that seeds
accounts, or does anything else that would be dangerous against production, can
refuse to run when it is absent.

```
AF-RUN-042 Service web depends on cache, which the manifest does not declare.
AF-RUN-041 The services depend on each other in a cycle: web -> worker -> web
```

A cycle has no order that can start, so it is refused rather than resolved
arbitrarily.

### `build`

| Key | Notes |
| --- | --- |
| `strategy` | `auto` (default), `dockerfile`, `buildpack`, or `image`. |
| `dockerfile` | Path, when it is not `./Dockerfile`. |
| `context` | Build context directory, relative to the repository root. Defaults to the root, so a service can copy from a shared package. |
| `target` | A stage in a multi stage Dockerfile. |
| `image` | A prebuilt image, instead of building. |
| `args` | Build arguments. |
| `allow_hosts` | What the build needs to reach, recorded and not enforced in this release. |

### `env`

```yaml
    env:
      - name: STRIPE_SECRET_KEY
        sandbox: true
      - name: LOG_LEVEL
        value: debug
      - name: API_URL
        from: web
```

A name, never a secret. `sandbox: true` marks a variable that must hold a
sandbox credential and never a live one, which is checked before anything
starts. `from` takes the value from another service's URL, so a worker can be
told where the web service is without hardcoding a port.

A service receives what it declares and nothing else. The engine's own
environment is not passed through, or a preview would inherit whatever is
exported on the laptop that started it.

## `database`

| Key | Notes |
| --- | --- |
| `provider` | `docker` (default), `neon`, `supabase`, or `dblab`. |
| `version` | Postgres major, default 17. |
| `url_env` | The variable services receive the connection string in. |
| `source_url_env` | Names the variable holding production's read only URL. |
| `masking_rules` | Path to the rules, default `masking.yaml`. |
| `seed` | A command run against a fresh golden candidate. |
| `project` | For a hosted provider, its project identifier. |
| `api_key_env` | Names the variable holding that provider's API key. |
| `max_branches` | The plan's concurrent branch limit. |
| `golden` | `schedule`, `max_age`, `retain`, `storage`, `storage_url`. |
| `subset` | See below. |

### `subset`

```yaml
  subset:
    enabled: true
    seed_table: organizations
    seed_where: "created_at > now() - interval '90 days'"
    max_rows: 100000
    follow_dependents: 2
    virtual_relationships:
      - from: events.actor_id
        to: users.id
```

A production shaped slice rather than the whole database. `virtual_relationships`
is for joins your schema does not declare as foreign keys, which are the ones a
subset silently breaks.

## `egress`

| Key | Notes |
| --- | --- |
| `default` | Any mode: `block` (default), `allow`, `capture`, `mock`, `sandbox` or `synth`. |
| `allow_ipv6` | Off by default. |
| `rules` | See [egress](/docs/concepts/egress/). |

## `policy`

Which findings fail the check, which only warn, and which are dropped. Every
key takes `ignore`, `warn` or `fail`, and a value outside those three is
refused at the line rather than treated as the weakest one.

| Key | Default | The finding |
| --- | --- | --- |
| `migration_lock.warn_ms` | `500` | Report a lock held at least this long. |
| `migration_lock.fail_ms` | `2000` | Fail on a lock held at least this long. Must not be below `warn_ms`. |
| `migration_failed` | `fail` | The migrations did not apply to a branch of the golden. |
| `migration_rewrite` | `warn` | Postgres rewrote a table. |
| `migration_lint` | `warn` | Any of the seventeen migration lint rules. |
| `plan_regression` | `warn` | A query plan got worse. |
| `query_regression` | `warn` | A statement runs more often or slower than the baseline. |
| `load_regression` | `warn` | A threshold from the `load` block was exceeded. |
| `egress_surprise` | `fail` | The environment reached for a host the manifest does not mention. |
| `masking` | `fail` | The branch read back with data that still parses as real. |
| `cleanup` | `fail` | Teardown left a resource behind. |

See [verdicts](/docs/concepts/verdicts/) for what each level does to the run
and to the exit code.
## `fidelity`

| Key | Notes |
| --- | --- |
| `enabled` | On by default. Turning it off means the inventory is not taken, which is not the same as everything having passed. |
| `require` | Dimensions every component of which must be reproduced: `services`, `database`, `third_party`, `auth`, `runtime`, `traffic`. See [inventory](/docs/concepts/inventory/). |

There is no threshold here. A single percentage hides the one dimension that
matters to a particular change, so what a manifest requires is a dimension by
name.

## `runtime`

| Key | Notes |
| --- | --- |
| `provider` | `local`. `kubernetes` is named in the schema and not built yet; asking for it is refused rather than substituted. |
| `ttl` | How long an environment lives. |
| `idle_sleep` | Suspend after this long with no traffic. |
| `domain` | Wildcard domain for preview URLs. |

## `github`

| Key | Read by | Notes |
| --- | --- | --- |
| `mode` | `af explain` only | `actions`, `app` or `off`. Which half does the work is decided by your workflow, which has the address of a control plane or does not. |
| `comment` | `af change`, `af ci` | Whether to maintain one comment on the pull request. |
| `fork_policy` | `af ci`, `af up`, `af test`, `af load run` | `never`, `label` or `always`. Read from the BASE branch, not from the pull request. |
| `teardown_on` | `af explain` only | Accepted and read by nothing. Teardown is unconditional. |

**`fork_policy`** is enforced in the engine, before an environment is named and
before the Docker daemon is touched, on `pull_request` and on
`pull_request_target`. The policy is read from the base branch rather than from
the checked out tree, because the manifest is a file in your repository and a
fork's pull request carries its own copy of it: reading the setting from there
would let anybody lift their own restriction. A checkout that does not carry
the base branch falls back to `label` and says so. See
[Forks](/docs/guides/github/#forks).

The control plane applies `label` behaviour to every repository regardless of
what this says, and cannot do otherwise, for the reason two paragraphs down. Its
approval covers that exact commit: the next push withdraws it.

**`comment: false`** makes `af change` and `af ci` write `comment=false` to
`GITHUB_OUTPUT`, and the workflow's comment step is gated on it. The report
files are still written. That is the distinction the setting draws: do not
comment, not do not produce a report. The same `report.md` is the job summary
and the payload a control plane is sent, and a publish step that reads a file
somebody deleted fails rather than skipping. Outside GitHub Actions there is no
pull request for the setting to be about and nothing changes. Which half writes
the comment is still not this setting's business: with a control plane it
maintains one and the workflow's own step stands down, and without one the
workflow comments for itself.

**`mode` and `teardown_on` are read by `af explain` only**, and that is worth
being blunt about rather than leaving somebody to find out by setting one.
Removing `close` from `teardown_on` does not stop a closed pull request being
torn down, and no combination of its values turns teardown off. Teardown is
always asked for when the pull request closes or merges, when a newer commit
supersedes the run, and when the check times out, because a run that is stopping
leaks its environment if nothing cleans up after it. `af ci` tears down before
it writes the report, whatever the outcome, including on a cancelled job. The
`ttl` outcome is real and comes from a different key,
[`runtime.max_ttl`](#runtime).

The reason those two are inert is architectural rather than an oversight, and it
is the sentence the whole product rests on: **the hosted control plane never
reads your manifest.** The manifest lives in your repository beside your code,
and the control plane holds organizations, policy and aggregated reports. A
control plane that read the manifest would be a control plane that had to fetch
your repository, which is the boundary this product exists to keep. Anything in
this block that only a control plane could act on is therefore not acted on.

A test in `internal/manifest` fails if one of these fields gains a reader
without this table being updated, and if a new field is added to the block
without being classified, so this list cannot go quietly out of date.

## When the manifest is wrong

```
AF-MAN-001 No antifailure.yaml was found in /path or any parent directory.
AF-MAN-002 The manifest at ./antifailure.yaml is not valid: services[0].port
must be between 1 and 65535
AF-MAN-003 The manifest declares schema version 2, which this build does not
understand.
AF-MAN-005 The manifest is larger than the 256 KiB limit.
AF-MAN-006 The path ../secrets in the manifest resolves outside the repository.
```

The schema refuses a key it does not know, so a typo is an error at the line
rather than a setting that silently does nothing.

`af doctor` validates without running anything, which is the fast way to check
an edit.

## The JSON Schema

`schemas/manifest.v1.json` is the source of truth, and the Go types mirror it. A
test validates real manifests against both, so a field in one and not the other
fails the build. Point your editor at it for completion and inline errors.

Related: [detection](/docs/concepts/detection/), [egress](/docs/concepts/egress/),
[providers](/docs/providers/overview/).

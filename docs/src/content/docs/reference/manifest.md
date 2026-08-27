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
| `load` | block | Production shaped traffic. |
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
| `migrate` | string | Runs before the service, with a direct connection. |
| `schedule` | cron | For `kind: cron`. |
| `replicas` | int | Default 1. |
| `depends_on` | list | Other services that must start first. |
| `env` | list | Variables this service needs, by name. |
| `resources` | block | `cpu` and `memory`. |
| `build` | block | See below. |

```
AF-RUN-042 Service web depends on cache, which the manifest does not declare.
AF-RUN-041 The services depend on each other in a cycle: web -> worker -> web
```

A cycle has no order that can start, so it is refused rather than resolved
arbitrarily.

### `build`

| Key | Notes |
| --- | --- |
| `strategy` | `auto` (default), `dockerfile`, or `buildpack`. |
| `dockerfile` | Path, when it is not `./Dockerfile`. |
| `context` | Build context, default the service's directory. |
| `target` | A stage in a multi stage Dockerfile. |
| `image` | A prebuilt image, instead of building. |
| `args` | Build arguments. |
| `allow_hosts` | Hosts the build may reach. The build is sandboxed too. |

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
| `provider` | `docker` (default) or `neon`. |
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
| `default` | `block` (default) or `allow`. |
| `allow_ipv6` | Off by default. |
| `rules` | See [egress](/docs/concepts/egress/). |

## `runtime`

| Key | Notes |
| --- | --- |
| `provider` | `local`. `kubernetes` is named in the schema and not built yet; asking for it is refused rather than substituted. |
| `ttl` | How long an environment lives. |
| `idle_sleep` | Suspend after this long with no traffic. |
| `domain` | Wildcard domain for preview URLs. |

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

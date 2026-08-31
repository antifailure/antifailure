---
title: "Antifailure manifest schema"
description: "The file antifailure.yaml at the root of a repository."
---

The file antifailure.yaml at the root of a repository. It describes what to build, where the database comes from, what the environment may reach on the network, who the agents log in as, and what they do. It is the whole configuration surface: nothing about an environment is configured anywhere else.

:::note
This page is generated from `schemas/manifest.v1.json`. Edit the schema, then run `just generate`.
:::

## The document

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `auth` | [auth](#auth) | no | How personas come to exist. |
| `change` | [Change](#change) | no | How a pull request's diff is classified. |
| `database` | [Database](#database) | no | Where the environment's Postgres comes from, and how the production copy is made safe before anyone can branch from it. |
| `egress` | [Egress](#egress) | no | What the environment may reach on the network. |
| `github` | [GitHub](#github) | no | How Antifailure appears on a pull request: what runs it, whether it comments, what it does with forks, and when it tears the environment down. |
| `insights` | [Insights](#insights) | no | The Postgres native checks that turn a preview environment into a database review. |
| `invariants` | list of [Invariant](#invariant) | no | Read only statements that must hold after every workflow. They are the assertions a test cannot make from the outside: no orphaned rows, no negative balances, no subscription without a customer. Max items 100. |
| `load` | [Load](#load) | no | Traffic shaped like production, compared between the base branch and this one. |
| `name` | string | no | A short name for this application, used in environment hostnames and in the control plane. Defaults to the repository directory name. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `personas` | list of [Persona](#persona) | no | The accounts agents log in as. Each is created or reconciled in the golden by the authentication adapter, so a persona is a real user of the application rather than a bypass. Max items 50. |
| `runtime` | [Runtime](#runtime) | no | Where and how long the environment runs. |
| `services` | list of [Service](#service) | no | Every process the environment runs: web servers, API servers, background workers, and scheduled jobs. Min items 1, max items 50. |
| `version` | `1` | **yes** | The manifest schema version. Increment only for a breaking change; the engine refuses a version it does not understand rather than guessing. |
| `workflows` | list of [Workflow](#workflow) | no | What the agents do, written as sentences. A workflow is a goal, not a script: the runner decides the actions and verifies the outcome. Max items 200. |

## auth

How personas come to exist. Absent from most manifests, because detection answers it; present when detection is wrong, when the users table has names nothing could guess, or when the application's users live somewhere only a script can reach.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `adapter` | `auto`, `direct`, `supabase`, `supabase_api`, `nextauth`, `clerk`, `auth0`, `workos`, `seed` | no | Which authentication scheme personas are created in. auto picks it from the dependency list and the live schema. Defaults to `auto`. |
| `connection` | string | no | The Auth0 database connection users are created in. Defaults to Username-Password-Authentication. Max length 128. |
| `domain` | string | no | The tenant, for Auth0, for example dev-abc123.us.auth0.com. Max length 253. |
| `password` | [password rules](#password-rules) | no | The application's password policy, so the generated password satisfies it. |
| `sandbox` | boolean | no | That the configured tenant is a sandbox, development or staging tenant rather than the production one. A hosted adapter refuses to create anybody without this, because the only tenant it could otherwise fall back to is the real one. Defaults to `false`. |
| `seed` | string | no | The command the seed adapter runs, once per persona, with the persona in the environment as AF_PERSONA_NAME, AF_PERSONA_EMAIL, AF_PERSONA_PASSWORD, AF_PERSONA_TOTP_SECRET, AF_PERSONA_ROLE, AF_PERSONA_LOGIN and AF_PERSONA_ATTRIBUTES. It must be idempotent, because it runs again on every branch. Max length 2000. |
| `sessions` | list of string | no | Extra tables holding sessions or tokens, emptied so that no real session survives into a branch. Masking does not touch them, because a session token is not personal data by any rule a scanner applies. Max items 50. |
| `table` | [auth table](#auth-table) | no | The columns of an application's own users table, for the direct adapter. |
| `token_env` | string | no | The variable holding the provider's admin credential. The variable name, never the credential. Max length 128. |
| `url` | string | no | The project's API root, for Supabase. Max length 2048. |

## auth table

The columns of an application's own users table, for the direct adapter. Named rather than guessed, because guessing a column name is how provisioning writes a row the application cannot read.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `attributes` | object | no | Maps a persona attribute name to the column it is stored in. Max properties 50. |
| `email` | string | no | Defaults to `email`. Max length 63. |
| `id` | string | no | Defaults to `id`. Max length 63. |
| `json` | string | no | A JSONB column that persona attributes with no column of their own are written into. Max length 63. |
| `name` | string | **yes** | Max length 63. |
| `password` | string | no | The column the bcrypt hash goes in. Absent for a table that keeps no password. Max length 63. |
| `role` | string | no | Max length 63. |
| `schema` | string | no | Defaults to `public`. Max length 63. |
| `timestamps` | list of string | no | Columns set to now() on insert, and on update where the name contains 'updated'. Max items 10. |

## Build

How to turn the service directory into an image. Omitted means detect: a Dockerfile if there is one, otherwise a buildpack.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `allow_hosts` | list of string | no | Hosts the build is declared to reach, such as a package registry or an engine download. DECLARED RATHER THAN ENFORCED in this release: the list is validated and shown by af explain, and the local builder does not yet seal a build or apply it. Write it as the record of what your build needs, and do not rely on it as a control. Max items 50. |
| `args` | object | no | Build arguments. Never secrets: build arguments are recorded in image metadata and are visible to anyone who can pull the image. Secrets are mounted, and the linter rejects a secret shaped argument. Max properties 50. |
| `context` | string | no | Build context directory, relative to the repository root. Defaults to the repository root so that a service can copy from a shared package. Max length 512. |
| `dockerfile` | string | no | Path to the Dockerfile, relative to the repository root. Max length 512. |
| `image` | string | no | A prebuilt image reference, used with the image strategy. Pinned by digest is strongly preferred. Max length 512. |
| `strategy` | `auto`, `dockerfile`, `buildpack`, `image` | no | Defaults to `auto`. |
| `target` | string | no | Stage to build in a multi stage Dockerfile. Max length 128. |

## Change

How a pull request's diff is classified. The built in rules cover the layouts most projects use; these are for the ones they do not. A rule says what a path is, never which checks to run: an unrecognised path always selects every check, and no rule here can take a check away.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `rules` | list of [Change rule](#change-rule) | no | Path patterns this repository wants classified its own way. The longest matching pattern wins, so order does not decide. Max items 100. |

## Change rule

One path pattern and what the paths it matches are. It says what a file IS, never which checks to run.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `note` | string | no | The sentence the report prints for this rule, replacing the default one that restates the pattern. Max length 200. |
| `path` | string | **yes** | A glob against the repository relative path. A single star does not cross a slash and a double star does. A pattern that matches everything is refused, because it would defeat the rule that an unrecognised path selects every check. Min length 1, max length 256. |
| `surface` | `schema`, `code`, `asset`, `build`, `dependency`, `config`, `infrastructure`, `pipeline`, `test`, `docs` | **yes** | What the matched paths are. Surfaces the engine assigns from the manifest itself, such as a service or the masking rules file, cannot be set here. |

## Database

Where the environment's Postgres comes from, and how the production copy is made safe before anyone can branch from it.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `api_key_env` | string | no | The name of the variable holding the provider's API key. Named rather than carried: a manifest is committed and a key is not. Defaults to NEON_API_KEY for the neon provider. |
| `golden` | [Golden](#golden) | no | The masked, verified copy every environment branches from. |
| `masking_rules` | string | no | Path to the masking rules file, relative to the repository root. Defaults to `masking.yaml`. Max length 512. |
| `max_branches` | integer | no | The plan's concurrent branch limit, where the provider has one it cannot read from its own API. Reaching it fails with AF-DB-006 rather than hanging. Minimum 1. |
| `project` | string | no | The account-side project a hosted provider creates branches in, such as a Neon project. Not a secret, which is why it lives here and the key that reaches it does not. |
| `provider` | `docker`, `neon`, `supabase`, `dblab` | no | Which provider creates branches. docker is local and needs nothing; neon, supabase, and dblab talk to a service. Defaults to `docker`. |
| `seed` | string | no | Command that seeds a branch, for a project with no production database yet. Mutually exclusive with source_url_env. Max length 1024. |
| `source_url_env` | string | no | Name of the environment variable holding the read only connection string of the production database. The value is read once, during a golden refresh, on the operator's machine or runner, and never stored. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `subset` | [Subset](#subset) | no | Take a production shaped slice rather than the whole database. |
| `url_env` | string | no | Name of the environment variable to inject into services with the branch's connection string. Defaults to `DATABASE_URL`. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `version` | `14`, `15`, `16`, `17` | no | Postgres major version. Must match the source, because a dump does not restore across majors. Defaults to `17`. |

## Egress

What the environment may reach on the network. Everything leaves through the sidecar, and everything not named here is blocked.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `allow_ipv6` | boolean | no | Whether the environment may open IPv6 connections. Off by default, because an IPv6 path that bypasses the proxy is the most common way an egress control is silently defeated. Defaults to `false`. |
| `default` | `block`, `allow`, `capture`, `mock`, `sandbox`, `synth` | no | What happens to a host with no rule. Changing this away from block is a deliberate act with a real cost: it is how a preview environment emails a real customer. Defaults to `block`. |
| `rules` | list of [Egress rule](#egress-rule) | no | What the environment may do with one host. Max items 500. |

## Egress rule

What the environment may do with one host. A rule is per host because that is the unit a person can reason about: allowed, blocked, answered from a fixture, or sent to the provider's own sandbox.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `credential` | string | no | Name of the environment variable holding the sandbox credential for this host. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `fixtures` | string | no | Path to a fixture pack or an OpenAPI document for mock mode, relative to the repository root. Max length 512. |
| `host` | string | **yes** | Host to match. A leading *. matches one or more labels. An IP literal matches only itself. Max length 253. |
| `methods` | list of string | no | Restrict the rule to these HTTP methods. Max items 10. |
| `mode` | `block`, `allow`, `capture`, `mock`, `sandbox`, `synth` | **yes** | block refuses with a readable decision. allow passes through with a rate limit. sandbox substitutes test credentials and forwards to the provider's sandbox. capture records the message into the inbox and returns the provider's success shape. mock answers from a fixture or an offline pack. synth asks a model to invent a response and marks every result that touched it as unverified. |
| `note` | string | no | Why this rule exists. Rendered in the network policy view, because a rule nobody can explain is a rule nobody dares remove. Max length 512. |
| `paths` | list of string | no | Restrict the rule to these path prefixes. Anything else on the same host falls through to the next rule. Max items 100. |
| `rate_limit` | string | no | Token bucket rate, for example 10/s or 600/m. Applies to allow and sandbox. Matches `^[0-9]+/(s\|m\|h)$`. |
| `webhook_path` | string | no | Path on the application that this provider posts webhooks to. The sandbox forwarder and the offline pack both deliver here. Max length 512. |

## Environment variable

One variable a service needs. The manifest declares the name and where the value comes from; it never holds the value itself, which is why the file is safe to commit.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `from` | string | no | Where to read the value: a secrets adapter name, or the name of a different variable to copy. Max length 256. |
| `name` | string | **yes** | Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `required` | boolean | no | Whether the environment fails to start without it. Defaults to true, because a service silently missing configuration is the failure this product exists to prevent. Defaults to `true`. |
| `sandbox` | boolean | no | Marks a credential that must be a sandbox one. The secrets subsystem refuses a value carrying a known live prefix, and the proxy trips a wire if one reaches the network anyway. Defaults to `false`. |
| `value` | string | no | A literal value for a variable that is configuration rather than a secret, such as a feature flag or a public URL. A value that looks like a credential is rejected. Max length 2048. |

## GitHub

How Antifailure appears on a pull request: what runs it, whether it comments, what it does with forks, and when it tears the environment down.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `comment` | boolean | no | Whether to maintain a single comment on the pull request. It is updated in place rather than appended, so a busy pull request does not accumulate twenty bot comments. Defaults to `true`. |
| `fork_policy` | `never`, `label`, `always` | no | What to do with a pull request from a fork. label requires a maintainer to add antifailure:allow first, which is the only safe default: a fork's code would otherwise run with the environment's credentials. Defaults to `label`. |
| `mode` | `actions`, `app`, `off` | no | actions runs everything inside a workflow with no server. app uses the GitHub App and the control plane. Defaults to `actions`. |
| `teardown_on` | list of string | no | Events that tear the environment down. Defaults to `[close merge ttl]`. Max items 5. |

## Golden

The masked, verified copy every environment branches from.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `max_age` | string | no | How stale a golden may be before af up refreshes it first. Defaults to `168h`. Matches `^[0-9]+(h\|d)$`. |
| `retain` | integer | no | How many versions to keep. A referenced version is never collected regardless of this. Defaults to `5`. Minimum 1, maximum 100. |
| `schedule` | string | no | Cron expression for automatic refreshes, with an optional CRON_TZ prefix. A refresh that would overlap a running one is skipped with an event rather than queued. Max length 128. |
| `storage` | `local`, `azure_blob`, `s3` | no | Where dumps and attestations live. Defaults to `local`. |
| `storage_url` | string | no | Container or bucket URL for a remote store. Credentials come from the secrets subsystem, never from this URL. Max length 1024. |

## Insights

The Postgres native checks that turn a preview environment into a database review.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | boolean | no | Defaults to `true`. |
| `large_table_rows` | integer | no | Row count above which a migration lint treats a table as large, where a rewrite or an exclusive lock is an outage rather than a pause. Defaults to `100000`. Minimum 0. |
| `migration_rehearsal` | boolean | no | Apply pending migrations to a fresh branch, recording per statement duration and the strongest lock held per table. Defaults to `true`. |
| `plan_diff` | boolean | no | Compare query plans between branches to catch an index that stopped being used. Defaults to `true`. |
| `query_regression` | boolean | no | Diff pg_stat_statements between the base branch and this one after running the same workflows, to catch a query loop before it reaches production. Defaults to `true`. |
| `regression_factor` | number | no | How much slower a query may get before it is reported. Defaults to `1.5`. Minimum 1. |
| `regression_min_ms` | number | no | Minimum absolute change in mean milliseconds before a regression is reported, so that a query going from 0.1 to 0.2 milliseconds is not news. Defaults to `5`. Minimum 0. |

## Invariant

One read only statement that must hold after every workflow. Invariants are the assertions a test cannot make from the outside, checked against the database rather than the interface.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `description` | string | no | What is wrong when this fails, in one sentence. It becomes the failure message. Max length 512. |
| `name` | string | **yes** | Max length 64, matches `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `sql` | string | **yes** | A single read only statement. It runs inside a read only transaction with a statement timeout, so a write is refused by Postgres as well as by validation. The invariant fails when the statement returns any row, so write it to select the violations. Min length 6, max length 4000. |

## Load

Traffic shaped like production, compared between the base branch and this one. Results are always deltas, never absolute capacity claims.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `duration` | string | no | How long to run. Capped at fifteen minutes. Defaults to `2m`. Matches `^[0-9]+(s\|m)$`. |
| `enabled` | boolean | no | Defaults to `false`. |
| `safe_routes` | list of string | no | Routes that may be called freely because they do not mutate state. Max items 500. |
| `scale` | number | no | Fraction of production arrival rate to reproduce. Defaults to `0.05`. Minimum 0.001, maximum 1. |
| `source` | `none`, `datadog`, `newrelic`, `otel`, `access_log` | no | Where the endpoint mix comes from. Defaults to `none`. |
| `source_config` | object | no | Adapter specific settings, such as a service name or a log path. Credentials come from the secrets subsystem. Max properties 20. |
| `thresholds` | object | no | Deltas that fail the run. Applied to the difference against the base branch, never to absolute numbers. |
| `unsafe_routes` | list of string | no | Routes that mutate state destructively. They are included only against a fresh branch that is reset afterwards. Max items 500. |

## password rules

The application's password policy, so the generated password satisfies it. Without this, an application stricter than the generator refuses a correct password at sign in and the run reports a login failure that looks like the application's fault.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `forbid` | string | no | Characters the application will not accept. Max length 32. |
| `min_length` | integer | no | Minimum 1, maximum 128. |
| `symbols` | string | no | Replaces the default symbol set, for an application that rejects the ones it uses. Max length 32. |

## Persona

One account an agent logs in as. Personas are created or reconciled in the golden by the authentication adapter, so an agent signs in the way a person does rather than through a bypass.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `attributes` | object | no | Extra columns to set on the persona's row, for a schema with application specific fields. Max properties 50. |
| `email` | string | no | Login address. Defaults to name@example.test, which is a reserved domain that can never receive mail. Max length 254. |
| `login` | `none`, `password`, `magic_link`, `email_code`, `sms_code`, `totp`, `session` | no | How this persona signs in. none is for an application with no sign in, or a page that is public: the agent goes straight to start_path. magic_link and email_code read the message from the captured inbox, so they work with no mail provider at all. The runner drives none, password, magic_link, email_code and sms_code today; a persona set to totp or session is reported as blocked with the reason, rather than failing the change. Defaults to `password`. |
| `mfa` | boolean | no | Whether to enroll a time based one time password secret, which the runner holds so that it can complete a challenge. Defaults to `false`. |
| `name` | string | **yes** | Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `phone` | string | no | Number an SMS code is sent to. Defaults to a number in the +1 555 0100 block, which is reserved for fictional use and can never reach a real handset. Only sms_code uses it. Max length 32. |
| `role` | string | no | Application role to provision, for example admin or member. Interpreted by the authentication adapter. Max length 64. |

## Resources

What one replica of a service is allowed to use. Absent means the runtime decides, which locally means no limit and on a cluster means the namespace default.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `cpu` | string | no | CPU limit, in cores or millicores. Defaults to `1`. Matches `^[0-9]+(\.[0-9]+)?m?$`. |
| `memory` | string | no | Memory limit. Defaults to `1Gi`. Matches `^[0-9]+(Mi\|Gi\|M\|G)$`. |

## Runtime

Where and how long the environment runs. The provider decides the machinery; the rest is the lifetime, the address, and the naming the environment gets.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `domain` | string | no | Wildcard domain for environment hostnames. Defaults to localhost, which needs no DNS at all. Defaults to `localhost`. Max length 253. |
| `idle_sleep` | string | no | How long an environment may sit idle before it is scaled to zero. It wakes on the next request. Defaults to `30m`. Matches `^[0-9]+(m\|h)$`. |
| `kubeconfig_context` | string | no | Which kubeconfig context to use. Naming it prevents an environment landing on whatever cluster happened to be current. Max length 253. |
| `namespace_prefix` | string | no | Prefix for Kubernetes namespaces. Defaults to `af`. Max length 40. |
| `provider` | `local`, `kubernetes` | no | Defaults to `local`. |
| `ttl` | string | no | How long an environment lives before automatic teardown. Defaults to `168h`. Matches `^[0-9]+(h\|d)$`. |

## Service

One process the environment runs. A service is built from the repository, given the variables it declared, attached to the environment's private network, and started in dependency order.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `build` | [Build](#build) | no | How to turn the service directory into an image. |
| `command` | string | no | Command that starts the service, overriding the image's own. Executed with an argument vector, never through a shell. Max length 1024. |
| `depends_on` | list of string | no | Services that must be ready first. A cycle is rejected at validation. Max items 50. |
| `env` | list of [Environment variable](#environment-variable) | no | Names of environment variables this service needs. Names only. Values come from the secrets subsystem, and a name with no value anywhere fails with AF-SEC-001 rather than starting a service that will misbehave. Max items 200. |
| `health_path` | string | no | HTTP path that reports readiness. A service is not considered up until this returns a 2xx or 3xx status. Defaults to `/`. Max length 512. |
| `health_timeout` | string | no | How long to wait for readiness before failing with AF-RUN-004. Defaults to `180s`. Matches `^[0-9]+(ms\|s\|m)$`. |
| `kind` | `web`, `worker`, `cron` | no | What the service is. A web service gets a hostname and a readiness check; a worker gets neither; a cron service is invoked on a schedule instead of run continuously. Defaults to `web`. |
| `migrate` | string | no | Command that applies pending migrations. Run once against a fresh branch before the services start, and rehearsed with timing and lock analysis when insights are on. Max length 1024. |
| `name` | string | **yes** | Unique within the manifest. Appears in hostnames, logs, and container names. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `path` | string | no | Directory containing the service, relative to the repository root. Defaults to the root. A path outside the repository is rejected. Max length 512. |
| `port` | integer | no | Port the service listens on. Required for a web service unless detection found it. Minimum 1, maximum 65535. |
| `replicas` | integer | no | How many instances to run. Defaults to `1`. Minimum 1, maximum 10. |
| `resources` | [Resources](#resources) | no | What one replica of a service is allowed to use. |
| `schedule` | string | no | Cron expression for a cron service, with an optional CRON_TZ prefix. Evaluated in the declared zone. Max length 128. |

## Subset

Take a production shaped slice rather than the whole database. The closure is computed over foreign keys, so a subset always satisfies every constraint the schema declares.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | boolean | no | Defaults to `false`. |
| `follow_dependents` | integer | no | How many levels of rows that reference the seed to include. Zero includes only what the seed rows reference, which is the minimum that satisfies foreign keys. Defaults to `1`. Minimum 0, maximum 5. |
| `max_rows` | integer | no | Upper bound on rows per table, applied deterministically so two runs produce the same subset. Defaults to `1e+06`. Minimum 1. |
| `seed_table` | string | no | Table the selection starts from, for example the tenant or account table. Max length 128. |
| `seed_where` | string | no | A SQL predicate selecting the seed rows, for example created_at > now() - interval '90 days'. Max length 2048. |
| `virtual_relationships` | list of object | no | Relationships the schema does not declare as foreign keys but the application relies on. Without these, a subset can look complete and still break the application. Max items 200. |

## Workflow

One thing the agents do, written as a goal rather than a script. The runner decides the actions and verifies the outcome, so a workflow survives a redesign of the page it happens on.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `budget` | object | no | Hard caps. A workflow that exhausts its budget ends as blocked with the reason, never as a partial pass. |
| `description` | string | **yes** | What a person would do, in sentences. Say the goal and what proves it happened, not the selectors. Min length 10, max length 4000. |
| `expect` | list of string | no | Observations that must hold for a pass, written as sentences. These are assertions about what the user can see, not about the DOM. Max items 50. |
| `independent` | boolean | no | Whether this workflow can run at the same time as others. Workflows that share an environment run one at a time unless this says otherwise, because two agents mutating the same data produce failures nobody can reproduce. Defaults to `false`. |
| `name` | string | **yes** | Max length 64, matches `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `persona` | string | no | Which persona runs it. Defaults to the first persona. Max length 40. |
| `start_path` | string | no | Where to begin. Defaults to the application root. Defaults to `/`. Max length 512. |
| `tags` | list of string | no | Max items 20. |


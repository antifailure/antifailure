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
| `datastores` | list of [Datastore](#datastore) | no | Every store the environment holds, and what is done about each one's contents. The database: block above normalizes into the entry named primary, so a manifest that declares only database: already has this list and does not have to write it. A stance is declared rather than defaulted, because an empty ClickHouse nobody chose looks exactly like an empty ClickHouse somebody decided on. Max items 25. |
| `desktop` | [Desktop application](#desktop-application) | no | Which application the desktop workflows drive, declared once because a manifest describes one product. |
| `diversity` | [Diversity](#diversity) | no | Behavioral variance for the agents that drive the workflows. |
| `egress` | [Egress](#egress) | no | What the environment may reach on the network. |
| `explore` | [Explore](#explore) | no | Agents that pursue a goal with no declared workflow, discover the paths an application offers, and report where it costs somebody effort without failing. |
| `fidelity` | [Fidelity](#fidelity) | no | The component inventory: what the environment reproduces, what stands in for something, and what it could not reproduce at all. |
| `github` | [GitHub](#github) | no | How Antifailure appears on a pull request: what runs it, whether it comments, what it does with forks, and when it tears the environment down. |
| `insights` | [Insights](#insights) | no | The Postgres native checks that turn a preview environment into a database review. |
| `invariants` | list of [Invariant](#invariant) | no | Read only statements that must hold after every workflow. They are the assertions a test cannot make from the outside: no orphaned rows, no negative balances, no subscription without a customer. Max items 100. |
| `load` | [Load](#load) | no | Traffic shaped like production, sent at an environment. |
| `name` | string | no | A short name for this application, used in environment hostnames and in the control plane. Defaults to the repository directory name. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `oracle` | [Oracle](#oracle) | no | Deploy a baseline version alongside the candidate, send both the same requests, and report every difference in what came back and in what ended up in the database. |
| `personas` | list of [Persona](#persona) | no | The accounts agents log in as. Each is created or reconciled in the golden by the authentication adapter, so a persona is a real user of the application rather than a bypass. Max items 50. |
| `policy` | [Policy](#policy) | no | What each class of finding does to the pull request check. |
| `runtime` | [Runtime](#runtime) | no | Where and how long the environment runs. |
| `security` | [Security](#security) | no | Fixtures the dynamic security suite needs and the engine cannot infer from a diff. |
| `services` | list of [Service](#service) | no | Every process the environment runs: web servers, API servers, background workers, and scheduled jobs. Min items 1, max items 50. |
| `terminal_workflows` | list of [Terminal workflow](#terminal-workflow) | no | What the agents do at a command line. Written the same way a browser workflow is, as a goal and what proves it happened, and run in the same `af test` against the same environment, so a terminal result is counted and reported exactly like a browser one. Max items 200. |
| `version` | `1` | no | The manifest schema version. Increment only for a breaking change; the engine refuses a version it does not understand rather than guessing. |
| `workflows` | list of [Workflow](#workflow) | no | What the agents do, written as sentences. A workflow is a goal, not a script: the runner decides the actions and verifies the outcome. Max items 200. |

## AccessObject

One ownership-scoped object the access-probe pass reaches. The application's own seed plants the canary into the object; this only declares the ownership and the planted value, so the engine stays application-agnostic. The canary value stays inside the engine; a finding reports the location and the class, never the value.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `canary` | string | **yes** | The token the application's seed planted into this object so it appears in the object's response body. Its presence in a response a persona should not have been able to read is what proves the leak. The value stays inside the engine. Max length 256. |
| `canary_kind` | `pii`, `secret` | no | What the planted canary is, which decides the canary_leak finding key when the same value surfaces in a response it must not. Defaults to pii, because another owner's object content is another person's data; set secret for a planted credential. Defaults to `pii`. |
| `id` | string | **yes** | The concrete object id substituted into the route's dynamic segment. It names one real seeded object, so a refusal proves a boundary dropped a real row rather than that the id was invented. Max length 256. |
| `object_class` | string | **yes** | A category label for the object, for example "another customer's order". It is what a finding says was reached, so it is a label and never an id or a value. Max length 128. |
| `owner` | [AccessOwner](#accessowner) | **yes** | Who owns an access object, given either as a declared persona by name or as an explicit identity. |
| `route` | string | **yes** | The object's location template, for example /api/orders/{id}. The id is substituted into its dynamic segment to form the concrete reach, and the template, never the concrete url, is what a finding reports. Max length 512, matches `^/`. |

## AccessOwner

Who owns an access object, given either as a declared persona by name or as an explicit identity. Naming a persona keeps one source of truth for the identity; an explicit identity is for an owner that seeds data but never signs in.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `persona` | string | no | A declared persona whose identity owns the object. When set, user and role are resolved from that persona, and tenant is resolved from it unless tenant here supplies one the persona does not carry. Max length 40. |
| `role` | string | no | The explicit owning role, for an owner that is not a declared persona. Max length 64. |
| `tenant` | string | no | The explicit owning tenant, or a tenant supplied for a persona owner that carries none, so a cross-tenant reach can be expressed. At least one of persona, tenant or user must be set. Max length 128. |
| `user` | string | no | The explicit owning user identifier, for an owner that is not a declared persona. Max length 128. |

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
| `api_key_env` | string | no | The name of the variable holding the provider's API key. Named rather than carried: a manifest is committed and a key is not. Defaults to NEON_API_KEY for the neon provider. For the pgurl provider it names the connection string of the server that holds the goldens and the branches, which is the credential in that case, and defaults to PGURL_ADMIN_URL. |
| `extensions` | list of string | no | Extensions to create in the golden before the source is copied into it, one CREATE EXTENSION IF NOT EXISTS each, in the order given. Declare the ones the schema depends on: an extension that is installed in the image but never created carries no types, no operators and no table access methods, so a table stored with one is refused by the restore rather than created. An extension the image does not carry is refused by name, with the image named, rather than surfacing later as a type nobody can find. The golden is committed after this runs, so every branch of it already has them. Max items 32. |
| `golden` | [Golden](#golden) | no | The masked, verified copy every environment branches from. |
| `image` | string | no | The container image the docker provider runs Postgres from, instead of the stock postgres:<version>-alpine. This is how a schema that needs PostGIS, pgvector, TimescaleDB, pg_cron or a custom table access method gets a golden at all: the stock image carries the contrib modules and nothing else, so an extension the source has and the image does not stops the restore. Name an image that already carries what the schema needs, such as pgvector/pgvector:pg17 or postgis/postgis:17-3.5, and pin it by digest where the golden has to be reproducible. The image must run the official entrypoint and honour PGDATA, because the golden is the container's filesystem committed, and it must be the major version this block declares: a mismatch is refused rather than committed. Only the docker provider has an image to choose, so any other provider refuses this key rather than ignoring it. Max length 512. |
| `masking_rules` | string | no | Path to the masking rules file, relative to the repository root. Defaults to `masking.yaml`. Max length 512. |
| `max_branches` | integer | no | The plan's concurrent branch limit, where the provider has one it cannot read from its own API. Reaching it fails with AF-DB-006 rather than hanging. Minimum 1. |
| `migrations` | [Migrations](#migrations) | no | Where the project's own SQL migrations live, for a project whose migrate command is its own script rather than a tool the rehearsal recognises. |
| `preload_libraries` | list of string | no | Libraries to add to shared_preload_libraries, for an extension that has to be loaded at server start rather than created in a database, such as timescaledb, citus or pg_cron. They are ADDED to shared_preload_libraries rather than replacing it, and they come FIRST, with pg_stat_statements after them: citus refuses to load from anywhere but the front and the server then exits during initialisation, while the statistics module chains with whatever else hooks the executor and does not care where it sits. Dropping the statistics module is not an option this key has, because that leaves the insights reading a permanently empty table and reporting that statement timing is unavailable on every environment. A plain library name only, never a path, because this value is a list of shared objects the server loads as its own code. The list is stamped on the golden image and read back when a branch starts, so a branch of a golden built with a library preloaded starts with it too even if the manifest has since stopped asking for it. Max items 32. |
| `project` | string | no | The account-side project a hosted provider creates branches in, such as a Neon project. Not a secret, which is why it lives here and the key that reaches it does not. |
| `provider` | string | no | Which provider creates branches. docker is local and needs nothing; neon, supabase, dblab and xata talk to a service; pgurl is any reachable Postgres, which is where the goldens and the branches are kept as databases on a server you name. For xata, database.project is '<organization>/<project>'. aurora clones an Amazon Aurora PostgreSQL cluster and is in the enterprise edition, so a community build names it here and refuses it when a manifest selects it. `cloudsql` fast clones a Google Cloud SQL for PostgreSQL instance and `azurepg` restores an Azure Database for PostgreSQL Flexible Server to a point in time; both are enterprise for the same reason. `azurepg` is the one provider here that does not branch in time flat in the size of the database, because a restore replays write ahead logs after the snapshot and that half is not flat. `rds` restores an Amazon RDS for PostgreSQL DB snapshot, is enterprise for the same reason, and does not branch in flat time either, because a restore hydrates a new volume with every byte. Defaults to `docker`. |
| `seed` | string | no | Command that fills the golden with data, for a project with no production database yet. It runs once per refresh with DATABASE_URL set, and every branch is a copy of what it made, so the cost is paid once rather than per environment. Mutually exclusive with source_url_env. Max length 1024. |
| `source_url_env` | string | no | Name of the environment variable holding the read only connection string of the production database. The value is read once, during a golden refresh, on the operator's machine or runner, and never stored. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `subset` | [Subset](#subset) | no | Take a production shaped slice rather than the whole database. |
| `url_env` | string | no | Name of the environment variable to inject into services with the branch's connection string. Defaults to `DATABASE_URL`. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `version` | `14`, `15`, `16`, `17`, `18` | no | Postgres major version. Match it to the source: a golden built on a different major is an environment running a Postgres your application does not. Defaults to `17`. |
| `volume` | [Volume](#volume) | no | The committed record of what production holds, which is the denominator every row count in a report is measured against. |

## Datastore

One store the environment holds. Database is a single struct and it is Postgres, so before this list existed there was one golden, one masking pass, one verification scan and one branch, and every other store a manifest declared was an empty container no part of the report mentioned.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `because` | string | no | Why this stance was chosen, in the words of whoever chose it. It is carried into the fidelity report as written. An empty store nobody explained and an empty store somebody decided on look identical in a running environment, and this is the only thing that tells them apart afterwards. Required for the empty stance. Max length 512. |
| `engine` | string | **yes** | What the store runs, such as `postgres`, `clickhouse`, `redis`, `kafka` or `elasticsearch`. Open rather than a fixed list: a manifest naming an engine this build has no provider for is refused by the provider lookup, by name, which says more than an unknown value would. Max length 40, matches `^[a-z0-9]([a-z0-9_-]{0,38}[a-z0-9])?$`. |
| `from` | string | no | The datastore a derived store is rebuilt from, named. Required for the derived stance and refused for the others. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `name` | string | **yes** | Unique within the manifest. The name primary is reserved for the entry the database: block normalizes into. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `provider` | string | no | Which implementation provides the engine, for an engine more than one thing can provide. Omit it for the engine's own default. Max length 64. |
| `rebuild` | [Datastore rebuild](#datastore-rebuild) | no | How a derived store is built from the one named in from. Required for that stance and refused for the others. |
| `source_url_env` | string | no | The NAME of the variable holding this store's production connection string, which is what a golden of it is copied from. A variable name rather than a URL, because the value is a credential for production and a manifest is checked in. Omitted, the golden is EMPTY and every refresh says so: that is the same answer database.source_url_env gives a project that has not connected production yet, and it is not a refusal because a store whose tables are made by migrations is still worth branching. A connection string written here rather than a variable name is refused, and the refusal does not print it back. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `stance` | `golden`, `empty`, `derived`, `topics_only` | **yes** | What happens to this store's contents. golden is a masked, verified copy environments branch from. empty starts it with nothing, on purpose, and because says why. derived rebuilds it from the store named in from, once that one is ready, which is how a search index is built from the Postgres branch rather than cloned and left stale against it. topics_only creates topics and consumer groups with no messages. There is no default: a datastore that declares no stance is refused, because a silent default is how somebody ends up trusting a blank ClickHouse. |
| `topics` | list of [Datastore topic](#datastore-topic) | no | The topics a topics_only broker is created with, and the consumer groups created against them. Required for that stance and refused for the others. Declared rather than discovered, because there is nothing to discover: a broker's topics live in production and copying the messages in them is what this stance exists to refuse. What a twin needs is the SHAPE, and the shape is something only the person writing the manifest knows. Max items 200. |

## Datastore rebuild

How a derived store is built from the one it reads. A command rather than a copy, and that is the whole argument for the stance: a search index cloned from production is stale against the branch the moment the branch is masked, because the documents in it name people who do not exist in the twin's Postgres. An index BUILT from the branch cannot be stale against it.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `command` | string | **yes** | What rebuilds the store. It runs once, to completion, inside the environment, after every service is up, and a non-zero exit fails the environment rather than leaving an index nobody built. Max length 1024. |
| `service` | string | **yes** | The service whose image the command runs in, and whose variables it receives. It is the application's own in almost every case, because the code that knows how to index this product's rows is the product's code. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |

## Datastore topic

One topic a topics_only broker is created with. An empty broker is not a twin of a broker: a consumer subscribing to a name that is not there reads nothing and reports nothing, and the run goes green having tested one poll loop against a name that will only exist in production.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `consumer_groups` | list of string | no | The groups created against this topic, with their offsets committed to the earliest message and nothing behind them. Created rather than left to appear on their own, because a consumer joining a group nobody created reads from the END by default, so the twin's first run of a consumer silently skips everything the twin's own producers wrote before it started. Max items 100. |
| `name` | string | **yes** | The topic, named the way production names it. Unique within the store. Max length 249, matches `^[a-zA-Z0-9._-]{1,249}$`. |
| `partitions` | integer | no | How many partitions the topic is created with. Not cosmetic: ordering is per partition and a consumer group with more members than partitions leaves members idle, so a twin whose topic has one partition where production has twelve cannot reproduce a reordering bug at all. Defaults to `1`. Minimum 1, maximum 10000. |

## Desktop application

Which application the desktop workflows drive, declared once because a manifest describes one product. It is what `base_url` is to a browser run: a workflow says what to do and this says what to do it to.

Required by a manifest that has one. A workflow whose `surface` is `desktop` names no application of its own, because a list whose entries each name their own would be a list of unrelated runs sharing one report, with nothing in it saying which of them the change under review was about. So the application is declared here, once, and a manifest that asks for the desktop surface without it is refused while the manifest is read, before an environment is built for a run that could never open anything.

The application is driven through its ACCESSIBILITY TREE, the same thing a screen reader reads, which is why a desktop workflow is written exactly like a browser one: a goal, a persona, and what proves it happened. Nothing here names a coordinate, a window position or a control's internal id.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `application` | string | **yes** | What to launch. For `electron`, the Electron binary itself, which inside a packaged application is the executable in Contents/MacOS and in a project under development is the one in node_modules. For `macos`, the .app bundle. Relative paths are resolved against the directory holding the manifest, because the runner is a subprocess started from somewhere the manifest never mentions and a path resolved there would name a different file. Min length 1, max length 512. |
| `args` | list of string | no | The arguments, one per entry. Passed as written and never through a shell, so a space in a value is part of that value. An Electron project under development is usually launched by passing the directory holding its package.json. A native application is given these after its bundle is opened. Max items 64. |
| `kind` | `electron`, `macos` | **yes** | Which kind of application this is, and so which accessibility tree it publishes.  `electron` covers anything built on Electron, which is most of the desktop software a team would want rehearsed: VS Code, Slack, Discord. Underneath one is Chromium, so it publishes the same accessibility tree a web page does.  `macos` covers a native application, read through the platform's own accessibility API. It needs the macOS Accessibility permission, which a person grants in System Settings and which nothing in software can grant. A run without it is reported as blocked with that step named, never as an application with no controls on it.  Stated rather than guessed from the path, because guessing would mean an application that is driven the wrong way reports as an application that does not work. |
| `process` | string | no | What macOS calls the running application, when that is not the bundle's own name. Only for `macos`, and refused on `electron`, which is launched directly and never looked up.  It exists because opening a bundle returns before the application is ready, so the process still has to be found by name, and the two names are not always the same: Visual Studio Code.app runs as Code. Defaults to the bundle's name without .app, which is right for most applications. Min length 1, max length 128. |

## Diversity

Behavioral variance for the agents that drive the workflows. A personality is HOW an agent behaves while pursuing a workflow's goal, a separate axis from the persona it signs in as, and it changes only which listed control the agent prefers, never what the agent can do. Off by default, reproducible from a seed, and never a reason a workflow fails: it widens the paths a change is exercised over, so a behavioral regression that one scripted path misses is surfaced by another personality taking a different one.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `agents_per_workflow` | integer | no | How many personality varied agents drive each workflow. One is the default and reproduces a single run; more produces one result per agent, each labelled with its personality, so behavior coverage is visible in the report. Defaults to `1`. Minimum 1, maximum 10. |
| `enabled` | boolean | no | Whether personality varied agents drive the workflows. Off is today's behavior: one neutral agent per workflow, identical to a manifest with no diversity block. Defaults to `false`. |
| `mix` | `balanced`, `realistic_population`, `aggressive_diversity` | no | How the population is drawn. balanced spreads a few common strategies evenly, realistic_population follows the built in population weights, and aggressive_diversity spreads strategies as widely across agents as the count allows. Defaults to `balanced`. |
| `personalities` | list of [Personality](#personality) | no | Which personalities may be drawn, and their weights. Absent means the ten built in personalities at their default population weights. Max items 50. |
| `seed` | string | no | Decides which personality drives which workflow and the behavioral profile layered on top. The same seed against the same application assigns the same personalities, step for step, which is what lets a personality driven finding be replayed. Defaults to the run id and is echoed into the report. Max length 200. |
| `variance` | `low`, `medium`, `high` | no | How far each agent's behavioral profile may drift from the neutral centre. low keeps agents close to regular behavior, high lets them diverge. Defaults to `medium`. |

## Egress

What the environment may reach on the network. Everything leaves through the sidecar, and everything not named here is blocked.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `allow_ipv6` | boolean | no | Whether the environment may open IPv6 connections. Off by default, because an IPv6 path that bypasses the proxy is the most common way an egress control is silently defeated. Defaults to `false`. |
| `default` | `block`, `allow`, `capture`, `mock`, `emulate`, `sandbox`, `synth` | no | What happens to a host with no rule. Changing this away from block is a deliberate act with a real cost: it is how a preview environment emails a real customer. emulate is listed here and is refused as a default, because the emulator is named on a rule and a default names no rule; the refusal says so, which a missing enum value could not. Defaults to `block`. |
| `rules` | list of [Egress rule](#egress-rule) | no | What the environment may do with one host. Max items 500. |

## Egress rule

What the environment may do with one host. A rule is per host because that is the unit a person can reason about: allowed, blocked, answered from a fixture, answered by an emulator inside the environment, or sent to the provider's own sandbox.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `credential` | string | no | Name of the environment variable holding the sandbox credential for this host. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `emulator` | string | no | Name of the registered emulator that answers this host, for a rule in emulate mode. Required there and refused on every other mode. A name this build has not registered is refused rather than falling through to block. Max length 63, matches `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`. |
| `fixtures` | string | no | Path to a fixture pack or an OpenAPI document for mock mode, relative to the repository root. Max length 512. |
| `host` | string | **yes** | Host to match. A leading *. matches one or more labels. A star anywhere else is one whole label, so email.*.amazonaws.com reaches SES in any region and reaches nothing else, and *.s3.*.amazonaws.com reaches a bucket in any region. An IP literal matches only itself. Max length 253. |
| `methods` | list of string | no | Restrict the rule to these HTTP methods. Max items 10. |
| `mode` | `block`, `allow`, `capture`, `mock`, `emulate`, `sandbox`, `synth` | **yes** | block refuses with a readable decision. allow passes through with a rate limit. sandbox substitutes test credentials and forwards to the provider's sandbox. capture records the message into the inbox and returns the provider's success shape. mock answers from a fixture or an offline pack. emulate answers from an emulator running inside the environment, which the application reaches with no endpoint override. synth asks a model to invent a response and marks every result that touched it as unverified. |
| `note` | string | no | Why this rule exists. Rendered in the network policy view, because a rule nobody can explain is a rule nobody dares remove. Max length 512. |
| `paths` | list of string | no | Restrict the rule to these path prefixes. Anything else on the same host falls through to the next rule. Max items 100. |
| `rate_limit` | string | no | Token bucket rate, for example 10/s or 600/m. Applies to allow and sandbox. Matches `^[0-9]+/(s\|m\|h)$`. |
| `webhook_path` | string | no | Path on the application that this provider posts webhooks to. The sandbox forwarder and the offline pack both deliver here. Max length 512. |

## Environment variable

One variable a service needs. The manifest declares the name and where the value comes from; it never holds the value itself, which is why the file is safe to commit.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `from` | string | no | The name the value is stored under, when it differs from the name the service reads. The service receives it under name. With scope set to service, this stored name is the one spelled as the service's own. Max length 256. |
| `name` | string | **yes** | Max length 128, matches `^[A-Za-z_][A-Za-z0-9_.]*$`. |
| `required` | boolean | no | Whether the environment fails to start without it. Defaults to true, because a service silently missing configuration is the failure this product exists to prevent. Defaults to `true`. |
| `sandbox` | boolean | no | Marks a credential that must be a sandbox one. The secrets subsystem refuses a value carrying a known live prefix, and the proxy trips a wire if one reaches the network anyway. Defaults to `false`. |
| `scope` | `service` | no | Whose value this is. Leave it out for a value every service that declares the name shares. service makes it this service's own: it is looked up as the service's name in capitals with hyphens as underscores, two underscores, then the name, so the storage service's DATABASE_URL is looked up as STORAGE__DATABASE_URL and no other service receives it. A sandbox credential cannot be scoped, because the egress proxy holds one value per credential for the whole environment. |
| `value` | string | no | A literal value for a variable that is configuration rather than a secret, such as a feature flag or a public URL. A value that looks like a credential is rejected. Max length 2048. |

## Explore

Agents that pursue a goal with no declared workflow, discover the paths an application offers, and report where it costs somebody effort without failing. An exploration is reproducible from its seed and never counts against the change.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | boolean | no | Defaults to `false`. |
| `goals` | list of [Goal](#goal) | no | One thing an exploratory agent tries to achieve. Max items 50. |

## Fidelity

The component inventory: what the environment reproduces, what stands in for something, and what it could not reproduce at all.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | boolean | no | Defaults to `true`. |
| `require` | list of string | no | Dimensions every component of which must be reproduced. A dimension that could not be measured neither satisfies a requirement nor breaks one. |

## GitHub

How Antifailure appears on a pull request: what runs it, whether it comments, what it does with forks, and when it tears the environment down.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `comment` | boolean | no | Whether to maintain a single comment on the pull request. It is updated in place rather than appended, so a busy pull request does not accumulate twenty bot comments. Set false and af change and af ci write comment=false to GITHUB_OUTPUT for the workflow to gate its comment step on. The report files are still written, because the report is also the job summary and the payload a control plane is sent. Defaults to `true`. |
| `fork_policy` | `never`, `label`, `always` | no | What to do with a pull request from a fork. label requires a maintainer to add antifailure:allow first, which is the only safe default: a fork's code would otherwise run with the environment's credentials. Enforced by af ci, af up, af test and af load run before an environment is created, on pull_request and pull_request_target, and read from the base branch rather than from the pull request, because the pull request's copy of this file belongs to the contributor. Defaults to `label`. |
| `mode` | `actions`, `app`, `off` | no | actions runs everything inside a workflow with no server. app uses the GitHub App and the control plane. Defaults to `actions`. |
| `teardown_on` | list of string | no | Accepted and read by nothing. Teardown is unconditional: af ci tears down whatever the outcome, and the control plane asks for teardown on close, merge, supersession and timeout without reading your manifest. The lifetime ceiling is runtime.max_ttl. Defaults to `[close merge ttl]`. Max items 5. |

## Goal

One thing an exploratory agent tries to achieve. Unlike a workflow this declares no outcome, so it cannot fail: what it produces is the path it took and the friction it met on the way.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `budget` | object | no | Hard caps. An exploration that exhausts its budget reports what it found up to that point and says the budget ran out. |
| `goal` | string | **yes** | What somebody is trying to do, in one sentence. The agent has no script, so this is the only thing telling it where to go, and its words are what decide whether the goal was reached. Min length 10, max length 1000. |
| `name` | string | **yes** | Max length 64, matches `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `persona` | string | no | Which persona explores. Defaults to the first persona. Max length 40. |
| `seed` | string | no | Decides every choice the agent makes. The same seed against the same application takes the same path, step for step, which is what lets a finding be replayed. Defaults to the goal's name. Max length 64. |
| `slow_ms` | integer | no | How long one step may take before it is reported as friction. Defaults to `3000`. Minimum 1, maximum 600000. |
| `start_path` | string | no | Where to begin. Defaults to the application root. Defaults to `/`. Max length 512. |

## Golden

The masked, verified copy every environment branches from.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `max_age` | string | no | How stale a golden may be before af up refreshes it first. Defaults to `168h`. Matches `^[0-9]+(ms\|s\|m\|h\|d)$`. |
| `retain` | integer | no | How many versions to keep. A referenced version is never collected regardless of this. Defaults to `5`. Minimum 1, maximum 100. |
| `schedule` | string | no | Cron expression for automatic refreshes, with an optional CRON_TZ prefix. A refresh that would overlap a running one is skipped with an event rather than queued. Max length 128. |
| `storage` | `local`, `azure_blob`, `s3`, `gcs` | no | Where dumps and attestations live. Defaults to `local`. |
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
| `rolling_compatibility` | [Rolling compatibility](#rolling-compatibility) | no | Run the previous release against the migrated schema and see whether its workflows still pass, which is the invariant a rolling deploy actually depends on. |

## Invariant

One read only statement that must hold after every workflow. Invariants are the assertions a test cannot make from the outside, checked against the database rather than the interface.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `description` | string | no | What is wrong when this fails, in one sentence. It becomes the failure message. Max length 512. |
| `name` | string | **yes** | Max length 64, matches `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `sql` | string | **yes** | A single read only statement. It runs inside a read only transaction with a statement timeout, so a write is refused by Postgres as well as by validation. The invariant fails when the statement returns any row, so write it to select the violations. Min length 6, max length 4000. |

## Load

Traffic shaped like production, sent at an environment. Results are never absolute capacity claims: one machine under a fraction of production's rate cannot tell you what the fleet serves. Two different baselines are available and they are not interchangeable. load.thresholds judges one run against PRODUCTION's own per route p95, carried by the traffic source. load.comparison judges this branch against a second run of the same workload on the base branch, which is the only place in this block a base branch delta is measured.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `comparison` | object | no | Run this same workload against the base branch too, and difference the two. A second environment is brought up from the base revision and pinned to the candidate's golden, so both sides start from identical rows, and both are sent the same request sequence under the same seed. This is the only part of load that measures a base branch delta. What the seed cannot control is said in the report rather than left implied: two runs against two environments are not a controlled experiment, so a difference is a difference and a threshold is what turns it into a verdict. |
| `duration` | string | no | How long to run. Capped at fifteen minutes. Defaults to `2m`. Matches `^[0-9]+(s\|m)$`. |
| `enabled` | boolean | no | Defaults to `false`. |
| `safe_routes` | list of string | no | Routes that may be called freely because they do not mutate state. Max items 500. |
| `scale` | number | no | Fraction of production arrival rate to reproduce. Defaults to `0.05`. Minimum 0.001, maximum 1. |
| `scenarios` | list of [Load scenario](#load-scenario) | no | Declared journeys run against the environment beside the mix. Each entry names a scenario document in the repository. Max items 50. |
| `source` | `none`, `otel`, `access_log` | no | Where the endpoint mix comes from. An OpenTelemetry trace export or a combined format access log, both read from a file named in source_config.path. Defaults to `none`. |
| `source_config` | object | no | Adapter specific settings. Both sources take a path: the OTLP/JSON trace export, or the access log. Credentials come from the secrets subsystem. Max properties 20. |
| `sql` | [SQL workload](#sql-workload) | no | A concurrent workload run directly against the branch's database, rather than through the application. |
| `thresholds` | object | no | What fails a SINGLE run. These are measured against production, or against the run's own responses, and never against the base branch: nothing here brings a second environment up, so no key in this object can see another build. The base branch comparison is load.comparison, which has its own thresholds. |
| `traffic` | [Traffic](#traffic) | no | The committed record of what production actually serves, which is the denominator every route in a load run is measured against. |
| `unsafe_routes` | list of string | no | Routes that mutate state destructively. They are included only against a fresh branch that is reset afterwards. Max items 500. |

## Load scenario

One journey document and how hard to run it.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `iterations` | integer | no | How many times each session walks it. Defaults to `1`. Minimum 1, maximum 1000. |
| `path` | string | **yes** | The scenario document, relative to the repository root. Max length 512. |
| `sessions` | integer | no | How many sessions walk the journey at once. Defaults to `1`. Minimum 1, maximum 1000. |
| `start_after` | string | no | Delay before this scenario starts, so one journey can burst while another is already running. Matches `^[0-9]+(ms\|s\|m)$`. |

## SQL workload

A concurrent workload run directly against the branch's database, rather than through the application. N clients, each on its own connection, executing whole transactions, so a change to an index, a lock or a query is measured in transactions per second and statement latency rather than through whatever the application does on the route you can reach. Declaring the block is what turns it on: 'af load sql' runs it and nothing else does, so there is no enabled flag for a command to ignore.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `clients` | integer | no | How many clients run at once, each on its own connection. The run refuses rather than running short handed if the server will not give it this many. Defaults to `8`. Minimum 1, maximum 1000. |
| `duration` | string | no | How long to run. Capped at fifteen minutes. Defaults to `60s`. Matches `^[0-9]+(s\|m)$`. |
| `max_statements` | integer | no | How many statements a derived mix may hold. The tail of pg_stat_statements is one call apiece and taking it makes a mix that costs more to set up than to run. Defaults to `20`. Minimum 1, maximum 200. |
| `script` | string | no | The workload document, relative to the repository root. Required under source declared and refused under statement_statistics, where the server supplies the statements. Max length 512. |
| `source` | `declared`, `statement_statistics` | no | Where the statement mix comes from. declared reads the document named by script. statement_statistics reads pg_stat_statements on the branch, so the mix is the traffic that really ran, weighted by how often it ran. Defaults to `declared`. |
| `think_time` | string | no | How long a client waits between transactions. Zero measures the server at saturation; a real wait measures it at the concurrency an application actually holds. Defaults to `0ms`. Matches `^[0-9]+(ms\|s)$`. |
| `thresholds` | object | no | What fails the run. Applied to this run's own measurements, never to an absolute throughput claim. |
| `transactions` | integer | no | How many transactions each client runs, the way pgbench's -t does. Set it instead of a duration for a run whose size is the same on every machine. Minimum 1, maximum 1e+06. |
| `writes` | boolean | no | Whether a derived mix may include statements that change data. Off by default, because pg_stat_statements normalises the values away and replaying a write would write values nobody chose. It does not apply to a declared workload, whose author wrote the values. Defaults to `false`. |

## Migrations

Where the project's own SQL migrations live, for a project whose migrate command is its own script rather than a tool the rehearsal recognises. Declared, the rehearsal replays the files in this directory and nothing is inferred from the tree.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `dir` | string | **yes** | Directory of .sql files, relative to the repository root, applied in filename order. A directory of numbered files such as 0042_add_index.sql is also recognised without this key when no tool is; declaring it removes the guess. Max length 512. |
| `format` | `sql` | no | How the files are read. Only sql exists. Defaults to `sql`. |
| `table` | string | no | The ledger table the project's runner records applied files in, so the rehearsal computes the pending set the way the runner would: a file is applied when its name, its stem or its leading number appears in the table's name, version, filename or migration column. Unset, schema_migrations and migrations are tried. Max length 128, matches `^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`. |

## Oracle

Deploy a baseline version alongside the candidate, send both the same requests, and report every difference in what came back and in what ended up in the database.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `base_ref` | string | no | The git ref the baseline comes from, or the ref the merge base is taken against. Empty tries origin/HEAD, then origin/main, then origin/master, and says which it used. Max length 256. |
| `baseline` | `merge_base`, `ref` | no | How the version to compare against is chosen. merge_base answers what this branch changes; ref answers what changes when it ships. Defaults to `merge_base`. |
| `compare_timestamps` | boolean | no | Compare timestamp strings exactly instead of treating two well formed timestamps as equal. Turn it on when timestamps come from the data rather than from the clock. Defaults to `false`. |
| `compare_uuids` | boolean | no | Compare UUIDs exactly instead of treating two well formed UUIDs as equal. Turn it on when identifiers are stored rather than generated per request. Defaults to `false`. |
| `database` | [Oracle database](#oracle-database) | no | The comparison of the two branches' contents. |
| `enabled` | boolean | no | Whether the comparison runs. Present but false is how a project keeps its probe plan and turns the check off for a while. Defaults to `true`. |
| `fail_on` | `none`, `minor`, `major`, `critical` | no | The lowest severity that fails the command. critical is a request the baseline served and the candidate did not, a status that fell into an error class, or a row the baseline wrote and the candidate did not. Defaults to `critical`. |
| `ignore` | [Oracle ignore](#oracle-ignore) | no | What the comparison is told not to look at. |
| `probes` | list of [Probe](#probe) | no | The requests sent to both versions, in order, byte for byte the same on each side. Max items 200. |

## Oracle database

The comparison of the two branches' contents.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | boolean | no | Defaults to `true`. |
| `exclude` | list of string | no | Tables to leave out, applied after tables. Max items 500. |
| `max_rows` | integer | no | How many rows a table may hold and still be compared. A table over the bound is reported as not compared, never silently skipped. Defaults to `10000`. Minimum 1, maximum 1e+06. |
| `tables` | list of string | no | Tables to compare. Empty compares every table. A pattern is schema.table, and either half may be an asterisk. Max items 500. |

## Oracle ignore

What the comparison is told not to look at. Everything here is printed in the report along with the defaults, because an oracle that silently ignores a field is worse than one that reports it.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `fields` | list of string | no | JSON paths to skip, in a response body and in a table row alike. $.token, $.orders[*].placed_at and $..created_at are all accepted. Max items 200. |
| `headers` | list of string | no | Response headers to skip, in addition to the defaults. Max items 100. |

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
| `sign_in_path` | string | no | Where this persona's sign-in form lives, when it is not where the workflow starts. The runner looks for a form at the workflow's start path first and then at the usual paths, which finds the wrong form for a persona whose sign-in surface is elsewhere on the same origin, such as an operator portal beside a customer console. Max length 512. |
| `tenant` | string | no | The account boundary this persona belongs to, an identity label only. It is never a credential and does not change how the persona is provisioned or signs in; the security suite's access-probe pass reads it to decide a cross-tenant reach. Absent when the application has no tenant boundary. Max length 128. |

## Personality

One personality that may drive a workflow, selected from the built in catalogue by id and optionally reweighted. A personality is a behavioral lens, not an account: it changes which listed control an agent prefers and how it phrases its reasoning, never the set of controls the page offers.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `id` | string | **yes** | The built in personality this entry selects: one of explorer, fast_actor, cautious_analyst, goal_oriented, distracted, skeptic, text_oriented, visual_follower, keyboard_user, edge_case. Max length 40, matches `^[a-z0-9]([a-z0-9_-]{0,38}[a-z0-9])?$`. |
| `weight` | number | no | How likely this personality is to be drawn, relative to the others listed. Weights are renormalized to sum to one hundred. Defaults to the personality's built in population weight. Minimum 0, maximum 100. |

## Policy

What each class of finding does to the pull request check. A finding at 'fail' fails the check, one at 'warn' is reported and the check still passes, and one at 'ignore' is not reported at all. Every key here is read when the report is built, so the answer to why a check failed is always one of these keys.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `cleanup` | `ignore`, `warn`, `fail` | no | Teardown left a resource behind. The journal remembers what is left, so 'af down' can finish the job. Defaults to `fail`. |
| `egress_surprise` | `ignore`, `warn`, `fail` | no | The environment tried to reach a host the manifest does not mention. The request was refused either way; this decides whether the attempt stops the merge. Defaults to `fail`. |
| `load_regression` | `ignore`, `warn`, `fail` | no | A load threshold from the load block being exceeded. Defaults to `warn`. |
| `masking` | `ignore`, `warn`, `fail` | no | The environment's own branch read back with something in it that still parses as real data. Defaults to `fail`. |
| `migration_failed` | `ignore`, `warn`, `fail` | no | A migration that did not apply to a branch with production's shape in it. A migration that fails here is one that would have failed in production. Defaults to `fail`. |
| `migration_lint` | `ignore`, `warn`, `fail` | no | Any of the seventeen migration lint rules. They share one setting because the rules are already scoped by table size. Defaults to `warn`. |
| `migration_lock` | object | no | How long a migration may hold a lock on a table. Both figures are compared against a sampled lower bound, so a breach really did hold the lock at least that long. |
| `migration_rewrite` | `ignore`, `warn`, `fail` | no | A statement Postgres reported as rewriting a table, which copies every row under a lock nothing can read through. Defaults to `warn`. |
| `plan_regression` | `ignore`, `warn`, `fail` | no | A query plan that got worse in one of three plan regressions: a table is now read end to end, an index is no longer used, or the planner's estimate grew. Defaults to `warn`. |
| `query_regression` | `ignore`, `warn`, `fail` | no | A statement that runs more often, or slower, than the saved baseline did. Needs a baseline to compare against. Defaults to `warn`. |
| `review` | `ignore`, `warn`, `fail` | no | A finding from the static code reviewer, the model-backed lane that reads the change's added lines and reports the correctness defects a diff introduces: an off-by-one, a nil dereference, an unhandled error, a boundary the new code does not hold, a new code path with no caller. It defaults to warn rather than fail because the reviewer is an LLM reading a diff and its findings are probabilistic, so it advises without blocking a merge on a model's say-so. Raise it to fail once the project trusts it, or set it to ignore to drop the findings. The reviewer runs only when the change touched code and only when a model key is configured; with no key it is skipped and the run says so. Defaults to `warn`. |
| `security` | object | no | What each dynamic security check finding does to the pull request check, keyed by the finding's rule such as security.authz.idor or security.headers.cookie_not_secure. A finding at fail stops the merge, one at warn is reported and the check still passes, and one at ignore is dropped. The keys are open on purpose: the legal ones are the keys the security check families declare, which the engine knows and this document does not, so a key set here that no family reads is carried until the family that reads it lands. Only the level is constrained, the same three values every other policy key takes. |
| `workflows_unverified` | `ignore`, `warn`, `fail` | no | A run in which no workflow reached a verdict about the application, because every one was blocked or unverified or because none was declared. Distinct from a single blocked workflow, which is never counted against the application: one gap in the tooling is not evidence, and a run where every workflow was a gap has tested nothing at all, so reporting it as a pass says the application was checked when it was not. Set it to warn if the project has no workflows yet and you would rather record that choice than be told about it. Defaults to `fail`. |

## Probe

One request sent to both versions.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `body` | string | no | The request body, sent byte for byte to both sides. Max length 65536. |
| `headers` | object | no | Headers sent on both sides. Credentials come from the secrets subsystem, never from here. Max properties 20. |
| `method` | `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS` | no | Defaults to `GET`. |
| `name` | string | **yes** | Identifies the request in the report. Min length 1, max length 64, matches `^[a-z0-9][a-z0-9-]*$`. |
| `path` | string | **yes** | The path and query, starting with a slash. Min length 1, max length 2048. |

## Resources

The size one instance of this service is given. Each value is both the request and the limit, so the service gets what it asked for and takes no more. Omit either key to leave that dimension uncapped.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `cpu` | string | no | CPU for one instance, as a number of cores or as thousandths with an m: 2, 0.5, 500m. On Kubernetes it is the request and the limit, which puts the pod in the Guaranteed class; on the local runtime it is the daemon's own cpu constraint. A value the runtime cannot place is refused with AF-RUN-047 naming the shortfall, rather than accepted and left Pending. Matches `^[0-9]+(\.[0-9]+)?m?$`. |
| `memory` | string | no | Memory for one instance, with a unit: 512Mi, 2Gi. Mi and Gi are powers of two, M and G powers of ten. A bare number is refused, because nobody who writes 512 means 512 bytes. On Kubernetes it is the request and the limit; on the local runtime it is the daemon's memory constraint, so a service over it is killed rather than allowed to take the machine down. Matches `^[0-9]+(Mi\|Gi\|M\|G)$`. |

## Rolling compatibility

Run the previous release against the migrated schema and see whether its workflows still pass, which is the invariant a rolling deploy actually depends on.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `against` | string | no | Which commit the previous release is: merge-base, previous-commit, or any revision git can resolve, such as a release tag. Defaults to `merge-base`. Max length 256. |
| `when` | `never`, `risky`, `always` | no | risky runs the check only when the pending migrations contain a change the previous release could notice, such as a dropped or renamed column. always runs it for every migration, and costs a second image build and a second environment every time. Defaults to `risky`. |

## Runtime

Where and how long the environment runs. The provider decides the machinery; the rest is the lifetime, the address, and the naming the environment gets.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `domain` | string | no | Wildcard domain for environment hostnames. Defaults to localhost, which needs no DNS at all. Defaults to `localhost`. Max length 253. |
| `idle_sleep` | string | no | How long an environment may sit idle before it is scaled to zero. It wakes on the next request. Defaults to `30m`. Matches `^[0-9]+(m\|h)$`. |
| `kubeconfig_context` | string | no | Which kubeconfig context to use. Naming it prevents an environment landing on whatever cluster happened to be current. Max length 253. |
| `max_ttl` | string | no | The furthest af env extend may push an environment's expiry, measured from when it was created. A lifetime that can be extended forever is not a lifetime, and this is the bound. Defaults to `168h`. Matches `^[0-9]+(ms\|s\|m\|h\|d)$`. |
| `namespace_prefix` | string | no | Prefix for Kubernetes namespaces. Defaults to `af`. Max length 40. |
| `provider` | string | no | Which runtime places the environment. local and kubernetes are built in. Open rather than a fixed list, for the reason datastore.engine is: a build registers the runtimes it carries, so a manifest naming one this build has no runtime for is refused by the provider lookup, by name, against the runtimes that build actually has, which says more than an unknown value would. Defaults to `local`. Max length 64. |
| `requires` | object | no | What a target must offer for this repository to be placed on it, as attribute equals value matched against a target's tags. Empty means anywhere. A requirement no declared target satisfies is refused at validation, because both are in this file. Max properties 16. |
| `targets` | list of [Runtime target](#runtime-target) | no | The places an environment may be placed, in preference order. Empty means the single runtime the provider names, which is every manifest written before placement existed. Max items 32. |
| `ttl` | string | no | How long an environment lives before the reaper tears it down. Extend one you are still using with af env extend, up to max_ttl. Defaults to `24h`. Matches `^[0-9]+(ms\|s\|m\|h\|d)$`. |

## Runtime target

One place an environment may be placed. A runtime plus the facts about where it is, and the second half is the part no runtime supplies for itself: a kubeconfig context is a name on somebody's laptop and it does not say which region the cluster is in.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `domain` | string | no | Wildcard domain for environments placed here. Omitted inherits runtime.domain. Max length 253. |
| `kubeconfig_context` | string | no | Which cluster this target is. Two targets resolving to the same cluster are refused, because a placement decision between them decides nothing. Max length 253. |
| `name` | string | **yes** | Unique within the manifest. It names the target in the placement decision and in the refusal when none will do. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `namespace_prefix` | string | no | Prefix for Kubernetes namespaces on this target. Omitted inherits runtime.namespace_prefix. Max length 40. |
| `provider` | `local`, `kubernetes` | no | The runtime this target uses. Omitted inherits runtime.provider, which is what lets a fleet of clusters be one provider line and a list of contexts. |
| `tags` | object | no | What this target offers, matched against runtime.requires. The region tag is also what fills the organization policy hook's residency check. Max properties 16. |

## Security

Fixtures the dynamic security suite needs and the engine cannot infer from a diff. Off by default: absent, or present with no access block, runs the suite exactly as before and pays nothing for access probing.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `access` | object | no | The ownership-scoped objects the authenticated authorization differential reaches as each persona. Absent means no access probing. |

## Service

One process the environment runs. A service is built from the repository, given the variables it declared, attached to the environment's private network, and started in dependency order.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `build` | [Build](#build) | no | How to turn the service directory into an image. |
| `command` | string | no | Command that starts the service, overriding the image's own. Executed with an argument vector, never through a shell. Max length 4096. |
| `depends_on` | list of string | no | Services that must be ready first. A cycle is rejected at validation. Max items 50. |
| `env` | list of [Environment variable](#environment-variable) | no | Names of environment variables this service needs. Names only. Values come from the secrets subsystem, and a name with no value anywhere fails with AF-SEC-001 rather than starting a service that will misbehave. Max items 200. |
| `health_path` | string | no | HTTP path that reports readiness. A service is not considered up until this returns a 2xx or 3xx status. Defaults to `/`. Max length 512. |
| `health_timeout` | string | no | How long to wait for readiness before failing with AF-RUN-004. Defaults to `180s`. Matches `^[0-9]+(ms\|s\|m)$`. |
| `kind` | `web`, `worker`, `cron` | no | What the service is. A web service gets a hostname and a readiness check; a worker gets neither; a cron service is invoked on a schedule instead of run continuously. Defaults to `web`. |
| `migrate` | string | no | Command that applies pending migrations. Run once against a fresh branch before the services start, and rehearsed with timing and lock analysis when insights are on. Max length 1024. |
| `name` | string | **yes** | Unique within the manifest. Appears in hostnames, logs, and container names. Max length 40, matches `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`. |
| `path` | string | no | Directory containing the service, relative to the repository root. Defaults to the root. A path outside the repository is rejected. Max length 512. |
| `port` | integer | no | Port the service listens on. Required for a web service unless detection found it. Minimum 1, maximum 65535. |
| `replicas` | integer | no | How many instances of this service to run. Both runtimes start this many, behind the one name other services resolve, so a bug that only appears at more than one instance appears here. Omitted means one. A cron service may not ask for more than one, because a scheduled job that runs on three instances runs three times. Minimum 1, maximum 10. |
| `resources` | [Resources](#resources) | no | The size one instance of this service is given. |
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

## Terminal screen

The size of the screen the program draws, and its presence is what says the program draws one.

A program that takes over the screen is driven through a pseudo terminal: it is given a real terminal, it is sent raw keystrokes rather than lines, and it is judged on the grid of cells its cursor moves and erases leave behind rather than on the bytes it wrote. A program that only prints is driven through a pipe and judged on everything it printed. Leaving this out is the second one.

The distinction is not a preference. A pseudo terminal echoes what is typed into it, so a program that has not turned echo off shows the driver's own keystrokes on its screen, and an expectation naming them would be satisfied by the workflow rather than by the program. Say a program draws a screen when it does, and not otherwise.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `cols` | integer | no | How many columns the terminal has. Text past it wraps or is truncated by the program, which is the behaviour a narrow terminal is worth testing for. Defaults to `80`. Minimum 20, maximum 500. |
| `rows` | integer | no | How many rows the terminal has. A program lays its screen out from this, so a narrow one and a tall one are different tests of the same program. Defaults to `24`. Minimum 4, maximum 200. |

## Terminal workflow

One thing the agents do at a command line: a program to run, what a person types at it, and what the terminal must show.

WHY THIS IS ITS OWN LIST rather than a `surface` key on `workflows`. The two surfaces share the sentence and nothing else. A browser workflow needs a persona to sign in as, a path to start at and a step budget; a terminal workflow needs a program, its arguments, and the size of the screen it draws. Putting both in one entry would mean half of every entry's keys are refused by the other half's surface, which is a conditional this schema has nowhere else and which a reader would have to hold in their head on every field. The list a workflow is written in says which surface it drives, and that is a fact a person can see.

Names are unique across both lists, because a name is what `--only` selects and what the report prints, and two workflows answering to one name is a run nobody can read.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `args` | list of string | no | The arguments, one per entry. Passed to the program as written and never through a shell, so a space in a value is part of that value and nothing is expanded behind your back. Max items 64. |
| `budget` | object | no | What this workflow may spend. Only a duration, because the two other things a browser workflow spends do not exist here: there are no steps to count, the keys are written down rather than decided, and no model is asked anything, so there is no cost to cap. |
| `command` | string | **yes** | The program to run. Resolved against the working directory and the PATH the engine runs with, so `./bin/deploy` and `psql` both work. Min length 1, max length 512. |
| `cwd` | string | no | Where to run it. Relative paths are resolved against the directory holding the manifest. Defaults to that directory. Max length 512. |
| `description` | string | **yes** | What a person would do and what proves it happened, in sentences. It is what a reader of the report is told this workflow was for. Min length 10, max length 4000. |
| `expect` | list of string | **yes** | What the terminal must show, written as sentences about what a person would read. Judged against every screen the program drew and the scrollback it left behind, not against the bytes it wrote.  At least one is required here, unlike a browser workflow. A terminal workflow with nothing to expect can only ever report that nothing confirmed or contradicted it, which is blocked, so a manifest that declares one has written a workflow that cannot pass.  A sentence in double quotes is required on the screen character for character. Prefer that form here: a screen is small and its words repeat, so the sense of a sentence is matched far more easily on eighty columns than on a page. Min items 1, max items 50. |
| `input` | list of string | no | What a person types, in order.  Without `screen` each entry is a line written to standard input, followed by a newline.  With `screen` each entry is keystrokes sent to the program as a keyboard would send them. Text is typed as written, and a name in angle brackets becomes that key: `<enter>`, `<tab>`, `<esc>`, `<space>`, `<backspace>`, `<delete>`, `<insert>`, `<up>`, `<down>`, `<left>`, `<right>`, `<home>`, `<end>`, `<pageup>`, `<pagedown>`, `<f1>` through `<f12>`, `<backtab>`, and `<ctrl-a>` through `<ctrl-z>`. Anything else between angle brackets is typed literally, so a workflow that types `<html>` into a field gets `<html>` and there is no escape syntax to learn. After every entry the driver waits for the program to redraw and reads the screen, so an expectation may name something that was only on screen in the middle of the workflow. Max items 200. |
| `name` | string | **yes** | What the report calls it and what the `--only` flag selects. Unique across this list and `workflows` together. Max length 64, matches `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `screen` | [Terminal screen](#terminal-screen) | no | The size of the screen the program draws, and its presence is what says the program draws one.  A program that takes over the screen is driven through a pseudo terminal: it is given a real terminal, it is sent raw keystrokes rather than lines, and it is judged on the grid of cells its cursor moves and erases leave behind rather than on the bytes it wrote. |

## Traffic

The committed record of what production actually serves, which is the denominator every route in a load run is measured against. Without one safe_routes is a list written from memory and nothing says how much of production it misses. Measured on this repository on 2026-09-06: a migration held an exclusive lock on nine relations for thirty seconds and the run over four hand written routes reported 0.0 percent failed, because none of the four reads the locked table.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `max_age` | string | no | How old the profile may be before it is refused. A stale profile is not a smaller number, it is an unknown one, so it is refused the way a stale golden is rather than quoted. Fourteen days by default rather than the volume profile's thirty, because an endpoint mix moves at the rate a team ships rather than at the rate a business grows. Defaults to `336h`. Max length 32, matches `^[0-9]+(ms\|s\|m\|h\|d)$`. |
| `profile` | string | **yes** | The profile file, relative to the repository root. Written by af traffic record from an OpenTelemetry trace export or a combined format access log, and committed, because the machine that reads it on a pull request cannot reach production. It carries the endpoint mix, the arrival rate, the peak concurrency and the per route p95, and no request body, header, query string or identifier. Max length 512. |

## Volume

The committed record of what production holds, which is the denominator every row count in a report is measured against. Without one the fidelity report says a branch holds twelve tables over a hundred thousand rows and has nothing to compare that against, so a golden built from a staging database with two hundred rows in it reports as reproducing a production holding four billion.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `max_age` | string | no | How old the profile may be before it is refused. A stale profile is not a smaller number, it is an unknown one, so it is refused the way a stale golden is rather than quoted. Thirty days by default rather than the golden's seven, because a profile is the shape of the data rather than the data. Defaults to `720h`. Max length 32, matches `^[0-9]+(ms\|s\|m\|h\|d)$`. |
| `profile` | string | **yes** | The profile file, relative to the repository root. Written by af volume record from a read only connection to production or a replica, and committed, because the machine that reads it on a pull request cannot reach production. It carries counts, sizes, partition shape and key cardinality, and no data. Max length 512. |

## Workflow

One thing the agents do, written as a goal rather than a script. The runner decides the actions and verifies the outcome, so a workflow survives a redesign of the page it happens on.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `budget` | object | no | What one workflow may spend. `steps` is the most actions one attempt may take: a workflow that uses every step passes if everything it expected is visible on the page it reached, fails if that page answered with an HTTP error, and otherwise ends as blocked with the step budget named. `duration` is the time the whole workflow may take, retries included: a workflow that reaches it is stopped where it is and ends as blocked with the budget named, and no further attempt starts. A blocked workflow is never a partial pass. |
| `description` | string | **yes** | What a person would do, in sentences. Say the goal and what proves it happened, not the selectors. Min length 10, max length 4000. |
| `expect` | list of string | no | Observations that must hold for a pass, written as sentences. These are assertions about what the user can see, not about the DOM. Max items 50. |
| `independent` | boolean | no | Whether this workflow can run at the same time as others. Workflows that share an environment run one at a time unless this says otherwise, because two agents mutating the same data produce failures nobody can reproduce. Defaults to `false`. |
| `name` | string | **yes** | Max length 64, matches `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `persona` | string | no | Which persona runs it. Defaults to the first persona. Max length 40. |
| `personality` | string | no | Pin one personality to this workflow rather than drawing from the diversity mix. One of the built in ids: explorer, fast_actor, cautious_analyst, goal_oriented, distracted, skeptic, text_oriented, visual_follower, keyboard_user, edge_case. This is the HOW the agent behaves and is independent of persona, the WHO it signs in as. Absent means the personality is assigned from the mix by the seed, which is the usual case. Read only when diversity is enabled. Max length 40, matches `^[a-z0-9]([a-z0-9_-]{0,38}[a-z0-9])?$`. |
| `personas` | list of string | no | The personas this workflow signs in as, in order, in one browser, for a person who holds more than one session at once: an operator who is also a customer, an account with a second sign-in surface. Each is signed in through its own strategy and the sessions accumulate; the last one named is the identity the workflow acts as. Mutually exclusive with persona. Min items 1, max items 5. |
| `start_path` | string | no | Where to begin. Defaults to the application root. Defaults to `/`. Max length 512. |
| `surface` | `web`, `terminal`, `desktop`, `ios`, `android` | no | What this workflow drives. Defaults to `web`, which is a browser.  Every surface the product knows is named here, including the ones a given build cannot drive yet, and that is the same decision `runtime.provider` documents. A build registers the drivers it carries, so a manifest naming a surface this build has no driver for is refused BY NAME, against the surfaces that build actually has, which tells a person far more than a schema saying the value is unknown. The refusal happens twice on purpose: the engine says it at validation, so the answer is immediate, and the runner says it again before it drives anything, so a surface nothing drove can never come back green.  Write `terminal` in `terminal_workflows` rather than here. A terminal workflow needs a program to run where this one needs a persona to sign in as, so the two do not share an entry; naming it here is refused with that sentence rather than treated as a typo.  This is not `change.rules[].surface`, which says what a changed FILE is. This says what a workflow DRIVES. Defaults to `web`. |
| `tags` | list of string | no | Labels for the person reading the manifest, and nothing else. The engine does not read them: no command selects workflows by tag and no report prints one, so grouping workflows here groups them for a reader and not for a run. Name the workflows with --only to run a subset. This key had no description at all until somebody counted the fields nothing reads, which is how a label and a broken promise came to look alike. Max items 20. |


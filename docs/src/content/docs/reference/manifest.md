---
title: Manifest reference
description: Every block in antifailure.yaml, what it does, and what happens when it is wrong.
sidebar:
  order: 2
---

`antifailure.yaml` sits at the repository root. `af init` writes one from what
is already in the repository; nothing regenerates it afterwards, so an edit you
make survives.

The rule worth knowing before reading anything else: an environment can reach
nothing on the network except the hosts listed under `egress`, each in the mode
named. Everything else is refused with a decision you can read.

A manifest declaring `version: 1` keeps working for the whole of version 1 of
Antifailure. Keys are added and existing ones gain new accepted values; a key is
not removed, renamed, or given a different meaning without a major version.
[What is stable](/docs/reference/stability) is the whole commitment, including
what it deliberately does not cover.

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
| `change` | block | Path rules for [change analysis](/docs/concepts/change-analysis), for a layout the built in rules do not predict. |
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
| `replicas` | int | How many instances to run, 1 to 10. Both runtimes start this many behind the one name other services resolve. See below. |
| `depends_on` | list | Other services that must start first. |
| `env` | list | Variables this service needs, by name. |
| `resources` | block | `cpu` and `memory`, the size one instance is given. Each is the request and the limit on both runtimes. See below. |
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

### `replicas`

```yaml
services:
  - name: roller
    kind: worker
    replicas: 3
```

Three containers, or three pods, behind the one name every other service
resolves. Requests and lookups spread across them.

This is not a scale knob. An environment is a copy of production on one
machine, and nobody needs three copies of a worker for throughput there. What
more than one instance buys is a class of bug that cannot be reproduced at one
and is expensive in production:

- a nightly job with no leader election, which sends its email once per
  instance
- a queue consumer that reads a row and then claims it, so two instances
  process the same piece of work
- a session, a cache or a rate limiter held in one process's memory, which the
  next request does not reach
- a migration that is safe against one writer and not against three

Every one of those passes at one instance. That is the point: a service that
runs one container whatever the manifest says reports a green run to somebody
who wrote `replicas: 3` precisely because they suspected one of these, and the
green run reads as the bug being absent.

The migration runs once for the service, not once per instance. The ingress is
one forwarder for the service, not one per instance. Readiness waits for every
instance, so a service reported ready is not one that is two thirds up.

`af status` names the count when it is more than one, and the fidelity report
says how many instances are running against how many were asked for.

The bound is 1 to 10, and a `cron` service may not ask for more than one: every
instance runs the schedule, so three instances send the nightly email three
times, which is a bug to reproduce inside a service rather than the meaning of
a manifest key.

### `resources`

```yaml
services:
  - name: clickhouse
    kind: worker
    resources:
      cpu: "2"
      memory: 4Gi
```

The size ONE instance is given. A service asking for `replicas: 3` and `2` of
CPU asks the machine for six cores, not two.

`cpu` is a number of cores, or thousandths with an `m`: `2`, `0.5`, `500m`.
`memory` needs a unit: `512Mi`, `2Gi`. `Mi` and `Gi` are powers of two, `M` and
`G` powers of ten, which is what those suffixes mean in a Deployment and what
somebody copying a value out of one expects. A bare `memory: 512` is refused,
because Kubernetes reads it as 512 bytes and nobody who writes it means that.

**Each value is the request AND the limit**, not a request with a larger limit
behind it. On Kubernetes that is the Guaranteed quality of service class. The
familiar shape, a small request under a large limit, is where a node gets
oversubscribed: every container is placed against its request and then grows
into its limit, so a machine that fits ten environments on paper runs eleven
and the eleventh takes memory from the others. The symptom is a workflow that
reads as flaky, and a twin whose failures belong to the machine rather than to
the change under test is worth less than no twin. One number also means
environments per node is a division rather than a guess.

On the local runtime there is no scheduler to reserve anything, so the value is
the daemon's own cpu and memory constraint: the container gets that share under
contention and no more, and one over its memory cap is killed rather than
allowed to take the machine down with it.

Omitting a key leaves that dimension uncapped, which is what every service had
before the key was honoured, so an existing manifest produces the identical
container and the identical Deployment it did before. The two keys are
independent: a service may cap CPU alone, memory alone, or neither.

**A size the runtime cannot place is refused before anything is created**, with
**AF-RUN-047** naming the shortfall. Without that, a request larger than any
node is accepted by the API server, the pod sits `Pending` with an event nobody
is watching, and `af up` waits out its readiness timeout and reports a service
that did not start, which reads as a slow cluster. On a cluster the check is
against allocatable minus what the pods already there requested, so a full
cluster refuses rather than accepts. It is a necessary condition and not a
sufficient one: it refuses the sets for which no placement exists, and leaves
bin packing to the scheduler.

A service's `migrate` command runs under the same cap as the service. It does
not double what the environment asks the machine for, because the migration
finishes before the service starts. A migration that needs more memory than the
service it belongs to is a case this key cannot express today.

`af status` reports the size the runtime ACTUALLY applied, read back off the
running pod or the daemon's record of the container rather than echoed from
the manifest. A runtime that accepts a cap and emits none would otherwise
report exactly what a correct one reports.

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
| `provider` | `docker` (default), `neon`, `supabase`, `dblab`, `pgurl`, or `aurora`. `aurora` is in the enterprise edition; a community build names it and refuses it. |
| `version` | Postgres major, 14 through 18, default 17. Match it to production: a golden on a different major is an environment running a Postgres your application does not. |
| `url_env` | The variable services receive the connection string in. |
| `source_url_env` | Names the variable holding production's read only URL. |
| `masking_rules` | Path to the rules, default `masking.yaml`. |
| `seed` | A command run against a fresh golden candidate. |
| `project` | For a hosted provider, its project identifier. `pgurl` has none and refuses one. |
| `api_key_env` | Names the variable holding that provider's API key. For `pgurl` it names the connection string of the server the goldens and branches live on, which is the credential in that case. |
| `max_branches` | The plan's concurrent branch limit. |
| `golden` | `schedule`, `max_age`, `retain`, `storage`, `storage_url`. |
| `subset` | See below. |
| `migrations` | See below. For a project that applies its own directory of SQL files. |
| `volume` | See below. The committed record of what production holds. |

### `volume`

```yaml
  volume:
    profile: .antifailure/volume.json
    max_age: 720h
```

The denominator. Without it a fidelity report can say a branch holds twelve
tables over a hundred thousand rows and has nothing to compare that against, so
a golden built from a staging database with two hundred rows in it reports as
reproducing a production holding four billion, in the same words and with the
same verdict as a full copy.

`af volume record` writes the profile from the database `source_url_env` names.
It reads no row: row counts, table and index sizes, partition counts and how
much sits in the largest partition, and the cardinality of every column
anything joins on, all of it from `pg_class`, `pg_stats` and the partition
catalogs. That is why a read only role on a replica is enough, and why the
result is safe to commit, which it has to be: the check running on a pull
request cannot reach production.

With a profile, the database dimension states the fraction per table, and the
migration rehearsal states what a lock it measured would cost at production's
row counts, labelled as an extrapolation rather than printed as a second
measurement.

`max_age` defaults to `720h`, thirty days. A profile older than that is refused
rather than quoted, the same way a stale golden is refused rather than
branched: a stale denominator is not a smaller number, it is an unknown one.
Thirty days rather than the golden's seven because a profile is the shape of
the data rather than the data, and it moves at the rate a business grows.

### `migrations`

```yaml
  migrations:
    dir: web/packages/db/migrations
    format: sql
    table: schema_migrations
```

The migration rehearsal recognises Prisma, the Supabase CLI, Drizzle, Flyway,
Rails, Django, Alembic and Knex from their marker files, and a directory of
numbered `.sql` files from the files themselves. A project that applies such a
directory with a script of its own, `node migrate.mjs` say, has no marker to
recognise, and without this block the rehearsal has to find the directory by
searching the tree. Declaring it removes the search: the files in `dir` are
replayed in filename order, each statement timed, and nothing is inferred.

`table` names the ledger the script records applied files in, so the pending
set against a branch is computed the way the script computes it. A file counts
as applied when its name, its stem or its leading number appears in the
table's `name`, `version`, `filename`, `migration` or `id` column. Left unset,
`schema_migrations` and `migrations` are tried, and a branch with neither is
reported as one where every file is pending. `format` has one value, `sql`,
and it is the default.

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

## `datastores`

Every store the environment holds, and what is done about each one's contents.

```yaml
datastores:
  - name: events
    engine: clickhouse
    stance: golden

  - name: cache
    engine: redis
    stance: empty
    because: a cache is rebuilt from the primary and a copy would be noise

  - name: search
    engine: elasticsearch
    stance: derived
    from: primary

  - name: bus
    engine: kafka
    stance: topics_only
```

| Key | Notes |
| --- | --- |
| `name` | Unique, and usable as a hostname. `primary` is reserved. |
| `engine` | What the store runs: `postgres`, `clickhouse`, `redis`, `kafka`, `elasticsearch` and so on. Open rather than a fixed list. |
| `provider` | Which implementation provides the engine, where more than one can. |
| `stance` | Required. See below. |
| `because` | Why that stance was chosen, carried into the fidelity report as written. Required for `empty`. |
| `from` | The store a `derived` one is rebuilt from. Required for `derived` and refused for the rest. |
| `source_url_env` | The NAME of the variable holding this store's production connection string, which a `golden` is copied from and which the cross store check reads the schema from. Never the connection string itself, which is refused. Omitted, the golden holds no rows and every refresh says so. |

The `database` block above is not replaced and does not move. It normalizes
into the entry named `primary`, so a manifest that declares only `database:`
already has a datastores list and never has to write one, and every later part
of the engine reads one list rather than a struct and a list.

### The stances

| Stance | What happens |
| --- | --- |
| `golden` | A masked, verified copy that environments branch from, which is what `database:` has always meant. |
| `empty` | The store starts with nothing in it, on purpose, and `because` says why. |
| `derived` | The store is rebuilt from the one named in `from`, once that one is ready. |
| `topics_only` | Topics and consumer groups are created, with no messages. |

**There is no default, and a datastore that declares no stance is refused.**
That refusal is the point of the key. Not every store should be cloned: a cache
is correct to start empty and copying one would be copying noise and calling it
fidelity, and a broker usually wants topics rather than a replay of production
traffic. So the right answer differs per store and only the person writing the
manifest knows it.

What a default would do instead is choose silently, once per manifest. An
analytics product's twin held a masked Postgres and zero events, because the
events live in ClickHouse and ClickHouse came up empty; nobody decided that,
every query path that mattered was tested against a store with nothing in it,
and the run went green. `empty` is a legitimate answer. An invisible `empty` is
not, which is why it is written down and why `because` is required with it.

### What this build does with them

**A ClickHouse declared `golden` is refreshed, masked, verified and branched**,
beside the primary database and by the same commands. `af golden refresh` makes
a golden of every store the manifest declares as well as of the database, and
`af up` branches each of them into the environment. The masking rules are one
`masking.yaml` for the whole twin, so a rule about `distinct_id` covers the
column wherever it is and one customer masks to one fake customer in both
stores; the verification scanner reads the second store back with the same
detectors, and a golden that fails it is never published and can never be
branched.

Every other stance, and every other engine, is still declaration only: the
validator refuses a store with no stance and the
[component inventory](/docs/concepts/inventory) names each one, and nothing here
starts an `empty` store, runs a `derived` rebuild or creates a topic. A store
whose engine this build cannot mask is REFUSED rather than published unmasked.

### Checking that one person is one person in both stores

The paragraph above says one customer masks to one fake customer in both
stores. `af mask crossstore` is what checks it rather than asserting it:

```
af mask crossstore
```

It reads each declared store's catalog through the variable its
`source_url_env` names, assigns the one `masking.yaml` to all of them, finds
every identifier that appears in more than one store, masks probe values
through each side, and reports the share that come out identical.

**It reads catalogs and no rows**, which is why it is safe to point at
production: the probe values are its own, so what it needs from a store is the
schema. `rows_read` is a field of the report rather than a promise on this
page, and a live test reads the ClickHouse server's own `system.query_log` back
and fails if any statement the check sent selected from a data table. See
[masking](/docs/concepts/masking) for what it finds.

Every store it could not read is named with the reason, a store that names no
`source_url_env` is named as never read at all, and a run that reached one store
says it proved nothing rather than reporting a hundred percent of one. A pair
that disagreed and a store that was never opened carry different exit codes,
because one is a statement about your data and the other is a statement about
what could be reached.

**The fidelity report reads the branch**, not the declaration. A store declared
`golden` that this environment branched is reported the way the primary
database is, with its golden, its attestation, its tables and its rows; one the
environment has not branched is `absent`, and the report names those same four
things as the ones it does not have. Until that was true the dimension was
built from the manifest alone, so it said `absent` about a store holding a
masked, verified copy of production, which understated a twin rather than
overstating one and was still an instrument saying something untrue about what
it could see.

What the report still cannot tell you about a branched store is whether what it
holds is what production holds. Nothing here records a second store's
production row counts, so that half is reported as an unknown with the reason
named rather than as a copy of production, which is the same rule
`database.volume` applies to the primary.

### Reaching a store from a service

Every service is given `AF_DATASTORE_<NAME>_URL` for each store the environment
provides, and the store answers to its own name on the environment's network:
an application already configured to talk to a ClickHouse called `events` finds
it at `events` with nothing changed.

A store the environment provides is not also started as a service. A manifest
that declares a datastore called `events` and a service called `events` is
declaring one thing twice, the service being how the store used to be started
and the datastore being what it holds, so the service is skipped and the run
says so. Without that there would be two ClickHouses on one network under one
name, and half the application's queries would go to the empty one.

The interface an implementation has to satisfy is `provider.Datastore`, and the
suite that decides whether one of them is finished is
`conformance.RunDatastore`.

## `egress`

| Key | Notes |
| --- | --- |
| `default` | Any mode: `block` (default), `allow`, `capture`, `mock`, `sandbox` or `synth`. |
| `allow_ipv6` | Off by default. |
| `rules` | See [egress](/docs/concepts/egress). |

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
| `workflows_unverified` | `fail` | No workflow reached a verdict about the application, because every one was blocked or unverified or because none was declared. |

See [verdicts](/docs/concepts/verdicts) for what each level does to the run
and to the exit code.
## `load`

The whole block is in [Load](/docs/concepts/load). One key is here because it
is the counterpart of `database.volume` above.

### `traffic`

```yaml
load:
  traffic:
    profile: .antifailure/traffic.json
    max_age: 336h
```

The committed record of what production actually serves. Without it
`safe_routes` is a list written from memory and nothing says how much of
production it misses. Measured on this repository on 2026-09-06: a migration
held an exclusive lock on nine relations for thirty seconds and the run over
four hand written routes reported 0.0 percent failed, because none of the four
reads the locked table.

`af traffic record` writes the profile from an OpenTelemetry trace export or a
combined format access log, both files a collector or a reverse proxy already
wrote. It carries the endpoint mix, the arrival rate, production's p95 per
route and the peak concurrency, and no request body, header, query string or
identifier. Nothing in it opens a socket and no application code changes, which
is why the result is safe to commit, which it has to be: the check running on a
pull request cannot reach production.

With a profile, the traffic dimension states what fraction of production's
requests the run actually sends and names the heaviest route it never touches,
the arrival rate is stated beside production's own, and `p95_increase` becomes
able to fire under a source that carries no durations of its own.

A profile past `max_age` is refused rather than quoted. Fourteen days by
default, where the volume profile's is thirty: an endpoint mix moves at the rate
a team ships, and a volume profile at the rate a business grows.

## `fidelity`

| Key | Notes |
| --- | --- |
| `enabled` | On by default. Turning it off means the inventory is not taken, which is not the same as everything having passed. |
| `require` | Dimensions every component of which must be reproduced: `services`, `database`, `third_party`, `auth`, `runtime`, `traffic`, `datastores`, `topology`. See [inventory](/docs/concepts/inventory). |

There is no threshold here. A single percentage hides the one dimension that
matters to a particular change, so what a manifest requires is a dimension by
name.

## `runtime`

| Key | Notes |
| --- | --- |
| `provider` | Which runtime places the environment. `local` and `kubernetes` are built in, and a build registers any others it carries. The schema keeps no list, the way `datastore.engine` keeps none: a name this build has no runtime for is refused by name, against the runtimes that build actually has, rather than substituted. |
| `ttl` | How long an environment lives. |
| `max_ttl` | The furthest `af env extend` may push an environment's expiry, measured from creation. |
| `idle_sleep` | Suspend after this long with no traffic. |
| `domain` | Wildcard domain for preview URLs. |
| `namespace_prefix` | Prefix for Kubernetes namespaces. |
| `kubeconfig_context` | Which cluster. Naming it stops an environment landing on whatever context happened to be current. |
| `requires` | What a target must offer for this repository, as tag equals value. See below. |
| `targets` | The places an environment may be placed, in preference order. See below. |

### Placement

Most repositories have one place environments run, name it in `provider`, and
never write either of the last two keys. `targets` is for the case where there
is more than one: two clusters in two regions, a pool with more memory, an
isolated pool for repositories that handle regulated data.

```yaml
runtime:
  provider: kubernetes
  domain: preview.example.com
  requires:
    region: eu-west-1
  targets:
    - name: frankfurt
      kubeconfig_context: eu-prod
      domain: eu.preview.example.com
      tags:
        region: eu-west-1
        class: standard
    - name: virginia
      kubeconfig_context: us-prod
      domain: us.preview.example.com
      tags:
        region: us-east-1
        class: standard
```

A target inherits `provider`, `domain`, `namespace_prefix` and
`kubeconfig_context` from the block above it, so a fleet of clusters is one
provider line and a list of contexts rather than the same four settings written
out per target. `af explain` prints each target with its tags and marks the one
this manifest would be placed on.

**The tags are declared here rather than discovered from the cluster**, and that
is deliberate. A kubeconfig context is a name on somebody's laptop and it does
not say which region the cluster is in. Putting the claim in the repository puts
it under review, next to the requirement that reads it.

**Placement is a pure function of this file.** The first target satisfying every
requirement wins, every time, on every machine. `af up`, `af status`, `af logs`
and `af down` each decide independently and have to agree: a placement that
consulted a cluster's health would send `af up` to one cluster and `af status` to
another the moment one of them was unreachable, and the second command would
report that your environment does not exist.

**A requirement nothing can satisfy is refused rather than ignored**, when the
manifest is read, before anything is dispatched:

- `requires` with no `targets`. There is one runtime, it carries no tags, and so
  nothing could ever match.
- A requirement no declared target offers. The message names what the targets do
  offer, because the fix is usually a typo in the value.
- Two targets with one name, or two Kubernetes targets resolving to one cluster.
  Choosing between two targets on one cluster decides nothing.

**The `region` tag is read by more than placement.** It is what fills the region
an organization policy's `allowed_regions` rule compares against, so a target
that carries one can be refused by a residency policy and a target that carries
none cannot be. See [policy](/docs/enterprise/policy).

**More than one target requires an enterprise license** carrying `multi_runtime`;
see [multiple runtimes](/docs/enterprise/runtimes). One target needs no license.
It decides nothing, it only says where the runtime you already had is, which is
what a residency policy reads.

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
[Forks](/docs/guides/github#forks).

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

Related: [detection](/docs/concepts/detection), [egress](/docs/concepts/egress),
[providers](/docs/providers/overview).

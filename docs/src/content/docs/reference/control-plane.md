---
title: Control plane configuration
description: Every environment variable the control plane reads, what it does, and what happens when it is missing.
sidebar:
  order: 7
---

The control plane reads its configuration from the environment and refuses to
start without what it needs, naming the variable that is missing. A process
that starts with a missing secret and fails on the first request that needs it
is a process that fails in production rather than at deploy time.

## Required

| Variable | What it is |
| --- | --- |
| `AF_DATABASE_URL` | The connection string the application uses. This is the unprivileged role, not the owner: it cannot run DDL, because a role that can `ALTER TABLE` can drop the policies that isolate tenants. |
| `AF_GITHUB_CLIENT_ID` | The OAuth App's client identifier. |
| `AF_GITHUB_CLIENT_SECRET` | The OAuth App's client secret. |
| `AF_GITHUB_REDIRECT_URI` | Where GitHub returns the browser after sign in. Must match what the App is configured with exactly. |

## Optional

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_PORT` | `8080` | The port to listen on. |
| `AF_POOL_MAX` | `10` | Connections in the application pool. |
| `AF_APP_BASE_URL` | unset | The public origin, used to build absolute links. |
| `AF_SIGNIN_ALLOWLIST` | unset | GitHub logins, comma or whitespace separated, that may sign in. Unset means any GitHub account may sign in, which is the right default for an installation whose network already decides who reaches it. **Set but empty means nobody**, not everybody: a deployment that lost this value should close, not open. The mode is printed at startup. |
| `AF_INSECURE_COOKIES` | unset | Set to `1` to drop the `Secure` attribute from cookies. For local development over plain HTTP and nothing else. |
| `AF_MIGRATE` | unset | Set to `1` to apply migrations at startup. Requires `AF_MIGRATION_DATABASE_URL`. |
| `AF_MIGRATION_DATABASE_URL` | unset | A connection string for a role that may run DDL. |
| `AF_VERSION` | `dev` | The build's version, reported by `/readyz`. Stamped into the image at build time; setting it by hand only makes the endpoint lie. |
| `AF_COMMIT` | `unknown` | The commit the build came from, reported by `/readyz`. Stamped the same way. |
| `AF_GITHUB_APP_ID` | unset | The numeric App ID from the GitHub App's settings page. Needed together with the private key and the webhook secret; setting some and not others stops the process at startup rather than producing a half-working App. |
| `AF_GITHUB_APP_PRIVATE_KEY` | unset | The PEM GitHub generated when the App's private key was created, or that PEM base64 encoded. Literal `\n` sequences are turned back into newlines, because most ways of getting a multi-line value into a container flatten it, and the resulting key fails with a message about DECODER routines that sends you somewhere else entirely. |
| `AF_GITHUB_APP_WEBHOOK_SECRET` | unset | The webhook secret set on the App. Every delivery is verified against it before its body is parsed. Unset means `/webhooks/github` answers 503 rather than accepting unsigned deliveries. |
| `AF_GITHUB_API_BASE` | `https://api.github.com` | Where the GitHub API lives. For GitHub Enterprise Server, and for tests. |
| `AF_MODEL_PRICES` | unset | What a model costs, as `model=input/output` in US dollars per million tokens, comma separated: `claude-sonnet-5=3/15,gpt-4.1=2/8`. Adds to the built-in defaults rather than replacing them. A model with no price is **refused** rather than charged nothing, because a request that spends money and adds nothing to the total is a spend cap that does not cap spending. A malformed entry stops the process at startup rather than being skipped, since a skipped entry is a model silently falling back to another price. |
| `AF_PROVIDER_KEY_SECRET` | unset | 32 bytes of base64, the secret that seals customers' Anthropic and OpenAI keys. Generate one with `openssl rand -base64 32`. Unset means keys cannot be stored at all: saving one is refused rather than written in the clear. It must not live in the same place as the database, or a database dump carries both halves. Anything other than 32 bytes stops the process at startup rather than failing later on the one action the feature exists for. |
| `AF_STRIPE_SECRET_KEY` | unset | The Stripe API key, server side only. Needed together with the webhook secret and both price identifiers; setting some and not others leaves billing **off** and prints the missing names at startup, because an operator who sets three of four believes billing works and the one they miss is usually the webhook secret, which fails only when a real customer pays. |
| `AF_STRIPE_WEBHOOK_SECRET` | unset | The signing secret for the endpoint registered at Stripe. Every delivery is verified against it, timestamp included, before its body is parsed. Unset means `/webhooks/stripe` answers 503 rather than accepting unsigned deliveries. |
| `AF_STRIPE_PRICE_TEAM` | unset | The Stripe price the `team` plan is sold at. A subscription for a price that is not named here is recorded and does **not** change the plan: somebody who bought through a link nobody configured has paid, and entitling them to the free plan would take away capacity they just bought. |
| `AF_STRIPE_PRICE_ENTERPRISE` | unset | The Stripe price the `enterprise` plan is sold at. |
| `AF_STRIPE_API_BASE` | `https://api.stripe.com` | Where the Stripe API lives. For tests, which point it at the engine's own Stripe mock pack, and for nothing else. |
| `AF_CONSOLE_DIR` | `/app/console-out` | Where the console's build is. The published image carries it at the default and nothing needs setting. Point it elsewhere only if you build `console/` yourself. A directory that is not there is not fatal: the API serves normally, the start-up log says the console is missing, and every page answers with that sentence rather than a blank 404 that reads like a routing bug. |

## Set on the engine, not here

| Variable | Where it is set | What it is |
| --- | --- | --- |
| `AF_CONTROL_PLANE_TOKEN` | On the engine, or in a CI job | An engine token, which the control plane **issues and verifies but never reads from its own environment**. Somebody running their own control plane creates one by posting to `/v1/tokens`, then sets it where `af` runs so the CLI can reach a hosted control plane. It is listed here because this is the page somebody setting up a self-hosted installation reads, and a token the control plane mints is easy to mistake for a variable the control plane consumes. Setting it on the control plane process does nothing at all. |

Everything above this section is read by the control plane process itself.


## Who may sign in

Two gates, and they are not the same one.

`AF_SIGNIN_ALLOWLIST` decides who may complete a GitHub sign-in at all. An
account not on it is refused during the OAuth callback, before any row is
written, so a refused person leaves no account behind.

Membership decides what a signed-in person can see, and it is derived from
GitHub rather than granted here: an account is a member of an organization only
where a GitHub App installation exists for that organization. That installation
row is written by `/webhooks/github` when somebody installs the App, so a
control plane with no App configured has no installations, and everybody who
signs in lands with no tenant. Somebody can
therefore sign in successfully and have no tenant at all, which is what happens
to any account added to the allowlist before it is invited anywhere.

Both are needed. The allowlist is a closed door; the installation check is what
makes an open one safe.

## What role somebody gets

The role comes from GitHub, read at sign-in with an installation token: an
organization owner on GitHub becomes an `admin` here, and everybody else
becomes a `member`.

An owner on GitHub deliberately does not become an `owner` here. That role also
holds `billing.manage`, and who pays is this application's decision rather than
GitHub's. Promote somebody with the role control on the Members page; a role set
that way is marked `manual` and is never overwritten by a later sign-in.

With one exception, and it is the first sign-in. An organization is created by
the installation webhook, before anybody has signed in, so every organization
passes once through a state where it has no members at all. The first person to
sign in becomes its `owner` rather than its `admin`, provided GitHub confirms
they administer the organization. Without that, no organization created this way
would ever have an owner, and nothing would hold `billing.manage`. The
promotion is marked `manual`, so a later sync does not take it back, and it is
recorded in the audit log as `member.bootstrapped`.

Two cases where nothing changes rather than something being guessed. Sometimes
GitHub will not say what somebody's role is: no App configured, a rate limit, an
outage. An existing membership then keeps the role it already had, because a
transient failure must not demote the only administrator out of their own
organization. A first sign-in during the same failure gets `member`, because
guessing upward would hand out administrative rights on a timeout, and that
applies to the first member of an empty organization as well: GitHub has to say
`admin` for anybody to become an owner. If the App is permanently broken and
that leaves an organization with nobody who can act, the way back is
[break-glass](/docs/self-hosting/operations/#nobody-can-sign-in), which is an
operator holding the database credential rather than a guess made by a web
request.

Sign-in can only ever speak for the person signing in. **Sync from GitHub** on
the Members page reconciles everybody at once, and it is the only thing that
takes access away: somebody removed from the GitHub organization keeps their
role until it runs, because a person who has been removed has no reason to come
back and sign in. It needs `members.manage`, it refuses an empty member list
from GitHub rather than removing every owner, and it records what it changed in
the audit log.

## Health

Two endpoints, answering two different questions. Point the right thing at the
right one.

| Endpoint | Answers | Touches the database |
| --- | --- | --- |
| `GET /health` | Is the process alive? | No |
| `GET /readyz` | Can it serve a request? | Yes, one trivial query |

`/health` is a static literal, and it stays one. A liveness probe restarts the
container when it fails, so wiring it to the database turns a slow Postgres
into a restart loop that makes the outage worse.

`/readyz` takes a connection from the pool the application serves with and asks
the database a question. It answers `200` with the build, or `503` with the
reason:

```json
{ "ready": true, "version": "v0.2.0", "commit": "31ce3f7" }
```

```json
{ "ready": false, "version": "v0.2.0", "commit": "31ce3f7",
  "reason": "password authentication failed for user \"af_app\"" }
```

Use `/readyz` for a deploy gate, and check the `commit` as well as the status.
The first deploy of this application to Azure answered `/health` with `200` for
thirteen minutes while every endpoint that touched a table returned `500`: the
schema had never applied, because the managed Postgres refused
`CREATE EXTENSION pgcrypto`. A gate watching `/health` would have called that
deploy a success. Checking the commit catches the other half, a rollout that
silently did not happen and left the previous build serving.

## Signing in with a link

GitHub is the front door and it needs a route to github.com. A preview
environment has none by design, and an isolated network has none at all, so
there is a second way in: a link sent to an address that already belongs to a
member of an organization.

It is off unless all three variables below are set. Setting one or two of them
stops the process at startup and says which are missing, because two of three
is a link that goes nowhere or mail that cannot be sent, and both of those fail
at the moment somebody is locked out rather than at deploy time.

There is no sign-up on this path. An address receives a link only once somebody
has invited it into an organization, the link works once, and it expires in
fifteen minutes.

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_RESEND_API_KEY` | unset | The Resend key the link is sent with. An HTTP mail API rather than SMTP on purpose: it is a request the egress sidecar can capture, which is what lets a preview environment read its own sign-in mail instead of delivering it to somebody. |
| `AF_MAIL_FROM` | unset | The From address. Resend refuses a domain it has not verified, which is a configuration error worth failing loudly on. |
| `AF_PUBLIC_URL` | unset | Where the link points: the origin a browser reaches this deployment on. Wrong here means a link that lands somewhere nobody is serving. |
| `AF_ENV_URL` | injected | Set by Antifailure inside a preview environment: the address of the environment's first web service, which is the application a person opens. `AF_PUBLIC_URL` is preferred where a deployment sets one, and this is the fallback, because the address a preview answers on is allocated at run time and no value written in a manifest can be right. Ignored outside a preview, where nothing sets it. |
| `AF_RESEND_BASE_URL` | `https://api.resend.com` | Where the mail API is. Set it to point at a local capture during development. |
| `AF_PRODUCT_NAME` | `Antifailure` | The name in the subject line, for a white-labelled deployment. |

## Schema maintenance

The `events` table is partitioned by month. Partitions are created ahead of the
writes, because a range-partitioned table with no partition for an incoming row
does not slow down, it fails.

Keeping ahead is DDL, so it runs as the migration role and not as the
application role. The connection is opened for each pass and closed after it,
rather than held idle between them.

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_MAINTENANCE_DATABASE_URL` | falls back to `AF_MIGRATION_DATABASE_URL` | The role that creates and drops partitions. When neither is set, this process logs a warning at startup and does not keep the partitions ahead. Something else must. |
| `AF_EVENT_RETENTION_MONTHS` | unset | Drop event partitions entirely older than this many whole months. Unset keeps everything forever, which is the default because retention is an operator's decision. A value that is not a whole number of months at least 1 stops the process at startup rather than silently keeping everything. |
| `AF_EVENT_ARCHIVE_DIR` | unset | Write a month out as newline delimited JSON here before dropping it. |

### What a pass does, in order

1. **Creates** the current month and the three after it. This happens
   unconditionally and first. Nothing below is allowed to prevent it.
2. **Archives** each month that retention has condemned, if
   `AF_EVENT_ARCHIVE_DIR` is set. The file is written under a temporary name and
   renamed when it is complete, so a file appearing in the directory always
   means a whole one.
3. **Drops** those months, but only if every archive finished. A failed write
   costs a retention run rather than the events, because a month deleted with no
   copy anywhere cannot be undone.
4. **Prunes** the default partition by age, a bounded number of rows per pass.

A pass runs at startup and then once a day. A pass that throws is logged and
the schedule continues: the failure that matters is running out of partitions,
and giving up after one transient error is how that happens quietly.

### If the job has not run for a while

Nothing needs to be done by hand. Events whose month does not exist land in the
default partition rather than failing, and the next pass moves them into the
month it creates for them. It detaches the default partition, creates the
month, moves the rows through the parent so that Postgres decides where each
one goes, and reattaches, all in one transaction.

### Why the partition key is `occurred_at`

Ingestion depends on a unique constraint to make retries safe:

```sql
INSERT INTO events (...) VALUES (...)
ON CONFLICT (org_id, idempotency_key, occurred_at) DO NOTHING
```

An engine that sent a batch and lost the response cannot know which half
landed, so it sends the batch again and the database drops the copy.

Postgres will not enforce a unique constraint that omits the partition key, so
the partition column is necessarily part of that key. `received_at` is assigned
here, by the clock, and would differ between an attempt and its retry: the
conflict would never fire and every retry would duplicate. `occurred_at` is
assigned by the sender when the event happened and is resent unchanged, so it
does not vary between attempts and costs nothing by being in the key.

The usual objection to partitioning on a value a client supplies is a skewed
clock inventing partitions forever. Ingestion already rejects `occurredAt` more
than a day in the future or more than a year in the past, so the live range is
bounded before a row reaches the table.

The cost, stated plainly: the idempotency key is now
`(org_id, idempotency_key, occurred_at)` rather than
`(org_id, idempotency_key)`. A sender that reuses an identifier under a new
timestamp gets two rows where it used to get one. No sender does that by
accident, since the identifier and the timestamp are minted together and resent
together, but it is a real difference and not a free one.

## Reading an archive

Each line is one event, as JSON, with timestamps as RFC 3339 text rather than
in a driver's own format, because the file is read by something that is not
this process.

```sh
# how many events, and over what span
wc -l events_2026_03.jsonl
head -1 events_2026_03.jsonl | jq -r .occurred_at

# everything one environment did
jq -c 'select(.env_id == "env-1234")' events_2026_03.jsonl
```

## Analytics

Off unless a surrogate secret is configured, and said out loud at startup either
way. There is no fallback to a constant key: a constant key is a surrogate
anybody can recompute, which is an organization identifier with extra steps.

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_ANALYTICS_SURROGATE_SECRET` | unset | 64 hex characters, which is 32 bytes. The key organization surrogates are computed under. Unset records nothing at all, and the dashboard says so rather than showing an empty chart. A value of any other length stops the process at startup rather than on the first event. Generate one with `openssl rand -hex 32`. |
| `AF_ANALYTICS_OPERATOR_ORG` | unset | The slug of the organization that operates this control plane. Its owners and admins may read the analytics dashboard; nobody else may, whatever permissions they hold in their own organization. Unset means nobody, and the route says which variable to set. |
| `AF_ANALYTICS_RETENTION_DAYS` | unset | Delete raw analytics events older than this many days. The daily aggregates computed from them are kept, because a count of page views by channel has nothing in it that identifies anybody. Unset keeps the raw events forever, which is the default because retention is an operator's decision. |
| `AF_SITE_ORIGIN` | unset | The origin the marketing site is served from, for the one endpoint a browser calls cross origin. Unset refuses every beacon rather than reflecting whatever `Origin` arrives, which is what a permissive default would do. |

### What is recorded, and what is not

The analytics stream is a closed schema. An event whose name is not in the
catalog is refused and counted, and so is a payload field the catalog does not
declare. There is no free-text field of any kind, so a repository name, a branch,
a query string or a page URL cannot reach the store even by mistake.

The organization is recorded as a keyed hash rather than as an identifier. The
store can count organizations and follow one through a funnel, and it cannot
name one without the key.

The application role holds `INSERT` on the stream and no `SELECT`. Only the
rollup, which runs as the schema owner, ever reads it, and only daily aggregates
come back out. A read attempted by the application raises `42501` rather than
returning nothing, which is the difference between a mistake somebody sees and
one somebody ships.

### The marketing site's beacon

The site sends one event per page a reader lands on, one when the waitlist
dialog opens, and one when an address is submitted. It sets no cookie, loads no
third-party script, and keeps its session identifier in `sessionStorage`, so it
dies with the tab and two visits a day apart cannot be joined. It turns itself
off for a reader who has set Global Privacy Control or Do Not Track.

The referrer, the URL and the query string are turned into a bounded channel, a
page shape and a campaign identifier **in the browser**, so the raw values never
cross the network at all. That is a stronger claim than discarding them on
arrival, and it is why the normalization lives in the page rather than in a
server reading a `Referer` header.

The endpoint is unauthenticated, because a shared secret in a static page is a
secret everybody has. Its counts are therefore a floor and a shape rather than
an audited total, which the dashboard says beside them.

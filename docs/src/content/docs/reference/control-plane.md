---
title: Control plane configuration
description: Every environment variable the control plane reads, what it does, and what happens when it is missing.
sidebar:
  order: 5
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
| `AF_GITHUB_APP_INSTALL_URL` | unset | The public `https://github.com/apps/<slug>/installations/new` address. When it is set, a person who signs in without an organization gets an **Install the GitHub App** action instead of a dead-end empty state. Any other origin or path stops the process at startup. |
| `AF_GITHUB_API_BASE` | `https://api.github.com` | Where the GitHub API lives. For GitHub Enterprise Server, and for tests. |
| `AF_MODEL_PRICES` | unset | What a model costs, as `model=input/output` in US dollars per million tokens, comma separated: `claude-sonnet-5=3/15,gpt-4.1=2/8`. Adds to the built-in defaults rather than replacing them. A model with no price is **refused** rather than charged nothing, because a request that spends money and adds nothing to the total is a spend cap that does not cap spending. A malformed entry stops the process at startup rather than being skipped, since a skipped entry is a model silently falling back to another price. |
| `AF_PROVIDER_KEY_SECRET` | unset | 32 bytes of base64, the secret that seals customers' Anthropic and OpenAI keys. Generate one with `openssl rand -base64 32`. Unset means keys cannot be stored at all: saving one is refused rather than written in the clear. It must not live in the same place as the database, or a database dump carries both halves. Anything other than 32 bytes stops the process at startup rather than failing later on the one action the feature exists for. |
| `AF_STRIPE_SECRET_KEY` | unset | The Stripe API key, server side only. Needed together with the webhook secret and both price identifiers; setting some and not others leaves billing **off** and prints the missing names at startup, because an operator who sets three of four believes billing works and the one they miss is usually the webhook secret, which fails only when a real customer pays. |
| `AF_STRIPE_WEBHOOK_SECRET` | unset | The signing secret for the endpoint registered at Stripe. Every delivery is verified against it, timestamp included, before its body is parsed. Unset means `/webhooks/stripe` answers 503 rather than accepting unsigned deliveries. |
| `AF_STRIPE_PRICE_TEAM` | unset | The Stripe price the `team` plan is sold at. A subscription for a price that is not named here is recorded and does **not** change the plan: somebody who bought through a link nobody configured has paid, and entitling them to the free plan would take away capacity they just bought. |
| `AF_STRIPE_PRICE_ENTERPRISE` | unset | The Stripe price the `enterprise` plan is sold at. |
| `AF_STRIPE_API_BASE` | `https://api.stripe.com` | Where the Stripe API lives. For tests, which point it at the engine's own Stripe mock pack, and for nothing else. |
| `AF_HOSTED_REQUIRED_PLAN` | unset | Set to `enterprise` on a hosted control plane that is sold only to enterprise organizations. Authentication, sign-out and the exits remain reachable; browser procedures, CLI provider operations, model proxy requests and engine ingestion are refused until Stripe grants the enterprise plan. The exits are billing, exporting the organization's data, deleting the organization, closing an account, and listing and revoking sessions: a plan gate may restrict what the product does for a customer and may never restrict their ability to leave, to retrieve what is theirs, or to secure their account. Any other value stops the process. Setting this while billing is off also stops the process, because otherwise no customer could satisfy the gate. Leave it unset when self-hosting. |
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

When sign-ups are open and `AF_GITHUB_APP_INSTALL_URL` is set, a new customer
can complete the whole path without an operator: sign in with GitHub, install
the App on an organization, then choose **Check my GitHub membership**. The
second OAuth exchange reads the installation GitHub just created and grants the
membership. The first GitHub administrator to claim an empty organization
becomes its owner under the rule below.

On an enterprise-only hosted deployment that owner lands on Plan. Checkout is
the only path that can grant the required plan; `billing.set` is refused, so an
owner cannot turn a free organization into an enterprise one without Stripe.
The signed subscription webhook changes the plan. **Refresh from Stripe** asks
Stripe for every subscription belonging to that customer and repairs the same
state when a webhook never arrives, including the case where no local
subscription row exists yet.

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

## Running the organization

Everything on this page is reachable by whoever the role table says can reach
it. The console hides what a role cannot do; the server refuses it, and the
refusal is what the permission matrix tests, one route against each of the four
roles.

### Inviting somebody who is not in your GitHub organization

Membership follows the GitHub App installation, which is right for engineers and
useless for the two cases every company has: a finance person who needs the
billing page and no repository access, and a contractor who is not in the GitHub
organization at all. **Invitations** on the Members page sends a link.

The link carries a token that exists only in the link. What is stored is its
hash, the same way a session is stored, so a leaked backup is a list of hashes
rather than a list of ways into your organization. Two consequences worth
knowing before you use it:

- **The link is shown to you as well as sent.** A control plane with no
  `AF_MAIL_FROM` cannot send anything, and an invitation that only existed as an
  email would silently do nothing there. Copy it and send it however you like.
- **Sending it again produces a NEW link and the old one stops working.** The
  original cannot be resent because it is not stored. That is also the better
  behaviour: an invitation forwarded to the wrong person is invalidated by
  asking for a fresh one.

A link expires after fourteen days. An invitation stays good after the person
who sent it has left, because it was authorised when it was sent, and the record
keeps their name as it was at the time. Accepting adds the account that is
signed in, which is not necessarily the address the invitation was sent to: the
token is the proof and the address is a label.

### Signing people out

**Signed in now**, under Settings, lists every live session in the organization
with who it belongs to, where it came from and when it was last used, and marks
the one you are reading it in. It never shows a token or a hash of one.

Signing a session out takes effect on that session's next request. Removing
somebody from the organization signs them out in the same transaction, so there
is no window in which a person who is no longer a member still has a working
session. Both need `sessions.manage`; removal needs `members.manage`.

A session that is not used for twelve hours stops working, and no session lives
longer than thirty days however active. The list shows when each one expires so
that a session which is about to go on its own can be left alone.

### Taking a copy

**Download a copy**, under Settings, produces one JSON file holding people,
invitations, repositories, masking rules, egress policy, environments, runs,
verdicts, runtimes, credentials by name, billing history and the audit log. It
needs `data.export`.

Every reference in it is the name you already use: a repository is `owner/name`,
a person is their login, an environment is its env id. There is not one internal
identifier in the file. Inside it, `files` holds text keyed by path, and those
are the parts you can put straight back: `masking.yaml` is a masking file the
engine reads as it is, and `egress.yaml` is the `egress:` block from
`antifailure.yaml`.

What it deliberately does not contain is listed in the file itself, under
`notIncluded`, with the reason for each. Engine token values and provider key
material are the important two: an export carrying either would be a way into
your CI.

### Deleting an organization

`organization.delete` is held by an owner and nobody else. It is not a delete
statement, and the order is the point:

| Step | What happens |
| --- | --- |
| Stop what is running | Every environment is marked torn down, every queued or running run is cancelled, and the organization is suspended so nothing new can be started. |
| End the subscription | Cancelled at Stripe at the end of the period you have paid for. Nothing is refunded and nothing is taken away early. |
| Wait | Nothing else happens until that period ends. Everything still reads, and the deletion can still be called off. |
| Revoke credentials | Engine tokens, provider keys, sessions, and the GitHub App installation, which is removed at GitHub rather than only marked here. |
| Produce the export | The same document as **Download a copy**, taken before anything is removed, because afterwards there is nothing left to build one from. |
| Delete | The organization and every row belonging to it, including the audit log. |

Two things follow from that order and both matter.

**A deletion that is interrupted picks up where it stopped.** Each step records
that it happened in the same transaction as the change it describes, so a
process that dies between two steps leaves a record saying exactly which
happened. The control plane retries on its own, and **Continue now** does the
next step immediately.

**The download link is shown once, when you ask for the deletion.** After the
organization is gone there is no membership left to authorise a download, so the
link is the authorisation. Keep it. It works for seven days, and **Destroy the
copy** removes the held document early if you would rather we did not keep one.

Your database is not touched by any of this, because none of it is here: no
snapshot, no masked branch and no captured request body ever reaches this
control plane.

### Closing your own account

Every role can close their own account, including `viewer`. It erases your name,
address, GitHub identity and avatar, removes your memberships, and signs you out
everywhere. Signing in again afterwards creates a new account.

It is called closing rather than deleting because the row is not removed. The
audit log references it, and that reference is deliberately one the database
refuses to break: an audit log whose subject can erase themselves from it is not
an audit log. The entries keep the name you had at the time, because the log is
a hash chain and rewriting an entry breaks it, and they go when the organization
does.

The only refusal is the last owner of an organization. An organization with no
owner cannot grant anybody the permission to become one, so make somebody else
an owner first, or delete the organization.

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

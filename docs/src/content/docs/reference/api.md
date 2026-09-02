---
title: HTTP endpoints
description: What answers on antifailure.dev, what answers on the control plane, and which of the two is the product's API.
sidebar:
  order: 6
---

Two hosts serve HTTP, and only one of them is an API worth building against.
This page says which, because the difference is not guessable from the outside
and the marketing domain is the one people try first.

## antifailure.dev

The marketing site and this documentation. It is a static export, so almost
everything on it is a file. The one exception is `/api`, which is a Static Web
Apps managed function with a single endpoint behind it.

| Method | Path | What it does |
| --- | --- | --- |
| `GET` | `/api` | Returns this list as JSON. |
| `GET` | `/openapi.json` | The control plane's OpenAPI 3.1 document, published at the apex address. |
| `GET` | `/errors.v1.json` | The versioned error catalog: code, message, recovery, whether retrying is safe, documentation and exit status. |
| `POST` | `/api/waitlist` | Adds one email address to the design partner waitlist. |

`POST /api/waitlist` takes `{"email": "...", "source": "..."}` and answers
`200` with `{"ok": true, "alreadyJoined": false}`. Signing up twice is not an
error: the row is keyed on a hash of the address, so the second attempt answers
`200` with `alreadyJoined` set to `true`. A body that is not an address is
`400`, more than five attempts a minute from one address or one caller is
`429`, and a storage failure is `503`. Every failure carries a stable `code`, a
human `message`, and a `resolution` an agent or the sign-up form can act on.

There is no endpoint that reads the waitlist back, and there will not be one.
An anonymous endpoint that can enumerate its own signups is how a waitlist
becomes a leaked mailing list. The list is read with the Azure CLI by somebody
who already has access to the account.

Any other path under `/api` answers `404` with a body saying so. That is the
whole surface. The source is `api/` in the repository, and it is a helper for
one form on the marketing site rather than a product.

## app.antifailure.dev

The control plane, and the API the product actually has. It is a separate
deployment with a separate hostname, described in
[Control plane configuration](/docs/reference/control-plane). Self-hosted
installations serve it wherever they put it.

Every row says what authenticates it. There is deliberately no count in that
sentence: the last version of this page said "the four unauthenticated routes
at the top" and there were five paths in four rows, with two webhook routes
below that take no session either.

| Path | Authentication | What it is |
| --- | --- | --- |
| `GET /health`, `GET /readyz` | none | Liveness and readiness. See [Control plane configuration](/docs/reference/control-plane). |
| `GET /openapi.json` | none | The OpenAPI 3.1 document this deployment serves. |
| `GET /metrics` | none | Prometheus text format. |
| `/trpc/*` | session cookie | The console's own API. Every procedure states the permission it needs. |
| `/v1/*` | session cookie | Sign-in state and provider keys, for a browser. Answers `401` without one. |
| `POST /v1/events` | engine token | Where an engine sends what it did. |
| `POST /v1/workloads/claim` | engine token | Takes the workload run waiting for an environment. |
| `POST /v1/workloads/runs/{id}/heartbeat` | engine token | Says a claimed run is still going. |
| `POST /v1/commands/claim` | engine token | Takes the cancel requests waiting for this organization. |
| `POST /v1/commands/{id}/ack` | engine token | Says what happened to one of them. |
| `POST /v1/auth/github-oidc` | a GitHub Actions workflow identity token, in the body | Exchanges a job's own identity for a short lived engine token, so nothing has to be pasted into a repository secret. The identity says which repository the job runs in and never whose, so the organization comes from a claim on that repository. See [GitHub](/docs/guides/github#sending-events-with-no-token-at-all). |
| `POST /v1/pr/callback-token` | a GitHub Actions workflow identity token | Exchanges a job's own identity for a credential scoped to one commit. |
| `POST /v1/pr/report` | that credential | What a job says about the commit it checked. |
| `POST /webhooks/github`, `POST /webhooks/stripe` | an HMAC over the raw body | Deliveries. Verified before the body is parsed, and each one handled once. |
| `/auth/*` | varies | GitHub sign in for a browser, and the device flow `af login` uses. |
| `GET /exports/deletion` | the token in the link, and nothing else | Downloads the export of an organization that has been deleted. |
| `POST /webhooks/github` | HMAC signature | Deliveries from the GitHub App. No session and no token: the body's signature is the credential, and an unsigned delivery is refused. |
| `POST /webhooks/stripe` | HMAC signature | Billing deliveries, verified the same way. |
| `POST /byok/anthropic/v1/messages` | engine or CLI token, in that provider's own header | The budgeted model proxy. See [Model keys](/docs/guides/model-keys). |
| `POST /byok/openai/v1/chat/completions` | engine or CLI token, in that provider's own header | The same, for OpenAI-shaped requests. |
| `GET /console/api/providers` | session cookie and CSRF header | Which provider keys and budgets an organization holds. Never the keys. |
| `PUT /console/api/providers/{provider}` | session cookie and CSRF header | Seals and stores one provider key. |
| `DELETE /console/api/providers/{provider}` | session cookie and CSRF header | Revokes one. |
| `PUT /console/api/providers/{provider}/budget` | session cookie and CSRF header | Sets the spend cap that the proxy above enforces. |

The two `/byok` routes are the mechanism [Model keys](/docs/guides/model-keys)
and [Provider keys](/docs/guides/provider-keys) describe, and this page omitted
both until now, so it described everything except the thing those guides are
about. Either token kind is accepted on them, because an engine on a build
machine has no person attached and a terminal has a personal token, and both
are asking the same organization to spend its own money. The token goes in
whichever header that provider's own client already sends, `x-api-key` for
Anthropic and an `Authorization` bearer for OpenAI, so pointing an existing SDK
at this host is a base URL change rather than an edit to the caller.

`GET /exports/deletion` is the one row here whose credential is the URL. An
organization that has been deleted has no members left to authenticate, so a
session cannot be the thing that opens its export; the link mailed at closure
is. It is rate limited like a sign in rather than like an API read for that
reason, because it is the one address on this list somebody could usefully
guess at. `?describe=1` returns the export's size and expiry without the body,
so the page that opens the link can say whether the export is still there
before it offers a download rather than after. A link naming nothing answers
`404`, and one naming an export that is not built yet answers `409`, which is
a real link and worth trying again.

The `/console/api/*` routes need the CSRF token as well as the cookie, and
saying "session cookie" alone would send somebody to a `403` they could not
explain. They exist separately from `/v1/providers`, which authenticates a
bearer token for `af provider`, because teaching one endpoint both schemes is
how it ends up accepting the weaker one.

The two `/v1/pr` routes are how a pull request check reports its result, and
they exist so that there is no repository secret to paste. A job asks GitHub
Actions for an identity token with the audience
`antifailure-control-plane`, posts it with the commit it is checking, and gets
back a bearer credential good for that one commit and that one run, expiring
within the hour. It reports once with it.

Nothing about that is optional for a fork and nothing has to remember to check:
GitHub does not mint a workflow identity token for a pull request job running on
a fork at all, so the exchange simply fails there, and the control plane
separately refuses a credential for a fork's commit until a maintainer has
approved that exact commit. See [GitHub](/docs/guides/github).

`/openapi.json` does not describe all of that, and it is worth knowing which
part it does. It is generated by walking the tRPC router, so it carries every
`/trpc` procedure plus the paths written by hand: `/health`, `/readyz`,
`/v1/events` and the four Studio endpoints above. Each operation has a stable
`operationId`; mutation bodies and query inputs are generated from the same Zod
validators the route executes, rather than copied into a second schema by hand.
Success and refusal envelopes are described as response schemas.
The `/auth` routes, the rest of `/v1`, and `/metrics` are real and answer, and
are not in the document.

### Two copies, and which one to read

`https://app.antifailure.dev/openapi.json` is generated at request time by the
deployment answering it, so it always describes exactly what that host serves.

`https://antifailure.dev/openapi.json` is a file, generated from the router at
build time, validated before it is published, and pinned to the site revision
that produced it. It is the address to guess at and the one `llms.txt`
advertises, and it cannot fail because the control plane is unreachable.

They can differ. The site deploys on every push to `main` and the hosted
control plane moves on a release promotion, so the apex copy can describe an
operation the hosted deployment does not serve yet. That is additive: calling
one returns `404` rather than something surprising. The deploy compares the
API version in both and fails if those disagree, because a caller reading one
version of the contract and calling another is the failure worth stopping. When
the two answers matter to you, read the control plane's own.

A browser gets a session by signing in with GitHub. A machine gets a token
through the device flow, which is what
[Signing in](/docs/guides/signing-in) walks through. The generated description
of both is `web/apps/api/src/openapi.ts`.

## Why an engine pulls its work rather than being told

The console does not run anything. `environments.create`, `agents.run`,
`load.run` and `workloads.start` ask GitHub to run the workflow in your own
repository, because the engine works against a masked branch of your production
database, your secrets and your third-party credentials, and none of those may
cross into a hosted service.

A `workflow_dispatch` carries only the inputs the workflow declares, and the
identifier of a recorded workload run is not one of them: the engine's command
line has no flag for it, and sending an input nothing can act on is a socket
that goes nowhere. So the dispatch says what to run and `POST
/v1/workloads/claim` says which recorded request it belongs to. The engine asks
what is waiting for the environment it is working on and takes it, with a lease.

That also survives the dispatch failing. A run whose dispatch was refused, for
a missing App installation or a workflow file that has not been updated, is
still recorded and still claimable. A run nobody ever claims ends as
`abandoned` when its deadline passes, which is the control plane saying it never
heard rather than a claim about whether the work happened.

The lease is what stops two engines measuring the same run. A heartbeat extends
it; enough missed heartbeats and it expires, and another engine polling the same
environment may take the run and carry on with the work. Two rules follow, and
both exist because getting them wrong loses measurements rather than merely
confusing a display:

An engine answered `409` by the heartbeat has lost the run and stops. It does
not send a final event, because the engine that took the run may be running it
right now, and ending the run from here would refuse that engine's report when
it arrives. The result document is still written and still uploaded by the job,
so nothing is lost locally.

The control plane accepts a final event only from the engine holding the run, or
from any engine while nothing holds it, which is the ordinary case for a run
started by hand with `--run-id` and for a spooled event that overtook its own
claim. An event from an engine that has lost the run is stored whole and
answered with a sentence saying so, and it changes nothing about the run.

An `abandoned` run says which kind of silence it was, because they call for
different things. Nobody ever claimed it, so look at the dispatch. One engine
took it and went quiet, so look at that runner. It changed hands and then went
quiet, so look at the runner that took it. Or it changed hands and the first
engine was still alive enough to try to end it, in which case the mechanism
worked and the engine holding the run is the one that said nothing.

Teardown works the same way from the other end. `environments.teardown` writes a
durable command and dispatches `af down`; whichever route reaches your runtime,
the engine's own `env.destroyed` event is the acknowledgement, and a teardown
nothing confirmed says so rather than sitting silent.

## What does not exist

There is no public REST API for building your own integration, and no client
library. `GET /openapi.json` describes an API whose primary callers are this
product's own console and its own engine, and the permission model behind it
assumes both. If you need something the engine cannot already do, the
[contributing guide](/docs/contributing/provider-authoring) is the shorter
path than an integration would be.

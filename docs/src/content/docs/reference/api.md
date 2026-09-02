---
title: HTTP endpoints
description: What answers on antifailure.dev, what answers on the control plane, and which of the two is the product's API.
sidebar:
  order: 9
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
| `POST /v1/pr/callback-token` | a GitHub Actions workflow identity token | Exchanges a job's own identity for a credential scoped to one commit. |
| `POST /v1/pr/report` | that credential | What a job says about the commit it checked. |
| `POST /webhooks/github`, `POST /webhooks/stripe` | an HMAC over the raw body | Deliveries. Verified before the body is parsed, and each one handled once. |
| `/auth/*` | varies | GitHub sign in for a browser, and the device flow `af login` uses. |
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
`/trpc` procedure plus `/health`, `/readyz`, and `/v1/events`. Each operation
has a stable `operationId`; mutation bodies and query inputs are generated from
the same Zod validators the route executes, rather than copied into a second
schema by hand. Success and refusal envelopes are described as response
schemas.
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

## What does not exist

There is no public REST API for building your own integration, and no client
library. `GET /openapi.json` describes an API whose primary callers are this
product's own console and its own engine, and the permission model behind it
assumes both. If you need something the engine cannot already do, the
[contributing guide](/docs/contributing/provider-authoring) is the shorter
path than an integration would be.

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
| `POST` | `/api/waitlist` | Adds one email address to the design partner waitlist. |

`POST /api/waitlist` takes `{"email": "...", "source": "..."}` and answers
`200` with `{"ok": true, "alreadyJoined": false}`. Signing up twice is not an
error: the row is keyed on a hash of the address, so the second attempt answers
`200` with `alreadyJoined` set to `true`. A body that is not an address is
`400`, more than five attempts a minute from one address or one caller is
`429`, and a storage failure is `503`. Every one of those carries a `message`
the sign-up form shows the visitor.

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

Everything below is authenticated, apart from the three probes.

| Path | Authentication | What it is |
| --- | --- | --- |
| `GET /health`, `GET /readyz` | none | Liveness and readiness. See [Control plane configuration](/docs/reference/control-plane). |
| `GET /openapi.json` | none | The OpenAPI description of everything else, generated from the router rather than written by hand. |
| `GET /metrics` | none | Prometheus text format. |
| `/trpc/*` | session cookie | The console's own API. Every procedure states the permission it needs. |
| `/v1/*` | session cookie | Sign-in state and provider keys, for a browser. |
| `POST /v1/events` | engine token | Where an engine sends what it did. This is the only route an engine calls. |
| `/auth/*` | varies | GitHub sign in for a browser, and the device flow `af login` uses. |

A browser gets a session by signing in with GitHub. A machine gets a token
through the device flow, which is what
[Signing in](/docs/guides/signing-in) walks through. The generated description
of both is `web/apps/api/src/openapi.ts`.

## What does not exist

There is no public REST API for building your own integration, and no client
library. `GET /openapi.json` describes an API whose two callers are this
product's own console and its own engine, and the permission model behind it
assumes both. If you need something the engine cannot already do, the
[contributing guide](/docs/contributing/provider-authoring) is the shorter
path than an integration would be.

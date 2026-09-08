---
title: Webhooks
description: Inbound callbacks reach an environment that has no public address.
sidebar:
  order: 6
---

An environment is not on the internet, so a provider cannot call it. Without
something in between, every flow that waits for a callback stops halfway: a
checkout that never completes, a subscription that never activates, a webhook
handler nothing has ever exercised.

A rule with a `webhook_path` closes that loop. When a sandboxed provider emits
an event, it is delivered to that path on the service that owns it.

```yaml
egress:
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
      webhook_path: /api/webhooks/stripe
```

## Signatures verify

The delivery is signed the way the provider signs it, with the sandbox signing
secret, so your existing verification code runs and passes. A webhook handler
that skips verification in previews is a handler nobody has tested, and the
first time it matters is in production.

The secret is derived per environment and handed to every service under the
provider's conventional name: `STRIPE_WEBHOOK_SECRET`, `GITHUB_WEBHOOK_SECRET`,
`RESEND_WEBHOOK_SECRET`. `af webhook list` names the variable for each provider.
An application that reads the same value under a name of its own says so with
`from`, and receives what the environment will sign with:

```yaml
services:
  - name: api
    env:
      - name: AF_STRIPE_WEBHOOK_SECRET
        from: STRIPE_WEBHOOK_SECRET
```

`af explain` reports that variable as coming from the environment's webhook
signing secrets. A value typed into the manifest instead would be the first
thing to drift from the one the sender uses, and every event would then be
refused as unsigned by the very verification this exists to exercise.

## The GitHub App's own key

A GitHub App is three credentials, and the webhook secret is only one of them.
The other two are the numeric App id and the private key the App signs its JWT
with, and an application that reads all three usually refuses to start with
some of them: a webhook secret with no private key is an endpoint that verifies
deliveries and can do nothing with them.

So a manifest that declares a webhook path for GitHub is offered a private key
as well, under `GITHUB_APP_PRIVATE_KEY`, generated for the life of the
environment:

```yaml
services:
  - name: api
    env:
      - name: AF_GITHUB_APP_ID
        value: "1"
      - name: AF_GITHUB_APP_PRIVATE_KEY
        from: GITHUB_APP_PRIVATE_KEY
      - name: AF_GITHUB_APP_WEBHOOK_SECRET
        from: GITHUB_WEBHOOK_SECRET
```

It is a real RSA key in PKCS#8, because the applications that read one reject a
placeholder, and it is a different key in every environment. It authenticates
nothing: GitHub has never seen it, and `api.github.com` is reachable only if
your own egress rules allow it.

Exporting `GITHUB_APP_PRIVATE_KEY` yourself wins over the generated one, for
the case where you are rehearsing against an App you really registered.

Writing the key into the manifest instead is the thing this replaces. A
manifest is committed, so a key written there is a key in the repository for as
long as the file is there, and the engine refuses a value that carries one.

## Delivery failed

```
AF-NET-012 The webhook could not be delivered to web: connection refused.
```

The service was not accepting connections when the event arrived. Usually the
event was emitted during startup, before the service was ready. Setting
`health_path` to something that answers only when the application is genuinely
ready is what fixes it, rather than a longer timeout.

A 4xx or 5xx from your handler is not this error. That is delivered and
recorded, and `af net log` shows the status, because a handler that returns 500
is a bug in the handler and reporting it as a delivery failure would point at
the wrong place.

## Retries

A provider retries the same event with the same identifier, and a handler that
is right about that does nothing the second time. Two triggers a second apart
are two different events, so to rehearse a retry pin the identifier:

```sh
af webhook trigger stripe invoice.paid --set event_id=evt_retry_1
af webhook trigger stripe invoice.paid --set event_id=evt_retry_1
```

`event_id` is the one `--set` name that is not a payload field. The MCP tool
`send_webhook_event` takes it the same way, in `fields`.

## Replaying

```sh
af net log              # every decision, including deliveries and what answered
af net log --blocked    # only what was refused
```

Every delivery is recorded, so a handler that failed can be examined against
exactly what it received rather than against what you think it received.

## Capture and mock modes

`capture` records outbound calls and does not generate events. `mock` answers
from a fixture pack, and a pack may include events to deliver, which is how a
provider with no sandbox still exercises a callback path.

Related: [egress](/docs/concepts/egress), [mocking](/docs/guides/mocking),
[sandbox credentials](/docs/guides/sandbox).

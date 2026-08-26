---
title: Webhooks
description: Inbound callbacks reach an environment that has no public address.
sidebar:
  order: 5
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

Related: [egress](/concepts/egress/), [mocking](/guides/mocking/),
[sandbox credentials](/guides/sandbox/).

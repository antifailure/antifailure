---
title: Egress
description: Why an environment reaches nothing by default, and what each mode does.
sidebar:
  order: 4
---

An environment can reach nothing on the network except the hosts listed in the
manifest, each in the mode named. Everything else is refused, and every refusal
carries a decision you can read.

That default is the point. A preview environment that can reach production
Stripe will eventually charge somebody, and a preview that can reach production
Sentry will drown the error feed the day somebody opens a branch that throws.

```yaml
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
      webhook_path: /api/webhooks/stripe
      note: "Stripe has a real sandbox, so billing runs end to end"

    - host: api.resend.com
      mode: capture
      note: "mail goes to the inbox; no real address receives anything"

    - host: "*.ingest.sentry.io"
      mode: block
      note: "preview errors would drown the production feed"
```

## The modes

| Mode | What happens |
| --- | --- |
| `block` | Refused, with a decision naming the rule. |
| `allow` | Passed through untouched, and not intercepted. |
| `sandbox` | Sent to the provider's sandbox, with the sandbox credential substituted for the one the application holds. |
| `capture` | Answered locally and recorded, so a workflow finishes and nothing leaves. |
| `mock` | Answered from a fixture pack, with no network at all. |
| `synth` | Answered by a model, for an API with no sandbox and no fixture. |

`sandbox` is the one worth understanding. The application inside the container
never holds the live credential: it holds a placeholder, the proxy substitutes
the sandbox key on the way out, and the live key is never inside the
environment at all. There is a conformance test that starts a container and
proves the live value is not in its environment, its filesystem, or its process
list.

## Narrowing a rule

A rule can be narrower than a host.

```yaml
    - host: api.github.com
      mode: allow
      methods: [GET]
      paths: ["/repos/*/issues*"]
      rate_limit: 10/s
      note: "reading issues only, and not quickly enough to be noticed"
```

`paths` and `methods` narrow what the rule covers; a request to the same host
outside them falls through to the next rule that matches, and then to the
default. `rate_limit` is a token bucket, which is what stops a retry loop in a
preview from looking like an attack to somebody's rate limiter.

`fixtures` names a pack for `mock` mode.

## Reading a decision

```sh
af net explain GET https://api.stripe.com/v1/charges
af net log            # everything the environment tried
```

```
GET https://api.stripe.com/v1/charges

  SANDBOX

  The rule for api.stripe.com decided sandbox because the host matches exactly.
  Stripe has a real sandbox, so billing runs end to end.

  Credential   STRIPE_SECRET_KEY, substituted at the proxy
  Webhooks     delivered to /api/webhooks/stripe

  No other rule matches this request.
```

`af net explain` and the proxy share the same decision code, so the explanation
cannot disagree with what actually happened.

## When something is blocked

```
AF-NET-001 The request to api.segment.io was blocked by rule default.
  Next: Add an egress rule for api.segment.io with the mode you intend, or
  leave it blocked.
```

Leaving it blocked is a real answer, and often the right one. Analytics from a
preview pollutes production reporting, and a build that fails because a
telemetry call was refused is a build telling you something useful about your
error handling.

## A live credential on the way out

```
AF-NET-004 A request to api.stripe.com carried a live credential in the
Authorization header and was blocked.
  Next: Replace the credential with a sandbox key; an environment must never
  hold a live one.
```

The request is refused, not redacted. A live key inside an environment is a
problem whether or not this particular request reached anywhere, and quietly
stripping it would hide that the key is in there.

The value is never printed. The detector recognises the prefixes providers use,
which is the same detector CI runs over the repository.

## Certificate pinning

```
AF-NET-020 api.example.com rejected the environment certificate, which usually
means the client pins its own.
  Next: Set the host to ALLOW so that its traffic is not intercepted, or
  disable pinning in the client for previews.
```

`sandbox`, `capture`, `mock` and `synth` all terminate TLS, because deciding
what a request means requires reading it. A client that pins a certificate will
refuse. `allow` does not intercept, so a pinned client works, at the cost of
the engine not seeing what it sent.

## IPv6

```
AF-NET-021 api.example.com resolves only to IPv6 and the environment has IPv6
disabled.
  Next: Set egress.allow_ipv6 for this environment, or use a host with an IPv4
  address.
```

IPv6 is off by default, because an environment that can reach a host by an
address the policy did not evaluate is an environment whose policy is advisory.
Turning it on is one line, and the policy applies to both families equally.

Related: [mocking](/guides/mocking/), [sandbox credentials](/guides/sandbox/),
[the inbox](/guides/inbox/), [webhooks](/guides/webhooks/).

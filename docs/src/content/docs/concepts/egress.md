---
title: Egress
description: Why an environment reaches nothing by default, and what each mode does.
sidebar:
  order: 5
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

## The agents' own model call is not governed by this

A model call is outbound HTTP, so it is reasonable to expect a `default: block`
manifest to switch the agents' planner off. It does not, and you do not have to
name Anthropic or OpenAI in your manifest.

The policy governs traffic *through* the sidecar. Services sit on a network
with no route out and every name they resolve points at the sidecar, so their
packets have nowhere else to go. Neither model caller is on that network. The
runner is a subprocess of `af` on your own machine, outside the environment
entirely, and a [synth](/docs/guides/synth) rule's model call originates in
the sidecar itself, which is the container that has the route out.

What this *does* govern is your **application** calling a model. If your own
code calls `api.anthropic.com`, that is traffic through the sidecar like
everything else, and under `default: block` it is refused until a rule names
it. The same provider in the same run is reached from two places for two
different reasons, so `af net log` is worth reading before concluding that the
planner is broken. `af model test` answers the other half: it reports whether
this machine can reach the endpoint at all, and says in as many words that the
manifest is not what is stopping it.

See [your own model key](/docs/guides/model-keys).

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

## What the sidecar refuses whatever the policy says

The sidecar is the only thing in an environment with a route out, so a service
that cannot reach an address itself can still ask the sidecar to reach it. Some
addresses are refused there regardless of the rules, because no rule was ever
written about them.

- **Loopback, link local, private and carrier grade addresses.** The link local
  range holds the instance metadata endpoint, which hands out the node's own
  cloud credentials to anything on the node that asks. `default: allow` is a
  sentence about the internet, not about the machine the environment is running
  on, so it does not cover these.
- **A name that resolves to one of them.** The check reads the address the name
  resolved to, so pointing a domain you control at `169.254.169.254` reaches
  nothing.
- **Anything but an address lookup for an external name.** `TXT`, `NULL`,
  `CNAME` and `SRV` queries are answered inside the environment rather than
  forwarded, because the payload of a DNS query is whatever the client puts in
  the name and forwarding one is a way out that opens no connection. Names
  inside the environment resolve normally.
- **A port the client picked.** A transparent connection arrives on 80 or 443,
  and that is the port the rule is evaluated against. The port in a `Host`
  header is not a destination.

To reach a private address on purpose, name it in a rule:

```yaml
    - host: 10.0.4.20
      mode: allow
      note: "the staging API on our own network"
```

Naming the address is the consent. A wildcard is not: `*` means every host on
the internet.

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

The refusal is per address rather than per name, so a host that resolves to
both families is still reached over IPv4 with IPv6 off, and a host with only an
IPv6 address is refused with that as the reason.

Related: [mocking](/docs/guides/mocking), [sandbox credentials](/docs/guides/sandbox),
[the inbox](/docs/guides/inbox), [webhooks](/docs/guides/webhooks).

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

## Matching a host

A rule names one host, or a shape that several hosts share.

| Pattern | Matches |
| --- | --- |
| `api.stripe.com` | that host and nothing else |
| `10.0.0.1` | that address, and not a name that resolves to it |
| `*.stripe.com` | one label or more before `.stripe.com`, but not `stripe.com` itself |
| `email.*.amazonaws.com` | exactly one label where the star is, so every SES region and no other service |
| `*.s3.*.amazonaws.com` | a bucket in any region, in the virtual hosted form |

A star anywhere but the front stands for exactly one label. That is what lets a
rule name an AWS service rather than the whole account: every regional endpoint
is `<service>.<region>.amazonaws.com`, so the only leading wildcard that reaches
S3 also reaches SES, SQS, STS and Secrets Manager. Antifailure's own catalog
took that wildcard once, in `capture` mode under a mail rule, and an S3 `PUT`
was answered with a mail provider's success.

A star has to be a whole label. `web-*.example.com` is refused rather than read
as a prefix somebody did not write, and a pattern of nothing but stars is
refused because it matches every host while reading as though it named one.
Only a bare `*` matches everything, and only in `block` mode.

Specificity decides, never order. An exact host beats everything. A pattern
whose stars are all interior beats a leading wildcard, because it pins both
ends and the number of labels. Among leading wildcards, the one that pins more
text after the star wins, so `*.s3.*.amazonaws.com` beats `*.amazonaws.com`. A
`*.amazonaws.com` block and an `email.*.amazonaws.com` capture can therefore sit
in one manifest, and neither reaches the other's hosts.

## Capture answers as the provider would, or refuses

`capture` returns the shape the provider's own client expects to parse, because
an application that gets a 200 with the wrong body from its mail provider
usually carries on and fails three steps later in a way that looks like an
application bug. Resend, SendGrid, Postmark, Mailgun, Twilio, Amazon SES and
Slack each have a handler.

For anything else, capture records the body and answers `200 {}`, which is a
guess. It makes that guess only when the rule **names the host**: an exact host,
an address, or a pattern whose stars are all interior. A host swept in by a
leading wildcard, or reached through `default: capture` with no rule at all, is
refused instead, with a decision saying so, because an invented success is
believed and nobody wrote that host down.

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

## Protocols that are not HTTP

An application talks to more than websites. A broker, a managed database, a
mail relay and a cache are all outbound calls, and none of them is HTTP.

Those connections reach the sidecar the same way an HTTPS call does. The
environment's resolver answers every external name with the sidecar's own
address, so the client connects to it believing it reached the broker.

Which ports it answers on is the manifest's decision, and only the manifest's.
A rule that spells out a port opens a listener for that port. A rule that names
a host and no port opens none:

```yaml
    - host: broker.example.com:5671
      mode: allow
```

That is stricter than it looks and it is deliberate. A connection accepted on
this path is forwarded on the strength of the name in its handshake, and a rule
that names no port applies to every port, so answering on a port nobody asked
for would carry an allowed host's cache and its mail alongside its website.
Writing the port down is the consent, in the same way that naming a private
address is. A listener is shared by all destinations on its port, so the
matched allow rule must name the port for this host too. Allowing
`broker.example.com:5671` never grants `website.example.com` that port.

Rules scoped to a path or method require inspection. If any rule for the host
and port needs inspection, the opaque connection is refused, including when a
broader allow rule would otherwise match. No synthetic path or method can
stand in for the bytes the sidecar cannot read.

Antifailure still knows what these ports usually carry: 5671 and 5672 for AMQP,
9092 and 9093 for Kafka, 27017 for MongoDB, 6379 and 6380 for Redis, 5432 for
PostgreSQL, 3306 for MySQL, 25, 465 and 587 for mail, 8883 for MQTT, 636 for
LDAP, 4222 for NATS and 22 for SSH. That table is what lets a refusal name the
protocol you were probably speaking, and what lets a rule be refused at
validation rather than at the connection. It is not what decides which ports
are answered.

The decision is made on the server name in the TLS handshake, which is what the
client wrote. Nothing inside the connection is read, and nothing about the
design could read it.

### What that means for a rule

Two modes work on these connections and four do not.

`block` and `allow` are decisions about whether a connection happens, and the
handshake carries everything they need.

`capture`, `mock`, `synth` and `sandbox` are decisions about a request.
Capture has to understand a message before it can record one, mock has to
understand a request before it can choose a fixture, synth has to describe one
to a model, and sandbox has to find the credential before it can replace it.
None of that exists in an opaque stream, so a rule that uses one of them on a
port carrying one is refused rather than quietly treated as `allow`.

Sandbox is the one worth stating on its own. A sandbox rule that forwarded
without replacing the credential would send the application's own key to the
real provider and report a successful sandbox call, which is worse than
blocking and worse than refusing.

### A connection with no name in it

Most of these protocols have a cleartext form. AMQP on 5672, Redis on 6379 and
Kafka on 9092 send no handshake, and PostgreSQL, MySQL and SMTP submission
negotiate TLS after a cleartext exchange rather than before one. There is no
host name anywhere in those bytes, so the connection cannot be attributed to a
host, so no rule can apply to it and it is refused with that as the reason.

For providers offering TLS from the first byte, use that form: `amqps` on 5671, `rediss` on 6380, Kafka's
`SASL_SSL` on 9093, MongoDB Atlas, and mail on 465.

### Reading it afterwards

Every one of these decisions is recorded with `stream` set as well as
`host_only`, and `af net log` and the containment report both count them and
name the hosts. The two flags say different things: `host_only` means a path
was not seen on a request that had one, and `stream` means there was no request
to see. A twin whose broker traffic was never inspected is a twin with a blind
spot, and it should be possible to point at it.

Related: [mocking](/docs/guides/mocking), [sandbox credentials](/docs/guides/sandbox),
[the inbox](/docs/guides/inbox), [webhooks](/docs/guides/webhooks).

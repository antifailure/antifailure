---
title: Audit stream
description: Privileged actions forwarded to the SIEM your security team already reads.
sidebar:
  order: 9
---

*Requires an enterprise license with the `audit_stream` feature.*

The engine records the privileged things it does and forwards them to
destinations you configure. Nothing here replaces the control plane's own audit
log, which is written regardless: a sink that is unreachable loses forwarding
and never loses the entry.

## What is forwarded

Five actions, and the list is deliberately short. An audit stream a security
team can read is one where every entry is an act somebody could be asked about.

| Action | When |
| --- | --- |
| `environment.refused` | organisation policy refused an environment, before anything was created |
| `environment.created` | an environment was brought up, with the outcome when it failed |
| `environment.torn_down` | an environment was removed, with what was left behind |
| `golden.published` | a masked copy of production was written to a shared store |
| `golden.pulled` | a published golden was restored onto this machine |

Egress decisions and build steps are not forwarded. They are high volume, they
are already reported through the event bus, and a stream nobody can read is
worse than a smaller one they can.

## What one entry looks like

One line of JSON, the same bytes at every destination, so a query written
against your SIEM works against your archive:

```json
{"occurred_at":"2026-09-07T11:22:33.456789Z","forwarded_at":"2026-09-07T11:22:33.481204Z","org":"acme","actor":"dana@acme.example","action":"golden.published","target_type":"golden","target_id":"gv_9f2c","origin":"engine","detail":{"repository":"acme/shop","store":"the bucket s3://acme-goldens/audit"}}
```

Both timestamps are there because they are different instants. `occurred_at` is
when the action happened and `forwarded_at` is when a sink succeeded in sending
it, which a retry can put minutes later. A stream carrying only the second
reorders itself whenever one destination is slow. An entry whose producer did
not say when it happened carries no `occurred_at` at all rather than borrowing
the sink's clock, because a guessed timestamp in an audit log is evidence that
is wrong rather than evidence that is missing.

`org` and `actor` come from `AF_ORG` and from `AF_ACTOR`, falling back to
`GITHUB_ACTOR` on a GitHub Actions runner. Neither is invented when it is
absent. The operating system user is never consulted: on a CI runner it is
`runner` for everybody, which reads as an attribution and is not one.

## Turning it on

`AF_AUDIT_SINKS` lists the destinations, in the order they are written:

```sh
export AF_AUDIT_SINKS=syslog,webhook,object_store
```

A sink named here that cannot be built stops the engine at startup with the
reason. That is deliberate: somebody who sets this variable has said that every
privileged action must be forwarded, and starting anyway with the sink absent
means nothing is forwarded and nothing says so, which is indistinguishable from
a quiet week.

With the variable unset nothing is registered and nothing is printed. Nothing is
ever detected automatically, so a machine that happens to carry cloud
credentials for something unrelated does not start writing your audit trail into
somebody's bucket.

### syslog over TLS

```sh
export AF_AUDIT_SYSLOG_ADDRESS=collector.internal:6514
export AF_AUDIT_SYSLOG_CA_FILE=/etc/ssl/collector-ca.pem
# Optional, for a collector that authenticates its senders:
export AF_AUDIT_SYSLOG_CERT_FILE=/etc/ssl/engine.pem
export AF_AUDIT_SYSLOG_KEY_FILE=/etc/ssl/engine-key.pem
# Optional, what the messages claim to come from. Defaults to the hostname.
export AF_AUDIT_SYSLOG_HOSTNAME=runner-7
```

RFC 5424 messages with RFC 5425 octet counted framing, at facility 13, "log
audit", so a receiver routing on facility files them as what they are. The
action is the message id, which is what a receiver filters on. Port 6514 is
assumed when the address carries none.

There is no plaintext option. An address written as `syslog://` or `tcp://` is
refused rather than downgraded: the entries say who was given a copy of
production, and sending that unencrypted to an unauthenticated receiver is the
thing the entries exist to prove is not happening.

### HTTPS webhook

```sh
export AF_AUDIT_WEBHOOK_URL=https://siem.example/ingest
export AF_AUDIT_WEBHOOK_DEAD_LETTER_FILE=/var/lib/antifailure/audit-dead-letter.jsonl
# Optional. Keys an HMAC-SHA256 over the exact bytes posted.
export AF_AUDIT_WEBHOOK_SECRET=...
# Optional, for a receiver that takes a bearer token.
export AF_AUDIT_WEBHOOK_HEADER="Authorization: Bearer ..."
```

With a secret set, every request carries `Af-Audit-Signature: sha256=<hex>` over
the body, in the same shape GitHub and Stripe use. A receiver that accepts audit
entries on an open endpoint accepts audit entries from anybody, and a forged
entry in an audit log is worse than a missing one.

The dead letter file is required, and it is the reason the retry is allowed to
be short. Three attempts over about a second and a half, then the entry is
appended to that file and flushed, in the same JSON the receiver would have
been given. A webhook that posts once and gives up loses an entry every time its
receiver restarts, and loses it silently. A hole you can replay is not a hole.

### Object store

```sh
export AF_AUDIT_OBJECT_STORE_URL=s3://acme-audit/antifailure
```

Or a server that speaks the same API, as `https://minio.internal/bucket/prefix`,
or an Azure Blob container URL carrying a shared access signature. The S3 form
signs its requests with `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` read
from the environment, by the names the AWS tools already use, so a machine set
up for the AWS CLI needs nothing else.

One object per entry, keyed by date:

```
antifailure/2026/09/07/112233.456789000-golden.published-9f2ca10b.json
```

Not a batch and not an append. An object written once can be locked, which is
what a retention obligation is usually satisfied by, and an appended file has to
be read, extended and rewritten, which is a race between two teardowns and
cannot be locked at all. The date is a path so a lifecycle rule and a
partitioned query both work without anybody parsing a filename. An object is
never replaced.

## What a sink cannot do

A sink observes. It cannot refuse an environment, cannot change an entry, and
cannot see what another sink received. An error from one is recorded and the
lifecycle continues.

That last part matters most on teardown. A forwarding outage that stopped an
environment being destroyed would turn a logging problem into a resource leak,
which is strictly worse than the problem it came from. So a SIEM you cannot
reach costs you a line on standard error and nothing else:

```
af: audit sink: forwarding to syslog over TLS at collector.internal:6514 failed
```

## The licence is asked per action, not at startup

Every sink checks `audit_stream` on every entry rather than once when it is
registered. A licence that lapses while a long lived process is running stops
forwarding immediately, without a restart, and one that renews starts again the
same way. A configured sink on an installation without the feature accepts every
entry and writes none, which is correct and is also silence, so the engine says
so once at startup:

```
af: audit sink: configured, and audit_stream is not licensed on this
installation, so nothing is forwarded
```

## Measuring the delay yourself

`just benchmark` writes a dated report of how long an action takes to reach each
destination, and how long an undeliverable entry takes to become durable on disk
while a receiver is down. With nothing configured it measures loopback, which is
the delay this product is responsible for and no more. Point it at your own
collector and the number becomes the whole path, measured by you:

```sh
AF_AUDIT_BENCHMARK_SYSLOG_ADDRESS=collector.internal:6514 \
  AF_AUDIT_BENCHMARK_SYSLOG_CA_FILE=/etc/ssl/collector-ca.pem \
  AF_AUDIT_BENCHMARK_WEBHOOK_URL=https://siem.example/ingest \
  just benchmark
```

Related: [licensing](/docs/enterprise/licensing),
[policy](/docs/enterprise/policy), [compliance](/docs/enterprise/compliance).

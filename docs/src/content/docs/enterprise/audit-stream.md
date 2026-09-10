---
title: Audit stream
description: Privileged actions forwarded to the SIEM your security team already reads.
sidebar:
  order: 9
---

*Requires an enterprise license with the `audit_stream` feature.*

Two streams, from two places, and they are configured separately because they
run on different machines. The engine forwards the privileged things it does
from wherever you run it. The control plane forwards its own audit log, the one
with the hash chain in it, from wherever you run that. Neither replaces the
other and neither replaces the log itself, which is written regardless: a sink
that is unreachable loses forwarding and never loses the entry.

Until this page said so, only the first half existed. The control plane's audit
log carried a tamper evident chain and reached no destination at all, so single
organization sign on, directory provisioning and administrative actions were
recorded and forwarded nowhere.

## What the engine forwards

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

## Turning the engine's stream on

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
export AF_AUDIT_SYSLOG_ADDRESS=collector.example.com:6514
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
be short. Three attempts, pausing 200 ms and then 600 ms between them, and the
entry is appended to that file and flushed before the call returns, in the same
JSON the receiver would have been given. The measured total, round trips
included, is in the report `just benchmark` writes.

A webhook that posts once and gives up loses an entry every time its receiver
restarts, and loses it silently. A hole you can replay is not a hole.

### Object store

```sh
export AF_AUDIT_OBJECT_STORE_URL=s3://acme-audit/antifailure
```

Or a server that speaks the same API, as `https://minio.example.com/bucket/prefix`,
or an Azure Blob container URL carrying a shared access signature. The S3 form
signs its requests with `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` read
from the environment, by the names the AWS tools already use, so a machine set
up for the AWS CLI needs nothing else.

One object per entry, keyed by date:

```
antifailure/2026/09/07/112233.456789000-golden.published-9f2ca10b.json
```

Not a batch and not an append. An object written once can be locked, which is
what a retention obligation is usually satisfied by, and an appended file has
to be read, extended and rewritten, which is a race between two environments
being torn down and cannot be locked at all. The date is a path so a lifecycle
rule and a partitioned query both work without anybody parsing a filename. An
object is never replaced.

Two entries in the same nanosecond are two objects, because the key carries
eight random characters as well as the time. Without them the second would
silently replace the first, and an audit log that loses the entries which
arrived together loses exactly the ones somebody is investigating.

## What a sink cannot do

A sink observes. It cannot refuse an environment, cannot change an entry, and
cannot see what another sink received. An error from one is recorded and the
lifecycle continues.

That last part matters most on teardown. A forwarding outage that stopped an
environment being destroyed would turn a logging problem into a resource leak,
which is strictly worse than the problem it came from. So a SIEM you cannot
reach costs you one progress line and nothing else, carrying the sink's own
words about what went wrong:

```
audit sink: forwarding to syslog over TLS at collector.example.com:6514: dial tcp
10.0.0.9:6514: i/o timeout
```

The environment still comes up, and the teardown still finishes. What is lost is
the forwarding, and for the webhook not even that: an entry no receiver would
take is in the dead letter file before `Write` returns.

## The control plane's own audit log

A different stream with a different shape, and the shape is the reason it is
worth having. The engine forwards five actions from a machine with no database.
The control plane forwards `audit_entries`, the organization log covering
actions including sign on, directory provisioning and administration. The
separate global operator log, `admin_audit_entries`, is forwarded only where its
writer also produces an organization entry. Each organization has its own hash
chain: each entry holds the hash
of the one before it, so altering an old entry breaks every entry after it.

### What one batch looks like

Batched rather than one entry per request, because the batch carries the proof.
The webhook posts this JSON field schema directly. Splunk and Event Hubs wrap
entries in their collector formats, described below. Organization identifiers
are UUID strings and `occurredAt` is an ISO timestamp:

```typescript
interface AuditBatch {
  entries: Array<{
    seq: number
    orgId: string
    actor: string
    action: string
    targetType: string
    targetId: string | null
    origin: string
    detail: Record<string, unknown>
    occurredAt: string
    entryHash: string
  }>
  manifest: {
    org: string
    count: number
    firstSeq: number
    lastSeq: number
    headHash: string
    digest: string
    signature: string
  }
}
```

`headHash` is the chain hash of the last entry in the batch, and `digest` is a
sha256 over the canonical batch body with `signature` an HMAC of that digest
under `AF_AUDIT_STREAM_KEY`. That is what lets a batch sitting in an archive be
checked without reaching back to the control plane that wrote it, which is the
situation an auditor is usually in.

For webhooks, `x-antifailure-timestamp` is the first entry's event time, not
the delivery time. Catching up after an outage can deliver old events. Verify
the signature and deduplicate by organization and sequence; signature
verification alone does not reject replay.

One batch holds one organization. A manifest names an organization, so a batch
carrying two would name one and cover both, and a receiver checking it would be
checking the wrong claim.

### Turning the control plane's stream on

```sh
export AF_AUDIT_STREAM_SINK=webhook
export AF_AUDIT_STREAM_KEY="$(openssl rand -base64 32)"
export AF_AUDIT_STREAM_WEBHOOK_URL=https://siem.example/ingest
export AF_AUDIT_STREAM_WEBHOOK_SECRET=...
```

`AF_AUDIT_STREAM_SINK` takes `splunk`, `event_hubs` or `webhook`. Splunk reads
`AF_AUDIT_STREAM_SPLUNK_URL` and `AF_AUDIT_STREAM_SPLUNK_TOKEN`, with
`AF_AUDIT_STREAM_SPLUNK_INDEX` and `AF_AUDIT_STREAM_SPLUNK_SOURCETYPE` optional.
Event Hubs reads `AF_AUDIT_STREAM_EVENT_HUBS_URL` and
`AF_AUDIT_STREAM_EVENT_HUBS_AUTHORIZATION`, the second being a shared access
signature you generate, so no key reaches this process and managed identity
stays possible.

Event Hubs receives each entry as a JSON string in the event body and retains
the signed batch manifest in the `antifailure_manifest` application property.
Its batch API ignores properties supplied only through HTTP headers.
Splunk stores the same manifest in the indexed `antifailure_manifest` field,
alongside the audit entry's event data.

`AF_AUDIT_STREAM_KEY` is required whenever a sink is named. A manifest signed
under a key nobody chose is decoration rather than evidence.

Remote collector URLs require HTTPS and cannot contain user information.
Loopback HTTP is permitted for a local collector. Redirects are refused, each
request carries a thirty second deadline, and response bodies are discarded
without being buffered or included in error logs.

`AF_AUDIT_STREAM_INTERVAL_MS` is how often a pass runs, ten seconds by default.
`AF_AUDIT_STREAM_BATCH` is how many entries one pass reads, 500 by default, and
`AF_AUDIT_STREAM_DELIVERY_BATCH` is how many one request carries, defaulting to
the pass size. They are two numbers rather than one because how fast the
forwarder catches up and what your collector accepts in one request are
different questions.

A sink named with its variables missing stops the control plane at startup with
the reason, for the same reason the engine's does.

An object store sink exists in the code and cannot be turned on from the
environment, because it needs a request signer this half of the product does not
carry. Naming one is refused rather than accepted and then silently writing
nowhere.

### Delivery, and what happens when your collector is down

Transient failures are retried with at least once delivery. Each organization's
position advances after delivery, so a collector outage causes forwarding lag.
A crash after acceptance and before saving the position can redeliver a batch; use
`orgId` and `seq` to deduplicate. Positions are separate because transactions
from different organizations can commit in a different order from their
sequence numbers. The installation cursor is only an operational summary.

A batch your endpoint will never accept, meaning it answers 400, 401, 403, 404
or 413, is given up on rather than retried forever, because one batch nobody
will ever take must not stop every entry behind it. The rest of the stream
continues.

### What is not forwarded, and it is stated rather than implied

An organization that is not entitled to `audit_stream` is skipped and the stream
moves on past it. It is not held for an entitlement that might arrive later, and
that is the same behaviour the engine has. Its delivery position advances so
these deliberately declined entries are not reconsidered on every pass.

## The licence is asked per action, not at startup

Every sink checks `audit_stream` on every entry rather than once when it is
registered, and the control plane's forwarder asks it once per organization on
every pass. A licence that lapses while a long lived process is running stops
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
AF_AUDIT_BENCHMARK_SYSLOG_ADDRESS=collector.example.com:6514 \
  AF_AUDIT_BENCHMARK_SYSLOG_CA_FILE=/etc/ssl/collector-ca.pem \
  AF_AUDIT_BENCHMARK_WEBHOOK_URL=https://siem.example/ingest \
  just benchmark
```

Related: [licensing](/docs/enterprise/licensing),
[policy](/docs/enterprise/policy), [compliance](/docs/enterprise/compliance).

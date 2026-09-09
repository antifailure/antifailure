# added

`audit_stream` was a licensed feature whose sink nothing ever wrote to.

`extension.AuditSink`, `Registry.AddAuditSink` and `Registry.Audit` were
written, documented and tested, and on the tree this was measured against
`Registry.Audit` had exactly two callers in the whole repository. Both were in
`engine/pkg/extension/extension_test.go`. There was no implementation of the
interface anywhere, and no binary registered one. So a customer who bought
`audit_stream` got a licence that granted a feature, a documentation page
listing what is audited, and not one entry forwarded to anything. Every piece
was present and the behaviour was absent, which is the same shippable gap as a
block button that hides nothing.

The engine now records five privileged actions through the socket: an
environment refused by organization policy, an environment created, an
environment torn down, a golden published to a shared store, and a published
golden pulled onto a machine. The refusal is the one a security team came for,
because it is the only kind of entry that shows a control holding rather than
merely being configured, and it reaches a sink before anything has been created.

The enterprise edition answers with three sinks, each gated on the licence per
call rather than at registration so that a licence lapsing mid-process stops
forwarding without a restart: syslog over TLS, framed by octet count so a
refusal written for a terminal arrives as one entry rather than four corrupt
ones; an HTTPS webhook with a signed body, a bounded retry and a dead letter
file, because a webhook that posts once loses an entry every time its receiver
restarts and loses it silently; and a drop into an object store in the same two
shapes the goldens already use, one object per entry so that a retention policy
and an object lock can hold over it.

An entry now carries `OccurredAt`, because a sink stamps when forwarding
succeeded and a retry can put that minutes after the action. A stream carrying
only the second timestamp reorders itself whenever one destination is slow.

A sink that cannot be reached is reported and does not stop anything. That is
the interface's own contract and it matters most on teardown: a forwarding
outage that stopped an environment being destroyed would turn a logging problem
into a resource leak.

Here is one `environment.created` entry as each of the three sinks actually
puts it on the wire, captured from the sinks themselves rather than described.
The webhook and the object store carry the same bytes:

```json
{"occurred_at":"2026-09-08T19:04:11.082Z","forwarded_at":"2026-09-08T19:04:11.328Z","org":"acme","actor":"dana@acme.example","action":"environment.created","target_type":"environment","target_id":"env_7c31a8","origin":"engine","detail":{"branch":"add-checkout-retry","outcome":"created","project":"shop","repository":"acme/shop","seconds":41.7}}
```

The two timestamps are the point of carrying both: the action happened at
`.082` and forwarding succeeded at `.328`, and only the first is a fact about
the environment.

Syslog wraps exactly those bytes in an RFC 5424 header, with the action as the
message id so a collector can route on it without parsing the payload, and the
byte order mark that declares the payload UTF-8:

```
<110>1 2026-09-08T19:04:11.082Z host antifailure 5799 environment.created - {"occurred_at":"2026-09-08T19:04:11.082Z", ...}
```

The object store writes one object per entry, under a key partitioned by the
day the action OCCURRED rather than the day it was forwarded, so a retention
rule expires entries by when they happened:

```
s3://acme-audit/antifailure/2026/09/08/190411.082000000-environment.created-cafebabe.json
```

It is sent with `If-None-Match: *`, so an entry can never replace one already
there, and signed with SigV4 over the request the store actually receives.

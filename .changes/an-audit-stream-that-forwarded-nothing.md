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

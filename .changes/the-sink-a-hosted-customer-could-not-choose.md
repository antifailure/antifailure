# added

A hosted organization can point its own audit stream at its own collector.

The control plane's audit log reached a sink from pull request 372, and the sink
was one per process, read from `AF_AUDIT_STREAM_SINK`. That is right for a self
hosted installation, where the operator and the customer are the same person.
The hosted enterprise plan sells `audit_stream` to an organization that shares
its process with every other organization on the installation, so an entitled
hosted customer was told their audit log could reach their own security
information and event management system while the only destination in existence
was the operator's.

An organization now owns a destination: a Splunk, Event Hubs or signed webhook
endpoint, and the credential it needs, sealed under the mechanism a customer's
provider key already uses, which never puts the value in Postgres. An owner or
an admin sets it through `/enterprise/audit-stream`, the entitlement is asked
on every request and on every pass of the forwarder, and the forwarder resolves
the destination per organization so one organization's entries reach its
collector and no other. Delivery state is recorded per organization, because a
credential the customer's own system revokes produces a refusal that was
previously visible only in the operator's container log.

A destination supplied by a customer is untrusted input and is held to a
stricter rule than an operator's: HTTPS always with no loopback exception, no
credentials in the URL, and no literal address that is not a public one.

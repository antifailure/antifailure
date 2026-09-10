# added

The enterprise control plane can forward organization audit entries to Splunk,
Event Hubs, or a signed webhook.

The enterprise edition sold "SIEM streaming with a tamper evident hash chain".
Both halves were real and neither was joined to the other. The chain lives in
`audit_entries` and records organization actions including sign on, directory
provisioning and administration. The separate global operator log is exported
only where its writer also produces an organization entry. The streaming was the
engine's, forwarding five actions from a machine with no database. So the log
with the chain in it reached no destination at all, and the sentence could point
at a real half whenever it was questioned.

The forwarder that carries it had been written, tested and left imported by
nothing: not a declared dependency of any package in the workspace, while its
own suite ran green on every pull request because the command that runs it runs
every workspace. `AF_AUDIT_STREAM_SINK` now starts a poll loop over
`audit_entries.seq` that batches per organization, signs a manifest over the
chain head, and tracks delivery per organization. A late commit in one
organization cannot be skipped because another organization committed first.
Transient failures remain pending. Permanent collector refusals are logged and
skipped; the primary audit log is retained either way.

Two things the wiring found. `web/apps/api/src/entitlements.ts` had no
`audit_stream` entry, so the per organization gate answered no for every
organization on every plan and could never have been passed. And forwarding is a
cross tenant read by nature, which now has a policy and a pool scope of its own
rather than a reuse of the sweeper's, so the four existing sweeps do not gain
the ability to read anybody's audit log.

The receiver verifies the manifest's organization, count, sequence range and
chain head against the signed entries. Remote destinations require HTTPS,
redirects are refused, requests carry a deadline, and collector response bodies
are cancelled rather than buffered or copied into logs. Polling stops through
the control plane's shutdown hook.

Event Hubs batches encode each message body as a JSON string and carry the
signed manifest in event properties, so it survives delivery to a consumer.
Splunk retains the manifest in an indexed field beside each audit event.

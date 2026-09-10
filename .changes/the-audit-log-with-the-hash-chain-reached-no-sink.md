# added

The control plane's audit log now reaches a SIEM, an archive, or a webhook.

The enterprise edition sold "SIEM streaming with a tamper evident hash chain".
Both halves were real and neither was joined to the other. The chain lives in
`audit_entries` and is written by every sign on, every directory provisioning
call, every operator impersonation and every admin action. The streaming was the
engine's, forwarding five actions from a machine with no database. So the log
with the chain in it reached no destination at all, and the sentence could point
at a real half whenever it was questioned.

The forwarder that carries it had been written, tested and left imported by
nothing: not a declared dependency of any package in the workspace, while its
own suite ran green on every pull request because the command that runs it runs
every workspace. `AF_AUDIT_STREAM_SINK` now starts a poll loop over
`audit_entries.seq` that batches per organization, signs a manifest over the
chain head, and advances a cursor only past entries a sink accepted, so a
collector that is down costs lag and never an entry.

Two things the wiring found. `web/apps/api/src/entitlements.ts` had no
`audit_stream` entry, so the per organization gate answered no for every
organization on every plan and could never have been passed. And forwarding is a
cross tenant read by nature, which now has a policy and a pool scope of its own
rather than a reuse of the sweeper's, so the four existing sweeps do not gain
the ability to read anybody's audit log.

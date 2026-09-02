# fixed

`af net log -o json` could not say whether the sandbox swapped the credential.

The sidecar records a decision, the engine decodes it, and `af net log` shows
it. Three structs, and until now nothing made their json tags agree. Seven
fields the proxy wrote on every request reached no surface at all:
`substituted`, `host_only`, `via`, `duration`, `seq`, and `waited_ms` and
`limit`, which reached the table and not the JSON.

`substituted` is the one that matters most. It is the answer to the question
the sandbox exists to answer, and a row that reached a real service and a row
that reached a sandbox were the same row. It is now reported on every decision,
as `false` rather than absent when the swap did not happen, because a key that
disappears makes "the credential was not swapped" and "this build cannot report
swaps" the same document. The table says `sandbox credential` and, for an
invented response, the table already said so.

The fix that keeps it fixed is a test rather than this list. One compares the
json tags of the sidecar's record, the decoded decision and the published
document and fails on any fact recorded with nowhere to go; the other fills
every decoded field and fails on any published key nothing assigns. A field
added to the sidecar and forgotten in the other two now fails on the commit
that adds it.

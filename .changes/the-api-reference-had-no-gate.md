# added

`reference/api.md` is checked against the routes the server registers, in both
directions.

It was the only reference page in the documentation with no machine coverage,
and it is the one that drifted. That is not a coincidence: the command
reference, the error reference and the transform reference are all generated or
gated, and none of them has ever been wrong about what exists.

Forward: every URL in the documentation that hangs off one of this product's own
hosts has to match a route the Hono server registers or a page the console
serves. That is what caught `/auth/callback` in four documents.

Backward: every route the server serves has to be covered by a pattern the API
reference names. The page enumerates families rather than routes, `/trpc/*`,
`/v1/*`, `/auth/*`, which is the right way to write it, so the check reads it
that way and a finding is a family nobody mentioned rather than thirty three
lines of noise.

Three families are missing today: `/webhooks/`, `/byok/` and `/console/api/`,
eight routes, including the model proxy that two guides describe in full. That
page is another agent's to fix, so those three sit in a register with a second
assertion behind them: an entry that stops being missing fails as loudly as a
new one appearing. It is a known gap that cannot grow and cannot outlive itself,
rather than an exemption that quietly becomes permanent. Both halves were
watched failing: emptying the register reports the eight routes, and adding a
`/byok/*` row to the page fails the staleness check until the entry is removed.

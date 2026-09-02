# security

An expired token was refused by `/v1/whoami` and accepted by `/v1/events`,
which is the surface that writes.

`authenticateEngine` read `revoked_at` and never read `expires_at`. That was
correct for exactly as long as every row in `engine_tokens` was immortal, and it
stopped being true when the device grant landed: `af login` mints a ninety day
CLI token with an expiry, and the device sign-in path honours it. So a CLI token
that had aged out was correctly turned away from the route that only tells you
who you are, and was still accepted by the route that ingests events.

Both routes now read the expiry. Anybody whose CLI token is older than ninety
days will find event submission refused where it previously succeeded, which is
the point: the token had expired and only one of the two doors was checking.

Revoking a claim on a repository was also unreachable by that repository's name.
`owner/name` contains a slash and one path parameter matches one segment, so the
delete route never matched and answered with a refusal about a missing rate
limit declaration rather than doing the work. It is two routes now, and the
two-segment one is the one a repository name reaches.

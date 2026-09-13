# security

A connection string with one wrong character in it printed its own password.

The standard library describes an address it cannot parse by quoting the
address, and building an HTTP request passes that error back unchanged. The
HTTP client replaces a password with `***` when a request fails, but an
address that never parses never gets that far. So a Vault address with a user
and password and a stray letter in its port, a `DATABASE_URL` whose password
held an unescaped slash, or a signed storage URL with a typo put the credential
into the error that reported the typo, and from there into a terminal, a CI log
or an agent's transcript.

Hiding the address in that error is not enough, because the rest of the error
quotes pieces of the address too: a password holding a slash has its first half
reported as a port, and a stray percent sign has the two characters after it
quoted. The Postgres driver prints exactly that part. So the address is now
redacted before anything describes it. `secret.ParseURL` parses a copy with the
user information, any port that is not a number and the query removed, and
reports what is wrong with that copy, which still names the fault wherever the
fault is not inside the credential.

It is used everywhere an address that can carry a credential is parsed and the
failure can be seen: the cloud credential client and the secret stores behind
it, the audit webhook and object store sinks, all four golden stores, the
production database variable, the database URL a local environment rewrites,
the control plane address, the telemetry export endpoint, the Database Lab
endpoint, a model gateway's base URL, `af net explain`, and the egress probe the
MCP server answers. The audit webhook also kept the URL it was given rather than
the one it checked, so a URL pasted with a trailing newline was accepted and
then failed on every delivery.

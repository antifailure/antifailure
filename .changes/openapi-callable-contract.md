# fixed

The control plane's OpenAPI document described an API a generated client could
not call. Every query said its input was optional, including the routes that
answer 400 without one, and every mutation demanded a body shaped exactly `{}`
even when the route reads no input at all. Both are now taken from the
validators the router actually executes: whether an input exists at all, and
whether the validator refuses an absent one.

The refusal schemas did not match the wire either. One `Error` shape stood for
three different bodies, and validated none of them: the readiness 503 carries no
`error` member, so a client parsing it as an error read `undefined` and reported
the service healthy. Ingestion could answer 400 and 403 and the document listed
neither. There are three schemas now, one per real body, and a test drives real
requests through the real HTTP boundary and checks each answer against the
schema declared for that status.

The event type was published as a closed enum while the server deliberately
accepts and stores a type it has never seen, which is what lets an older control
plane ingest a newer engine's events. A generated client would have refused at
the boundary exactly what the server was built to take. The known values are
published as examples and as `x-antifailure-event-types` instead.

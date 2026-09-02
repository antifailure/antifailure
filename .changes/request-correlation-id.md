# fixed

An unexpected control-plane failure told the caller to find their request in the
logs and gave them nothing to find it with. There was no identifier in the body,
none on the response, and deliberately no query, no parameters and no payload in
the log, so there was nothing on either side to match. Every request now carries
an `x-request-id`, the 500 body repeats it, and the log line carries it beside
the error's class and the driver's code.

The property that made the resolution unusable is the one worth keeping: Drizzle
writes a query failure as the whole statement with its parameters after it, so
the error and its message stay out of the log. A test drives a real query
failure through the real HTTP boundary and asserts the statement reaches neither
the caller nor the log, and it was written after a first version passed while
the handler logged the whole error object, because an Error stringifies to `{}`.

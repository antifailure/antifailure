# fixed

Three defects found by running the arrival orderings rather than by reading
them.

A statement that raises inside a transaction cannot be recovered from on this
database layer. postgres.js records the first failed query and rethrows it after
the callback returns, whatever the callback did about it, so a savepoint around
a constraint violation recovers correctly and the caller still receives the
error. Every collision the control plane turns into a sentence is now a
collision it never causes: `ON CONFLICT DO NOTHING` and a read back.

The default idempotency key for a workload run was a timestamp, so two requests
in the same millisecond collided and the second was answered with the first
one's run. It is random when the caller does not supply one, which is what
"every call is a new run" has to mean.

An empty array bound through the query builder rendered as an empty pair of
brackets, which is a syntax error, so every report with no refused routes, which
is every healthy report, failed the whole ingestion batch.

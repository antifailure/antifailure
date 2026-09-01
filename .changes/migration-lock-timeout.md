# fixed

A migration could wait forever for a lock, and waiting is worse than failing.

Migration `0018` takes ACCESS EXCLUSIVE on `network_rules` and SHARE ROW
EXCLUSIVE on `users`, and the revision still serving traffic writes to both. A
lock request that cannot be granted queues, and every later request queues
behind the request rather than behind the table, so one ordinary transaction
that happens to be open on `users` at the wrong moment turns a two second
migration into a sign-in outage lasting as long as that transaction does.
Nothing bounded it: `lock_timeout`, `statement_timeout` and
`idle_in_transaction_session_timeout` are all zero on the flexible server.

The migration runner now gives up after three seconds of waiting. Three seconds
because the number separates two populations rather than measuring one: an
uncontended deploy is granted its locks in milliseconds, and a blocked one is
blocked by something unbounded. It is not tied to how long a migration takes,
because `lock_timeout` bounds the wait and not the work.

`statement_timeout` is set beside it at five minutes, for the same outage from
the other end: a migration whose own statement never finishes holds the lock
itself, and a lock timeout is blind to that because the lock was granted. Five
minutes is above `0018`'s measured 2.7 seconds against 200,000 rows by a
hundredfold, and below the bootstrap job's own 900 second timeout, which matters
because a job killed by its timeout leaves the lock held on a connection
Postgres has not yet noticed is gone.

Both are set on the migration runner's reserved connection rather than on the
role or the bootstrap job, because `migrate` has seven callers and a guard in
the job would leave the path the server itself runs under `AF_MIGRATE=1`
unprotected. Both are set after the advisory lock rather than before:
`lock_timeout` applies to `pg_advisory_lock` too, so setting it earlier would
turn two deploys racing each other into a failed deploy when waiting is the
correct answer to that race. Both are reset before the connection goes back to
the pool, because a session setting survives that and the next borrower would
otherwise inherit a five minute statement budget it never asked for.

A timeout failure now says which migration, that it was waiting for a lock, that
nothing was applied so the retry is safe, and the query to find the holder if it
happens twice.

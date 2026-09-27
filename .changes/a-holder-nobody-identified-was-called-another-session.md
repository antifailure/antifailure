# fixed

`af load sql` said a lock was held by another session on the database when
nothing had identified the holder at all.

A wait pair's holder has three outcomes and the result carried two. It can be one
of this run's own clients, it can be another session on the database, or it can
be NOT NAMED: `pg_blocking_pids` returns an empty array for a backend that is
genuinely waiting when the holder disconnected between the two reads. The third
arrived looking exactly like the second, so the report printed "waited on another
session on this database" about a lock whose holder nothing knew, which is a
positive claim about whose lock it was made by something that did not know. It is
the same defect as reading a null wait count as a zero, one level in: an absence
rendered as a finding.

It is also a prepared transaction. `pg_blocking_pids` reports a two phase commit
holder as pid ZERO rather than omitting it, so the holder is named while nothing
in `pg_stat_activity` answers for it. Reported as another session, that sends a
reader looking for a session to cancel when there is none: the lock goes when
somebody runs `COMMIT PREPARED` or `ROLLBACK PREPARED`. A wrong remedy rather
than only a wrong label, so it gets its own sentence.

Why it survived review and a merge, which is a shape worth naming: it is RARE
exactly where it is tested and CERTAIN exactly where it is shipped. The same assertion failed on one head of another branch and
passed on a later one with nothing between them touching this package, because as
a superuser it fires only when an unnamed or stateless holder happens to turn up
in a sample, which is load dependent. Off superuser, or with a prepared
transaction outstanding, it is every run. A defect with that profile passes
review, passes CI, and fails at a customer.

The three are now distinct, the run still reports the wait when the holder is
unnamed, because the wait is the part that was measured, and a reader is told "a
holder the server would not name" instead of being given a stranger.

Also fixed: a test demanded a holder state the server is entitled to withhold. An
empty state has three causes and none of them is that the holder was doing
nothing. The first is the ordinary case for a customer pointing this product at
their own Postgres: a role without superuser and without `pg_read_all_stats` sees
a foreign backend's row while its state, type and query are withheld, so off
superuser a named holder has no state every time rather than occasionally. The
second is any backend that is not a client, of which an idle server has several.
The third is the holder's row being gone by the time the join runs.

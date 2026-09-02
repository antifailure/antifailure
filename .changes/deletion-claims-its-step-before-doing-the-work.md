# fixed

An organization deletion claimed its step after doing the step's work, so a
second caller did all of it before being told it had lost.

`stopWork` ran four statements in one transaction: tear down the environments,
cancel the runs, suspend the organization, and only then mark the record with
`WHERE work_stopped_at IS NULL`, returning false if that matched nothing. The
comment above `advanceDeletion` says every step's write carries `WHERE <its
timestamp> IS NULL` so that two callers arriving at once do not both do it.
That was true of the bookkeeping row and false of the three statements that do
the work.

This was not reachable as data loss, and saying so precisely is the point.
Each work statement locks the rows it touches and re-evaluates its `WHERE`
after the winner commits, so the loser found nothing left to change and the
counts stayed right. That is a real protection, it is not the one the comment
describes, it is written down nowhere, and it holds only for as long as every
step happens to touch a row the other caller also locks.

The claim now comes first. The loser blocks on it, re-reads after the winner
commits, matches nothing, and returns before it has torn down an environment.

The ledger moves with it. `environments_stopped` and `runs_cancelled` were
taken from whichever caller won the final statement, which is not necessarily
the caller that did the work, so a deletion that stopped one environment could
record that it stopped none. They are now written by the caller holding the
claim, which is now necessarily the one that did the work.

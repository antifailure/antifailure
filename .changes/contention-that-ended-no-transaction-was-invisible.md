# added

Lock contention, observed while the concurrent SQL workload runs, reported with
both statements named, and expressed as two measures a baseline comparison can
carry.

The product could already say that a run deadlocked or lost a serialization
race. Both of those END a transaction, so the client gets a SQLSTATE and
somebody writes it down. The commonest outcome of lock contention ends nothing
at all: a transaction queues behind another one, gets its lock, and commits.
Nothing is raised, nothing is retried, and a build that takes a lock a little
earlier or holds it a little longer moves the percentiles and changes no other
number in the result. So a slower run could be reported as slower and could not
be reported as slower BECAUSE it blocked, which is the difference between a
number and a cause.

The only `pg_locks` query in the repository sampled the MIGRATION rehearsal,
filtered to `locktype = 'relation'`, and answered a different question: what one
DDL statement HOLDS and for how long, on a branch nothing else is using. It
reports a hold whether or not anybody ever waited behind it, and it is
untouched here. `pg_blocking_pids` had never been called anywhere.

The watching connection that already proves the run's clients overlapped now
also asks `pg_blocking_pids` which of this run's own backends are in a lock
queue and which backends stand in front of them. Not a third connection, and
never the workload's own clients: an observer that competes with the thing it
observes is measuring itself.

`af load sql` reports how many times one of its clients started waiting, how
many backend milliseconds of waiting the samples found, and the pairs. Both
sides of a pair are named with the mix's own statement labels rather than with a
process id, because each client publishes what it is about to execute and
withdraws it when the statement returns. A pid means nothing between two runs; a
label is the thing somebody acts on. A holder with no statement against it was
idle in transaction, which is to say holding every lock it had taken and running
nothing, and that is usually the finding.

The waiter is always one of this run's clients, because a neighbour's wait is
never this run's result. The holder may be anything else connected to the same
database, and a run blocked from outside itself says so rather than blaming its
own mix.

`lock_waits` and `lock_wait_ms` reach the workload result, the stored history
and `af workload compare`, so "this build blocked more than the last one" is a
sentence the comparison can now make. They are null rather than zero when
nothing watched, and that rule matters more here than anywhere else it is
already applied: zero lock waits is the most reassuring thing this product can
say, so an instrument that did not run must not be able to produce it.

What the sampling cannot see is stated in the output rather than in a document.
The wait queues are read every 200 milliseconds, so a wait that began and ended
between two samples is missing entirely and the counts are floors rather than
totals. Every lock type the server queues on is in scope, including the
transaction id waits a row conflict produces, tuple locks and advisory locks,
and each pair says which kind it was. Contention that never becomes a wait is
out of scope by definition.

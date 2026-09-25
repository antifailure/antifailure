# fixed

A lock finding could name a table nothing had locked, and a contention count
could be raised by a run nobody was looking at.

Both readings are taken from `pg_locks` and `pg_stat_activity`, and both of
those views are cluster wide: they show every backend and every lock on the
server, not only the ones in the database you asked from. `pg_locks` names a
relation by object id alone, and the documentation says what that costs in the
sentence after it, that joining that column to `pg_class` "will only work
correctly for relations in the current database". Neither reading carried that
condition, and the workload's reading scoped its backends by application name
alone, which every run of this product shares.

The topology is what turns that from a curiosity into a wrong number. On a host
server, which is how a self hosted or managed Postgres is used here, the
provider creates a database per golden and a database per environment on ONE
server. A branch is `CREATE DATABASE ... TEMPLATE`, a server side file copy that
carries `pg_class` verbatim, so two branches of one golden agree on the object id
of every table in them. A foreign lock therefore resolved against the local
catalogue and came back wearing a real local table name rather than an obvious
error. Measured on two such copies, with the table renamed in one of them so the
misresolution could not hide behind a matching name: a session queued on the
second branch's `shipments` was reported from the first as a wait on `orders`, a
table nothing had waited on.

Three things were wrong, and the quietest is the one that reached stored history.

A migration's lock report could name a relation from another branch, at
`AccessExclusiveLock` and a duration that fails the check, for a change that had
touched nothing of the kind. It could also mark a lock as contended because a
session in another branch was queued behind a relation that merely shared an
object id, and a contended lock is reported even when its duration is below the
threshold that would otherwise ignore it, so a foreign queue could promote a
harmless hold into a finding and then explain it with a sentence that was not
true of anything.

A concurrent SQL workload's contention count could be raised by a neighbouring
run. That count is not a label: it is the number the result carries as how many
times one of this run's own backends was seen to start waiting, and it is what a
two build comparison differences. The backend counts beside it, the peak active
backends and the peak open transactions, could count a neighbour's sessions as
this run's own.

A migration's lock report also named TOAST tables and temporary tables. The
filter excluded two namespaces by name and every TOAST relation lives in a
third, so `pg_toast_2618` reached a pull request, and a table rewrite put the
author's own `pg_toast_<oid>` above the table they changed at the strongest lock
mode there is, under a name they had no way to look up. Reporting a temporary
relation is worse than noise, because nothing in production can queue behind one.

All three readings now ask for the database they are about, and the lock report
names only relations in a namespace the project owns. Nothing is lost: a lock in
another database cannot queue a query in this one, and Postgres refuses to create
any schema whose name begins with `pg_`, which a test measures from the server
rather than trusting.

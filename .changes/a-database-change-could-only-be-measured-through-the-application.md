# added

A concurrent SQL workload, so a database change is measured as a database
change.

Everything this engine could send at a preview environment went over HTTP. A
load run sends a weighted mix of requests, a scenario walks a journey, a
workflow drives a browser. All three reach the database only through the
application, so every number they report is the application's latency with the
database somewhere inside it. That is the right measurement for an application
change and the wrong one for a change to an index, a lock, a storage parameter
or a query: the person making that change wants transactions per second and the
cost of one statement, and could only reach either through whatever the
application happens to do on a route they can call.

`af load sql` opens connections to the branch and runs statements on them. N
clients, each on its own connection, each running whole transactions inside one
BEGIN and COMMIT, with think time between them and a seed that makes two runs
execute the same sequence. It reports throughput, transaction latency, the cost
of every statement, deadlocks, serialization failures and retries. A deadlock
that was retried into a commit is counted as contention rather than as a
failure, because that is what a correct application does about a deadlock.

The statements come from a document in the repository, or from
`pg_stat_statements` on the branch, which is the traffic that really ran
weighted by how often it ran. The derived path cannot recover the parameter
VALUES, because the statistics normalise them away, so it prepares each
statement to ask the server for the parameter TYPES and generates values of
those types. Two consequences are reported rather than hidden: a write is
refused unless the manifest allows one, and every run says how many rows its
statements actually touched, so a run that matched nothing cannot be read as a
fast one. A statement that will not prepare at all is refused by name with the
server's own message, which catches a query against a column the change dropped
before a single transaction runs.

The claim that a run was concurrent is measured rather than made. A separate
connection samples `pg_stat_activity` while the run is going and reports how
many distinct backends it saw and how many were inside a transaction at one
instant. N goroutines are not N database sessions. A run whose observer could
not connect reports nothing rather than zero, because "no overlap" is a finding
and "nobody looked" is not.

A run that committed no transaction reports no throughput and no latency and
exits non-zero. Every threshold it carries passed over an empty measurement,
which is not the same as passing.

`sql_workload` is a fifth `workload_kind` in the control plane, so a hosted run
dispatches it, stores its numbers and reproduces through the same plain
`af load sql` command. Migration 0048 also rebuilds the shape constraint the
four kinds already had: it was a CASE with no ELSE, and a CASE with no ELSE
returns NULL for an unmatched value, which makes a CHECK pass. A fifth kind
would have been entirely unconstrained while the constraint went on claiming
otherwise.

# added

Antifailure could not inject a failure into the system it rehearses, so it had
nothing to say about what that system does when a process dies, a node goes
away, a disk fills or the network splits. The nearest thing was
`engine/chaos`, which breaks Antifailure's own engine, and the backup drill,
which restores Antifailure's own control plane database. Neither one touches a
customer's environment, and a Postgres storage engine developer who asked
whether the product could kill a database and validate its crash recovery was
asking about something that did not exist.

A new `chaos:` block declares faults a rehearsal may inject, and `af chaos`
runs them. Seven kinds, all real: `SIGKILL` to a process inside a container,
`SIGKILL` to a container's main process, a clean stop, a cgroup freeze, a
detach from the environment's network, a data directory made read only, and a
bounded fill of the filesystem holding it. Each one carries an undo that runs
even when the run fails, and each one is aimed at a container resolved from the
labels the runtime stamped at create time. A container with no Antifailure
label, a container belonging to another environment, and the egress sidecar are
refused, and the ownership is read again from the daemon at the instant of the
act rather than trusted from the moment the target was resolved.

Around a fault aimed at the database, the run proves the recovery instead of
observing that it finished. Concurrent writers commit while the fault lands,
and a ledger kept on the client side of the wire records exactly which commits
the database acknowledged. Afterwards: every acknowledged commit must still be
there, nothing may be there that no client attempted, the write ahead log must
have replayed from the position the control file named before the crash to past
the last flush a writer saw, and the heap and its index must still agree. The
first two claims are about what the database SAID, which the database itself
cannot answer, and they are the ones the question was really about.

A fault that was applied and changed nothing is refused rather than reported as
survived, because every assertion after it would describe a system that never
broke. A `process_kill` whose pattern matches nothing, a `read_only_data` fault
on a directory whose owner can still write it, a pause the daemon accepted that
left the container running: all three are refusals. In the same spirit,
findings split across two policy keys. `policy.chaos_failure` defaults to
`fail` and covers a lost commit, a phantom row, a short replay and a damaged
relation. `policy.chaos_unverified` defaults to `warn` and covers everything
the run could not establish: nothing crashed, no replay is recorded, the
control file would not parse, checksums are off so a torn page would not have
been seen. A check that found a problem and a check that could not look are
different facts.

Network latency and packet loss are not included, and that is a stated limit
rather than an omission. Shaping traffic needs `tc` in the target's network
namespace, which the images do not carry and which the environment cannot fetch
through a default deny egress policy. A declared fault that silently did
nothing would be worse than an absent one.

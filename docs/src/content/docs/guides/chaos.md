---
title: Fault injection and crash recovery
description: Break the environment on purpose, then prove the database did not lose a commit it said it had.
sidebar:
  order: 28
---

A rehearsal tells you what a change does to a system that works. The chaos
block tells you what the system does when it stops working, and then it proves
the answer instead of reporting that everything came back.

```yaml
chaos:
  enabled: true
  faults:
    - name: postgres-crash
      kind: process_kill
      target: database
      process: "postgres: checkpointer"
```

Run it with `af chaos` against a running environment, or let `af ci` run it at
the end of a check.

## What it proves

Around a fault aimed at the database, concurrent writers commit into a schema
the engine owns, and the fault lands while they are committing. Afterwards the
run establishes four things:

1. **No lost durable commit.** Every transaction the client was told was
   committed is still there.
2. **No phantom commit.** Nothing is there that no client ever tried to write.
3. **The write ahead log replayed.** Recovery started at the position the
   control file named before the crash, and reached past the last flush a
   writer saw.
4. **The relations survived.** A sequential scan and an index only scan count
   the same rows, and `amcheck` finds an index entry for every live heap tuple.

The sequential scan also reads every page of the writers' table, and on a
cluster with data checksums on, a page torn by the crash fails its checksum and
stops that read. `af chaos` prints the result on each crash fault's `pages`
line. It covers the writers' table and no other, and it says the pages were
not checked when checksums are off, when the control file could not be read
after the fault, or when the read did not finish. The `amcheck` line beside it
prints what the index verifier said, or that it did not run.

The first two need something the database cannot give you, because they are
claims about what the database *said* rather than about what it holds. The
engine keeps a ledger on the client side of the wire: an identifier goes in
before the statement is sent, and moves to acknowledged only when the call
returns without an error. A commit that returned success and is absent
afterwards is a durability failure whatever caused it.

## The faults

| Kind | What happens | Undo |
| --- | --- | --- |
| `process_kill` | `SIGKILL` to one process inside the container, matched by a substring of its command line. The container keeps running. | None. The recovery is the system's own, and that is the fault. |
| `container_kill` | `SIGKILL` to the container's main process. The container stops. | Starts it again. |
| `container_stop` | `SIGTERM`, then `SIGKILL` after a grace period. | Starts it again. |
| `container_pause` | Freezes every process with the cgroup freezer. Nothing is killed and no connection closes. | Thaws it. |
| `network_partition` | Detaches the container from the environment's network. | Attaches it again, with the aliases it had. |
| `read_only_data` | Removes write permission from the data directory. | Restores the mode it recorded. |
| `disk_fill` | Fills the filesystem holding the data directory to a stated headroom. Needs `database.data_filesystem.size_bytes`, below. | Removes the file it wrote. |

`process_kill` and `container_kill` are the two kinds that stop Postgres
uncleanly, so they are the two the recovery proof expects a replay from. The
others are useful and they are honest about what they are: a `container_stop`
shuts the database down cleanly and replays nothing, and a run that declared it
as a crash reports that it could not establish a recovery rather than reporting
a clean one.

## What it will not touch

A fault reaches the containers this environment created and nothing else. The
target resolves from the labels the runtime stamped at create time, never from
a name a fault supplied, and the ownership is read again from the daemon at the
instant of the act. Three refusals have no override:

- a container carrying no `dev.antifailure.managed` label is not ours
- a container belonging to a different environment
- the egress sidecar and the emulators, whatever environment they belong to

The sidecar carries the egress policy. A fault that could stop it would switch
off the control that decides what the environment may reach, and a chaos
feature that can disable a safety control is a way out with a feature name. An
emulator stands in for a third party the environment must not reach, so
stopping one does not produce an outage: it produces a request that goes
looking for the real host.

`disk_fill` carries a fourth refusal, and a declaration that lifts it.

A container's writable layer is the daemon's own disk, so filling a directory
on it fills the machine and every other container running on it. The fault
reads the mount at the data directory from the daemon and refuses unless it is
a volume this environment created with a size fixed when it was created. A
mount of its own is not enough on its own: a plain named volume is its own
mount and is still a slice of the daemon's disk, so it would pass a device
check and take the machine down having satisfied the guard. Both refusals are
reported as `chaos.fault.unsafe`: the claim the fault was declared to establish
was not established, and nothing else in the run was touched by it.

## Giving the data directory a filesystem of its own

```yaml
database:
  storage:
    size_bytes: 536870912
chaos:
  enabled: true
  faults:
    - name: fill-the-data-volume
      kind: disk_fill
      target: database
      headroom_bytes: 8388608
      max_fill_bytes: 536870912
```

With that, the branch keeps its data directory on a filesystem of the declared
size and `disk_fill` lands: the fill writes one file until the stated headroom
is left, Postgres meets a real `No space left on device` on its next extend,
and the undo removes the file and the free space comes back. Without it the
data directory is on the writable layer and the fault is refused before it acts.

The filesystem is held in memory, and that is the containment argument rather
than an implementation detail. A volume on the daemon's disk cannot be filled
without taking space from every other container on the machine; one in memory
has a size fixed at creation and takes nothing from anything outside the
environment. Three things follow, and they are the cost of the feature:

- The whole database lives in it, so the size has to hold the data directory
  with room left for the fault to fill. A copy that does not fit is refused by
  name, with both numbers, rather than truncated.
- A size of more than half the memory the Docker daemon reports is refused.
  A filesystem in memory larger than the machine moves the same problem from
  the disk to the memory, and a daemon killed for memory takes every other
  environment with it.
- The data directory does not survive the Docker daemon restarting. `af up`
  builds it again from the golden.

The branch pays a copy of the data directory when it comes up, where an
ordinary branch pays nothing because the daemon's storage driver copies on
write. So this is the layout for rehearsing a disk that fills, and not the one
to measure how a disk performs.

The environment also runs one container that holds that filesystem mounted and
does nothing else. It is not decoration: the local volume driver unmounts a
memory backed volume when the last container using it stops, so without it a
`container_kill` or `container_stop` would delete the data directory rather
than crash the database, the undo would start a container that initialised an
empty one, and the durability proof would report every acknowledged commit
lost. Faults refuse to touch it for the same reason they refuse to touch the
egress sidecar.

## Nothing that changed nothing counts as survived

A fault that was applied and had no effect is refused, not reported. The
reason is the whole point of the feature: every assertion after such a fault
describes a system that never broke, and a recovery check that passes on one is
a check that answers the same whether or not it ran.

So a `process_kill` whose pattern matches nothing is refused rather than
reported as a crash the database survived. A `read_only_data` fault probes a
write as the directory's owner and refuses if the write still succeeds, which
is what happens on a directory owned by root, because root ignores the mode.
A `container_pause` that the daemon accepts and that leaves the container
running is refused.

The same discipline runs through the findings. A run that could not establish
what it set out to is reported as unverified and never as a pass:

| Finding | Meaning |
| --- | --- |
| `chaos.durability.lost_commit` | A transaction the client was told was committed is gone. |
| `chaos.durability.phantom_commit` | A row is present that no client wrote. |
| `chaos.recovery.replay_short` | Recovery stopped before the last position the client saw flushed. |
| `chaos.recovery.timeline_moved` | The timeline changed, and crash recovery does not change it. |
| `chaos.integrity.relation_damaged` | The heap and its index disagree. |
| `chaos.recovery.no_crash` | The fault was declared as a crash and nothing crashed. |
| `chaos.recovery.no_replay` | The database came back and the log records no replay. |
| `chaos.integrity.checksums_off` | Data page checksums are off, so a torn page would not be seen. |
| `chaos.integrity.amcheck_unavailable` | The index could not be verified. |
| `chaos.durability.inconsistent_ledger` | The engine's own bookkeeping does not add up. |
| `chaos.fault.refused` | A fault tried to go in and failed, so it established nothing. |
| `chaos.fault.unsafe` | A fault was refused before it acted, because its effect would reach past this environment. It changed nothing the other faults measured. |
| `chaos.fault.not_undone` | A fault went in and its undo failed, so the environment is still broken and anything measured after it is suspect. |

The first five are failures and carry `policy.chaos_failure`, which defaults to
`fail`. The last eight are the ones the run could not look at, and they carry
`policy.chaos_unverified`, which defaults to `warn`. They are two keys because
a check that found a problem and a check that could not look are different
facts, and reporting the second as the first teaches a project to ignore both.

## Asking for a run that loses data

`crash_recovery.synchronous_commit` sets what the writers ask of the database.
With it off, Postgres acknowledges a commit before the write ahead log record
has left shared memory, so a crash that discards shared memory loses commits
the client was told were durable. That is the setting's documented behavior and
the run reports the loss:

```yaml
chaos:
  enabled: true
  crash_recovery:
    synchronous_commit: off
  faults:
    - name: prove-the-check-can-say-no
      kind: process_kill
      target: database
      process: "postgres: checkpointer"
```

Leave it out unless you mean it. A manifest that sets it to `off` is asking for
a run that is expected to report lost commits, which is useful exactly once:
to see the check say no before you trust it saying yes.

## Reading the numbers

The `unreachable` line is measured by a probe that starts with the fault and
runs beside it. Every 100 milliseconds it opens a connection and runs
`SELECT 1`, and an attempt that gets no answer within a second counts as
unanswered. The outage runs from the first unanswered attempt to the first
answer after it, so it is known to the probe's interval, which the line
prints: `unreachable    110ms, probed every 100ms`. The settle, the undo and
the stopping of the writers happen while the probe runs and are not part of the
number. When every attempt was answered the line says `never` rather than
printing a zero. A frozen database counts as unreachable: the kernel accepts
the connection and nothing answers it.

The first crash after `af up` can take noticeably longer to recover than later
ones. Before it replays anything, Postgres syncs every file in the data
directory to disk (`recovery_init_sync_method`, which defaults to `fsync`), and
on the first crash those files include every page written when the branch was
created. Measured on the demo ledger, that step took between 1.9 and 8.4
seconds on the first crash after bringing the environment up, and under 0.2
seconds on the crashes after it. The database's log shows it between
`database system was interrupted` and `redo starts at`, and with
`log_startup_progress_interval` lowered it prints `syncing data directory
(fsync)` as it goes. It is Postgres making the data directory durable before
trusting it, not the fault or the engine, and how long it takes depends on the
disk under the container.

## Tuning

| Key | Default | What it is |
| --- | --- | --- |
| `crash_recovery.writers` | 8 | Connections committing at once. |
| `crash_recovery.commits_before_fault` | 200 | Acknowledged commits before a fault lands. |
| `crash_recovery.recovery_timeout` | `2m` | How long the database has to answer a query again. |
| `faults[].after` | `5s` | A floor on how long the run waits before the fault: with the writers committing around a database fault, and as a plain wait before any other. |
| `faults[].hold` | `3s` | How long the fault stays in place. |

Every fault reports how long it was in place, measured from the moment the
injection returned to the moment its undo began, beside the hold it declared:
`It was in place for 5.001s (declared 5s), then undone.` in the terminal and
the pull request comment, and `in_place_ms`, `hold_declared_ms` and `in_place`
in the MCP result. The `duration_ms` beside them is the whole step, including
the wait before the fault, and is not how long the fault lasted.

Around a database fault the fault is undone at its hold and the writers are
stopped after it, so a freeze lasts as long as it declares. Commits the writers
make after the undo are counted and checked like every other: each one the
client was told was committed must still be there.

`commits_before_fault` counts commits rather than seconds on purpose. A second
on a loaded machine can be a second in which nothing committed, and a crash
with nothing to lose passes every durability assertion by having none to make.

## Limits

Faults run on the local runtime, against Docker containers. On Kubernetes the
run reports `AF-CHS-007` rather than injecting anything.

Network latency and packet loss are not implemented. Shaping traffic needs
`tc` inside the target's network namespace, which the database and application
images do not carry and which the environment cannot fetch, because everything
it reaches goes through a default deny egress policy. A declared fault that
silently did nothing would be worse than an absent one, so the kind does not
exist. `network_partition` is the network fault that does work.

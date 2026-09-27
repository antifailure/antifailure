---
title: Comparing two database builds
description: Run one workload and one set of rows against a baseline and a candidate build of your own Postgres, then break it and prove what survived.
sidebar:
  order: 29
---

If you build Postgres itself, or a storage engine inside it, the question you
need answered is not whether your application got slower. It is whether your
build did, on the same rows, under the same workload, against the build it
replaces. And then whether it still holds a commit it acknowledged after it
crashes.

This page walks that end to end. Every other page here compares two builds of an
application over one database; this is the other axis, and it is five steps.

## What you get, and what holds still

One data directory, two database builds. One build writes the rows and the other
opens them, which is the asymmetry that makes the comparison mean something: a
second set of rows would turn every difference in the report into a difference in
the data.

Held still: the golden both sides branch, the application revision, the tree that
revision is compiled from, the client count, the think time, and the per round
seed that decides the transaction order and every generated parameter value.
Varied: one thing, the database build.

## Step 1: declare the build under test

`database.image` is the build every environment for this project runs.

```yaml
database:
  provider: docker
  version: 17
  image: your-registry/postgres:candidate
```

The image has to be a Postgres the manifest can use, and that is checked against
the server rather than against the tag, because a tag is a string somebody chose.
A build whose `server_version_num` disagrees with `version`, or that is missing
an extension the manifest declares, is refused before anything is built.

## Step 2: run one workload against both builds

The workload is a document of whole transactions, not a list of statements, so
the locks a transaction holds between its statements are part of what runs. See
[SQL workloads](/docs/concepts/sql-workloads) for the document's own reference.

```yaml
load:
  comparison:
    enabled: true
    thresholds:
      throughput_drop: 0.25
  sql:
    source: declared
    script: workload.yaml
    clients: 8
    duration: 30s
    think_time: 100ms
```

Then name the other build on the base side:

```
af load compare --sql --baseline HEAD --baseline-image your-registry/postgres:baseline
```

`--image` and `--baseline-image` each default to `database.image`, so naming one
varies that side and leaves the other where it was. Naming a base revision equal
to this one is normally refused, because two identical builds of one application
are nothing to compare. With two database images it is the point, and the report
says so.

### Which build writes the pages is a choice, and it is probably the one you care about

There is one golden and one build made it: the build `database.image` names. The
side that names a different image OPENS a data directory it did not write. So the
two arrangements answer two different questions, and the flags let you pick.

- Declare your candidate and name the old build with `--baseline-image`, as above,
  and your candidate laid the pages out while the old build reads them.
- Declare the old build and name your candidate with `--image`, and your candidate
  is the one opening a data directory the trusted build wrote.

The second is usually the question a storage engine team is really asking, because
it is what an upgrade does to data that already exists. The report names the
writer on every run, so you never have to remember which way round you ran it.

## Step 3: read the throughput and the distribution

The run this section shows came from the command above, against
`examples/go-api` in the Antifailure repository, with `--rounds 8 --duration 10s
--warmup 3s`, and with two published images rather than the placeholders above,
because a run has to name images that exist. The candidate side ran the stock image
for Postgres 17 and the base side ran `pgvector/pgvector:pg17`, which is the same
major built against glibc instead of musl, so nothing about the two is the same but
the on disk format. That is what makes them a usable stand in for two builds of one
engine.

That example ships with the `load.sql` block and without the `load.comparison`
and `chaos` blocks above, so the two were added to its manifest for these runs and
taken out again. It is a reference manifest and turning fault injection on in it
would turn it on for every check that reads it.

```
  46f132cbf2b8 against 46f132cbf2b8
  the base was resolved the merge base with HEAD
  the axis that differed is the database build, pgvector/pgvector:pg17 against
  the stock Postgres image for the declared major version, on one application
  revision
  the golden was made on the stock Postgres image for the declared major
  version, so a side on another build opened a data directory it did not write

  declared statements, the reads this API serves, and the order it writes, 8
  clients on each side.

  MEASURE                     BASE  THIS BUILD   CHANGE  MOVED
  error_rate                     0           0     none  same
  p50_ms                      6.78        6.12    -9.8%  better
  p95_ms                      22.6        33.4   +47.9%  worse
  p99_ms                      33.3        76.6  +130.3%  worse
  tps                         75.5        74.3    -1.6%  worse
  transactions                 756         744    -1.6%  worse
  transactions_failed            0           0     none  same
  retries                        0           0     none  same
  deadlocks                      0           0     none  same
  serialization_failures         0           0     none  same
  statements_run               970         954    -1.6%  worse
  lock_waits                     0           0     none  same
  lock_wait_ms                   0           0     none  same
  rows_touched            3.67e+03    3.68e+03    +0.2%  unmeasurable
```

Then the same numbers per transaction, and per statement inside it:

```
  Latency is p50 / p95 / p99. The change and the verdict are on the p95.
  a customer's orders
    BASE        8.29 / 23.9 / 43.4ms
    THIS BUILD  7.99 / 27.2 / 88.3ms
    P95 CHANGE  +13.9%
    MOVED       too close to say
    CAN SEE     179%

  read one order
    BASE        5.98 / 19.5 / 29.9ms
    THIS BUILD  5.25 / 18.2 / 63.8ms
    P95 CHANGE  -6.8%
    MOVED       too close to say
    CAN SEE     147%

    their orders
    BASE        1.91 / 6.86 / 14.5ms
    THIS BUILD  2.16 / 9.89 / 30.8ms
    P95 CHANGE  +44.3%
    MOVED       too close to say
    CAN SEE     115%
```

Read that run the way it asks to be read. The run wide `p95_ms` moved 47.9
percent and every unit says `too close to say`, because eight rounds of ten
seconds on a developer laptop can see a change of 115 percent at best. Nothing
there is a finding about either build. It is a demonstration that the pipe is
connected and an illustration of the column that stops you believing the
headline.

Throughput is committed transactions a second, judged against
`load.comparison.thresholds.throughput_drop`. The distribution is reported per
transaction and per statement inside it, as p50, p95 and p99 on both sides.

Read the `CAN SEE` column before you read the change. It is the smallest change
that unit could have shown on this host, measured from how much the rounds
disagreed with each other, and a change inside it is reported as `too close to
say` rather than as a result. A quiet machine, more rounds, or a longer duration
narrows it. A number that a noisy host could have produced by itself is not a
finding, and this is the column that tells you which you have.

The report then states which axis differed and which build wrote the pages:

```
  both sides ran the same application revision 46f132cb, built from the same
  tree, and differed only in the database build, pgvector/pgvector:pg17 against
  the stock Postgres image for the declared major version, so a difference in
  these numbers is the database's and not the application's
  the golden's data directory was written by the stock Postgres image for the
  declared major version and opened by pgvector/pgvector:pg17, so the base
  branch read pages another build laid out; a build that could not open it at
  all would have been reported as a finding rather than as a slow round, and one
  that opened it is being measured partly on how well it reads another build's
  layout
```

Both sentences are in the JSON report as well, under `notes`, beside
`"axis": "image"` and each side's own `image`. A run that named no database build
prints neither and reports `"axis": "revision"`, so nothing has to be inferred
from their absence.

## Step 4: break it and read what survived

```yaml
chaos:
  enabled: true
  faults:
    - name: postgres-crash
      kind: process_kill
      target: database
      process: "postgres: checkpointer"
```

```
af chaos
```

`process_kill` sends `SIGKILL` to one process inside the database container and
leaves the container running, which is the real crash: the postmaster discards
shared memory and replays its write ahead log.

Around a fault aimed at the database, concurrent writers commit into a schema the
engine owns while the fault lands. Afterwards every commit a client was told had
committed must still be there, and nothing may be there that no client ever
wrote. That needs a record the database cannot give you, because the claim is
about what the database said rather than about what it holds, so the ledger is
kept on the client side of the wire.

This is `af chaos` against the example in this repository, on one build:

```
Breaking it on purpose

  ok    postgres-crash               process_kill on database

  sent SIGKILL to pid 27 (postgres: checkpointer)

  It was followed by a wait of 3.001s (declared 3s) before the result was read,
  since a killed process has no undo.
      crash          a server process was killed by signal 9
      replay         0/19EF838 to 0/1AC8F90
      commits        4513 acknowledged, 0 lost, 0 phantom, 2 in flight landed
      relations      heap 4515, index 4515
      amcheck        the index verified, with every heap tuple present in it
      pages          not checked, because data checksums are off on this cluster
                     and a torn page would read back as data
      unreachable    10.936s, probed every 100ms

  warn  chaos.integrity.checksums_off Data page checksums are off on this
                                      cluster
```

`replay` is the evidence that recovery actually happened rather than the
container merely coming back: the position recovery started from, against the one
the control file named before the crash, and the position it reached. `commits`
is the ledger, and `4513 acknowledged, 0 lost` is the claim this whole step
exists to make. The two in flight are transactions the client never heard an
answer for, which are free to land or not; the failure would be a commit in the
acknowledged column and absent from the table.

The `pages` line is what an honest instrument looks like when it could not look.
This cluster has data checksums off, so a page torn by the crash would read back
as data rather than be reported, and the run says that instead of counting the
read as a pass. Initialise your cluster with checksums on and that line becomes
a measurement.

Anything that could not be established is reported as unverified rather than as a
pass, and a fault that changed nothing is refused outright, because every
assertion after it would be measuring a system that never broke. For the full
account of the faults and the four durability claims, see
[Fault injection and crash recovery](/docs/guides/chaos).

## Step 5: ask your own rules of the recovered data

The durability proof is about the engine's own ledger. Your schema has rules of
its own, and they are worth asking after a crash as well as before one.

```
af invariants
```

```
Asking the data

  invariants
  ok    no-orphaned-orders           held in 13ms
  ok    no-negative-totals           held in 1ms

  2 held, 0 violated, 0 could not be checked
```

An invariant holds when its statement returns no rows, so each one selects the
rows that violate it. See [Invariants](/docs/guides/invariants).

## When the other build cannot open the data directory

For somebody hardening a storage engine this is often the most useful thing the
tool will say, so it is a finding of its own rather than an environment that
would not start.

```
AF-DB-044: The build postgres:16-alpine could not open the data directory of
golden gv_20260927070738148927_rebase20, and the server said: 2026-09-27
07:08:10.280 UTC [1] FATAL:  database files are incompatible with server /
2026-09-27 07:08:10.280 UTC [1] DETAIL:  The data directory was initialized by
PostgreSQL version 17, which is not compatible with this version 16.15.
```

That is real output, from
`TestABuildThatCannotOpenTheOtherBuildsDataDirectoryIsAFinding` in
`engine/internal/db/docker/rebase_live_test.go`, which provokes the refusal at the
provider rather than through the command. Two different majors are the cheapest
way to produce a data directory a server will not open, and `af load compare`
refuses two majors before it builds anything, so the command can never show you
this particular sentence. The shape is what matters: a build of your own engine
with a catalog version, a block size or a page layout the other build does not
accept produces the same finding with its own detail line.

The server's own words are carried into the message, and the detail line is the
reason it is worth carrying: the verdict line is the same sentence for a catalog
version, a block size, a write ahead log format and a toast chunk size, and only
the detail beneath it says which. A container that stops without the server
refusing anything reports that instead, and the refusal is noticed when the
container stops rather than after the readiness wait, so it never arrives as a
timeout.

A major version mismatch between the two images is refused earlier still, before
either environment is built, because a build of another major cannot open the
golden at all and there is nothing to learn from starting.

## What this cannot tell you

Two runs against two databases are not a controlled experiment, and the report
says so on every run rather than leaving it implied. The seed makes the
transaction order and the parameter values the same. It does not make the
machine, the load on the host, or what autovacuum and the checkpointer chose to
do during each run the same.

Three things are worth knowing before you read a number as a property of your
build:

- A mix that writes changes the rows, the table size and the index depth it is
  measuring, so the two sides drift from the golden as soon as the first write
  commits.
- A branch is copy on write, so the first write to a page pays for copying it and
  a later write to the same page does not. A write heavy round measures the
  branching as well as the build, on whichever side reached that page first.
- The side that opens a data directory another build wrote is being measured
  partly on how well it reads another build's layout. That is a real property of
  your build and it is not the same property as its throughput on pages it laid
  out itself.

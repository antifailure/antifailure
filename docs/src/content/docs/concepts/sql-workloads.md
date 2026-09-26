---
title: SQL workloads
description: Clients on their own connections running transactions against the branch, so a database change is measured as a database change.
sidebar:
  order: 13
---

Every other kind of traffic in this product goes over HTTP. A load run sends a
weighted mix of requests, a scenario walks a journey, a workflow drives a
browser. All three reach the database only through the application, so the
number they report is the application's latency with the database somewhere
inside it.

That is the right measurement for an application change and the wrong one for a
database change. If you are altering an index, a lock, a storage parameter or a
query, you want transactions per second and the cost of one statement. The HTTP
path can answer that only through whatever the application happens to do on a
route you can reach.

A SQL workload opens connections to the branch and runs statements on them. N
clients, each on its own connection, each running whole transactions, with think
time between them and a seed that makes two runs execute the same sequence.

```yaml
load:
  sql:
    source: statement_statistics
    clients: 16
    duration: 2m
    think_time: 10ms
```

```
af load sql
```

## Where the statements come from

Two sources, and they answer different questions.

### Declared

A document in the repository holds the transactions. You write the statements
and say where their parameter values come from, so it is exact, and it is the
only way to rehearse a write path honestly: you are the only one who knows which
values are legal.

```yaml
load:
  sql:
    source: declared
    script: db/workload.yaml
    clients: 8
    duration: 60s
```

```yaml
sql_workload: storefront
description: the read path a storefront runs
transactions:
  - transaction: read one order
    weight: 8
    statements:
      - label: order by id
        sql: SELECT id, status, total FROM orders WHERE id = $1
        params:
          - query: SELECT id FROM orders
  - transaction: a merchant page
    weight: 2
    statements:
      - label: orders for a merchant
        sql: SELECT id, total FROM orders WHERE merchant_id = $1 ORDER BY created_at DESC LIMIT 20
        params:
          - int: {min: 1, max: 200}
      - label: the merchant
        sql: SELECT name FROM merchants WHERE id = $1
        params:
          - int: {min: 1, max: 200}
```

A transaction is an ordered list of statements that run inside one `BEGIN` and
`COMMIT`, because that is the unit a database's throughput is measured in and
because a lock held across two statements is the thing worth rehearsing. The
weights decide how often each one is picked, relative to the others.

A parameter sets exactly one of three things:

| Parameter | What it draws from |
| --------- | ------------------ |
| `int: {min, max}` | A whole number in the range, inclusive |
| `text: {values: [...]}` | One of the strings you list |
| `query: SELECT ...` | The values the query's first column returned when the run started |

`query` is the one that turns a benchmark into a rehearsal. An id drawn from the
table is an id that exists, so the statement reads a row rather than proving
that an empty result is fast. The query runs once when the run starts, on one
connection, and every client draws from the same pool, so the seed alone decides
which value each client picks. A query that returns no rows fails the run before
anything executes, because a statement bound to nothing measures nothing.

The statements are sent to the server unchanged and the values are bound by the
driver. There is no substitution language, so a value can never become syntax,
and the statement in the document is the statement you can paste into `psql`.

### Derived from `pg_stat_statements`

The other source reads the statistics on the branch and takes the statements
that actually ran, weighted by how often they ran. The mix is your own traffic
rather than a shape somebody invented, and the mean the statistics recorded for
each statement becomes a baseline.

```yaml
load:
  sql:
    source: statement_statistics
    max_statements: 20
    thresholds:
      mean_increase: 0.25
```

What it cannot do is recover the parameter values, because `pg_stat_statements`
stores the normalised text with every literal replaced. Two things follow, and
neither is hidden.

**A write is refused unless you ask for it.** A generated value in a `SET`
clause writes nonsense and a generated value in the `WHERE` clause of a `DELETE`
either deletes nothing or deletes the wrong row. Set `writes: true` when the
branch is disposable and you want them replayed anyway. Anything that is not a
query is refused under every setting.

**A read is replayed with a value of the right type and not the right value.**
The type is not guessed: the statement is prepared on the branch and the server
reports what it inferred, so a uuid primary key comes back as a uuid. The plan,
the locks, the buffer traffic and the storage engine are exercised faithfully,
and the result set size is not. A selective predicate filled this way may match
no rows, which is why every run reports the rows its statements touched. A run
of forty thousand statements that touched nothing measured the cost of finding
nothing, which is a real measurement of an index and is not a measurement of
your result sets.

Values can be generated for `smallint`, `integer`, `bigint`, `numeric`, `real`,
`double precision`, `text`, `character varying`, `name`, `boolean`, `uuid`,
`date` and the two timestamp types.

An integer is drawn from one to a million, a string is twelve lowercase
letters, and a timestamp falls in the five years after 2020. Any other type is
refused by name, so a `jsonb` parameter tells you it cannot be replayed rather
than being filled with an empty object you would read as a measurement of your
document workload.

Preparing every candidate has a second use worth as much as the first. A
statement that will not prepare does not parse against this branch's schema: a
column your change renamed, a function it dropped, a type it altered. Those
appear as refusals naming the server's own message, before a single transaction
runs.

## What a run measures

```
af load sql --concurrency 8 --duration 3s
```

```
Running a SQL workload

  declared statements, the read path a storefront runs.
  8 clients held 8 separate sessions, and the server had 7 of them inside a transaction at once (5 executing).
  231 transactions committed in 3.082s at 75.0 a second, 0 failed, 0 retried.
  Transaction p50 70.0ms, p95 341.0ms, p99 511.7ms. 281 statements touched 1193 rows.

  TRANSACTION      STATEMENT              RAN      P95  ROWS  ERRORS
  a merchant page  orders for a merchant   48  235.6ms   960       0
  a merchant page  the merchant            47  187.0ms    47       0
  read one order   order by id            186  121.2ms   186       0
```

Those are measurements rather than an illustration: one run of eight clients
against a Postgres 18 container on a busy laptop, which is why the latencies
are what they are. The statements are listed slowest first, because that is the
line somebody changing an index is looking for.

Throughput is counted from committed transactions alone. A rate that counted
failures would report a database refusing every transaction instantly as the
fastest database anybody ever measured.

A run that commits nothing reports no throughput and no latency, and exits
non-zero. Every threshold it carries passed over an empty measurement, which is
not the same as passing, so the run says so rather than leaving three zeros to
be read as a fast run:

```
Running a SQL workload

  declared statements.
  2 clients held 2 separate sessions, and the server had 0 of them inside a transaction at once (0 executing).
  0 transactions committed in 812ms at 0.0 a second, 40 failed, 0 retried.
  Transaction p50 0.0ms, p95 0.0ms, p99 0.0ms. 0 statements touched 0 rows.

  warn 40 attempts: SQLSTATE 22012

  fail This run committed nothing, so it measured neither a throughput nor a latency: all 40 transaction attempts failed, so there is neither a throughput nor a latency to report.
```

A deadlock and a serialization failure are retried up to three times, counted,
and reported on their own line. They are what a database says when two
transactions wanted the same rows, and the correct response is to run the
transaction again. A generator that did not retry would report every concurrent
run as broken. The error rate counts transactions that failed, over commits plus
failures, with retries in neither.

### The evidence that it was concurrent

N goroutines are not N database sessions, and N sessions are not N overlapping
ones. A pool, a lock, a client library that serialises or a think time longer
than the statement all produce a run that asked for eight clients and never had
two statements in the server at once.

So the claim is measured rather than made. A separate connection samples
`pg_stat_activity` while the run is going and reports three numbers: how many
distinct backends of this run it ever saw, the most it saw executing a statement
at one instant, and the most it saw holding a transaction open. A run whose peak
is one did not rehearse concurrency whatever its client count said, and you can
see that without taking anybody's word for it.

The sampling understates rather than overstates. Two statements that overlapped
entirely between two samples are not counted, which is the right direction for
the error to go: it can never manufacture the evidence it exists to provide. A
run whose watching connection could not open reports nothing rather than zero,
because "no overlap" and "nobody looked" are different answers.

### The contention it was under

A deadlock and a serialization failure end a transaction, so the client sees a
`SQLSTATE` and the run counts it. The commonest outcome of lock contention ends
nothing at all: a transaction queues behind another one, gets its lock, and
commits normally. Nothing is raised, nothing is retried, and a build that takes
a lock a little earlier or holds it a little longer moves the percentiles and
changes no other number in the result.

So the same watching connection also asks `pg_blocking_pids` which of this
run's backends are in a lock queue and which backends are in front of them.
The run reports how many times one of its clients started waiting, how many
backend milliseconds of waiting the samples found, and the pairs: the statement
that waited, the statement that blocked it, the kind of lock and the mode.

Both sides are named with the mix's own statement labels rather than with a
process id, because the run knows what each of its clients is executing. A
holder with no statement against it was idle in transaction, which is to say
holding its locks and doing nothing, and that is usually the finding. A holder
reported as another session on the database is exactly that: the waiter is
always one of this run's clients, because nobody else's wait is this run's
finding, and the holder may be anything else connected to the same database.

```
  6 times a client of this run queued for a lock, 3.6s of waiting between them across 3 backends.

    bump the counter / take the row
      waited on bump the counter / hold it
      queued on transactionid, ShareLock, 4 times, 3.6s
    bump the counter / take the row
      waited on another session on this database, idle in transaction
      queued on tuple on counters, ExclusiveLock, 2 times, 400ms
```

The same understatement applies and it is stated in the result rather than left
to be discovered. The wait queues are sampled every 200 milliseconds, so a wait
that began and ended between two samples is missing entirely and the counts are
floors rather than totals. Every lock type the server queues on is in scope,
including the transaction id waits a row conflict produces, tuple locks and
advisory locks, and each pair says which kind it was. Contention that never
becomes a wait is out of scope by definition: a lock granted with nobody ahead
of it cost nothing.

A run nobody watched reports nothing here rather than zero, and that matters
more than it does above. Zero lock waits is the most reassuring answer this
result can give, so an instrument that did not run must not be able to produce
it.

`af workload compare` differences `lock_waits` and `lock_wait_ms` between two
runs the way it differences deadlocks and retries, so "this build blocked more
than the last one" is a sentence the comparison can now make. It differences
the two numbers rather than the pairs, which stay in `af load sql -o json` and
in the MCP result.

## Thresholds

```yaml
load:
  sql:
    source: statement_statistics
    thresholds:
      mean_increase: 0.25
      error_rate: 0.01
```

`error_rate` is the share of transaction attempts that may fail. It is counted
from the run's own attempts, so it needs no baseline and works under both
sources.

`mean_increase` divides a transaction's measured mean by the mean
`pg_stat_statements` recorded for it. It needs a baseline, so it applies under
`statement_statistics` only, and the engine refuses it under `declared` where a
statement somebody wrote has never run and nothing could compare it with. A
threshold that was in force and measured nothing exits non-zero rather than
passing, for the same reason `af load run` refuses an inert `p95_increase`: a
check that ran nothing and reported green is a check everybody believes is
running.

## What this does not do

It does not bring up a second environment. Comparing two builds is
`af workload compare`, which differences two results that already exist.

It does not replace the differential oracle, which brings up a baseline
revision, branches one golden for both sides and diffs the responses and the
database contents. That is a much stronger claim than a throughput comparison.

It does not shell out to `pgbench`. The generator is Go, so it is present
wherever the engine is, its output is the same result shape every other workload
produces, and the parameter types the server reported are bound directly rather
than being written into a second script language and hoping the quoting
survived.

It measures the database this environment is running, which is a copy of
production's shape rather than production's hardware. Two runs against two
environments are not a controlled experiment: the seed makes the sequence the
same and does not make the machine, the cache or the neighbours the same. A
difference is a difference, and calling it a regression is a judgement you or a
threshold makes.

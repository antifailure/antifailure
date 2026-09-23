# added

The SQL workload could be measured on one build and compared against nothing.

`af load compare` brought a second environment up from the base revision,
branched one golden for both sides so they answered over identical rows, and
sent both the same traffic under the same seed in interleaved rounds. It could
send exactly one kind of traffic: the weighted HTTP mix. So the promise, the
same workload on two builds over the same data at the same concurrency, was
true of API traffic and false of the workload that reaches the database
directly, which is the half somebody changing an index, a lock, a storage
parameter or a storage engine is asking about. They could measure their own
build with `af load sql` and had nothing to measure it against.

`af load compare --sql` compares it. An explicit flag rather than an inference
from the manifest, because comparing the SQL workload whenever `load.sql`
happens to be declared would silently change what the command measures in every
repository that adds one, and change it inside a pipeline. It is refused with
AF-LOD-017 when there is no `load.sql` block, `--scale` is refused with it
because a SQL workload has no arrival rate to take a fraction of, and a SQL knob
typed without it is refused rather than read and ignored.

The unit of comparison is the transaction and the statement inside it, each
with p50, p95 and p99 on both sides, because a p95 alone is not a latency
distribution and a transaction whose p99 doubled while its p50 held is a lock
that no statement row reports. Throughput is committed transactions a second,
judged against the same `load.comparison.thresholds.throughput_drop`: reading
the achieved REQUEST rate for a workload that sends no requests would have
reported a declared limit as unmeasurable forever, which is a threshold in
force that evaluates nothing.

Every knob is settled once, on this build, and handed to both sides, including
the mix itself. A derived mix is read from `pg_stat_statements` on the database
it is about to run against, so a side left to build its own would weight the
statements by whatever that environment's own startup executed, and the
comparison would be differencing two workloads and calling the result a
regression.

What a SQL comparison cannot see is written for a SQL comparison rather than
copied from the HTTP one. A mix that writes changes the rows, the table size
and the index depth it is measuring; a branch is copy on write, so a write
heavy round measures the branching on whichever side reached the page first;
and autovacuum, the checkpointer and the background writer run on the server's
schedule rather than the comparison's.

The HTTP comparison's report is unchanged, to the character, and a test now
holds it that way.

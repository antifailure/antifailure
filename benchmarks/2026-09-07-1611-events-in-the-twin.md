# Events in the twin, before and after a second datastore

Run on 2026-09-07T16:11:41Z.

- Machine: darwin arm64, 8 cores, load averages: 25.71 23.48 22.89.
- Store: ClickHouse 25.3.14.14.
- Harness: `just benchmark`, which is `engine/internal/datastore/clickhouse/benchmark_test.go` in this repository. AF_BENCHMARK_EVENTS sets the row count.

| | Events in the environment's ClickHouse |
| --- | --- |
| Before | 0 |
| After | 1,000,000 |

The before figure is measured rather than asserted: it is a ClickHouse started from the image a manifest declares, with the tables its migrations would create and nothing in them, which is what an environment's second store was. Every chart in that twin drew nothing.

## What the refresh did

| Step | Time |
| --- | --- |
| Copy from production | 8.39s |
| Mask | 1m44.41s |
| Verify | 274ms |
| Whole refresh | 1m53.25s |

2,000,253 distinct values masked across 1 tables, and 5 columns read back by the verification scan. The golden holds 68.4 MiB on disk.

## Branch time

| Golden | Rows | Branch |
| --- | --- | --- |
| Small | 3 | 69ms |
| Large | 1,000,000 | 35ms |

Which of those two is the larger changes from run to run, and that is the result rather than an accident of this one: both are a few tens of milliseconds of metadata, so the noise of a loaded machine is bigger than the difference the row count makes.

A branch is a fresh database with the golden's partitions attached, and ClickHouse hardlinks the parts when the source and the destination are on one disk, so the two times above are about the same however far apart the row counts are. **The provider still declares CopyOnWrite false**, and that is deliberate: a multi disk storage policy copies the parts instead, and the provider cannot see the server's storage policy from the client. The measurement is published here rather than promised in a capability, because a capability is something the engine acts on and this one would be a promise about somebody else's hardware.

Loading production's own rows took 8.15s and is not part of any figure above; it is the fixture being built.

# Benchmarks

Every number this product says out loud is produced by a harness in this
repository, with the methodology beside it, so that anybody can run it against
their own stack and get their own number.

That constraint is the point rather than a tax. A figure whose harness is not
published is a figure nobody can disagree with, and buyers can tell. It also
means a number older than the code that produced it is withdrawn rather than
rounded: each run writes its own dated file here instead of editing one in
place, so a stale number is visibly stale.

```
just benchmark
```

## Database providers: first golden, and branch time

Two numbers per provider, because they are two different claims.

**First golden** is what it costs to make the first masked, verified copy of a
production sized database. It is paid once per refresh.

**Branch** is what it costs to give one environment its own database from that
golden. It is paid per environment, and whether it is flat or proportional to
the database is the single most important thing to know about a provider before
choosing one.

All of these are at 1.43 GB unless the row says otherwise, because a small
database is mostly fixed cost and dividing seconds by megabytes produces a rate
that is real for nothing.

| Provider | Copy on write | First golden, per GB | Branch, 8 MB | Branch, 1.43 GB | Runs |
| --- | --- | --- | --- | --- | --- |
| `pgurl` | no | 55 s to 169 s | 0.2 s | 25 s to 110 s | `2026-09-07-0941`, `2026-09-07-1002` |
| `aurora` | yes | not measured yet, L2.2 | | | |
| `rds` | no | not measured yet, L2.3 | | | |
| `cloudsql` | yes | not measured yet, L2.4 | | | |
| `azure-pg` | no | not measured yet, L2.5 | | | |
| `alloydb` | yes | not measured yet, L2.6 | | | |

A row with no number is a row that has not been measured. It is left visible on
purpose: a table that only listed the providers somebody had got around to
timing would read as a claim about the ones it omitted.

**A range rather than a figure, because that is what was measured.** The two
`pgurl` runs are the same commit against the same server twenty one minutes
apart, at load averages of 11.8 and 20.1 on an eight core laptop, and the second
one is three times slower than the first. Quoting the faster number would be
quoting the machine's mood. That spread is also the argument for shipping the
harness rather than the figure: on a server that is not a laptop running a
hundred containers, a customer's own number is the only one worth having, and
`just benchmark` is how they get it.

## How to read a branch number

A provider whose branches are copy on write should show the SAME branch time at
a hundred rows and at a terabyte, because a branch shares storage with its
golden and nothing is copied. A provider that copies files, which `pgurl` does,
shows a branch time proportional to the database. Both are legitimate. Only one
of them is what somebody with a terabyte should buy, and telling them which
before they run their own trial is why the slow numbers are published here too.

## Events in the twin

The number the second datastore exists for, and it was **zero**. An
environment held one golden and it was Postgres, so a manifest could declare a
ClickHouse and the environment started an empty container: for an analytics
product that is the twin holding the metadata and none of the data, and every
chart in it drew nothing.

| | Events in the environment's ClickHouse | Refresh | Branch | Runs |
| --- | --- | --- | --- | --- |
| Before | 0 | | | |
| After | 1,000,000 | 1m 24s to 1m 53s | 35 ms to 48 ms | `2026-09-07-1611`, `2026-09-07-1613` |

The before figure is measured rather than asserted: it is a ClickHouse started
from the image a manifest declares, with the tables its migrations would create
and nothing in them.

The refresh is paid once and the branch is paid per environment, the same split
the database providers have. A range rather than a figure for the same reason
the `pgurl` rows carry one: the two runs are the same commit against the same
server two minutes apart, at load averages of 25.7 and 18.3 on an eight core
laptop.

**The branch time does not move with the data.** Three rows and a million rows
branch in tens of milliseconds either way, and the two runs disagree about
which of them was faster: 69 ms against 35 ms in the first, 14 ms against 48 ms
in the second. A branch attaches the golden's partitions and ClickHouse
hardlinks them, so what is being timed is metadata. The provider still declares
copy on write FALSE, because a multi disk storage policy copies the parts
instead and nothing on the client side can see which one a server has. The
measurement is published here instead of promised in a capability.

`AF_BENCHMARK_EVENTS` sets the row count, so a customer's own number is one
command away.

## Cross store join keys

A different question again, and the only figure here that is not a time. An
environment holds more than one store and the same person is usually in several
of them, joined on an identifier that is in both. **Of the identifiers that
appear in more than one store, how many mask to the same value in all of them?**

| Stores | Candidate join keys | Verified identical | Share | Run |
| --- | --- | --- | --- | --- |
| Postgres and ClickHouse | 6 | 6 | 100% | `2026-09-07-1307` |

Anything below 100 percent is a bug rather than a slower number. A join key that
masks to two different values is a join that returns nothing, and a join key
masked in one store and copied in the other is also a leak. That is what makes
this worth quoting: it is a figure that can only be said out loud when it is
perfect, and the same run publishes what it was before the dialect boundary
existed, which was 3 of 6.

A report that found nothing to compare is not a pass either, and the harness
refuses one.

## The share of production a twin holds

The denominator, and the only figure here that is supposed to be tiny.
**Of the rows production holds, how many are in the twin, per table?**

| Environment | This twin | Production | Share | Run |
| --- | --- | --- | --- | --- |
| The analytics twin in `engine/internal/fidelity/testdata` | 184,000 | 6,445,324,600 | 0.0028% | `2026-09-07` |

Until this number existed the fidelity report said `reproduced` for that
environment and had nothing to compare it against, so a golden built from a
staging database with two hundred rows in it scored exactly like a full copy of
a production holding four billion. The same run publishes what the report said
before, recorded from the instrument at `d02fc3de` rather than recomputed: 9 of
10 components, 90 percent. With the profile it is 8 of 10, 80 percent, and the
component that moved says why.

A low share is not a failure of the twin. It is the fact somebody needs before
quoting a lock timing taken against it, which is why the same sentence carries
"a timing measured against this branch is a lower bound and not a prediction".

Run `af volume record` against your own database and the table is yours. It
reads no row: every figure comes from `pg_class`, `pg_stats` and the partition
catalogs, so a read only role on a replica is enough.

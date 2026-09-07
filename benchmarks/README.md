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

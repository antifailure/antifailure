# fixed

A twin holding two hundred rows scored as reproducing a production holding four
billion.

The fidelity report's database dimension said "12 tables over 184000 rows,
branched from gv_20260830120000_abcd1234", called that `reproduced`, and never
compared it against anything. It could not: nothing in the engine had ever been
told what production holds. A golden built from a staging database got the same
verdict and the same words as a full copy, and the report whose whole job is to
say what a twin does not reproduce was silent about the largest thing it can
fail to reproduce, which is the size of the data.

That undermines the strongest number the product has. A migration holding an
`ACCESS EXCLUSIVE` lock for five hundred milliseconds over two hundred rows is
a real measurement and a worthless prediction, and nothing said which of the
two it was.

`af volume record` now reads production's shape over a read only connection to
production or a replica, and writes a committed profile: row counts, table and
index sizes, partition counts and how much sits in the largest partition, and
the cardinality of every column anything joins on. It reads no row. Every
figure comes from `pg_class`, `pg_stats` and the partition catalogs, which is
what makes it safe to run against production itself and safe to commit beside
the manifest, where the check running on a pull request can read it.

Declare it under `database.volume.profile`. Then:

The database dimension states the fraction, per table. A branch holding less
than production is `substituted` with the share and with the sentence that
stops somebody quoting a timing taken against it. A branch with no profile is
`unmeasured`, and says so, rather than being reported as a reproduction nobody
checked.

The migration rehearsal states its extrapolation as an extrapolation. "This
rehearsal held AccessExclusiveLock on events for 512ms over 1,390 rows.
Production holds 4,200,000,000 rows in that table, 3,021,582 times as many. At
this rate the lock would be held for roughly 17.9 days." Never silently, and
never in place of the measurement.

A profile older than `database.volume.max_age`, thirty days by default, is
refused rather than quoted, the same way a stale golden is refused rather than
branched. A stale denominator is not a smaller number, it is an unknown one.

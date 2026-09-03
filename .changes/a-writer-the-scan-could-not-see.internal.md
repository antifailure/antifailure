# fixed

The scan that finds a table a screen reads and nothing but a fixture writes
looked only for `INSERT INTO`, so it could not see a singleton seeded by its
own migration and maintained by `UPDATE`.

`analytics_rollup_state` holds exactly one row. The migration that declares it
creates that row, and the rollup updates it on every maintenance pass, which is
a production writer on a real path. The scan reported zero writers and told the
reader to delete the feature or write a disclosure for a gap that does not
exist, which is the more dangerous direction for a gate: a false alarm gets
routed around, and then the true ones are ignored too.

Widened narrowly rather than generally. An `UPDATE` counts as a writer only
when the same table is also inserted into by a migration, because that is what
makes the row's existence guaranteed rather than hoped for. Counting every
`UPDATE` would have hidden the real defect this file exists to catch: a table
whose rows must be created per customer and which nothing inserts into outside
a fixture. Proved able to still fail by removing the rollup's `UPDATE` and
watching the scan go red on this table again.

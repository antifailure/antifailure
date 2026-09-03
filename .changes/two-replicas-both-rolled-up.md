# fixed

The analytics rollup rides the maintenance pass, every replica runs that pass,
and it runs once immediately on start. Production is configured for two
replicas, so on every deploy two rollups began within milliseconds of each
other and raced: the day recompute is a delete and an insert, and the second
insert failed on the primary key and took the whole maintenance pass down with
it. The visible symptom was not a wrong number, it was a dashboard that stopped
updating while a line went into a log. One rollup now holds a lock and the
others report that they skipped rather than doing nothing quietly.

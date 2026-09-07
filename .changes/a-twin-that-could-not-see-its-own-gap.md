# fixed

The fidelity report scored a twin holding no events at all as a faithful copy
of production.

Two of the report's own blind spots, each of which made the number confident
rather than wrong. A datastore the manifest declares `golden` was reported
`unmeasured`, which kept it out of the score in both directions, so a stack
shaped like an analytics product scored 100 percent with a masked Postgres and
an empty ClickHouse: every query path that mattered untested, every chart
blank, and the one instrument whose job is to say "this is not production"
saying nothing. Nothing has to read a ClickHouse to know that nothing built a
golden for it, so that store is `absent` now, counted, with the four missing
facts named: no golden, no attestation, no tables and no rows.

The second was the shape of the environment rather than its contents. Nothing
measured how many instances of each service were running, so a manifest asking
for three and a runtime starting one produced the same report as a correct one,
and everything that only appears above one instance stayed invisible: leader
election, a queue processed twice, a cache coherent with one instance and not
two. A `topology` dimension counts instances against the count each service
asked for. A service that names no count is `unmeasured` rather than
`reproduced`, because an omitted key is not a statement about how many
instances production runs, and a manifest that names no count anywhere excludes
the dimension whole with one line saying so, which leaves the score of every
manifest written before instance counts exactly where it was.

The number both changes exist for, on one manifest, measured rather than
described: the analytics twin scored **100 percent before and 90 after**, and
the same twin running one instance of each service that asked for several
scored **100 percent before and 70 after**. `just benchmark` writes both.

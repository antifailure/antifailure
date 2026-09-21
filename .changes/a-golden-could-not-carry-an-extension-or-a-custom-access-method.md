# added

A golden could not carry PostGIS, pgvector, TimescaleDB, or a table stored in
an access method that is not the heap.

The docker provider hardcoded `postgres:<version>-alpine`, which ships the
contrib modules and nothing else, and hardcoded
`shared_preload_libraries=pg_stat_statements`. An extension out of that set was
not a limitation anybody could work around, it was a refusal: the restore
stopped on the first object that needed one, and AF-DB-007 said so and offered
only "point the provider at a different service". A Postgres storage engine
developer asking whether their tables would survive a rehearsal was being told
no.

`database.image` names the image the provider runs Postgres from,
`database.extensions` is created in the golden before the source is copied into
it, and `database.preload_libraries` is ADDED to `shared_preload_libraries`
rather than replacing it, because dropping the statistics module leaves the
insights reading a permanently empty table on every environment. The list is
recorded on the golden image and read back when a branch starts, so a branch
carries what its golden was built with even after the manifest stops asking:
a library like timescaledb is loaded by the postmaster, and a server holding
its catalog entries without it does not start degraded, it does not start.

Two things a named image can silently get wrong are now refused instead. An
image declaring a volume over the data directory would publish a golden holding
no rows at all, because a golden is the container's filesystem committed and a
declared volume is not in it. An image on a different major than the manifest
declares would give every environment a Postgres the application does not run.
Both are checked against the running server before anything is loaded.

A table created `USING` an access method out of an extension now survives the
golden, every branch of it, `pg_dump` and `pg_restore`, and a subset's binary
`COPY`, with `pg_class.relam` intact on the far side. Masking is the one thing
that will not touch it, and it now says so at planning time rather than partway
through: it addresses a row by the primary key or by `ctid`, both of which are
guarantees of the heap, and against citus columnar on Postgres 17.2 each is
refused outright while the table accepts a primary key regardless. The refusal
names the table and the access method, and it is narrow enough that a column
nothing rewrites goes through untouched, which is what lets a golden hold such
a table at all.

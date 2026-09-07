# added

A Postgres this product could not copy from unless somebody had written a
provider for its vendor.

There were four database providers and each one was a provider for one product:
a Docker daemon, a Neon project, a Supabase project, a Database Lab Engine. A
self hosted cluster, a machine at Hetzner or Scaleway or OVH, an internal
server behind a bastion, or any managed Postgres whose vendor had no provider
here could not be a source at all. The manifest could name it and the engine
would refuse, which was the honest answer to a gap that should not have been
there: the copy itself is `pg_dump` and `pg_restore`, and neither of those
cares who runs the server.

`database.provider: pgurl` is that provider. It knows nothing about any vendor.
It takes the source connection string the manifest already names, and one more
variable holding the connection string of the server the goldens and the
branches live on, and it keeps a golden as a database on that server and a
branch as `CREATE DATABASE ... TEMPLATE`, which is a server side file copy.

Two things it declares rather than implies. Branching is NOT copy on write, so
branch time grows with the database, and the measured seconds per gigabyte are
published in `benchmarks/` beside the harness that produced them rather than
described. And the Postgres major it supports is the one the server actually
runs, read from the server, so a manifest asking for a version that server does
not have is refused instead of quietly building the golden on something else.

Nothing is dropped or written over on the strength of its name. Every database
this provider creates carries a marker in its comment, and a name collision
with a database it did not create is refused rather than adopted.

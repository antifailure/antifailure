# added

`database.provider` now accepts `cloudsql` and `azurepg`, so a manifest can name
the managed Postgres of Google Cloud and Azure rather than only of AWS.

Before this the runtimes, the emulators and the secret stores were symmetric
across the three clouds and the database was not: `aurora` was the only managed
provider, so "works on AWS, GCP and Azure" was true of three dimensions out of
four and the fourth was the one holding the data.

The two are not copies of the Aurora provider and each says where it stops.

`cloudsql` branches with a Cloud SQL FAST clone, which Google creates from an
Instant Snapshot and documents as metadata only, so branch work does not depend
on the size of the database. Cloud SQL also has a STANDARD clone whose duration
scales with that size, it selects between the two from the shape of the request,
and it returns the same operation either way. Three things force the slow one:
naming a zone, asking for a point in time, and disk properties that do not
match. The first is the trap, because Google documents that re-specifying even
the source's own zone falls back, so the request that looks most careful is the
one that stops being copy on write. The clone request type here has no field for
a zone or a point in time at all, so asking for the slow path does not compile.

`azurepg` branches with a point in time restore and declares copy on write
FALSE. Microsoft's statement that a snapshot restore does not depend on the size
of the data reads like the copy on write sentence, and the rest of the same
paragraph is why it is not one: log replay follows, and the overall recovery is
given as a few minutes up to a few hours. It also does the three things Azure
does not do for you, each of which is an outage or an exposure if a provider
assumes otherwise: firewall rules are not copied across a restore, so a branch
is a server nobody can reach; a restored server keeps the SOURCE's administrator
login, so without a reset every preview holds production's credential; and a
restore cannot cross between public and private access, which is refused before
provisioning rather than after.

Neither has been run against the real service. Both suites drive a fake control
plane with a real Postgres behind it, so the behaviours that are claims about
bytes are checked against bytes, and neither asserts `RealService`, so the
service owned verdicts report unproven rather than passed.

One question is recorded unanswered rather than guessed: Google's documentation
does not say whether a STOPPED Cloud SQL instance can be cloned, in either
direction. That decides whether a published golden can drop its compute, so the
default keeps goldens running, which costs more and is known to work.

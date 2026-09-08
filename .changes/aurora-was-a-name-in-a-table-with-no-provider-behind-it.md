# added

Amazon Aurora PostgreSQL is a database provider, in the enterprise edition.

A branch is an Aurora clone, requested copy on write and never any other way.
The clone shares the source cluster's storage volume and diverges a page at a
time, so branching moves no data and the work it does is the same at a hundred
rows and at a terabyte. Nothing connects to production at any point: the copy
is made by the storage layer from the cluster the manifest names, so no
credential for the production database is ever read or held.

The half of that claim which is not the clone is in the provider's
documentation rather than left for somebody to discover. A clone has no
instances and nobody can connect to storage, so a preview environment waits for
a writer instance, which is minutes. That wait is flat in the size of the
database too, and the provider declares its expected branch latency in minutes
rather than in the seconds the fast half would support.

Two things it does rather than the obvious nothing. A published golden's writer
instance is deleted once the masking and the verification have run, because a
cluster's volume is clonable with nothing attached to it, so a golden costs
storage and no compute. And every clone's master password is rotated before
anything connects, because a clone inherits the source's credential and a
provider that left it alone would hand production's database password to every
preview environment.

Three things it refuses rather than substitutes. A source cluster that is not
Aurora PostgreSQL is refused at startup instead of falling back to a snapshot
restore that would copy every byte. Reset and pooled connection strings are
declared unsupported instead of being destroy-and-recreate and a second copy of
the direct string. And the benchmark prints UNMEASURED for the wall clock of a
real Aurora clone, because no test here has an AWS account and a number from
somewhere else is not a measurement.

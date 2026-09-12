# added

`database.provider` now accepts `rds`, which branches an Amazon RDS for
PostgreSQL instance. The comparison table in `benchmarks/` had a row for RDS
and no code behind the name, while most Postgres on AWS runs on plain RDS
rather than Aurora.

A branch is `RestoreDBInstanceFromDBSnapshot`. RDS provisions an instance and
hydrates a new volume from the snapshot, and the volume is every byte of the
database, so the provider declares `CopyOnWrite: false` and its branch time is
minutes and grows with the data. It refuses an Aurora cluster and names the
`aurora` provider instead.

A restore inherits production's whole role catalog, not just its master
password, so the provider closes every inherited login and ends its sessions
before a golden is masked, again before it is published, and again on every
branch. It rotates the master password to a key derived per instance, turns
IAM database authentication off, and places the instance in the source's own
subnet group and security groups with no public address. Connections default
to `verify-full` against AWS's published RDS root bundles. Every resource is
bound to the source instance's ARN and carries an HMAC receipt under the branch
key, so a second source in the same account is never adopted or deleted, and a
branch interrupted before its logins were closed is never handed out.

Requests follow AWS's published RDS service model: tags go out as
`Tags.Tag.N`, which is what botocore's query serializer sends, and an
instance's subnet group is read as the structure AWS returns. Every request
goes through the air gap guard, and the provider is refused under
`AF_AIR_GAPPED`.

No AWS account was available, so none of this has run against AWS. The suite
runs every line of the provider against a fake RDS control plane over a real
Postgres, the benchmark prints `UNMEASURED` for every wall clock cell, and the
copy on write verdict is recorded as unproven, because over the fake a restore
is a local `CREATE DATABASE ... TEMPLATE` and a pass there would say nothing
about RDS.

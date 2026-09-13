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

After rotating the master password, the provider waits until RDS no longer
lists it as pending and the new password actually opens the database, because
RDS reports an instance available before a password change is in force. The
attestation, the masking rules hash and the provenance are stored in tags as
base64, because a tag value refuses the braces, quotes and commas a document
contains.

Part of this has run against real AWS. One run in `us-east-1` reached a masked,
verified candidate over `verify-full` against RDS's own certificate, with a
snapshot in 1 minute 11 seconds and a restore in 5 minutes 4 seconds at 20 GB,
and found the two defects above and a third in instance role credentials, all
fixed. It stopped before a golden was published, so publishing, branching,
isolation and destroy are proved against a fake RDS control plane over a real
Postgres only. The benchmark prints `UNMEASURED` for every wall clock cell, and
the copy on write verdict is recorded as unproven, because one restore at one
size cannot decide whether branch time grows with the data.

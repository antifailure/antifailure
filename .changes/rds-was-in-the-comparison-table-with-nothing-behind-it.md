# added

Enterprise Postgres lives in RDS, the plan's comparison table has a row for it,
and there was no code behind the name. `ee/engine/db/rds` is that provider.

A branch is `RestoreDBInstanceFromDBSnapshot`. RDS provisions an instance and
hydrates a new volume from a snapshot, and the volume is every byte of the
database, so the provider declares `CopyOnWrite: false` and goes into the table
with minutes rather than seconds.

That number is published unflattered on purpose. A buyer on plain RDS who finds
out from their own failed trial that branching takes minutes is a lost deal; one
who learned it from our own table has a reason to believe the rest of it. The
provider refuses to be pointed at an Aurora cluster rather than quietly becoming
the slow half of a fast provider, and says which provider to use instead. The
Aurora provider makes the mirror refusal.

The declaration is now falsifiable rather than decorative, because
`CopyOnWrite_BranchTimeMatchesTheDeclaration` landed first. The suite branches a
small golden and one half a gibibyte larger and requires the extra time to
exceed what the machine's noise can account for, which is the false side of a
two sided assertion. A self test in the package declares copy on write TRUE over
this same, honestly copying, provider and requires the suite to refuse it: before
that behaviour existed, that child would have passed.

What that self test does NOT do, said here because it passes and a pass is
where an unexamined claim survives. The bytes it times are a local
`CREATE DATABASE ... TEMPLATE`, so the false side of the assertion holds for
the simulator whatever AWS would have done. The green covers this provider's
logic and the instrument's ability to refuse the opposite declaration. It
covers nothing about how long RDS takes, and the provider's own package comment
says so where a reader of the code will meet it.

The model, which is the mechanism rather than an implementation detail. A golden
is a manual DB SNAPSHOT, built by snapshotting the source instance, restoring
that into a candidate, masking it, verifying it, and only then snapshotting the
candidate. The candidate and the intermediate snapshot are deleted, so a
published golden costs snapshot storage and no compute, which is the one place
this mechanism is cheaper than the fast one. Four control plane operations
before anything is masked, where a cluster clone is one.

Reset and Subsetting are declared false and the methods refuse. RDS has no
restore in place and no rewind, so the only way back to a golden is to delete
the instance and restore again, which is what the capability is defined against;
and a candidate here is a restore rather than an empty instance, so there is
nothing to load a slice into. A manifest asking for a subset is refused naming
the provider rather than copying everything and calling it a subset.

A restored instance inherits the snapshot's master credential, which is
production's. The provider rotates each one to an HMAC of a manifest named
branch key and the instance identifier, immediately after the restore and before
anything connects, so a preview environment never holds production's database
password and no password is written down anywhere.

Signing is `ee/engine/cloudauth` and there is no second copy of it. AWS's SDK is
not here for the reason it is not there: a hundred packages in a binary that
holds credentials, to save a form encoded POST and an XML unmarshal.

WHAT THE TESTS PROVE AND WHAT THEY DO NOT. A fake RDS control plane answers on
localhost over a real Postgres, so every line of the provider runs and the
behaviours that are claims about bytes are checked against bytes. That proves
the provider's logic, its request shapes, that each request is signed correctly
for the right region and service, and its error mapping. It does NOT prove that
AWS accepts those requests, and it CANNOT produce a wall clock number for a real
RDS snapshot and restore, because the thing being timed would be a local
`CREATE DATABASE ... TEMPLATE`.

So the two numbers the wave asks every provider for, time to the first golden
per 100 GB and time to branch at a small database and a large one, are PUBLISHED
AS A REFUSAL rather than as an estimate. The benchmark refuses outright if it is
pointed at a real endpoint it has no account for. What it does measure is the
half that is a property of this code: the control plane work per branch is
identical at twenty gibibytes and at a tebibyte, and the data work is not, and
the second sentence is the one the table is about.

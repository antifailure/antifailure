# added

A database provider for Xata, the one managed Postgres vendor whose branches
are copy on write snapshots rather than forks restored from a backup.

`database.provider: xata` with `database.project: <organization>/<project>`
makes a golden a branch of the project's root, masked and verified in place and
published by a rename, and makes every environment a branch of that golden. The
attestation travels inside the golden, so a branch carries what was scanned.

The provider declares copy on write, because Xata's own branching page says so,
and the copy on write ledger records that declaration as unproven. The
conformance suite runs in full against a fake Xata control plane over a real
Postgres, built from Xata's published API document, and a fake over one Postgres
can only copy. The command that settles it against a real account is on the
provider's page.

Refreshing now refuses a manifest asking for a Postgres major the project does
not run, with `AF-DB-003`, before anything is loaded. A refusal from Xata keeps
Xata's own code and message. The page for this provider names every call it
makes, the three API key scopes it needs, and the capabilities it declares
false, with the reason for each.

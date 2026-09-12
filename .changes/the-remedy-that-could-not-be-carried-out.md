# added

A provider for Xata, and the refusal that told a Heroku user to run a statement
Heroku has no role for.

Thirteen managed Postgres vendors were read and placed, each against its own
published documentation with the page and the date recorded. Twelve of them call
their copying feature a fork and restore a backup, where the clock grows with the
data. One, Xata, branches by taking a copy on write storage snapshot, and it now
has a provider: goldens and environments are both branches that share storage
with their parent, the attestation travels inside the golden itself, and
publishing is a rename so a half built golden is never visible as one.

That provider declares `CopyOnWrite: true` because it is true, and nothing here
has confirmed it. The conformance behaviour that can falsify the claim needs a
harness able to exhibit copy on write, and a fake control plane over one local
Postgres can only branch by copying. The declaration, the measurement that was
not made, and the one command that would make it are all on the provider's page
rather than left to be discovered.

The other half is a refusal that could not be carried out. `AF-DB-035` ends "Run:
ALTER ROLE {role} CREATEDB", which is right on a server you administer and
impossible on most managed Postgres: Heroku provisions one database per add on
and hands out no superuser, and a Tiger Cloud service holds exactly one database
and says so in its own troubleshooting page. `AF-DB-037` now names the vendor,
quotes where the verdict was read, and gives the remedy that works, which is that
the vendor stays as `database.source_url_env`, needing read access only, and the
host server is a Postgres you administer. `af start` names the vendor on its
database rung and blocks there too, without connecting to anything. A vendor
whose documentation did not answer is named and not refused, because a guess
printed as a citation is the same defect in the other direction.

One of the thirteen turned out to have withdrawn its managed Postgres entirely.
Its row is kept rather than deleted, because a vendor missing from a list of
thirteen reads as a vendor nobody looked at.

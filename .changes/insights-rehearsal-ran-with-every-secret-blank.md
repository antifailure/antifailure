# fixed

`af insights` blamed a project's migrations for a failure the rehearsal caused
itself, because the migration container received every declared secret as an
empty string.

A project whose migrations are Ruby, Python or JavaScript is rehearsed by
running the service's own migrate command in the service's image. That
container was given the manifest's literal values and nothing else, so a
variable read from a secret source arrived as `NAME=` with nothing after it,
while `af up` had started the same service with every value. A Rails app with no
`SECRET_KEY_BASE` refuses to boot, so the rehearsal failed before a single
migration ran, and the report said AF-DB-030, "Migrations failed on the branch",
and told the reader to fix the migration.

The rehearsal now resolves the service's secrets through the same chain and the
same per service lookup `af up` uses, so a value scoped to another service is
not handed to it.

The same container also decided which database a rehearsal reached by the order
of its variables. The branch's address went first and the service's variables
after it, and Docker keeps the last of two entries with one name, so a service
declaring `DATABASE_URL` replaced the branch's address with its own. The
environment is now built with exactly one entry per name and the branch's
address written last, so a rehearsal only ever reaches the throwaway branch,
whatever the service declares.

A failed rehearsal's quote of the tool also carried a stray character in front
of the tool's first word, such as `1rehearsal sees`, because Docker's frame
header was filtered by whether its bytes printed rather than read as a header.
The log is demultiplexed properly now, and a message longer than one read
arrives whole.

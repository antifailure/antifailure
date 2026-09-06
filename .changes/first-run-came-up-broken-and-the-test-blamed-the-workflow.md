# fixed

`af init` run fresh against the project's own showcase example, `examples/next-app`
with its manifest deleted, wrote a service block with no `migrate` key at all.
The Dockerfile installs `postgresql-client` specifically because the manifest's
migrate command needs it, and `migrations/0001_init.sql` creates the tables the
whole application is built on. Nothing recognised that shape, so `af up`
started the service, its health check ran `SELECT 1` against an empty database
and passed, and the page itself answered every request with `relation
"customers" does not exist`. A directory of SQL files with a name that looked
like a migration directory was already detected, at low confidence, and then
thrown away: it became a finding no candidate ever read, reported to nobody,
and the manifest that came out the other end looked exactly like one detection
had checked and approved.

A directory whose files are numbered now gets a real migrate command: the
plain `psql $DATABASE_URL -v ON_ERROR_STOP=1 -f ...` invocation the numbering
itself implies, one `-f` flag per file in name order, owned by whichever
service builds from the same directory. A directory whose files carry no
number has no ordering evidence at all, and guessing one would be worse than
the blank it replaces, since a migration applied in the wrong order against a
real database can corrupt it in a way an empty key never could; that case is
still surfaced as a question rather than a silent guess, exactly as before.

The second failure was worse than the first, because it hid the reproduction
of it. `af test` against the broken environment never mentioned the server
error. Three different rewrites of the workflow's expectation, including the
page's own literal empty state string, all came back `unverified`, with a note
blaming the wording, because judgement ran only on the page's rendered text
and the response status the runner already had in hand never reached it. A
page that answers with a real HTTP error is not an unread expectation, it is a
failed one. The runner now carries the status of the document it navigated to
alongside everything else it already reads, and a workflow whose page answered
400 or above is reported `failed`, naming the status and the page, before any
judgement about the expectation's own words runs at all.

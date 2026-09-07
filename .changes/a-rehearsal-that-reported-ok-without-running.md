# fixed

`af insights` printed `ok  nothing to report` and exited 0 when the migration
rehearsal was asked for and did not run.

The body of the report was honest: the reason sat in `Missing`, worded plainly,
"the migrations were not rehearsed, because no fresh branch was available to
rehearse them against". The headline above it and the exit code below it were
not, and CI reads the exit code. A branch whose rehearsal could not start was
indistinguishable from a branch whose rehearsal found nothing.

There are three outcomes here and there were two codes for them. A run that
found nothing exits 0. A run that proved a break exits 5. A check that was
asked for and could not run now exits 7, `ExitVerification`, as AF-DB-033, and
prints no `ok` line at all. `--no-rehearsal` and `insights.migration_rehearsal:
false` still exit 0, because declining a check is a decision and not a blocked
one; the two sides of that distinction have a test each.

Two more doors onto the same room were open and are now shut. `af insights -o
json` returned as soon as it had written the document, so every exit below that
line was unreachable and adding `-o json` reported success on a migration that
had failed. And a rehearsal that ran against a repository where no migration
tool was recognised timed nothing, linted nothing, produced no findings, and
was therefore a pass, both at the command and to an agent through
`rehearse_migration_safety`, whose verdict function had no test at all.

Migration discovery also stopped at one directory below the repository root,
and looked for `supabase/config.toml`, `alembic.ini`, `manage.py` and
`knexfile.js` only at the root itself. Against ten layouts taken from real
repositories it found one. It now searches to five levels for every marker and
finds all ten, including `services/api/src/main/resources/db/migration` and
this repository's own `web/packages/db/migrations`. Two markers stop being
distinctive at depth, so each gained a guard: a `manage.py` is read for the
word django rather than trusted by name, and a `drizzle` directory has to hold
`meta/_journal.json` before the search settles on it. A dependency's own
migrations under `node_modules` are still never this repository's.

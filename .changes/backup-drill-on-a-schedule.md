# added

The disaster recovery drill runs weekly against a scratch database in
`.github/workflows/drill.yml` and fails the run when the restore is not one.
`af-control-plane-backup` had no caller anywhere in this repository until now:
the suite exercised the library, and nothing exercised the command an operator
is told to type. `just drill` runs the same thing on a laptop, and the workflow
invokes that recipe rather than a second copy of it.

The drill reports the recovery time it measured, writes it to a JSON report and
holds it against a budget, exiting 4 when the restore is sound and slow so that
a busy runner never reads as a broken backup.

# changed

The drill now finishes with the behavioural check the module's header always
described: the restored database is asked, through the unprivileged
application role, to read another tenant's rows in every tenant table, and it
has to refuse. That check lived only in the test suite, so the drill an
operator runs compared catalogue text against catalogue text and stopped. It
is the only check in the module that is absolute rather than relative to the
manifest, and it is the only one that notices a source database whose
isolation was already broken before the backup was taken. A drill that cannot
attempt it now fails rather than passing quietly.

The operations page records a recovery point objective: five minutes inside the
region, a fourteen day recovery window, and up to an hour with geo-redundant
backup, which is off by default and can only be turned on when the server is
created.

# fixed

The SOC 2 and HIPAA packs were proven by a run nobody could repeat.

`docs/plan/STATUS.md` row 13.12 said `proven` and described a run against a
real control plane. Not one test under `ee/engine/compliance` opened a database
connection, the word `compliance` appeared in no workflow and in no justfile
recipe, and the doc comment on the chain verifier stated in the present tense
that a test ran a chain written by the control plane's own code through it.
None of that existed. A run that happened once on somebody's laptop and left no
re-runnable artifact is the definition that page itself gives for the
difference between proven and written, and the row was on the wrong side of its
own line.

There is now a suite that creates a database of its own on a real Postgres,
applies every migration with the control plane's own runner, appends every
audit entry through `appendAudit`, and runs both packs through the same command
the enterprise binary contributes. It publishes the two reports, their JSON
forms and a note saying what the run did not check. The `enterprise` job runs
it on every pull request against the Postgres that job already had, refuses to
pass when the documents are absent, and keeps them as an artifact. `just
compliance` is the same run on a laptop.

Four negative arms, each pointed at the case it exists to catch: an entry
altered with a privileged connection, an entry deleted with one, a table
carrying tenant data with row level security switched off, and an application
role granted UPDATE on the audit log. A check that cannot say no cannot say yes
either.

The number of tables carrying an `org_id` is not asserted anywhere. It is a
property of the schema on the day it runs, which is exactly how the row came to
claim seventeen of them after three more migrations had landed. What is
asserted is that none of them has row level security disabled.

Installing the control plane's dependencies moved above the Go tests in the
enterprise job and in `just test-ee`. The audit chain drift guard had been
sitting behind that line since it was written, so it skipped in the one job on
this repository with a Postgres to run it against.

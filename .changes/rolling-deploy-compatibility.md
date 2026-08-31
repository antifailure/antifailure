# added

`af insights` now runs the previous release against the migrated branch and
reports whether its workflows still pass, which is the invariant a rolling
deploy depends on and the one the rehearsal never checked. A failure is
confirmed against a second branch of the same golden with the migrations left
off, so a workflow that fails on that release either way is reported as
unverified rather than blamed on the change, and anything the check itself
could not do is blocked rather than failed. The finding names the object when
the previous release's own output supports it: "the previous release still
reads `customers.email`, and this migration dropped `customers.email`", with
the Postgres error and the statement beside it. It exits `AF-DB-031` on a
proven break.

Configured by `insights.rolling_compatibility`. `when` defaults to `risky`,
which runs it only when the migrations take something away, and `against`
defaults to the merge base with the base branch.

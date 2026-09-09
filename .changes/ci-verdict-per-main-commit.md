# fixed

Two commits reached main on 2026-09-08 with no CI verdict at all, and nothing
said so. Four pull requests merged inside four minutes, and the API records the
rest: the run on `09078be0` was in progress and survived; the runs on
`39a771ff` and `f5e2ef68` were cancelled 36 and 33 seconds after they were
created, one second after the next run was created in each case, and both carry
an empty job list. Not one check ran on either commit.

`cancel-in-progress: false` protects a run that has STARTED and nothing else.
GitHub holds one PENDING run per concurrency group, so each merge took the place
of the one queued behind the run in progress. The comment in `ci.yml` opened by
saying a run on main is never cancelled and closed, thirty lines later, by
saying a pending one still is and that fixing it would need a merge queue. Both
halves were in the file, the first is what people read, and the second was
wrong: `ci.yml` now keys its concurrency group on the commit for a push to main,
which gives each one a group of one, so no main run can supersede another.
`security.yml` had already used the same technique for its daily scan three days
earlier and nobody carried it back. The cost is runner minutes, because a burst
of six merges now runs six full CI runs where two ran before, and past the
account's concurrency limit they queue. A queue is a delay. A cancelled pending
run was a permanent hole, and a hole cannot be re-run into existence once the
next merge has landed on top of it.

The configuration change removes the cause this had. It cannot remove the
condition, because a run can still be cancelled by hand, a job can still hit its
own `timeout-minutes`, and the expression can be edited back. So
`tools/mainverdict` reads what CI concluded on each of main's recent commits and
refuses a branch carrying one that was never judged, running in
`.github/workflows/ci-watch.yml` on every completed CI run on main and daily,
and leaving one issue open until main is whole again. It passes `failure` along
with `success`, because a red main is already the loudest thing in the
repository; what it catches is the silent case, where `cancelled` and `skipped`
render in a list exactly as a pass does. It has a third answer for the case
where the run history it read does not reach the commits it was asked about,
because reporting a branch clean on having read the top of it is the defect this
repository keeps finding in its own instruments.

`tools/cigate` already refused a cancelled run, and still does. Its explanation
of what `cancelled` means on main was written against the old setting and now
says what is true.

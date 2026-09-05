# fixed

The runbook for a stale vulnerability scan named one cause, and the case that
happened was the other one.

It said a scan goes stale because GitHub disables a scheduled workflow in a
repository nobody has touched for sixty days, and sent you to
`gh workflow enable`. On 2026-09-05 the scan went stale for a different reason
entirely: the schedule fired 21 seconds after a merge to `main` and a
concurrency group cancelled it 17 seconds later, before it reached a single job.
`gh workflow list --all` said `active`, which is where that page runs out of
advice.

The two look identical from outside, because the watchdog reports the same
sentence for both: the newest completed scheduled run is too old. The page now
opens with the query that tells them apart, a gap with no rows in it against a
row that says `cancelled`, and gives the remedy for each. For a cancellation the
remedy is re-running that run rather than starting a new one, because only a run
of the `schedule` event counts and `gh workflow run` produces a
`workflow_dispatch` one the watchdog deliberately ignores.

It also says to check whether a fix is already in the tree and simply was not
live yet. `security.yml` gives the schedule its own concurrency group precisely
so activity on `main` cannot reach it, and that change had landed 83 minutes
after the run that was cancelled. A scheduled run cancelled after it is a
regression; one cancelled before it is not, and the difference is two commit
timestamps rather than a judgement.

The worked example is dated and kept in its failed state, with a note that the
run it names now reads `completed/success` because following this page is what
somebody did to it. A runbook whose example shows the healthy state teaches
nothing about the sick one.

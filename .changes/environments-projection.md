# fixed

Environments now appear in the hosted console. Nothing in the control plane
had ever inserted a row into the `environments` table: the projection that
events feed was an UPDATE, and the only INSERTs anywhere were the test
harness, the staging seeder and the backup drill. So the console's environment
list was empty for every real customer no matter what they ran, the expiry it
shows was permanently blank, and anything computed from those rows was
arithmetic over an empty table.

The row is now created the first time the control plane hears about the
environment, from whichever lifecycle event arrives first, and updated by the
ones that follow. Every `environment.*` event the engine sends carries the
repository, the branch, the pull request number when there is one, and the
lifetime the manifest declared in `runtime.ttl`, so the console shows a real
expiry and a real pull request link rather than empty fields.

Usage is measured from when the environment came up rather than from when the
control plane heard about it. Every event that a run emits carries the instant
the work began, so an environment whose creating event was lost still bills
from before its build rather than after it.

The per-day spend cap and the cost attribution read these rows, so both
answered zero for every customer and a cap that computes zero can never trip.
They now compute over real environments.

An engine older than this release can still advance an environment but cannot
create one, because it does not say which repository it is running against.
Rather than dropping such an event, the control plane stores it, counts it as
`af_ingest_events_total{outcome="unprojected"}`, and returns a note on the
event saying what is missing.

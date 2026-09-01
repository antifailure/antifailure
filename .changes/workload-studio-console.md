# added

A `/workloads` area in the console for the three kinds of workload the engine
actually has. Observed traffic compiled from a production window, deterministic
scenarios somebody wrote and pinned, and exploratory runs an agent drove from a
seed are shown as three different things with three different payloads, because
what each one proves is different: a pinned scenario reproduces exactly, an
observed shape reproduces as a shape, and a seeded exploration reproduces only
against the same seed and the same build. The screen says which, next to the
numbers, rather than leaving a reader to assume they are equally strong.

A run's outcome keeps `blocked`, `unverified` and `errored` as their own
values, and none of them is ever drawn as a pass. Thresholds are three-valued
for the same reason: one that nothing evaluated has not held. When a run's
recorded verdict disagrees with its own thresholds, a pass over something that
broke or over something never evaluated, the console says so above the table
instead of presenting the contradiction quietly.

A measurement the control plane did not record renders as absent rather than as
zero, everywhere. The reproducible command is printed only when it was recorded
at dispatch and is never rebuilt from the form, because a command the console
assembles can drift from the one that ran.

The area calls `workloads.*` on the control plane. Those routes land separately;
until they do this ships the screen and not the data behind it.

# added

A hosted workload run now tells the control plane what happened to it. Before
this the engine emitted none of `workload.started`, `workload.finished` or
`workload.cancelled` and claimed nothing, so a run started from the console was
dispatched, appeared, and was recorded as *abandoned* at its deadline whatever
it actually did. The control plane's side of that wire was already built and
tested; it was fed by nobody.

`af workload run` claims the run waiting for its environment, says when it
started, says once a minute that it is still going, and reports what it
measured. The report is the same document `--result` writes, so the artifact a
job uploads and the numbers a console draws are the same bytes, and a report
that cannot be delivered is spooled to disk rather than dropped.

The engine asks for the run rather than being told which it is, because a
`workflow_dispatch` carries only the inputs the workflow declares, GitHub reads
that declaration from your default branch, and it refuses an undeclared input
with a 422 that is indistinguishable from the file being missing. `--run-id`
still names a run by hand, and claims nothing, so reproducing a hosted run on a
laptop cannot take the next queued run away from CI.

A cancel pressed in the console now reaches a run that is already going, and
stops it. So does a lease taken by another engine, which is what happens after
a run has gone quiet long enough for somebody else to pick it up.

# fixed

`examples/github-workflow.yml` never set `AF_CONTROL_PLANE_TOKEN`, so a
repository following the documented workflow sent no engine events at all. Not
the workload ones, and not the environment lifecycle either: the console's
environment list was fed by nothing on every such repository. The example sets
it and the GitHub guide says what happens without it.

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
stops it. It arrives on the heartbeat the engine is already making rather than
on a poll of its own, so there is no command client in the engine at all. So does a lease taken by another engine, which is what happens after
a run has gone quiet long enough for somebody else to pick it up. In that one
case the engine stops and then says nothing more, because the control plane's
terminal statement is gated on the run's state rather than on who holds it: a
report from an engine that has lost the run would end it for whoever now has
it, and their measurements would arrive against a finished row and be refused.
The result document is still written and uploaded, so nothing is lost on the
machine that did the work.

A test reads the control plane's report decoder and checks that every field it
reaches for inside the run's aggregate is a field the engine's own struct tags
emit. Two green suites had never put a real message on the wire between them,
and running one against the other found that the decoder was reading the shape
of the engine's internal load result rather than the result document: it would
have recorded a run that sent twelve hundred requests as having sent none, with
every percentile null and every route beside it decoding perfectly.

# fixed

A hosted run that did not pass now says why on the line a person reads first.
A load run that breached a threshold reported a `fail` verdict and an empty
detail, so the reason lived only in the threshold rows and the line a console
leads with was blank. It now names the breach in the units the manifest declares
it in, and says how many more there were. The scenario path beside it had always
named its failing scenario, which is why nobody saw this: both read correctly on
their own.

Two siblings had the same gap. An exploration that missed a goal said nothing,
on the reasoning that an exploration can never fail so the blank never lands
under a red verdict; it lands under `unverified`, which is not a pass, and the
goal that was missed is the one thing worth saying. And a failing scenario or
workflow whose own reason was empty rendered as a name, a colon and nothing,
which is a worse absence than an empty one because it reads as a sentence that
was cut off. Both closed.

The control plane answers every event it stored and could not apply with a
sentence saying why, and the engine threw all of them away. The wire type had no
field for the note at all, so it was dropped by the decoder, and the only caller
of `Send` discarded the whole result, so the rejections were dropped too. That
is the one channel that explains why a run reported and the console still shows
nothing, written at one end and discarded at the other. Those sentences now
reach the job log, bounded, with duplicates left out because a duplicate is the
idempotency key working rather than a fault.


`examples/github-workflow.yml` never set `AF_CONTROL_PLANE_TOKEN`, so a
repository following the documented workflow sent no engine events at all. Not
the workload ones, and not the environment lifecycle either: the console's
environment list was fed by nothing on every such repository. The example sets
it and the GitHub guide says what happens without it.

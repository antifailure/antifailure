# added

A Load area in the console: the workloads you can run against a twin, every
version of each, every run, and what each run actually measured.

Four kinds, kept apart because they measure materially different things. An
observed mix has no order, a scenario has no browser, a browser workflow has no
request rate, and an exploration has no pass. Each says what a result from it
is worth: a scenario replays request for request at the same seed, and an
observed mix replays only as a shape.

A workload names things rather than containing them. Everything runnable is
declared in your own manifest and selected by name, the same way the command
line selects one, so a version is a selection plus the knobs its command
actually declares. That is a security property and not a simplification: a
scenario is checked against your safe route list before anything is sent, and a
control plane able to hand an engine an arbitrary journey could send traffic
you never allowed.

Every knob lives in the version rather than on the run, so changing the scale
writes a new version and comparing scale 1 against scale 4 is comparing two
versions. Versions are immutable and every run records which one it used, which
is what makes a run from three weeks ago readable. A form offers a knob only
when the kind's command has a flag for it, and says why underneath when it does
not.

State and verdict are two separate answers and neither implies the other. A run
can do all its work cleanly and fail every threshold in it. `abandoned` is
drawn apart from a failure and said in words: the deadline passed with no
engine reporting, which is a defect in the plumbing rather than in the change,
and what is missing is the report rather than necessarily the work.

There are five verdicts, not four. `flaky` is one of them, and a run that came
back flaky has found something rather than nothing.

Results are the engine's own rather than a summary of them. Latency is the five
percentiles it measures and one it did not record is absent rather than drawn
at zero. Errors are broken out by reason, because a thousand timeouts and a
thousand refused connections are the same number and completely different
problems. Routes are compared against production's own p95 and one with no
baseline says so rather than reporting no change. The achieved rate is shown
against the rate that was asked for, and a run that fell more than a tenth
short says outright that the application did not keep up, because every latency
figure under it was then measured behind a queue. A browser run shows five
outcome counts, and a run that drove workflows and passed, failed and flaked
none of them says that nothing was checked.

Evidence says whether it can still be read. A trace written to a path on a CI
runner that no longer exists is shown as the record it is, never as a link.

Stopping a run is a durable command a runtime has to confirm, and the console
shows where that request got to. A stop nothing acknowledged before its
deadline says it was never confirmed rather than showing a cancelled run that
may still be going.

Promoting an exploration compiles what an agent found into a workflow for your
manifest, and returns two things in full: everything the compilation could not
carry, one sentence each, and the block you have to paste into
`antifailure.yaml` before anything can run it. A control plane that cannot put
a file in your repository says so.

The area calls `workloads.*` on the control plane. Those routes land
separately; until they do this ships the screen and not the data behind it.

# added

A Load area in the console: the sources you can send at a twin, every run of
them, and what each run actually measured.

The two sources are kept apart because they are different things. An observed
mix is compiled from what production served, so it replays as a shape, seeded,
but not request for request. A deterministic scenario is written down and
pinned, so it replays exactly. The screen says which, beside the numbers,
rather than leaving a reader to assume they are equally strong evidence.

Exploration is on the page and is deliberately not presented as a third source,
because it is not one: `af explore` compiles what it reached into a workflow
for your manifest, which `af test` runs, and never into a load scenario.
Promoting a discovery adds a workflow, and the page says so in the first
sentence.

Results are the engine's own, not a summary of them. Latency is the five
percentiles the engine measures and a percentile it did not record is absent
rather than drawn at zero. Errors are broken out by reason, because a thousand
timeouts and a thousand refused connections are the same number and completely
different problems. Routes are compared against production's own p95, and one
with no baseline says so rather than reporting no change. The achieved rate is
shown against the rate that was asked for, and a run that fell more than a
tenth short says outright that the application did not keep up, because every
latency figure under it was then measured behind a queue.

A run carries a state and a verdict as two separate things. `blocked` and
`unverified` are two of the product's four verdicts and neither is drawn as a
pass. When a recorded verdict disagrees with the assertions under it, a pass
over something that broke or over something never evaluated, the console says
so above the table instead of showing the contradiction quietly.

The area calls `load.*` on the control plane. Those routes land separately;
until they do this ships the screen and not the data behind it.

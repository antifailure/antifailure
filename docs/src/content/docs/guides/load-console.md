---
title: Reading a load run
description: The Load area of the console, the two kinds of source, and what every number on a result means.
sidebar:
  order: 16
---

`af load run` prints a summary and exits. That is the right amount of output
for one run on your own machine. It is the wrong amount when you want to
compare this week against last week, hand a colleague the evidence, or answer
"was that route always this slow".

The Load area of the console keeps every run, and shows what each one measured
rather than a summary of it.

## The two kinds of source

A source is traffic with a known origin. There are two, and the console keeps
them apart because what a result from each is worth is different.

| Source | Where the traffic comes from | How exactly it replays |
| --- | --- | --- |
| Observed | A weighted mix compiled from an OTLP export or an access log, so it is the endpoint mix production actually served | As a shape. The mix and the rate replay, and the route picker is seeded so two runs send the same sequence. The individual production requests do not replay. |
| Deterministic | A scenario committed to your repository: named steps in an order, with think time between them | Exactly. The same scenario at the same seed plans the same requests in the same order. |

The console states the reproducibility of a source above its numbers. A
scenario that replays request for request and a mix that replays only as a
shape are not equally strong evidence, and that difference matters more when
somebody disagrees with the result than when they agree with it.

Both are described in full under [Load](/docs/concepts/load/).

### Exploration is not a load source

`af explore` also appears on this page, in its own card, and it does not
produce load. It drives a real browser from a seed and compiles what it reached
into a **workflow** for your manifest, which `af test` runs. Promoting a
discovery adds a workflow, not a scenario.

It is on the Load page because it is the other way a route nobody wrote down
gets found. It is not in the source table because it would then look like a
third kind of traffic, which it is not. See
[Workflows](/docs/guides/workflows/) for what a workflow does once it exists.

## Starting a run

Open a source and use **Start a run**. The fields are the same ones
`af load run` takes.

**Scale** multiplies the source's own rate. An observed mix at scale 1 arrives
at the rate production served it.

**Duration** and **Concurrency** bound the run. Leaving concurrency empty
leaves the engine's own default.

**Safe patterns** and **Unsafe patterns** decide what may be sent, and this is
the field to read twice. Every route is unsafe until a safe pattern matches
it. That default is deliberate: a generator that finds `POST /checkout` in an
access log and runs it four hundred times charges four hundred cards. A
pattern is a method and a path glob, as in `GET /api/*`, where `*` covers one
segment and `**` covers the rest; a bare glob matches any method.

An empty safe list sends nothing at all.

On an observed source the console lists the routes the safe list excluded,
under the mix. A mix that looks thinner than production usually means the safe
list is too narrow rather than that the traffic was not there, and that list is
how you tell.

## Reading the result

### Did it keep up

The first number is the achieved rate against the rate that was asked for. A
run that asked for 200 requests a second and achieved 60 has already found
something, before any latency figure is read, because every latency figure
under it was then measured behind a queue. The console says so outright when a
run falls more than a tenth short.

### Latency

Five percentiles: p50, p90, p95, p99 and max. Percentiles rather than an
average, because an average hides the tail and the tail is what a user
notices. A p50 that halves while the p99 doubles is a regression an average
reports as an improvement.

A percentile the run did not record is absent from the ladder. It is never
drawn at zero, because a p99 of nothing and an unmeasured p99 are different
facts.

### Errors, by reason

The error count is broken out by reason rather than totalled. A thousand
timeouts and a thousand refused connections are the same number and completely
different problems.

| Reason | What it usually means |
| --- | --- |
| `timeout` | The application did not answer inside the request deadline. |
| `connection refused` | Nothing was listening. Usually the service is not up yet. |
| `connection reset` | The connection was closed mid-request, often a crash or a restart. |
| `name not resolved` | DNS did not answer for the host, which under a deny-all egress policy is what a blocked host looks like. |
| `malformed request` | The request could not be built. This is the scenario or the mix, not the application. |
| `request failed` | A transport error the runner could not classify further. |

### Routes against production

Each route is compared against production's own p95, worst regression first,
so the answer is the first row rather than something to read the whole table
for.

A route with no production baseline says **no baseline**. It does not say "no
change", and it can never count as a regression. Comparing against nothing and
calling the answer a regression is how a check becomes noise.

### Assertions and thresholds

A deterministic scenario's assertions are shown in the scenario's own field
names, so what you read here is what you edit in the YAML.

Each assertion carries the same four verdicts a run does, and shows the
threshold it declared beside what was measured against it. A `blocked` or
`unverified` assertion is not a pass: an assertion about requests that were
never sent has not held, and a run whose assertions were all unevaluated has
proved nothing either way.

The threshold and the observation are blank for `every_request_succeeded` and
`status_in`, which are not numeric comparisons. The observation alone is blank
when nothing was sent, which is a different answer from an observation of
zero.

## Verdicts

A run carries a state and a verdict, and they answer different questions. The
state says where the run is; the verdict says what it decided. A run that is
still going has no verdict, and a run somebody stopped never gets one.

| Verdict | Means |
| --- | --- |
| `pass` | Every assertion was evaluated and every one of them held. |
| `fail` | At least one assertion was evaluated and did not hold. |
| `blocked` | The traffic never reached the application, so nothing measured is a judgement about it. |
| `unverified` | The run finished and its assertions could not be evaluated, so it proved nothing either way. |

**`blocked` and `unverified` are not passes**, and the console never draws them
as one. If you are gating anything on a load result, gate on `pass` rather than
on the absence of `fail`.

When a recorded verdict disagrees with the assertions under it, a pass over
something that broke or over something that was never evaluated, the console
says so above the table. It cannot correct a verdict the engine computed, but
it will not show you the contradiction quietly.

## Stopping and repeating a run

**Stop this run** asks the runner to stop. The runner acknowledges it on its
next check in, so the state moves to stopping before it moves to cancelled.

A cancelled run keeps whatever it measured, labelled as covering only the part
that ran. Those numbers are real and they are not a measurement of the whole
run.

**Run it again** starts a new run from the same source and settings. It does
not change the original, which stays in the history.

Every finished run shows the command that reproduces it, exactly as the
control plane recorded it at dispatch. It is not rebuilt from the form, so it
cannot drift from the one that actually ran.

## Who can do what

Reading sources and runs needs `environments.view`. Starting, stopping and
repeating a run needs `load.run`, which owners, admins and members hold and
viewers do not. A viewer sees the runs and their results, and is told which
control their role cannot use rather than being shown a page with the control
missing.

---
title: Running a workload from the console
description: The Load area of the console, the four kinds of workload, and what every number on a result means.
sidebar:
  order: 16
---

`af load run` prints a summary and exits. That is the right amount of output
for one run on your own machine. It is the wrong amount when you want to
compare this week against last week, hand a colleague the evidence, or answer
"was that route always this slow".

The Load area of the console keeps every run, and shows what each one measured
rather than a summary of it.

## What a workload is

A workload is something the engine can run against a disposable twin. There are
four kinds, and the console keeps them apart because they measure materially
different things: a mix has no order, a journey has no browser, a workflow has
no request rate, and an exploration has no pass.

| Kind | Command | What it is | How exactly it replays |
| --- | --- | --- | --- |
| Observed load | `af load run` | A weighted mix compiled from an OTLP export or an access log, so it is the endpoint mix production actually served | As a shape. The mix and the rate replay and the picker is seeded, so two runs send the same sequence. The individual production requests do not replay. |
| Scenario | `af load scenario` | Journeys written into your manifest and selected by name: steps in an order, with think time between them | Exactly. The same scenarios at the same seed plan the same requests in the same order. |
| Workflow | `af test` | Workflows out of your manifest, driven in a real browser by an agent | As an outcome rather than as a sequence. An agent reads the page it is on, so two runs can reach the same result by different routes. |
| Exploration | `af explore` | An agent choosing its own way through the application from a goal and a seed | At the same seed, the same wander. |

The console states the reproducibility of a kind above its numbers. A scenario
that replays request for request and a mix that replays only as a shape are not
equally strong evidence, and that difference matters more when somebody
disagrees with the result than when they agree with it.

The first two are described in full under [Load](/docs/concepts/load/), the
third under [Workflows](/docs/guides/workflows/) and the fourth under
[Exploration](/docs/concepts/exploration/).

### A workload names things; it does not contain them

This is the part that surprises people. A workload does not carry a scenario
document or a journey. Every runnable thing is declared in **your** manifest
and selected by name, the same way the command line selects one:

```
af load scenario --only checkout
af test --only sign-up
af explore --only upgrade-a-plan
```

So a workload is a selection plus the knobs its command actually declares. That
is smaller than it first appears and it is the whole of what is real. It is
also a security property rather than a simplification: a scenario is checked
against your manifest's safe route list before anything is sent, and a control
plane able to hand an engine an arbitrary journey would be a control plane that
can send traffic you never allowed.

## Versions

Changing what a workload runs writes a new **version** beside the old one.
Versions are immutable, every run records which one it used, and that is what
makes a run from three weeks ago readable at all.

It also makes comparison work. Running a mix at scale 1 and at scale 4 is two
versions of one workload, so comparing those runs is comparing two versions
rather than two runs whose settings live only in a form somebody has closed.

Saving a form you did not change adds nothing, and the console says so rather
than filling the history with entries that differ in nothing.

### Which knobs each kind has

A knob exists only when the kind's command has a flag for it. Offering one it
does not have would be a control that exists to be refused, so the console does
not draw it and says why underneath.

| Knob | Observed load | Scenario | Workflow | Exploration |
| --- | --- | --- | --- | --- |
| Selection (`--only`) | not applicable | required | optional | required |
| Duration | yes | no | no | no |
| Scale | yes | no | no | no |
| Concurrency | no | yes | no | no |
| Seed | no | a whole number | no | any string |

**Scale** multiplies production's rate, so an observed mix at scale 1 arrives
at the rate production served it, and **duration** bounds the run. Neither
exists for a scenario, which runs its steps in order for as long as they take
rather than sending at a rate.

**Concurrency** caps requests in flight for a scenario. `af load run` has no
such flag, so an observed mix cannot set it: accepting the knob and running at
the generator's own default would produce a run that did not do what its author
asked, with nothing in the result saying so.

**Seed** makes two runs send the same schedule or walk the same way, which is
what makes one run comparable with another. `af load run` takes one on the
command line and a version cannot: a dispatch carries the inputs the workflow
file declares, and the four-input workflow this product shipped before the
console could start a run has no seed among them. Sending an input a workflow
does not declare is a 422 from GitHub.

**A selection is required for a scenario and for an exploration**, and an empty
one is refused. Their commands default to everything the manifest declares, so
a manifest that later gains a scenario would silently change what a saved
workload runs. `af test` genuinely means every workflow when it is given no
`--only`, so a workflow workload may leave it empty and the console says what
that means.

## Starting a run

Open a workload and use **Start a run**. It takes an environment and a version,
and nothing else, because every knob is in the version.

The environment has to belong to the same repository as the workload. A
workload names routes and workflows out of one repository's manifest, so
running it against another one measures nothing, and the console offers only
the environments that can work.

**Nothing runs in the control plane.** Starting a run asks GitHub to run
`.github/workflows/antifailure.yml` in your own repository, on the
environment's own branch. That is what keeps your database, your secrets and
your third-party credentials inside your own cloud. See
[GitHub](/docs/guides/github/) for the workflow file itself.

Two of the four kinds need inputs that the workflow gained when the console
learned to start runs. Against an older copy GitHub refuses the dispatch, and
because it reads the trigger definition from your repository's **default
branch**, adding the newer file on a feature branch alone does nothing. The run
is recorded either way, carrying the refusal, so a dispatch that never happened
is visible rather than silent.

### Safe and unsafe routes are a manifest decision

They are not on this form, and that is deliberate. No load command has a
`--safe` or `--unsafe` flag: the lists live under `load` in your manifest, so
they are committed alongside the code and reviewed with it rather than being
set per run.

The rule they express is the one worth reading twice. Every route is unsafe
until a safe pattern matches it, because a generator that finds
`POST /checkout` in an access log and runs it four hundred times charges four
hundred cards. A pattern is a method and a path glob, as in `GET /api/*`, where
`*` covers one segment and `**` covers the rest; a bare glob matches any
method. An empty safe list sends nothing at all.

A run's result lists the routes the safe list refused. A run that sent less
than you expected usually means the safe list is too narrow rather than that
the traffic was not there, and that list is how you tell.

## Where a run is, and what it found

A run carries a **state** and a **verdict**, and they answer different
questions. Neither implies the other: a run can do all its work cleanly and
fail every threshold in it, which is `succeeded` and `fail`.

| State | Means |
| --- | --- |
| `requested` | Recorded here and asked of GitHub Actions. No engine has picked it up yet, so nothing is running. |
| `accepted` | An engine has claimed the run and is bringing the environment up. |
| `running` | The engine is doing the work now. |
| `succeeded` | The engine did the work and reported. What it found is the verdict. |
| `failed` | The engine reported that the work itself failed. |
| `cancelled` | Stopped before it finished. |
| `timed_out` | The engine reported that it ran out of time. |
| `abandoned` | The deadline passed with no engine reporting. |

**`abandoned` is not a failure.** A failure is something an engine told us;
this is the control plane admitting it never heard. The run may well have
happened, and what is missing is the report rather than necessarily the work.
The two want different things done about them: a failed run is a defect in the
change, and an abandoned one is a defect in the plumbing.

### The commonest reason a run never reports

`af` does the work with or without a token. An environment comes up, the agents
run, the report is written. What the token decides is whether any of that is
**reported** back here.

Without `AF_CONTROL_PLANE_TOKEN` the engine claims no hosted run and sends no
events, so a run you start from the console is dispatched, actually runs, does
everything you asked, and ends `abandoned` at its deadline. Nothing is wrong
with your software and nothing is wrong with the run. The console simply never
heard about it.

Make one with `af token create ci` and add it to the repository's secrets under
that name. The workflow reads it as `secrets.AF_CONTROL_PLANE_TOKEN`. Leave it
out and everything except the hosted reporting keeps working, which is the
self-hosted path and stays supported.

The console says this on the run itself, and only where it applies: on a run
nothing ever claimed. A run an engine **did** claim and then went quiet on is a
different problem, and the console says which two things it cannot tell apart
rather than guessing between them. An engine that loses its lease to a second
engine stops and deliberately says nothing, rather than ending a run the second
one is now doing and destroying its measurements, so a lost lease and a dead
runner look identical from here. The Actions run for the branch is where to
look next.

### A run waiting to be claimed

`requested` with a dispatch behind it is neither running nor an error, and the
console says so rather than leaving it looking like a hang. A GitHub Actions
job has to start, check the code out and reach the control plane, so a minute
or two is ordinary. Much longer than that is usually the token above.

| Verdict | Means |
| --- | --- |
| `pass` | Everything that was evaluated held. |
| `fail` | At least one thing was evaluated and did not hold. |
| `flaky` | The same check answered differently on repeat. |
| `blocked` | The work never reached the application, so nothing measured is a judgement about it. |
| `unverified` | It finished and nothing could be evaluated, so it proved nothing either way. |

**`flaky`, `blocked` and `unverified` are not passes**, and the console never
draws them as one. If you are gating anything on a result, gate on `pass`
rather than on the absence of `fail`.

When a recorded verdict disagrees with the thresholds under it, a pass over
something that broke, or came back flaky, or was never evaluated, the console
says so above the table. It cannot correct a verdict the engine computed, but
it will not show you the contradiction quietly.

A failing run always says what failed it, beside the verdict. That is not
decoration: a load run that sent traffic and broke a threshold carries no
message of its own, because the engine writes one only when nothing was sent.
Left alone it would be a red word with nothing next to it. So the console names
the thresholds that broke, out of the rows that recorded them, and when none of
them did it says that instead rather than going quiet.

## Reading the result

Nothing is written until a run reaches an end, so a run that is still going has
no result at all rather than a partly filled one. The console says which.

### Did it keep up

For a run that sent traffic, the first number is the achieved rate against the
rate that was asked for. A run that asked for 200 requests a second and
achieved 60 has already found something, before any latency figure is read,
because every latency figure under it was then measured behind a queue. The
console says so outright when a run falls more than a tenth short.

### Did anything get checked

For a browser workflow the console shows five counts, not two: passed, failed,
flaky, blocked and unverified. A run with workflows to drive and none passed,
none failed and none flaky checked nothing at all, which is not the same as
nothing being wrong, and the console says that outright. With passed and failed
alone it would have drawn as a run with no failures.

### Latency

Five percentiles: p50, p90, p95, p99 and max. Percentiles rather than an
average, because an average hides the tail and the tail is what a user notices.
A p50 that halves while the p99 doubles is a regression an average reports as
an improvement.

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

An HTTP status of 500 or above arrives spelled as its number.

### Routes against production

Each route is compared against production's own p95, worst regression first, so
the answer is the first row rather than something to read the whole table for.

A route with no production baseline says **no baseline**. It does not say "no
change", and it can never count as a regression. Comparing against nothing and
calling the answer a regression is how a check becomes noise.

A run that selected more than one scenario carries the scenario beside the
route, because two scenarios can send the same route and their two p95 values
do not average into a p95.

### Thresholds

Each threshold carries the same five verdicts a run does, and shows the limit
it declared beside what was measured against it. The limit and the observation
are blank for `every_request_succeeded` and `status_in`, which are not numeric
comparisons; the observation alone is blank when nothing was sent, which is a
different answer from an observation of zero.

### Evidence

What the run left behind, and whether it can still be read. Three answers, not
two:

| Availability | Means |
| --- | --- |
| Kept | Stored, with a checksum to verify it against. |
| On the runner | Written to a path on the CI runner and never uploaded. The machine is gone, so the path is a record of where it was rather than somewhere to fetch it from. |
| Dropped | It existed and retention did not keep it. |

A path on a runner is never drawn as a link. Reports in this product have
carried exactly those paths, and a link to one sends you to a 404 and blames
itself.

## Stopping and repeating a run

The control plane cannot reach a runtime, so **Stop this run** is a durable
command with a deadline rather than a flag. A run nothing has claimed yet is
over immediately; anything else waits for a runtime to confirm, and the console
shows where that request got to. If the deadline passes with nothing
acknowledging it, it says the stop was never confirmed rather than showing you
a cancelled run that may still be going out there.

A stopped run keeps whatever it measured, labelled as covering only the part
that ran. Those numbers are real and they are not a measurement of the whole
run.

**Run it again** runs the **same version**, deliberately, and not the latest. A
retry answers "was that a fluke", and answering it with a definition somebody
edited in the meantime answers a different question while looking like it
answered this one. Running the latest is Start, which is a different button. A
run can be retried once: two independent successors to one failure is a history
nobody can read, and the console links to the one that already exists.

Every run an engine reported on shows the command that reproduces it, exactly
as the engine reported it. It is not rebuilt from the version, so it cannot
drift from the one that actually ran, and a run nothing reported shows no
command rather than a plausible one.

## Promoting an exploration

An exploration finds a route nobody wrote down. Promotion compiles what it
found into a **workflow** for your manifest, which `af test` runs. It never
produces a load scenario: nothing in an exploration record carries a rate.

Paste the document `af explore --json` printed. It lives on whichever machine
ran the command and nothing sends it here on its own.

Two things come back with the new version and neither is decoration.

**What the compilation did not carry.** This list is never empty. It always
carries at least the note that the expectation is the goal, because an
exploration knows what it was looking for and does not know what a passing page
should say; a workflow whose expectation cannot be read comes back `unverified`
rather than as a pass. It also names every friction finding it refused to turn
into an expectation, because "pressing Upgrade plan changes nothing" is a
defect to fix rather than an outcome to assert, and it says how much of the
application was left unexplored.

**The block to paste into `antifailure.yaml`.** Until that block is committed,
`af test --only` cannot find the workflow the new version selects, and the run
comes back saying so. The control plane cannot put a file in your repository,
and it says that rather than returning a name and letting you find out.

## Who can do what

| Permission | Held by | Lets you |
| --- | --- | --- |
| `workloads.view` | every role | read workloads, their versions and their runs |
| `workloads.edit` | owner, admin, member | create a workload, add a version, archive one, promote an exploration |
| `workloads.run` | owner, admin, member | start, stop and repeat a run |

A viewer sees the runs and their results, and is told which control their role
cannot use rather than being shown a page with the control missing. A control
that always answers "your role cannot do this" is worse than no control, and a
missing one leaves somebody unable to tell whether the product lacks the
feature or their role lacks the permission.

Archiving hides a workload from the list and deletes nothing: every run of it
stays readable, and its versions are what those runs mean. A workload with a
run still going cannot be archived, because that would hide the run somebody
may need to stop.

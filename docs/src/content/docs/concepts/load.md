---
title: Load
description: Traffic shaped like production, replayed against a branch.
sidebar:
  order: 12
---

A preview environment with one person clicking through it does not resemble
production. Load replays your real traffic shape against the branch: the same
endpoint mix, the same relative rates, at whatever fraction of production you
ask for.

```yaml
load:
  enabled: true
  source: otel
  source_config:
    path: traffic/production.otlp.json
  scale: 0.05
  duration: 5m
  safe_routes: ["GET /**", "POST /api/search"]
  unsafe_routes: ["POST /api/payments/**", "DELETE /**"]
  traffic:
    profile: .antifailure/traffic.json
    max_age: 336h
  thresholds:
    p95_increase: 0.25
    error_rate: 0.01
```

## Where the shape comes from

| Source | What it reads |
| --- | --- |
| `otel` | An OpenTelemetry trace export in OTLP/JSON, at `source_config.path` |
| `access_log` | A combined format log file, at `source_config.path` |
| `none` | Equal-weight literal safe GET and HEAD routes, or the root when none can be derived. Reported as an assumed smoke, not production traffic. |

Both file sources are read from the repository, so no credential and no
outbound call is involved in deciding what traffic to send.

`af ci` runs load when `load.enabled` is true. The `--load` flag also requests
it when the block is absent or disabled. With no telemetry, literal read routes
in `safe_routes` become a five-request-per-second smoke before `scale` applies.
Glob patterns are filters, not URLs, and write methods are never invented.
`unsafe_routes` still overrides every allowance. If filtering leaves no route,
the report is inconclusive, not a pass. Each completed route's request and
error counts appear in the report.

A smoke counts 4xx responses as errors: a literal page you named must exist.
An observed production mix retains its recorded 4xx semantics. Neither generator
follows redirects, because a response cannot authorize another route
or an external destination.

```
AF-LOD-012 There is no load source called datadog.
```

There were four sources here once. Two of them existed only in the schema and
were refused when a run reached them, which is worse than not offering them at
all: a key you can set that cannot work reads as a broken product rather than
an unfinished one. They are gone, and anything unrecognised is refused by name
with the sources that do work.

The shape is the point. Uniform traffic across every endpoint exercises nothing
real: production is ninety percent reads on three routes, and a change that
makes the fourth-busiest endpoint slow is invisible under a flat mix.

Arrivals are Poisson, not evenly spaced, because real traffic arrives in
clumps and evenly spaced requests hide the queueing behaviour that matters.

### OpenTelemetry

Point `source_config.path` at what an OpenTelemetry collector's file exporter
wrote. One OTLP/JSON document is read, and so is a file with one document per
line, which is what that exporter appends. A line that will not parse is
counted and skipped, because a truncated last line is the normal state of a
file something is still writing to.

Only server spans become traffic. A client span is an outbound call your
service made, and replaying those would send the environment's own dependency
calls at itself. `http.route` is preferred over `url.path` because it is
already templated, and both the current semantic convention attribute names
and the pre-1.21 ones are read.

A trace carries a duration, which a log line does not, so a shape read this way
arrives with production's own p95 for each route already in it. That is the
baseline `p95_increase` compares against. A route seen fewer than twenty times
in the export arrives with no baseline at all and can never be a breach:
comparing against a percentile made of three numbers is how a check becomes
noise people turn off.

### Access logs

A combined format line has no duration in it, so routes read from a log have no
baseline and `p95_increase` has nothing to measure. The manifest refuses the
combination rather than accepting it and staying quiet, and no default fills
the threshold in under this source, so a run here is judged on `error_rate`
alone and says as much.

```
load.thresholds.p95_increase: The load source is access_log and p95_increase
is set.
```

Everything else works: the mix, the relative weights and the arrival rate,
which is counted from the timestamps rather than assumed. When no line carries
a readable timestamp the report says the arrival rate was assumed rather than
presenting a guess as production's number.

## What production actually serves

```yaml
load:
  traffic:
    profile: .antifailure/traffic.json
    max_age: 336h
```

A route list written by hand cannot know which routes touch which tables.
Measured on the Antifailure repository on 2026-09-06: a migration held an
`ACCESS EXCLUSIVE` lock on nine relations for thirty seconds, `pg_locks`
confirmed it from a second connection, and `af load smoke` ran through the
whole window reporting 0.0 percent failed with p95 improving from 41 ms to
17 ms. None of its four `safe_routes` reads the locked table. It was not a
weak result. It was a green one.

`af traffic record` counts what production served, from an OpenTelemetry trace
export or a combined format access log that a collector or a reverse proxy
already wrote, and writes a profile you commit beside the manifest:

| It records | From a trace export | From an access log |
| --- | --- | --- |
| The endpoint mix, per route | yes | yes |
| The arrival rate, over the window it saw | yes | yes |
| Production's p95, per route | yes | no, a log line carries no duration |
| Peak concurrency | yes | no |

It carries no request body, no header, no query string and no identifier: a
path with an identifier in it collapses to `/users/{id}` before it is counted,
so what lands in the file is a route and a number. Nothing here opens a socket,
there is no agent, and no application code changes. The file is one you already
have.

```
af traffic record --from telemetry/traces.json
af traffic show
```

`af traffic show` prints what production serves, busiest route first, with a
mark against every route your run reaches, and prints the `safe_routes` lines
that would cover the ones it does not. It prints them. It does not write them:
this measures and states, and the manifest confirms it. A route being served in
production is not a promise that sending it a thousand times is safe.

With a profile, three things change. The fidelity report's traffic dimension
states the fraction of production's requests your run actually sends and names
the heaviest route it never touches, instead of reporting any shape at all as
a reproduction. The arrival rate is stated beside production's own. And
`p95_increase` becomes able to fire under `access_log` and `none`, because the
profile carries the baseline the source could not.

A profile older than `max_age` is refused rather than quoted, the way a stale
golden is refused rather than branched. Fourteen days by default, where the
volume profile's is thirty: an endpoint mix moves at the rate a team ships, and
a volume profile at the rate a business grows.

## Safe and unsafe routes

`unsafe_routes` are never called. Payments, deletes, anything that emails a
person. Everything they touch is still sandboxed, so this is a second layer
rather than the only one, but a load run that charges a thousand sandbox cards
is a mess to read even when no money moves.

`safe_routes` is the allowlist when you would rather state what may be called
than what may not.

`*` covers exactly one path segment and `**` covers the rest, and for these two
lists the difference matters more than it looks. `DELETE /*` blocks
`DELETE /orders` and does not block `DELETE /orders/42`, and a delete almost
always carries an id, so the entry written to stop deletes would send the
realistic ones and say nothing. Write `**` unless you mean one segment
exactly. The asymmetry is worth knowing in both directions: getting it wrong in
`safe_routes` is loud, because the run refuses everything and tells you, and
getting it wrong in `unsafe_routes` is silent.

## Scenarios

A mix says what production serves. It says nothing about order, and order is
where a lot of breakage lives: the second request arriving while the first is
still in flight, fifty sessions walking one journey while everything else
carries on underneath.

A scenario is that journey, declared:

```yaml
scenario: impatient_upgrade
description: A returning customer opens billing and resubmits when it feels slow.
ramp_ms: 500
steps:
  - request: GET /settings/billing
    think_ms: 400
    jitter_ms: 200
  - request: GET /api/subscriptions
  - parallel:
      - request: GET /api/subscriptions
        after_ms: 300
      - request: GET /settings/billing
        after_ms: 450
assertions:
  - name: every_request_answered
    every_request_succeeded: true
  - name: billing_stayed_fast
    step: GET /settings/billing
    p95_below_ms: 800
```

Name it from the manifest and say how hard to run it:

```yaml
load:
  enabled: true
  safe_routes: ["GET /**"]
  scenarios:
    - path: scenarios/impatient_upgrade.yaml
      sessions: 50
      iterations: 4
    - path: scenarios/checkout_browse.yaml
      sessions: 10
      start_after: 30s
```

Then `af load scenario`.

The steps are HTTP requests. Clicking a button is `af test` and the browser
agents; this is what the load generator sends, at the concurrency load runs at,
with no model call in the loop.

`sessions` walk the journey at once, spread over `ramp_ms` so fifty of them do
not arrive on the same millisecond. `iterations` is how many times each session
repeats it, so the work a scenario does is declared rather than decided by how
long the clock happened to run. `start_after` delays a scenario, which is how
you get a burst landing on an application that is already busy.

Every step is checked against `safe_routes` before anything is sent. A scenario
that names a route nobody declared safe does not run at all, including the safe
half of it, because a measurement of half a journey under the whole journey's
name is worse than no measurement.

### Assertions

An assertion sets exactly one of four measures, and each one is something the
generator observes directly:

| Measure | Holds when |
| --- | --- |
| `every_request_succeeded` | No transport error and no status at or above 400 |
| `p95_below_ms` | The ninety fifth percentile is under the number |
| `error_rate_below` | The share of failed requests is under the fraction |
| `status_in` | Every response carried one of the listed codes |

Add `step: GET /settings/billing` to scope one to a single request. Without it
the assertion covers the whole scenario.

A 400 counts as a failure here and does not in the mix. A 404 inside
production's own traffic is production's own traffic; a 404 inside a declared
journey means the journey is broken.

Assertions about a database row belong to
[invariants](/docs/guides/invariants), which run against the branch after the
workflows and can see the data. Assertions about what a page shows belong to
workflows. A scenario measures the requests it sent.

### Verdicts

Scenarios answer in the same words the rest of a run does.

| Verdict | Means |
| --- | --- |
| `pass` | Every assertion held |
| `fail` | An assertion was measured and did not hold |
| `blocked` | It did not run, because a route it sends is not in `safe_routes` |
| `unverified` | It ran and nothing could be measured, or it asserts nothing |

`blocked` is deliberately not a failure: a scenario that could not be sent has
found nothing wrong with your change. `af ci` exits non-zero only on `fail`, so
what keeps it from reading as a pass is `AF-LOD-015` below and its own count in
the summary.

```
AF-LOD-014 3 scenario assertions did not hold.
AF-LOD-015 The scenario impatient_upgrade proved nothing: it did not run,
1 request is not named in safe_routes
```

## Thresholds

```
AF-LOD-011 Load exceeded 2 thresholds the manifest sets.
```

`p95_increase: 0.25` means a quarter slower than the baseline is a failure. The
baseline is production's own p95 for that route, which comes from the traffic
source, so a route the source could not measure is never a breach. Absolute
numbers are deliberately not used: they fail on a slow CI runner and tell you
nothing about the change.

Which means the threshold needs durations from somewhere, and the traffic
source carries them only under `otel`. Setting it under `access_log` or `none`
with nothing else to compare against is refused by the manifest, and the
default is not applied there either: a threshold the report lists and no route
can be measured against is a check everybody believes is running.

The second place a baseline can come from is a recorded traffic profile, which
carries production's own p95 per route. Declare `load.traffic.profile` and the
threshold is allowed under any source, because the comparison now has something
on the other side of it. The run says which routes took their baseline from the
profile, and says so when none could.

```
AF-LOD-016 The p95_increase threshold proved nothing: no baseline for any of
the 4 routes the run sent, so nothing was compared.
```

That is the case the manifest cannot see. A trace export whose every route was
seen fewer than twenty times arrives with no baseline anywhere, so the
threshold was in force and evaluated nothing, and the run exits non-zero rather
than reporting a clean p95. Point `source_config.path` at a longer export.

`error_rate: 0.01` is counted from the run's own responses, so it needs no
baseline and applies under every source.

Neither of these compares against the base branch, and nothing in
`load.thresholds` does: no key in it brings a second environment up, so none of
them can see another build. That comparison is `load.comparison` below.

There is no `query_count_increase`. It was in the schema, nothing ever read it,
and a manifest that sets it is now refused by name. The check it describes is
`insights.query_regression`, and how much growth fails it is
`insights.regression_factor`.

## Comparing two builds

Everything above measures ONE build. `p95_increase` divides a measured p95 by
production's own p95 for that route, which answers "is this route slower than
the fleet serves it". It does not answer "did my change make it slower", and
for a long time nothing here did, while the schema's own description of this
block claimed otherwise. The block that answers the second question is
`load.comparison`.

```yaml
load:
  enabled: true
  source: otel
  source_config:
    path: telemetry/traces.json
  safe_routes:
    - GET /orders
  comparison:
    enabled: true
    baseline: merge_base
    thresholds:
      # There is no default for either of these, and these numbers are not one.
      # Measure your own noise floor first, below, and set them above it.
      p95_increase: 0.6
      throughput_drop: 0.3
```

```
af load compare
```

It brings a second environment up from the base revision, branches the SAME
golden for both so the two sides answer queries over identical rows, sends both
the same weighted mix in the same order under the same seed, and reports every
route and every run wide number that moved.

```
  route          base p95  this build p95  change    moved
  GET /orders    41.2      104.7           +154.1%   worse
  GET /health    2.1       2.0             -4.8%     better
```

One golden for both sides is the part that makes the number worth anything. Two
goldens would mean the two builds answered queries over different rows, and
every latency difference would be a difference in how much data each side held
rather than a difference in the code. The candidate environment comes up first
so that its golden is the one the base side is pinned to, which also means a
scheduled golden refresh landing mid comparison cannot separate the two.

### What the comparison cannot control

Every report says this, because a number labelled a regression that is really
machine noise is how a check stops being read.

The two runs are sequential. Two environments sending traffic at once on one
host would contend with each other and measure that instead, so the base
branch runs first and this build runs second, and the second meets a host the
first has just warmed. The seed makes the request sequence identical. It does
not make the machine, the neighbours on the host or the time of day identical.

So a difference is a difference. A threshold is what turns one into a verdict,
and it is yours to set.

### Measure your own noise floor first

None of the comparison thresholds has a default, and that is a measurement
rather than an omission.

Two builds of IDENTICAL code, sent the same requests under the same seed,
differed by this much. Five repeats per run length.

| run length | worst p95 difference | median | worst throughput difference | median |
| --- | --- | --- | --- | --- |
| 2 seconds | 52.0% | 24.7% | 9.6% | 3.1% |
| 10 seconds | 44.3% | 17.9% | 20.9% | 4.9% |
| 30 seconds | 36.4% | 7.6% | 6.8% | 1.1% |

Where those numbers came from, because a measurement with no conditions
attached is worth less than no measurement. They were taken on one 8 core
developer laptop running several other builds at the same time, at a load
average around 49 with the container virtualisation taking most of a core.
That is six times the point at which this repository's own gate warns that
timing measurements stop meaning anything. The test prints the core count, the
load average and the virtualisation share beside every cell it measures, so
nobody reads one machine's figures as another's.

They are therefore an UPPER bound, and how much of that bound is the
instrument rather than the machine is NOT known. Two things are mixed together
in it and they behave differently. A p95 estimated from a few hundred samples
carries sampling error on any machine, and that part shrinks as the run
lengthens: the MEDIAN divergence above falls from 24.7% to 7.6% between a two
second run and a thirty second one. Contention adds spikes on top, and that
part barely moves with run length: the WORST divergence only falls from 52% to
36% over the same range. Sampling error is the product's, spikes are the
host's, and this measurement does not separate them.

So the claim this product is entitled to make is the narrow one. This
comparison reliably catches large regressions. How small a regression it can
catch depends on the hardware you run it on, and the only honest way to know
yours is to measure it.

### What a run can see, and when it refuses

Before it judges anything, the comparison measures its own resolution, per
route, from the run's own sample count and distribution. A percentile taken
from n samples is an order statistic whose rank is itself random, so a p95 from
twenty samples sits one slow request from the maximum and moves by the width of
the whole tail. That distance is printed beside the difference:

```
  route          base p95  this build p95  change    moved             can see
  GET /accounts  83.1      570             +585.9%   too close to say  1024%
  GET /statements 237      309             +30.2%    too close to say  480%
```

A difference of plus 586 percent beside a resolution of plus 1024 is a reading
nobody can mistake for a regression, and those two numbers came from comparing
a branch against itself where the only change was a comment.

The verdict follows from where your limit falls relative to that interval:

| the interval around the difference | verdict |
| --- | --- |
| entirely above the limit | the limit was crossed |
| entirely at or below the limit | the limit held |
| the limit falls inside it | this run cannot tell, and says so |

The third case is reported as unverified and exits non-zero. It is never a
pass. A run that could not place your limit has not cleared it.

This does not loosen your threshold. A limit is your declared tolerance for a
real change, and widening it to silence a false alarm would hide real ones. A
run that CAN see the difference still decides: a regression of 600 percent
against a 100 percent limit, measured by a run whose resolution is 200 percent,
is still a failure, because even the pessimistic end of that interval is above
the limit.

A direction is withheld on the same evidence. A change smaller than the
distance the number could have moved on its own reads `too close to say`
instead of better or worse.

If a route refuses, the two things that fix it are more samples and a quieter
machine. Send for longer, or raise the rate.

One limit, stated rather than implied: this band is the sampling error a SINGLE
run can see in itself. It does not include drift between the two runs on a busy
host, which is larger. The two samples described above disagree with each other
by more than the band around either of them. So treat it as a floor on the
uncertainty and not the whole of it, which is the other reason to measure your
own noise floor below.

### Measuring yours

Point the comparison at a branch that changes nothing, and run it a few times.
Every difference it reports is noise by construction, because there is no
change for it to be measuring.

```
git switch -c noise-floor origin/main
af load compare --baseline origin/main --duration 30s
```

Repeat that five times and read the largest p95 difference it prints. That
number is your floor. Set `p95_increase` above it, and prefer a longer
`duration`: more samples in the tail is the one thing that helps on every
machine.

Nothing is wrong with either side during those runs. A p95 is the tail of a
distribution, a short run has few samples in that tail, and a shared machine
has neighbours. Even so, the obvious defaults, 0.25 for latency to match the
production facing threshold and 0.1 for throughput, sit UNDER the floor
measured above: shipping them would have failed builds that changed nothing,
and a check that cries wolf is the last one anybody reads.

The table above was produced by this product's own test of the same thing,
which is in the repository if you want to read what it does:

```
AF_NOISE_FLOOR=1 go test ./internal/workload -run TestNoiseFloor -v
```

For scale: the deliberate regression this product tests against, a single route
given a sleep of 40 milliseconds, moves that route's p95 by roughly 600 to 750
percent and cuts throughput by roughly 78 percent, measured on the same
contended machine as the floor. That is an order of magnitude clear of it. A
regression of 20 percent on a two second run is not, and no threshold can
rescue that. Lengthen the run instead.

### Thresholds against the base branch

`p95_increase` under `load.comparison.thresholds` is a different number from
the one under `load.thresholds`, and they are spelled the same on purpose: the
question "how much slower is too slow" has one answer, and the two keys differ
in what they divide by. This one divides by the base branch's own p95 for that
route.

`throughput_drop: 0.1` fails a build serving a tenth fewer requests per second
than the base branch did. It is read from the rate each run actually achieved
rather than the rate it aimed at, because a run that fell behind its target
reports the target as fine while the queue grows. Nothing else in this product
compares throughput, and a build can serve every request it completes quickly
while completing half as many.

`error_rate_increase` is in absolute points rather than as a ratio, and has no
default. A base branch that failed nothing has no ratio to be measured against,
and a build that introduces errors where there were none is the case that most
needs catching.

A route present on one side only is `unmeasurable`, never a breach and never a
pass. A candidate that stopped serving a route has no p95 to be slower than,
and reporting that as clean would hide the loudest result the run can produce.

```
AF-LOD-024 The base branch comparison judged nothing: every declared base
branch threshold went unmeasured, so this comparison judged nothing.
```

That is the same discipline `AF-LOD-016` applies to the single run threshold. A
limit that was in force and evaluated zero routes has not passed, and the
command exits non-zero rather than reporting a clean comparison.

### How it differs from the oracle

`af oracle` also brings a second environment up from a baseline revision and
also branches one golden for both. It sends declared probes and diffs the
RESPONSES and the DATABASE CONTENTS, which is a much stronger claim about
correctness and says nothing about speed. `af load compare` sends the traffic
mix and differences the TIMING and the THROUGHPUT. They answer different
questions and neither replaces the other.

## Aborting

```
AF-LOD-002 The load run was aborted after the error rate exceeded 50% for 30s.
```

A branch that is failing every request has already answered the question, and
continuing wastes several minutes to produce a number nobody needs.

## Targets

```
AF-LOD-001 The load target https://staging.example.com is not an environment
this engine created.
```

Load runs against environments Antifailure made, and refuses anything else.
This is a load generator with a production traffic shape pointed at it; the one
thing it must never do is point at production.

## Everything on this page goes over HTTP

Which is the right measurement for a change to a handler and the wrong one for
a change to an index, a lock or a query. A mix, a scenario and a workflow all
reach the database through the application, so the number each reports is the
application's latency with the database somewhere inside it.

[A SQL workload](/docs/concepts/sql-workloads) is the other half: clients on
their own connections running whole transactions against the branch, reported
as transactions per second and statement latency. It runs under `af load sql`
and is configured under `load.sql`.

`af load compare --sql` compares THAT workload on two builds instead of the
HTTP mix. Same second environment, same golden for both sides, same
interleaved rounds and the same `load.comparison.thresholds`. What changes is
the unit, which becomes the transaction and the statement inside it with p50,
p95 and p99 on each side, and the throughput, which becomes committed
transactions a second. It is refused without a `load.sql` block rather than
quietly falling back to the mix.

Related: [SQL workloads](/docs/concepts/sql-workloads),
[insights](/docs/concepts/insights), [scheduling](/docs/concepts/scheduling).

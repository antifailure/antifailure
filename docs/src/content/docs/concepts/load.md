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
  safe_routes: ["GET /*", "POST /api/search"]
  unsafe_routes: ["POST /api/payments/*", "DELETE /*"]
  thresholds:
    p95_increase: 0.25
    error_rate: 0.01
```

## Where the shape comes from

| Source | What it reads |
| --- | --- |
| `otel` | An OpenTelemetry trace export in OTLP/JSON, at `source_config.path` |
| `access_log` | A combined format log file, at `source_config.path` |
| `none` | No shape; a default that exercises the root, and says so |

Both file sources are read from the repository, so no credential and no
outbound call is involved in deciding what traffic to send.

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

## Safe and unsafe routes

`unsafe_routes` are never called. Payments, deletes, anything that emails a
person. Everything they touch is still sandboxed, so this is a second layer
rather than the only one, but a load run that charges a thousand sandbox cards
is a mess to read even when no money moves.

`safe_routes` is the allowlist when you would rather state what may be called
than what may not.

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
[invariants](/docs/guides/invariants/), which run against the branch after the
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
found nothing wrong with your change. It still exits non-zero, because a check
that ran nothing and reported green is a check everybody believes is running.

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

Which means the threshold needs a source that carries durations, and only
`otel` does. Setting it under `access_log` or `none` is refused by the
manifest, and the default is not applied there either: a threshold the report
lists and no route can be measured against is a check everybody believes is
running.

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

There is no `query_count_increase`. It was in the schema, nothing ever read it,
and a manifest that sets it is now refused by name. The check it describes is
`insights.query_regression`, and how much growth fails it is
`insights.regression_factor`.

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

Related: [insights](/docs/concepts/insights/), [scheduling](/docs/concepts/scheduling/).

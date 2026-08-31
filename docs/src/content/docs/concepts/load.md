---
title: Load
description: Traffic shaped like production, replayed against a branch.
sidebar:
  order: 10
---

A preview environment with one person clicking through it does not resemble
production. Load replays your real traffic shape against the branch: the same
endpoint mix, the same relative rates, at whatever fraction of production you
ask for.

```yaml
load:
  enabled: true
  source: datadog
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
| `datadog` | Endpoint mix and rates from APM |
| `newrelic` | The same, from New Relic |
| `otel` | An OpenTelemetry collector |
| `access_log` | A log file, when you have no APM |
| `none` | No shape; only what the manifest states |

The shape is the point. Uniform traffic across every endpoint exercises nothing
real: production is ninety percent reads on three routes, and a change that
makes the fourth-busiest endpoint slow is invisible under a flat mix.

Arrivals are Poisson, not evenly spaced, because real traffic arrives in
clumps and evenly spaced requests hide the queueing behaviour that matters.

## Safe and unsafe routes

`unsafe_routes` are never called. Payments, deletes, anything that emails a
person. Everything they touch is still sandboxed, so this is a second layer
rather than the only one, but a load run that charges a thousand sandbox cards
is a mess to read even when no money moves.

`safe_routes` is the allowlist when you would rather state what may be called
than what may not.

## Thresholds

```
AF-LOD-011 Load exceeded 2 thresholds the manifest sets.
```

Thresholds are deltas against the same run on the base branch, not absolute
numbers. Absolute numbers fail on a slow CI runner and tell you nothing about
the change. `p95_increase: 0.25` means a quarter slower than base is a failure.

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

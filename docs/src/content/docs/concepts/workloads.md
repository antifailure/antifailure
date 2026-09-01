---
title: Workloads
description: A saved selection out of your manifest, run through the command that names it, with the exact command that reproduces the result.
sidebar:
  order: 14
---

A workload is a saved selection out of your manifest plus the knobs the command
that runs it actually has. `af workload run` reads one, runs it through the
command that kind names, and writes a result document.

It exists so a hosted control plane can ask this engine to do something without
a second implementation of anything. Every kind executes through the same call
`af load run`, `af load scenario`, `af test` and `af explore` already make.

`af workload` is hidden from `af --help` on purpose. The commands a person runs
are `af load run`, `af load scenario`, `af test` and `af explore`; this is what
a control plane calls on their behalf, and it is documented here rather than in
the command reference for that reason.

```
af workload run       --kind --select --duration --scale --seed --concurrency
                      --run-id --branch --result --timeout --teardown
af workload teardown  --branch --result
af workload promote   <report.json> --only --persona --seed --against
af workload compare   <baseline.json> <candidate.json>
```

## Four kinds, and they stay four

| Kind | Runs through | Measures |
|---|---|---|
| `observed_load` | `af load run` | a weighted mix compiled from OTLP or access logs. Routes, percentiles, no order. |
| `http_scenario` | `af load scenario` | a declared journey with waits, sessions and assertions. An order, no browser. |
| `browser_workflow` | `af test` | declared workflows driven through a real browser. Steps and a verdict, no request rate. |
| `exploration` | `af explore` | a seeded wander towards a goal. Findings rather than a pass. |

There is no shared representation underneath them and there is not going to be
one. A mix has no order, a journey has no browser, a workflow has no request
rate, and an exploration has no pass. A single type that all four compiled into
would have to be the union of what none of them share, and every reader of it
would then have to ask which fields are real for the run in front of them.

## The result carries the command that reproduces it

Every result document carries the plain command that produced the same run:

```
af load run --duration 1m0s --scale 1 --seed 1
```

Not `af workload run`. A hosted measurement whose command only the hosted caller
can run is a number you have to believe.

Two rules follow from that. Every knob is stated explicitly, even when the
definition left it out and the default filled it in, because a command line that
omits a flag reproduces whatever that flag defaults to on the day you paste it.
And a knob only exists if the plain command has a flag for it, which is why the
next section reads the way it does.

## A knob with no flag is refused, not ignored

```
$ af workload run --kind observed_load --concurrency 40
AF-WLD-002: The observed_load kind cannot set concurrency.
```

`af load run` has no `--concurrency` flag. Accepting the knob and running at the
generator's own default of 20 would produce a run that did not do what its
author wrote, and nothing in the result would say so.

The rule is exactly that, with nothing added: a knob is refused when, and only
when, the command this kind runs has no flag for it.

| Knob | `observed_load` | `http_scenario` | `browser_workflow` | `exploration` |
|---|---|---|---|---|
| `--select` | refused | required | optional, empty means all | required |
| `--duration` | yes | refused | refused | refused |
| `--scale` | yes | refused | refused | refused |
| `--seed` | yes, a number | yes, a number | refused | yes, free text |
| `--concurrency` | refused | yes | refused | refused |

An empty selection is refused for `http_scenario` and `exploration`, because
those commands would then run everything the manifest declares, and a manifest
that gains a scenario would silently change what a saved workload runs.

## What the exit code means

| Outcome | Exit |
|---|---|
| `pass` or `flaky` | 0 |
| `fail` | 8 |
| `blocked` or `unverified` | 7 |
| cancelled, or past its deadline | 9 |
| torn down with resources still standing | 10 |
| a refused knob | 2 |

The row that differs from `af test` on purpose is the third. `af test` exits 0
on `unverified` and does not count `blocked` against a run, which means a job
gating on its exit code cannot tell "the tests passed" from "nothing was
tested". A workload is a job somebody gates on, so a run that measured nothing
gets its own non-zero code and its own error, `AF-WLD-013`, separate from the
one a real failure gets.

## Cancellation, deadlines and teardown

`--timeout` bounds the run. A deadline that fires produces a result document
saying `timed_out` rather than an error, because "it did not finish in time" is
a finding.

`--teardown` removes the environment when the work ends, however it ends. The
teardown runs on a context the cancellation cannot reach, so pressing stop
cleans up rather than leaving containers running behind a run that says it
ended. What was actually removed, and everything still standing, is in the
result.

`af workload teardown` is the same teardown on its own, with the same
acknowledgement.

```
af workload teardown --result torn-down.json
```

## Reporting to a hosted control plane

Everything above works with no control plane at all, and that is the ordinary
case: `af workload run` on a laptop measures the same things and writes the same
document. What a control plane adds is a row somebody can watch while it
happens.

Set `AF_CONTROL_PLANE_TOKEN` where `af` runs and four things change.

The run is **claimed**. A hosted run reaches your repository as a
`workflow_dispatch`, and a dispatch carries only the inputs your workflow file
declares. GitHub reads that declaration from your **default branch** and refuses
an undeclared input with a 422 that looks exactly like the file being missing,
so the control plane cannot put the run identifier in the dispatch without
breaking every copy of the workflow already in the wild. It sends what to run,
and the engine asks which recorded request the job belongs to. That also means a
run whose dispatch was refused, because no App is installed or Actions are off,
is still picked up by an engine you start by hand.

The run **says when it started**, so the console shows it running rather than
waiting to be picked up.

The run **says it is still going**, once a minute. Without that a long run is
recorded as *abandoned* at its deadline, and abandoned and failed are different
sentences: a failure is something the engine reported, and abandoned is the
control plane admitting it never heard.

The run **reports what it measured**. The payload is the same document
`--result` writes, so the artifact your job uploads and the numbers the console
draws cannot disagree. A report that cannot be delivered is spooled to disk
rather than dropped, and the next `af` command on that machine sends it.

A cancel pressed in the console reaches the run on the same minute tick, stops
the work, and is reported as cancelled.

A lease taken by another engine also stops the work, and is the one case where
nothing more is reported. That happens when a run went quiet long enough for
somebody else to pick it up, and it means this engine no longer has any standing
to say how the run ended: another engine may be running it right now, and a
report from here would end it for them. The result document is still written and
still uploaded, so nothing is lost where the work happened.

`--run-id` is for reproducing one particular hosted run by hand. Passing it
claims nothing, deliberately: an engine reproducing a run on a laptop must not
take the next queued run away from CI.

Without a token none of this happens and nothing fails. The work is the thing
and the reporting is a view of it.

## Promoting an exploration

`af workload promote` compiles one exploration into the workflow definition a
hosted `browser_workflow` runs.

```
af explore -o json > explored.json
af workload promote explored.json --only upgrade
```

The compiled workflow is planned again from the start path on every run rather
than replayed, so it can take a different route to the same goal. That is what
makes a declared workflow survive a redesign, and it is also why a promotion
that did not say so would mislead whoever reads it. Every promotion lists what
the compilation could not carry over, one line each:

- the workflow is planned again rather than replayed
- the values the exploration typed into forms are not carried over
- the seed does not steer the workflow, because it makes no random choices
- the pages visited on the way are not asserted, only the goal
- friction findings are recorded and not asserted, because a defect to fix is
  not an outcome to require

An exploration that did not reach its goal is refused. The expectation a
compiled workflow asserts is the goal sentence, and a wander that never got
there is no evidence the goal is reachable at all.

Each promotion records a digest of the journey the exploration walked. Walk the
same goal from the same seed later and compare:

```
af workload promote fresh.json --against promotion.json
```

A different digest means the route to the goal has changed since the workflow
was promoted, which a passing workflow cannot tell you on its own.

## Comparing two runs

```
af workload compare baseline.json candidate.json
```

Two result documents of the same kind, differenced: the run wide numbers, every
route on either side, and every threshold whose verdict changed. A threshold
that went from `pass` to anything else is counted as a regression; one that went
from `unverified` to `fail` is reported as changed and not as a regression,
because it was never passing.

This is not [the differential oracle](/docs/concepts/oracle/). `af oracle`
brings a second environment up from a baseline revision, branches one golden for
both so they start from identical rows, sends both the same probes and diffs the
responses and the database contents. That is a far stronger claim. `af workload
compare` differences two runs that already happened, which is cheaper and works
over history, and every comparison it produces states what it cannot control:
two runs against two environments are not a controlled experiment.

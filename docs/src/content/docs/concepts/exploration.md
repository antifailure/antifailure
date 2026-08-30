---
title: Exploration
description: Agents that pursue a goal with no declared workflow, and report where an application costs somebody effort without failing.
sidebar:
  order: 9
---

A workflow says what to do and what proves it happened. An exploration says
only what somebody is trying to achieve, and then wanders.

It reads each page through the accessibility tree, chooses somewhere to go,
goes there, and writes down every place the application cost it effort. That
answers the question a declared workflow cannot ask: nothing broke, so why
would somebody give up here.

```yaml
explore:
  enabled: true
  goals:
    - name: upgrade-a-plan
      goal: Upgrade the workspace from the free plan to the paid one.
      persona: owner
      seed: upgrade-a-plan
      start_path: /settings/billing
      slow_ms: 3000
      budget:
        steps: 40
```

Run it with `af explore`. Every finding names the page, the control and the
step, so you can go and look.

## An exploration cannot fail your build

`af explore` reports `pass` unless it could not run at all, and it exits zero
either way. That is deliberate.

Nobody declared what the application should do on the pages an exploration
wanders onto. A run that noticed people would hesitate at a control has not
shown that the change under review broke anything, and turning that into a red
mark would put a failing check on a pull request that is fine. A check like
that gets muted, and a muted check is worse than none, because everybody
believes it is still running.

So findings go in the report body. They never reach the exit code, and
`af test` still decides whether the change is safe.

An exploration that could not open the application, or whose persona could not
sign in, reports `blocked`, the same as a workflow would. Blocked is not a
clean run: it means nobody looked.

## Reproducible from the seed

Every choice an exploration makes comes from its seed, and every duration from
the injected clock. The same seed against the same application takes the same
path, step for step, and finds the same things.

That is the difference between a finding you can act on and one you have to
take on trust. Each result carries the command that replays it:

```
af explore --only upgrade-a-plan --seed upgrade-a-plan
```

The seed defaults to the goal's name, so a manifest that sets nothing still
replays. Two goals may not share a seed: they would walk the same tie breaks
and cover less than their step counts suggest.

One consequence is worth stating. The values an exploration types into a form
come from the seed too, so replaying a sign up types the same address as the
first run, and an application is right to refuse it. Fresh data and a path
that repeats cannot both come from one seed, and the path that repeats is what
an exploration is for.

## What it will not press

An exploration signs in as a real persona with real permissions on a real
branch. An agent that presses "Delete workspace" on step three has removed
what every later step would have looked at, and one that signs out turns every
page after it into the logged out one.

Controls whose accessible name reads as destructive are refused: sign out, log
out, delete, remove, revoke, and cancelling an account, subscription, plan or
workspace. "Cancel" on its own is left alone, because it usually closes a
dialog.

Each refusal is listed, so an unexplored corner reads as unexplored rather
than as clean.

## The taxonomy

Six kinds. Every one is decided from something the runner measured, which is
why there is no "confusion" and no "frustration" here: the runner can see a
control that did nothing and a page it came back to twice, and it cannot see a
person's patience.

| Kind | What it means |
| --- | --- |
| `no_effect` | A control was activated and nothing changed: same address, same controls, same fields, same text. |
| `dead_end` | A page offers no way onward at all: no control and no field, or nothing but controls an exploration must not press. Not a page whose controls this run happens to have tried already, which is just the run finishing. |
| `revisit` | The path left a page and came back to it unchanged. The route loops. |
| `unnamed_control` | The page carries interactive elements with no accessible name, so neither a screen reader nor an agent can say what they do. |
| `slow_response` | One step took longer than `slow_ms` allows. The reading and the threshold are both on the finding. |
| `goal_unreached` | The whole run ended without the goal ever being visible on any page. It names the goal's words that appeared nowhere, which is usually how you find out the goal described where somebody started rather than where they end up. |

Each finding carries the page, the control where one element is responsible,
the step, a confidence, what happened and what to do about it. Confidence is
`high` when the runner measured it and `medium` when it inferred it from the
goal's words.

There is deliberately no severity score and no estimate of lost conversions. A
number with no measurement behind it reads as evidence and is not.

## Turning a discovery into a workflow

The report is not the valuable part. The valuable part is that a run which
found something becomes a check that runs on every pull request.

```
af explore --only upgrade-a-plan --emit-workflow
```

That prints the `workflows:` block which replays the path, built from the moves
the agent actually made, with the accessible names it used. Paste it into
`antifailure.yaml` and `af test` runs it from then on.

Two things about the emitted block are said out loud rather than hidden. Its
expectation is the goal sentence, because an exploration knows what it was
looking for and not what a passing page should say: check the words appear on
the page the run ended on, or rewrite it. And a friction finding is not an
expectation. "Pressing Upgrade plan changes nothing" is something to fix, not
an outcome to assert, so the emitted workflow will not carry it. The notes
printed alongside name every finding it leaves behind.

## What it types, and what it prints

An exploration fills a form with the same values a declared workflow uses: a
reserved `example.test` address, the `+1 555 0100` block, and Stripe's test
card. Nothing it types can reach a real inbox, handset or processor.

A form submitted with GET puts every field in the address bar, and that address
travels into a finding and into a pull request comment. So anything the agent
typed is replaced with `[typed]` in every URL it reports. It knows exactly what
it typed, which is what makes that precise rather than a guess at what looks
sensitive.

## Evidence

An exploration captures what a workflow captures: a video, a Playwright trace,
a screenshot, the browser console, and the requests the page could not make.
The trace is the thing to open.

Those files live in the run's artifacts directory. On a CI runner that
directory does not outlive the job, so treat a trace path in a report as
something to open while the run is fresh rather than as a durable record.

## What this does not do

It drives one browser, one context, one page, in a serial loop. There are no
parallel tabs and no shared session between them.

It does not model personality. Timing and choice come from the seed and the
goal's words, not from a trait vector, so an exploration is not a claim about
how any particular kind of person behaves.

It chooses without a model. `af test` will read a page with a model when you
set a key; `af explore` never does, because a model's answer is not
reproducible from a seed and reproducibility is the property this feature
exists to have.

## See also

- [Agents](/docs/concepts/agents/), for declared workflows and the verdicts
- [Workflows](/docs/guides/workflows/), for writing the block an exploration compiles into

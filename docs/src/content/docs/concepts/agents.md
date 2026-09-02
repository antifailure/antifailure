---
title: Agents
description: What the agent runner does, and why a workflow is described rather than scripted.
sidebar:
  order: 10
---

An agent uses the environment the way a person would: it opens the application
in a browser, signs in as a persona, and works through a workflow described in
prose.

```yaml
personas:
  - name: owner
    email: owner@example.test
    role: admin
    login: password

workflows:
  - name: sign-up
    persona: owner
    description: >
      Sign up for a new account with a fresh email address. Complete every
      required field, submit, and confirm you land on a signed in page rather
      than back on the form with an error. Then confirm a welcome email arrives.
    expect:
      - The account is created and the session is signed in.
      - A welcome message arrives in the inbox.
```

## Why prose and not a script

A selector-based script tests that the page still has the elements it had when
somebody wrote the script. It breaks when a button moves and passes when a
button stops working, which is close to the opposite of what is wanted.

A description says what a person is trying to do. The agent finds its own way,
so a renamed field does not fail the test and a broken flow does.

The cost is honest: it is slower and less deterministic than a selector script.
It is worth it for the flows that matter and wasteful for a unit test.

## What `expect` is for

`description` is what to do. `expect` is what must be true afterwards, and it is
what the verdict is decided against. Without it, an agent that clicked around
and got nowhere can be reported as having finished.

Afterwards is the word to read twice. An expectation is checked against the page
the workflow ends on, so naming something that is only on the page it starts
from asks for a page that cannot exist, and the workflow can never pass however
well it works. A sign-in workflow expects the signed in state, not the button it
pressed to get there.

With no model key, the check is made against the page's visible text. Two
consequences worth knowing before you write one:

- A sentence about your product ("the totals are right") usually shares no word
  with the page, so it can be neither confirmed nor contradicted, and the run
  comes back `unverified` rather than passing. Name what the page says.
- A placeholder is not visible text. `filter by action` inside an empty input is
  what a browser shows and not what it reports, so an expectation naming one
  never matches. Name a heading, a label, or a value instead.

A model key removes both limits, because the model reads the page rather than
matching words against it.

## Budgets

```yaml
    budget:
      steps: 40
      usd: 0.50
      duration: 5m
```

```
AF-AGT-002 Workflow sign-up exhausted its budget of 40 steps before completing.
```

An agent that cannot find its way will keep trying. The budget is what turns
that into a result instead of a bill, and a workflow that regularly exhausts one
is usually telling you the flow is genuinely hard to complete.

## The runner

The agent runner ships beside the binary and travels with the release, so the
source a release was tested with is the source it runs.

```
AF-AGT-004 The agent runner could not be found: no runner directory beside the
binary.
AF-AGT-001 The agent runner could not be started: node: command not found.
AF-AGT-003 The agent runner produced no readable output: exited with status 1.
```

`af runner check` verifies it can start before you need it, and `af doctor`
includes that check.

## The model

A model reads the page and decides what a person would do next. The key is
yours and it stays on your machine. See
[your own model key](/docs/guides/model-keys) for storing one, proving it
works, pointing it at a local model, and what does and does not leave the
machine when it is used.

With no key the deterministic planner runs instead, which is a supported mode
rather than a broken one: workflows still drive a real browser and still
produce a verdict.

## Recording what the model answered

Asking a model is the only part of a run that is not deterministic: the same page can produce a different plan
twice, so a check that asks a model on every pull request is a check that can
change its answer with nothing in the repository changing. That is what makes a
workflow written as a sentence work, and it is also what makes it worth
pinning.

Recording fixes both that and the bill. Point the runner at a directory and
every prompt and answer is written to it, one readable JSON file per exchange.
Every run afterwards reads from that directory, reaches no network, and costs
nothing.

```sh
# Once, with a key set, to make the recording.
AF_MODEL_CASSETTE=.antifailure/cassette AF_MODEL_CASSETTE_MODE=record af test

# Afterwards, and in CI, with no key at all.
AF_MODEL_CASSETTE=.antifailure/cassette af test
```

| Variable | Default | What it does |
| --- | --- | --- |
| `AF_MODEL_CASSETTE` | unset | The directory of recordings. Unset means the model is asked live. |
| `AF_MODEL_CASSETTE_MODE` | `replay` | `record` asks the model and writes what it answers. `replay` reads only. The default is the one that does not spend money on a schedule. |
| `AF_MODEL_PROVIDER` | `anthropic` | Which provider a replay is filed under, when there is no key to read it from. |
| `AF_MODEL` | the provider's default | Which model, likewise. |

A recording is filed under the whole prompt, which already contains the page's
accessibility snapshot, the workflow, and the history. So a page that changed
is a different key, and a replay that finds nothing **refuses**. It does not
fall back to asking the model, and it does not fall back to the deterministic
planner: the workflow is reported `blocked`, which is a statement about the
recording rather than about your application, and the message says to
re-record.

That refusal is the point. A cassette that quietly reached the network would
spend money nightly and nobody would notice; one that quietly degraded to the
deterministic planner would keep passing while the recording rotted.

## Independent workflows

```yaml
    independent: true
```

By default workflows share an environment and run in order, because a sign-up
usually has to happen before a subscription. `independent: true` says this one
does not depend on the others, which lets it run in parallel.

Related: [workflows](/docs/guides/workflows), [personas](/docs/guides/personas),
[invariants](/docs/guides/invariants).

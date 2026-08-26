---
title: Agents
description: What the agent runner does, and why a workflow is described rather than scripted.
sidebar:
  order: 8
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

## Independent workflows

```yaml
    independent: true
```

By default workflows share an environment and run in order, because a sign-up
usually has to happen before a subscription. `independent: true` says this one
does not depend on the others, which lets it run in parallel.

Related: [workflows](/guides/workflows/), [personas](/guides/personas/),
[invariants](/guides/invariants/).

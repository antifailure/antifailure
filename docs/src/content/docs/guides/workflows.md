---
title: Workflows
description: Writing a description an agent can follow and a verdict can be decided against.
sidebar:
  order: 7
---

A workflow is one thing a user does, described well enough that somebody who
had never seen your product could do it.

```yaml
workflows:
  - name: subscribe
    persona: owner
    start_path: /pricing
    description: >
      Open the pricing page, choose the paid plan, and complete checkout with
      the standard test card. Confirm the account shows the paid plan
      afterwards, not a pending or failed state.
    expect:
      - The account shows the paid plan after checkout completes.
    budget:
      steps: 50
      duration: 8m
    tags: [billing]
```

## Writing a good description

Say what a person is trying to achieve and what they would check. Do not say
which element to click.

Bad, because it breaks when the button moves and passes when the flow breaks:

> Click `#signup-btn`, fill `#email`, click `#submit`.

Good, because it fails when the flow fails:

> Sign up with a fresh email address. Complete every required field and submit.
> You should land on a signed in page, not back on the form with an error.

Name the negative case where there is one. "not back on the form with an error"
is the sentence that turns a vague pass into a real one.

## `expect` decides the verdict

`description` is the task; `expect` is the outcome. Each line is checked
independently, and a workflow with no `expect` can be reported as finished by an
agent that clicked around and achieved nothing.

Expectations can name things outside the browser. "A welcome message arrives in
the inbox" is checked against [the inbox](/guides/inbox/), which is why capture
mode exists.

## Ordering

Workflows share an environment and run in order, because a subscription usually
needs an account. `independent: true` opts one out of that and lets it run in
parallel.

Order the file the way a user meets the product: sign up, then the first useful
thing, then the thing you charge for.

## Budgets

```
AF-AGT-002 Workflow subscribe exhausted its budget of 50 steps before
completing.
```

Either the budget is too small for a long flow, or the flow is genuinely hard
to complete. The run's trace shows which: an agent going in circles looks
different from one making steady progress and running out.

## `start_path`

Where to begin. Defaults to `/`. Worth setting for a workflow that starts deep
in the application, so the agent does not spend its budget navigating to the
starting line.

Related: [agents](/concepts/agents/), [personas](/guides/personas/).

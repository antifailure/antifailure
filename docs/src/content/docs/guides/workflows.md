---
title: Workflows
description: Writing a description an agent can follow and a verdict can be decided against.
sidebar:
  order: 8
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
the inbox" is checked against [the inbox](/docs/guides/inbox), which is why capture
mode exists.

## Quote a sentence the page either shows or does not

An ordinary expectation is a sentence about the product, and it is judged by how
many of its meaningful words appear on the page. Two thirds of them is enough,
because an expectation carries connective words no page repeats and requiring
all of them would mean writing expectations for the matcher instead of for a
person.

That reading is wrong for a page that renders one specific sentence when
something works and a different one when it does not, which is the ordinary case
for a form. Put such a sentence in double quotes and it is required on the page
character for character, up to case and runs of whitespace:

```yaml
expect:
  - '"It is written down."'
```

Two thirds of the words is a low bar on a page with four thousand characters of
prose on it. Our own careers page is the case that earned this: the control
plane's refusal, "Use a public http or https link without credentials", scores
six of its seven words against that page before the form has been touched,
because `public`, `link`, `use`, `credentials` and an install command containing
`https` are all already on it. The expectation was satisfied before the agent
did anything, and the workflow passed in one step over a form it never
submitted.

A quoted expectation that is absent is a FAILURE rather than an unclear result.
A string is on the page or it is not, and there is no third answer to hedge
towards. That is the difference that matters: an unclear result is `unverified`,
and `unverified` exits zero.

## Signing in as more than one person

Most workflows sign in as one persona. A workflow about somebody who holds more
than one session at once names them as a list instead, and the runner signs in
as each in turn, in the same browser, so the cookies accumulate:

```yaml
workflows:
  - name: an-operator-who-is-also-a-customer-can-start-checkout
    personas: [operator-owner, owner]
    start_path: /plan
    description: >
      Sign in to the operator portal, then to the console as the owner, open the
      plan page, and start checkout. Confirm the request reaches the control
      plane rather than being refused by a check meant for operator requests.
    expect:
      - '"Checkout is not available on this control plane."'
```

The last persona named is the one the workflow acts as; the ones before it are
signed in first and kept. `personas` and `persona` are mutually exclusive. This
is for a real product state that a single login cannot reach: an operator who is
also a customer holds a session in each of two independent tables at once, and a
request that carries both is a case a workflow signed in as one identity can
never produce. A persona whose sign-in form is not where the workflow starts
says so with [`sign_in_path`](/docs/guides/personas), which the runner tries
before the usual paths.

## Naming a button

Without a model key the runner presses the controls every application shares:
sign up, continue, subscribe, the button that sends a form it has just filled.
A page with no shared shape, an operator's review queue say, gets nothing
pressed and a run that says so. Name the control in the description by the
label a person reads:

```yaml
    description: >
      Open the application from Preview Applicant, press Mark reviewed, and
      confirm the waiting queue is empty afterwards.
```

A control whose whole visible label appears in the description is pressed once
the shared words have nothing left to offer, in the order the description
mentions them. This is a label, not a selector, and it still says nothing about
when: a control is pressed when it is on the page and not before. The sign-in
vocabulary is never pressed this way, because every description says "sign in"
somewhere and the runner already did.

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

Related: [agents](/docs/concepts/agents), [personas](/docs/guides/personas).

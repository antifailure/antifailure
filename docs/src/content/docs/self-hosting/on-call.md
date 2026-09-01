---
title: On-call
description: What the rotation is, what an acknowledgement means, and what to do first for each class of page, even for a team of one.
sidebar:
  order: 5
---

A rotation of one person is still a rotation. Writing it down changes what
happens at three in the morning: without this page, "who is on call" is
answered from memory, "did anyone see this" is answered by asking around, and
"what do I do first" is answered by reading code while the page is still
buzzing. None of that is available to someone who has just woken up.

## The rotation

One person, holding the pager continuously, until this page names a second
one. That is not a gap to apologize for; it is the honest state of a project
this size, and pretending otherwise with an empty schedule tool would be worse
than saying so plainly.

The moment a second person exists, the rotation is a fixed weekly handoff
rather than anything dynamic: whoever is on call through Sunday hands off
Monday morning, in a message that says explicitly which alerts fired that week
and what is still open, not just "nothing happened". A handoff that only
speaks when something is wrong is a handoff nobody trusts when it says nothing
happened.

## What an acknowledgement means

Acknowledging a page is a promise, not a formality: **I have seen this, I am
looking at it now, stop paging anyone else about it.** It is not "I will look
at it after this meeting" and it is not "I saw the notification go by."

Concretely, an acknowledgement means, within the next few minutes:

- Read [the first thirty seconds](/docs/self-hosting/operations/#the-first-thirty-seconds)
  of the operations page and answer its three questions.
- Say, somewhere a second person could read it, what you found. A one-line
  status is enough: "`/readyz` is failing, looks like the database, digging in."
- Decide whether this is something you can carry alone or something that needs
  the second escalation below, before you are an hour into it and out of
  runway.

An unacknowledged page after the escalation window is treated as a missed page,
full stop. Nobody gets credit for having seen it in their peripheral vision.

## When to wake somebody

Three questions, and any one of them being true is enough on its own.

**Is a customer's data at risk?** A row-level security failure, a masking
failure that let raw data leave the boundary it is supposed to stay inside, a
credential that may have leaked. Wake somebody now, and do not wait for a
second opinion on whether it is bad enough; the two agents who once held
conflicting Terraform state and destroyed a container registry between them
learned that a five-minute pause to double-check would have been cheap and
skipping it was not, described in `docs/plan/prod_guide.md`'s account of the
incident that reshaped how this project applies infrastructure changes.

**Is the whole control plane down, not one organization?** The operations
page's second and third questions tell you which: many error codes in one
organization is that organization's own repository and can wait for morning.
One error code across many organizations, or `/readyz` failing outright, is a
platform fault and does not wait.

**Has `IngestionIsLosingEvents` fired?** Named explicitly because it is the one
alert the operations page marks "act on immediately": an engine that has an
event rejected treats it as delivered and never sends it again, so every
rejected event is gone for good and the environment it described may never
advance in the dashboard again. There is no fail-open behind this one.

Everything else on [the alerts page](/docs/self-hosting/operations/#what-the-alerts-mean)
carries its own judgment call in its own section; read the alert's own
runbook before deciding it can wait, rather than guessing from the name.

## What to do first, by class of page

**The control plane will not answer at all.**
Read [The control plane is down](/docs/self-hosting/operations/#the-control-plane-is-down)
before doing anything else. The single most important fact on that page: `af
up`, `af down`, and every environment already running keep working with no
control plane at all, so this is not the five-alarm fire it feels like at
first. Bring the control plane back; do nothing to the engines, they catch up
on their own.

**A deploy just went out and something looks wrong.**
Check whether the automatic rollback already fired: a failed post-promotion
health gate moves traffic back within the same CD run and the run's summary
says so. If it did not, and the run finished green, the failure showed up after
the gate stopped watching. Follow
[Upgrade and rollback, the manual path](/docs/self-hosting/azure/#upgrade-and-rollback-the-manual-path),
in order, including the migration compatibility check in its fourth step. Do
not skip to "just roll the code back" before reading that step: a migration
that is not backward compatible makes a code rollback the wrong fix, not the
safe default.

**A specific alert fired.**
Its entry under [What the alerts mean](/docs/self-hosting/operations/#what-the-alerts-mean)
is the runbook. Read that section's specific guidance before touching
anything; several of them exist precisely to stop a reasonable-sounding first
instinct that turns out to be wrong for that failure.

**Something feels wrong and no alert has fired.**
Trust it, and start from
[the first thirty seconds](/docs/self-hosting/operations/#the-first-thirty-seconds)
anyway. An alert is a threshold somebody guessed in advance; a person noticing
something first is not a false alarm just because nothing crossed the line yet.

## Collecting evidence before you fix anything

`af support bundle` for one environment, and for the control plane itself, the
steps under
[Collecting evidence before you change anything](/docs/self-hosting/operations/#collecting-evidence-before-you-change-anything).
The state that explains an incident is usually the first thing a fix destroys,
so gather it before you start changing things, not after.

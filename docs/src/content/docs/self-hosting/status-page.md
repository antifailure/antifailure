---
title: Status page
description: The cheapest honest way to tell customers something is wrong, and why the signal has to come from outside the thing it reports on.
sidebar:
  order: 3.5
---

Customers of an availability product ask for a status page before they ask for
almost anything else. The wrong status page is worse than none. A page that
says "all systems operational" during an outage has not failed to inform
anyone. It has actively told them something false, at the exact moment they
are checking because they suspect it is not true.

## The one property that decides the design

**The check has to come from somewhere other than the thing it checks.** A
status page hosted on the control plane's own Container App, reading the
control plane's own `/metrics`, cannot report a total outage of the control
plane. The process that would say "I am down" is the process that is down.
This is not a hypothetical. `/health` answered `200` for thirteen minutes with
no database schema behind it, once, already, in this project. That is
described on the [operations page](/docs/self-hosting/operations/). A status
page built the same way would repeat that failure in front of customers,
instead of in a log nobody reads yet.

The corollary: whatever hosts the check and whatever hosts the page both have
to survive an outage of the thing being watched. They do not need to survive
an outage of *everything*. A status page cannot promise more resilience than
exists to give it.

## What this rules in and out

A synthetic external monitor, checking the public origin from somewhere else,
satisfies the property by construction. Two shapes of it exist.

**A hosted uptime or status page product** is a legitimate answer. Several
have a workable free tier for one monitor. For a team that already uses one
for something else, it is probably the right one: someone else's
infrastructure runs the check and hosts the page. The only work is pointing it
at `https://app.antifailure.dev/readyz` and reading its `ready` field. It does
add a real, if usually small, ongoing dependency, and often a cost once more
than one monitor or one page is needed.

**A scheduled check on infrastructure the project already trusts for
something else** is the other answer, with the page hosted apart from Azure.
This is the one built here. GitHub already holds this repository, runs CI and
CD, and issues this project OIDC credentials. Adding a status probe to it is
not a new vendor. It is the existing one doing one more scheduled thing. The
check runs on GitHub's compute, not Azure's, so an Azure-wide event that took
out the control plane would not also take out the thing reporting on it. Free
at this scale, and it needed nothing this repository did not already have:
`curl`, `jq`, and a place to push a branch.

This project is small enough that the second answer costs less to build than
it costs to evaluate the first. That is why it is what exists today. A team
that already pays for a monitoring product should point it at the same
`/readyz` endpoint instead of adopting this one. The two are not exclusive,
and nothing here assumes this is the only way to watch this system.

## What is built

- `deploy/status/targets.json` names what to check. Today that is staging,
  because production does not exist yet. The same file gets a second entry
  the day production does, and nothing else about this changes.
- `deploy/status/probe.sh` reads it and checks each target's `/readyz`, the
  same endpoint and the same reasoning as
  [`deploy/cd/health-gate.sh`](/docs/self-hosting/azure/#upgrade-and-rollback-the-manual-path).
  `/health` is a static literal that answers even when the database cannot. A
  status page built on it would report an outage as healthy, the same way a
  liveness probe would.
- `deploy/status/render.sh` folds a probe's readings into a bounded history,
  a little over seven days at the five minute interval this runs on, and
  renders a static page from it: current state, when it was last checked, and
  a bar per check for the last day.
- `.github/workflows/status.yml` runs the probe on a schedule and pushes the
  result to a branch named `status-data`, deliberately not `main`. A commit to
  `main` every five minutes would fire `cd.yml`'s staging deploy every five
  minutes. That is a second reason this lives apart from the branch that
  ships code, on top of the first reason: the page's own history should not
  pile up in the commit log of the product it is watching.

## The one manual step

Publishing `status-data` as a URL is a repository setting: **Settings > Pages
> Deploy from a branch > `status-data` / `/ (root)`**. Nothing in this
repository can flip that switch, the same way nothing in `deploy.yml` can set
the static site's publish token. Both are one person's action, once, and both
say so in the workflow that is otherwise ready and waiting. Until it is set,
the workflow still runs. It still writes `status-data`, and the history is
still there to read with `git log` or by cloning that branch. There is simply
no public URL yet.

## What this is not

**It is not the pager.** The alerting stack behind
[the alert rules](/docs/self-hosting/operations/#what-the-alerts-mean) is what
wakes a person. This page is what a customer reads. They watch the same
system from different distances, and neither substitutes for the other. A
fast burn alert can page someone before a single failed check has accumulated
enough history to move the page's five minute bars. The page also has no
opinion about whether one organization's own repository is failing, which is
exactly the distinction the alerts are built to make and this page is not.

**It is not real time.** Five minutes between checks is the cost of running
on a free scheduled trigger rather than a dedicated always-on watcher. It is
an honest five minutes: the page never claims to know about anything more
recent than its last check, and the timestamp on the page says when that was.

**It does not exist for production yet**, because production does not exist
yet. The day it does, it is one line in `targets.json`.

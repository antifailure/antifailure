---
title: Environment lifetime and cost caps
description: How long an environment lives, what removes it, how to keep one you are using, and what happens when a run would cost more than the plan allows.
sidebar:
  order: 7
---

An environment is not free while nobody is looking at it. Each one holds a
database branch, a network, and a container per service, for as long as it
exists. This page is how long that is, what ends it, and how to say "not yet".

## The lifetime

Every environment is created with a stated lifetime, taken from `runtime.ttl`
in the manifest of the repository it belongs to.

```yaml
runtime:
  ttl: 24h
  max_ttl: 168h
```

`ttl` defaults to `24h`. `max_ttl` defaults to `168h` and is the furthest an
environment can ever be extended to.

The lifetime is stamped onto the environment's resources when they are created.
That matters more than it sounds: a sweep reads the expiry off each
environment's own resources, never out of the manifest it happens to be running
with. A machine holding environments from three repositories with three
different lifetimes gets all three right, and running a sweep from a repository
with a two hour lifetime cannot remove somebody else's week long environment.

## Removing what has expired

```sh
af env reap
af env reap --dry-run
```

`af env reap` removes every environment on this machine that has passed its
stated lifetime, and nothing else.

It is not `af env prune`, and the difference is who chose the cutoff. `af env
prune --older-than 48h` takes a cutoff from you and applies it to everything on
the machine, which is the right shape for "this laptop is full". `af env reap`
applies each environment's own lifetime, which is the only shape safe to run
unattended.

Three things it will never remove:

- **An environment that states no lifetime.** Everything created before this
  feature existed carries no expiry. Reading "states no lifetime" as "lifetime
  already over" would turn an upgrade into a machine wipe. Use `af env prune
  --older-than` for those, where you name the cutoff yourself.
- **An environment something is running against.** A sweep takes each
  environment's own lock before removing it, the same lock every other command
  on that environment takes. If `af test` is running, the sweep reports the
  environment as deferred and moves on. The environment is still expired and
  the next sweep takes it, so this is a deferral of one sweep rather than a
  reprieve.
- **Anything that is not an environment**, such as the sidecar image every
  environment on the machine shares.

A deferral is not a failure and does not change the exit code. A teardown that
errored is, because something is then neither removed nor accounted for.

## Keeping one you are using

```sh
af env extend af-app-main-a1b2c3 --for 8h --reason "bisecting a flake"
```

This is the answer to the question a lifetime has to answer before it is a
product rather than a timer: what happens to an environment somebody is in the
middle of using.

Destroying it silently takes away work from a person who did not know the
policy applied. Letting anyone push the expiry back forever means there is no
lifetime at all, only a chore nobody does. So an extension is granted, and it
is bounded.

No extension may take an environment past `runtime.max_ttl`, measured from when
the environment was **created**, not from now. Measuring from now would mean an
environment extended late in its life was entitled to a longer total lifetime
than one extended early, and each extension would carry the limit forward with
it. Measured from creation, twenty extensions and one reach the same ceiling.

Asking for more than the maximum grants the maximum and tells you so, rather
than refusing. Being given less time than you asked for without being told is
how you come back to an environment that is gone.

An extension is local to the machine holding the environment. The sweep that
would have destroyed it runs there and reads the lease there, so the extension
takes effect. What it does not yet do is tell the control plane: the expiry
shown for an environment in the console is the one it was created with, and an
extension does not move it. The environment lives, the console is behind. This
is a known gap rather than a design decision, and it is recorded in
`docs/plan/STATUS.md`.

If an environment genuinely needs longer, raise `runtime.max_ttl` in the
manifest. That is a deliberate, reviewable change to the repository, which is
the right place for a decision about what this project's environments cost.

## Cost caps

The control plane bounds spend in **environment-hours**: one environment, held
for one hour. It is the unit the caps use because it is the only thing here
that is both what actually costs money and what the system already records. A
cap in dollars would need a price list per runtime, per region and per service
size, and a cap that cannot be measured is decoration.

There are two, and they refuse different mistakes.

- **Per run** bounds what a single creation may commit to. An environment asked
  for with a thirty day lifetime is 720 environment-hours promised in one call.
- **Per day** bounds accrual over a rolling twenty four hours. A workflow stuck
  in a loop creating one environment per push stays inside every per-run cap
  and still produces a bill nobody expected. The window rolls rather than
  resetting at midnight, because midnight is the middle of the afternoon for
  somebody, and a runaway that starts at 23:00 should not be handed a fresh
  allowance an hour later.

| Plan | Per run | Per rolling day |
| --- | --- | --- |
| free | 24 hours | 72 hours |
| team | 168 hours | 2,000 hours |
| enterprise | 720 hours | 20,000 hours |

The free per-run cap is exactly the default `runtime.ttl`, so the ordinary case
of one environment for one branch is never refused.

Usage counts the **overlap with the window**, not the whole lifetime. An
environment created three days ago and still running has contributed 24 hours
to a 24 hour window, not 72. The other reading would make one long-lived
environment exceed every daily cap forever. An environment that is still
running counts up to now, so an organization cannot hold a hundred of them and
report nothing.

### When a run is refused

A refusal names the cap, what has been used, and who can change it:

```
This organization has used 71.5 hours of environment time in the last 24
hours, and the free plan allows 72 hours. This run would need another 24
hours. Tear down an environment you are finished with, wait for the window to
move, or ask an owner of this organization to change the plan. Nothing was
created and nothing was removed.
```

All three parts are there on purpose. Without the number nobody knows how far
over they are; without the role nobody knows who to ask; and a refusal that
reads like a failure sends somebody looking for wreckage that is not there.

Reaching a cap refuses the next creation. It never removes anything that
already exists.

## Cost attribution

A bill that says "you used 900 environment-hours" tells nobody which repository
to look at or which branch left something up over a weekend. Usage is recorded
per environment, and each line names the repository, the branch, when it was
created, when it was torn down, and how many runs were made against it, so the
questions people actually ask are answerable from the record.

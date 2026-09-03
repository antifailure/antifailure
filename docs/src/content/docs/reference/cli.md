---
title: Command reference
description: Every command and every flag, generated from the command tree itself.
sidebar:
  order: 1
---

Generated from the command tree, so it cannot fall behind the binary: a flag
added, renamed, or removed changes this page in the same commit, and the build
fails if it does not.

## Global flags

These work on every command.

| Flag | Default | What it does |
| --- | --- | --- |
| `-C`, `--directory` | - | Run as if started in this directory. |
| `--no-color` | `false` | Do not emit colour, regardless of the terminal. |
| `-o`, `--output` | `text` | Output format: text or json. |
| `-q`, `--quiet` | `false` | Print only what was asked for. |
| `-v`, `--verbose` | `false` | Print the underlying cause of an error. |

## Flags on `af` itself

These work on `af` on its own rather than on a command under it.

| Flag | Default | What it does |
| --- | --- | --- |
| `--short` | `false` | With --version, print only the version number. |
| `--version` | `false` | Print the version, commit, and edition. |

## How output adapts

Text output is stable for the same input. There are no timestamps and no
durations in it, so a snapshot test, a diff and two CI logs of the same run
compare cleanly. Timestamps live in `--output json`, where a machine
wants them.

What does vary is layout, and only where there is a terminal to lay anything
out on. Colour, width and the live status line under a long run are decided
once, from the output stream, when the command starts.

| Variable | What it does |
| --- | --- |
| `NO_COLOR` | Any non-empty value turns colour off. It wins over everything, including `AF_FORCE_COLOR`. |
| `AF_FORCE_COLOR` | Any non-empty value turns colour on for a stream that is not a terminal, for a CI system that renders escape codes. |
| `AF_WIDTH` | Lay output out at this many columns rather than measuring the terminal. Clamped to between 40 and 200. |

A stream that is not a terminal, a pipe, a file, or a CI log, is laid out at 80
columns and carries no escape sequences. That is what keeps the output of a
piped run identical from one machine to the next. `TERM=dumb` is
treated the same way.

## Commands

### `af change`

Read the diff and say which checks will exercise what it touched.

What this change touches, and which checks cover it.

Every changed path is classified by a rule that names it, and every check is
reported as selected or not, together with whether the manifest configures it
at all. A check that is selected and unavailable is the line worth reading:
something changed and nothing is going to look at it.

Two things it will not do. It never says a change is safe or risky; it says
which checks exercise which files, and what it cannot see. And a path no rule
recognises selects every check rather than none, because the cost of the two
mistakes is not the same.

In a GitHub Actions job it writes one output per check, so a later step can
skip work this change does not need.

This is the one command that does not need antifailure.yaml. Without one it
still says what the diff touches, and reports every check as unavailable
because nothing is configured to run it.

```
af change [flags]
```

```
# Against the base branch this job names.
af change
# Against a ref you choose, or a diff you already have.
af change --base origin/main
af change --diff pr.patch
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--base` | - | Ref to measure against, defaulting to this job's base branch. |
| `--branch` | - | Branch to read the manifest for, defaulting to the checked out one. |
| `--diff` | - | Read a unified diff from this file instead of asking git. |
| `--head` | - | Ref to measure, defaulting to HEAD. |
| `-w`, `--write` | - | Write the report section here as markdown. |

### `af ci`

Bring an environment up, run everything, write a report, tear it down.

The whole check in one command, for a pull request.

The agents drive the workflows, the invariants are asked of the data, the
migrations are rehearsed against a throwaway branch of the golden, and what the
environment reached for is summarised. Every finding is ranked by the manifest's
policy block, which decides what fails the check and what is only reported.

Teardown happens whatever the outcome, including a failure and including an
interrupt, because an environment that outlives its pull request is the leak
this product exists to prevent. It happens before the report is written, so a
teardown that left something behind is in the report rather than after it.

Only a real finding exits non zero. A blocked run says what was missing and
exits zero, so an incomplete environment is not indistinguishable from a broken
change.

```
af ci [flags]
```

```
# What CI runs: up, migrate, test, load, gate, report, down.
af ci
# --report is Markdown for a person, --report-json is the same run
# for a program.
af ci --report report.md --report-json report.json --keep
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--baseline` | - | Compare queries and plans against a report saved on the base branch. |
| `--branch` | - | Branch to check, defaulting to the checked out one. |
| `--docs` | - | Where documentation links point. |
| `--keep` | `false` | Leave the environment up, for debugging a failure. |
| `--load` | `false` | Generate load as well as running the workflows. |
| `--report` | - | Write the report here as well as to the terminal. |
| `--report-json` | - | Write the same report here as JSON, for a program to read. |
| `--runner` | - | Path to the runner's entry point. |
| `--save-baseline` | - | Save this run's queries and plans, to compare a later branch against. |
| `--timeout` | `30m0s` | Give up after this long. |

### `af doctor`

Check that this machine can run Antifailure, and say how to fix what cannot.

Every check names what to do about a failure. A diagnostic that tells you
something is wrong and stops is worse than no diagnostic, because it costs the
same attention and yields nothing.

```
af doctor
```

```
af doctor
af doctor -o json
```

### `af down`

Remove the environment and everything it created.

Replay the journal in reverse and delete what this environment recorded
creating. Every resource is journaled before it is made, so what teardown
removes is what was actually created rather than what a list somebody
maintained remembers to look for.

Teardown never stops at the first failure. A provider that is unreachable must
not strand the other resources, so each is attempted, and two things survive
the run: anything that could not be removed, and anything this build has no way
to delete, which is left recorded rather than forgotten. Both are named
individually in the output, and exit code 10 means resources are still pending.

So the answer to what this is about to remove is not a sentence here. It is
'af status' for what is running and the pending list this prints for whatever
it could not reach.

```
af down [flags]
```

```
af down
af down --branch feature/checkout
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to tear down, defaulting to the checked out one. |

### `af env`

See and clean up the environments on this machine.

Reads the daemon rather than a registry, because the daemon is the thing that
actually has them. A registry can be wrong; a container either exists or it
does not, and a list that disagrees with reality is worse than no list.

```
af env
```

```
af env list
```

Subcommands:

- [`af env extend`](#af-env-extend) Keep an environment past its lifetime, up to its maximum.
- [`af env list`](#af-env-list) List the environments this machine is holding.
- [`af env prune`](#af-env-prune) Remove environments older than a cutoff.
- [`af env pull`](#af-env-pull) Read an environment's record from the control plane.
- [`af env reap`](#af-env-reap) Remove the environments whose lifetime has ended.

### `af env extend`

Keep an environment past its lifetime, up to its maximum.

Moves an environment's expiry, so a sweep does not take one you are still
using.

There is a bound, and it is the point. No extension may take an environment
past runtime.max_ttl measured from when it was CREATED, not from now, so
extending repeatedly cannot walk the limit forward. Asking for more than the
maximum grants the maximum and says so rather than failing, because being given
less time than you asked for silently is how you come back to an environment
that is gone.

```
af env extend <environment> [flags]
```

```
# The ceiling is measured from when the environment was created, so
# extending twice does not buy twice the time.
af env extend af-orders-feature-checkout-05ca6c --for 2h
af env extend af-orders-feature-checkout-05ca6c --for 2h --reason 'debugging the failing checkout'
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--for` | `4h0m0s` | How long from now the environment should live. |
| `--reason` | - | Why, recorded with the extension. |

### `af env list`

List the environments this machine is holding.

```
af env list
```

```
af env list
af env list -o json
```

### `af env prune`

Remove environments older than a cutoff.

An environment nobody tore down holds a database branch, a network, and a
container per service, and the machine that accumulates a dozen of them is a
machine somebody reboots to fix.

It refuses to remove anything without a cutoff, and prints what it would do
before doing it, because removing somebody's environment while they are looking
at it is the kind of help nobody wants.

```
af env prune [flags]
```

```
# Nothing is removed until you drop --dry-run.
af env prune --dry-run
af env prune --older-than 24h
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--dry-run` | `false` | Print what would be removed without removing it. |
| `--older-than` | `24h0m0s` | Only remove environments older than this. |

### `af env pull`

Read an environment's record from the control plane.

Reads what the control plane holds for one environment: its branch, its state,
its preview URL, and the golden version it was built from.

This never changes anything locally. The control plane is a record of what
happened, not a source of configuration: what an environment does comes from
the manifest in the repository, on the machine the environment is on. A control
plane that could change what an environment runs would be a control plane that
could change what it masks.

Needs a credential. Run af login, or set AF_CONTROL_PLANE_TOKEN to an engine
token, which is what a build machine with nobody sitting at it uses.

```
af env pull <environment> [flags]
```

```
af env pull af-orders-feature-checkout-05ca6c
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to read from (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af env reap`

Remove the environments whose lifetime has ended.

Removes every environment on this machine that has passed the lifetime it was
created with, and nothing else.

The lifetime is read off each environment's own resources, stamped there when
it was created from that repository's runtime.ttl. It is never taken from the
manifest this command was run with, so a repository with a two hour lifetime
cannot remove another project's week long environment on the same machine.

Three things are never removed. An environment whose resources state no
lifetime, which is everything created before this feature existed: use
'af env prune --older-than' for those, where a person names the cutoff. An
environment something is running against, which is deferred to the next sweep
rather than pulled out from under a command. And anything that is not an
environment, such as the shared sidecar image.

An environment you are still using can be kept with 'af env extend'.

```
af env reap [flags]
```

```
# Only environments past the lifetime they were created with, and
# nothing is removed until you drop --dry-run.
af env reap --dry-run
af env reap
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--dry-run` | `false` | Print what would be removed without removing it. |

### `af explain`

Show the effective configuration, with every default filled in.

The most common configuration bug is a default nobody knew about. This prints
the resolved value of every setting, so "why is it blocking that host" has a
one line answer.

```
af explain
```

```
af explain
af explain -o json
```

### `af explore`

Send agents at a goal with no declared workflow.

An exploration is a goal without a script. The agent reads each page through
the accessibility tree, chooses somewhere to go, and writes down every place
the application cost it effort. It answers the question a workflow cannot ask:
nothing broke, so why would somebody give up here.

It cannot fail your build. Nobody declared what should happen on the pages it
wanders onto, so a finding is an observation and never a red mark. Only a run
that could not start is reported as blocked.

Every choice comes from the goal's seed, so the same seed takes the same path
and every finding arrives with the command that replays it.

```
af explore [flags]
```

```
# Agents go at a goal with no workflow written for it.
af explore
af explore --emit-workflow checkout.yaml
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to run against, defaulting to the checked out one. |
| `--emit-workflow` | `false` | Print the workflow block that replays what was explored, instead of the report. |
| `--headed` | `false` | Show the browser rather than running it hidden. |
| `--only` | - | Explore just these goals, by name. |
| `--runner` | - | Path to the runner's entry point. |
| `--seed` | - | Replay with this seed rather than the one the manifest declares. |

### `af fidelity`

What this environment reproduces, component by component, and what it does not.

An inventory of the copy against the thing it is a copy of.

Every line comes from something the engine already knew: the runtime says what
is running, the database provider says which golden the branch came from and
whether its attestation still checks out, the branch says how much it holds and
whether the personas exist in it, and the manifest says which third party hosts
the policy names and what answers for each.

There is a headline number and it is defined on the page it prints: how many of
the measured components are production's own thing rather than a substitution,
a refusal or an absence. What could not be measured is excluded from it and
named, never counted as either a pass or a failure, because a percentage that
quietly absorbs an unknown is worth less than no percentage at all.

The per dimension verdict is the part to read. A change to billing cares about
the third party hosts and not about traffic; a migration cares about the data
and about neither. One averaged number hides whichever of those is yours.

Set fidelity.require in the manifest to fail this command when a dimension is
not fully reproduced.

```
af fidelity [flags]
```

```
# An inventory of the copy against the thing it is a copy of.
af fidelity
af fidelity -o json
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to inventory, defaulting to the checked out one. |

### `af golden`

Manage the masked copies branches are made from.

A refresh copies production, masks it, reads it back to check the masking, and
publishes it only if that check passes.

A golden that fails verification is never published, so it cannot be branched,
so no environment can ever hold it. That is enforced by the provider rather
than by remembering to check.

```
af golden
```

```
af golden list
```

Subcommands:

- [`af golden gc`](#af-golden-gc) Remove old goldens, keeping the newest.
- [`af golden list`](#af-golden-list) List the goldens that exist.
- [`af golden pull`](#af-golden-pull) Bring a published golden onto this machine.
- [`af golden refresh`](#af-golden-refresh) Copy production, mask it, verify it, and publish it.
- [`af golden verify`](#af-golden-verify) Re-check a published golden.

### `af golden gc`

Remove old goldens, keeping the newest.

How many to keep comes from database.golden.retain in the manifest, so that
every machine and every runner collects the same way. --keep overrides it for
one run.

Two versions are never removed. One is any version an environment is still
branched from: taking away the copy something is running on breaks the
environment rather than tidying it, and that refusal comes from the provider,
which is the only thing that knows. The other is the newest verified golden,
whatever the count says, because a project with nothing left to branch cannot
bring an environment up at all, which is worse than the disk it saved.

```
af golden gc [flags]
```

```
af golden gc
af golden gc --keep 3
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |
| `--keep` | `0` | How many of the newest goldens to keep, overriding database.golden.retain. |

### `af golden list`

List the goldens that exist.

```
af golden list [flags]
```

```
af golden list
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |

### `af golden pull`

Bring a published golden onto this machine.

One machine holds the production credential and refreshes; every other machine
pulls what it published and never reads production at all. That is what
database.golden.storage and storage_url are for.

With no version, the newest complete one is taken. A version is complete when
its attestation is in the store: the dump is written first and the attestation
second, so a version with only a dump is a publish that did not finish, and it
is invisible here rather than offered.

A pulled golden is NOT trusted because it came from the store. The verification
scan runs again, here, against the database that actually arrived. A pull that
skipped it would make the store a way to get an unverified database branched.

```
af golden pull [version] [flags]
```

```
af golden pull
af golden pull gv_20260830044013_74234e98
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |

### `af golden refresh`

Copy production, mask it, verify it, and publish it.

```
af golden refresh [flags]
```

```
af golden refresh
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |

### `af golden verify`

Re-check a published golden.

Branches the golden, reads it back with the detectors, and removes the branch
whether or not the check passed.

Worth doing because a golden published under one set of rules is not verified
under another, and because a golden that arrives by import was never checked
here at all.

```
af golden verify <version> [flags]
```

```
af golden verify gv_20260830044013_74234e98
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |

### `af inbox`

Read the mail and messages the environment sent.

Every message a captured provider was asked to send is recorded here instead of
being delivered. Nobody receives anything, and the workflow that was waiting on
it can carry on.

The link and code are extracted for you, because an agent following a magic
link should not have to parse HTML to find it.

```
af inbox
```

```
af inbox list
```

Subcommands:

- [`af inbox get`](#af-inbox-get) Show one message in full.
- [`af inbox list`](#af-inbox-list) List what the environment sent.
- [`af inbox wait`](#af-inbox-wait) Block until a matching message arrives.

### `af inbox get`

Show one message in full.

```
af inbox get <number> [flags]
```

```
af inbox get 1
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to read, defaulting to the checked out one. |

### `af inbox list`

List what the environment sent.

```
af inbox list [flags]
```

```
af inbox list
af inbox list --to ada@example.com --limit 5
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--limit` | `50` | How many messages to show. |
| `--to` | - | Only messages addressed to this recipient. |

### `af inbox wait`

Block until a matching message arrives.

Waits for a message, checking what already arrived first.

That order matters. The message has usually been sent before anybody starts
waiting for it, and a wait that only looks forward is how a test passes on a
slow machine and fails on a fast one.

```
af inbox wait [flags]
```

```
# Blocks until the message arrives, or the timeout runs out.
af inbox wait --to ada@example.com
af inbox wait --subject 'Verify your email' --timeout 60s
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--subject` | - | Wait for a subject containing this text. |
| `--timeout` | `1m0s` | How long to wait. |
| `--to` | - | Wait for a message addressed to this recipient. |

### `af init`

Read the repository and write antifailure.yaml.

Detection reads the repository and proposes a manifest: the services it found,
the port each listens on, the migration command, and, most usefully, a network
policy derived from the SDKs you depend on.

It never runs anything from the repository. Everything it reports names the
file it came from, so you can check the reasoning rather than trust it.

Anything detection is not sure about becomes a question rather than a silent
guess, because a manifest you have to audit is worth less than one you can
read.

A service is identified by the directory it is built and run from, not by its
name, because every source spells the name differently: a Dockerfile and a
language analyzer use the directory, a compose file uses its own key, a
Procfile uses the process name, and a package manifest uses the package. One
application described by several of those is one service, and the name it keeps
comes from the source that identifies an application best, a package manifest
ahead of a compose key ahead of a Procfile process ahead of the directory.
Where one source declares two services in a directory, which is what a compose
file with a web and an admin container on one build context is, they stay two.

A Dockerfile in a subdirectory is built either from that directory, which is
what 'docker build <dir>' does, or from the repository root, which is what a
monorepo image reaching a lockfile at the top of the tree needs. Its COPY lines
say which: a path that exists beside the Dockerfile and not at the root means
the directory, and one that exists only at the root means the root. Where they
do not settle it, this is a question rather than a default, because building
from the wrong one either fails on a missing path or, with COPY . ., succeeds
and produces an image assembled from the wrong directory.

--answer settles a question, and also overrides a value detection read with
confidence, which is how you separate two services a repository really does
have on one port. An id naming nothing is refused with the ids that would have
worked rather than dropped in silence.

```
af init [flags]
```

```
af init
af init --non-interactive --answer database.present=yes
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--answer` | - | Answer a question, or override a detected value, as id=value. Repeatable. |
| `--force` | `false` | Overwrite an existing manifest instead of merging into it. |
| `--non-interactive` | `false` | Do not ask questions; accept every default and report what was assumed. |

### `af insights`

What Postgres can tell you about this change before anybody clicks anything.

A branch is a real database with production's shape in it, which makes some
questions answerable without running the application at all.

The migrations are rehearsed against a throwaway branch and every statement is
timed, so a migration that takes four seconds on an empty test database and
ninety on production row counts is visible before the deploy window rather than
during it. The plans on that branch are compared before and after, which is how
a sequential scan appearing where an index scan was gets found. And the queries
this environment ran are compared against a report saved on the base branch.

Where the migrations take something away, the previous release is built and run
against the migrated branch as well, because a rolling deploy leaves both
releases talking to the same database for the length of the window and nothing
else here checks that. It exits non zero only when a workflow passes without
the migrations and fails with them.

It says what it could not measure, and it names any check the manifest turned
off. A report that silently omits a check reads exactly like a check that found
nothing.

```
af insights [flags]
```

```
# Rehearses the migration against a branch of the golden.
af insights
# Save a report on the base branch, compare against it on this one.
af insights --save baseline.json
af insights --baseline baseline.json
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--against` | - | Which commit the previous release is, overriding the manifest. |
| `--baseline` | - | Compare against a report saved earlier. |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--limit` | `20` | How many queries to show. |
| `--no-rehearsal` | `false` | Skip the migration rehearsal, which is the only check that makes a second branch. |
| `--runner` | - | Path to the runner's entry point. |
| `--save` | - | Save this report to compare against later. |

### `af invariants`

Ask the data the questions the manifest declares.

An invariant is a read only statement that must return no rows, asked of the
branch, so that a flow which appears to succeed while corrupting data is caught
by the data rather than by the screen.

They are asked automatically after the workflows in 'af test' and 'af ci'. This
runs them on their own, which is what you want while writing one, or after a
migration, or when a run failed and you want to know whether the data is the
reason.

Every statement runs inside a transaction opened READ ONLY, so a write is
refused by Postgres rather than trusted not to happen, and each one has its own
timeout. Rows returned means the invariant is violated, and the rows are the
evidence: they are printed, because a check that tells you something is wrong
without telling you which rows has told you to go and do the work yourself.

```
af invariants [flags]
```

```
af invariants
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to ask, defaulting to the checked out one. |

### `af license`

Show the license status of this installation.

This is the community edition. It has no license and needs none.

Everything the engine does is here and stays here: masked environments, sealed
egress, captured mail, agents, load, insights, and teardown. None of it expires
and none of it phones home.

A license adds the enterprise edition, which is a separate binary built from
the ee directory of the same repository: single sign on, SCIM, custom roles and
approvals, SIEM streaming, organization wide policy enforcement, customer owned
runtime clusters, enterprise secret managers, and billing.

```
af license
```

```
af license status
```

Subcommands:

- [`af license install`](#af-license-install) Install an enterprise license key.
- [`af license remove`](#af-license-remove) Remove the installed license key.
- [`af license status`](#af-license-status) What this installation is licensed for.

### `af license install`

Install an enterprise license key.

```
af license install <key>
```

```
af license install AF-LICENSE-KEY
```

### `af license remove`

Remove the installed license key.

```
af license remove
```

```
af license remove
```

### `af license status`

What this installation is licensed for.

```
af license status
```

```
af license status
```

### `af load`

Send traffic shaped like production's at the environment.

A weighted mix rather than one endpoint at a fixed rate. Hammering one endpoint
proves that endpoint is fast, which nobody doubted; what breaks under real
traffic is the mix, and the page nobody thinks about that is nine percent of
requests.

Every route is treated as unsafe until the manifest names it safe. A generator
that finds POST /checkout in an access log and exercises it four hundred times
is a generator that charges four hundred cards.

```
af load
```

```
af load smoke
```

Subcommands:

- [`af load run`](#af-load-run) Run the full load profile.
- [`af load scenario`](#af-load-scenario) Run the declared journeys against the environment.
- [`af load smoke`](#af-load-smoke) Send a short burst, to check the environment answers under any load at all.

### `af load run`

Run the full load profile.

```
af load run [flags]
```

```
# A weighted mix, not one endpoint at a fixed rate.
af load run
af load run --duration 60s --scale 2
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to send at, defaulting to the checked out one. |
| `--duration` | `1m0s` | How long to send for. |
| `--scale` | `1` | Multiplier on production's rate. |
| `--seed` | `1` | Makes two runs send the same sequence. |

### `af load scenario`

Run the declared journeys against the environment.

A scenario is an ordered journey rather than a mix: open the billing page, ask
for the subscription, submit, and submit again three hundred milliseconds later
because the first one felt slow. Sessions walk it at once, and one scenario can
start after another so a burst arrives while something else is already running.

The requests are HTTP. Clicking a button is 'af test' and the browser agents;
this is what the load generator can send, at the concurrency load runs at.

Every step is checked against load.safe_routes before anything is sent, so a
scenario that names an undeclared route is blocked rather than run.

```
af load scenario [flags]
```

```
af load scenario
af load scenario --only checkout --concurrency 20
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to send at, defaulting to the checked out one. |
| `--concurrency` | `20` | Ceiling on requests in flight. |
| `--only` | - | Run just these scenarios, by name. |
| `--seed` | `1` | Makes two runs send the same schedule. |

### `af load smoke`

Send a short burst, to check the environment answers under any load at all.

```
af load smoke [flags]
```

```
af load smoke
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to send at, defaulting to the checked out one. |
| `--duration` | `10s` | How long to send for. |
| `--scale` | `0.1` | Multiplier on production's rate. |
| `--seed` | `1` | Makes two runs send the same sequence. |

### `af login`

Sign in to a control plane from this terminal.

Signs this machine in to a control plane using the device authorization grant.

af login prints a short code and opens a browser. Approve it there, and the
token arrives here over TLS and goes straight into the operating system's
credential store. The credential is never shown, never copied through a
clipboard, and never written to a shell history file.

By default the token can read environments and runs and write events, and
nothing else: it cannot manage members, change policy, or touch a provider key.

--scope asks for more. The scope is shown on the screen where the login is
approved, so nobody grants a capability without seeing the words:

  af login --scope providers.write

Nothing reads a key back. There is no scope for it, because storing a secret and
retrieving one are different capabilities and a terminal needs only the first.

Run af logout to remove it from this machine and revoke it everywhere.

```
af login [flags]
```

```
af login
af login --control-plane https://app.antifailure.dev --no-browser
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to sign in to (default: AF_CONTROL_PLANE_URL, or the hosted instance). |
| `--no-browser` | `false` | Do not try to open a browser; print the address instead. |
| `--scope` | - | Ask for a capability beyond the default, e.g. providers.write. Repeatable. |

### `af logout`

Remove this machine's credential and revoke it.

Removes the stored token and tells the control plane to revoke it.

Both halves matter. Removing it locally stops this machine using it; revoking
it stops anybody who copied it. A logout that only deleted the local copy would
leave a working credential in whatever backup or screen recording captured it.

If the control plane cannot be reached, the local credential is still removed
and the command says the revocation did not happen, so nobody is left believing
a token is dead when it is not.

```
af logout [flags]
```

```
af logout
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to sign out of (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af logs`

Show what the environment's services have written.

Output from every service, or from one if you name it.

Everything here goes through the redactor on the way out. A service's own log
is the second likeliest place for a secret to surface after a build log, and
this is the command people paste into issues.

```
af logs [service] [flags]
```

```
af logs
af logs web --tail 100
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--tail` | `200` | How many lines to show per service. |

### `af mask`

Plan, apply, and check the masking of this environment's data.

Masking is compiled from the live schema rather than from a list, because a
list of columns goes stale the moment somebody adds one and the failure mode is
silent: the new column holds real addresses and nothing says so.

A column no rule covers is reported rather than left alone. Left alone, for a
column called customer_notes, means the notes ship.

```
af mask
```

```
af mask plan
```

Subcommands:

- [`af mask apply`](#af-mask-apply) Rewrite this environment's data according to the plan.
- [`af mask plan`](#af-mask-plan) Show what masking would do, column by column.
- [`af mask preview`](#af-mask-preview) Show what a few rows would look like after masking.
- [`af mask verify`](#af-mask-verify) Read the data back and report anything that still looks real.

### `af mask apply`

Rewrite this environment's data according to the plan.

Applies the plan to the branch this environment is using.

This is irreversible: once a column is overwritten the original is gone. It is
safe here because the branch is a copy, and it is exactly how a golden is
produced, so trying it on a branch first is the way to iterate on rules.

```
af mask apply [flags]
```

```
# Rewrites this environment's data in place.
af mask apply
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to mask, defaulting to the checked out one. |

### `af mask plan`

Show what masking would do, column by column.

```
af mask plan [flags]
```

```
af mask plan
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to plan against, defaulting to the checked out one. |

### `af mask preview`

Show what a few rows would look like after masking.

Reads a few rows, transforms them in memory, and writes nothing.

Somebody iterating on rules has to see the output before committing to it, and
the alternative, applying and then looking, is irreversible on a branch they may
want to keep.

```
af mask preview [flags]
```

```
af mask preview
af mask preview --table users --rows 5
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--rows` | `3` | How many rows to show. |
| `--table` | - | Preview one table, defaulting to the first being masked. |

### `af mask verify`

Read the data back and report anything that still looks real.

Reads a sample of every text column and runs the same detectors that would find
the data if it leaked.

Masking that is not checked is masking somebody believes in. A rule that missed
a column, a transform that failed on a null, a table added last week: each
produces data that looks masked and is not, and none of them announces itself.

```
af mask verify [flags]
```

```
af mask verify
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to check, defaulting to the checked out one. |

### `af mcp`

Serve the rehearsal tools to a model over the Model Context Protocol.

Serve this repository's rehearsal tools to an MCP client on standard input and
output.

The agent on the other end chooses what to rehearse. It does not choose how
safely the rehearsal runs: there is no argument on any tool that can disable
sanitization, widen the egress policy, lower a threshold or name a database.
Thresholds come from this project's manifest, and the verdict is decided by the
same evaluator af ci uses, so a tool call and a pull request check cannot
disagree about the same change.

The server serves exactly this checkout. A tool call may state which project it
believes it is talking to, and a call naming a different one is refused rather
than followed.

Standard output carries the protocol and nothing else. Progress, warnings and
errors go to standard error, where the client's log will show them.

A client configured by JSON runs af with ["-C", "/absolute/path", "mcp"],
because most of them have nowhere to set a working directory.
https://antifailure.dev/docs/reference/mcp has the entry for each client, and
says why there is no hosted endpoint to point one at.

```
af mcp
```

```
# Started by an MCP client, not typed. It speaks the protocol on
# standard input and output, so running it in a terminal looks idle.
af mcp
# It serves exactly the checkout it starts in, so the client is
# configured to run it there, or to pass -C with an absolute path
# when it has nowhere to set a working directory.
af mcp
```

### `af model`

The model key the agents use on this machine.

The agents can read a page and decide what a person would do next, which takes a
model. The key is yours: it is stored on this machine, the call goes straight to
the provider, and nothing hosted is involved.

With no key the deterministic planner runs instead. That is a supported mode,
not a broken one: workflows still run, still drive a real browser and still
produce a verdict. The model is what turns a workflow written as a sentence into
one the runner follows without being told every field.

  af model show     what is configured, and what a run will use
  af model test     prove the key works, with one cheap call
  af model set      store a key, without it touching the command line
  af model rm       remove a stored key

If you have a control plane, 'af provider' is the better place for a key: it
seals it, caps what may be spent on it per month, and checks that cap before the
key is ever decrypted. This command is the one that needs nothing but a terminal.

```
af model
```

```
af model show
```

Subcommands:

- [`af model rm`](#af-model-rm) Remove a stored key.
- [`af model set`](#af-model-set) Store a key, without it touching the command line.
- [`af model show`](#af-model-show) What is configured, and what a run will use.
- [`af model test`](#af-model-test) Prove the key works, with one cheap call.

### `af model rm`

Remove a stored key.

Removes the key from every place this command can write it, not from the first
one that answers. A key left in the encrypted store after the keyring entry was
removed is a key the next run silently uses, which is the exact failure somebody
is trying to prevent when they type this.

It cannot remove a key from a shell you exported it in or from a .env file, and
it says so when one is still there rather than reporting a removal that changed
nothing.

Removing a key that is not there is not an error. This is a command people run
in a hurry, and a retry after a timeout must not report failure for reaching the
state you asked for.

This does not reach the provider. If the key leaked, revoke it at Anthropic or
OpenAI as well: removing it here stops this machine using it and stops nobody
else.

```
af model rm <provider>
```

```
af model rm anthropic
```

### `af model set`

Store a key, without it touching the command line.

Stores a key in the system keyring where this platform has one, and in the
encrypted local store where it does not.

The key is never an argument. There is no --key flag, deliberately: a secret on
a command line is written to your shell's history file, is visible in ps to
every other user on the machine, and is captured by any recording of the
terminal. So there are three ways to give it, and none of them put it in the
argument vector:

  af model set anthropic                      asks, without echoing
  af model set anthropic --stdin < key.txt    reads one line
  af model set anthropic --from-env NAME      reads that environment variable

Where it lands is reported rather than assumed, because the two places are not
equivalent. macOS gates the keychain on the login keychain, Linux on the session
keyring daemon, and Windows on the user's credentials. The encrypted local store
is a file, and it is only as strong as the passphrase protecting it.

This does not reach the provider. Storing a key here does not create one and
removing it does not revoke one.

```
af model set <provider> [flags]
```

```
# The key is read from the environment or from stdin, so it never
# reaches the command line or the shell history.
af model set anthropic --from-env ANTHROPIC_API_KEY
af model set anthropic --stdin < key.txt
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--from-env` | - | Read the key from this environment variable instead of asking. |
| `--stdin` | `false` | Read the key from standard input, one line. |

### `af model show`

What is configured, and what a run will use.

Reports the provider, the model, the endpoint, where the key was found and when
it was last proven to work.

It does not show the key and there is no flag that would. The fingerprint
answers the question this is usually asked to answer, which is whether the key
here is the one you think it is, and it answers it without either person having
to read a secret out loud.

"Where it came from" is worth as much as the rest together. A key exported in
one shell and a key in the keyring look identical from a run's point of view
until they disagree, and then the only useful sentence is which one won.

```
af model show
```

```
af model show
af model show -o json
```

### `af model test`

Prove the key works, with one cheap call.

Sends one completion of a single token and reports what came back.

A real call rather than a check of the key's shape, because a well formed key
that was revoked this morning passes every shape check there is. It costs a
fraction of a cent, which is the point: this is meant to be run whenever you are
unsure, and a check people avoid because of the price is a check nobody runs.

What it can tell apart matters more than that it runs. A revoked key, an empty
balance, a model name that does not exist, a throttle, a provider outage and an
endpoint nothing answers on all fail, they all have different fixes, and being
told only that the call failed sends you to the wrong one first.

On success it writes down that this exact key worked, and 'af model show'
reports it. Rotating the key discards that, because a previous key's success
says nothing about the new one.

```
af model test [flags]
```

```
# One cheap call, so a broken key is found here and not mid run.
af model test
af model test --timeout 10s
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--timeout` | `30s` | How long to wait for the endpoint, which a local model may need more of. |

### `af net`

Inspect and explain the environment's network policy.

An environment reaches nothing on the network except the hosts in the manifest,
each in the mode named there. These commands say what that adds up to, without
needing an environment to be running.

```
af net
```

```
af net policy
```

Subcommands:

- [`af net explain`](#af-net-explain) Say what would happen to one request, and which rule decides it.
- [`af net log`](#af-net-log) Show what the environment tried to reach, and what happened.
- [`af net policy`](#af-net-policy) Show the effective policy, in the order that decides.

### `af net explain`

Say what would happen to one request, and which rule decides it.

Prints the decision, the rule that made it, and every other rule that also
matched, so a surprising answer is diagnosable rather than mysterious.

```
af net explain <method> <url>
```

```
af net explain GET https://api.stripe.com/v1/charges
af net explain POST https://api.resend.com/emails
```

### `af net log`

Show what the environment tried to reach, and what happened.

Every outbound request the environment made, allowed or refused, with the rule
that decided it.

The allowed ones are the point. A log of refusals answers "why was this
blocked"; a log of everything answers "did anything reach Stripe", which is the
question somebody asks after an incident.

```
af net log [flags]
```

```
af net log
af net log --blocked --limit 20
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--blocked` | `false` | Show only requests that were refused. |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--limit` | `200` | How many decisions to show, most recent last. |

### `af net policy`

Show the effective policy, in the order that decides.

Rules are printed most specific first, which is the order they are evaluated
in. An exact host beats a wildcard, a longer path beats a shorter one, and an
explicit method beats any, so where a rule sits in this list is where it sits
in the decision, no matter where it sits in the file.

```
af net policy
```

```
af net policy
```

### `af oracle`

Run this change beside the version it is replacing and diff what they did.

Brings a second environment up from a baseline revision, branches the same
golden for both so they start from identical rows, sends both the same requests
in the same order, and reports every difference in what came back and in what
ended up in the database.

Responses and database contents are compared. Events, outbound effects, traces
and query plans are not: two comparisons done completely are worth more than six
done shallowly, because the first one that cries wolf is the last one anybody
looks at.

Values that no two runs can agree on are normalised before they are compared:
two timestamps within an hour, two UUIDs, two numbers within a relative
tolerance. Everything the comparison declined to look at is printed, defaults
included, because an oracle that silently ignores a field is worse than one that
reports it.

The candidate environment is left running whether or not this command brought it
up. The baseline is torn down unless --keep says otherwise.

```
af oracle [flags]
```

```
# Runs this change beside the version it replaces and diffs both.
af oracle
af oracle --baseline origin/main --fail-on any
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--baseline` | - | Revision to compare against, overriding oracle.base_ref. |
| `--branch` | - | Branch to compare, defaulting to the checked out one. |
| `--fail-on` | - | Lowest severity that fails the command: none, minor, major, or critical. |
| `--keep` | `false` | Leave the baseline environment up, for looking at a difference. |
| `--report` | - | Write the report here as well as to the terminal. |

### `af provider`

Your own model provider keys and their monthly caps.

Stores your Anthropic and OpenAI keys on the control plane, sealed with a secret
that is not in its database, and caps what may be spent on each one per month.

Runs use your key. We never see it after you save it: what any screen or any
command here can read is the last four characters and a fingerprint.

These commands need a token that asked for the capability:

  af login --scope providers.write

A token from a plain af login cannot reach a key, which is deliberate. The scope
appears on the screen where the login is approved, so nobody grants this without
seeing the words.

```
af provider
```

```
af provider list
```

Subcommands:

- [`af provider budget`](#af-provider-budget) Cap what may be spent on a provider this month.
- [`af provider list`](#af-provider-list) What is stored, and what it may spend this month.
- [`af provider rm`](#af-provider-rm) Remove a stored key.
- [`af provider set`](#af-provider-set) Store or rotate a key, without it touching the command line.

### `af provider budget`

Cap what may be spent on a provider this month.

Sets the monthly cap in US dollars. The cap is checked BEFORE the key is
decrypted, so a run with no allowance never causes the key to exist in the
control plane's memory at all. That ordering is the difference between a cap and
a suggestion.

A provider with no cap cannot spend anything. A missing cap reads as zero rather
than as unlimited, because the alternative on somebody else's key is an
unbounded bill.

A cap of zero is allowed and means exactly that: spend nothing on this provider.

```
af provider budget <provider> <usd> [flags]
```

```
af provider budget anthropic 50
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af provider list`

What is stored, and what it may spend this month.

Shows which providers have a key, the last four characters of each, and the
monthly cap against what has been spent.

It does not show a key, and there is no flag that would. The last four and the
fingerprint are enough to answer the question this is usually asked to answer:
whether the key here is the one you think it is.

```
af provider list [flags]
```

```
af provider list
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af provider rm`

Remove a stored key.

Removes the stored key. Runs that need this provider are refused afterwards,
with a message saying why, rather than falling back to a key of ours.

This does not reach the provider. If the key leaked, revoke it there as well:
removing it here stops us using it and stops nobody else.

Removing a key that is not there is not an error. This is the command somebody
runs in a hurry, and a retry after a timeout must not report failure for
reaching the state they asked for.

```
af provider rm <provider> [flags]
```

```
af provider rm anthropic
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af provider set`

Store or rotate a key, without it touching the command line.

Stores a key for anthropic or openai, replacing whatever was there.

The key is never an argument. There is no --key flag, deliberately: a secret on
a command line is in the shell's history file, is visible in ps to everybody
else on the machine, and is in any recording of the terminal. So there are three
ways to give it, and none of them put it in the argument vector: it is asked
for without echoing, read as one line from stdin, or read from an environment
variable this process already has.

Rotating stores the new key and revokes the old one together. If the key given
is the one already stored, that is reported rather than accepted quietly: it is
the mistake people make at the moment they believe they have replaced a leaked
key.

```
af provider set <provider> [flags]
```

```
# The key is read from the environment or stdin, never from a flag,
# so it does not land in shell history.
af provider set anthropic
af provider set anthropic --stdin
af provider set anthropic --from-env ANTHROPIC_API_KEY
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |
| `--from-env` | - | Read the key from this environment variable instead of asking. |
| `--stdin` | `false` | Read the key from standard input, one line. |

### `af runner`

Install and check the agent runner.

The runner drives a real browser, so it is a separate program in a separate
language. It is installed from a copy that ships with this engine rather than
downloaded, because the source a release was tested with is the source that
release should run.

```
af runner
```

```
af runner check
```

Subcommands:

- [`af runner check`](#af-runner-check) Say whether the runner can run.
- [`af runner install`](#af-runner-install) Put the runner where af test will find it.

### `af runner check`

Say whether the runner can run.

Reports each thing af test needs from the runner separately: the source, the
dependencies it declares, a node new enough to run it, and the browser.

It does not claim the runner executes. Knowing that means starting node and
launching a browser, which is what af test is. Anything this cannot determine
is reported as not checked rather than as ok, because a check that answers ok
about something it never examined is worse than one that admits the gap: this
command used to report "ok runner" whenever src/main.ts existed, which was true
of an install with no dependencies at all, and the real failure surfaced much
later inside af test as a node error about a module it could not resolve.

```
af runner check
```

```
af runner check
```

### `af runner install`

Put the runner where af test will find it.

```
af runner install [flags]
```

```
af runner install
af runner install --skip-browser
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--from` | - | Copy from this directory rather than the one beside the engine. |
| `--skip-browser` | `false` | Do not download the browser. |

### `af secret`

Store values in the encrypted local store.

The last place the engine looks for a declared variable, after this shell's
environment and after .env.

The file is encrypted with a key derived from a passphrase, and it lives under
.antifailure, which 'af init' adds to .gitignore. It is a convenience for a
workstation and it is not a secret manager for a team: a value here is as safe
as the passphrase and the disk it is on.

Set AF_SECRET_PASSPHRASE before using it. There is deliberately no default:
a store encrypted with a passphrase everybody knows is a store that only looks
encrypted.

```
af secret
```

```
af secret list
```

Subcommands:

- [`af secret list`](#af-secret-list) List the names in the store.
- [`af secret rm`](#af-secret-rm) Remove a value from the store.
- [`af secret set`](#af-secret-set) Store a value, read without echo.

### `af secret list`

List the names in the store.

Names only. There is no command that prints a stored value: a store that can
print its contents is one screenshot away from not being a store.

```
af secret list
```

```
af secret list
```

### `af secret rm`

Remove a value from the store.

```
af secret rm <name>
```

```
af secret rm STRIPE_SECRET_KEY
```

### `af secret set`

Store a value, read without echo.

Reads the value from the terminal without echoing it, or from stdin when there
is no terminal.

It is never taken as an argument. An argument is in the shell history, in the
process list, and in the CI log of whatever ran it.

```
af secret set <name> [flags]
```

```
# Prompts for the value, or reads it from stdin. Never a flag.
af secret set STRIPE_SECRET_KEY
af secret set STRIPE_SECRET_KEY --stdin
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--stdin` | `false` | Read the value from stdin rather than prompting. |

### `af start`

Say where you are on the first run, and what to run next.

The first run is a sequence, and a sequence can be interrupted. This reports
each step of it as observed on this machine right now, and names the one command
that moves you forward.

It runs nothing and writes nothing. Every answer comes from the machine rather
than from a record of what this command last did, so closing the laptop,
switching branches, or tearing an environment down by hand all move the answer
with you.

A step that cannot be answered without side effects is reported as not checked,
with the reason and the command that does answer them. That is the point rather
than a gap: a step reported as fine because nothing looked at it is how a green
run over nothing happens.

Exit 0 means every step is either done or simply not reached yet, which is the
normal state of a first run in progress. Exit 3 means a step is broken and the
next command cannot work until it is fixed.

```
af start
```

```
# Where you are on the first run, and the one command that moves you on.
af start
af start -o json
```

### `af status`

Show what is running for this branch.

```
af status [flags]
```

```
af status
af status -o json
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to report on, defaulting to the checked out one. |

### `af support`

Collect a redacted diagnostic bundle.

```
af support
```

```
af support bundle
```

Subcommands:

- [`af support bundle`](#af-support-bundle) Write logs, decisions, the manifest, and doctor output, redacted.

### `af support bundle`

Write logs, decisions, the manifest, and doctor output, redacted.

Everything in the bundle goes through the redactor on the way in, and the
bundle lists exactly what it included so you can see what you are about to
send before you send it.

A bundle you have to trust is a bundle nobody sends, and a report nobody sends
is a bug nobody fixes.

```
af support bundle [flags]
```

```
# Redacted on the way in, with a list of what it included.
af support bundle
af support bundle --archive af-support.zip
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--archive` | - | Where to write the bundle. |
| `--branch` | - | Branch to collect, defaulting to the checked out one. |

### `af test`

Run the manifest's workflows against the environment.

Agents drive the application the way a person does, through the accessibility
tree, and return a verdict with a video, a trace, and steps to reproduce it.

Five verdicts, not two. The one that matters is blocked: a browser that
crashed, a page that never loaded, or a persona with no password is not
evidence about the application, and charging it to the application is how
people learn to ignore the results. Only a real failure exits non zero.

```
af test [flags]
```

```
af test
af test --only checkout --headed
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--attempts` | `2` | How many times to try a workflow before deciding. |
| `--branch` | - | Branch to run against, defaulting to the checked out one. |
| `--headed` | `false` | Show the browser rather than running it hidden. |
| `--only` | - | Run just these workflows, by name. |
| `--runner` | - | Path to the runner's entry point. |

### `af token`

Engine tokens, which is what CI and a self-hosted engine present.

An engine token is what goes in AF_CONTROL_PLANE_TOKEN. It belongs to the
organization rather than to you, so it keeps working after you leave, and it
carries no identity: it can send events and read an environment back, and it
cannot reach a key, a member, or another token.

These commands need a token that asked for the capability:

  af login --scope tokens.manage

A token from a plain af login cannot mint one, which is deliberate. A credential
that can make more credentials is a credential worth stealing twice.

```
af token
```

```
af token list
```

Subcommands:

- [`af token create`](#af-token-create) Mint an engine token and show it once.
- [`af token list`](#af-token-list) What engine tokens exist, and when each was last used.
- [`af token rm`](#af-token-rm) Revoke an engine token.

### `af token create`

Mint an engine token and show it once.

Mints a token and prints it. Only its hash is stored, so this is the one and
only time it can be read: there is no command and no screen that will show it
again. If you lose it, mint another and revoke this one.

The name is a label you will read in a list months from now, so name it after
where it is going rather than after today.

```
af token create <name> [flags]
```

```
# Shown once, at creation. There is no command that prints it again.
af token create ci
af token create ci --control-plane https://app.antifailure.dev
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af token list`

What engine tokens exist, and when each was last used.

Shows every engine token, revoked ones included. A revoked one is shown rather
than hidden, because the question this is usually asked is whether the token
that stopped working is the one you revoked.

It does not show a token and there is no flag that would. The prefix is what
tells two of them apart, and it is what af token rm accepts.

```
af token list [flags]
```

```
af token list
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af token rm`

Revoke an engine token.

Revokes a token immediately. Anything presenting it stops being accepted on the
next request rather than at the end of a cache window.

Takes the prefix af token list shows, or the full id. Running it twice is not an
error: the second run says it was already revoked, because during an incident
the same command gets run twice and the second must not read as a new problem.

```
af token rm <id or prefix> [flags]
```

```
af token rm afe_1a2b3c4d
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af up`

Create an environment for the current branch.

Build every service, branch the database from its masked golden, seal the
network, and bring the environment up.

The environment is created under a lock for this branch, so two invocations
cannot fight over it, and every resource is journaled before it is made, so an
interrupt at any point leaves something af down can clean up.

```
af up [flags]
```

```
af up
af up --rebuild --hud
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to create the environment for, defaulting to the checked out one. |
| `--hud` | `false` | Watch the run on a live dashboard, or a line per event where there is no terminal. |
| `--rebuild` | `false` | Build every image again, even when an identical one exists. |

### `af version`

Print the version, commit, and edition.

```
af version [flags]
```

```
af version
af version --short
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--short` | `false` | Print only the version number. |

### `af webhook`

Send the inbound events a flow is waiting on.

Sends a provider's callback into the environment, signed the way that provider
signs it.

The signature is the point. An application that verifies signatures, which is
every application that should, will reject an unsigned event, and a simulator
that cannot get past the application's own verification simulates nothing.

```
af webhook
```

```
af webhook list
```

Subcommands:

- [`af webhook list`](#af-webhook-list) List the providers and events that can be sent.
- [`af webhook trigger`](#af-webhook-trigger) Send one signed event into the environment.

### `af webhook list`

List the providers and events that can be sent.

```
af webhook list
```

```
af webhook list
af webhook list stripe
```

### `af webhook trigger`

Send one signed event into the environment.

The path is taken from the manifest's webhook_path for that provider unless
--path says otherwise, and the signing secret from the same variable the
application reads, so both sides agree without anybody configuring twice.

```
af webhook trigger <provider> <event> [flags]
```

```
af webhook trigger stripe checkout.session.completed
af webhook trigger stripe invoice.paid --set id=in_123 --set amount_paid=4900
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to deliver to, defaulting to the checked out one. |
| `--path` | - | Path to deliver to, defaulting to the manifest's webhook_path. |
| `--secret` | - | Signing secret, defaulting to the provider's variable in this shell. |
| `--service` | - | Service to deliver to, defaulting to the first reachable one. |
| `--set` | - | Set a field on the event payload, as key=value. |

### `af whoami`

Who this machine is signed in as.

Asks the control plane who the stored token belongs to.

It asks rather than reading the stored copy, because the stored copy is what
this machine believed at login time and the control plane is what is true now.
A token whose membership has been removed still looks perfectly good on disk,
and reporting it would tell somebody they have access they do not have.

--offline reports the stored copy without a network call, and says so.

```
af whoami [flags]
```

```
af whoami
af whoami --offline
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to ask (default: AF_CONTROL_PLANE_URL, or the hosted instance). |
| `--offline` | `false` | Report the stored credential without asking the control plane. |


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

## Commands

### `af ci`

Bring an environment up, run everything, write a report, tear it down.

The whole check in one command, for a pull request.

Teardown happens whatever the outcome, including a failure and including an
interrupt, because an environment that outlives its pull request is the leak
this product exists to prevent.

Only a real failure exits non zero. A blocked run says what was missing and
exits zero, so an incomplete environment is not indistinguishable from a broken
change.

```
af ci [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to check, defaulting to the checked out one. |
| `--docs` | - | Where documentation links point. |
| `--keep` | `false` | Leave the environment up, for debugging a failure. |
| `--load` | `false` | Generate load as well as running the workflows. |
| `-o`, `--output` | - | Write the report here as well as to the terminal. |
| `--runner` | - | Path to the runner's entry point. |
| `--timeout` | `30m0s` | Give up after this long. |

### `af doctor`

Check that this machine can run Antifailure, and say how to fix what cannot.

Every check names what to do about a failure. A diagnostic that tells you
something is wrong and stops is worse than no diagnostic, because it costs the
same attention and yields nothing.

```
af doctor
```

### `af down`

Remove the environment and everything it created.

Replay the journal in reverse and delete every resource the environment
created: database branches, containers, volumes, and networks.

Teardown never stops at the first failure. A provider that is unreachable must
not strand the other resources, so each is attempted and anything that could
not be removed stays recorded for the next run. Exit code 10 means resources
are still pending.

```
af down [flags]
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

Subcommands:

- [`af env list`](#af-env-list) List the environments this machine is holding.
- [`af env prune`](#af-env-prune) Remove environments older than a cutoff.
- [`af env pull`](#af-env-pull) Read an environment's record from the control plane.

### `af env list`

List the environments this machine is holding.

```
af env list
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

Needs a token in AF_CONTROL_PLANE_TOKEN. Create one in the control plane under
engine tokens.

```
af env pull <environment> [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to read from (default: AF_CONTROL_PLANE_URL, or the hosted instance). |

### `af explain`

Show the effective configuration, with every default filled in.

The most common configuration bug is a default nobody knew about. This prints
the resolved value of every setting, so "why is it blocking that host" has a
one line answer.

```
af explain
```

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

Subcommands:

- [`af golden gc`](#af-golden-gc) Remove old goldens, keeping the newest.
- [`af golden list`](#af-golden-list) List the goldens that exist.
- [`af golden refresh`](#af-golden-refresh) Copy production, mask it, verify it, and publish it.
- [`af golden verify`](#af-golden-verify) Re-check a published golden.

### `af golden gc`

Remove old goldens, keeping the newest.

A golden that something is still branched from is never removed, and that is
reported rather than forced: taking away the copy an environment is running on
breaks the environment rather than tidying it.

```
af golden gc [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |
| `--keep` | `3` | How many of the newest goldens to keep. |

### `af golden list`

List the goldens that exist.

```
af golden list [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch context to use, defaulting to the checked out one. |

### `af golden refresh`

Copy production, mask it, verify it, and publish it.

```
af golden refresh [flags]
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

Subcommands:

- [`af inbox get`](#af-inbox-get) Show one message in full.
- [`af inbox list`](#af-inbox-list) List what the environment sent.
- [`af inbox wait`](#af-inbox-wait) Block until a matching message arrives.

### `af inbox get`

Show one message in full.

```
af inbox get <number> [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to read, defaulting to the checked out one. |

### `af inbox list`

List what the environment sent.

```
af inbox list [flags]
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

```
af init [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--answer` | - | Answer a question without a prompt, as id=value. Repeatable. |
| `--force` | `false` | Overwrite an existing manifest instead of merging into it. |
| `--non-interactive` | `false` | Do not ask questions; accept every default and report what was assumed. |

### `af insights`

Show what the database noticed while the environment ran.

The bugs this looks for are the ones no test catches, because the test passes:
the endpoint that now runs four hundred queries instead of two, the index that
stopped being used, the sequential scan on a table that grew. Each is correct
and slow, and correct and slow is what takes a site down under load rather than
in review.

  af insights --save baseline.json     on main
  af insights --baseline baseline.json on the branch

It says what it could not measure. pg_stat_statements is an extension somebody
has to install, and an insight that silently reports nothing because it is
missing looks exactly like a clean bill of health.

```
af insights [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--baseline` | - | Compare against a report saved earlier. |
| `--branch` | - | Branch to read, defaulting to the checked out one. |
| `--limit` | `20` | How many queries to show. |
| `--save` | - | Save this report to compare against later. |

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

Subcommands:

- [`af license install`](#af-license-install) Install an enterprise license key.
- [`af license remove`](#af-license-remove) Remove the installed license key.
- [`af license status`](#af-license-status) What this installation is licensed for.

### `af license install`

Install an enterprise license key.

```
af license install <key>
```

### `af license remove`

Remove the installed license key.

```
af license remove
```

### `af license status`

What this installation is licensed for.

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

Subcommands:

- [`af load run`](#af-load-run) Run the full load profile.
- [`af load smoke`](#af-load-smoke) Send a short burst, to check the environment answers under any load at all.

### `af load run`

Run the full load profile.

```
af load run [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to send at, defaulting to the checked out one. |
| `--duration` | `1m0s` | How long to send for. |
| `--scale` | `1` | Multiplier on production's rate. |
| `--seed` | `1` | Makes two runs send the same sequence. |

### `af load smoke`

Send a short burst, to check the environment answers under any load at all.

```
af load smoke [flags]
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

The token is scoped to reading environments and runs and writing events. It
cannot manage members, change policy, or read a provider key, and asking for
more is refused by the control plane rather than granted quietly.

Run af logout to remove it from this machine and revoke it everywhere.

```
af login [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to sign in to (default: AF_CONTROL_PLANE_URL, or the hosted instance). |
| `--no-browser` | `false` | Do not try to open a browser; print the address instead. |

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

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to mask, defaulting to the checked out one. |

### `af mask plan`

Show what masking would do, column by column.

```
af mask plan [flags]
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

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to check, defaulting to the checked out one. |

### `af net`

Inspect and explain the environment's network policy.

An environment reaches nothing on the network except the hosts in the manifest,
each in the mode named there. These commands say what that adds up to, without
needing an environment to be running.

```
af net
```

Subcommands:

- [`af net explain`](#af-net-explain) Say what would happen to one request, and which rule decides it.
- [`af net log`](#af-net-log) Show what the environment tried to reach, and what happened.
- [`af net policy`](#af-net-policy) Show the effective policy, in the order that decides.

### `af net explain`

Say what would happen to one request, and which rule decides it.

Prints the decision, the rule that made it, and every other rule that also
matched, so a surprising answer is diagnosable rather than mysterious.

  af net explain GET https://api.stripe.com/v1/charges
  af net explain POST https://api.resend.com/emails

```
af net explain <method> <url>
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

### `af runner`

Install and check the agent runner.

The runner drives a real browser, so it is a separate program in a separate
language. It is installed from a copy that ships with this engine rather than
downloaded, because the source a release was tested with is the source that
release should run.

```
af runner
```

Subcommands:

- [`af runner check`](#af-runner-check) Say whether the runner can run.
- [`af runner install`](#af-runner-install) Put the runner where af test will find it.

### `af runner check`

Say whether the runner can run.

```
af runner check
```

### `af runner install`

Put the runner where af test will find it.

```
af runner install [flags]
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

### `af secret rm`

Remove a value from the store.

```
af secret rm <name>
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

| Flag | Default | What it does |
| --- | --- | --- |
| `--stdin` | `false` | Read the value from stdin rather than prompting. |

### `af status`

Show what is running for this branch.

```
af status [flags]
```

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to report on, defaulting to the checked out one. |

### `af support`

Collect a redacted diagnostic bundle.

```
af support
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

| Flag | Default | What it does |
| --- | --- | --- |
| `--branch` | - | Branch to collect, defaulting to the checked out one. |
| `-o`, `--output` | - | Where to write the bundle. |

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

| Flag | Default | What it does |
| --- | --- | --- |
| `--attempts` | `2` | How many times to try a workflow before deciding. |
| `--branch` | - | Branch to run against, defaulting to the checked out one. |
| `--headed` | `false` | Show the browser rather than running it hidden. |
| `--only` | - | Run just these workflows, by name. |
| `--runner` | - | Path to the runner's entry point. |

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

Subcommands:

- [`af webhook list`](#af-webhook-list) List the providers and events that can be sent.
- [`af webhook trigger`](#af-webhook-trigger) Send one signed event into the environment.

### `af webhook list`

List the providers and events that can be sent.

```
af webhook list
```

### `af webhook trigger`

Send one signed event into the environment.

af webhook trigger stripe checkout.session.completed
  af webhook trigger stripe invoice.paid --set id=in_123 --set amount_paid=4900

The path is taken from the manifest's webhook_path for that provider unless
--path says otherwise, and the signing secret from the same variable the
application reads, so both sides agree without anybody configuring twice.

```
af webhook trigger <provider> <event> [flags]
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

| Flag | Default | What it does |
| --- | --- | --- |
| `--control-plane` | - | The control plane to ask (default: AF_CONTROL_PLANE_URL, or the hosted instance). |
| `--offline` | `false` | Report the stored credential without asking the control plane. |


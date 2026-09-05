---
title: Quickstart
description: From an empty machine to a working environment, and what each command actually did.
sidebar:
  order: 1
---

This goes from nothing to a running environment on your own machine. It needs
Docker and a Postgres connection string you are allowed to read from. It does
not need an account, a control plane, or a cloud provider: everything here runs
locally, and the hosted pieces are optional and come later.

## Install

```bash
curl -fsSL https://antifailure.dev/install.sh | sh
```

The installer downloads the release for your platform, checks it against the
published checksum, and puts `af` and its runner under `~/.antifailure`. It is
POSIX `sh` rather than bash, so it works in an Alpine container as well as on a
laptop. If you would rather read it before running it, it is the same file
served at that URL, and the [source is in the repository](https://github.com/antifailure/antifailure/blob/main/install.sh).

### What it does to your PATH

`~/.antifailure/bin` is on nobody's PATH by default, so the installer puts it
there. It appends one line to the file your login shell reads at startup,
prints that line, and names the file:

```
Added this to ~/.zshrc, so every new terminal finds af:

  export PATH="$HOME/.antifailure/bin:$PATH"
```

Delete that line to undo it. zsh gets `.zshrc` under `ZDOTDIR`, bash gets
`.bash_profile` on macOS and `.bashrc` on Linux, fish gets `fish_add_path` in
`config.fish`, and a shell the installer does not recognise is told so rather
than having a file guessed for it. Running the installer again does not add the
line a second time.

The terminal you ran the installer in cannot see a file written a second ago,
so the installer ends with one line to paste that fixes that shell and runs the
first command:

```bash
export PATH="$HOME/.antifailure/bin:$PATH" && af start
```

To manage PATH yourself, decline in advance. Nothing is written, and the
installer prints the full path to `af` instead of commands that would not
resolve:

```bash
curl -fsSL https://antifailure.dev/install.sh | AF_NO_MODIFY_PATH=1 sh
```

In GitHub Actions no profile is touched at all: the installer writes to
`GITHUB_PATH`, which is how a step extends the PATH of the steps after it, so
`af` resolves in every later step of the job.

## Find out where you are

```bash
af start
```

This is the one command worth remembering, and it is the only one on this page
you can run at any point. It reports every step below as observed on this
machine right now, and names the single next command.

```
Your first run
  ok    af on your PATH              ~/.antifailure/bin/af
  ok    Docker                       version 28.5.1, linux containers
  ...   the agent runner             runner: no runner at ~/.antifailure/runner
  ...   a manifest                   no antifailure.yaml here or in any parent directory
  skip  the database source          after the manifest
  skip  a golden                     after the manifest
  skip  an environment               after the manifest
  skip  workflows to run             after the manifest
  ok    a model key                  none set, so agents use the deterministic planner
  skip  evidence on disk             after the manifest
  ok    nothing left behind          none are being held

Next

    af runner install
```

It runs nothing and writes nothing, so running it costs you a second and
changes nothing. Every answer comes from the machine rather than from a record
of what it last did, which is why it is still right after you close the laptop,
switch branches, or tear an environment down by hand.

Four states, and it never collapses one into another. `ok` was observed to be
finished. `...` was observed not to be, and is where you are. `fail` is
something broken that has to be fixed before the next command can work. `skip`
is a step it deliberately did not look at, and it says why and what to run
instead: whether a golden exists is one of those, because listing goldens takes
this branch's lock and a status command that took locks could not be run while
`af up` was in flight.

Exit 0 means every step is either done or not reached yet, which is the normal
state of a first run in progress. Exit 3 means something is broken.

## Check the machine

```bash
af doctor
```

`af start` reports Docker because it is the one thing nothing below can work
without. `af doctor` is the wider check: disk, ports, DNS, outbound reachability,
kernel isolation, proxy settings, git, and the environments this machine is
still holding. Every problem it names carries what to do about it, and every one
of them is a problem you would otherwise meet halfway through a run.

It also validates the manifest when one exists and compares a stable CLI version
with the latest published GitHub release, with a three second network timeout.
An outdated version or invalid manifest fails the check. No network, a development
build, or no manifest is reported explicitly, never as a successful check of
something it could not inspect. An absent manifest is normal before initialization;
it does not mean the machine is broken.

```bash
af update
```

This downloads the latest stable release for this platform, verifies its published
checksum, and replaces the installed binary and its bundled runner source. The old
binary stays in place until the replacement is ready. Shell profiles and project
files are left alone. If a package manager owns the binary, upgrade through that
manager instead. Enterprise binaries use their enterprise distribution, not the
public community release. Afterwards, run `af runner install` to refresh the installed
runner and `af doctor` to check the installation. To see the latest release without
changing files:

```bash
af update --check
```

## Install the agent runner

```bash
af runner install
```

The runner drives a real browser, so it is a separate program in a separate
language and it needs node 22.6 or newer. It is copied from the source that
ships beside `af` rather than downloaded, because the source a release was
tested with is the source that release should run, and its dependencies are
installed from the lockfile that ships with it, so two people installing one
release get one tree. It then downloads chromium, which is the slow part.

```bash
af runner check
```

reports each thing separately: the source, every dependency the runner declares
against what is actually under `node_modules`, whether the lockfile pinned them,
node against the range the runner requires, and the browser. It does not claim
the runner executes, because knowing that means starting node and launching a
browser, which is what `af test` is. Anything it cannot determine it reports as
not checked rather than as ok.

A failed browser download is not fatal. The runner is usable the moment a
browser arrives, and until then a workflow that needs a page read comes back
`unverified` rather than guessed at.

Then finish the agent runner, which is the third step the installer prints:

```bash
af runner install
```

The runner drives a real browser, so it needs Node and a copy of Chromium that
the install script deliberately does not download for you. It reports what it
copied and what it fetched, and once it says it is ready, `af test` finds it
without a flag. Everything up to `af up` works without it; only `af test` needs
it.

## Describe the repository

```bash
af init
```

Detection reads the repository and writes `antifailure.yaml`: the services it
found, the port each listens on, the migration command, and a network policy
derived from the SDKs in your dependency list. If your `package.json` has
`stripe` in it, the Stripe hosts arrive in the manifest without being asked.

Two things about this worth knowing, because they are deliberate:

It never executes anything from the repository. Detection reads files. A
repository that would like to run a script during setup does not get to.

Anything it is unsure about becomes a question rather than a silent guess, and
everything it reports names the file it came from. You can answer the questions
without a prompt if you are scripting it:

```bash
af init --non-interactive
```

That accepts every default and prints what it assumed, which is the honest
version of a silent run.

Read the manifest before going further. It is meant to be audited rather than
trusted, and the [manifest reference](/docs/reference/manifest) explains every
key.

## Look at what would happen

```bash
af explain
```

This resolves the manifest and prints the plan: which golden a branch would come
from, what each service would build from, and the mode every host in the network
policy has been given. Nothing is created. It is the cheapest way to find out
that a setting does not mean what you assumed.

## Bring an environment up

```bash
af up
```

That builds the services, creates a masked branch of the golden, and starts
everything inside a network namespace that reaches nothing except the hosts your
policy allows. The first run is the slow one, because the golden has to be built
and masked before anything can branch from it. Later runs branch from what
already exists.

While it runs, or afterwards:

```bash
af status
af logs
```

## Run the workflows

```bash
af test
```

Agents drive the application the way a person does, through the accessibility
tree, and return one of five verdicts for each workflow in the manifest with a
video, a trace, and steps to reproduce it.

Five verdicts rather than two, and the one that matters is `blocked`. A browser
that crashed, a page that never loaded, or a persona with no password is not
evidence about your application, and charging it to your application is how
people learn to ignore the results. Only a real failure exits non zero.

```
  ok    sign in                      pass in 4.1s
  ok    place an order               pass in 11.7s

  2 passed, 0 failed, 0 flaky, 0 blocked, 0 unverified, in 16s
```

A manifest that declares no workflows is refused rather than reported as a run
that examined nothing. `af start` says so before `af up`, so you find out in a
second rather than after a build.

### The evidence

Everything a run produced is under `.antifailure/artifacts/<environment>` in
the repository: a video and a Playwright trace per workflow, screenshots, the
console log, and the list of requests the page could not make, which is usually
the egress policy doing its job. `af start` reports whether anything is there.

### A model key is optional

```bash
af model show
```

Nothing above needs one. With no key the agents plan deterministically, the
workflows still run, and the verdicts are real. A key lets an agent read a page
it has not seen before, and where one is set it is reported by fingerprint, from
which source, and whether it has been checked.

```bash
af model set anthropic
```

reads the key without echo and puts it in your operating system's keyring. It is
never passed on a command line, never written to the manifest, and there is no
command that prints it back.

## Prove the containment

The interesting property is not that the environment came up. It is that it
cannot reach anything you did not name.

```bash
af net policy
```

prints the decision for every host the policy knows, and

```bash
af net explain https://api.stripe.com/v1/charges
```

answers for one specific request: which rule matched, which mode it is in, and
what would happen. If something reached the network unexpectedly,
`af net log` has the record of it, including the denials.

The modes are covered in [egress](/docs/concepts/egress). The short version is
that `BLOCK` refuses with a decision you can read, `SANDBOX` swaps in test
credentials and trips a wire if a live key ever appears, `CAPTURE` records mail
and messages into an inbox your tests can read, and `MOCK` answers from an
offline pack with no network at all.

## Tear it down

```bash
af down
```

Everything it created is removed, and the removal is checked rather than
assumed. If a previous run was killed halfway, the journal reconciles it: see
[the journal](/docs/concepts/journal) for why that matters and
`af env prune` for sweeping up after a machine that lost power.

## What to read next

[Goldens](/docs/concepts/goldens) and [masking](/docs/concepts/masking) are the
two ideas everything else rests on: how a masked copy of production is built
once and branched cheaply, and how identifiers are replaced deterministically so
the same customer is the same fake customer in every table and every refresh.

[Verification](/docs/concepts/verification) explains why an unverified golden
cannot be branched at all, which is enforced in code rather than in a checklist.

[Building services](/docs/guides/build) covers what happens when detection
guessed wrong about how your services are built, which is the most common reason
a first `af up` does not go cleanly.

[Watching a run](/docs/guides/dashboard) is the live view: `af up --hud` draws
the same run as a dashboard, and where there is no terminal it writes one line
per event instead.

## Running it somewhere other than your laptop

Everything above is the same wherever the engine runs, and there are two other
places to run it.

[An environment per pull request](/docs/getting-started/pull-requests) is
Antifailure inside GitHub Actions: the same `af up`, in a workflow, with one
comment on the pull request that is updated in place rather than appended to.
Nothing else is needed, and in particular no server. It is the next page in
this section, and [GitHub](/docs/guides/github) is the reference behind it:
the two modes, what the App must be granted, forks, and teardown.

[The control plane](/docs/self-hosting/control-plane) is the optional hosted
piece, and the page opens by saying what still works without it, which is all
of it. Read that one when you want environments that outlive a workflow run, a
shared address for them, or a record across repositories.

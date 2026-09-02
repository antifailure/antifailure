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
export PATH="$HOME/.antifailure/bin:$PATH" && af doctor
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

Check the machine has what the engine needs:

```bash
af doctor
```

`af doctor` reports what it found and what it could not, by name. It is worth
running first because every problem it names here is one you would otherwise
meet halfway through a run.

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

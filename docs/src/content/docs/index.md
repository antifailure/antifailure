---
title: Antifailure
description: A disposable copy of your production stack for every pull request, and what to read first.
template: splash
hero:
  tagline: >-
    Staging drifts, seed data lies, and the bug you ship is the one no fixture
    predicted. Antifailure gives every branch its own environment built from the
    shape of production.
  actions:
    - text: Quickstart
      link: /docs/getting-started/quickstart
      icon: right-arrow
    - text: Read the manifest reference
      link: /docs/reference/manifest
      variant: minimal
---

## What it is

An environment is a masked branch of your production database, your services
built and running, and a network that reaches nothing except the hosts you
named. Agents drive your real workflows against it and return verdicts with
evidence. Then it is destroyed, and the destruction is proved rather than
assumed.

```bash
curl -fsSL https://antifailure.dev/install.sh | sh
af init          # reads your repo, writes antifailure.yaml
af up            # masked database branch, built services, sealed network
af test          # agents run your workflows and return verdicts with evidence
af down          # every resource it created, gone
```

The installer puts `af` under `~/.antifailure` and puts that on your PATH by
appending one line to the startup file your login shell reads, printing the
line and naming the file. [Quickstart](/docs/getting-started/quickstart) has
the detail, including how to decline it.

## Where to start

If you have not run it yet, read [Quickstart](/docs/getting-started/quickstart).
It goes from an empty machine to a working environment, and says what each
command actually did.

If you arrived from an error message, the code in that message has its own page.
The [error reference](/docs/reference/errors) lists every code the engine can
return, what causes it, and what to do next.

If you are deciding whether this fits your stack, read
[goldens](/docs/concepts/goldens) and [masking](/docs/concepts/masking) first.
They are the two ideas the rest depends on, and they are where the guarantees
live.

## The parts

| Read this | To understand |
| --- | --- |
| [Goldens](/docs/concepts/goldens) | How a masked copy of production is built once and branched cheaply |
| [Masking](/docs/concepts/masking) | How identifiers are replaced, deterministically, and how that is verified |
| [Verification](/docs/concepts/verification) | Why an unverified golden cannot be branched |
| [Egress](/docs/concepts/egress) | What an environment can reach, and the mode each host is given |
| [Agents](/docs/concepts/agents) | How workflows written as sentences become a run with evidence |
| [The journal](/docs/concepts/journal) | How a killed engine reconciles instead of leaking |

## Reference

Three of the four reference pages are checked against the thing they document,
so they cannot drift: the [command reference](/docs/reference/cli) against the
command tree, the [error reference](/docs/reference/errors) against the
catalogue, and the [transform reference](/docs/reference/transforms) against
the registry. A build gate fails if any of those stops matching.

The [manifest reference](/docs/reference/manifest) is written by hand and no
gate compares it to `schemas/manifest.v1.json`. The generated rendering of the
schema is the [manifest schema page](/docs/reference/schemas/manifest-v1), and
that is the one to trust where the two disagree.

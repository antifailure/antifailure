---
title: Antifailure documentation
description: A disposable copy of your production stack for every pull request, and what to read first.
---

Antifailure gives a branch its own environment: a masked copy of your production
database, your services built and running, and a network that reaches nothing
you did not name. Agents drive your real workflows against it and return
verdicts with evidence. Then it is destroyed, and the destruction is proved
rather than assumed.

Everything here runs on your own machine first. The hosted pieces are optional
and come later.

<div class="af-home-lead not-content">
  <a class="af-home-card" href="/docs/getting-started/quickstart">
    <span class="af-home-card-kicker">Start here</span>
    <span class="af-home-card-title">Quickstart</span>
    <span class="af-home-card-body">From an empty machine to a running environment, and what each command
    actually did. Needs Docker and a Postgres connection string. No account.</span>
  </a>
  <a class="af-home-card" href="/docs/getting-started/pull-requests">
    <span class="af-home-card-kicker">Then</span>
    <span class="af-home-card-title">An environment per pull request</span>
    <span class="af-home-card-body">The same run inside GitHub Actions, with one comment on the pull request
    that is edited in place. Two commands and no server.</span>
  </a>
  <a class="af-home-card" href="/docs/getting-started/hosted">
    <span class="af-home-card-kicker">When you need it</span>
    <span class="af-home-card-title">When one machine is not enough</span>
    <span class="af-home-card-body">What a control plane adds, why nothing depends on it, and the shortest
    path to running one.</span>
  </a>
</div>

## Install it

```bash
curl -fsSL https://antifailure.dev/install.sh | sh
af init          # reads your repo, writes antifailure.yaml
af up            # masked database branch, built services, sealed network
af test          # agents run your workflows and return verdicts with evidence
af down          # every resource it created, gone
```

The installer puts `af` under `~/.antifailure` and puts that on your PATH by
appending one line to the startup file your login shell reads, printing the line
and naming the file. [Quickstart](/docs/getting-started/quickstart) has the
detail, including how to decline it.

## The ideas the rest depends on

These pages carry the guarantees. Everything else is a consequence of them.

<div class="af-home-grid not-content">
  <a class="af-home-row" href="/docs/concepts/goldens">
    <span class="af-home-row-title">Goldens</span>
    <span class="af-home-row-body">How a masked copy of production is built once and branched cheaply.</span>
  </a>
  <a class="af-home-row" href="/docs/concepts/masking">
    <span class="af-home-row-title">Masking</span>
    <span class="af-home-row-body">How identifiers are replaced, deterministically, and how that is proved.</span>
  </a>
  <a class="af-home-row" href="/docs/concepts/verification">
    <span class="af-home-row-title">Verification</span>
    <span class="af-home-row-body">Why an unverified golden cannot be branched, enforced in code.</span>
  </a>
  <a class="af-home-row" href="/docs/concepts/egress">
    <span class="af-home-row-title">Egress</span>
    <span class="af-home-row-body">What an environment can reach, and the mode each host is given.</span>
  </a>
  <a class="af-home-row" href="/docs/concepts/agents">
    <span class="af-home-row-title">Agents</span>
    <span class="af-home-row-body">How a workflow written as a sentence becomes a run with evidence.</span>
  </a>
  <a class="af-home-row" href="/docs/concepts/journal">
    <span class="af-home-row-title">The journal</span>
    <span class="af-home-row-body">How a killed engine reconciles instead of leaking.</span>
  </a>
</div>

## Look something up

If you arrived from an error message, the code in it has its own page. The
[error reference](/docs/reference/errors) lists every code the engine can
return, what causes it, and what to do next.

Three of the four reference pages are checked against the thing they document,
so they cannot drift: the [command reference](/docs/reference/cli) against the
command tree, the [error reference](/docs/reference/errors) against the
catalogue, and the [transform reference](/docs/reference/transforms) against the
registry. A build gate fails if any of those stops matching.

The [manifest reference](/docs/reference/manifest) is written by hand and no
gate compares it to `schemas/manifest.v1.json`. The generated rendering of the
schema is the [manifest schema page](/docs/reference/schemas/manifest-v1), and
that is the one to trust where the two disagree.

The rest of the documentation is in the sidebar: guides for a stack or a task,
database providers, security, self-hosting, and the enterprise edition.

## Hand it to an agent

Every page on this site is available as plain text, and the whole of it is one
file.

<div class="af-home-agent not-content">
  <a class="af-home-agent-link" href="/docs/llms-full.txt">
    <span class="af-home-agent-title">The whole documentation, as one file</span>
    <span class="af-home-agent-body">Every page here as plain text, in the order the sidebar reads. Paste the
    address into an assistant, or fetch it.</span>
    <code>https://antifailure.dev/docs/llms-full.txt</code>
  </a>
  <a class="af-home-agent-link" href="/llms.txt">
    <span class="af-home-agent-title">The index, for a crawler</span>
    <span class="af-home-agent-body">What this product is and where each part of the site lives, in the
    <code>llms.txt</code> convention.</span>
    <code>https://antifailure.dev/llms.txt</code>
  </a>
</div>

One page on its own works the same way: add `.md` to any documentation address,
or use the copy control in the bar at the top of every page. The address of this
page as Markdown is [`/docs/index.md`](/docs/index.md).

<style>
  /* The documentation home.
   *
   * This page used to carry `template: splash`, which is Starlight's marketing
   * template and turns the sidebar OFF. The one page a new reader lands on was
   * the only page in the set with no way to see what else exists, and below
   * 800px it had no menu button either, so on a phone it had no navigation at
   * all. It also rendered its `title` frontmatter, the single word
   * "Antifailure", as a display headline that told the reader nothing.
   *
   * Hairlines and space rather than shadowed cards, one accent, and the type
   * scale the rest of the documentation already uses. It has to look like the
   * page after it, not like a landing page bolted to the front. */

  .af-home-lead {
    display: grid;
    gap: 1px;
    margin-block: 2.5rem 3rem;
    background: var(--af-rule);
    border-block: 1px solid var(--af-rule);
  }

  @media (min-width: 60rem) {
    .af-home-lead {
      grid-template-columns: repeat(3, 1fr);
      border: 1px solid var(--af-rule);
    }
  }

  .af-home-card {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 1.25rem 1.25rem 1.4rem;
    background: var(--af-surface);
    text-decoration: none;
    color: inherit;
  }

  .af-home-card:hover {
    background: color-mix(in srgb, var(--af-rule) 30%, var(--af-surface));
  }

  .af-home-card-kicker {
    font-size: 0.6875rem;
    font-weight: 500;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--color-gray-new-40);
  }

  .af-home-card-title {
    font-size: 1.125rem;
    font-weight: 500;
    letter-spacing: -0.02em;
    color: var(--af-accent-ink);
  }

  .af-home-card-body {
    font-size: 0.9375rem;
    line-height: 1.6;
    color: var(--color-gray-new-40);
  }

  .af-home-grid {
    display: grid;
    gap: 0;
    margin-block: 1.5rem 2rem;
    border-top: 1px solid var(--af-rule);
  }

  .af-home-row {
    display: grid;
    gap: 0.15rem 1.5rem;
    padding: 0.9rem 0;
    border-bottom: 1px solid var(--af-rule);
    text-decoration: none;
    color: inherit;
  }

  @media (min-width: 40rem) {
    .af-home-row {
      grid-template-columns: 11rem 1fr;
      align-items: baseline;
    }
  }

  .af-home-row-title {
    font-weight: 500;
    color: var(--af-accent-ink);
  }

  .af-home-row-body {
    font-size: 0.9375rem;
    line-height: 1.6;
    color: var(--color-gray-new-40);
  }

  .af-home-agent {
    display: grid;
    gap: 1rem;
    margin-block: 1.5rem 2rem;
  }

  @media (min-width: 50rem) {
    .af-home-agent {
      grid-template-columns: repeat(2, 1fr);
    }
  }

  .af-home-agent-link {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 1.1rem 1.25rem;
    border: 1px solid var(--af-rule);
    border-radius: var(--af-radius-md);
    text-decoration: none;
    color: inherit;
  }

  .af-home-agent-link:hover {
    border-color: var(--color-gray-new-60);
  }

  .af-home-agent-title {
    font-weight: 500;
    color: var(--af-accent-ink);
  }

  .af-home-agent-body {
    font-size: 0.9375rem;
    line-height: 1.6;
    color: var(--color-gray-new-40);
  }

  .af-home-agent-link code {
    font-family: var(--sl-font-mono);
    font-size: 0.8125rem;
    color: var(--color-gray-new-40);
    overflow-wrap: anywhere;
  }

  .af-home-agent-body code {
    font-size: 0.875em;
  }

  /* Every interactive row here is a link and the ring has to be visible on a
   * white ground, so it is the same neon ring the rest of the site uses rather
   * than each row's own colour. */
  .af-home-card:focus-visible,
  .af-home-row:focus-visible,
  .af-home-agent-link:focus-visible {
    outline: 2px solid var(--color-neon);
    outline-offset: 2px;
  }
</style>

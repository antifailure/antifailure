<h1 align="center">Antifailure</h1>

<p align="center">
  <strong>A disposable copy of your production stack for every pull request.</strong><br>
  Masked Postgres branches, contained third-party APIs, agents that use the app like people, and load shaped like your real traffic.
</p>

<p align="center">
  <a href="https://antifailure.dev">Website</a> &middot;
  <a href="https://antifailure.dev/docs">Documentation</a> &middot;
  <a href="https://antifailure.dev/docs/getting-started/quickstart">Quickstart</a> &middot;
  <a href="docs/plan/STATUS.md">Status</a> &middot;
  <a href="CONTRIBUTING.md">Contributing</a> &middot;
  <a href="SECURITY.md">Security</a>
</p>

<p align="center">
  <a href="https://github.com/antifailure/antifailure/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/antifailure/antifailure/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-black"></a>
  <a href="https://antifailure.dev/docs"><img alt="Documentation" src="https://img.shields.io/badge/docs-antifailure.dev-33bf00"></a>
  <img alt="Status: pre-1.0" src="https://img.shields.io/badge/status-pre--1.0-orange">
</p>

---

Staging drifts, seed data lies, and the bug you ship is the one no fixture
predicted. Antifailure gives every branch its own environment built from the
shape of production: the real schema and real data volume with every
identifier masked and the masking proved, your services built and running in a
sandbox that cannot reach the internet except where you say it can, and inbound
webhooks simulated so flows actually finish.

```bash
curl -fsSL https://antifailure.dev/install.sh | sh
af init          # reads your repo, writes antifailure.yaml
af up            # masked database branch, built services, sealed network
af test          # agents run your workflows and return verdicts with evidence
af down          # every resource it created, gone
```

## What it does

**Masked data, verified.** Masking is compiled to SQL and executed in
resumable chunks, deterministic so the same customer maps to the same fake
customer across every table and every refresh. Then a scanner reads back every
column of every table looking for anything that still parses as an email, a
card, a phone number, or a key, and signs an attestation. An unverified golden
cannot be branched. That is enforced in code, not in a checklist.

**A network you control.** Every environment gets a sidecar that owns its
network namespace. Nothing leaves except through it. Each host gets a mode:
`BLOCK` refuses with a decision you can read, `ALLOW` lets it through with a
rate limit, `SANDBOX` swaps in test credentials and trips a wire if a live key
ever appears, `CAPTURE` records the email or SMS into a searchable inbox your
agents can read, and `MOCK` answers from a stateful offline pack. The Stripe
pack is complete enough to run checkout, subscribe, renew, and cancel with
signed webhooks and no network at all.

**Agents, not scripts.** Workflows are written as sentences. The runner drives
a real browser through the accessibility tree, logs in the way a person does
(password, magic link from the captured inbox, one-time code, TOTP), and
returns `pass`, `fail`, `flaky`, `blocked`, or `unverified` with a video, a
trace, and reproduction steps. A failure caused by the runner is classified as
such and never counted against your application.

**Database review, automatically.** Pending migrations are rehearsed on a
fresh branch with per-statement timing and the strongest lock held per table.
`pg_stat_statements` is diffed between main and your branch to catch the N+1
you just introduced. Query plans are compared to catch the index you stopped
using.

**Nothing outlives its environment.** Every resource is journaled before it is
created and compensated on teardown, so a crash at any instant is recoverable
by replay. The leak detector inventories every provider and fails the build if
anything untracked exists.

## Where it runs

Locally on Docker, in GitHub Actions, or on your own Kubernetes. The database
comes from Docker, Neon, Supabase, or DBLab thin clones in front of any
Postgres, including RDS, Cloud SQL, and Azure Database. Providers are an
interface with a published conformance suite, so adding one is a package, not
a fork.

## How this differs from the things it sits next to

Most of these are not competitors. Several are dependencies. The distinction
worth drawing is what each one leaves undone.

| Instead of | What it gives you | What is still missing |
| --- | --- | --- |
| A shared staging environment | One long-lived place to try things | It drifts from production, its data is invented, and everyone queues for it. The bug you ship is the one no fixture predicted. |
| Preview environments from a PaaS | Your app, built and running, per branch | The database is a seed file. Nothing about the data has production's shape, size, or distribution, so a migration that is instant there can hold a lock for minutes in production. |
| Postgres branching on its own | A real copy of real data, quickly | The data is still production data. Something has to mask it, prove the masking, and stop an unverified copy from being used. And the branch alone does not build your services or contain their egress. |
| A masking or synthetic-data tool | Safe data | Safe data sitting in a file. It is not attached to a running stack, a sealed network, or anything that exercises a workflow end to end. |
| A migration linter | A fast opinion on your SQL, statically | Static analysis cannot tell you how long `ALTER TABLE` holds a lock on a table with your row count, or that a query plan regressed. That needs execution against production-shaped data. |
| Testcontainers and friends | Real dependencies in your test process | Fixtures, scoped to one test, in one process. Not the whole stack under production-shaped load with third-party calls contained. |

Antifailure uses several of these rather than replacing them: the database can
come from Neon, Supabase, or DBLab thin clones, and the runner drives a real
browser. The part it adds is the assembly, and the proof that the assembly held.

### When not to use this

Being honest about this is cheaper than a disappointed evaluation.

- **You do not use Postgres.** The safe-state work is Postgres-specific. Other
  engines are not on the near roadmap.
- **You need a guarantee.** This produces evidence, not certainty. The
  [terms](https://antifailure.dev/terms) say so in the same words.
- **You want a hosted product today.** There is no generally available control
  plane yet. The signup page is a waitlist and says so.
- **Your changes are not risky.** If nothing you ship touches schema, data
  volume, or third-party side effects, a normal test suite is the right tool
  and this is overhead.
- **You cannot run containers in CI.** It needs Docker, GitHub Actions, or
  Kubernetes somewhere it can build and run your services.

## Questions people actually ask

**Does my production data leave my infrastructure?**
No. The hosted control plane holds organizations, policy, aggregated reports,
and billing. Raw snapshots, secrets, and captured request bodies stay in your
cloud by default. That boundary is described on the
[architecture page](https://antifailure.dev/product/architecture).

**How do I know the masking actually worked?**
A scanner reads back every column of every table looking for anything that
still parses as an email, a card number, a phone number, or a key, then signs
an attestation. An unverified golden cannot be branched, and that is enforced
in code rather than in a checklist.

**What stops a test run from emailing real customers or charging a real card?**
Every environment gets a sidecar that owns its network namespace, and nothing
leaves except through it. Each host gets a mode: `BLOCK`, `ALLOW`, `SANDBOX`
(test credentials, with a tripwire if a live key ever appears), `CAPTURE`
(mail and SMS into a searchable inbox), or `MOCK` (a stateful offline pack).
The default for an unlisted host is to fail closed.

**Can it run with no network access at all?**
Yes for the covered surface. The Stripe pack is complete enough to run
checkout, subscribe, renew, and cancel with signed webhooks and no network.

**What happens when a test fails because the tooling broke, not my code?**
It is classified as such. The runner returns one of `pass`, `fail`, `flaky`,
`blocked`, or `unverified`, and a failure caused by the runner is never counted
against your application.

**Which databases and platforms are supported?**
Postgres, sourced from Docker, Neon, Supabase, or DBLab thin clones in front of
any Postgres including RDS, Cloud SQL, and Azure Database. It runs locally on
Docker, in GitHub Actions, or on your own Kubernetes.

**Is it open source?**
This repository is MIT licensed except for `ee/`, which is under the
Antifailure Enterprise License. `ee/` is never compiled into the community
binary, images, or Helm chart.

**Is it production ready?**
No, and [docs/plan/STATUS.md](docs/plan/STATUS.md) is the honest answer per
component rather than a single claim. It marks each one proven, written, or
planned, and is updated in the same pull request as the code.

## Status

Pre-1.0 and under construction. [docs/plan/STATUS.md](docs/plan/STATUS.md)
tracks every component with one of three states: proven (it runs and its tests
pass in CI), written (the code exists and its fakes pass, but it has not been
exercised against the real service), and planned. That table is the honest
answer to "does it do X yet", and it is updated in the same pull request as the
code. Breaking changes are announced in the changelog.

## License

This repository is MIT licensed, except for the `ee/` directory, which is
licensed under the Antifailure Enterprise License (see
[ee/LICENSE.md](ee/LICENSE.md)).

The `ee/` directory is never compiled into the community binary, images, or
Helm chart. Those are built from `antifailure/antifailure-foss`, a generated
mirror of this repository with `ee/` deleted, so the boundary is proved by the
community build passing green rather than asserted in a comment. A depguard
rule, the `ee` build tag, and a symbol inspection of the shipped artifacts
check it three more times.

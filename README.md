<h1 align="center">Antifailure</h1>

<p align="center">
  <strong>A disposable copy of your production stack for every pull request.</strong><br>
  Masked Postgres branches, contained third-party APIs, agents that use the app like people, and load shaped like your real traffic.
</p>

<p align="center">
  <a href="https://antifailure.dev/docs">Documentation</a> &middot;
  <a href="https://antifailure.dev/docs/getting-started/quickstart">Quickstart</a> &middot;
  <a href="CONTRIBUTING.md">Contributing</a> &middot;
  <a href="SECURITY.md">Security</a>
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

## Status

Version 1.0. That is a promise about what keeps working, and it is written down
surface by surface in
[what is stable](https://antifailure.dev/docs/reference/stability/) rather than
made as a blanket claim: a manifest declaring `version: 1`, the commands and
their flags and exit codes, the documented `--output json` fields, the provider
interfaces, and the error codes. Breaking any of those costs a major version.
That page also names what is deliberately not covered, which is the half worth
reading before you build against something.

It is a promise about interfaces, not a claim that every component is finished.
[docs/plan/STATUS.md](docs/plan/STATUS.md) tracks each one with one of three
states: proven (it runs and its tests pass in CI), written (the code exists and
its fakes pass, but it has not been exercised against the real service), and
planned. That table is the honest answer to "does it do X yet", and it is
updated in the same pull request as the code. [CHANGELOG.md](CHANGELOG.md) is
what changed in each release.

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

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

## What it is

Every pull request gets its own environment built from the shape of production: the real schema and real data volume with every identifier masked and the masking proved, your services built and running in a sandbox that cannot reach the internet except where you say it can, and inbound webhooks simulated so flows actually finish.

```bash
curl -fsSL https://antifailure.dev/install.sh | sh
af start           # where you are on the first run, and what to run next
af runner install  # the agent runner, which drives a real browser and needs node
af init            # reads your repo, writes antifailure.yaml
af up              # masked database branch, built services, sealed network
af test            # agents run your workflows and return verdicts with evidence
af down            # every resource it created, gone
```

`af start` is the one to remember. It reports every step of that list as
observed on this machine right now and names the single next command, so a first
run you walked away from is one you can walk back into. It runs nothing and
writes nothing.

The installer puts `af` under `~/.antifailure` and puts that on your PATH, by
appending one line to the startup file your login shell reads. It prints the
line and names the file, so deleting it undoes the change, and
`AF_NO_MODIFY_PATH=1` declines it. The terminal you ran it in gets one line to
paste, because a running shell cannot see a file written a second ago.

## What it does

**Masked data, verified.** Masking is compiled to SQL and executed in resumable chunks, deterministic so the same customer maps to the same fake customer across every table and every refresh. Then a scanner reads back every column of every table, sampling rows rather than reading all of them, looking for anything that still parses as an email, a card, a phone number, or a key, and signs an attestation that records the sample size it used. An unverified golden cannot be branched. That is enforced in code, not in a checklist.

**A network you control.** Every environment gets a sidecar that owns its network namespace. Nothing leaves except through it. Each host gets a mode: `BLOCK` refuses with a decision you can read, `ALLOW` lets it through with a rate limit, `SANDBOX` swaps in test credentials and trips a wire if a live key ever appears, `CAPTURE` records the email or SMS into a searchable inbox your agents can read, `MOCK` answers from a stateful offline pack, and `SYNTH` asks a model to invent a response and marks every result that touched it as unverified rather than passed. The Stripe pack is complete enough to run checkout, subscribe, renew, and cancel with signed webhooks and no network at all.

**Agents, not scripts.** Workflows are written as sentences. The runner drives a real browser through the accessibility tree, logs in the way a person does (password, magic link from the captured inbox, one-time code, TOTP), and returns `pass`, `fail`, `flaky`, `blocked`, or `unverified` with a video, a trace, and reproduction steps. A failure caused by the runner is classified as such and never counted against your application.

**Database review, automatically.** Pending migrations are rehearsed on a fresh branch with per-statement timing and the strongest lock held per table. `pg_stat_statements` is diffed between main and your branch to catch the N+1 you just introduced. Query plans are compared to catch the index you stopped using.

**Nothing outlives its environment.** Every resource is journaled before it is created and compensated on teardown, so a crash at any instant is recoverable by replay. The leak detector inventories every provider and fails the build if anything untracked exists.

## Installation

```bash
curl -fsSL https://antifailure.dev/install.sh | sh
```

The installer downloads the release for your platform, refuses to install it unless it matches the published checksum, and puts `af` and its runner under `~/.antifailure`. There is no path through that check that installs an unverified archive: a missing `checksums.txt`, a `checksums.txt` with no line for your platform's archive, and a machine with neither `shasum` nor `sha256sum` all stop the install rather than warning and carrying on. It is POSIX sh rather than bash, so it works in an Alpine container as well as on a laptop.

Then install the agent runner, which is a separate program in a separate language because it drives a real browser:

```bash
af runner install
```

It needs node 22.6 or newer. The runner is copied from the source that ships beside `af` rather than downloaded, so the source a release was tested with is the source it runs, and its dependencies come from the lockfile that ships with it, so two people installing one release get one tree. It then downloads chromium, which is the slow part and is not fatal if it fails: a workflow that needs a page read comes back `unverified` rather than guessed at.

Two commands report on the machine, and neither one guesses:

```bash
af doctor          # disk, ports, DNS, egress, kernel isolation, leftovers
af runner check    # the runner source, its dependencies, node, and the browser
```

Read the [quickstart](/docs/src/content/docs/getting-started/quickstart.md) for a complete walkthrough from an empty machine to a proven run.

## A manifest example

The manifest describes what to build, where the database comes from, what the environment may reach on the network, who the agents log in as, and what they do. It is the whole configuration surface: nothing about an environment is configured anywhere else.

```yaml
version: 1
name: next-orders

services:
  - name: web
    kind: web
    path: .
    port: 3000
    health_path: /api/health
    migrate: "psql $DATABASE_URL -v ON_ERROR_STOP=1 -f migrations/0001_init.sql"
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
    env:
      - name: PORT
        value: "3000"
      - name: NODE_ENV
        value: "production"

database:
  provider: docker
  version: 17
  masking_rules: masking.yaml
  golden:
    schedule: "0 3 * * *"
    max_age: 168h
    retain: 3

egress:
  default: block
```

See the [manifest reference](/docs/src/content/docs/reference/manifest.md) for every option.

## Where it runs

Locally on Docker, in GitHub Actions, or on your own Kubernetes. The database comes from Docker, Neon, Supabase, or DBLab thin clones in front of any Postgres, including RDS, Cloud SQL, and Azure Database. Providers are an interface with a published conformance suite, so adding one is a package, not a fork.

## Self-hosting

Antifailure works without a control plane. `af up` builds an environment on the machine it runs on, and nothing calls home.

The hosted control plane at https://app.antifailure.dev holds organizations, policy, aggregated reports, and billing. It is optional. When you are ready to add it, you can run your own or use the hosted version. The control plane degrades gracefully: environments keep working if the control plane is unreachable, events are buffered and sent when it returns, and teardown still works because it reads the local journal instead.

For self-hosting:

```sh
docker run --rm \
  -e AF_MIGRATION_DATABASE_URL=postgres://owner:...@db:5432/antifailure \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  ghcr.io/antifailure/control-plane:main-b53906a node bootstrap.mjs

docker run \
  -e AF_DATABASE_URL=postgres://af_app:...@db:5432/antifailure \
  -e AF_GITHUB_CLIENT_ID=... \
  -e AF_GITHUB_CLIENT_SECRET=... \
  -e AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/github/callback \
  -p 8080:8080 ghcr.io/antifailure/control-plane:main-b53906a
```

On Kubernetes, use the chart in `deploy/helm/antifailure-control-plane`. The Helm chart is developed against kind and runs on any conformant cluster. See [self-hosting documentation](/docs/src/content/docs/self-hosting/) for configuration, operations, upgrades, and runbooks.

## Status

Version 1.0. That is a promise about what keeps working, and it is written down surface by surface in [what is stable](https://antifailure.dev/docs/reference/stability/) rather than made as a blanket claim: a manifest declaring `version: 1`, the commands and their flags and exit codes, the documented `--output json` fields, the provider interfaces, and the error codes. Breaking any of those costs a major version. That page also names what is deliberately not covered, which is the half worth reading before you build against something.

It is a promise about interfaces, not a claim that every component is finished. [docs/plan/STATUS.md](docs/plan/STATUS.md) tracks every component with one of four words, and the words are the ones that file defines rather than a paraphrase of them: proven (the code exists, its tests pass, and the behavior has been exercised end to end against the real thing), written (the code exists and passes its tests against a fake that enforces the real service's validation rules, and has never talked to the real service), planned (specified, not built), and mixed (the parts are genuinely in different states, and the row says which are which). Proven does not mean it runs in CI: a suite that needs a real Neon or Supabase account cannot run on a fork's pull request, so those rows say in their own prose that they are run by hand. That table is the honest answer to "does it do X yet", and it is updated in the same pull request as the code. [CHANGELOG.md](CHANGELOG.md) is what changed in each release.

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for how to build locally, run the gates, and structure commits.

Before opening a pull request:

1. Run `just gate` locally or the targeted gates for what you changed.
2. Write a changelog fragment in `.changes/<slug>.md` with the first line being one of `# added`, `# fixed`, `# changed`, or `# security`. `just changecheck` says whether your change needs one, and [the changelog](https://antifailure.dev/changelog) is built from them.
3. Update `docs/plan/STATUS.md` surgically: touch only the rows your work changes.
4. Update published docs under `docs/src/content/docs/` if a user-visible behavior changes.

Every commit is signed with `git commit -s` per the Developer Certificate of Origin.

## License

This repository is MIT licensed, except for the `ee/` directory, which is licensed under the Antifailure Enterprise License (see [ee/LICENSE.md](ee/LICENSE.md)).

The `ee/` directory is never compiled into the community binary, images, or Helm chart, and the boundary is proved by the community build passing green rather than asserted in a comment. The proof is run in place rather than in a mirror: the `edition boundary` job in `.github/workflows/ci.yml` deletes `ee`, then builds and tests the engine from what is left, and then inspects the binary it shipped for enterprise package paths. `.dockerignore` keeps `ee` out of the image build context.

# Changelog

One section per released tag. `tools/relnotes` reads the section matching the
tag being published and the release workflow puts it in the release notes, so a
tag with no section here, or with an empty one, does not publish at all.
Releases before v1.0.0 predate this file and their notes are on the GitHub
releases page.

## v1.0.0

The first stable release. 577 commits since v0.1.1, 197 of them landings on
`main`, over the four days from 26 August 2026.

### What 1.0 means, and what it does not

A major version is a promise about what will keep working, so here is the
promise, named surface by surface rather than as a blanket claim about an API.
The standing version is at antifailure.dev/docs/reference/stability.

Stable, and breaking any of these costs a major version:

- **The manifest.** A manifest declaring `version: 1` keeps working. Keys may be
  added and existing ones may gain new accepted values; a key will not be
  removed, renamed, or given a different meaning. The promise runs backwards,
  not forwards: an older manifest works on a newer `af`, and a manifest using a
  key added in a later 1.x does not work on an earlier one, because the parser
  refuses a key it does not know rather than ignoring it.
- **The command line.** The commands, their flags, and their exit codes. A
  command will not be removed or renamed, and a flag will not change what it
  means. New commands and new flags are minor releases.
- **`--output json`.** The documented fields of every command's JSON. Fields may
  be added, so parse for the fields you want rather than refusing unknown ones.
  A documented field will not be removed or change type.
- **The provider interfaces.** `engine/pkg/provider` and the `engine/pkg/schema`
  types they carry. A provider is meant to be written outside this repository
  and each interface ships with a conformance suite, so it is an integration
  surface and is treated as one.
- **The error codes.** A code in the error reference keeps its meaning. Codes
  are the stable identifier for a refusal; the sentence beside one is not.

Explicitly not stable, and free to change in a minor release:

- The Helm chart's values and the Terraform module's variables. The chart is
  versioned separately and is at 0.1.1 for that reason.
- The control plane's HTTP API, which the console and the engine speak to each
  other and which is not a published integration surface.
- Every Go package except the two named above, and everything under
  `engine/internal`, which is unimportable on purpose.
- Lint rule names and their findings, which move as the rules improve. The
  stable identifier for a finding is its rule name within a release.
- The event stream's set of types. Types are added as features land.

### What moves when this tag is pushed

Pushing `v1.0.0` publishes the control plane image as
`ghcr.io/antifailure/control-plane:v1.0.0` and **moves
`ghcr.io/antifailure/control-plane:latest` onto it.** `latest` resolves to the
v0.1.1 digest until then. If you self host and pull `latest`, this tag changes
what your next pull gets, on your infrastructure, at a time we chose rather than
one you did. Pin `v1.0.0`, or pin the digest that tag resolves to, if that
matters to you. A tag can be moved and a digest cannot.

Nothing else moves on its own. No deployment is triggered by this tag, and no
existing environment is upgraded.

### Supply chain

This release is the first whose workflow runs the SPDX bill of materials and the
cosign keyless signing. Both were written after v0.1.1 was tagged and no
published artifact has ever carried them, so this run is the first execution of
either and not a track record. You can settle for yourself whether they ran: a
release that ran them carries `checksums.txt.sigstore.json` and
`sbom.spdx.json`, and v0.1.0 and v0.1.1 carry neither. The verification commands
are at the top of these notes.

### Installing

`install.sh` is unchanged. It asks the GitHub releases API for the newest
release rather than carrying a version of its own, verifies the checksum of what
it downloaded, and installs `af` into `~/.local/bin`. `AF_VERSION=v0.1.1
./install.sh` still installs an older one.

### Behaviour you may depend on that changed

Read this section before upgrading. Everything here changes what an existing
manifest or pipeline does.

- A run holding a workflow verdict the engine cannot read is now `blocked`. It
  used to report the whole run as passed and exit zero while the table beside
  that line printed the same workflow as unverified.
- A run that tried to reach a host the manifest does not mention no longer
  reports `pass`. The request was always refused; the attempt reached the pull
  request comment and changed nothing about the verdict.
- `af ci` runs teardown before it writes the report, so a run that left a
  resource behind says so and, by default, does not ship.
- A migration that holds an exclusive lock past `policy.migration_lock.fail_ms`
  now fails the check and names the table. The rehearsal has sampled `pg_locks`
  since phase 3 and none of it reached a pull request before now.
- `load.source` no longer accepts `datadog` or `newrelic`. Both were in the
  schema, so a manifest could set them, and both refused when a run reached
  them. An unrecognised source is now refused by name at validation time, with
  the sources that do work.
- An installation licensed for `policy_enforcement` now actually refuses an
  environment that violates the organization policy. `policyenforce.Hook` was
  written and tested and no binary ever constructed one, so the feature refused
  nothing and its compliance report said no policy was configured.
- The egress sidecar enforces the same decision on all three of its request
  paths. A plain HTTP request through the explicit proxy port, which is what
  every client reading `http_proxy` sends, used to skip the live credential
  tripwire, forward the application's own credential in `sandbox` mode, and
  refuse `capture`, `mock` and `synth` instead of serving them.
- The example GitHub Actions workflow runs `af change` first and gates `af ci`
  on its output, so a pull request that touches nothing any check exercises no
  longer gets an environment. It checks out with `fetch-depth: 0`, because a one
  commit deep clone has no merge base to diff against.
- The documentation now runs the control plane image as
  `ghcr.io/antifailure/control-plane:latest` rather than naming a version. The
  tag is moved by the push of a `v*` tag and never by a build off `main`.

### Added

**Before an environment exists.** `af change` reads the diff of a pull request
and says which checks will exercise what it touched, opening no environment and
touching no database. Every changed path is classified by a rule that names it,
and a path no rule recognises selects every check rather than none. It never
grades the change: there is no score and no risk word, because both would be a
judgement made from a file listing. `change.rules` teaches it a layout the built
in rules do not predict.

**Migration safety.** The migration rehearsal now carries 17 DDL lint rules. The
one that matters most is `lock_timeout`: a migration that waits for a lock
queues every subsequent query on that table behind its own lock request, so a
four millisecond `ALTER TABLE` behind one long running transaction stops all
writes for as long as that transaction runs. The rest cover `SET NOT NULL` on an
existing column, a `CHECK` added without `NOT VALID`, `ADD CONSTRAINT ... UNIQUE`
building its index in place, a backfill in the same transaction as its schema
change, and `DROP INDEX`, `REINDEX`, `VACUUM FULL`, `CLUSTER`, `DROP TABLE` and
`TRUNCATE`. Each names the lock mode, the real row count of the table on the
branch, and the multi deploy sequence that avoids the problem.

`af insights` also runs the previous release against the migrated branch and
reports whether its workflows still pass, which is the invariant a rolling
deploy depends on. A failure is confirmed against a second branch with the
migrations left off, so a workflow that fails either way is reported as
unverified rather than blamed on the change. Configured by
`insights.rolling_compatibility`, which defaults to running only when the
migrations take something away.

**`af oracle`.** Runs a change beside the version it replaces: a second
environment from a baseline revision, the same golden branched for both, the
same requests in the same order, and every difference in what came back and in
what ended up in the database. Responses and database contents are compared
completely; events, outbound effects, traces and query plans are not compared at
all, and everything the comparison declined to look at is printed on every run.

**`af explore`.** Sends agents at a goal with no declared workflow. They read
each page through the accessibility tree and report where the application cost
somebody effort without failing: a control that did nothing, a page with nothing
left to try, a route that loops back, an element with no accessible name, a step
slower than the goal allows. Every choice comes from the goal's seed, so each
result carries the command that replays it. `af explore --emit-workflow` prints
the `workflows:` block that turns a discovery into a check.

**`af fidelity`.** An inventory of what an environment reproduces and what it
does not, across the services, the data, the third party hosts, the personas,
the runtime and the traffic. Nothing is estimated: a component whose state could
not be determined is reported as unmeasured, excluded from the headline and
named with the reason, and when nothing could be measured there is no score
rather than nought percent. `fidelity.require` names dimensions that must be
fully reproduced.

**Load.** `af load scenario` runs declared journeys: an ordered list of requests
with waits between them, parallel blocks so the second submit arrives while the
first is in flight, and assertions over what came back. `load.source: otel` reads
the traffic mix out of an OpenTelemetry trace export on disk, and because a span
carries a duration the shape arrives with production's own p95 per route, which
is the baseline `p95_increase` compares against and which nothing could provide
before.

**A `warn` verdict.** A run can come back with a real finding about the change
that does not fail the check. A new `policy` block decides which class of
finding warns and which fails, with `ignore` for the ones a project does not
want. `blocked` keeps its meaning exactly: the runner could not evaluate this,
it exits zero, and it never counts against the change.

**Cost control.** An environment now has a lifetime, and `af env reap` destroys
the ones that have outlived it. It refuses to pull an environment out from under
a run that is using it.

**Hosted control plane.** Billing through Stripe: customers, subscriptions,
invoices, a checkout session, the customer portal, and the webhook that moves
`organizations.plan`, which is what `PLAN_QUOTAS` and `checkQuota` have always
been pointed at and never been able to reach. Billing is off unless every one of
its four variables is set, and a partly configured one is reported as off with
the missing names. Every billing table has row level security enabled and
forced, and signatures are checked over the raw body before anything is parsed.

The console can act rather than only report: an egress rule waits for approval
instead of enforcing the moment a member proposes it, create environment, run
agents and run load dispatch your own workflow in your own repository, runtimes
can be registered and removed, and the plan can be read and set. A break glass
command sets a role directly in the database for when the GitHub App is gone and
nobody inside the organization can act, writing an audit entry carrying the
reason and refusing to leave an organization with no owner. The first member of
an organization becomes its owner when GitHub confirms they administer it.

**Operations.** A status page checked from GitHub Actions rather than from the
control plane, so an outage cannot silence the page reporting it. A disaster
recovery drill on a weekly schedule that reports the recovery time it measured
and holds it against a budget. Eleven Azure Monitor rules and an action group
where there were none, each naming its runbook in the notification it sends. An
on call page, a manual rollback procedure, and a rotation runbook for every
secret in the control plane's Key Vault.

**The public site.** antifailure.dev, its documentation, and the four legal
documents a security review asks for by name: a data processing agreement, the
subprocessor list, a statement that there is no service level agreement, and
retention and deletion commitments. Every subprocessor was established by
reading the code that talks to the vendor. No lawyer has read any of it and
every page says so on its face.

### Fixed

- AF-EE-004 and AF-EE-010 are on the errors reference page. Both ship and both
  were marked as reserved for a feature this version does not have, so somebody
  refused by single sign on or by organization policy searched the reference for
  the code they had just been shown and found nothing.
- A golden refresh says what it is doing on the event stream. Six `mask.*` types
  were in the catalog and on the reference page and nothing emitted one, so
  dashboard mode drew an empty pane through the longest part of a refresh. A
  failed verification now puts every finding on the stream rather than only the
  first one the refusal names.
- The waitlist form on antifailure.dev dropped every address for two days,
  because the deploy published a site with no API and the platform removed the
  managed function a hand deploy had put there. The deploy publishes `api/` now
  and refuses to finish green unless the endpoint answers.
- Every security header the site's own configuration declares was being thrown
  away before publishing. The site now serves the two year HSTS with preload
  rather than the platform's 126 day default, a permissions policy, a cross
  origin opener policy, and immutable cache headers on hashed assets, and
  `/product/crowdi` redirects instead of answering 200.
- The production runbook prescribed a GitHub App permission and four webhook
  events nothing in this repository uses, and was missing the one the console's
  dispatch verbs need.
- The documentation shipped two of the eight head tags it was written to carry,
  on all 76 pages. `just docscheck` is the gate that would have caught it.
- The console was built one page at a time and the seams showed: four heading
  sizes, four primary buttons, four radii where there is a scale of three. That
  is one vocabulary now. Its tertiary grey measured 3.2:1 and is 4.6:1. Every
  list stacks into one record per row below `sm` instead of hiding the two
  columns a reader came for behind a horizontal scroll, rows are reachable by
  keyboard, and nothing pulses.
- Documentation code blocks were painted at 1.12:1 against the page and had
  neither a visible surface nor a visible edge. Every control on a phone is now
  at least 44px and no page scrolls sideways at 375px.

### Security

- Release archives are reproducible and proven to be. Two builds of one commit
  produced four different archives every time, because `tar` takes each entry's
  timestamp from the filesystem and `gzip` writes another into its own header.
  The old check compared `bin/af`, which always matched. `tools/reltar` writes
  the archives now and the check builds twice and compares the archive.
- The bill of materials described nothing. It was generated from a directory of
  `.tar.gz` files, which the generator does not open, so every release would
  have carried a valid SPDX document listing one package instead of the 363 in
  the binaries. `tools/sbomcheck` now requires it to record the SHA256 of every
  binary that ships.
- Signing could not have worked at all. The pinned installer supplies cosign v3,
  where `sign-blob` refuses to run without `--bundle`. Releases sign a bundle
  now, and the workflow verifies both signatures and requires a copy with one
  byte changed to be rejected before it publishes anything.
- The egress sidecar refuses to open a loopback, link local, private or carrier
  grade address on the environment's behalf unless a rule names it, which closes
  the instance metadata endpoint under `default: allow`. It answers non-address
  DNS queries for external names itself, takes a transparent connection's port
  from the listener rather than from the client's `Host` header, and enforces
  `egress.allow_ipv6`, which nothing read.
- `npm audit` runs against every lockfile beside govulncheck, on every pull
  request and every morning. govulncheck reads Go modules and stops there, and
  every `npm ci` in CI passes `--no-audit`, so seven lockfiles, one of which
  builds the control plane, had no advisory check at all.
- `SECURITY.md` stops claiming three things the evidence does not support, and
  the disclosure section now says what happens when a target is missed and that
  there is no bug bounty.

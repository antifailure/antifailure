---
title: Cutting a release
description: What a version tag sets off, what green looks like at every stage of it, and what to do when a stage goes red.
sidebar:
  order: 6
---

**The same tag deploys the hosted control plane, applies migrations to
production before any traffic moves, and then waits on a human approval.** That
sentence is why this page exists. A tag here is not a bookkeeping act.

It also publishes the binary that `curl -fsSL https://antifailure.dev/install.sh | sh`
hands to a stranger, and the installer follows `releases/latest`, so the
download changes the moment the release is created. Two workflows fire on the
same tag, they run in parallel, and neither knows the other exists.

This page is the order to do it in and the thing to look at after each step.
[Releases and how to verify one](/docs/security/releases) is the companion
page, written for the person downloading a release rather than the person
cutting one.

## What one tag sets off

| Workflow | Triggered by | What it does |
| --- | --- | --- |
| `.github/workflows/release.yml` | `push` of a tag matching `v*` | Waits for CI, builds four platforms, packages, signs, and creates the GitHub release |
| `.github/workflows/cd.yml` | `push` to `main` **and** `push` of a tag matching `v*` | Waits for CI, builds the control plane image, deploys staging, then waits for a human to approve production |

Two things follow from that table and both have bitten somebody somewhere.

`release.yml` has a gate of its own, and until recently it did not. A `gate`
job runs before the build, waits for CI's conclusion on the commit the tag
names, and refuses anything but `success`. So a tag on a red commit now
publishes nothing. It waits for about 38 minutes before giving up, and giving up
is a refusal too. The judgement is `tools/cigate`.

That gate refuses a run GitHub reports as `cancelled`, and this is the case
worth knowing about before you tag. GitHub uses that one word for three
unrelated things: a job that hit its own time limit, a run somebody stopped by
hand, and a run that a newer push superseded. None of them is a verdict, so none
of them publishes.

`ci.yml` no longer cancels a superseded run on `main` or on a tag, which is why
this is now rare rather than routine. Six merges once landed inside one run's
length and each cancelled the one before it, and `main` went hours with no
completed run. If you do meet a cancelled run on the commit you want to tag,
re-run CI on it, wait for green, then re-run the release from the Actions page.

Checking before you tag is still the cheaper order. The gate turns a mistake
into a refused release rather than a published one, which is not the same as
turning it into no mistake.

`cd.yml` runs a second time on the tag, on the same commit it already ran on
when that commit merged to `main`. Its concurrency group is keyed on the ref,
so the tag run and the `main` run are in different groups and do not queue
behind each other. Wait for the `main` run to finish before you push the tag.

## Before you tag

Everything here is read only. Run it all.

**1. CI is green on the exact commit you are about to tag.**

```sh
SHA=$(git rev-parse origin/main)
gh run list --commit "$SHA" --workflow ci.yml
gh run list --commit "$SHA" --json workflowName,conclusion,status \
  --jq '.[] | "\(.workflowName)\t\(.status)\t\(.conclusion)"'
```

Read the second command's output rather than counting checks. **Do not assert a
number.** The count has been wrong every time somebody has quoted one: it was
"seven" in a briefing while `ci.yml` alone had nine jobs, and splitting the
credential scan into its own job took that to ten. Enumerate what actually ran
on that sha and require every entry to be `success`.

A `cancelled` entry is resolved by WORKFLOW, not by trigger. A scheduled run can
cancel a push-triggered run of the same workflow on the same commit, which
leaves a cancelled row that is not a failure. Look at which workflow it belongs
to and whether another run of that same workflow succeeded on that sha.

`cd.yml`'s first job polls for that same CI conclusion, as `release.yml`'s now
does, and both give up after about 38 minutes. If CI has not finished when you
tag, the tag's deploy fails on a timeout rather than on anything real.

**1a. `just gate` is not the bar, and cannot be met as written.**

The bar is CI green on the sha, above. `just gate` is the local approximation of
it and is deliberately a superset: `coverage` is in `gate` and CI does not run
it at all. `coverage` reads a profile that `coverage-profile` writes, and
`coverage-profile` is NOT in `gate` because producing it needs the whole engine
suite against a Docker daemon and a Postgres and takes the better part of an
hour. `tools/gatecheck` exempts it by name with that reason recorded.

So a clean checkout runs `just gate` and gets one red, `coverage`, over a
profile nobody made. That is the documented exception and not a defect. Either
run it first, or read the gate's other lines and ignore that one:

```sh
just coverage-profile   # about an hour, needs Docker and a Postgres
just coverage
```

Nothing else in `gate` is excused. A criterion nobody can meet is one people
learn to skip, which is why this paragraph exists rather than a rule saying
"all gates green" that is false on a fresh clone.

**1b. Every branch that landed reached CI before it landed.**

Pushing a `w-*` or `prep-*` branch to this repository runs NOTHING. `ci.yml`
triggers on `push` to `main` and on `pull_request`, and no other branch triggers
any workflow. A branch that was merged without a pull request has therefore
never been through CI, and the tag's commit is the first run of it. Open a draft
pull request per branch before landing, so that its first CI run is not on
`main`.

**2. The `main` deploy of that commit has finished.**

```sh
gh run list --workflow cd.yml --limit 3
curl -sS https://app.dev.antifailure.dev/readyz
```

The `commit` field in that answer should already be the commit you are tagging.
Staging is then serving the build production is about to serve.

**3. The release build works on this commit.**

```sh
just ldcheck
just relnotes
just tagsync
just reproducible
```

`ldcheck` reads the `-X` flags out of `tools/release/build.sh` and proves each
one names a variable that exists. The linker accepts a `-X` for a symbol it
cannot find and says nothing, which is how v0.1.0 shipped four platforms that
all reported themselves as `dev`.

`relnotes` and `tagsync` are the two that decide whether the tag can publish at
all, and both are cheap here and expensive later. `relnotes` refuses a
`CHANGELOG.md` section that is a heading with nothing under it; at tag time the
same check runs inside `release.yml`, where the only remedy is deleting a tag
people may already have fetched. `tagsync` refuses a version pin naming a tag
nobody published, and holds the four version literals in [verifying a
release](/docs/security/releases) to the version at the top of the changelog.

**4. The release notes are written before the tag, not after it.**

`release.yml` passes `generate_release_notes: false` and a `body_path` that
`tools/relnotes` writes, so the notes are the `## vX.Y.Z` section of
`CHANGELOG.md` with the verification instructions prepended. Write that section
first: a tag whose section is missing or empty fails the release job, and by
then the tag is pushed.

Read the section you are about to publish for figures. Anything counted out of
the tree, commits, landings, pages, days, is counted against a tree that was
still moving when it was written, and `just figurecheck` does not read this
file. The v1.0.0 section carried a commit count that had drifted by 40 percent
before anybody looked. Either re-count it against the commit you are tagging or
take it out.

Nothing reads the fragments under `.changes/`, so they are the raw material and
not the notes. Gather them into the changelog section by hand:

```sh
head -n 1 .changes/*.md | grep -v '^==>' | sort | uniq -c
cat .changes/*.md
```

**5. Nothing in the release path has moved since it was last exercised.**

Everything on this page was checked against a tree, not against the idea of a
tree. Nothing in the mechanism depends on any particular branch having landed,
so a release can be cut at any point. What does depend on the tree is whether
the checks behind this page still describe what is about to run. Ask, rather
than assume:

```sh
git diff --stat 8389faf..origin/main -- \
  .github/workflows/release.yml .github/workflows/cd.yml \
  tools/release/ tools/sbomcheck/ tools/ldcheck/ tools/relnotes/ \
  tools/tagsync/ deploy/cd/ install.sh \
  web/packages/db/migrations/
```

Empty output means this page still holds. Anything outside `migrations/` means
the pipeline changed and the rehearsal behind this page no longer covers it. A
new file under `migrations/` means production is being asked to apply a
migration nobody on this page has read, and that one is worth stopping for: a
migration is the only part of a deploy that cannot be rolled back.

**It is not empty today, and here is what has been done about each half.**
`install.sh` and `tools/release/build.sh` have both moved since that revision,
so the release build path was re-run rather than assumed: `just build-release
v1.0.0` on this tree, the archive unpacked and the binary inside it run out of
the unpacked directory, `af version` reporting the version passed to the script
with the real commit and that commit's own date, the checksum file verified,
`just reproducible` building twice with a cold cache and getting the same
archive, and `just ldcheck`, `just relnotes`, `just tagsync` and
`just releasecheck` green. That re-run is what found `build.sh` packaging an
archive with no `af` in it, so the drift here was carrying a real defect and not
only a stale sentence.

The `migrations/` half is not resolved and is read below rather than here.
```sh
git tag -a v0.1.2 -m "v0.1.2"
git push origin v0.1.2
```

Annotated and unsigned, and pushed on its own. The signing in this pipeline is
cosign over `checksums.txt` and the bill of materials, done by the publish job,
and it does not depend on the tag carrying a signature. Setting up signed tags
is optional and the steps are on the
[releases page](/docs/security/releases#signing-the-tags-too). Do not push the
tag in the same command as a branch: a tag that arrives before its commit is on
`main` has no CI run for `cd.yml` to wait for.

## Watching release.yml

```sh
gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
```

Five jobs. Four of them build one platform each and only compile; the fifth is
the only one in the repository that holds `contents: write`.

| Stage | Green looks like | Red means |
| --- | --- | --- |
| `build darwin-arm64` and its three siblings | Each uploads one `.tar.gz` and one `.sha256` | A compile failure, or a `-X` flag naming a symbol that no longer exists. `just ldcheck` locally is the same question |
| Third party notices | `THIRD_PARTY_NOTICES.md` regenerated from what is linked | A dependency whose licence the generator does not know |
| Checksums | Four lines in `checksums.txt` | Fewer than four archives arrived, so a build job silently produced nothing |
| Unpack | Four paths printed, one per platform | Two archives unpacked over each other, which would leave the bill of materials describing three of four binaries |
| Software bill of materials | An SPDX document written to `dist/sbom.spdx.json` | syft failed. The document is not published unless the next stage passes |
| The bill of materials describes this release | `sbomcheck: <n> packages, 4 binaries, every one described`, where n is in the hundreds | The count is the load bearing number and the floor is 50. A document listing one package is what syft produces when it is pointed at archives instead of binaries, and it is valid SPDX, so only this stage can tell you |
| Sign the checksums and the bill of materials | Two `.sigstore.json` bundles written | Sigstore was unreachable, or the job lost `id-token: write` |
| The signature verifies, and a changed byte does not | `Verified OK` twice, then `a tampered checksums.txt was rejected, as it must be` | Either half failing stops the release. The second half failing means cosign accepted a file that does not match its signature, and every verification instruction the project publishes is worthless until that is understood |
| The release notes | `tools/relnotes` prints the notes it wrote, opening with the verification instructions and then this version's changelog section | `CHANGELOG.md` has no `## vX.Y.Z` section for this tag, or the section is empty. `just relnotes` before tagging is the same question, and the only remedy here is deleting a tag people may already have fetched |
| Release | The tag appears under Releases with nine assets | The publish itself failed. A `files:` pattern matching nothing is one of the ways, because `fail_on_unmatched_files` is set, which turns the silent version of this into a red stage. Nothing was signed with a key, so there is nothing to revoke |

### The two stages nobody has watched, and the two checks only a person can do

**The signing and the bill of materials have never run in a real release.**
v0.1.1 predates both, and its assets are four archives, `checksums.txt` and
`THIRD_PARTY_NOTICES.md` and nothing else. So the first real run of both is the
release you are cutting. That is correct-looking code that has never executed,
which is the category this project keeps getting caught by, and the answer is
that somebody watches it rather than assuming it.

Every step of both has been rehearsed locally against the real artifacts: four
platforms built, unpacked, catalogued by the exact syft version
`anchore/sbom-action` pins rather than whatever was on the machine, and
`tools/sbomcheck` watched passing on the good document and failing on the old
broken shape. `cosign sign-blob --bundle` and `verify-blob` were exercised the
same way, including the one byte change being rejected, with a local key pair.

Two things that rehearsal could not reach, so they are checked by hand, on the
run, and neither has a tick that means anything on its own.

**Did the release itself publish what it should have?** Nine assets, not eight
and not four:

```sh
gh release view v0.1.2 --json assets --jq '[.assets[].name]'
```

Four archives, `checksums.txt` and its bundle, `sbom.spdx.json` and its bundle,
and `THIRD_PARTY_NOTICES.md`. Four assets is the shape of a release that
published before the signing stage existed.

**If it is not nine, do this.** The release notes tell people to fetch a file
that is not there, so the release is wrong even though every stage was green.

1. Mark it immediately, before anything else. The installer is already serving
   it and every minute counts more than the diagnosis does:

   ```sh
   gh release edit v0.1.2 --notes "Incomplete assets. Superseded shortly. Do not use."
   ```

2. Find which asset is missing and read the log of the stage that produces it.
   A missing `.sigstore.json` means the signing stage; a missing
   `sbom.spdx.json` means syft or `sbomcheck`; a missing archive means one of
   the four build jobs. The stage cannot have failed, because a failure stops
   the release, so what you are looking for is a stage that succeeded while
   producing less than it should have. That is the same defect shape as the
   empty bill of materials, one layer up.

3. Do not re-run the publish job against the same tag. `softprops/action-gh-release`
   would upload onto the existing release, so the tag would quietly come to mean
   something different from what people already downloaded. Fix the cause and cut
   the next patch, following [If a release goes out wrong](#if-a-release-goes-out-wrong).

**Did the Fulcio identity binding work?** Open the log of the stage named *The
signature verifies, and a changed byte does not*. It runs `cosign verify-blob`
with `--certificate-identity` bound to this workflow, in this repository, at
this tag. That is the only thing in the pipeline that proves the certificate
says who signed rather than merely that somebody did, and it cannot be
exercised anywhere but on GitHub, because the certificate is issued against the
job's own OIDC token. It must print `Verified OK` twice and then
`a tampered checksums.txt was rejected, as it must be`. A green tick on that
stage without those three lines in its log is not the same thing.

## Watching cd.yml

The tag's `cd` run is a second run, distinct from the one `main` already had.

| Stage | Green looks like | Red means |
| --- | --- | --- |
| `gate` | `CI is green on <sha>` in the step summary | CI is not green on this commit, or it never ran on it. Nothing has deployed. Fix `main`, then tag again with a new patch version |
| `build` | A digest printed, then `bootstrap refuses and names the variable` | The image does not build, or it built without the entrypoints the deploy needs |
| `staging` | `DEPLOYED: https://app.dev.antifailure.dev is serving <sha>` | See the failure table below. Production does not start |
| `production` | Waits for a reviewer, then the same line for `https://app.antifailure.dev` | See below |

Production does not begin until somebody named on the `production` environment
approves it. That approval is a deployment protection rule rather than an `if:`
in the workflow, so it cannot be edited in the same pull request that deploys.

### What the production job does, in order

1. Asks Azure whether `afcpprod-app` exists in `af-cp-prod-centralus`, and
   refuses by asking rather than by asserting. It stops being a refusal the
   moment the apply has happened, with no workflow edit.
2. Runs `tools/azguard` against the resource group, offline, failing closed.
3. Runs `deploy/cd/deploy.sh`, which reads what is serving now, **applies
   migrations first in the `afcpprod-bootstrap` job**, creates the new revision
   at zero traffic, health checks it on its own address, shifts traffic, health
   checks the public origin, and rolls traffic back if that last check fails.

The migration runs before any traffic moves. If it fails, nothing has changed
and the previous revision is still serving. That ordering is the reason a
failed release is usually a non event.

**Migrations are not rolled back.** `deploy.sh` can put traffic back on the old
revision and cannot un-apply a schema change, so the old code has to tolerate
the new schema. Read every migration in the tag that production has not seen
before you approve, and satisfy yourself that each one is additive.

```sh
git diff --name-only v0.1.1..v0.1.2 -- web/packages/db/migrations
```

### This is the first time production will deploy itself

Every production `cd` run so far has been skipped. The script inside it has run
against production once, by hand: `afcpprod-app` carries a revision named
`afcpprod-app--cf66d6af2-164545`, which is `deploy.sh`'s own naming, and
`afcpprod-bootstrap` has exactly one execution, `Succeeded`, a minute before it.
So the script is not the untested part. The job around it is.

What has no prior run behind it:

* `azure/login` under the `production` environment needs a federated credential
  for `repo:antifailure/antifailure:environment:production`. Staging's proves
  the pattern and not this subject. This is the step to read first if the job
  fails early with nothing else to go on.
* `tools/azguard` against `af-cp-prod-centralus`. It is offline and fails
  closed, and it has only ever been pointed at staging's group.
* The approval itself.

The app is in `Multiple` revision mode with one revision at 100 percent, so
there is a revision to roll back onto. The case where there is not is the one
`deploy.sh` reports plainly rather than pretending a rollback happened.

### This is an unusually large deploy, and one of its migrations wants a window

Production is serving `f66d6af`. Ask how far ahead the tag is rather than
carrying a number that goes stale between two merges:

```sh
curl -sS https://app.antifailure.dev/readyz
git rev-list --count f66d6af..origin/main
```

At the time of writing that was 178, so the first tag is not a normal
increment. It is every change since, arriving at once.

**Ask which migrations rather than reading a count off this page**, because the
count has already gone stale once:

```sh
git diff --name-only f66d6af..origin/main -- web/packages/db/migrations
```

All of them have been checked, and the checks are recorded here so nobody
repeats them nervously at tag time. Every migration from `0001` to `0023`
applies cleanly to a real PostgreSQL 17 from an empty database, and `0023` was
applied a second time to a database built to `0022` and then seeded, so that it
met existing rows rather than an empty table. It validated its constraint and
left every seeded session in place.

**Correctness is settled. Duration is not, and that is the one thing to decide
before you approve production.** The seeded table held 500 rows and production
does not, so what has been proved is that these migrations do the right thing,
not that they do it quickly enough to run while the site is serving.

Only three tables that exist at `0017` are touched at all. Everything else in
`0018` to `0023` creates a new table, which locks nothing and cannot block a
running request. The three are `network_rules`, `users` and `sessions`, and
this is every operation against them:

* **Nine nullable column adds with no default**, five on `users` and four on
  `sessions`. On PostgreSQL 11 and later these rewrite nothing and touch only
  the catalog, whatever the table holds.
* **`0018` backfills `network_rules`**, in the same transaction as its own
  schema change, and builds `network_rules_pending_idx` without
  `CONCURRENTLY`. That takes a SHARE lock and blocks writes to
  `network_rules` for the length of the build. It is a small table.
* **`sessions.impersonated_by` carries a foreign key to `users`**, so adding it
  locks `users` as well as `sessions`. Nothing on this path sets a
  `lock_timeout`, which is the same exposure `0018` already has and which is
  written out under the three migration budgets below.
* **Two full scans of `sessions`, which is the hottest table in the product**,
  because `resolveSession` reads it on every request.
  `ADD CONSTRAINT sessions_impersonation_is_complete` takes ACCESS EXCLUSIVE
  and validates every existing row, and `sessions_impersonated_idx` is partial
  but still reads the whole table to evaluate its predicate. For as long as
  the first of those runs, every authenticated request waits.

So measure before you approve, rather than assuming the table is small:

```sh
psql "$PROD_URL" -c "SELECT count(*) FROM sessions"
psql "$PROD_URL" -c "SELECT count(*) FROM network_rules"
```

`sessions` holds live sessions rather than history, so it is bounded by how
many people are signed in and is very likely small enough that none of this
matters. If it is not, this deploy needs a quiet window. The standard way out
is `ADD CONSTRAINT ... NOT VALID` followed by `VALIDATE CONSTRAINT` as a
separate statement, which holds ACCESS EXCLUSIVE only for an instant and
validates under a lock that lets writes through. That is deliberately not being
done to `0023`: a migration's digest is frozen the moment it is applied
anywhere, staging has already applied this one, and `migrate` refuses a file
whose digest has changed. If the split is ever wanted it belongs in a later
migration, not in a rewrite of this one.

The two records below are from the earlier rehearsal and are kept because they
say what was observed rather than what was expected. `0001` through `0017` were
applied to a real PostgreSQL 17, seeded with two organizations and three
`network_rules` rows, and then `0018` and `0019` were applied on top.

* **`0018`** adds three nullable columns to `network_rules` and backfills
  `approved_at` from `created_at`. After it ran, zero rules were left pending
  and every existing one carried `approved_at = created_at` with no approver,
  which is the true statement: nobody approved them because there was nothing
  to approve with. **No live egress rule stops enforcing.**
* **`0019`** creates `runtimes`. Row level security is enabled and forced,
  proved not by reading the catalog but by connecting as a real unprivileged
  role that is a member of `antifailure_app`: the other tenant's runtime is
  invisible, a query with no organization set returns zero rows, and an insert
  aimed at another tenant is refused by the policy.

Every one of `0018` to `0023` is additive, which is what makes a rollback safe:
`deploy.sh` can put traffic back on the old revision and cannot un-apply a
schema change, so the old code has to tolerate the new schema. Nothing in the
range drops a column, drops a table, renames anything, or adds a NOT NULL to a
column that already exists, which is the property that lets the currently
deployed revision keep running against the new schema.

## After it is green

### There is no window between publishing and shipping

The installer resolves `latest` from the GitHub releases API. That is good news
with a sharp edge: a new tag is picked up with no further step and nothing to
publish by hand, and it is picked up **the moment the release is created**. The
next person to run the install command gets it, whether or not anybody has
looked at it yet.

So the checks below are not a gate. By the time you run them the download is
already live, and what they decide is whether to announce it and whether to cut
the next patch immediately. If you want a version people cannot reach yet, the
release has to be a GitHub prerelease, which `releases/latest` skips by
definition, and `release.yml` does not currently create one.

That same API has one more property, and it decides whether a recovery works
rather than whether a release does, so it is written out under
[If a release goes out wrong](#if-a-release-goes-out-wrong) where you will need
it: `latest` follows the newest tagged **commit**, not the newest publish.

### Prove the thing a stranger gets

From outside, with nothing of yours in the path.

```sh
curl -fsSL https://antifailure.dev/install.sh | AF_PREFIX=$(mktemp -d) sh
```

Then check that the binary knows what it is:

```sh
af version
```

`version`, `commit` and `built` are stamped by the linker from the tag, the
commit and that commit's own date. A binary reporting `dev`, `none` and
`unknown` means the `-X` flags missed, which is a release to replace rather
than to explain.

Then check production is serving the tag:

```sh
curl -sS https://app.antifailure.dev/readyz
```

## When a stage fails

| Symptom | What it means | What to do |
| --- | --- | --- |
| `gate` times out | No CI conclusion for this commit inside twenty minutes | Nothing deployed. Wait for CI, then re run the `cd` run |
| `sbomcheck` reports a low package count | The bill of materials describes the directory rather than the binaries | Nothing published. Read the unpack stage above it: it printed the binaries it found |
| The tampered file was accepted | cosign is not rejecting a file that does not match its signature | Nothing published, and this is the loudest thing in the pipeline. Do not retry it |
| `MIGRATION FAILED` | The bootstrap job returned Failed or Degraded | No traffic moved. Read the job's logs before retrying. A partly applied schema is not something the script papers over |
| `MIGRATION DID NOT FINISH within the budget` | The job was still running when `deploy.sh` stopped watching | **No traffic moved and nothing was killed.** The shorter budget belongs to the watcher, not to anything that can terminate a replica. Let the job finish, confirm the execution succeeded, then re run the deploy, which will find the schema already up to date. See the note below |
| `NEW REVISION FAILED TO START` | The revision never reached Running | Traffic never moved. The new revision is deactivated |
| `healthy but wrong build` | The origin answers, on the previous commit | The rollout did not happen. This is the check that exists to catch exactly that, and it is doing its job |
| `ROLLED BACK` | The deploy failed and the damage was contained | The previous revision is serving again. The job still fails, which is correct: a successful rollback is not a successful deploy |
| `ROLLBACK DID NOT RESTORE HEALTH` | Both builds are unhealthy | This needs a person. Start at [Operations](/docs/self-hosting/operations) |

### The three migration budgets, and which one can kill something

Three numbers govern the migration step and only one of them can terminate
anything. Written down because working out which is which under pressure is
exactly what a runbook is for.

| Budget | Value | What happens when it runs out |
| --- | --- | --- |
| The migration's own work | measured at about a sixth of a second | Nothing. `0018` and `0019` were timed against 2000 `network_rules` rows, far more than production carries |
| `deploy.sh`'s poll | 60 attempts five seconds apart, so five to seven minutes of wall clock | It stops watching and refuses to move traffic. **It kills nothing.** The job carries on and usually succeeds a moment later |
| The job's `replica_timeout_in_seconds` | 600, with `replica_retry_limit = 2` | The replica is terminated. This is the only budget that can kill a migration, and it is the longest of the three |

So the mismatch is the harmless way round: the shorter budget belongs to the
observer. The failure it produces is a deploy that did not happen while the
schema moved forward, which is recoverable by re running the deploy.

The one path to 600 seconds is not work, it is waiting. `0018` takes an ACCESS
EXCLUSIVE lock on `network_rules` and a SHARE ROW EXCLUSIVE lock on `users` for
its foreign keys, and **nothing sets `lock_timeout` anywhere on this path**, so
it waits for as long as another transaction holds what it needs. The old
revision is still serving while this happens, so a long transaction over
`users` is what would do it.

That is checked against the running server rather than inferred from the
repository. `az postgres flexible-server parameter show -g af-cp-prod-centralus
-s afcpprod-pg -n lock_timeout` returns `0` from `system-default`, and nothing
in the migration path sets one per session either.

This product's own migration linter agrees, and says it better than this page
can. Run against `0018` on a database at `0017`, its `no_lock_timeout` rule
fires and names the mechanism exactly: *"A lock request that is not granted
immediately queues, and every query that arrives after it queues behind the
request rather than behind the table, so a statement that would have taken
milliseconds stops all traffic on `network_rules` for as long as whatever it is
waiting for runs."*

It has never seen these migrations, because `insights.Discover` looks for a SQL
migration directory at the repository root and the control plane's live at
`web/packages/db/migrations`. That is a dogfooding gap rather than a broken
check, and it does not change the risk here: the fix the rule asks for cannot
go into `0018` or `0019` now, because staging has already applied both and
`migrate` refuses a file whose digest has changed. If a `lock_timeout` is
wanted, it belongs on the migration role or in `bootstrap.mjs` before
`migrate()` runs, which covers every migration without editing any of them.

Even then nothing half applies. Each migration file is one transaction and is
recorded in the same transaction that ran it, so a terminated replica drops the
connection, PostgreSQL rolls the file back, and it is not written down as
applied. The retry takes the advisory lock and runs it again from the start.

### If a release goes out wrong

Do not delete the tag and push it again. A tag that changes meaning breaks
everybody who already fetched it, and it breaks the signature's identity
binding, which names the tag. Cut the next patch version and mark the bad
release as such on GitHub.

```sh
gh release edit v0.1.2 --notes "Superseded by v0.1.3. Do not use."
```

**The replacement has to be tagged on a newer commit, and this is the part that
will catch somebody.** GitHub decides which release is latest as *"the most
recent non-prerelease, non-draft release, sorted by the `created_at`
attribute"*, where `created_at` *"is the date of the commit used for the
release, and not the date when the release was drafted or published"*. The
installer follows that, so it follows the newest tagged **commit**, not the
newest publish.

A hotfix cut from an older commit therefore publishes perfectly, reports
nothing wrong, and never reaches a single installer: `latest` stays on the bad
release. There is no error anywhere, and it strikes at precisely the moment
somebody is trying to pull a bad release back.

A patch branched off `main` is always newer, so the ordinary path is safe. The
case to refuse is reverting to an earlier good commit and tagging that. If the
bad release has to be undone rather than moved past, revert the commits on
`main` and tag the revert, so the tagged commit is still the newest one.

```sh
git log -1 --format=%cI v0.1.2      # the bad release's commit date
git log -1 --format=%cI v0.1.3      # must be later than the line above
```

The hosted control plane is a separate decision from the published binary. If
the binary is wrong and production is fine, leave production alone.

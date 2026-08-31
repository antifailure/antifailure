---
title: Cutting a release
description: What a version tag sets off, what green looks like at every stage of it, and what to do when a stage goes red.
sidebar:
  order: 6
---

Pushing a `v*` tag is the single largest thing anybody does to this project. It
publishes the binary that `curl -fsSL https://antifailure.dev/install.sh | sh`
hands to a stranger, and it deploys the hosted control plane that customers are
signed in to. Two workflows fire on the same tag, they run in parallel, and
neither knows the other exists.

This page is the order to do it in and the thing to look at after each step.
[Releases and how to verify one](/docs/security/releases/) is the companion
page, written for the person downloading a release rather than the person
cutting one.

## What one tag sets off

| Workflow | Triggered by | What it does |
| --- | --- | --- |
| `.github/workflows/release.yml` | `push` of a tag matching `v*` | Builds four platforms, packages, signs, and creates the GitHub release |
| `.github/workflows/cd.yml` | `push` to `main` **and** `push` of a tag matching `v*` | Waits for CI, builds the control plane image, deploys staging, then waits for a human to approve production |

Two things follow from that table and both have bitten somebody somewhere.

`release.yml` has no gate of its own. It does not wait for CI and it does not
read `cd.yml`'s verdict. A tag on a red commit publishes binaries. The gate is
you, before you tag.

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
```

`cd.yml`'s first job polls for that same CI conclusion and gives up after
twenty minutes. If CI has not finished when you tag, the tag's deploy fails on
a timeout rather than on anything real.

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
just reproducible
```

`ldcheck` reads the `-X` flags out of `tools/release/build.sh` and proves each
one names a variable that exists. The linker accepts a `-X` for a symbol it
cannot find and says nothing, which is how v0.1.0 shipped four platforms that
all reported themselves as `dev`.

**4. Decide what the release notes say.**

`release.yml` passes `generate_release_notes: true`, so GitHub writes the notes
from the merged pull requests. Nothing reads the fragments under `.changes/`.
If this version deserves a written summary, gather them yourself and paste the
result into the release on GitHub after it is created:

```sh
head -n 1 .changes/*.md | grep -v '^==>' | sort | uniq -c
cat .changes/*.md
```

## Tag and push

```sh
git tag -s v0.1.2 -m "v0.1.2"
git push origin v0.1.2
```

Signed, and pushed on its own. Do not push the tag in the same command as a
branch: a tag that arrives before its commit is on `main` has no CI run for
`cd.yml` to wait for.

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
| Release | The tag appears under Releases with nine assets | The publish itself failed. Nothing was signed with a key, so there is nothing to revoke |

Nine assets: four archives, `checksums.txt` and its bundle, `sbom.spdx.json`
and its bundle, and `THIRD_PARTY_NOTICES.md`.

```sh
gh release view v0.1.2 --json assets --jq '[.assets[].name]'
```

**The signing and the bill of materials have never run in a real release.**
v0.1.1 predates both, and its assets are four archives, `checksums.txt` and
`THIRD_PARTY_NOTICES.md` and nothing else. Every step of both has been
rehearsed locally against real artifacts, with the same syft the workflow pins,
and the keyless certificate is the one part that cannot be exercised outside
GitHub. Read that stage's log rather than its tick.

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

Two things that are fine and are worth knowing anyway. The app is in `Multiple`
revision mode with one revision at 100 percent, so there is a revision to roll
back onto; the case where there is not is the one `deploy.sh` reports plainly
rather than pretending a rollback happened. And production is at migration
`0017`, so a tag from here applies `0018` and `0019`, both of which are
additive: `0018` adds three nullable columns to `network_rules` and backfills
`approved_at` from `created_at` so no live egress rule stops enforcing, and
`0019` creates `runtimes` with row level security enabled and forced.

## After it is green

Prove the thing a stranger gets, from outside, with nothing of yours in the
path.

```sh
curl -fsSL https://antifailure.dev/install.sh | AF_PREFIX=$(mktemp -d) sh
```

The installer resolves `latest` from the GitHub releases API, so a new tag is
picked up with no further step and nothing to publish by hand. Then check that
the binary knows what it is:

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
| `MIGRATION DID NOT FINISH within the budget` | The job was still running after five minutes | No traffic moved, and the job itself is not dead: its `replicaTimeout` is ten minutes, twice the script's poll. Let it finish, confirm the execution succeeded, then re run the deploy, which will find the schema already up to date |
| `NEW REVISION FAILED TO START` | The revision never reached Running | Traffic never moved. The new revision is deactivated |
| `healthy but wrong build` | The origin answers, on the previous commit | The rollout did not happen. This is the check that exists to catch exactly that, and it is doing its job |
| `ROLLED BACK` | The deploy failed and the damage was contained | The previous revision is serving again. The job still fails, which is correct: a successful rollback is not a successful deploy |
| `ROLLBACK DID NOT RESTORE HEALTH` | Both builds are unhealthy | This needs a person. Start at [Operations](/docs/self-hosting/operations/) |

### If a release goes out wrong

Do not delete the tag and push it again. A tag that changes meaning breaks
everybody who already fetched it, and it breaks the signature's identity
binding, which names the tag. Cut the next patch version and mark the bad
release as such on GitHub.

```sh
gh release edit v0.1.2 --notes "Superseded by v0.1.3. Do not use."
```

The hosted control plane is a separate decision from the published binary. If
the binary is wrong and production is fine, leave production alone.

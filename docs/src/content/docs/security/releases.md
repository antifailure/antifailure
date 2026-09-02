---
title: Releases and how to verify one
description: What a release is made of, how to check that what you downloaded is what we published, and how to rebuild it yourself and compare.
sidebar:
  order: 1
---

The install command on the front page pipes a script into a shell. That is
convenient and it means the release artifacts are the security boundary for
everybody who uses this product. This page is how you stop taking our word for
it.

Three checks are available, and they answer different questions:

| Check | Question it answers |
| --- | --- |
| Checksum | Did the file arrive intact? |
| Signature | Did we publish it? |
| Rebuild | Was it built from the source it claims? |

The first is the weakest and the fastest. The third is the strongest and takes
a few minutes. Most people should do the first two.

## What a release contains

Each tag publishes four archives, one per platform, plus the files you check
them with.

| File | What it is |
| --- | --- |
| `antifailure_<version>_<os>_<arch>.tar.gz` | The `af` binary, the agent runner's source, the licence and the README |
| `checksums.txt` | The SHA256 of every archive |
| `checksums.txt.sigstore.json` | A signature over `checksums.txt`, with the certificate that made it |
| `sbom.spdx.json` | An SPDX bill of materials, read out of the built binaries |
| `sbom.spdx.json.sigstore.json` | A signature over the bill of materials |
| `THIRD_PARTY_NOTICES.md` | Attribution, generated from what is actually linked, as the union over all four platforms |

Only `checksums.txt` is signed rather than each archive. That is deliberate.
`checksums.txt` names every archive by its hash, so one signature covers all of
them, and checking it is two commands instead of eight. Eight things to check
get checked zero times.

## Check the checksum

The installer does this for you and refuses to install a file that does not
match. If you downloaded an archive by hand:

```sh
sha256sum --check --ignore-missing checksums.txt
```

On macOS, `shasum -a 256 -c --ignore-missing checksums.txt`.

This proves the file is not corrupt. It proves nothing about who wrote
`checksums.txt`, which is what the signature is for.

## Check the signature

Everything on this page works from v1.0.0 onwards. v0.1.0 and v0.1.1 were built
before the signing and the reproducible archives existed, so they carry no
`.sigstore.json` bundle and rebuilding them does not produce the bytes that were
published. A release that ran these steps carries `checksums.txt.sigstore.json`
and `sbom.spdx.json`; a release that carries neither did not, and that is a
thing you can check rather than take on trust.

Install [cosign](https://docs.sigstore.dev/cosign/system_config/installation/).
The identity is long and you need it three times, so name it once:

```sh
TAG=v1.0.0
REPO=antifailure/antifailure
WORKFLOW=.github/workflows/release.yml

IDENTITY="https://github.com/$REPO/$WORKFLOW@refs/tags/$TAG"
ISSUER="https://token.actions.githubusercontent.com"
```

Then check the checksums:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer "$ISSUER" \
  checksums.txt
```

`Verified OK` means the file is the one that was signed.

There is no public key to fetch, because there is no signing key. The release
workflow asks GitHub for a short lived identity token, proves to Sigstore that
this workflow in this repository is running, and gets a certificate that expires
almost immediately. Nothing is stored, so there is nothing to leak and nothing
to rotate.

`--certificate-identity` is the part that makes this mean anything. Without it
you would be checking that somebody signed the file, which anybody can do.
With it you are checking that this workflow, in this repository, at this tag
signed it. If you leave it out, cosign refuses rather than checking less.

The bill of materials is signed the same way, with its own bundle:

```sh
cosign verify-blob \
  --bundle sbom.spdx.json.sigstore.json \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer "$ISSUER" \
  sbom.spdx.json
```

### Proving your check can fail

A verification you have only ever run against good input has told you nothing.
Change a byte and watch it refuse:

```sh
cp checksums.txt tampered.txt
printf 'x' >> tampered.txt
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer "$ISSUER" \
  tampered.txt
```

That must fail. The release workflow runs this same pair, the good file and the
tampered copy, on every release, and refuses to publish if the tampered one is
accepted.

## Rebuild it yourself

The archives are reproducible. Building a tag again produces the same bytes, so
you can compare a hash you computed against the one we published instead of
trusting either of us.

```sh
git clone https://github.com/antifailure/antifailure
cd antifailure
git checkout v1.0.0
./tools/release/build.sh linux amd64 1.0.0 \
  "$(git rev-parse HEAD)" "$(git show -s --format=%cI HEAD)" dist stage
sha256sum dist/antifailure_1.0.0_linux_amd64.tar.gz
```

That hash should be the line for your platform in `checksums.txt`. You need the
same Go version the release used, which is the one in `engine/go.mod`.

Three things make this work, and all three are load bearing:

* `-trimpath`, so the directory you built in does not reach the binary.
* The build date comes from the commit, not from the clock. Every build of one
  commit therefore agrees.
* The archive is written by `tools/reltar` rather than by `tar`, with a fixed
  modification time, no ownership, normalised permissions and sorted entries.

Without the third the binaries matched and the archives never did. `tar` takes
each entry's timestamp from the filesystem and `gzip` writes another into its
own header, so two builds a minute apart produced two different archives of one
identical binary. That is fixed, and `just reproducible` builds twice in two
directories and compares, on every pull request.

### What reproducibility here does and does not cover

Covered: the four release archives and the binaries inside them.

Not covered: `sbom.spdx.json`. An SPDX document records the moment it was
created and a unique document namespace, so two runs differ by design. Verify
it with its signature, not by rebuilding it.

## The bill of materials

`sbom.spdx.json` lists what is inside the binaries. It is read out of the built
artifacts rather than generated from `go.mod`, because Go records the module
graph it actually linked inside the binary. Reading the artifact answers what
shipped; reading `go.mod` answers what was asked for. Those differ whenever a
build constraint or a pruned dependency changes what the linker kept.

Every release runs `tools/sbomcheck` over it before publishing. That validates
the document against the published SPDX 2.3 schema and then asks the question a
schema cannot: does it record the SHA256 of every binary that actually ships. A
bill of materials can be perfectly valid SPDX and describe nothing at all, which
is exactly what this one did before the check existed.

One gap, stated rather than left to be found: the agent runner ships as source
with `playwright` declared as a version range, resolved on your machine when you
run `af runner install`. The bill of materials covers the Go dependencies
compiled into `af` and cannot name a runner dependency version that is not
chosen yet.

## Cutting a release

For maintainers. Everything below runs from a tag and nothing runs from a
branch, because a release built from a branch is a release nobody can reproduce.

The same tag also deploys the hosted control plane, which this page does not
cover because it is not something a person verifying a download needs to know.
[Cutting a release](/docs/self-hosting/releasing) is the operational runbook:
what green looks like at every stage of both workflows, and what to do when one
of them goes red.

1. Confirm the gates are green on the commit you are about to tag. `just gate`
   locally, and CI green on the merge.
2. Write the release's section in `CHANGELOG.md`, headed `## vX.Y.Z`. The
   release publishes that section and nothing else, so a tag with no section, or
   with a heading and nothing under it, does not publish at all. `just relnotes`
   is that check and it runs on every pull request.
3. Tag and push:

   ```sh
   git tag -a v1.0.0 -m "v1.0.0"
   git push origin v1.0.0
   ```

   The tag is annotated and carries no signature. What is signed is
   `checksums.txt` and the bill of materials, by the publish job, which is what
   the verification steps above check. `git verify-tag` on a release tag of
   ours answers "no signature found", and that is the honest answer rather than
   a broken one. [Signing the tags too](#signing-the-tags-too) is what to set
   up if you want it to answer differently.

4. Watch `.github/workflows/release.yml`. It builds four platforms, packages
   each with `tools/release/build.sh`, unpacks them so the bill of materials can
   read the binaries, signs `checksums.txt` and the bill of materials, verifies
   both signatures, proves a tampered file is rejected, and only then creates
   the release.
5. Check the published artifacts the way this page tells a user to. If the
   instructions do not work, the release is not done.
6. **After the tag has published, and in its own commit,** bump the Terraform
   `image_tag` defaults in `infra/terraform/stacks/control-plane/variables.tf`
   and `infra/terraform/modules/control-plane/variables.tf` to the new tag.

Step 6 is separate on purpose and it is the one step here that must not be done
early. Those defaults are live: `azurerm_container_app_job.maintenance` reads
the image with no `ignore_changes`, so an apply from `main` takes whatever they
say. A default naming a tag that has not published yet does not produce a stale
deployment, it produces a failed apply on the stack that runs the product.
`tools/tagsync` is that ordering as a gate, so the mistake is a red check rather
than a bad afternoon.

Step 6 is a person's job on purpose, and it is not an oversight waiting to be
automated. A release job that opened the bump as a pull request would need
`contents: write` and `pull-requests: write` on a workflow whose stated rule is
that only the publishing job gets write at all, and widening that surface is a
change that deserves its own review rather than riding along with a release.
The risk worth removing was the silent one, doing the bump too early, and
`tagsync` removes it. Doing it late costs a stale default and nothing else.

Pushing the tag also publishes `ghcr.io/antifailure/control-plane:<tag>` and
**moves `:latest` onto it**, which changes what anybody self hosting off
`latest` gets on their next pull. Say so in the release notes.

The workflow fails rather than publishing when any of those checks fail. That
ordering is the point: every previous version of this pipeline signed and
published first and verified never.

### Signing the tags too

Optional, and nobody has done it. A signed tag would say which maintainer cut
the release. The artifact signature says something different and stronger: that
this workflow, in this repository, at this tag produced the files. So a tag
signature adds a second smaller claim, and its absence takes nothing away from
the one you can already check.

Setting it up is the account owner's work rather than the release pipeline's,
because it means holding a private key. Four steps, once:

1. Have a key. An SSH key you already use is enough, or make a GPG key with
   `gpg --full-generate-key`.
2. Tell git which key signs, and in which format:

   ```sh
   git config --global gpg.format ssh
   git config --global user.signingkey ~/.ssh/id_ed25519.pub
   ```

   With GPG instead, leave `gpg.format` unset and give `user.signingkey` the
   key id.
3. Add the public half to your GitHub account as a signing key, under Settings,
   SSH and GPG keys. Skip this and the signature is still good, and GitHub
   still shows the tag as unverified, because it has nothing to check against.
4. Turn it on for every tag, so a forgotten flag cannot quietly produce an
   unsigned one:

   ```sh
   git config --global tag.gpgsign true
   ```

Step 3 of the runbook then becomes `git tag -s`, and `git verify-tag v1.0.0`
starts answering. Until somebody does that, this page describes what the
repository does rather than what it could do.

### If a release goes out wrong

Do not delete the tag and re-push it. A tag that changes meaning breaks
everybody who already fetched it, and it breaks the signature's identity
binding, which names the tag. Cut a new patch version instead and mark the bad
release as such on GitHub.

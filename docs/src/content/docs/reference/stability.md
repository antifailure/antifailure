---
title: What is stable
description: The surfaces version 1 promises to keep working, the ones it deliberately does not, and what a major version costs.
sidebar:
  order: 10
---

Antifailure follows [semantic versioning](https://semver.org). A major version
is the only thing that may break a surface named as stable below, and the
release notes for it say what changed and what to do.

This page is the promise itself rather than a summary of it. It is deliberately
a list of named surfaces and not a sentence about "the API", because a blanket
claim is one nobody can hold us to and one we cannot check ourselves against.

## Stable

Breaking any of these costs a major version.

### The manifest

A manifest declaring `version: 1` keeps working. Within version 1:

- Keys may be added, and an existing key may gain a new accepted value.
- A key will not be removed, renamed, or given a different meaning.
- A default will not change in a way that changes what an existing manifest
  does.

The promise runs backwards, not forwards. An older manifest works on a newer
`af`; a manifest using a key added in 1.4 does not work on 1.2, because the
parser refuses a key it does not know rather than ignoring it. That refusal is
deliberate: a silently ignored key is a setting somebody believes is in force.

`schemas/manifest.v1.json` is the source of truth, the Go types mirror it, and a
test walks both structurally so the two cannot drift apart in a release. A
manifest written today parses in every 1.x that follows.

If a version 2 ever exists, version 1 manifests keep being accepted for the
whole of the major version that introduces it. You will not be asked to rewrite
a manifest to take a patch release.

### The command line

The commands in the [command reference](/docs/reference/cli), their flags, and
their exit codes. A command will not be removed or renamed and a flag will not
change what it means. New commands and new flags arrive in minor releases.

### `--output json`

The documented fields of each command's JSON output. Fields may be added, so
parse for the fields you want rather than refusing a document that carries one
you have not seen. A documented field will not be removed or change type.

### The provider interfaces

`engine/pkg/provider` declares the database and runtime interfaces, and it is
meant to be implemented outside this repository: each ships with a conformance
suite an implementation runs, so conformant is something a test says.

Four packages are stable, and they are stable together because an interface is
only as usable as the types its signatures name.

| Package | What it is |
| --- | --- |
| `engine/pkg/provider` | The database and runtime interfaces themselves. |
| `engine/pkg/schema` | The manifest types those interfaces carry across the boundary. |
| `engine/pkg/secret` | The `Value` type that carries a credential without printing it. `Database.ConnString` returns one and `EnvSpec` holds several. |
| `engine/conformance` | The suite that decides whether an implementation is conformant. |

`engine/pkg/secret` is new in 1.0.0 and it is the fix for a promise that was
not true. The type lived in `engine/internal/secrets` until the release, and
`Database.ConnString` returned it, so writing that method outside this module
was impossible: naming the return type needed an import the Go toolchain
refuses by path. The interface compiled here, reviewed as correct, and would
have failed on the first line of the first provider anybody wrote. Moving the
type is the only change to these interfaces, it is source compatible inside the
module because the old name is an alias, and `tools/surfacecheck` is what stops
the next one happening quietly.

### The error codes

A code in the [error reference](/docs/reference/errors) keeps its meaning. The
code is the stable identifier for a refusal; the sentence printed beside it is
not, and it is reworded whenever a clearer one exists. Match on the code.

## Not stable

These are free to change in a minor release, and saying so plainly is more
useful than a promise that quietly bends.

- **The Helm chart's values and the Terraform module's variables.** The chart
  carries its own version, which is why it is not at 1.0.0 alongside the engine.
- **Most of the control plane's HTTP API.** It is mostly how the console and
  the engine speak to each other rather than a published integration surface,
  and the part that is published is named rather than described. Every route
  the router serves is classified in `web/apps/api/src/boundary.ts` as either
  part of the published contract, which means it appears in
  [the OpenAPI document](https://antifailure.dev/openapi.json), or as
  deliberately excluded on one of seven recorded grounds, with a sentence
  saying which case it is. A route that is neither fails the build. Before that
  existed, a route missing from the document could equally mean "nobody outside
  could call it" or "somebody forgot", and four live routes under
  `/v1/oidc/bindings` were the second. The prose form of the same boundary is
  the [HTTP endpoints reference](/docs/reference/api).
- **Every Go package except the four named above.** `engine/pkg/afcli`,
  `engine/pkg/edition` and `engine/pkg/extension` are the sockets the enterprise
  binary plugs into and are deliberately narrow rather than a general embedding
  API. `engine/pkg/livekey` and `engine/chaos` are ours. Every importable
  package is listed with its classification and a reason in
  `engine/api/packages.txt`, and a new one that is listed nowhere fails the
  build rather than arriving public by default. Nothing outside this module can
  import `engine/internal` at all: the Go toolchain refuses an import of an
  internal path from outside the subtree rooted at its parent, so that half
  needs nothing from us and gets nothing.
- **Lint rule names and their findings.** Rules are added and sharpened, and a
  release may find something in a migration an earlier one passed. That is the
  product working. Within a release the rule name identifies the finding.
- **The event stream's set of types.** Types are added as features land.
- **Anything under `docs/plan/`.** Working notes, not documentation.

## What holds these lines

Each of the two carve-outs above is checked rather than described, and both
checks run in CI and in `just gate`.

`tools/surfacecheck` reads the Go tree and refuses:

- a Go module in the repository that nothing says anything about, and an
  importable package inside a shipped one that nothing classifies;
- a change to a stable package that version 1 does not allow, measured against
  `engine/api/v1.0.0.txt`, which records the exported surface as it stood at
  the tag. Adding an export passes. Removing one, changing a signature,
  changing an exported constant's value, and adding a method to an interface
  published for implementing do not;
- an exported signature in a stable package naming a type from a package that
  is not stable, which is the one that was already broken.

`web/apps/api/test/route-boundary.test.ts` asks the control plane's router for
its own route table and holds the answer against the published document both
ways: a route classified as contract that the document does not carry fails,
and a route classified as excluded that it does carry fails too. The check
before it compared the published file to what the generator declares, which is
the file against itself, so a route the generator never mentioned was missing
from both sides and the comparison stayed green.

## Deprecation

A stable surface that is going away is deprecated first, not removed. A
deprecated flag or key keeps working for the rest of the major version, the
release notes name what to use instead, and removal waits for the next major
version. Nothing is deprecated today.

## Versions

Released versions are the git tags in this repository, and the version a binary
reports is stamped into it at release time. `af version` prints it, with the
commit and the build date, and `af version --output json` is the machine
readable form.

Every release is signed and carries a bill of materials.
[Releases and reproducibility](/docs/security/releases) has the commands to
verify one and to rebuild the archives yourself.

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

### The lint finding identifiers

Every migration lint finding carries an identifier of the form `LINT-NNN`, and
the [lint findings reference](/docs/reference/lint-findings) lists them. An
identifier is assigned once and keeps its meaning. It is never reused, not even
after the rule that earned it is deleted, because a number handed out twice is
worse than one that changed: the first breaks a filter silently and the second
breaks it loudly.

What stays free to move is everything else about a finding, and deliberately
so. The rule name, the title, the sentence saying what will happen and the
suggested fix are prose. Rules are sharpened, split and renamed as they get
better at their job, and a name that cannot be improved is a rule that cannot
be improved. So the identifier is what a filter or a suppression should match
on, and the rule name is what a person should read.

`engine/internal/insights/lintcatalog.yaml` is the source of truth, and
`findings.register.json` beside it records every identifier ever handed out.
`tools/lintcheck` refuses a rule with no identifier, an identifier for a rule
that no longer exists, and an identifier that has left the catalogue since it
was registered.

### The self-hosting configuration

Every key in the Helm chart's `values.yaml`, and every variable and output in
the Terraform under `infra/terraform`. Within version 1:

- A key or a variable will not be removed or renamed.
- Its type will not change.
- An optional input will not become required, and a new input arrives with a
  default rather than without one.

The reason this is a promise and not a preference is that the values file and
the tfvars file somebody self hosting writes are their configuration. They are
written once, kept in that operator's own repository, and applied by that
operator's own pipeline. A rename does not fail that pipeline loudly, it fails
it silently: Helm accepts a key no template reads, and Terraform only warns
about a variable nothing declares. The setting stops being in force and the
apply still says it succeeded.

Terraform outputs are on the list by name, because a runbook reads them.
[Standing up on Azure](/docs/self-hosting/azure) pipes `backend_hcl` into a
backend configuration and [rotating
secrets](/docs/self-hosting/rotating-secrets) scopes a role assignment with
`key_vault_id`, and an output missing under the name a command asks for prints
nothing rather than failing.

What is promised is the input, not the value it carries. Defaults move, and one
of them has to: `image_tag` names the release being cut, and `tools/tagsync`
exists to make sure it does.

`tools/inputcheck` holds the tree to a snapshot of this surface taken at
v1.0.0, so a rename fails in the pull request that proposes it rather than in
somebody's upgrade.

The chart carries its own version, and it is 1.0.0 for this reason. A chart at
0.x says in the only language its ecosystem has that its values may be
rearranged at any time.

### The event stream

The types in the [event envelope reference](/docs/reference/schemas/events-v1)
and the envelope around them. A type is not removed and does not change what it
means. A field of the envelope is not removed, does not change type, and does
not become optional, and a field holding a closed set does not lose a value
from it.

Types are added as features land and fields may be added, so read the stream
the way you read `--output json`: take what you want and ignore what you have
not seen, rather than refusing an event carrying something new.

Two things are deliberately outside that. The `data` object is the type
specific payload, it is documented as an object and nothing further, and its
keys move with the code that writes them. And some types on that page are
reserved rather than live: the engine does not emit all of them yet, and
`engine/internal/events/emitters_test.go` carries the reason for each one. A
reserved type is stable in the sense above, and it may start being emitted in
any release.

`schemas/events.v1.json` is the published artifact,
`engine/internal/events/stream.register.json` is what version 1 promised, and
`tools/eventcheck` fails the build on a type that has gone, a field that has
changed shape, and a type the engine can emit that nothing documents.

## Not stable

These are free to change in a minor release, and saying so plainly is more
useful than a promise that quietly bends.

- **The defaults and validation rules on the self-hosting inputs.** The names
  and the types are promised above. A default moves with a release, and a
  validation tightens as a cloud teaches us what it refuses at apply time that
  it accepted at plan time. Set the values that matter to you rather than
  inheriting them.
- **What the Terraform actually creates.** The inputs are a contract; the
  resources behind them are not. A module may reach the same outcome with
  different resources, and the Azure guide says which changes force a replace.
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
- **Lint rule names, and which findings a release reports.** A rule is renamed
  when a clearer name exists, and a release may find something in a migration
  an earlier one passed. That is the product working, and it is why the
  identifier above is the thing to match on rather than the name.
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

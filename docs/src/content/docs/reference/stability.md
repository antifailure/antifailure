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
suite an implementation runs, so conformant is something a test says. Those
interfaces and the conformance suite are stable, along with `engine/pkg/schema`,
which they carry across the boundary.

### The error codes

A code in the [error reference](/docs/reference/errors) keeps its meaning. The
code is the stable identifier for a refusal; the sentence printed beside it is
not, and it is reworded whenever a clearer one exists. Match on the code.

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
- **The control plane's HTTP API.** It is how the console and the engine speak
  to each other, not a published integration surface. The endpoints that are
  published as an interface are named in the
  [HTTP endpoints reference](/docs/reference/api).
- **Every Go package except the two named above.** `engine/pkg/afcli`,
  `engine/pkg/edition` and `engine/pkg/extension` are the sockets the enterprise
  binary plugs into and are deliberately narrow rather than a general embedding
  API. Nothing outside this module can import `engine/internal` at all, which
  is deliberate.
- **Lint rule names and their findings.** Rules are added and sharpened, and a
  release may find something in a migration an earlier one passed. That is the
  product working. Within a release the rule name identifies the finding.
- **The event stream's set of types.** Types are added as features land.
- **Anything under `docs/plan/`.** Working notes, not documentation.

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

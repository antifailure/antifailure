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

- **The Helm chart's values and the Terraform module's variables.** The chart
  carries its own version, which is why it is not at 1.0.0 alongside the engine.
- **The control plane's HTTP API.** It is how the console and the engine speak
  to each other, not a published integration surface. The endpoints that are
  published as an interface are named in the
  [HTTP endpoints reference](/docs/reference/api).
- **Every Go package except the two named above.** `engine/pkg/afcli`,
  `engine/pkg/edition` and `engine/pkg/extension` are the sockets the enterprise
  binary plugs into and are deliberately narrow rather than a general embedding
  API. Nothing outside this module can import `engine/internal` at all, which
  is deliberate.
- **Lint rule names, and which findings a release reports.** A rule is renamed
  when a clearer name exists, and a release may find something in a migration
  an earlier one passed. That is the product working, and it is why the
  identifier above is the thing to match on rather than the name.
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

---
title: Licensing
description: What is MIT, what is not, and how a license is verified.
sidebar:
  order: 1
---

Everything in this repository is MIT licensed except the `ee/` directory, which
is under the Antifailure Enterprise License. The community build does not
contain `ee/` at all: it is a separate Go module the community build cannot
resolve, and CI has a job that fails if the community binary carries an
enterprise symbol.

That is stronger than a runtime check. A feature you cannot compile is a feature
that cannot be switched on by patching a boolean.

## Installing a license

There is nothing to install. The enterprise binary reads its license from the
environment and stores nothing, so a license is two variables set wherever the
engine runs:

```sh
export AF_LICENSE_KEY=<token>
export AF_ORG=globex
af license status
```

That is deliberate rather than unfinished. A key on disk is a key that outlives
the machine it was put on, survives a rollback, and has to be removed from every
copy. Two variables are removed by unsetting them, and every enterprise setting
is preserved when they are gone: features fall back to the community behaviour
rather than failing.

So `af license install` and `af license remove` exist and both say so instead of
pretending. On the enterprise binary they name these variables; on the community
binary they refuse outright, because storing a key that build can never act on
would leave somebody believing enterprise features are on until the rollout they
bought the license for.

A license is an Ed25519 signed statement carrying the organisation it was issued
to, the features it permits, the seat count, when it expires, and which key
signed it. Verification is a signature check against keys stamped into the
binary at release; it needs no network, which is what makes an air gapped
installation possible.

An installation that mints its own licenses supplies its key in
`AF_LICENSE_PUBLIC_KEYS`, as `kid=base64,kid=base64`. Those are merged with the
build's own rather than replacing them, taking precedence on a shared
identifier, because trusting your own key must not stop the vendor's from
working.

## When it does not verify

```
AF-EE-001 The enterprise license could not be verified.
  Next: Reinstall the license with 'af license install'; the token may have been
  truncated in transit.
```

Almost always truncation. A license token is long and survives being pasted into
a chat window less often than people expect.

## Wrong organisation

```
AF-EE-003 This license was issued for organization acme and this instance is
globex.
  Next: Install the license issued for globex.
```

The organisation is inside the signature, so a license cannot be edited to name
a different one. This is what stops a key being passed around.

## Clock

```
AF-EE-002 The system clock is 3 days behind the last time this license was
seen.
  Next: Correct the system clock. Enterprise features resume once it passes the
  recorded time.
```

Expiry is checked against the clock, and a clock that can be moved backwards is
an expiry that can be avoided. The last seen time is recorded, so going
backwards is detected rather than believed. Correcting the clock resolves it;
nothing has to be reinstalled.

## Seats

```
AF-EE-004 The license covers 25 seats and they are all in use.
  Next: Remove an inactive member, or ask for more seats at
  https://antifailure.dev/contact. No existing member was removed.
```

The last sentence is the important one. Reaching a seat limit refuses the
addition and never evicts somebody to make room.

## Expiry and grace

An expired license keeps working for a grace period, with a warning on every
command. Enterprise features that stop working the moment a renewal is late
turn a billing delay into an outage, and nothing in `ee/` is worth doing that
for.

After the grace period the enterprise features stop and everything else carries
on. The community edition is the whole product minus `ee/`, and an expired
license leaves you with it rather than with nothing.

## What is in `ee/`

`sso`, `scim`, `rbac`, `audit_stream`, `policy_enforcement`, `multi_runtime`,
`enterprise_secrets`, `billing`, `enterprise_dashboard`, `support_access`,
`compliance_packs`, `air_gapped`, `cloud_database`, `cloud_runtime`.

Each is named in the license, so a license permits exactly what was bought.

### Two of those cannot be sold

`billing` and `enterprise_dashboard` are names in the catalogue and nothing
else. There is no implementation of either, so there is nothing a license could
switch on, and both are refused twice: `tools/licensegen` will not sign a
request naming one, and the verifier carries the name through and never permits
it.

That is deliberate rather than an oversight waiting to be tidied. The
alternative, a license check placed in front of a capability that does not
exist, is a declared enforcement site that can never run, which reads as a
working feature from every direction and is harder to find than the gap it
covers. A feature nobody can buy and nobody can be granted cannot be mistaken
for one that ships.

### Two more are not enforced

A third case, and a different one from the two above: `rbac` and `air_gapped`.
Both name something real, and in neither case is the license what provides it.

The custom roles library is complete and tested, and nothing stores a role
model, so an organization has no way to have one. Air gapped operation is a
property every installation already has, licensed or not: verification is a
signature check against keys stamped into the binary, so it needs no network
whichever features a license names.

They are reported and not enforced, and that is written down rather than gated,
for the reason the paragraph above gives: a check on a path nothing reaches is
worse than no check. Unlike `billing` and `enterprise_dashboard` they are not
refused at issue, because refusing them would refuse a customer a capability
they can have. `tools/licensegen` prints a warning naming them beside the key it
signs instead, so that whoever issues it reads what the license does and does
not grant before a customer asks.

`air_gapped` was in this state and said so nowhere until 2026-09-08. Every
occurrence of the name in the repository was a copy of the catalogue, the
license vectors, a line of documentation, or a test, so a license naming it
verified, reported itself active, printed in `af license status`, and granted
nothing. That is the same failure the two refused names above exist to prevent,
reached through the case they do not cover.

All three lists are held to the code by a test rather than by a habit.
`notShipped` and `unenforced` in `ee/engine/license/license.go` are the single
place each statement lives, and this page, the generator and the enterprise
feature registry are all checked against them in both directions. A fourth
check asks the question none of those could: that every feature a license can
grant is refused, recorded as unenforced, or gated at a real site in one half of
the product or the other.

## Contributing

Contributions are under the DCO, not a CLA. You keep your copyright. See
`CONTRIBUTING.md`.

Related: [policy](/docs/enterprise/policy), [runtimes](/docs/enterprise/runtimes),
[air gapped](/docs/enterprise/air-gapped).

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

<!-- entitlement-names:start -->
The features a license can name are `air_gapped`, `audit_stream`, `billing`, `cloud_database`, `cloud_runtime`, `compliance_packs`, `enterprise_dashboard`, `enterprise_secrets`, `multi_runtime`, `policy_enforcement`, `rbac`, `scim`, `sso` and `support_access`.
<!-- entitlement-names:end -->

<!-- entitlement-count:start -->
Of the 14 features a license can carry, **11 are refused when the license does not name them**, 8 by the engine and 4 by the control plane, with some checked by both. The rest are listed here anyway, with what actually happens without each one, because a feature that is sold and never checked is worth knowing about and the number is only useful if it can come back unflattering.
<!-- entitlement-count:end -->

The table is generated from `ee/engine/feature/catalogue.go`, which is the one
place this product records what a license permits. Every row saying a feature is
refused names the file that refuses it, and a test opens that file and requires
the call that actually refuses: `feature.Enabled` for a row the enterprise
engine gates, `edition.Permits` for one the community engine gates by name. So a
row cannot claim an enforcement it does not have. Which of the two is required
is decided by the row's own state and never by the shape of the path, because a
check that guessed from the path would accept an enterprise file for a community
gate and never notice that the mechanism claimed is not the mechanism there.

Rows that say nothing changes are the honest answer rather than an omission: a
feature that is sold and never checked is a gap worth publishing, and this page
is where it gets published.

<!-- entitlements:start -->
| Feature | What it is | Without it |
| --- | --- | --- |
| `air_gapped` | An installation that reaches nothing outside the operator's own network. | Withheld. `airgapped/airgapped.go:RegisterFromEnvironment` asks the license, and the feature is off when the answer is no. |
| `audit_stream` | Privileged actions forwarded to the organization's own SIEM. | Withheld by both the engine at `auditsink/auditsink.go:auditsink.permitted` and the control plane at `ee/web/server/src/register.ts:startAuditStream`. Each checks the license. |
| `billing` | Subscriptions, invoices and the plan an organization is on. | Nothing changes, because the capability is not built yet. |
| `cloud_database` | Managed cloud database providers, the ones that need an organization behind them rather than a developer's own card. | Withheld. `cloudgate/cloudgate.go:gatedDatabase.Branch` asks the license, and the feature is off when the answer is no. |
| `cloud_runtime` | Managed cloud runtime providers, on the same rule as the databases. | Withheld. `cloudgate/cloudgate.go:gatedRuntime.Up` asks the license, and the feature is off when the answer is no. |
| `compliance_packs` | SOC 2 and ISO 27001 evidence gathered from the control plane's own records. | Withheld. `compliance/command.go:Command` asks the license, and the feature is off when the answer is no. |
| `enterprise_dashboard` | The console: environments, masking, egress, audit and workloads. | Nothing changes, because the capability is not built yet. |
| `enterprise_secrets` | Declared variables resolved from Vault or a cloud secret manager. | Withheld. `secrets/source.go:Source.Available` asks the license, and the feature is off when the answer is no. |
| `multi_runtime` | Placing an environment across several runtimes at once, by requirement and by tag. | Withheld. `engine/internal/env/env.go:Orchestrator.placement` asks the license, and the feature is off when the answer is no. |
| `policy_enforcement` | Organization policy that refuses an environment the manifest would have allowed. | Withheld. `policyenforce/policyenforce.go:Hook.Check` asks the license, and the feature is off when the answer is no. |
| `rbac` | Custom roles: a role an organization defines, granted to a member at a scope, on top of the four built-in roles. | Withheld by the control plane. `ee/web/rbac/src/enforce.ts:customRoleResolver` asks the license, and an unlicensed installation is answered 402 naming the feature rather than 404. |
| `scim` | Directory provisioning, so joiners and leavers arrive from the identity provider. | Withheld by the control plane. `ee/web/scim/src/routes.ts:guard` asks the license, and an unlicensed installation is answered 402 naming the feature rather than 404. |
| `sso` | Single sign on against the organization's own identity provider. | Withheld by the control plane. `ee/web/sso/src/store.ts:connectionByHandle` asks the license, and an unlicensed installation is answered 402 naming the feature rather than 404. |
| `support_access` | A supported way for the vendor to see what a customer sees. | Nothing changes. It is implemented and deliberately available to everyone. |
<!-- entitlements:end -->

The distinction in the third column between a feature that is refused and one
the hosted control plane covers under its plan is the one worth reading twice. A
license carries fourteen names and the hosted plan gate carries one boolean, so
a license naming a feature and a plan that does not are not reconcilable by
anything. Both are real refusals and only the first is keyed on what was bought.

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

### Custom roles were in a third state until 2026-09-11

`rbac` named something real that the license did not provide. The custom roles
library was complete and tested, and nothing stored a role model, so no
organization could have one. It was reported and not enforced, written down
rather than gated, because a check on a path nothing reaches is worse than no
check, and `tools/licensegen` printed a warning naming it beside every key it
signed.

That stopped being true on 2026-09-11. The enterprise control plane now stores a
role model per organization, mounts the routes that define one, and installs the
resolver every permission check asks, and that resolver asks the license and
then the organization's entitlement before a stored grant widens anything. The
table above reads Withheld for `rbac`, and the site it names is the one that
asks. The warning is gone with the state it described. The four built-in roles
are unchanged and are not what the license sells: every organization on every
plan has them. [Custom roles](/docs/enterprise/custom-roles) says how a model is
written, reviewed and applied.

`air_gapped` was in this state and said so nowhere until 2026-09-08. Every
occurrence of the name in the repository was a copy of the catalogue, the
license vectors, a line of documentation, or a test, so a license naming it
verified, reported itself active, printed in `af license status`, and granted
nothing. It is no longer in this state and this page said it was for a day
longer than it was true: the paragraph naming it stayed here while the gate that
made it false landed in another pull request, which is the same stale sentence
this catalogue exists to refuse and is why the row above is generated from the
code rather than written beside it. The table now reads Withheld for
`air_gapped`, and the site it names is the one that asks.

All three lists are held to the code by a test rather than by a habit.
`notShipped` in `ee/engine/license/license.go` is the single place the refused
set lives, and this page, the generator and the enterprise feature registry are
all checked against it in both directions. A fourth check asks the question none
of those could: that every feature a license can grant is either refused or
gated at a real site in one half of the product or the other. A third answer,
built and deliberately gated nowhere, was recorded in a map called `unenforced`
until custom roles, its last entry, were gated, and `license.go` says how to
bring it back with its checks if a feature is ever in that state again.

## Contributing

Contributions are under the DCO, not a CLA. You keep your copyright. See
`CONTRIBUTING.md`.

Related: [policy](/docs/enterprise/policy), [runtimes](/docs/enterprise/runtimes),
[air gapped](/docs/enterprise/air-gapped).

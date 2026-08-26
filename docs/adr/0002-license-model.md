# ADR 0002: MIT with a separately licensed ee directory

- **Status:** accepted
- **Date:** 2026-08-25
- **Deciders:** project owner

## Context

Antifailure handles production data. Teams will not adopt a tool in that
position that they cannot read, audit, run themselves, and keep running if the
company behind it disappears. That argues for a permissive license and a
genuinely complete open source product.

The features that a large company requires before a rollout, single sign on,
SCIM, custom roles, SIEM streaming, policy enforcement, customer owned
runtimes, billing, are also the features that a large company will pay for and
that an individual developer will never use. Giving them away permissively
funds nothing; withholding the core to sell them makes the open source product
a demo.

## Decision

The repository is MIT licensed, except the `ee/` directory, which is licensed
under the Antifailure Enterprise License: source visible and modifiable, but
production use requires a valid license key or subscription, no reselling, no
offering it to third parties as a hosted service, and no removing the license
check.

The community edition is complete. Masking, verification, every database
provider, the whole egress and mocking layer, the agent runner, insights, load,
and the control plane are MIT and stay MIT.

The boundary is enforced mechanically, not by convention: a depguard rule
forbids importing `ee/` from outside it, the `ee` build tag gates compilation,
the release pipeline inspects shipped symbols and bundles, and CI publishes
`antifailure/antifailure-foss`, a generated mirror with `ee/` deleted, from
which the community artifacts are built. A community build that does not
compile from the mirror is a broken boundary and fails the gate.

Contributions are under the Developer Certificate of Origin, with no CLA.

## Consequences

Easier: a self hoster gets everything they need under MIT, permanently, with no
key and no phone home. A company that needs SSO has a clear thing to buy. The
boundary cannot rot, because the community build is produced from a tree where
`ee/` does not exist.

Harder: every enterprise feature must attach through an extension point that
the community code declares, with a no-op default, because it cannot be reached
by an import. That is more design work per feature and it is the price of the
guarantee.

Committed to: no CLA means we cannot relicense contributed code later. If
relicensing ever becomes necessary, it requires contacting every contributor.
That is recorded as an open question rather than pre-solved with a CLA, because
a CLA has a real cost in contributor willingness and we do not have a reason
to pay it yet.

## Alternatives considered

**Apache-2.0 throughout, including `ee/`.** Rejected because it forecloses the
commercial model entirely; a hosted competitor could offer the enterprise
features on day one.

**AGPL for the whole repository.** Rejected because AGPL is a blocker at many
of the companies most likely to adopt this, whose legal departments deny it by
policy, and because it does not actually protect the enterprise features from a
company willing to comply.

**Business Source License with a time delay for the whole repository.**
Rejected because it makes the core not open source, which defeats the adoption
argument that motivates the permissive license in the first place.

**Elastic License 2.0 for `ee/`,** which the original plan defaulted to.
Superseded by the enterprise license above, which states the license key
condition explicitly rather than relying on the more general limitation
language, and matches the model contributors are already familiar with.

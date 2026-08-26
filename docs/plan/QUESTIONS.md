# Open questions

Questions the agent could not answer from the specification, the code, or the
repository, recorded with the interpretation it proceeded under. A question
with an answer is kept rather than deleted, because the reasoning is the useful
part.

## Open

### Q1. Legal name of the copyright holder

The MIT license header reads `Copyright (c) 2026 Antifailure`. If there is an
incorporated entity, that name belongs there instead, and in
`ee/LICENSE.md`, the enterprise license issuance tool, and the SBOM.

**Proceeding under:** the product name. This is a one line change in three
files whenever the entity name is known.

### Q2. Contributor License Agreement

The licensing amendment left this deliberately open. PostHog uses a CLA; the
plan's decision 2 specifies the Developer Certificate of Origin with no CLA,
and the repository currently implements DCO only.

A CLA is only needed if contributions might ever be relicensed, for example to
move a community feature into `ee/`, or to relicense the project wholesale. It
has a real cost: some contributors will not sign one, and the friction shows up
on first time contributions, which are the ones most sensitive to friction.

**Proceeding under:** DCO, no CLA. Reversing this later is possible but
requires contacting every prior contributor, so it is worth an explicit answer
before the contributor count grows.

### Q3. Crowdi-Agents code reuse

The owner granted access to `maksymrajszewski/Crowdi-Agents` as possible
material for the agent runner, matching decision 8 in the plan.

**Finding:** the repository has no `LICENSE` file and its `package.json` sets
`"private": true`, which means all rights reserved by default. Copying any of
it into this repository, which is MIT and public, would create a licensing
defect. Separately, the architectures diverge: Crowdi's browser controller is
selector driven and coupled to Supabase, BullMQ, and its own schema, while this
plan specifies a selector free accessibility tree harness that the engine
spawns over a socket API.

**Proceeding under:** a clean implementation. Ideas were carried, code was not:
human like typing cadence, deliberate misclick and mouse jitter for realism,
friction detection as a first class signal, browser pooling, and persona
diversity all appear in the runner design and all were reimplemented.

**Needed to change this:** the copyright holders of Crowdi-Agents relicensing
it, or the relevant files, under MIT, recorded in that repository.

### Q4. Azure provisioning is not done

Part B assumes four resource groups, registered providers, approved quotas,
Terraform state storage, two Key Vaults, a delegated DNS zone, and an Entra app
registration with OIDC federation. None of these exist yet. The subscription is
authenticated and reachable.

**Proceeding under:** everything that does not need Azure is built and proved
first. Terraform is written and validated but not applied. `tools/preflight`
reports each missing item with the exact command that provisions it, so this
becomes a single sitting of work rather than a blocker discovered piecemeal.

### Q5. Third party test accounts are not provisioned

Neon, Supabase with branching, the Stripe sandbox, Datadog, New Relic, Doppler,
1Password, and the Entra test tenant are all listed in the access matrix and
none exist.

**Proceeding under:** each provider is implemented against its documented API
with a fake that enforces the same validation rules, and runs the full
conformance suite against that fake. The provider is marked `written` rather
than `proven` in `STATUS.md` until it has run against the real service. The
distinction is load bearing and is never blurred.

### Q6. Corpus applications are not vendored

Appendix E.5 proposes eight real applications as submodules. Each needs a
license check before vendoring, and several are large.

**Proceeding under:** a purpose built fixture application in
`corpus/fixture-app` that deliberately contains every edge case the catalogs in
C.12 list, plus the seeded synthetic data generator. It is faster to iterate
against, it can be made to fail on purpose, and it does not depend on an
upstream project's release schedule. The real applications are added on top,
not instead.

## Answered

*(none yet)*

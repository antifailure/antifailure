# RFC 0001: Replay a captured agent failure locally

- **Status:** accepted for the local vertical slice
- **Date:** 2026-09-28
- **Decision record:** ADR 0003

## What this changes

A developer can import explicitly captured TypeScript agent boundaries, save them with a verified Postgres golden, reproduce a specified failure and test another revision in an independent environment. CLI and local MCP expose the evidence. A saved scenario can run as a regression case.

## Why now

The existing oracle compares controlled HTTP requests and database state, while the runner cassette records Antifailure's own model calls. Neither instruments a customer's separate agent. The requested first workflow is one billing recommendation whose fix can be tested with a pinned database and recorded external observations.

## The design

Use versioned local incident and scenario artifacts. Retain metadata by default and explicitly allow bodies. Match every boundary request in full, run the original revision before the candidate, and stop on divergence. An immutable scenario owns the assertion and harness. Each attempt owns separate application/database environments and confirmed teardown. PASS requires the original failure, the corrected outcome, no net writes to the declared database tables, complete evidence and cleanup. Unknown conditions are INCONCLUSIVE.

## What it costs

Customers add explicit wrappers and a replay endpoint enabled only in the contained environment. The first protocol supports sequential boundaries, synthetic identities and the SDK clock. A golden's creation time is reported and is not called incident-time state. Retaining prompts or tool bodies creates a separate data-custody obligation from database masking.

## Alternatives

The existing oracle lifecycle starts the candidate first and leaves it running, so reuse its database comparison and revision archive rather than changing its established behavior. A hosted trace store and framework auto-patching add scope before proving a failure replay and are outside this release.

## Open questions

Customer demand, production overhead, identity-preserving transformations and historical state availability require pilot evidence. The synthetic demonstration establishes behavior of the implementation, not those customer facts.

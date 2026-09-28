# ADR 0003: Prove a captured agent failure before judging its fix

- **Status:** accepted
- **Date:** 2026-09-28
- **Deciders:** Vir approved implementation and delivery after verification

## Context

Antifailure can branch a verified golden, contain an application and compare HTTP/database effects. Its browser runner cassette records that runner's model calls. Neither capability captures a separate customer's production agent. A trace also cannot reconstruct rows or external responses that were never retained.

## Decision

Add explicit local TypeScript capture and a versioned scenario store. The local replay coordinator starts the original code first and requires its specified wrong outcome. It runs the candidate in an independent branch of the same approved golden with strict cassette matching. Full request identity, complete dependency evidence and confirmed cleanup are conditions of a verdict. A missing condition yields INCONCLUSIVE. The scenario owns policy and assertions; a candidate revision belongs to an attempt.

## Consequences

The initial contract covers local Docker Postgres, sequential captured boundaries, synthetic identities and a cooperative application endpoint. It records those limits. The database is a pinned masked state, not automatically historical state. Content capture is opt-in and independent of database masking. Hosted storage, framework auto-patching, historical recovery and live-model exploration are outside this contract.

## Alternatives considered

- Extend the browser-runner cassette: rejected because its caller and request identity are different from customer-agent instrumentation.
- Reuse the oracle lifecycle unchanged: rejected because it starts the candidate first and leaves it running.
- Build a general hosted trace product first: rejected because it adds custody and interface work before proving one controlled replay.

---
title: Replay an agent incident
description: Record explicit agent boundaries, reproduce a failure and test a fix against a pinned golden.
sidebar:
  order: 40
---

Agent replay tests one recorded failure against one candidate revision. It uses a local TypeScript SDK, an immutable scenario and two independent application environments. It does not restore a historical database from a trace.

## Record the supported boundaries

Build `sdk/typescript` and install its npm archive in the application. Wrap the agent entry point with `AgentReplay.run` and each model, tool, HTTP, database and effect boundary with `boundary`. The package README contains the integration contract.

Capture defaults to metadata and keyed hashes. Input, output and each boundary body require explicit content names in the capture policy. Configure redaction before enabling content. The writer denies credential fields and recognized credential strings before persistence. A redaction failure records incomplete evidence, while the application's result or exception is preserved. If the writer itself fails, `onDiagnostic` names the lost capture; a disk that cannot be written cannot retain its own warning.

The first protocol supports sequential boundaries within each run and separate concurrent runs. An unfinished or concurrent boundary is incomplete evidence. Only application time read through the SDK clock is frozen. There is no claim to intercept arbitrary libraries, timers or background work.

## Save the incident

The capture carries a full source commit, W3C trace ID, policy version and per-boundary request identity. Import it into the application repository:

```sh
af incident import capture.json
af incident list
af incident inspect billing-failure --output json
```

Inspect the retained content before saving it. Metadata-only captures remain useful for diagnosis but cannot be replayed. The first release requires synthetic identities already consistent with the masked database; an unmapped production identifier blocks promotion.

Pin a verified golden made for this project. The original wrong outcome and the expected outcome must be distinct JSON values:

```sh
af incident save billing-failure \
  --scenario billing \
  --golden gv_20260927000000_example \
  --pointer /recommendation \
  --original '"charge"' \
  --expected '"review"' \
  --table subscriptions
```

Use an actual version from `af golden list` in place of the illustrative golden above. `--endpoint` defaults to `/af-replay`. This must be an application endpoint that enables the SDK replay handler only when `AF_REPLAY_ENABLED=true`.

The scenario freezes the input evidence, manifest, golden identity, relevant tables and outcome assertion. A changed evaluator or fixture belongs in a new scenario. The candidate revision belongs to a replay attempt and does not rewrite the scenario.

## Reproduce and test

```sh
af replay billing --candidate HEAD
af replay inspect rpl_example --output json
```

Use the attempt identifier printed by the first command in the second. The engine archives both revisions, starts the original revision first, and checks the specified failure. If it cannot reproduce that outcome, the candidate receives no fix verdict.

The candidate starts from an independent branch of the same golden. Its initial selected database facts must agree with the baseline. Every recorded boundary request must match its complete identity, including system instructions and tool versions. Changed requests stop with a cassette miss. The first release has no live-network fallback or exploratory mode.

Only local Docker Postgres and application services are supported. Replay refuses other datastores, remote runtime targets and external allow, sandbox, capture, mock, emulate or synth rules. Observations and effects are supplied by the SDK cassette; the runtime blocks all public egress. No process environment, dotenv file or credential store supplies application secrets. Explicit credential literals must be synthetic.

The existing image builder still uses its documented build network behavior. Runtime containment does not claim to sandbox an arbitrary Dockerfile build. Review application source and build inputs as you would for an ordinary Antifailure environment.

## Read the verdict

| Verdict | Meaning | CLI exit |
| --- | --- | --- |
| PASS | The original failure reproduced, the candidate met the assertion without net writes to declared tables, evidence was complete and both environments were removed | 0 |
| FAIL | The control reproduced and a valid candidate experiment missed the expected assertion | 8 |
| INCONCLUSIVE | Required evidence, compatibility, containment, execution or cleanup could not be confirmed | 7 |

Invalid command inputs and failures preparing a scenario exit 3. A missing blob, damaged digest, unavailable revision, missing golden, unsupported identity, cassette miss, incomplete database read or uncertain teardown cannot produce PASS.

Reports describe a **state-backed** experiment against a pinned masked golden. They do not claim incident-time equivalence. Database evidence covers net differences in the selected tables, not an insert and delete between snapshots. Tables that cannot be read completely make the experiment inconclusive.

The first evaluator requires the candidate to leave the selected database tables unchanged. A correct-looking recommendation that also changes one of those tables fails. Scenarios that intentionally change database contents need a different evaluator and are not supported by this first contract.

Both sides use unique attempt identifiers. Teardown checks pending journal resources and provider inventories. If execution was interrupted:

```sh
af replay recover rpl_example
```

Recovery operates on the recorded attempt's two environments, refuses an active attempt, and retains an inconclusive verdict. Run a new replay after recovery to obtain fresh evidence.

## Keep the incident as a regression case

A suite is a local JSON document:

```json
{"schemaVersion":1,"scenarios":["billing"]}
```

```sh
af eval run suite.json --candidate HEAD --output json
```

Each case gets a separate attempt and verdict. Retain the scenario store and its referenced golden on the CI runner. Copying a trace alone does not copy its database. Reintroduce the original bug as a negative control: the case must fail. Remove required evidence: it must become inconclusive.

The MCP tools `inspect_agent_incident`, `replay_agent_incident` and `recover_agent_replay` reach the same engine. Inspection pages boundary summaries; captured bodies remain available through the local CLI. Scenario approval is a CLI operation so candidate-driven tools cannot replace the evaluator or weaken replay policy.

## Local data custody

Artifacts are stored under `.antifailure/replay` with private file permissions. Payloads are content-addressed and published before scenarios. Incident and scenario names cannot traverse paths. Inspectable siblings remain visible when another artifact is malformed.

This first release has no hosted storage or tenant search. Anyone who controls the local project and its files controls its captures. Retain only opted-in content for which you have permission. A source merge installs neither a hosted collector nor a production capture policy.

Retire a case when its content should no longer be retained:

```sh
af replay retire billing --reason 'The billing workflow was removed'
```

Retirement refuses attempts with unconfirmed cleanup, removes their retained reports and unreferenced incident blobs, and keeps a small record of the case name, reference hashes, time and reason. Shared blobs remain until their last scenario is retired. The original capture file supplied to import remains yours to delete. Retrying an interrupted retirement completes the same deletion. A retired name cannot be reused.

The local golden collector refuses versions referenced by this project's active scenarios. Another checkout or an external Docker administrator can still remove an image; a missing golden then makes replay inconclusive. There is no background retention daemon.

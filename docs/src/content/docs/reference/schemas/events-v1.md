---
title: "Antifailure event schema"
description: "One thing that happened, as it appears on the event stream."
---

One thing that happened, as it appears on the event stream. The envelope is identical across the engine, the runner, and the control plane. Generated from the Go type and the event catalog by go test ./internal/events -update-schema.

:::note
This page is generated from `schemas/events.v1.json`. Edit the schema, then run `just generate`.
:::

## The document

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `data` | object | no | The type specific payload. Always an object, never a scalar or a list. |
| `env` | string | no | The environment identifier. Absent on engine wide events, which share the empty environment's sequence. |
| `id` | string | **yes** | Unique for this event. Min length 1. |
| `level` | `debug`, `info`, `warn`, `error` | **yes** | Classifies the event for display and filtering. |
| `msg` | string | no | A short human readable summary, already redacted, like everything else that reaches a log or an artifact. |
| `seq` | integer | **yes** | A monotonic counter per environment, so a consumer can order events and notice a gap. Minimum 0. |
| `ts` | string | **yes** | When it happened, from the engine's injected clock. Format `date-time`. |
| `type` | string | **yes** | What happened. Every value in the engine's catalog is listed here, so a consumer can reject an event it was not built to understand rather than guessing from the prefix. |

### Values for `type`

| Value | Meaning |
| --- | --- |
| `agent.finished` | An agent run finished. The data carries the verdict counts. |
| `agent.started` | An agent workflow has started. |
| `agent.step` | An agent took one action. The data carries its stated intent. |
| `agent.verdict` | A workflow reached a verdict. |
| `build.failed` | A service build failed. |
| `build.finished` | A service build succeeded. The data carries the image digest. |
| `build.log` | A line of build output, redacted. |
| `build.started` | A service build has started. |
| `capture.message` | An outbound email or message was captured into the inbox. |
| `cron.fired` | A scheduled job fired. |
| `db.branched` | A database branch is ready. |
| `db.branching` | A database branch is being created from a golden version. |
| `db.destroyed` | A branch was destroyed. |
| `db.reset` | A branch was reset to its golden state. |
| `egress.decision` | The proxy decided what to do with an outbound request. |
| `egress.tripwire` | A request carrying a live credential was blocked. |
| `engine.error` | An operation failed. The data carries the error code. |
| `engine.progress` | A step in a long running operation, for work with no more specific event of its own. |
| `engine.retry` | A provider call is being retried after a transient failure. |
| `engine.sink_dropped` | A sink fell behind and dropped events. The data carries the count. |
| `engine.warning` | Something is not right but the operation continues. |
| `env.creating` | An environment has started being created. |
| `env.destroyed` | Teardown finished and every recorded resource is gone. |
| `env.destroying` | Teardown has started. |
| `env.failed` | An environment could not be created. The data carries the error code. |
| `env.ready` | An environment is fully built, running, and reachable. |
| `env.sleeping` | An idle environment has been scaled to zero. |
| `env.waking` | A sleeping environment is being woken by a request. |
| `golden.collected` | An unreferenced golden version was garbage collected. |
| `golden.failed` | A golden refresh failed. No version was published. |
| `golden.ready` | A golden version is masked, verified, and available to branch from. |
| `golden.refreshing` | A golden refresh has started. |
| `insight.finding` | A database insight was found: a lock, a regression, or a plan change. |
| `load.finished` | A load run finished. The data carries the comparison against main. |
| `load.sample` | A load test metric sample. |
| `mask.applied` | Masking finished on a golden candidate. |
| `mask.finding` | Verification found data matching a detector. The value is never included. |
| `mask.planned` | Masking produced a plan. The data carries affected tables and row counts. |
| `mask.progress` | A masking chunk finished. The data carries the fraction complete. |
| `mask.verified` | Verification passed and an attestation was signed. |
| `mask.verifying` | The verification scanner has started reading back the golden. |
| `resource.created` | An external resource was created and committed to the journal. |
| `resource.deleted` | An external resource was deleted and its journal entry compensated. |
| `resource.leaked` | The leak detector found a resource the journal does not know about. |
| `service.exited` | A service exited. The data carries the exit code. |
| `service.log` | A line of service output, redacted. |
| `service.ready` | A service passed its readiness check. |
| `service.restarted` | A service was restarted after a crash or an eviction. |
| `service.starting` | A service container or pod is starting. |
| `webhook.delivered` | An inbound webhook was delivered and acknowledged. |
| `webhook.failed` | An inbound webhook could not be delivered after its retries. |
| `webhook.queued` | An inbound webhook was queued for delivery. |


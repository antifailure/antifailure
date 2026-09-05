# Usage that survives cleanup

## Contract

The lifetime recorded by the environment projection is the unit already used
by cost caps. Cleanup must not erase it. An organization deletion must erase
both its intervals and its daily totals. No backfill claims to reconstruct
environments that were removed before this migration.

This records the lifetime the existing environment projection reports. A
deleted projection recreated under the same environment name gets a distinct
interval. An in-place engine restart that reuses the same projection UUID is
still limited by the existing ingestion protocol: sequence numbers are local
to an engine state directory, and teardown events carry no lifetime identity.
Fixing that requires a persistent lifetime identifier on all lifecycle events,
including fresh runners. This change does not claim to solve that protocol gap.

## Data and writers

`environment_usage` retains the projection UUID, display identifier, tenant,
creation time and teardown time. Its primary key is the projection UUID, not
the reusable environment name. It has no foreign key to the disposable
environment or repository. Its organization foreign key cascades on deletion.

A database trigger runs on every environment insert, timestamp update and
delete, including repository cascades. This covers ingestion, direct teardown
and future cleanup callers without depending on each caller remembering a
second write. A deletion of an open projection closes its recorded interval at
the deletion time. Metadata already removed from cost attribution is labelled
as removed rather than reconstructed.

The trigger marks the organization's earliest affected time dirty before
writing the interval. The scheduled rollup takes that same checkpoint row lock,
replaces affected UTC days and advances its checkpoint in one transaction.
Unchanged closed history is not recomputed on every pass. Open intervals are
revisited from the previous checkpoint, and a late timestamp correction marks
earlier days for replacement. Retried and concurrent calls replace totals,
rather than adding them. An older maintenance clock cannot move the checkpoint
backward; future timestamps remain eligible until their recorded time arrives.

The existing maintenance entry points invoke the rollup after partition upkeep
and product-event aggregation. Product events and environment usage remain
distinct measurements. The operator reads saved calendar days for the chart
and current intervals for exact rolling totals. The daily chart preserves idle
day spacing and explains why calendar boundary days differ from rolling totals.

## Access and retention

Application connections can only select their own organization's records.
They cannot directly insert, update or delete ledger or aggregate rows. The
operator pool has its existing separate credential and tenant-read permission.
The privileged migration and maintenance role owns the scheduled writer.
Both SQL functions place the temporary schema last in their explicit search
path. A tenant-created temporary table cannot redirect the privileged trigger.
Organization deletion removes all three usage tables through foreign keys.
No email address, IP address, database row or application payload is copied
into usage history.

## Alternatives considered

An event-only writer misses direct teardown and cascade paths. A daily total
without its source intervals cannot correct late events or compute a rolling
24-hour cap. Keeping the disposable environment rows forever would retain
unneeded operational metadata and make cleanup depend on billing retention.
The interval ledger separates those lifetimes while preserving the current
metering definition.

## Verification

Behavioral tests use real PostgreSQL and cover cleanup before and after rollup,
concurrent retries, repository removal, organization erasure, reused names,
late corrections, out-of-order maintenance clocks, future teardown times,
ongoing accrual through the actual maintenance function, durable cost caps,
cost attribution, tenant isolation and the operator route's recorded series.
Each new assertion receives a production mutation and restored green run.
Browser review checks populated data, loading failures and recovery, narrow
and desktop layouts, the daily measurement table, and keyboard accessibility.

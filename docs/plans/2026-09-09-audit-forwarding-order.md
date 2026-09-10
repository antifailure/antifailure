# Audit delivery follows each organization's commit order

The audit writer takes a transaction lock per organization before allocating
its sequence number. Different organizations can commit in the opposite order
to their allocated numbers. A global high water mark therefore skips a valid
late commit forever.

Use a delivery position per organization and join it directly to audit entries.
No organization inventory permission is needed. Keep the singleton cursor only
as an operational summary for existing diagnostics. It never filters delivery.
Positions advance monotonically after a sink accepts entries or the licence
explicitly declines them. A failed delivery leaves its organization's position
unchanged without rewinding another organization's position.

A global writer lock would serialize unrelated customer requests. A receipt
per entry would work but grow with every audit event. Positions grow with the
number of organizations and match the writer's existing serialization boundary.

Verify with real transactions: hold one organization's append uncommitted,
commit a later sequence for another, run a pass, commit the earlier append,
and run again. Both must arrive. Also retain rollback gaps, restart, empty,
partial sink failure and licence transitions. Delivery remains at least once
across a crash between sink acceptance and checkpoint persistence.

## Ordering evidence

`ee/web/audit/test/forwarder.test.ts` runs against Postgres through the real
audit writer and scoped application connection.

| Ordering | Observed result |
| --- | --- |
| Write before first poll | The signed batch carries the stored entry and chain hash. |
| Poll before write, then another poll | The later entry arrives without restarting the forwarder. |
| No write | No batch or checkpoint movement. |
| Write with no running forwarder | The first later poll reads the backlog. |
| Lower sequence commits after another organization's higher sequence | Both entries arrive on successive polls. |
| Sequence allocated but transaction does not commit | Later committed rows cross the gap. |
| Restart after checkpoint | The new instance sends no already checkpointed entries. |
| Collector accepts a prefix then fails | The next poll retries exactly the pending suffix. |
| Permanent collector refusal between successful batches | The refused batch is logged and skipped; later batches arrive. |
| Entitlement withdrawn or granted between polls | The same instance changes forwarding on the next poll. |
| Organizations interleaved in one read | Each receives a separate signed batch. |

The real process test in `ee/web/server/test/audit-stream.test.ts` sends a SCIM
request over HTTP and watches its audit entry arrive at an HTTP webhook with a
valid signature. It checks both the process licence and organization plan
negative controls against an observed checkpoint advance, not silence alone.

## Security boundary and limits

The forwarder scope reads organization audit rows across tenants. Its setting
is a trusted application declaration, not a database credential: application
code with arbitrary SQL execution can set it. Tenant and sweeper scopes clear
it, and the database tests exercise the resulting reads and writes. The role
cannot rewrite or remove the primary audit log. Delivery bookkeeping is hidden
from ordinary tenant connections.

Manifest verification checks organization, count, sequence range and chain head
against the signed entries. Real HTTP tests prove that a redirect does not
receive the audit body and that the deadline aborts a collector request.
Removing either transport guard fails its respective test, and restoring it
passes. The deadline test injects the abort signal rather than waiting thirty
seconds; it also checks the configured duration.

This exports `audit_entries`, not every row in the separate global operator
log. Organization copies of operator actions are included. One configured
destination is shared by all forwarder replicas. A collector can fan out to
multiple destinations. Object storage still requires an injected writer and
cannot be selected through environment configuration. No live Splunk or Event
Hubs service was used for this verification. Webhook receivers must deduplicate
by organization and sequence, and signatures alone do not reject replay.

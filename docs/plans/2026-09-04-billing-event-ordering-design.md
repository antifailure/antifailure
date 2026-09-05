# Billing outcomes must not depend on delivery order

The existing contract is newest entitling subscription, not newest subscription
of any status. Stripe's creation timestamp decides age. Receipt time cannot:
webhooks may arrive in any order. Equal timestamps use the provider identifier
as a stable tie break, not a claim about which purchase happened first.

Three approaches were considered. Keeping the current newest-row query breaks
the contract for canceled rows. Choosing the largest plan invents a commercial
policy rather than implementing the existing one. This repair keeps the promised
newest known entitling subscription policy and changes its actual reader.

## Data and concurrency

The decoder retains the subscription's `created` timestamp. The existing
creation column stores it on insert and repairs older receipt-time values on
the next accepted delivery or reconciliation. When a payload omits creation,
an existing value is retained; a new row uses its event timestamp. No synthetic
history is backfilled without provider evidence.

Subscription writers lock their organization before writing a subscription.
Recomputation takes that same lock before selecting. This both serializes
different subscription writers and prevents a subscription-first lock ordering
from deadlocking with organization-first writers. Public callers are the live
webhook, pending-event replay during customer attachment, and reconciliation.

A known paid subscription in an entitling status wins over an ended subscription
or one whose price grants no known plan. When the only deciding active row has
an unknown price, the organization plan is left alone as documented. An unknown
status retains the existing conservative behavior instead of guessing a plan.
The billing screen independently prefers a live subscription so cancellation
and checkout do not act on a newer ended purchase.

Customer attachment and live delivery take the same customer-keyed transaction
advisory lock before either inserts a row. This closes the concurrent ordering
where the customer lookup and pending-event lookup each missed an uncommitted
row, then neither side retried. The complete lock order is customer,
organization, then billing entity. Invoice repair takes the organization lock
before the invoice, and every known-customer delivery takes it before applying
an event, so its final organization foreign-key check cannot invert that order.

Reconciliation snapshot ordering remains a separate required repair. Independent
review rejected a request-start timestamp change: a fresh canceled response
could then be overwritten by an older webhook delivered after the response.
Request-end timestamps have the opposite ambiguity, masking a newer event whose
change occurred after the response snapshot was taken. A local clock value is
not a provider object version. The safe follow-up is a persisted per-object
dirty generation and refresh lease: events schedule canonical provider reads,
reads run outside transactions, and only the current lease and generation may
apply the response. Periodic reconciliation reaches the same writer.

## Verification and boundary

Real PostgreSQL tests cover both subscription arrival orders, canceled and
unknown-price competitors, stable ties, correction of old creation values,
missing creation fields, and the lock boundaries. Forced overlap pauses actual
database statements so attachment and webhook delivery reach the precise race
that previously left a paid purchase unresolved. Every new assertion is independently
mutation-tested. Existing signed webhook, replay, missing-webhook recovery,
tenant isolation and entitlement enforcement suites remain required.

This repair does not claim to prevent a second Stripe checkout session. That
requires a separate durable reservation and provider idempotency lifecycle.
It makes no live charges and does not change Stripe account settings.

Provider references:

- [Subscription object](https://docs.stripe.com/api/subscriptions/object)
- [Webhook delivery behavior](https://docs.stripe.com/webhooks)

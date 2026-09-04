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

Reconciliation records its observation time before starting the provider
requests. A newer webhook arriving during those requests must not be overwritten
by a response that was already in flight. This applies to invoices as well as
subscriptions. Provider requests remain outside database transactions.

## Verification and boundary

Real PostgreSQL tests cover both subscription arrival orders, canceled and
unknown-price competitors, stable ties, correction of old creation values,
missing creation fields, and both lock boundaries. Controlled provider responses
prove that a subscription cancellation and an invoice payment delivered during
reconciliation survive its completion. Every new assertion is independently
mutation-tested. Existing signed webhook, replay, missing-webhook recovery,
tenant isolation and entitlement enforcement suites remain required.

This repair does not claim to prevent a second Stripe checkout session. That
requires a separate durable reservation and provider idempotency lifecycle.
It makes no live charges and does not change Stripe account settings.

Provider references:

- [Subscription object](https://docs.stripe.com/api/subscriptions/object)
- [Webhook delivery behavior](https://docs.stripe.com/webhooks)

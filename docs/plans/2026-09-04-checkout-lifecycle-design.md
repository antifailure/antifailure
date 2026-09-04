# One checkout attempt per organization

Repeated clicks and requests on different replicas must not create different
payable sessions. A permanent organization idempotency key would instead strand
every returning customer on an expired session. A second database read alone
would still race. The selected design persists a current attempt per organization
with a random key, customer, price and return URLs. Those parameters do not change
while the attempt can still be paid.

The organization row is locked before reading subscription state and claiming
the attempt. Network calls happen after the transaction commits. Repeated calls
reuse the provider session or repeat creation with exactly the same key and
parameters. Lost responses are recovered by paging through the customer's Stripe
checkout sessions and matching the attempt metadata.

An expired provider session permits a replacement. A completed session permits
replacement only when its linked subscription is affirmatively ended. Missing
webhooks do not permit a new purchase: the provider subscription collection is
checked directly. Unknown or malformed states refuse rather than being treated
as no subscription. A missing session or an unresolved attempt beyond 23 hours
requires operator reconciliation, because Stripe may discard idempotency keys
after 24 hours. No new key is minted merely because a network request failed.

The database persists the session identifier, never the checkout URL. Row-level
security restricts attempts to their organization. The operation has no card
fields, no live-charge tests and no new product or price configuration.

Verification covers concurrent clicks, sequential retries, response loss, failed
local persistence, missing and early webhooks, expiry, completed checkout,
subscription cancellation, parameter changes, tenant isolation and provider
pagination. Every new assertion is mutation tested independently against real
local Postgres and a stateful provider transport.

Provider contracts:

* https://docs.stripe.com/api/idempotent_requests
* https://docs.stripe.com/api/checkout/sessions/list
* https://docs.stripe.com/api/checkout/sessions/retrieve

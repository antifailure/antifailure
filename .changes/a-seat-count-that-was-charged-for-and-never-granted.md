# fixed

Checkout sold a per unit seat count that entitled nothing.

`subscriptions.checkout` took a `seats` number between one and a thousand and
passed it to Stripe as `line_items[0][quantity]`, so the price multiplied by it.
Nothing ever read it back. How many members an organization may hold is a
constant per plan in `entitlements.ts`, enforced by `seatVerdict`, and it never
consulted the subscription at all. An organization that bought three seats on
Team got fifty. An organization that bought two hundred seats on Team also got
fifty, and paid two hundred times as much for the same limit.

What this product sells is one hosted control plane per organization at a flat
fee, so there is no number for a price to multiply. The `seats` input is gone
from the route and from the published OpenAPI contract, and checkout sends
Stripe no quantity at all rather than a hardcoded one: Stripe bills a licensed
recurring price once when the parameter is absent, and omitting it also keeps
the call valid if a price is ever made metered, because Stripe refuses a
quantity on a metered price.

The `quantity` column stays and is now labelled for what it is. Stripe reports a
quantity on every subscription object it sends, so the row records it and the
admin money screen, the billing summary and the organization export display it,
which is what lets an operator reconciling an invoice see what was actually
billed. No entitlement and no quota reads it. The organization export called the
field `seats`, which told a customer their subscription had bought them that
many members; it is `stripeQuantity` now, because an export is the document
somebody takes to a third party and it should not state a limit the product does
not enforce.

Three tests hold the two halves apart, and each was checked by breaking the fix
and watching it go red. Checkout's request body must carry no quantity. The same
plan must resolve to the same seat limit whatever a subscription row says, in
both directions: a recorded quantity of two hundred does not raise a five seat
plan, and a recorded quantity of three does not lower a fifty seat one. And
`entitlements.ts` must contain no read of a quantity at all.

A billing ordering that was never covered is now covered: a plan downgrade while
an organization holds more members than the lower plan allows. Nothing is
removed, and the next invitation is refused naming what the organization is
holding. The webhook connection cannot see the members table under row level
security, which is the structural reason a plan change has never been able to
take somebody's colleague away.

# changed

The pricing page describes the two paid plans that exist.

It advertised a "Growth + Enterprise" band and a hero line about "Growth and
Enterprise". There is no Growth plan behind it: the control plane sells `team`
and `enterprise`, `PLAN_QUOTAS` knows free, team and enterprise, and the only
prices an operator can configure are `AF_STRIPE_PRICE_TEAM` and
`AF_STRIPE_PRICE_ENTERPRISE`. A third name on a pricing page is a plan a reader
can ask to buy and nobody can sell. No price number moved; the band that was
labelled Growth is labelled Enterprise.

Each paid plan now publishes how many people it holds, and the free plan's
number is answered in the questions. That is the question the removed seat
picker used to imply, and it was written down nowhere a reader could find it.
The numbers come from `www/lib/plan-facts.ts` and
`web/apps/api/test/plan-facts.test.ts` fails the build if they stop matching
`ENTITLEMENTS.seats.byPlan`, the same way the free plan's three numbers are held
against the quota table.

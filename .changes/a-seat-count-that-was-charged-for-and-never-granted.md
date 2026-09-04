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

# fixed

A control plane that sold one plan self-serve and one by arrangement took no
money at all.

`AF_STRIPE_PRICE_ENTERPRISE` was a required variable. A deployment that set the
Stripe secret key, the webhook secret and `AF_STRIPE_PRICE_TEAM` and nothing
else landed in the "partially configured" branch: `stripeConfigFrom` returned no
configuration, billing was entirely **off**, every billing route answered
PRECONDITION_FAILED, and the Team price that did exist could not be sold either.
The startup line called it an operator error. That is the shape this product
actually sells in, because Enterprise is agreed with a person and has no Stripe
price at all.

Measured by calling the function rather than by reading it: secret key, webhook
secret and Team price gave `config: null` and "billing is OFF and partially
configured: AF_STRIPE_PRICE_ENTERPRISE not set". It gives a configuration now,
and a startup line that names both halves, "team sold self-serve, enterprise has
no price and is arranged with a person", so the first Enterprise refusal does
not read like an outage to whoever is on call.

The three that are genuinely required are still required, each proved by
dropping it on its own. `AF_STRIPE_PRICE_ENTERPRISE` set by itself is still
reported as half configured rather than as untouched.

Checkout for a plan with no price is refused before Stripe is called, with a
sentence saying the plan is arranged with a person and where to ask, rather than
sending Stripe an empty price identifier and returning a generic "could not open
a checkout page" to the buyer with the largest cheque. Nothing reaches Stripe on
that path, which is asserted rather than assumed. The admin plan change refuses
the same way rather than replacing a paying subscription's item with nothing.

Two smaller things fell out of it. A plan with no price can no longer match a
subscription that carries no price identifier: the lookup compared `undefined`
against `undefined`, matched, and would have moved somebody onto the largest
plan for nothing. And `AF_HOSTED_REQUIRED_PLAN` now stops the process when it
names a plan with no price, which is the contradiction its existing billing-off
check already exists to prevent, reached through the door this change opened.

Three messages that named `AF_STRIPE_PRICE_ENTERPRISE` as something to go and
set were corrected, because on an installation that agrees Enterprise with a
person it is correctly unset forever and each of them sent somebody to fix a
thing that was not broken. The billing precondition names the three variables
that are actually required. The deletion path's refusal overstated it twice
over: it named all four while the only Stripe call on that path is
`cancelSubscription`, which needs the secret key and no price at all, so it now
says which variable the cancellation itself uses and which two make the
configuration resolve. And the console no longer offers a plan it cannot sell.

The refusal a buyer meets on a plan with no price is written as a route rather
than a failure: it says the plan is agreed with a person, why, where to ask, and
that nothing was charged. A test asserts those words, and asserts the sentence
never reads as an outage.

A start-up refusal named a price variable by writing a prefix and appending the
uppercased plan. That put a truncated fragment in the source and nothing else,
so `config-docs.test.ts`, which holds the configuration reference against the
variables the process reads, reported that fragment as read and undocumented.
It was right and the reference could not have fixed it: a name assembled at run
time is not a name anybody can set, and nothing can enumerate the settings a
process reads if it builds them. The variable for each paid plan is a full
literal in a closed map now, so a plan with no entry is a compile error rather
than a lookup that quietly returns undefined.

That suite gained two assertions for the same class: no variable name in the
source ends in an underscore, which is the signature of a concatenated name, and
every paid plan's price variable is a complete name. Each was checked by making
the break it guards.

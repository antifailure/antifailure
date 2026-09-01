# added

The control plane can take money. Stripe customers, subscriptions, invoices and
payment method metadata, a checkout session, the customer portal, and the
webhook that moves `organizations.plan`, which is what the quota enforcement in
`PLAN_QUOTAS` and `checkQuota` has always been pointed at and never been able to
reach. Billing is off unless `AF_STRIPE_SECRET_KEY`, `AF_STRIPE_WEBHOOK_SECRET`
and both price variables are set; a partly configured one is reported as off
with the missing names, because the one an operator misses is usually the
webhook secret and it fails only when a real customer pays.

Every billing table has row-level security enabled and forced. A Stripe delivery
has no tenant, so it declares the customer its verified payload named and the
policies tie the row it writes to the organization that already owns that
customer. Signatures are checked over the raw body, timestamp included, before
anything is parsed.

The whole integration is built and tested against the engine's own Stripe mock
pack, offline, with no Stripe account and no network.

# fixed

Five defects in the shipped Stripe mock pack, every one of them found by
building a real integration against it and invisible from the pack file.

A checkout session's `url` named a different session from the session's own id,
so an application that redirected a browser to it and read the session back was
told no such session exists; a payment intent's `client_secret` named an intent
that had never been created. Numeric fields came back as JSON strings, next to a
`current_period_end` that was a real number in the same object, so a typed
client rejected a response that a curl and a grep called fine. Cancelling a
subscription replaced the stored object with the five fields the cancel route
names, losing the customer, the period and the items. A subscription's `items`
were always empty, so the one question a billing integration asks a
subscription, which plan is this, had no answer. And there was no route for
`POST /v1/subscriptions/{id}` at all, so a pack that says it runs a billing flow
could not run a plan change.

The engine's webhook simulator gave every event signed in the same second the
same event id. A Stripe webhook handler must be idempotent on the event id
because Stripe retries, so a correct handler treated a subscription created and
an invoice paid in one second as one event and delivered only the first.

Both are now compared against the control plane's own implementations through
checked-in corpora, `schemas/mockpack-vectors.json` and
`schemas/webhook-vectors.json`, so neither side can drift without a test going
red in both languages.

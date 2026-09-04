# fixed

Repeated or simultaneous upgrade clicks share one durable Stripe checkout
attempt. A lost response or local process restart recovers that same session,
and a missing webhook no longer makes an existing subscription look absent.
Checkout permits a new attempt only after the provider confirms the previous
session expired or its subscription ended. An unresolved old attempt refuses
with recovery guidance instead of risking a second purchase.

# fixed

Billing could remove a paid plan when a newer canceled subscription arrived,
choose a plan according to webhook arrival time, or miss a payment event while
its customer was being attached. Plan selection now uses the provider's
creation time and the newest known entitling subscription, serializes concurrent
writes before reading the deciding rows, and serializes customer attachment
with its deliveries. Invoice and payment-method writes follow the same lock
order as subscription repair. The current subscription shown on Plan prefers
a live purchase over a newer ended one.

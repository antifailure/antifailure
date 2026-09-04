# fixed

Billing could remove a paid plan when a newer canceled subscription arrived,
choose a plan according to webhook arrival time, or overwrite a newer payment
with a delayed reconciliation response. Plan selection now uses the provider's
creation time and the newest known entitling subscription, serializes concurrent
writes before reading the deciding rows, and keeps newer deliveries ahead of
older reconciliation snapshots. The current subscription shown on Plan prefers
a live purchase over a newer ended one.

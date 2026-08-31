# fixed

The mock billing flow test now extracts the subscription id by its Stripe
prefix, so it reads the subscription rather than a nested object.

`POST /v1/subscriptions` answers with two more ids below the top level one, on
the subscription item and on its price, which is what real Stripe returns. sed
is greedy, so `.*"id":"` walked past the subscription id and captured the last
one in the body. The price id is empty when the request names no price, so the
probe silently set an empty subscription id and the flow failed at the next
step with exit 23. The pack was right and only the probe's parsing was wrong.

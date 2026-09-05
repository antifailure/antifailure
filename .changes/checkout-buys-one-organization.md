# fixed

Checkout sends the quantity required by Stripe for a licensed recurring price:
exactly one organization subscription. It never multiplies the price by the
number of members, including when an older client sends a seat count. The old
request omitted quantity, and its test incorrectly required that omission.

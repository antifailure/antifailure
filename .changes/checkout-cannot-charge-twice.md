# fixed

Two billing owners of one organization who pressed Subscribe a minute apart
each got their own Stripe checkout page, and both pages stayed payable for the
next twenty four hours. If both were paid, the organization held two
subscriptions on one Stripe customer and one card was charged twice. Nothing
refused the second page: an open checkout creates no subscription, so the
check of this database and the check of Stripe both answered honestly that
there was nothing there yet. The only thing that tied two requests together
was a thirty second window on the idempotency key, which covered a double
click and nothing else.

Each organization now holds one purchase attempt, a row claimed under the
organization's own lock, and the key sent to Stripe names that attempt rather
than a slice of the clock. A second press, a second owner, a second tab or a
second replica is given the page that already exists. When the page has to
change, because the buyer picked another plan or Stripe expired it, the old
page is expired at Stripe before a new one is opened, so two pages are never
payable at the same moment. Before any new page is opened, Stripe is asked for
the customer's open checkout sessions: a page whose creation response was lost
on the way back is found again by the attempt it carries, and every other
payable page, including one opened before this release, is expired. A page
that has been paid refuses another purchase even when the webhook for it is
late or never arrives, until the subscription it created is cancelled for
good. If Stripe cannot say what is open, nothing is opened, and the buyer is
told that nothing was charged and that pressing again resumes the same
purchase. Stripe's list of the customer's subscriptions is now read in full,
every page, before a purchase, and a list that fails or holds a subscription
that cannot be read refuses the purchase, so a subscription made in the billing
portal cannot be missed. A checkout Stripe has no record of is refused, and
Refresh from Stripe clears it.

A subscription that Stripe has paused, which happens when a trial ends with no
payment method, also counted as nothing and was sold a second plan alongside
it. Adding a card later resumed the first, and the organization paid for both.
A paused subscription now refuses a new purchase, and the refusal says to add a
card in the billing portal instead.

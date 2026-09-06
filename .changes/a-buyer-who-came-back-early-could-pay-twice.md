# fixed

A buyer who finished Stripe Checkout and came back to the Plan page before
Stripe's webhook arrived saw the same Subscribe to team button they had pressed
before they paid, with no sign that anything had happened. Pressing it again
opened a second checkout against the same customer, and a second completed
checkout was a second live subscription and a second charge for one
organization. Nothing on either side of the return could tell the two apart:
the page fetched once and never read the parameter Stripe sent it back with,
and the control plane's only guard was a read of a table the webhook had not
written yet.

Both sides now know. The Plan page reads the return, says the payment was
received, keeps the Subscribe controls off, and asks the control plane every
two seconds until the plan reads as entitled, then shows the active
subscription and drops the parameter from the address. After ninety seconds it
says plainly that the plan will activate shortly, that Refresh from Stripe asks
Stripe directly, and where to reach support, with the controls still off. The
checkout route asks Stripe itself for the customer's subscriptions before it
opens a session: an active, trialing, past due or unpaid one is refused with
the state named, an incomplete one younger than an hour is refused as a
payment still being confirmed, and a cancelled one or an abandoned attempt
lets the organization buy again. When the refusal is because Stripe holds a
subscription this database does not, the row is written before the answer
goes back, so the page catches up on its next read. Every checkout session
also carries an idempotency key on the organization, the plan and a thirty
second window, so a double click reaches Stripe as one request.

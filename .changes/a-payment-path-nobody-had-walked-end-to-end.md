# added

The runbook for turning payment on, which `production.tfvars` was already
pointing at. Setting `stripe_price_team` made this control plane able to charge;
the comment above it sends the reader to the production page for the commands,
and that page had no billing section at all. It has one now, in the order that
works: which endpoint to create at Stripe and why it has to be first, since the
signing secret does not exist until the endpoint does; the nine events
`HANDLED_EVENTS` acts on; the two vault entries; and four checks on the running
control plane, where the last one asks the product for the thing the plan was
withholding rather than asking the database what it wrote.

A test that walks one organization through the whole path rather than each hop
of it. Refused a fourth environment on the free plan, checkout through the tRPC
route, a delivery signed over raw bytes at `POST /webhooks/stripe`, the plan,
the subscription row, the SAME refused request now allowed, and what the billing
screen shows a signed in browser. Every hop of that was already proven and the
JOIN was not, which is the shape this repository keeps finding in itself: a
route that answers 200 is not evidence that a plan changed, and a plan column
reading `team` is not evidence that anybody got anything for their money.

Four arrival orderings driven through the two real entry points rather than
through the handler, which nothing in production calls. The delivery that beats
the customer row is forced from inside the Stripe call, so the webhook lands
while checkout is still between its network call and its write, which is not an
interleaving that ordering two awaits can reach. Then the same delivery twice,
two at once that disagree about the plan, and the delivery that never comes.

# fixed

The Azure page told an operator to put both Stripe credentials in the vault with
`az keyvault secret set --value "$STRIPE_SECRET_KEY"`, which the rotating
secrets page forbids by name for every other credential on this plane: the value
lands in shell history and in the argument list of a running process, where `ps`
shows it to anybody else on the machine. `production.tfvars` already told the
reader that the documented commands pass the value on standard input, and they
did not. They do now, taken at a prompt and written through a file nothing else
can read, with `printf '%s'` rather than `echo`, because a signing secret with a
trailing newline is a different string and every delivery Stripe makes is then
answered 401 while the plan, the deploy and the dashboard all look correct.

The Terraform test proved that both Stripe credentials reach the container and
never that the price does. `AF_STRIPE_PRICE_TEAM` is the third required setting
and a partial configuration is a refusal, so a module that delivered both
secrets and dropped the price would have satisfied every assertion in that file,
deployed cleanly, and taken no money, which is indistinguishable at the
infrastructure layer from billing having never been turned on. It is asserted
now, along with the price arriving as a value rather than as a reference to a
vault entry nobody created, that no Enterprise price is emitted, and that
billing off leaves no `AF_STRIPE_` setting at all.

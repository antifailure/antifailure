# added

The hosted control plane can take payment. `stripe_price_team` is set in
`production.tfvars`, which is the one switch the whole feature hangs from: it
turns on the two Key Vault secret references, the three environment variables
the process reads, and the checkout and webhook routes that were answering 503
"This control plane is not configured to take payments" until now.

Team is a flat 500 USD per month for the organization rather than per seat,
which is why checkout sends quantity exactly 1. There is deliberately no
enterprise price. That plan is arranged with a person, and checkout refuses it
by name rather than reaching Stripe with an empty identifier.

Two things are deliberately NOT part of this and both would have been easy to
include by accident. `hosted_required_plan` stays empty, because turning payment
on so somebody can buy is a different act from requiring everybody already here
to buy, and the second one locks out every organization on the plane the moment
it applies. `operator_sets_plan` stays unset, and Terraform refuses the
combination anyway: a plan that can be granted by hand is not a plan anybody has
to buy.

The API key and the webhook signing secret are not in this change and are not in
any file. They are addressed by their versionless Key Vault IDs and read by the
Container App identity when Azure creates the revision, so Terraform never sees
either value and no plan reads them. That also fixes the order this has to
happen in: both secrets go into the vault BEFORE the apply, because a missing
one fails when Azure resolves the reference rather than when Terraform plans.

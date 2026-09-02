# fixed

The Plan page told an organization that plan changes only change local quotas,
on a control plane where the plan could not be changed at all.

It is the sentence a page carries when it was written for one configuration and
then a second one appeared under it. The card said "This self-hosted
installation does not take payment. Plan changes only change local quotas."
whenever Stripe was off, which after the plan grant fix includes every
installation that has not set `AF_OPERATOR_SETS_PLAN`. Two cards below it, the
same screen said the plan can only be changed by the operator. Found by
rendering the page rather than by reading the code.

The card now says where the plan comes from on this particular installation, in
one place, and the table below it goes back to saying only what a plan allows.

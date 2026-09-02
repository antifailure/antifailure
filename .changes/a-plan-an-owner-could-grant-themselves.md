# security

An organization owner on a control plane that takes no payment could set their
own plan, including `enterprise`, and take the quota that goes with it.

`billing.set` exists so that somebody self-hosting can change their own quota,
and it refused the call wherever Stripe or `AF_HOSTED_REQUIRED_PLAN` was
configured. That guard asks whether billing was set up, and the dangerous
installation is precisely the one where it was not: a control plane serving
people who are not its operator, whose operator has not reached Stripe yet,
configures neither, so nothing refused. The first person into an organization
becomes its owner and an owner holds `billing.manage`, so it was every
signed-in tenant rather than an administrator.

The question is now the one that actually decides it. `AF_OPERATOR_SETS_PLAN=1`
is an operator saying that whoever runs this installation also decides each
organization's plan, and without it the plan can only come from a signed Stripe
delivery. Off is the default because the configuration at risk is the one
nobody has configured, so a flag meaning "this is hosted" would have to be
remembered by exactly the operator who has not thought about it yet, and
forgetting this one closes the hole instead of opening it.

Setting it alongside any Stripe variable or the hosted plan gate stops the
process at startup rather than being refused per request, because a plan that
can be granted by hand is not a plan anybody has to buy, and a start-up refusal
covers whatever writes the plan next rather than the one route that carries the
check today.

# changed

Self-hosted installations that change plans from the Plan page need
`AF_OPERATOR_SETS_PLAN=1` set on the control plane. Without it the page shows
what each plan allows and offers no control, rather than offering one that is
always refused. Nothing else changes: the plan a control plane is already on
stays exactly where it is, and an operator has always been able to write the
column directly.

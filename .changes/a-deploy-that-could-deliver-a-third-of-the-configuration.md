# fixed

The hosted deploy path could set 16 of the 45 variables the control plane reads.

The reference documents 45 environment variables and the Terraform module is the
only route onto that container: `deploy/cd/deploy.sh` updates the image and sets
no environment at all, and `az containerapp update --set-env-vars` is drift the
next apply removes. So 29 documented variables had no supported way to be set,
and every symptom of that reads as a broken feature rather than as an unset
variable. The operator portal's 23 routes all refused for want of a database
credential. No sign-in link and no invitation could be sent. Billing was off
with a real Stripe price behind Team. The marketing site's beacon was refused
cross origin as a bare network error, the analytics stream recorded nothing, and
both actions were missing from the "No organization yet" screen.

The module now sets 34 of them, the operator credential included: its role is
created `NOLOGIN` by the migrations and the bootstrap job is what gives it a
login, inside the VNet, because Terraform cannot reach a server with no public
endpoint. The other 10 are exempt with a written reason, and the reasons are
real ones: `AF_VERSION` and `AF_COMMIT` are stamped into the image at build
time, `AF_MIGRATE` is absent from the serving process on purpose, and Enterprise
has no Stripe price because it is arranged with a person.

`tools/wirecheck` is the gate. Two checks already covered this ground and both
answered a nearby question: `tools/varcheck` and `config-docs.test.ts` prove a
variable is DOCUMENTED, and a variable that is documented, read by the
application, and unreachable by every apply passes both of them cleanly. That is
how 29 accumulated in silence with every instrument green.

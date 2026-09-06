# changed

A configuration change in the hosted control plane's tfvars now reaches the
running container through `cd.yml`, staging on the merge and production on
the tag after the approval, instead of waiting for a person to run a
targeted `terraform apply` and a traffic shift by hand.

On 2026-09-05 three such changes had to reach production: the PostHog region
and project key, then the three Stripe variables with their two Key Vault
references. Each was a line in `production.tfvars`, each was merged and
released, and each was inert. `deploy/cd/deploy.sh` runs `az containerapp
update --image`, which carries whatever template the app already has and sets
no variable of its own, and `cd.yml` ran no Terraform at all. Each change got
into the container only because somebody ran the apply behind a one-off guard
script at a terminal and then moved traffic. In between, the marketing site
went live pointing at `/ph` and the proxy answered 404 for an hour, because
the variable that turns it on was in a file and not in the app.

Both deploy jobs now run `deploy/cd/apply-config.sh` before `deploy.sh`. It
initialises the stack against the environment's own state, plans the tfvars
targeted at the container app alone, and hands the plan to `tools/configguard`,
which accepts exactly two shapes: no changes, or one in-place update of that
app whose only differences are the container's environment list and the app's
secret references. The serving image, the traffic weights, every other
attribute and every other resource must be identical before and after. A
create, a destroy, a replace, a second resource, an image or traffic change,
or any other attribute drift is refused with the reason and the plan summary,
nothing is applied, and the job stops there. It never shifts traffic: the
revision it makes sits at zero percent, and `deploy.sh` creates the one that
takes traffic from the same template a minute later, behind the migration and
both health gates. Production's alert receivers are read back out of state so
the action group stays a no-op, and the GitHub OAuth inputs are placeholders
because the vault secrets that carry them ignore their value.

What still needs a person is everything in the tfvars that is not the app's
configuration, a database SKU, a role assignment, the alerting module, a Key
Vault change, and the guard refuses a plan that carries one. The deploy
identity needs Key Vault Secrets User on each environment's vault to plan,
because refreshing the app's secret references reads their values; `ci.tf`
declares it and the production grant has to be applied once by hand, since
the targeted apply cannot create it for itself.

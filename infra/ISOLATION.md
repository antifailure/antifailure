# Azure isolation

Antifailure creates resources only inside resource groups it owns, and refuses
to operate anywhere else. This is enforced in three places rather than
documented in one.

## The boundary

Every resource this project creates lives in one of four resource groups, all
tagged `project=antifailure`:

| Group | Holds | Budget |
| --- | --- | --- |
| `af-dev-scus` | Development: registry, key vault, storage, the DNS zone for previews | 400 USD per month |
| `af-corpus-scus` | Corpus testing: the preview cluster and its spot node pool | 600 USD per month |
| `af-cp-scus` | The hosted control plane | 300 USD per month |
| `af-tfstate-scus` | Terraform remote state only | negligible |

Nothing outside these four is created, modified, read for configuration, or
deleted. No role is ever assigned at subscription scope.

## Resource groups that already exist in this subscription

Recorded so that the guard has something concrete to protect. At the time the
boundary was established these were `Ravioli`, `bonfire`, `NetworkWatcherRG`,
`ME_bonfire-aca-env_bonfire_eastus`, and `postiz-rg`. None of them belongs to
Antifailure and none of them is ever touched.

## How the boundary is enforced

1. **Terraform state is scoped.** Each group is a separate workspace with its
   own state file. A plan cannot reference a resource outside its workspace, so
   an accidental import or a copied resource block fails at plan time rather
   than at apply time.

2. **Identity is scoped.** The agent workstation identity and the CI federated
   identity hold Contributor on the three working groups and nothing at
   subscription scope. An operation against another group fails with an
   authorization error, which is the correct outcome: a permission that does
   not exist cannot be misused.

3. **A guard runs before every apply and every destructive command.**
   `tools/azguard` resolves the target of the operation and exits non zero
   unless the target resource group starts with `af-` and carries
   `project=antifailure`. It is wired into the Terraform wrapper, the leak
   detector, and the cost estimator, so there is no path that skips it.

## Cost

Budgets with alerts at 50, 80, and 100 percent are applied to each group by the
`foundation` module. Preview environments run on a spot node pool with a
30 minute idle sleep. `tools/cost estimate` reads a Terraform plan against
`infra/pricing.yaml` and refuses to apply anything whose projected monthly cost
exceeds the group's budget.

## Teardown

`terraform destroy` per workspace removes everything Antifailure created. The
leak detector then inventories the four groups and reports anything left, which
is how "we removed it" becomes a checked claim rather than an assumption.

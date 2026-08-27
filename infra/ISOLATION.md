# Azure isolation

Antifailure creates resources only inside resource groups it owns, and refuses
to operate anywhere else. This is enforced in three places rather than
documented in one.

## The boundary

Every resource this project creates lives in one of four resource groups, all
tagged `project=antifailure`:

| Group | Holds | Budget |
| --- | --- | --- |
| `af-dev-eastus` | Development: registry, key vault, storage, the DNS zone for previews | 400 USD per month |
| `af-corpus-eastus` | Corpus testing: the preview cluster and its spot node pool | 600 USD per month |
| `af-cp-eastus` | The hosted control plane | 300 USD per month |
| `af-tfstate-eastus` | Terraform remote state only | negligible |

These were named `-scus` when this document was written, for South Central US.
That region is denied on this subscription by a policy assignment called
`bonfire-allowed-locations`, which permits only `eastus`, `centralus` and
`global`. Quota was never the constraint; both regions have 65 cores. Note that
`af-web`, created by the site work, is also ours and also tagged.

Nothing outside these four is created, modified, read for configuration, or
deleted. No role is ever assigned at subscription scope.

## Resource groups that already exist in this subscription

Recorded so that the guard has something concrete to protect. At the time the
boundary was established these were `Ravioli`, `bonfire`, `NetworkWatcherRG`,
`ME_bonfire-aca-env_bonfire_eastus`, and `postiz-rg`. None of them belongs to
Antifailure and none of them is ever touched.

## How the boundary is enforced

Each mechanism below says plainly whether it exists today. This document
previously described all of it in the present tense while none of it was built,
which is the same bug as a manifest key that reads as configuration and behaves
as decoration, just written in prose.

1. **Terraform refuses the name.** *Built.* `resource_group_name` carries a
   variable validation, and the `foundation` module carries a matching
   precondition, so a plan naming a group that is not prefixed `af-` fails
   before it touches Azure. Proved against `Ravioli` and `postiz-rg`, both of
   which are refused at plan time.

2. **A guard refuses the command.** *Built.* `tools/azguard` reads the target
   out of an `az` or `terraform` command line and exits non-zero unless every
   resource group named is ours. It works offline, needs no credential, and so
   cannot be skipped for want of one. With `--tags` it additionally requires
   the group to exist and carry `project=antifailure`, and it **fails closed**:
   an error reading Azure is a refusal, not an assumption that the target was
   fine. A command naming no group at all is also refused, because the
   alternative is guessing about `az group delete`.

   ```sh
   go run ./tools/azguard check --tags af-cp-scus
   go run ./tools/azguard guard -- terraform apply -var resource_group_name=af-cp-scus
   ```

   Its tests assert that all five foreign groups in this subscription are
   refused, in either case, along with the near-misses (`prod-af-cp`, `afcp`)
   that a substring check would wave through.

3. **Identity is scoped.** *Not built.* The intent is that the workstation
   identity and the CI federated identity hold Contributor on the working
   groups and nothing at subscription scope, so that an operation elsewhere
   fails with an authorization error. There is no Entra app registration yet
   and no federated credential, so today this rests on 1 and 2 alone. Until it
   exists, the CI plan job skips rather than passes, and says so.

4. **Terraform state is scoped.** *Partly built.* The stacks are separate, each
   with its own state key, so one cannot reference the other's resources.
   The remote state account itself is created by `stacks/tfstate` and does not
   exist yet, so state is local until somebody runs it.

## Azure Policy is a fourth mechanism, and it is not ours

The subscription carries deny assignments that no code here controls:
`bonfire-allowed-locations`, `bonfire-deny-public-data` (Postgres and storage
must have `publicNetworkAccess` Disabled) and `bonfire-sku-allowlist`.

They matter to this document because **`terraform plan` does not evaluate
them**. A plan can be entirely clean and every resource refused at apply. The
modules therefore repeat the same rules as variable validations, so the refusal
happens at plan time and names the policy instead of arriving as
`RequestDisallowedByPolicy`.

One trap worth recording, because it made a guard silently useless: a
validation on a value that is unknown at plan time is SKIPPED, not failed. The
location check was originally on the control plane module, whose location comes
from the resource group's attribute and is unknown until apply, so it never ran
and a plan in a denied region looked fine. It is now on the stack's own input,
which is known.

## Cost

Budgets with alerts at 50, 80 and 100 percent are applied by the `foundation`
module, on **forecast as well as actual**, because a forecast crossing 100
percent on the fourth of the month is the one worth acting on.

`tools/cost estimate` reads a Terraform plan against `infra/pricing.yaml` and
prints a projected monthly bill; `--budget N` makes it refuse. Every price in
that file was read from the Azure retail prices API on the date recorded there.
The control plane stack currently projects **32.49 USD a month**.

A resource the estimator cannot price is reported `UNKNOWN` and suppresses the
total. This is deliberate: an estimator that silently prices what it does not
recognise at zero produces a confident, small, wrong number, which is worse
than no number. Things that genuinely cost nothing are listed as free with a
reason, so "this is free" and "I have never heard of this" cannot be confused.

Preview environments on a spot node pool with an idle sleep are *not built*;
they belong to the environment pool, which does not exist.

## Teardown

`terraform destroy` per stack removes everything Antifailure created. Confirm
it rather than assume it:

```sh
az resource list -g af-cp-scus -o table
```

Key Vaults are soft-deleted rather than purged on destroy, so the name is
unavailable for seven days afterwards. That is on purpose: a vault that can be
destroyed and immediately recreated is a vault whose secrets can be replaced by
somebody holding only delete.

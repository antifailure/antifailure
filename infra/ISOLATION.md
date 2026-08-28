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
| `af-cp-centralus` | The hosted control plane | 300 USD per month |
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
   go run ./tools/azguard check --tags af-cp-centralus
   go run ./tools/azguard guard -- terraform apply -var resource_group_name=af-cp-centralus
   ```

   Its tests assert that all five foreign groups in this subscription are
   refused, in either case, along with the near-misses (`prod-af-cp`, `afcp`)
   that a substring check would wave through.

3. **Identity is scoped.** *Built, for CI.* The Entra application
   `af-infra-ci` exists, and GitHub Actions federates into it. It holds
   **no client secret and no certificate at all**: two federated credentials,
   `repo:antifailure/antifailure:pull_request` and
   `repo:antifailure/antifailure:ref:refs/heads/main`, both against
   `https://token.actions.githubusercontent.com`. There is nothing to leak,
   nothing to rotate, and nothing that can be committed by accident. Revoking
   it is deleting a federated credential.

   FOUR credentials rather than two, because GitHub has moved to IMMUTABLE
   OIDC subjects that carry numeric organisation and repository ids rather than
   their names, and every example everywhere still shows the name form:

       repo:antifailure/antifailure:pull_request                      (name)
       repo:antifailure@321004801/antifailure@1346757509:pull_request (immutable)

   Only the second is what this repository actually presents. Entra's refusal,
   AADSTS700213, quotes the subject it was given and is entirely accurate, and
   it reads exactly like a typo in your own configuration. Both forms are
   registered so that a change in either direction does not break the job.

   Its entire authority, and this is the whole list:

   | Scope | Role | Why |
   | --- | --- | --- |
   | the `af-cp-centralus` group | Reader | refresh the stack, change nothing |
   | the state storage account | Storage Blob Data Reader | read the state, never write it |

   **Nothing at subscription scope**, which is also why both stacks set
   `resource_provider_registrations = "none"`: azurerm 4.x otherwise registers
   providers at startup, and registration is a write at subscription scope, so
   that one default would have forced a subscription level role.

   Two grants are deliberately absent and each is half of a pair:

   - No **Storage Blob Data Contributor**, so the plan runs `-lock=false`. The
     azurerm backend locks with a blob lease and a lease is a write. Granting
     it would let any pull request corrupt the record of everything the project
     owns, and a pull request can edit the workflow that uses the credential in
     the same commit that runs it.
   - No **Key Vault Secrets User**, so the plan runs `-refresh=false`.
     Refreshing an `azurerm_key_vault_secret` reads the secret's VALUE, which
     would put the live database URLs into a pull request job.

   The workstation identity is still unscoped: applies here are run by a
   subscription Owner. That half is *not built*.

   ONE LIMITATION, STATED RATHER THAN DISCOVERED: the `pull_request` subject
   matches ANY pull request in this repository, so anybody who can push a
   branch can reach this identity. A fork cannot, because GitHub withholds both
   the OIDC token and the secrets from forks. Narrowing it further means a
   GitHub environment with required reviewers, which has not been built.

4. **Terraform state is scoped.** *Built.* The stacks are separate, each with
   its own state key, so one cannot reference the other's resources. The remote
   state account is live in `af-tfstate-eastus`: Entra identity only with
   `shared_access_key_enabled = false`, a private container, thirty day soft
   delete and blob versioning. Its NAME is deliberately not written down here.
   `storage_account_name` has no default for the same reason, because a storage
   account name is global and this repository carries no environment's
   identifiers; `terraform output -raw backend_hcl` is where it comes from.

   IT TOOK A POLICY EXEMPTION AND THAT IS WORTH READING BEFORE JUDGING IT.
   `bonfire-deny-public-data` forces any storage account to
   `publicNetworkAccess = Disabled`, which is not a firewall default a network
   rule carves an exception out of: it turns the data plane off for everything
   that is not a private endpoint. Neither a laptop nor a hosted runner can
   reach it, and a CI plan with no state to compare against cannot report a
   destroy, which is the only reason that job exists.

   The exemption is scoped to `af-tfstate-eastus` alone, names that one
   assignment, is categorised `Mitigated` rather than `Waiver`, and expires.
   `infra/terraform/stacks/tfstate/exemption.tf` lists the five settings that
   preserve the policy's intent by other means. What it restores is
   REACHABILITY, not readability: a read still needs an Entra identity holding
   a data role. Delete the exemption and the next write to the account is
   denied.

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
az resource list -g af-cp-centralus -o table
```

Key Vaults are soft-deleted rather than purged on destroy, so the name is
unavailable for seven days afterwards. That is on purpose: a vault that can be
destroyed and immediately recreated is a vault whose secrets can be replaced by
somebody holding only delete.

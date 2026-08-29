---
title: Azure
description: Running the hosted pieces on Azure with Terraform, what it costs, and what still does not exist.
sidebar:
  order: 2
---

Nothing here is required. The engine runs on a laptop and in a GitHub Actions
runner with no cloud account. This is for running the control plane and a
shared environment pool yourself.

## What exists, and what does not

Said first, because a self-hosting page that describes an architecture nobody
can run is worse than a short one.

| Piece | State |
| --- | --- |
| Terraform remote state | **applied**, `af-tfstate-eastus`, and it took a policy exemption to be reachable |
| Control plane under Terraform | **applied**, `infra/terraform/stacks/control-plane` |
| Its Postgres, private, two roles | **applied** |
| Key Vault and budgets | **applied** |
| CI identity, federated, no secret | **applied**, `af-infra-ci` |
| Control plane on Kubernetes instead | **works**, the Helm chart, installed on a real cluster in CI |
| Goldens storage | **off by default**, see below |
| Environment pool on AKS | **does not exist** |

The goldens storage account is `goldens_enabled = false` on purpose. Nothing in
the control plane reads blob storage: there is no `@azure/storage` dependency
anywhere in `web/`, and no code path that opens a container. Creating an
account nothing reads is a resource that looks like a feature. It also cannot
be reached without a private endpoint, per the policy above, so it would be a
recurring cost for a consumer that does not exist. Turn it on when the golden
storage backend lands, and add the private endpoint in the same change.

`runtime.provider: kubernetes` is named in the manifest schema and refused at
startup with a message saying so, rather than quietly giving you containers on
whichever machine ran `af`. So the environment pool row above is not a gap in
this page; it is a gap in the product, and it is stated here rather than
implied away.

## Azure Policy will deny things a clean plan accepted

Worth reading before your first `terraform apply`, because this is the failure
mode that wastes an afternoon: **`terraform plan` does not evaluate Azure
Policy.** A deny assignment is applied by Azure at write time, so a plan can be
completely clean and every single resource still be refused.

The subscription this was developed against carries three, and the modules here
now refuse the same things at plan time so that the failure is early and names
the policy rather than arriving as an opaque `RequestDisallowedByPolicy`:

| Assignment | What it denies |
| --- | --- |
| `bonfire-allowed-locations` | every region except `eastus`, `centralus`, `global` |
| `bonfire-deny-public-data` | any Postgres flexible server or storage account whose `publicNetworkAccess` is not `Disabled` |
| `bonfire-sku-allowlist` | any flexible server outside `Standard_B1ms`, `Standard_B2s`, `Standard_D2ds_v4` |

**Storage.** `default_action = "Deny"` on a network rule is *not* enough: the
policy checks `publicNetworkAccess`, and a firewalled account still has it
enabled. An account that satisfies the policy is reachable only through a
private endpoint.

Check what your own subscription enforces before planning anything:

```sh
az policy assignment list --query "[].{name:name,scope:scope}" -o table
az policy definition show --name <definition> --query policyRule
```

## A region has three gates, and only one of them is the one everybody checks

This stack has been in three regions and each move was forced by a different
system. It is worth reading before you pick one, because the three are checked
at three different times by three different things, and the last one is
invisible to everything else.

| Gate | Asked by | When | Visible to a plan |
| --- | --- | --- | --- |
| Quota | `az vm list-usage` | whenever you look | no, and it was never the constraint |
| Azure Policy | Azure, at write time | `apply` | **no**, a deny assignment refuses a clean plan |
| Regional service availability | the provider's capabilities endpoint | `apply` | **no**, and the policy cannot see it either |

`southcentralus` is what the spec names, and `bonfire-allowed-locations` denies
it. `eastus` is allowed by that policy and is cheaper, so the default moved
there. Then an apply created twenty six of twenty seven resources and failed on
the database:

```
ParameterOutOfRange: The value of 'Version' should be in: []
```

The empty list is literal, and asking Azure directly explains it:

```sh
az postgres flexible-server list-skus -l eastus \
  --query "[0].{reason:reason,versions:supportedServerVersions}"
```

```json
{
  "reason": "Provisioning is restricted in this region. Please choose a different region.",
  "versions": []
}
```

PostgreSQL flexible server cannot be created in `eastus` on this subscription at
any version in any SKU, while every other resource in the stack creates there
quite happily. `centralus` offers versions 11 through 18 and every burstable
SKU, so that is where the control plane lives and the group is
`af-cp-centralus`. It costs about two dollars a month more than `eastus` would
have.

**Run this before you plan, not after you apply:**

```sh
go run ./tools/azguard region centralus
```

It fails closed. A region it cannot get an answer about is refused, because "I
could not tell" and "it is fine" must never look alike.

## Remote state, and the one policy exemption in this project

The state has to exist before the control plane does, because it is where the
control plane's record lives. `stacks/tfstate` creates it.

```sh
cd infra/terraform/stacks/tfstate
terraform apply -var subscription_id=... -var storage_account_name=...
terraform output -raw backend_hcl > ../control-plane/backend.hcl
```

**This needs a policy exemption and you should decide about it rather than
inherit it.** `bonfire-deny-public-data` forces any storage account to
`publicNetworkAccess = Disabled`, which is not a firewall default that a network
rule carves an exception out of: it turns the data plane off for everything that
is not a private endpoint. Neither a laptop nor a GitHub-hosted runner can reach
it, and a CI plan with no state to compare against cannot report a destroy,
which is the only reason that job exists.

`stacks/tfstate/exemption.tf` therefore exempts **that one resource group** from
**that one assignment**, categorised `Mitigated` and with an expiry date. It
earns the word: the account keeps `shared_access_key_enabled = false` so no
storage key exists, `allow_nested_items_to_be_public = false` so nothing can be
made anonymous, a private container, a TLS 1.2 floor, and RBAC on the data
plane. What the exemption restores is *reachability*, not readability. Delete it
and the next write to the account is denied.

Three sharp edges this hit, all of which will hit you:

- **Turning storage keys off breaks the provider, not just you.** After creating
  an account the `azurerm` provider polls the blob service to see whether the
  data plane is up, using a shared key. With keys disabled it gets `403 Key
  based authentication is not permitted`, while the account is perfectly
  healthy. Set `storage_use_azuread = true` on the provider.
- **Owner on the subscription does not let you read a blob.** Azure splits
  storage into a control plane and a data plane; Owner covers the first and
  grants nothing on the second. You need an explicit data role, and expect to
  re-run once while RBAC propagates.
- **`prevent_destroy` and a tainted resource deadlock.** If a create fails after
  Azure made the resource, Terraform taints it, the next plan proposes a
  replace, and `prevent_destroy` refuses. The error names `prevent_destroy`, so
  the tempting move is to delete the guard on the resource you least want to
  lose. `terraform untaint` is the fix.

## The control plane

```sh
go run ./tools/azguard region centralus     # third gate, before anything else

cd infra/terraform/stacks/control-plane
terraform init -backend-config=backend.hcl
terraform apply \
  -var subscription_id=... \
  -var github_client_id=... \
  -var github_client_secret=... \
  -var github_redirect_uri=https://cp.example.com/auth/callback
```

One apply from nothing produces a resource group with a budget, a Postgres with
no public endpoint, a Key Vault holding every credential, a storage account for
goldens, the bootstrap job that makes the database usable, a maintenance job
that keeps the event partitions ahead, and the application on public HTTPS.

### Grant yourself write access to the vault, once

`assign_deployer_secret_officer` is **off by default** and that default is
deliberate. A role assignment whose principal is "whoever is running Terraform"
churns on every plan by a different caller, `principal_id` is ForceNew, and the
pull request plan job would then report a resource that **must be replaced** on
every single run. A plan that always carries a destroy is a plan people stop
reading, which is precisely how a real one gets waved through.

So it is one command, run once, by a human:

```sh
az role assignment create \
  --role "Key Vault Secrets Officer" \
  --assignee-object-id "$(az ad signed-in-user show --query id -o tsv)" \
  --assignee-principal-type User \
  --scope "$(terraform output -raw key_vault_id)"
```

### Plan with the same inputs you apply with

Every variable the plan job passes must match the apply, or its destroy count is
noise. That is why `TF_VAR_ci_principal_id` comes from a repository variable
rather than being left empty: unset, the count on a role assignment goes to zero
and every pull request reports "1 to destroy" for something nobody proposed to
remove.

The two GitHub OAuth secrets are the exception, and they are handled in the
module rather than by discipline. Terraform seeds them once and then carries
`ignore_changes` on the value, because it cannot know them and must not
overwrite them: creating an OAuth application is a human act on another service.
That is also what makes the rotation instruction in
[the control plane page](/docs/self-hosting/control-plane/) true. Without it,
the next apply would quietly put the placeholder back.

`resource_provider_registrations = "none"` is set on the provider, so Terraform
never tries to register a resource provider, because registration is a write at
subscription scope and no identity here holds one. On a subscription where a
provider is not yet registered, apply fails naming the namespace and the fix is
`az provider register --namespace <name>` run by somebody who is allowed to.

Container Apps rather than AKS, deliberately. The control plane is one web
process and a database. The cheapest always-on AKS control plane is around 75
USD a month before a single node runs, and buys nothing here. If you want it on
Kubernetes anyway, the [Helm chart](/docs/self-hosting/control-plane/) installs on
any conformant cluster.

### After an upgrade that carries new migrations

The bootstrap job is idempotent and applies whatever is outstanding.

```sh
az containerapp job start -n afcp-bootstrap -g af-cp-centralus
```

## What it costs

Read from the Azure retail prices API rather than remembered, for `centralus`,
and kept in `infra/pricing.yaml` with the date it was checked.

That file has carried three regions now, and the sentence you are reading said
`southcentralus` while the file said `eastus`, which is exactly the sort of
stale number a reader has no way to catch. If the two ever disagree again,
believe `infra/pricing.yaml`: it has a `checked` field and prose does not.

```sh
terraform show -json plan.tfplan > plan.json
go run ./tools/cost estimate --plan plan.json --pricing infra/pricing.yaml
```

At the defaults, **30.47 USD a month**:

| Item | Monthly |
| --- | --- |
| Postgres flexible server, B1ms, 32 GB | 18.18 |
| Container App, 0.5 vCPU / 1 GiB, one replica | 11.40 |
| Private DNS zone | 0.50 |
| Log Analytics, assuming 2 GB a month | 0.24 |
| Key Vault | 0.15 |

It was 28.34 in `eastus`, and the difference is what the third gate costs:
`centralus` charges 0.01921 an hour for a B1ms against 0.017, and 0.13 a
gigabyte-month for database storage against 0.115. Container Apps, the DNS zone
and Key Vault are the same in both.

`--budget N` turns the estimate into a gate that refuses a plan projected above
the resource group's budget. A resource the tool cannot price is reported
`UNKNOWN` and suppresses the total, because an estimator that silently prices
what it does not recognise at zero gives a confident, small, wrong number.

Three ways to spend much more than the table above, all off by default:
`high_availability` runs a second server and needs a non-burstable SKU (which
`bonfire-sku-allowlist` would refuse here anyway), a chatty diagnostic setting
bills Log Analytics ingestion at 2.30 USD a gigabyte, and a private endpoint is
a real hourly charge. `infra/pricing.yaml` deliberately carries no price for a
private endpoint, because the retail prices API does not expose one for this
region and the file only holds numbers that came from it, so the estimator
reports it `UNKNOWN` rather than as free.

## Two settings Azure adds that Terraform will try to remove

Both of these produce a plan that never converges, and a plan that always shows
a diff is a plan people stop reading, which is how a real destroy gets past a
reviewer.

- Creating a flexible server on a delegated subnet makes the platform attach the
  **`Microsoft.Storage` service endpoint** to that subnet for its own backup
  traffic.
- Every managed environment gets a default **`Consumption` workload profile**.

Terraform created neither, so it proposes to delete both, quietly, as small
blocks inside otherwise uninteresting in-place updates. Azure then puts them
back. Both are declared in the module for that reason, with a comment saying so,
and the stack now plans `0 to change` against itself.

If you fork these modules and see a permanent diff on a subnet or an
environment, this is why, and the fix is to declare what the platform set rather
than to keep deleting it.

## Isolation

Everything created lives in a resource group prefixed `af-` and tagged
`project=antifailure`, which is what makes a cleanup scoped to that tag unable
to reach anything else in a subscription that also holds other work. The full
boundary is in `infra/ISOLATION.md`.

It is enforced in three places rather than documented in one:

```sh
go run ./tools/azguard check --tags af-cp-centralus
go run ./tools/azguard guard -- terraform apply -var resource_group_name=af-cp-centralus
```

`azguard` refuses by name, offline, before any credential is needed, and fails
closed: if it cannot read the tags it refuses rather than assuming. Terraform
refuses the same names at plan time through a variable validation, so a group
belonging to another project cannot be reached even by someone who bypasses the
guard.

## Planning in CI, with no secret anywhere

`.github/workflows/infra.yml` plans on every pull request that touches
`infra/`, so a change that would **destroy** something is visible in review
rather than discovered by whoever runs apply.

It authenticates with a federated credential and **no client secret exists at
all**. The Entra application `af-infra-ci` carries no password and no
certificate; GitHub Actions presents an OIDC token and Azure exchanges it. There
is nothing to leak, nothing to rotate, and nothing that can be committed by
accident. Revoking it is deleting a federated credential.

```sh
az ad app create --display-name af-infra-ci --sign-in-audience AzureADMyOrg
az ad sp create --id <appId>
az ad app federated-credential create --id <objectId> --parameters '{
  "name": "github-pull-request",
  "issuer": "https://token.actions.githubusercontent.com",
  "subject": "repo:<owner>/<repo>:pull_request",
  "audiences": ["api://AzureADTokenExchange"]
}'
```

**The subject in that example is probably wrong for your repository, and the
error will not say so.** GitHub has moved to *immutable* OIDC subjects, which
carry the numeric organisation and repository ids rather than their names:

```
subject claim - repo:antifailure@321004801/antifailure@1346757509:pull_request
```

Every example on the internet, including the one above, shows the login form.
If yours is on the immutable format, Entra answers:

```
AADSTS700213: No matching federated identity record found for presented
assertion subject 'repo:<org>@<orgid>/<repo>@<repoid>:pull_request'
```

which is accurate and reads like a typo in your own configuration. **Read the
subject out of the failing job's log and create a credential that matches it
exactly.** Keeping both forms costs nothing, an application takes twenty
federated credentials, and it means a change to the format in either direction
does not break the job:

```sh
gh api repos/<owner>/<repo> --jq '{repo_id:.id, owner_id:.owner.id}'
```

Then set `AZURE_CLIENT_ID`, `AZURE_TENANT_ID` and `AZURE_SUBSCRIPTION_ID` as
repository secrets, plus `AZURE_TFSTATE_RG` and `AZURE_TFSTATE_ACCOUNT` if you
want it to read real state. None of those five is a credential; they are
identifiers, and the whole design is that the credential does not exist.

**The identity's entire authority**, which is short on purpose:

| Scope | Role |
| --- | --- |
| the control plane resource group | Reader |
| the state storage account | Storage Blob Data Reader |
| the state storage account | Reader |

The last two look redundant and are not. Azure splits storage into a control
plane and a data plane and a role on one grants nothing on the other, **in both
directions**: Owner on the subscription cannot read a blob, and Storage Blob
Data Reader cannot perform `Microsoft.Storage/storageAccounts/read`, which the
`azurerm` backend does before reading any state, to resolve the blob endpoint.
The error names a read action while the identity is called a Reader, so it takes
a moment to see. Both roles are read-only.

Nothing at subscription scope. Two grants are deliberately absent, and each is
half of a pair with a flag in the workflow:

- No **Storage Blob Data Contributor**, so the plan runs `-lock=false`. The
  backend locks with a blob lease and a lease is a write. Granting it would let
  any pull request corrupt the record of everything the project owns, and a pull
  request can edit the workflow that uses the credential in the same commit that
  runs it.
- No **Key Vault Secrets User**, so the plan runs `-refresh=false`. Refreshing
  an `azurerm_key_vault_secret` reads the secret's *value*, which would put the
  live database URLs into a pull request job.

### The job has three modes and always says which one it ran

| Condition | What you get |
| --- | --- |
| no `AZURE_CLIENT_ID` | **skipped**, and it says it checked nothing |
| credential, no state secrets | **planned from an empty state**: real Azure, real cost estimate, and a summary whose first line says it *cannot report a destroy* |
| credential and state secrets | **planned against real state**, the only mode in which "0 to destroy" is evidence |

A green check that could not see the thing it exists to see is worse than a
missing one, which is why the middle mode announces its own blindness instead of
passing quietly.

## Quota

```
AF-INF-001 The cloud API returned a quota error for standardDSv5Family in
eastus.
  Next: Request more standardDSv5Family in eastus, then run the command again.
```

The first thing to check on a new subscription, because the default limits are
low and an increase can take a day to be approved.

```sh
az vm list-usage --location eastus -o table
```

Ask for the family the node pool uses, not the total. A subscription can have
plenty of total cores and none of the family a pool wants, and the error names
which. This matters for an AKS pool; the control plane above needs no VM quota
at all, because Container Apps and a flexible server do not consume it.

## Tearing it down

```sh
terraform destroy
```

Then confirm, rather than assume:

```sh
az resource list -g af-cp-centralus -o table
```

The Key Vault is soft-deleted rather than purged, on purpose: a vault that can
be destroyed and recreated immediately is one whose secrets can be replaced by
somebody holding only delete.

**That has a consequence worth knowing before you destroy anything, not after.**
A Key Vault name is GLOBAL, a soft-deleted vault keeps its name for the
retention period, and purge protection means nobody can release it early, not
even the person who owns it. So `terraform destroy` followed by `terraform
apply` in the same region inside seven days fails on the vault, with an error
about a name conflict rather than about soft delete.

The vault name therefore includes the location, `<name>-kv-<location>`, so that
moving regions works. Nothing can make same-region recreation work inside the
window, because that is precisely the sequence purge protection exists to
prevent. Set `key_vault_name` yourself if you need to sidestep it knowingly.

Related: [the control plane](/docs/self-hosting/control-plane/),
[configuration](/docs/reference/control-plane/).

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
| Control plane under Terraform | **written**, `infra/terraform/stacks/control-plane`. Plans clean; never applied. |
| Its Postgres, private, two roles | **written** |
| Key Vault and budgets | **written** |
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

Two consequences that shaped the modules:

**The region.** The spec names the control plane's group `af-cp-scus`, and
South Central US is denied here, so the default is `eastus` and the group is
`af-cp-eastus`. Quota was never the constraint; both regions have 65 cores.
eastus is also cheaper for this stack.

**Storage.** `default_action = "Deny"` on a network rule is *not* enough: the
policy checks `publicNetworkAccess`, and a firewalled account still has it
enabled. An account that satisfies the policy is reachable only through a
private endpoint.

Check what your own subscription enforces before planning anything:

```sh
az policy assignment list --query "[].{name:name,scope:scope}" -o table
az policy definition show --name <definition> --query policyRule
```

## The control plane

```sh
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

Container Apps rather than AKS, deliberately. The control plane is one web
process and a database. The cheapest always-on AKS control plane is around 75
USD a month before a single node runs, and buys nothing here. If you want it on
Kubernetes anyway, the [Helm chart](/docs/self-hosting/control-plane/) installs on
any conformant cluster.

### After an upgrade that carries new migrations

The bootstrap job is idempotent and applies whatever is outstanding.

```sh
az containerapp job start -n afcp-bootstrap -g af-cp-eastus
```

## What it costs

Read from the Azure retail prices API rather than remembered, for
`southcentralus`, and kept in `infra/pricing.yaml` with the date it was checked.

```sh
terraform show -json plan.tfplan > plan.json
go run ./tools/cost estimate --plan plan.json --pricing infra/pricing.yaml
```

At the defaults, **28.34 USD a month**:

| Item | Monthly |
| --- | --- |
| Postgres flexible server, B1ms, 32 GB | 16.09 |
| Container App, 0.5 vCPU / 1 GiB, one replica | 11.40 |
| Private DNS zone | 0.50 |
| Log Analytics, assuming 2 GB a month | 0.20 |
| Key Vault | 0.15 |

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

## Isolation

Everything created lives in a resource group prefixed `af-` and tagged
`project=antifailure`, which is what makes a cleanup scoped to that tag unable
to reach anything else in a subscription that also holds other work. The full
boundary is in `infra/ISOLATION.md`.

It is enforced in three places rather than documented in one:

```sh
go run ./tools/azguard check --tags af-cp-eastus
go run ./tools/azguard guard -- terraform apply -var resource_group_name=af-cp-eastus
```

`azguard` refuses by name, offline, before any credential is needed, and fails
closed: if it cannot read the tags it refuses rather than assuming. Terraform
refuses the same names at plan time through a variable validation, so a group
belonging to another project cannot be reached even by someone who bypasses the
guard.

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
az resource list -g af-cp-eastus -o table
```

The Key Vault is soft-deleted rather than purged, on purpose: a vault that can
be destroyed and recreated immediately is one whose secrets can be replaced by
somebody holding only delete. It will block a new vault of the same name for
seven days.

Related: [the control plane](/docs/self-hosting/control-plane/),
[configuration](/docs/reference/control-plane/).

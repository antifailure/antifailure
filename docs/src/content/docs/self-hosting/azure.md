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
| Alerting, an action group and eleven rules | **applied** in production, `infra/terraform/modules/alerting`, off unless `alerting_enabled`. Staging runs without it on purpose |
| Production, `app.antifailure.dev` | **applied**, `af-cp-prod-centralus`, serving on a managed certificate. [Standing up production](/docs/self-hosting/production) |
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
  -var github_redirect_uri=https://cp.example.com/auth/github/callback
```

One apply from nothing produces a resource group with a budget, a Postgres with
no public endpoint, a Key Vault holding every credential, a storage account for
goldens, the bootstrap job that makes the database usable, a maintenance job
that keeps the event partitions ahead, and the application on public HTTPS.

### An apply that changes the app changes nothing, until traffic moves

The container app runs in `Multiple` revision mode, and ownership is split:
Terraform owns the template, continuous deployment owns the image and the
traffic weights. The module says so, with `ignore_changes` on
`template[0].container[0].image` and `ingress[0].traffic_weight`, and the split
is what lets the two run on their own schedules instead of undoing each other.

The consequence is not obvious and it has already caught us once. Any Terraform
change to the template creates a **new revision**, and that revision comes up
with **zero percent of traffic**. Terraform reports a successful apply.
Production is still serving the old revision, without the change. Add an
environment variable this way and the application will not see it, for as long
as nobody deploys.

The mechanism is worth stating exactly, because the obvious explanation is the
wrong one. Terraform does not leave the traffic block out. It sends one, and
`ignore_changes` decides which one: the value it refreshed from Azure rather
than the value written in the configuration.

Those two do not say the same thing. The configuration asks for
`latest_revision = true` at one hundred percent, which would put every new
revision straight into service. What Azure actually holds, once any deploy has
run, is a pin naming one revision:

```hcl
traffic_weight = [{
  latest_revision = false
  percentage      = 100
  revision_suffix = "c64e67a86-031648"
}]
```

So the apply reasserts the pin it just read, the revision named there keeps all
of the traffic, and the one Terraform has built gets none of it. It is not that
Terraform declines to move traffic. It is that Terraform faithfully puts back
the arrangement it found, and the revision it is creating is not in it.

Which is why the question "where will the traffic be after this apply" is
answered by Azure and not by anything in this repository. Ask it directly, with
the commands below, before and after.

So after an apply that touched the template, check what is actually serving:

```sh
az containerapp ingress traffic show -n afcp-app -g af-cp-centralus -o table
az containerapp revision list -n afcp-app -g af-cp-centralus \
  --query "[?properties.active].{rev:name,created:properties.createdTime}" -o table
```

If the newest revision is not the one with the weight, either run a deploy,
which creates its own revision from the current image and shifts onto it, or
move the traffic yourself:

```sh
az containerapp ingress traffic set -n afcp-app -g af-cp-centralus \
  --revision-weight <newest-revision>=100
```

Moving it by hand is a traffic shift and not a rollback: both revisions run the
same image unless a deploy happened in between.

### Terraform state is not a record of what is serving

Once traffic moves, whether a deploy moved it or you moved it with the command
above, the stored state file keeps the OLD revision suffix, and it keeps it
indefinitely. Nothing writes the true value back, because `ignore_changes` on
`ingress[0].traffic_weight` is exactly what stops Terraform caring.

Both of our environments were stale that way when this was written, each by more
than one deploy. Neither was a fault and neither needed repairing.

The distinction that matters is between the STORED file and a REFRESH. A plan
and an apply both refresh, so the value they act on is the one they just read
from Azure, and it is current. `terraform state show` and `terraform state pull`
read the stored file, and it is not. On the deployment this was written against,
the stored file named a revision that had already been deactivated while the
plan's own view named the one actually serving.

So: **do not ask this repository what is serving.** Not the state file, which
answers confidently and wrongly, and not a plan either. An empty plan means
Terraform intends no change, and because this attribute is ignored, that is not
a statement about where traffic is. Ask Azure, with the two commands above.

The one case that needs real care is REMOVING that `ignore_changes`, and the
consequence is the opposite of what the stale file suggests. The stored suffix
is not what would take effect: the configuration is. `latest_revision = true`
would win, so traffic would follow the newest revision automatically, every
Terraform apply would put its own revision into service at one hundred percent
with no opportunity to probe it first, and each apply would undo the pin the
deploy pipeline sets. If you want that, it is a deliberate change to how
releases work here and not a tidy-up of a stale field.

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

### Turning on the parts that need a credential

Five features are off until somebody turns them on, and four of them need a
credential that Terraform must never hold: the operator portal, analytics,
signing in with a link, and billing.

**Terraform generates two of them and references the others.** The operator database
password and the analytics surrogate secret are generated by the module, so
nobody ever holds them: they go from the random provider into Key Vault and into
the container. The Stripe key, the Stripe webhook secret and the Resend key are
minted on somebody else's service. Stripe and GitHub App credentials are
addressed by their vault names without reading their values during planning.
The application's managed identity resolves them when it starts. Resend still
uses a data source and requires vault read permission for the planning identity.

So the order is: **put the secret in the vault, then set the switch.** A plan
for billing does not prove the credentials exist. Azure resolves those
references during deployment and names any missing secret. Verify both Stripe
credentials before enabling billing, and then verify checkout and its webhook
through the running application.

```sh
VAULT="$(terraform output -raw key_vault_name)"

# Billing. The Team price is the switch; there is no Enterprise price and there
# is not meant to be one, because Enterprise is arranged with a person.
az keyvault secret set --vault-name "$VAULT" -n stripe-secret-key     --value "$STRIPE_SECRET_KEY"
az keyvault secret set --vault-name "$VAULT" -n stripe-webhook-secret --value "$STRIPE_WEBHOOK_SECRET"

# Signing in with a link, and inviting somebody who is not in your GitHub
# organization. mail_from is the switch and public_url is then required.
# READ THE DNS SECTION BELOW FIRST: a verified key is not a domain that can send.
az keyvault secret set --vault-name "$VAULT" -n resend-api-key --value "$RESEND_API_KEY"
```

#### Mail needs DNS before it needs a key

Setting `mail_from` and putting a Resend key in the vault does not make mail
arrive. The domain has to be able to send, and that is DNS, which is not in this
repository and no `terraform apply` will fix it. Check before you set the
variable, because the failure is silent at the sender:

```sh
dig +short MX example.com
dig +short TXT example.com                    # the SPF record
dig +short TXT _dmarc.example.com
dig +short TXT resend._domainkey.example.com  # the DKIM key Resend published
```

`antifailure.dev` today answers with no MX, `v=spf1 -all`, a DMARC policy of
`p=reject; sp=reject; adkim=s; aspf=s`, and `v=DKIM1; p=` on the Resend selector.
Read in order: nothing receives mail for the domain, **no** sender is authorised
to send as it, receivers are told to reject anything that fails alignment, and
the DKIM key is **revoked** rather than merely absent, since an empty `p=` is
how a key is withdrawn. Somebody set Resend up for this domain and then revoked
it. Mail sent as anything at that domain fails SPF, fails DKIM, and is rejected
outright by every receiver that honours DMARC, which is all the large ones.

So the order for mail is: fix the DNS, verify the domain in Resend, then set
`mail_from`. Until then leave it empty. Nothing breaks in the meantime and it is
worth knowing exactly what still works, because it is more than it sounds:

- **Sign-in is unaffected.** GitHub is the front door and is always offered; the
  mailed link is an additional method for a preview environment or an isolated
  network, and its route is not registered at all when mail is not set up, so
  there is no button that fails on press.
- **Invitations work by copy and paste.** The link is returned to the inviter
  and shown on screen whether or not mail is configured, because an invitation
  that existed only as an email would silently do nothing on a self-hosted plane
  with no mailer. A send that fails does not fail the invitation either.
- **Enterprise leads are still recorded**, and are read with
  `af-control-plane-backup leads`. `lead_notify_email` is what announces them,
  and the module refuses a plan that sets it without `mail_from`.

Then the switches, in a tfvars file:

```hcl
operator_portal_enabled = true              # generates the operator credential
admin_pool_max          = 4

analytics_enabled      = true               # generates the surrogate secret
analytics_operator_org = "your-org-slug"    # who may read the dashboard
site_origin            = "https://example.com"

mail_from         = "no-reply@example.com"   # only once the DNS below is right
public_url        = "https://cp.example.com"
lead_notify_email = "sales@example.com"

stripe_price_team = "price_..."

github_app_install_url = "https://github.com/apps/your-app/installations/new"
```

**The operator portal is the one with a second half.** Its role,
`antifailure_admin`, is created by the migrations as `NOLOGIN` with no password
and holds `BYPASSRLS`, which is an attribute rather than a grant and is the only
mechanism that reads across tenants. Terraform cannot give it a login, because
the server has no public endpoint and a plan running in CI is not inside the
VNet. The bootstrap job does it, inside the network, from the same image, and it
refuses rather than guessing: a role that does not exist, does not hold
`BYPASSRLS`, or lacks the privileges of `antifailure_admin` stops the job with a
message naming which. So an apply that turns the portal on is not finished until
the bootstrap job has run, which a deploy does.

Which brings back [the revision trap above](#an-apply-that-changes-the-app-changes-nothing-until-traffic-moves).
Every switch here changes the container template, so every one of them creates a
revision at **zero percent of traffic**. The apply will report success and the
feature will not be on. Run a deploy, or move the traffic yourself, and check
what is actually serving:

```sh
az containerapp show -n afcp-app -g af-cp-centralus   --query "properties.template.containers[0].env[].name" -o tsv | sort
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
[the control plane page](/docs/self-hosting/control-plane) true. Without it,
the next apply would quietly put the placeholder back.

`resource_provider_registrations = "none"` is set on the provider, so Terraform
never tries to register a resource provider, because registration is a write at
subscription scope and no identity here holds one. On a subscription where a
provider is not yet registered, apply fails naming the namespace and the fix is
`az provider register --namespace <name>` run by somebody who is allowed to.

Container Apps rather than AKS, deliberately. The control plane is one web
process and a database. The cheapest always-on AKS control plane is around 75
USD a month before a single node runs, and buys nothing here. If you want it on
Kubernetes anyway, the [Helm chart](/docs/self-hosting/control-plane) installs on
any conformant cluster.

### After an upgrade that carries new migrations

The bootstrap job is idempotent and applies whatever is outstanding.

```sh
az containerapp job start -n afcp-bootstrap -g af-cp-centralus
```

### Upgrade and rollback, the manual path

`deploy/cd/deploy.sh` already does most of this: migrate first, start the new
revision at zero traffic, check it there, shift traffic, check the public
origin, and shift back on any failure after the shift. Read the script before
this section; it is short and it is the actual mechanism, not a summary of one.

What follows is for the case its own rollback does not fire, because the
failure showed up after the health gate already passed and the deploy exited.
An error rate that ramps up over the next hour, a customer report, a graph that
looks wrong: the gate cannot catch what has not happened yet, and once it exits
nothing is watching the deploy anymore. From here it is a person.

**1. Find the last revision that was actually good.**

```sh
az containerapp revision list -n afcp-app -g af-cp-centralus \
  --query "[?properties.active].{name:name, created:properties.createdTime, traffic:properties.trafficWeight, fqdn:properties.fqdn}" \
  -o table
```

Old revisions are left active at zero traffic rather than deactivated, exactly
so this list has something to go back to; deploy.sh's own comment says why.
"The one before this one" is not the same question as "the last one that was
good": if two bad releases shipped in a row, the previous revision is also
broken. Cross-reference against the CD run history
(`gh run list --workflow=cd.yml` or the Actions tab) for the last run whose
"What is serving" step summary showed a healthy `/readyz`, and note which
commit it deployed. That commit is what you are rolling back to, and the
revision list above tells you which revision name still serves it. If the
revision is gone, `deploy.sh`'s promotion step will make you a new one from the
same image, at zero traffic, checked before it takes any.

**2. Move traffic to it.**

```sh
az containerapp ingress traffic set -n afcp-app -g af-cp-centralus \
  --revision-weight <good-revision>=100
```

This is the exact command step 5 of `deploy.sh` runs on your behalf when its
own gate catches the failure. Running it by hand is not a lesser version of the
same action.

**3. Verify it took, the same way the pipeline does.**

A revision report and a real check are not the same evidence: `az` can say the
weight moved while the origin still answers from a cache or a stale connection.
Run the actual gate against the public origin:

```sh
deploy/cd/health-gate.sh https://app.antifailure.dev <commit-you-rolled-back-to> 20 3
```

It checks two things, not one: that `/readyz` answers, and that it names the
commit you expect. A healthy answer from the wrong commit is exactly the
failure this script exists to catch, and it is the one a plain `curl` would
miss.

**4. The migration that already applied.**

This is the genuinely hard part, and it deserves more than "roll the schema
back too", because there is no such command here. `web/packages/db`'s migration
runner has no down migration and has never had one: each file is one
transaction, applied and recorded together, so a migration is either fully
applied or not applied at all. There is no partial state to reason about, which
narrows the problem to exactly two cases.

**The migration is additive.** This is the case the whole design assumes, and
it is why `deploy.sh`'s own comment states the constraint plainly: migrations
in this project are expected to be backward compatible with the previous
release. A new nullable column, a new table, a new index, a new policy grant,
none of it is visible to a query the old code never learned to send. If that
holds, step 2 above is already the whole fix: the revision you just moved
traffic back to runs correctly against the schema as it now stands, and nothing
about the database needs to change. Do not assume this. Read the migration
files that shipped with the release you are rolling back, the same files
`git diff <good-commit>..<bad-commit> -- web/packages/db/migrations` shows you,
and check each statement is additive rather than something that removes or
narrows what the old code depends on: a dropped or renamed column, a `NOT NULL`
added with no default, a changed type, a revoked grant. This is a five minute
read and it is not optional. Assuming compatibility instead of checking it is
how a rollback becomes a second incident.

**The migration is not additive.** Now the old code is the one that breaks,
because it is querying a column, a type, or a grant that no longer matches what
it expects. Moving traffic back in this case does not fix anything; it trades
one broken revision for a different one. There is no third option that makes
both sides correct at once, because the schema and the code serving it cannot
disagree, and disagreement, not either release on its own, is the incident:

- Do not write a rollback migration under incident pressure. A migration
  authored in a hurry, run once and never tested against the same suite every
  other migration goes through, is exactly the kind of change this project's
  own migration runner has been burned by before: a failed statement leaves the
  connection in an aborted transaction, and every diagnostic that runs after it
  in the same connection reports the aborted state rather than the real cause.
  Fast, under-tested schema changes are where that shows up.
- Compare what each side actually does in production right now: is the new
  code erroring in a way worse than the old code would against the changed
  schema, or the other way around. Whichever fails less badly is what stays
  serving while the real fix is written, and that choice is a judgment call
  under real constraints, not a formula. Say which way you chose and why in the
  incident record, because the next person reading it needs the reasoning more
  than the outcome.
- The actual fix is forward, not back: a new migration that restores what the
  old code needs, or, if the new code is staying, a migration that finishes
  what it started, tested the same way any migration is, through a normal pull
  request and the kind cluster check in `control-plane-image.yml`, then
  deployed the same way any deploy is. There is no faster correct path than
  that, because a schema and the traffic serving it cannot be made to agree by
  moving a traffic weight.
- Afterwards, name the specific miss. It is almost always one release both
  dropping or renaming a column and no longer being read by anything that still
  expects it. The prevention is a convention rather than a tool: deprecate a
  column for one release before dropping it, so the release that stops writing
  it and the release that removes it are never the same one.

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

Related: [the control plane](/docs/self-hosting/control-plane),
[standing up production](/docs/self-hosting/production),
[the runbooks](/docs/self-hosting/runbooks),
[configuration](/docs/reference/control-plane).

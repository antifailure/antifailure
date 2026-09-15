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

| Piece | State |
| --- | --- |
| Terraform remote state | **applied**, `af-tfstate-eastus`, and it took a policy exemption to be reachable |
| Control plane under Terraform | **applied**, `infra/terraform/stacks/control-plane` |
| Its Postgres, private, two roles | **applied** |
| Key Vault and budgets | **applied** |
| CI identity, federated, no secret | **applied**, `af-infra-ci` |
| Control plane on Kubernetes instead | **works**, the Helm chart, installed on a real cluster in CI |
| Goldens storage | **off by default**, see below |
| Alerting, an action group and twelve rules | **applied** in production, `infra/terraform/modules/alerting`, off unless `alerting_enabled`. Staging runs without it on purpose |
| Production, `app.antifailure.dev` | **applied**, `af-cp-prod-centralus`, serving on a managed certificate. [Standing up production](/docs/self-hosting/production) |
| Environment pool on AKS | **does not exist** |

The goldens storage account is `goldens_enabled = false` on purpose. Nothing in
the control plane reads blob storage: there is no `@azure/storage` dependency
anywhere in `web/`, and no code path that opens a container. Turn it on when the
golden storage backend lands, and add the private endpoint in the same change.

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

| Gate | Asked by | When | Visible to a plan |
| --- | --- | --- | --- |
| Quota | `az vm list-usage` | whenever you look | no, and it was never the constraint |
| Azure Policy | Azure, at write time | `apply` | **no**, a deny assignment refuses a clean plan |
| Regional service availability | the provider's capabilities endpoint | `apply` | **no**, and the policy cannot see it either |

`southcentralus` is what the spec names, and `bonfire-allowed-locations` denies
it. `eastus` is allowed by that policy and is cheaper, so the default moved
there. An apply there then failed on the database:

```
ParameterOutOfRange: The value of 'Version' should be in: []
```

The empty list is literal:

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
any version in any SKU; every other resource in the stack creates there.
`centralus` offers versions 11 through 18 and every burstable SKU, so the
control plane lives there and the group is `af-cp-centralus`. It costs about two
dollars a month more than `eastus`.

**Run this before you plan, not after you apply:**

```sh
go run ./tools/azguard region centralus
```

It fails closed. A region it cannot get an answer about is refused.

## Remote state, and the one policy exemption in this project

The state has to exist before the control plane does. `stacks/tfstate` creates it.

```sh
cd infra/terraform/stacks/tfstate
terraform apply -var subscription_id=... -var storage_account_name=...
terraform output -raw backend_hcl > ../control-plane/backend.hcl
```

**This needs a policy exemption.** `bonfire-deny-public-data` forces any storage
account to `publicNetworkAccess = Disabled`, which turns the data plane off for
everything that is not a private endpoint. Neither a laptop nor a GitHub-hosted
runner can reach it, and a CI plan with no state to compare against cannot
report a destroy, which is the only reason that job exists.

`stacks/tfstate/exemption.tf` exempts **that one resource group** from **that
one assignment**, categorised `Mitigated` and with an expiry date. The account
keeps `shared_access_key_enabled = false` so no storage key exists,
`allow_nested_items_to_be_public = false` so nothing can be made anonymous, a
private container, a TLS 1.2 floor, and RBAC on the data plane. The exemption
restores *reachability*, not readability. Delete it and the next write to the
account is denied.

Three sharp edges:

- **Turning storage keys off breaks the provider.** After creating an account
  the `azurerm` provider polls the blob service to see whether the data plane is
  up, using a shared key. With keys disabled it gets `403 Key based
  authentication is not permitted`. Set `storage_use_azuread = true` on the
  provider.
- **Owner on the subscription does not let you read a blob.** Azure splits
  storage into a control plane and a data plane; Owner covers the first and
  grants nothing on the second. You need an explicit data role, and expect to
  re-run once while RBAC propagates.
- **`prevent_destroy` and a tainted resource deadlock.** If a create fails after
  Azure made the resource, Terraform taints it, the next plan proposes a
  replace, and `prevent_destroy` refuses. `terraform untaint` is the fix.

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
`template[0].container[0].image` and `ingress[0].traffic_weight`.

Any Terraform change to the template creates a **new revision**, and that
revision comes up with **zero percent of traffic**. Terraform reports a
successful apply while production still serves the old revision. Add an
environment variable this way and the application will not see it until somebody
deploys.

Terraform does not leave the traffic block out. It sends one, and
`ignore_changes` decides which one: the value it refreshed from Azure rather
than the value written in the configuration.

The configuration asks for `latest_revision = true` at one hundred percent. What
Azure actually holds, once any deploy has run, is a pin naming one revision:

```hcl
traffic_weight = [{
  latest_revision = false
  percentage      = 100
  revision_suffix = "c64e67a86-031648"
}]
```

So the apply reasserts the pin it just read, the revision named there keeps all
of the traffic, and the one Terraform built gets none of it.

After an apply that touched the template, check what is actually serving:
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

A stale suffix is not a fault and does not need repairing.

The distinction that matters is between the STORED file and a REFRESH. A plan
and an apply both refresh, so the value they act on is the one they just read
from Azure, and it is current. `terraform state show` and `terraform state pull`
read the stored file, and it is not.

So: **do not ask this repository what is serving.** Not the state file, which
answers confidently and wrongly, and not a plan either. An empty plan means
Terraform intends no change, and because this attribute is ignored, that is not
a statement about where traffic is. Ask Azure, with the two commands above.

The one case that needs real care is REMOVING that `ignore_changes`. The
configuration, not the stored suffix, is what would take effect:
`latest_revision = true` would win, so traffic would follow the newest revision
automatically, every Terraform apply would put its own revision into service at
one hundred percent with no opportunity to probe it first, and each apply would
undo the pin the deploy pipeline sets.

### Grant yourself write access to the vault, once

`assign_deployer_secret_officer` is **off by default**: `principal_id` is
ForceNew, so a role assignment whose principal is whoever runs Terraform would
make the pull request plan job report a resource that **must be replaced** on
every single run.

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

**`--value` is the wrong way to do this and it is not in the commands below.**
`rotating-secrets.md` states the rule for every other credential on this plane
and these three were the exception: a value passed as `--value` is in your shell
history and in the argument list of a running process, where `ps` shows it to
anybody else on the machine. It is also in the environment if it arrived as
`$STRIPE_SECRET_KEY`, and an environment variable is inherited by every child
process. The value comes from a file that nothing else can read instead, and the
file is written by a prompt rather than by a command somebody typed.

`printf '%s'` rather than `echo`. A webhook signing secret with a trailing
newline is a different string, and every signature computed with it is wrong, so
`POST /webhooks/stripe` answers 401 on every real delivery. `echo` appends a
newline. `read -r` strips the one your return key adds. Both halves are needed.

```sh
VAULT="$(terraform output -raw key_vault_name)"

# One helper, used for each secret below. The value is typed at a prompt, never
# echoed, never in an argument, never in the shell history, and written with no
# trailing byte you did not intend.
afsecret() {
  local name="$1" dir file
  umask 077
  dir="$(mktemp -d)"
  file="$dir/value"
  printf 'Value for %s (input is hidden): ' "$name" >&2
  IFS= read -rs value
  printf '\n' >&2
  printf '%s' "$value" > "$file"
  unset value
  az keyvault secret set --vault-name "$VAULT" --name "$name" --file "$file" --output none
  rm -P "$file" 2> /dev/null || rm -f "$file"
  rmdir "$dir"
}

# Billing. The Team price is the switch and it is NOT a secret: it goes in
# production.tfvars in plain text. There is no Enterprise price and there is not
# meant to be one; Enterprise is arranged with a person.
afsecret stripe-secret-key       # sk_live_... or sk_test_... from Stripe, Developers, API keys
afsecret stripe-webhook-secret   # whsec_..., shown once when you create the endpoint

# Signing in with a link, and inviting somebody who is not in your GitHub
# organization. mail_from is the switch and public_url is then required.
# READ THE DNS SECTION BELOW FIRST: a verified key is not a domain that can send.
afsecret resend-api-key
```

Confirm both arrived without printing either. The first command prints names,
the second a length, which catches a truncated paste or a stray newline:

```sh
az keyvault secret list --vault-name "$VAULT" \
  --query "[?starts_with(name, 'stripe-')].name" -o tsv

for n in stripe-secret-key stripe-webhook-secret; do
  printf '%s ' "$n"
  az keyvault secret show --vault-name "$VAULT" --name "$n" --query value -o tsv \
    | tr -d '\n' | wc -c
done
```

Compare each length against the value Stripe shows you, character for character.
One more than you expect is the trailing newline this section is about, and it
is the difference between a webhook endpoint that works and one that answers 401
to every delivery Stripe ever makes.

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
`mail_from`. Until then leave it empty. What still works:

- **Sign-in is unaffected.** GitHub is the front door and is always offered; the
  mailed link is an additional method, and its route is not registered at all
  when mail is not set up, so there is no button that fails on press.
- **Invitations work by copy and paste.** The link is returned to the inviter and
  shown on screen whether or not mail is configured. A send that fails does not
  fail the invitation either.
- **Enterprise leads are still recorded**, and are read with
  `af-control-plane-backup leads`. `lead_notify_email` is what announces them,
  and the module refuses a plan that sets it without `mail_from`.

Then the switches, in a tfvars file:

```hcl
operator_portal_enabled = true              # generates the operator credential
admin_pool_max          = 4

analytics_enabled      = true               # generates the surrogate secret
analytics_operator_org = "your-org-slug"    # who may read the dashboard
site_origin            = "https://example.com,https://www.example.com"
posthog_region         = "us"                # mounts the PostHog proxy at /ph

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

Every switch here changes the container template, so each one creates a revision
at **zero percent of traffic** ([above](#an-apply-that-changes-the-app-changes-nothing-until-traffic-moves)).
Run a deploy, or move the traffic yourself, and check what is serving:
```sh
az containerapp show -n afcp-app -g af-cp-centralus   --query "properties.template.containers[0].env[].name" -o tsv | sort
```

### Plan with the same inputs you apply with

Every variable the plan job passes must match the apply, or its destroy count is
noise. That is why `TF_VAR_ci_principal_id` comes from a repository variable
rather than being left empty: unset, the count on a role assignment goes to zero
and every pull request reports "1 to destroy" for something nobody proposed to
remove.

The two GitHub OAuth secrets are the exception. Terraform seeds them once and
then carries `ignore_changes` on the value, because it cannot know them and must
not overwrite them. That is what makes the rotation instruction in [the control
plane page](/docs/self-hosting/control-plane) true: without it, the next apply
would quietly put the placeholder back.

`resource_provider_registrations = "none"` is set on the provider, so Terraform
never tries to register a resource provider, because registration is a write at
subscription scope and no identity here holds one. On a subscription where a
provider is not yet registered, apply fails naming the namespace and the fix is
`az provider register --namespace <name>` run by somebody who is allowed to.

Container Apps rather than AKS, deliberately: the control plane is one web
process and a database, and the cheapest always-on AKS control plane is around
75 USD a month before a single node runs. If you want it on Kubernetes anyway,
the [Helm chart](/docs/self-hosting/control-plane) installs on any conformant
cluster.

### After an upgrade that carries new migrations

The bootstrap job is idempotent and applies whatever is outstanding.

```sh
az containerapp job start -n afcp-bootstrap -g af-cp-centralus
```

### Upgrade and rollback, the manual path

`deploy/cd/deploy.sh` already does most of this: migrate first, start the new
revision at zero traffic, check it there, shift traffic, check the public
origin, and shift back on any failure after the shift. Read the script first.

What follows is for the case its own rollback does not fire, because the failure
showed up after the health gate passed and the deploy exited: the gate cannot
catch what has not happened yet, and once it exits nothing is watching.

**1. Find the last revision that was actually good.**

```sh
az containerapp revision list -n afcp-app -g af-cp-centralus \
  --query "[?properties.active].{name:name, created:properties.createdTime, traffic:properties.trafficWeight, fqdn:properties.fqdn}" \
  -o table
```

Old revisions are left active at zero traffic rather than deactivated, so this
list has something to go back to. "The one before this one" is not "the last one
that was good": if two bad releases shipped in a row, the previous revision is
also broken. Cross-reference against the CD run history
(`gh run list --workflow=cd.yml` or the Actions tab) for the last run whose
"What is serving" step summary showed a healthy `/readyz`, and note which commit
it deployed. The revision list above tells you which revision still serves that
commit. If the revision is gone, `deploy.sh`'s promotion step makes you a new
one from the same image, at zero traffic, checked before it takes any.

**2. Move traffic to it.**

```sh
az containerapp ingress traffic set -n afcp-app -g af-cp-centralus \
  --revision-weight <good-revision>=100
```

This is the exact command step 5 of `deploy.sh` runs when its own gate catches
the failure.

**3. Verify it took, the same way the pipeline does.**

`az` can say the weight moved while the origin still answers from a cache or a
stale connection. Run the gate against the public origin:

```sh
deploy/cd/health-gate.sh https://app.antifailure.dev <commit-you-rolled-back-to> 20 3
```

It checks two things: that `/readyz` answers, and that it names the commit you
expect. A healthy answer from the wrong commit is what a plain `curl` would miss.

**4. The migration that already applied.**

`web/packages/db`'s migration runner has no down migration and has never had
one: each file is one transaction, applied and recorded together, so a migration
is either fully applied or not applied at all. That leaves two cases.

**The migration is additive.** `deploy.sh`'s own comment states the constraint:
migrations in this project are expected to be backward compatible with the
previous release. If that holds, step 2 above is the whole fix: the revision you
moved traffic back to runs correctly against the schema as it now stands. Do not
assume it. Read the migration files that shipped with the release you are
rolling back, which
`git diff <good-commit>..<bad-commit> -- web/packages/db/migrations` shows you,
and check each statement is additive rather than something that removes or
narrows what the old code depends on: a dropped or renamed column, a `NOT NULL`
added with no default, a changed type, a revoked grant.

**The migration is not additive.** The old code is then the one that breaks,
because it queries a column, a type, or a grant that no longer matches. Moving
traffic back trades one broken revision for a different one:
- Do not write a rollback migration under incident pressure. It would be run
  once and never tested against the suite every other migration goes through.
- Compare what each side actually does in production now: whether the new code
  errors worse against the changed schema than the old code would, or the other
  way around. Whichever fails less badly stays serving while the real fix is
  written. Say which way you chose and why in the incident record.
- The fix is forward: a new migration that restores what the old code needs, or,
  if the new code is staying, one that finishes what it started, tested through a
  normal pull request and the kind cluster check in `control-plane-image.yml`,
  then deployed the same way any deploy is.
- Afterwards, name the specific miss. Deprecate a column for one release before
  dropping it, so the release that stops writing it and the release that removes
  it are never the same one.

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

`eastus` would be 28.34: `centralus` charges 0.01921 an hour for a B1ms against
0.017, and 0.13 a gigabyte-month for database storage against 0.115.

`--budget N` turns the estimate into a gate that refuses a plan projected above
the resource group's budget. A resource the tool cannot price is reported
`UNKNOWN` and suppresses the total.

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
a diff is a plan people stop reading.

- Creating a flexible server on a delegated subnet makes the platform attach the
  **`Microsoft.Storage` service endpoint** to that subnet for its own backup
  traffic.
- Every managed environment gets a default **`Consumption` workload profile**.

Terraform created neither, so it proposes to delete both and Azure puts them
back. Both are declared in the module for that reason, and the stack plans
`0 to change` against itself. If you fork these modules and see a permanent diff
on a subnet or an environment, declare what the platform set rather than keep
deleting it.

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
certificate; GitHub Actions presents an OIDC token and Azure exchanges it.
Revoking it is deleting a federated credential.

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

If your repository is on the immutable format, Entra answers:

```
AADSTS700213: No matching federated identity record found for presented
assertion subject 'repo:<org>@<orgid>/<repo>@<repoid>:pull_request'
```

**Read the subject out of the failing job's log and create a credential that
matches it exactly.** Keep both forms: an application takes twenty federated
credentials, so a change to the format in either direction does not break the
job:

```sh
gh api repos/<owner>/<repo> --jq '{repo_id:.id, owner_id:.owner.id}'
```

Then set `AZURE_CLIENT_ID`, `AZURE_TENANT_ID` and `AZURE_SUBSCRIPTION_ID` as
repository secrets, plus `AZURE_TFSTATE_RG` and `AZURE_TFSTATE_ACCOUNT` if you
want it to read real state. None of those five is a credential; they are identifiers.

**What the plan job needs**:

| Scope | Role |
| --- | --- |
| the control plane resource group | Reader |
| the state storage account | Storage Blob Data Reader |
| the state storage account | Reader |

The last two look redundant and are not. A role on the storage control plane
grants nothing on the data plane and the reverse also holds: Storage Blob Data
Reader cannot perform `Microsoft.Storage/storageAccounts/read`, which the
`azurerm` backend does before reading any state, to resolve the blob endpoint.
Both roles are read-only.

Nothing at subscription scope. The plan job also passes two flags, and each
one is there so the job does not need a write:

- `-lock=false`. The backend locks with a blob lease and a lease is a write,
  and a pull request can edit the workflow that uses the credential in the
  same commit that runs it.
- `-refresh=false`. Refreshing an `azurerm_key_vault_secret` reads the
  secret's *value*, which would put the live database URLs into a pull
  request job.

**What the deploy job needs on top of that**, the same principal on the hosted
control plane, as `stacks/control-plane/ci.tf` spells out. `cd.yml` deploys with
it and applies each environment's container app configuration from its tfvars
before deploying, through `deploy/cd/apply-config.sh`:

| Scope | Role | For |
| --- | --- | --- |
| each control plane resource group | Contributor | `az containerapp update`, the bootstrap job, the traffic shift |
| the state storage account | Storage Blob Data Contributor | the apply writes the state and takes the lock lease |
| each control plane Key Vault | Key Vault Secrets User | the targeted plan refreshes the app's secret references, and a refresh reads the value |

The refresh is not optional for the apply the way it is for the plan: the
app's image is in `ignore_changes`, so the apply writes back the image the
prior state holds, and only a refreshed state holds the digest `deploy.sh` last
shipped. `ci.tf` and `stacks/tfstate/main.tf` declare these grants; both note
which of them were made by hand before they were declared and how to import
those rather than duplicate them.

This page said for nine days that the identity held none of the three. It held
two of them, made by hand on 2026-08-28, and the state Contributor is why the
plan's `-lock=false` is now a flag rather than a consequence. What still holds:
the plan job writes nothing, the deploy job's steps are the only ones that
apply, and both federated credentials name this repository.

### The job has three modes and always says which one it ran

| Condition | What you get |
| --- | --- |
| no `AZURE_CLIENT_ID` | **skipped**, and it says it checked nothing |
| credential, no state secrets | **planned from an empty state**: real Azure, real cost estimate, and a summary whose first line says it *cannot report a destroy* |
| credential and state secrets | **planned against real state**, the only mode in which "0 to destroy" is evidence |

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

Ask for the family the node pool uses, not the total: a subscription can have
plenty of total cores and none of the family a pool wants, and the error names
which. This matters for an AKS pool; the control plane above needs no VM quota.

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

A Key Vault name is GLOBAL, a soft-deleted vault keeps its name for the
retention period, and purge protection means nobody can release it early. So
`terraform destroy` followed by `terraform apply` in the same region inside
seven days fails on the vault, with an error about a name conflict rather than
about soft delete. The vault name therefore includes the location,
`<name>-kv-<location>`, so that moving regions works. Set `key_vault_name`
yourself if you need to sidestep it knowingly.

Related: [the control plane](/docs/self-hosting/control-plane),
[standing up production](/docs/self-hosting/production),
[the runbooks](/docs/self-hosting/runbooks),
[configuration](/docs/reference/control-plane).

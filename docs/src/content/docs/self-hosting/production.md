---
title: Standing up production
description: What Terraform owns, what it cannot, and the exact order the two have to happen in.
sidebar:
  order: 3
---

The production control plane is one `terraform apply` and eight things a person
has to do in a browser or a shell, and the order matters because several of them
fail if done early.

Read [Azure](/docs/self-hosting/azure/) first. Everything on that page about
policy, regions, the Key Vault name and the revision mode trap applies here and
is not repeated.

## What Terraform owns

`infra/terraform/stacks/control-plane/production.tfvars` is the whole
configuration and every value in it says why it differs from staging. One apply
produces the resource group, a zone redundant Postgres with geo redundant
backups, the Key Vault, the bootstrap and maintenance jobs, the application on
two replicas, the DNS records for `app.antifailure.dev`, the managed
certificate, the custom domain binding, and eleven alert rules with an action
group.

## What Terraform cannot own, and why

**The GitHub App's private key and webhook secret.** GitHub mints the key once
and shows it once. Terraform can neither create it nor recreate it, and a
resource that manages a value it cannot produce is one that will eventually set
it to the empty string. The module reads both from Key Vault with a data source
instead, which is also why setting `github_app_id` before those secrets exist
fails at plan rather than at the first delivery.

**The OAuth App's client secret.** Same reason. Terraform seeds a placeholder
once and then carries `ignore_changes` on the value, so rotating it with `az
keyvault secret set` stays true.

**Role assignments outside this stack's group.** The DNS zone is in `af-web`.
A stack that could grant itself write access to another group's resources would
defeat the point of scoping it.

**The federated credential and the deployment approval rule.** Both are how the
repository proves who it is, and both are deliberately outside anything a pull
request can change.

## The checklist, in this order

### 1. Give production its own Terraform state

**This is the step that can destroy staging, and it is first for that reason.**
The stack directory is shared: `staging.tfvars` and `production.tfvars` sit side
by side and the backend is configured at `init` time. Running `terraform apply
-var-file=production.tfvars` in a directory that was initialised against
staging's state produces a plan that destroys staging and creates production,
and it will look like a very large diff rather than like a mistake.

So production gets its own backend configuration with a different `key`:

```sh
cd infra/terraform/stacks/control-plane
cat > backend.production.hcl <<'EOF'
resource_group_name  = "af-tfstate-eastus"
storage_account_name = "<the state account>"
container_name       = "tfstate"
key                  = "control-plane-production.tfstate"
EOF
terraform init -backend-config=backend.production.hcl -reconfigure
```

`backend.hcl` and `backend.production.hcl` are both ignored by git, because the
storage account name is an identifier this repository does not carry.

**Read the first line of every plan.** A plan against the right state says
roughly forty to add and nothing to destroy. Anything with destroys in it is the
wrong state.

### 2. Check the region, before anything else

```sh
go run ./tools/azguard region centralus
```

It fails closed. A region it cannot get an answer about is refused.

### 3. Grant the deploying identity access to the DNS zone

The records for `app.antifailure.dev` are created in the `antifailure.dev` zone,
which lives in `af-web`. Whoever runs the apply needs to be able to write there.

```sh
az role assignment create \
  --role "DNS Zone Contributor" \
  --assignee-object-id "$(az ad signed-in-user show --query id -o tsv)" \
  --assignee-principal-type User \
  --scope "$(az network dns zone show -g af-web -n antifailure.dev --query id -o tsv)"
```

Subscription Owner already covers this. Run it anyway if the apply is done by a
service principal rather than by a person.

### 4. Decide who gets paged

The addresses are not in this repository and are passed as environment
variables. Enabling alerting with no receiver fails at plan, on purpose.

```sh
export TF_VAR_alert_emails='["you@example.com"]'
export TF_VAR_alert_sms_country_code='1'
export TF_VAR_alert_sms_number='5551234567'
```

### 5. Plan, price it, apply

```sh
terraform plan -var-file=production.tfvars -out=plan.tfplan
terraform show -json plan.tfplan > plan.json
go run ./tools/cost estimate --plan plan.json --pricing infra/pricing.yaml --budget 450
terraform apply plan.tfplan
```

The estimate is **353.04 USD a month**, and 310.10 of it is the database.
`high_availability` forces a General Purpose SKU and then runs two of them. That
is the decision to look at twice before applying, because it cannot be undone
cheaply: high availability can be turned off later, but `geo_redundant_backup`
is fixed when the server is created.

**Expect to run the apply twice.** The Key Vault Secrets Officer grant is
created in the same apply that writes the first secrets, and Azure RBAC takes a
minute or two to propagate. A second apply after the first fails on a secret
write is normal and is not a sign of anything wrong.

Sign-in does not work yet. The OAuth values in the vault are placeholders and
the next three steps replace them.

### 6. Confirm the assumptions the alerts are built on

Two numbers were derived rather than read, and both are quiet if wrong.

```sh
# The connection alert's denominator. Expect 859 for GP_Standard_D2ds_v4.
az postgres flexible-server parameter show \
  -g af-cp-prod-centralus -s afcpprod-pg -n max_connections \
  --query "{value:value,default:defaultValue}" -o json

# The action group actually delivers. This sends a real notification.
az monitor action-group test-notifications create \
  -g af-cp-prod-centralus -n afcpprod-pager \
  --alert-type metric --add-action email alerts "you@example.com"
```

Do the second one. An action group that creates cleanly, attaches to every rule
and delivers nothing looks exactly like one that works.

### 7. Create the production OAuth App

**This is your job, in a browser, at
`https://github.com/settings/developers`.** Production needs its own, not
staging's.

| Field | Value |
| --- | --- |
| Application name | `Antifailure` |
| Homepage URL | `https://app.antifailure.dev` |
| Authorization callback URL | `https://app.antifailure.dev/auth/github/callback` |
| Enable Device Flow | **unticked** |
| Allow wildcard matching | **unticked** |

The callback has to match `github_redirect_uri` in `production.tfvars`
character for character. A mismatch fails with an error GitHub shows the user
and this application never sees. The field takes more than one: GitHub's form
says you may add up to ten redirect URIs, so a second environment does not need
a second OAuth App.

Leave wildcard matching off. The registered callback is exact and nothing needs
it. While you are there, **untick it on the staging OAuth App too**: it is on,
and nothing there needs it either.

Leave Device Flow off, and it is worth knowing what it would be for so that the
default does not survive by accident. GitHub's device flow is for a client with
no browser to redirect: it shows a code, the user types it at
`github.com/login/device`, and the client polls GitHub for a token. `af login`
does look like that, and it is not that: it is this control plane's own device
grant, in `web/apps/api/src/auth/device.ts`, minting `afu_` tokens against
`/auth/device/code` on this server. Nothing here calls `github.com/login/device`
at all. Ticking it adds a way to obtain a GitHub token in this application's
name that nothing in the product would ever use.

Generate a client secret and keep the page open. GitHub shows it once.

### 8. Create the production GitHub App

**Also your job, in a browser, at `https://github.com/settings/apps`.** The
webhook secret and the private key are the credentials that let a delivery write
rows, so sharing staging's App would mean a staging compromise writing into
production's tenants. Installation ids also differ per App, and
`github_installations` keys on them.

| Field | Value |
| --- | --- |
| GitHub App name | `Antifailure` |
| Homepage URL | `https://app.antifailure.dev` |
| Callback URL | leave empty, sign-in uses the OAuth App |
| Webhook | Active |
| Webhook URL | `https://app.antifailure.dev/webhooks/github` |
| Webhook secret | generate a long random string and keep it |
| Where can this be installed | Any account |

Repository permissions, and what each one is actually for:

| Permission | Access | What uses it |
| --- | --- | --- |
| Metadata | Read-only | Mandatory for every App. |
| Contents | Read-only | Reading the manifest and the workflow file. |
| Pull requests | Read and write | The report comment, and the pull request a masking rule change becomes. |
| Actions | Read and write | The console's **Create environment**, **Run agents** and **Run load**. |

Organization permissions:

| Permission | Access | What uses it |
| --- | --- | --- |
| Members | Read-only | Membership sync, which is what stops everybody landing with no tenant. |

**Grant Actions write at creation even if the console's controls are not in
use yet.** It is the one on this list where waiting is worse than granting:
widening an existing App's permissions makes GitHub ask every installation to
accept the new grant, so adding it later interrupts every customer. Every one of
those controls, including starting a workload and tearing an environment down,
dispatches a `workflow_dispatch` run of the customer's own workflow
through `dispatchWorkflow` in `web/apps/api/src/auth/github.ts`, and without the
permission GitHub answers 404, which is the same answer it gives for a missing
workflow file.

**Checks is not on this list and should not be granted.** Nothing in this
repository calls the Checks API. The only two files that speak to GitHub are
`web/apps/api/src/auth/github.ts` and `web/apps/api/src/github/app.ts`, and
neither creates a check run. A permission granted for a feature that does not
exist is one nobody will remove later, because nobody will be able to tell
whether something depends on it.

Subscribe to events: **Installation**, **Installation repositories**,
**Repository**.

That is the whole list `web/apps/api/src/github/webhook.ts` acts on. **Pull
request** and **Push** are deliberately absent: nothing handles them, and an
event nobody consumes is delivery-log noise that makes a real failed delivery
harder to find. **Member** and **Membership** are absent for a sharper reason:
the handler names them and answers `handled: false`, because membership is
resolved at sign-in and reconciled by **Sync from GitHub** on the Members page.
Subscribing to them looks like membership is event driven and it is not.
Adding any of these later is an edit to the App's settings rather than a
reinstall, so there is no cost to leaving them off now.

Then, on the App's page, **Generate a private key**. GitHub downloads a `.pem`
and never shows it again. Note the numeric **App ID** at the top of the page.

### 9. Put the four values in Key Vault

Three of these replace placeholders Terraform seeded; two are ones Terraform
deliberately does not own.

```sh
VAULT=afcpprod-kv-centralus

az keyvault secret set --vault-name "$VAULT" --name github-client-id     --value '<oauth client id>'
az keyvault secret set --vault-name "$VAULT" --name github-client-secret --value '<oauth client secret>'
az keyvault secret set --vault-name "$VAULT" --name github-app-webhook-secret --value '<webhook secret>'
az keyvault secret set --vault-name "$VAULT" --name github-app-private-key --file ~/Downloads/<app>.private-key.pem
```

The private key goes in as a file. A PEM pasted through a shell loses its
newlines, and the application fails to sign a JWT with an error about the key
format rather than about how it was pasted.

### 10. Tell Terraform the App exists, and apply again

Set `github_app_id` in `production.tfvars` to the numeric id from step 8, then
plan and apply. The plan reads the two secrets you just wrote, and fails if
either is missing, which is the check working.

**Then read what is actually serving.** This is the trap that has caught this
project three times. Terraform owns the container app template and continuous
deployment owns the traffic, so an apply that adds an environment variable
creates a **new revision at zero percent** and reports success while production
keeps serving the old one without the change.

```sh
az containerapp ingress traffic show -n afcpprod-app -g af-cp-prod-centralus -o table
az containerapp revision list -n afcpprod-app -g af-cp-prod-centralus \
  --query "[?properties.active].{rev:name,created:properties.createdTime}" -o table
```

If the newest revision is not the one with the weight, move it:

```sh
az containerapp ingress traffic set -n afcpprod-app -g af-cp-prod-centralus \
  --revision-weight <newest-revision>=100
```

### 11. Install the App on the organization

On the App's page, **Install App**, and choose the account and repositories.
Nothing has a tenant until an installation exists: this is why everybody who
signed in during the first week landed with no organization.

### 12. Let continuous deployment reach production

**The federated credential already exists. Do not create it.** Checked rather
than assumed: `af-infra-ci` carries eight, including
`github-env-production` and `github-env-production-immutable`, which are the two
spellings of `repo:<owner>/<repo>:environment:production`. Both are registered
because GitHub has moved to immutable OIDC subjects carrying numeric
organisation and repository ids, and an application takes twenty credentials, so
keeping both means a change in either direction does not break the job.

Confirm rather than trust this page:

```sh
APP_ID=$(az ad app list --display-name af-infra-ci --query "[0].id" -o tsv)
az ad app federated-credential list --id "$APP_ID" \
  --query "[?contains(subject,'environment:production')].{name:name,subject:subject}" -o table
```

**What is missing is the role assignment**, because the production group does
not exist until step 5 and a grant cannot precede its scope. The identity needs
on the production group what it already has on staging's:

```sh
az role assignment create --role Contributor \
  --assignee "$(az ad app list --display-name af-infra-ci --query '[0].appId' -o tsv)" \
  --scope "$(az group show -n af-cp-prod-centralus --query id -o tsv)"
```

### 13. Set the approval rule on the production environment

In repository settings, Environments, `production`: add required reviewers. The
`cd.yml` job does not start until somebody clicks it, and the reviewer list
lives there rather than in an `if:` a pull request can edit in the same commit
that deploys.

### 14. Release

Push a `v*` tag. Continuous deployment builds once, deploys to staging, waits
for the approval, and then promotes **the same image digest** staging tested.
The production job asks Azure whether the app exists before doing anything, so
it refuses cleanly if any of the above was skipped.

## After the first release

- Watch the availability alert clear rather than assuming it did. It is severity
  0 and it fires on two failed probe locations.
- Run the backup drill and write down the number it prints. That number is your
  recovery time objective and nothing else is. The
  [operations page](/docs/self-hosting/operations/) has the command.
- The [runbooks](/docs/self-hosting/runbooks/) are the pages the alerts link to.
  Read the index once now, while nothing is broken.

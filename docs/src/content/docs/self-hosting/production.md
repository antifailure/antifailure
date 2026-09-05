---
title: Standing up production
description: What Terraform owns, what it cannot, and the exact order the two have to happen in.
sidebar:
  order: 3
---

The production control plane is one `terraform apply` and nine things a person
has to do in a browser or a shell, and the order matters because several of them
fail if done early.

Read [Azure](/docs/self-hosting/azure) first. Everything on that page about
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

**The managed certificate's binding to the custom domain.** Not a policy
decision, a circular one. Azure refuses to issue a managed certificate for a
hostname that is not already bound to an app in the environment, and refuses
`RequireCustomHostnameInEnvironment` if you ask the other way round, so the
binding cannot name a certificate that cannot exist until the binding does.
Terraform adds the hostname with no certificate, Terraform creates the
certificate, and one `az containerapp hostname bind` closes the loop. The
`ignore_changes` on the custom domain is what stops the next apply undoing it.
Step 6 below is that command.

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
use_azuread_auth     = true
EOF
terraform init -backend-config=backend.production.hcl -reconfigure
```

`backend.hcl` and `backend.production.hcl` are both ignored by git, because the
storage account name is an identifier this repository does not carry.

**Read the first line of every plan, and then read its exit status.** A plan
against the right state adds roughly forty resources and destroys nothing, and
anything with destroys in it is the wrong state.

The exit status is the separate check, and it is the one that has caught things
here. This stack has twice produced a plan that printed in full, ended with its
own `0 to destroy` summary, and then exited non-zero. Once for an output that
carried a provider-sensitive value without declaring itself sensitive, which
Terraform refuses while evaluating outputs and therefore after the whole diff
has been printed. Once for the managed certificate's
`RequireCustomHostnameInEnvironment`. Both look exactly like a plan that worked,
and the only thing that tells them apart from one is `echo $?`.

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

**The apply may need running twice.** The Key Vault Secrets Officer grant is
created in the same apply that writes the first secrets, and Azure RBAC takes a
minute or two to propagate, so a second apply after the first fails on a secret
write is normal and is not a sign of anything wrong. It did not happen on the
first real run of this stack, and it is still the likeliest reason you see one.

Whatever the cause, a partly finished apply is not a mess to clean up by hand.
Terraform records every resource that succeeded, and running `plan` again asks
for exactly the remainder. Read that plan the same way as the first: it should
add what is missing and destroy nothing.

Sign-in does not work yet. The OAuth values in the vault are placeholders and
the next three steps replace them.

### 6. Bind the certificate

Terraform has added the hostname and created the certificate. Until something
attaches one to the other, the name resolves and the TLS handshake is reset by
the peer with no certificate offered at all.

**Whether anything has to be that something is currently an open question, so
this step checks first and fixes second.** Under the older `domain.tf` the bind
below was a person's job, and the one recorded stand-up of this stack is the
evidence: `afcpprod-unreachable` held Sev0 for ninety five minutes with the
certificate issued and nothing serving it. `domain.tf` has since been rewritten
so that the hostname is bound with no certificate and Azure attaches one itself
when it issues, asynchronously and outside any apply. If that holds, the command
below is a no-op.

Nobody knows yet, and the honest reason is that nobody has applied the new
configuration. It reasons from the provider's documented behaviour rather than
from an observed apply, which is a good basis for a configuration change and a
poor one for deleting a step whose absence is an outage.

So prove it from outside first, because this is the step whose failure looks
like a network problem:

```sh
curl -sS -o /dev/null -w 'http=%{http_code} sslverify=%{ssl_verify_result}\n' \
  https://app.antifailure.dev/health
```

`sslverify=0` is a certificate the client trusts, and if you see it here then
Azure bound the certificate without you. That is the thing this page cannot yet
tell you, so say so, and the next person to stand production up can delete the
command below with evidence rather than with an argument.

A connection reset means the binding did not take, and this is the remedy:

```sh
CERT_ID=$(az containerapp env certificate list \
  -n afcpprod-env -g af-cp-prod-centralus \
  --query "[?properties.subjectName=='app.antifailure.dev'].id | [0]" -o tsv)

az containerapp hostname bind -n afcpprod-app -g af-cp-prod-centralus \
  --hostname app.antifailure.dev --environment afcpprod-env \
  --certificate "$CERT_ID" --validation-method CNAME
```

`terraform plan` stays clean afterwards. The custom domain resource carries
`ignore_changes` on the two fields this command writes, which is the provider's
documented handling for an Azure managed certificate.

### 7. Confirm the assumptions the alerts are built on

Two numbers were derived rather than read, and both are quiet if wrong.

```sh
# The connection alert's denominator. Expect 859 for GP_Standard_D2ds_v4.
az postgres flexible-server parameter show \
  -g af-cp-prod-centralus -s afcpprod-pg -n max_connections \
  --query "{value:value,default:defaultValue}" -o json

# The action group actually delivers. This sends a real notification.
az monitor action-group test-notifications create \
  --action-group afcpprod-pager -g af-cp-prod-centralus \
  --alert-type metricstaticthreshold \
  -a email email-0 "you@example.com" usecommonalertschema
```

Do the second one. An action group that creates cleanly, attaches to every rule
and delivers nothing looks exactly like one that works. A `Status` of
`Succeeded` in the result is the proof; anything else is a page that will not
arrive.

THE RECEIVER NAME IS NOT FREE TEXT and neither is the alert type. Azure matches
`email-0` against the receivers the action group already has and refuses
`ActionOrReceiverNotExistedInActionGroup` for a name it does not hold, so it has
to be the name the alerting module generates rather than a label of your own.
`--alert-type metric` is rejected as invalid; the accepted value is
`metricstaticthreshold`.

### 8. Create the production OAuth App

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

### 9. Create the production GitHub App

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
| Pull requests | Read and write | The one comment per pull request, and the pull request a masking rule change becomes. |
| Actions | Read and write | The console's **Create environment**, **Run agents**, **Run load** and **Tear down**, and cancelling the run that holds an environment when a pull request closes. |
| Checks | Read and write | The one check run per commit that a branch protection rule can require. |

Organization permissions:

| Permission | Access | What uses it |
| --- | --- | --- |
| Members | Read-only | Membership sync, which is what stops everybody landing with no tenant. |

**Grant Actions write at creation even if the console's controls are not in
use yet.** It is the one on this list where waiting is worse than granting:
widening an existing App's permissions makes GitHub ask every installation to
accept the new grant, so adding it later interrupts every customer, and until
somebody accepts, the App declares a permission that no installation holds.
Every one of those controls, including starting a workload and tearing an
environment down, dispatches a `workflow_dispatch` run of the customer's own
workflow through `dispatchWorkflow` in `web/apps/api/src/auth/github.ts`, and
without the permission GitHub refuses with
`403 Resource not accessible by integration`.

**Checks used to say "do not grant this" here, and that was right at the time:
nothing called the Checks API.** Something does now. Without it, a pull request
gets the comment and no check run, so no branch protection rule can require
Antifailure, and the control plane says which grant is missing in the comment
rather than failing quietly.

Subscribe to events: **Installation**, **Installation repositories**,
**Repository**, **Pull request**, **Workflow run**, **Check run**, **Check
suite**.

The last five are the pull request lifecycle. **Pull request** is what opens a
check on a commit and closes it when the pull request does. **Workflow run**
binds the check to the Actions run, which is the only route this control plane
has into the machine holding the environment. **Check run** and **Check suite**
are the two Re-run buttons: GitHub sends the first when somebody re-runs one
check and the second when they re-run all of them from the checks page, so
subscribing to only one leaves the other doing nothing at all. Each is handled
in `web/apps/api/src/github/lifecycle.ts`.

**Push** is still deliberately absent: nothing handles it, and an event nobody
consumes is delivery-log noise that makes a real failed delivery harder to find.
**Member** and **Membership** are absent for a sharper reason: the handler names
them and answers `handled: false`, because membership is resolved at sign-in and
reconciled by **Sync from GitHub** on the Members page. Subscribing to them
looks like membership is event driven and it is not.

### Adding either of these to an App that already exists

Widening an App's permissions **does not grant them**. GitHub raises a request
against every existing installation and nothing changes until a person accepts
it, so the App's settings page can read `Checks: Read and write` while every
installation still holds none of it. That is not a hypothetical: it cost most of
an hour on `Actions: write`, where a 403 was read as a code problem for as long
as it took somebody to look at the installation rather than at the App.

1. The App's settings, **Permissions and events**, Repository permissions,
   **Checks** to Read and write, then **Save**.
2. The same page, **Subscribe to events**, tick **Pull request**, **Workflow
   run** and **Check run**, then **Save**. Event subscriptions take effect
   without anybody accepting anything; only the permission needs step 3.
3. For every account the App is installed on: its **Installed GitHub Apps**
   settings, the App, **Review request**, **Accept new permissions**.

An installation token minted before step 3 is cached for an hour and carries
none of the new grant, so a permission accepted at 00:38 can still be refused at
01:30, and the refusal looks exactly like the permission never having been
granted. Restarting the control plane clears it, because those tokens live only
in memory and nothing writes them anywhere.

Then, on the App's page, **Generate a private key**. GitHub downloads a `.pem`
and never shows it again. Note the numeric **App ID** at the top of the page.

### 10. Put the four values in Key Vault

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

### 11. Tell Terraform the App exists, and apply again

Set `github_app_id` in `production.tfvars` to the numeric id from step 9, then
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

Read that from Azure and not from Terraform. The stored state file records the
traffic weight from before the last deploy and `ignore_changes` deliberately
keeps it there, so it is stale by design and says nothing about what is serving.
An empty plan is not an answer either, because the attribute that would say so
is the ignored one. See
[the revision mode trap](/docs/self-hosting/azure#terraform-state-is-not-a-record-of-what-is-serving).

### 12. Install the App on the organization

On the App's page, **Install App**, and choose the account and repositories.
Nothing has a tenant until an installation exists: this is why everybody who
signed in during the first week landed with no organization.

**Installing is not the same as being installed, and the difference is a webhook
this control plane may have refused.** Installing sends one `installation`
delivery, once. GitHub does not retry a webhook. So if the App was installed
before step 11, which is the order the App's own setup page encourages, because
Install App is on the page you are already looking at, then the delivery arrived
at a control plane whose `AF_GITHUB_APP_WEBHOOK_SECRET` was unset, was answered
**503**, and is gone. `github_installations` stays empty, every sign-in
lands with no organization, and nothing anywhere says why.

So check it. The App's **Advanced** tab lists every delivery with the status
code this control plane returned, and each row has a **Redeliver** button. Use
that tab: `gh api /app/hook/deliveries` does **not** work here, because the
deliveries endpoint authenticates as the App and `gh` holds a user token. The
API route needs a JWT signed with the App's private key, which is the same key
you put in the vault in step 10.

If the `installation` row is not 200, redeliver it. The response body is the
check that matters, and a successful one names the installation:

```
{"event":"installation","action":"created","handled":true,
 "detail":"installation 157834739 for antifailure, 1 repositories"}
```

One trap if you script this instead. Delivery ids are past the range a double
holds exactly, 3839993231035072512 being a real one, so a JSON parser backed by
doubles rounds the last digits and JavaScript's `JSON.parse` turns that id into
...072500. A redelivery aimed at the rounded id is a 404 on a delivery
that never existed, and it reads as "GitHub lost it" rather than as an
arithmetic bug. Take the id out of the raw body as text.

### 13. Let continuous deployment reach production

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
on the production group what it already has on staging's: Contributor, scoped to
that group and nothing wider.

**Terraform owns it. Do not create it by hand.** This page used to print an
`az role assignment create` here and that instruction outlived the code that
replaced it, which is worse than either alone: a grant made by hand is absent
from the stack's state, cannot survive a rebuild, and reads to the next person
as a resource Terraform does not manage. The grant is
`azurerm_role_assignment.cd_deploys_the_group` in
`stacks/control-plane/ci.tf`, and it is switched on by `cd_principal_id` in
`production.tfvars`, which is already set. Step 5 creates it along with
everything else.

Confirm it after the apply, rather than trusting this page:

```sh
az role assignment list \
  --assignee "$(az ad app list --display-name af-infra-ci --query '[0].appId' -o tsv)" \
  --scope "$(az group show -n af-cp-prod-centralus --query id -o tsv)" \
  --query "[].roleDefinitionName" -o tsv
```

If that prints nothing, `cd.yml`'s production job fails at its first
`az containerapp` call and continuous deployment cannot reach production at all.

### 14. Set the approval rule on the production environment

In repository settings, Environments, `production`: add required reviewers. The
`cd.yml` job does not start until somebody clicks it, and the reviewer list
lives there rather than in an `if:` a pull request can edit in the same commit
that deploys.

### 15. Release

Push a `v*` tag. Continuous deployment builds once, deploys to staging, waits
for the approval, and then promotes **the same image digest** staging tested.
The production job asks Azure whether the app exists before doing anything, so
it refuses cleanly if any of the above was skipped.

## After the first release

- Watch the availability alert clear rather than assuming it did. It is severity
  0 and it fires on two failed probe locations.
- Run the backup drill and write down the number it prints. That number is your
  recovery time objective and nothing else is. The
  [operations page](/docs/self-hosting/operations) has the command.
- The [runbooks](/docs/self-hosting/runbooks) are the pages the alerts link to.
  Read the index once now, while nothing is broken.

## Turning billing on

Billing is off on a control plane that has never been told about Stripe, and off
is a supported state rather than a half-finished one: a self-hosted installation
takes no money, and every route that would charge answers `PRECONDITION_FAILED`
naming the settings it needs. What follows turns it on, in the only order that
works. The four sections below are deliberately not numbered, because this is
not step sixteen of first setup: it is a separate procedure somebody runs later,
possibly years later, on a control plane that is already serving. Run them in
the order they are written.

**Three settings, and two of them are credentials.** `web/apps/api/src/billing/plans.ts`
requires exactly these:

| Setting | Secret | Where it comes from |
| --- | --- | --- |
| `AF_STRIPE_SECRET_KEY` | yes, Key Vault | Stripe, Developers, API keys |
| `AF_STRIPE_WEBHOOK_SECRET` | yes, Key Vault | shown once, when you create the webhook endpoint |
| `AF_STRIPE_PRICE_TEAM` | no | `stripe_price_team` in `production.tfvars` |

**Two of three is worse than none.** A partial configuration is reported as a
refusal, not as a partial success: the process prints `billing is OFF and
partially configured` with the missing names in it and takes no money at all.
That is deliberate, because the setting people forget is the webhook secret, and
an installation missing only that one appears to work right up until the first
customer pays and never gets what they bought.

**There is no `AF_STRIPE_PRICE_ENTERPRISE` and there is not meant to be one.**
Enterprise is agreed with a person, so no Stripe price exists behind it. Checkout
refuses that plan by name and points at the contact route. A plan with no price
is a plan that is not sold here, not a misconfiguration.

### First, create the webhook endpoint at Stripe

**Your job, in a browser, at `https://dashboard.stripe.com/webhooks`.** This step
is first because `AF_STRIPE_WEBHOOK_SECRET` does not exist until you do it:
Stripe generates the signing secret when the endpoint is created and shows it
once.

| Field | Value |
| --- | --- |
| Endpoint URL | `https://app.antifailure.dev/webhooks/stripe` |
| Listen to | Events on your account |
| API version | your account default |

Select exactly these nine events, which are the ones `HANDLED_EVENTS` in
`web/apps/api/src/billing/webhook.ts` acts on:

`customer.subscription.created`, `customer.subscription.updated`,
`customer.subscription.deleted`, `invoice.paid`, `invoice.payment_failed`,
`invoice.finalized`, `payment_method.attached`, `payment_method.detached`,
`checkout.session.completed`.

Subscribing to more is harmless and subscribing to fewer is not. An event this
control plane does not act on is acknowledged and not recorded, so a wider
selection costs a 200 and nothing else. A narrower one loses an entitlement.

**Do this in test mode first, against a control plane you can afford to be wrong
about.** Test mode has its own endpoint, its own signing secret, its own keys and
its own prices, and nothing crosses between the two.

### Then put the two credentials in Key Vault

The vault name is `afcpprod-kv-centralus` for production. Use the `afsecret`
helper on the [Azure page](/docs/self-hosting/azure), which takes the value at a
prompt rather than as an argument, writes it with no trailing newline, and
removes the file afterwards. A signing secret with a trailing newline fails every
signature and the endpoint answers 401 to every delivery Stripe makes, while the
plan, the deploy and the dashboard all look correct.

Confirm both are there before going on. This prints names, never values:

```sh
az keyvault secret list --vault-name afcpprod-kv-centralus \
  --query "[?starts_with(name, 'stripe-')].name" -o tsv
```

Two names, or stop here.

### Then set the price, and only then apply

`stripe_price_team` in `production.tfvars` is the switch. Setting it makes the
container app reference both vault secrets by their versionless ids.

**The plan cannot tell you the secrets are missing, and this is the one place
that matters.** `keyvault.tf` addresses them by constructed id rather than
reading them, because the identity that plans production holds nothing on the
vault and the only way to give it a read is to grant a pull request identity
access to production's credentials. So a plan is green whether or not the
secrets exist, Azure discovers a missing one while resolving references during
deployment, and the revision fails to start on a control plane that was serving
a moment earlier. Putting the credentials in the vault is not optional and it is not
reorderable.

Apply, then shift traffic the way every other change to this app is shifted:
the app runs in `Multiple` revision mode, so the apply creates a revision at zero
traffic. Probe it at zero, then shift.

### Then prove it, on the running control plane

**A route that answers 200 is not proof that a plan changed.** Four checks, in
order, each of which can only pass if the one before it did.

The endpoint stops refusing. Before, this is a 503 saying this control plane is
not configured to take payments; after, it is a 401, because the request is now
being checked against a signing secret rather than turned away:

```sh
curl -sS -X POST https://app.antifailure.dev/webhooks/stripe \
  -H 'content-type: application/json' --data '{}' -w '\n%{http_code}\n'
```

A 503 here means one of the three settings did not arrive. A 401 means all three
did, and that the process is verifying signatures.

Then **Send test webhook** from the endpoint's page in the Stripe dashboard. A
200 proves the signing secret is byte for byte the one Stripe holds, which is the
half a 401 above cannot distinguish from a wrong secret.

Then buy something in test mode, with Stripe's `4242 4242 4242 4242` card, and
watch the organization's plan change. Not the checkout page opening: the plan.

Then ask the product for the thing the plan was withholding. Create an
environment that the free plan's limit of three refused before the purchase. That
is the only check that cannot be satisfied by a payment path that is connected to
nothing.

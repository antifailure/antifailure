# Production: app.antifailure.dev
#
# Read staging.tfvars first. This file only explains what is DIFFERENT and why,
# and every difference here is a decision that costs money, so each one says
# what it buys.
#
# THE HEADLINE: this stack projects at 353.04 USD a month against staging's
# 30.47, measured rather than guessed by the command below. Almost all of the difference is one line, high_availability, which
# forces a General Purpose SKU and then runs two of it. Read the estimate before
# applying:
#
#   terraform plan -var-file=production.tfvars -out=plan.tfplan
#   terraform show -json plan.tfplan > plan.json
#   go run ./tools/cost estimate --plan plan.json --pricing infra/pricing.yaml \
#     --budget 450
#
# The four values that are secret or personal are NOT here and are passed as
# TF_VAR_ environment variables: subscription_id, github_client_id,
# github_client_secret, and the alerting contacts (alert_emails,
# alert_sms_country_code, alert_sms_number). An address in this file would reach
# a public pull request plan summary through a diff.

# A SEPARATE RESOURCE GROUP, WHICH IS THE POINT.
#
# Sharing staging's group would save nothing and would put a blast radius that
# includes customers' control plane around every experiment. `terraform destroy`
# aimed at staging, an azguard cleanup scoped to a tag, a policy assignment
# applied to the group: all of them would reach production.
resource_group_name = "af-cp-prod-centralus"

# Same region, and not by preference. centralus is the only one that satisfies
# all three gates on this subscription, and staging.tfvars and the location
# variable's own description explain each of them. A different region for
# production would need `azguard region` run against it first, and would find
# that eastus still cannot create a flexible server.
location = "centralus"

# A DIFFERENT PREFIX, AND IT IS NOT COSMETIC. Key Vault names are GLOBAL. The
# module derives `<name>-kv-<location>`, so `afcp` here would resolve to
# afcp-kv-centralus, which is staging's live vault, and the plan would propose
# to take it over.
name = "afcpprod"

# The public origin. All three of these have to agree exactly, and with the
# production OAuth App's registered callback, or sign-in fails with a
# redirect_uri mismatch that GitHub reports and this application never sees.
app_base_url        = "https://app.antifailure.dev"
github_redirect_uri = "https://app.antifailure.dev/auth/github/callback"
custom_domain       = "app.antifailure.dev"

# The zone is in af-web with the marketing site, NOT in this stack's group. The
# identity that applies this needs DNS Zone Contributor there, which this stack
# deliberately does not grant itself.
dns_zone_name           = "antifailure.dev"
dns_zone_resource_group = "af-web"

# ---------------------------------------------------------------------------
# The database, which is where the money goes.
# ---------------------------------------------------------------------------

# A second server in a second availability zone, synchronously replicated, that
# takes over without a restore. It is the difference between a zone failure
# costing minutes and costing however long a restore takes.
#
# It is also NOT a backup and must not be read as one. The standby has the same
# rows, so a bad migration or a DROP TABLE reaches both instantly. The thing
# that protects against that is point in time recovery below.
high_availability = true

# FORCED BY THE LINE ABOVE. The module refuses zone-redundant HA on a burstable
# SKU with a readable error, because Azure refuses it too, twenty minutes into
# an apply. GP_Standard_D2ds_v4 is the only General Purpose SKU this
# subscription's bonfire-sku-allowlist permits.
#
# 0.2010 USD an hour, so 146.73 a month, and HA runs two of them: 293.46. That
# single number is 83 percent of this stack's bill.
database_sku = "GP_Standard_D2ds_v4"

# 64 GB rather than staging's 32, and IOPS is the reason rather than capacity.
# On a flexible server the IOPS ceiling scales with provisioned storage, and the
# 32 GB tier is the floor. Storage can be grown later and can NEVER be shrunk,
# so this is the one number here that is cheap to get slightly wrong upward and
# permanent to get wrong downward.
database_storage_mb = 65536

# Backups leave the region.
#
# Without this, a region losing its storage loses the database and its backups
# together, and the only remaining copy of every customer's organization,
# policy and audit chain is whatever somebody has on a laptop.
#
# IT CANNOT BE TURNED ON LATER. Azure fixes geo-redundancy at create time. The
# cost of changing this decision after the fact is a dump, a new server, and the
# downtime in between.
geo_redundant_backup = true

# THE RPO, WRITTEN DOWN, WHICH IS THE POINT OF THIS BLOCK.
#
# 35 days is the RECOVERY WINDOW: how far back a restore point may be chosen.
# It is not the RPO and the two are constantly confused.
#
#   RPO, same region: 5 minutes. Azure backs up the write ahead log every five
#   minutes, so a point in time restore loses at most the last five minutes of
#   writes.
#   RPO, geo-restore into the paired region: up to 1 hour, because geo-redundant
#   backup storage replicates asynchronously.
#   RTO: unknown until the drill measures it on the hardware you would actually
#   recover onto. web/apps/api/src/backup.ts prints the number on every run and
#   the operations guide says to use yours rather than anybody else's.
#
# 35 rather than the 14 Azure defaults to here, because the failure this window
# exists for is not a disk dying: it is a defect that corrupts data quietly and
# is noticed weeks later. Fourteen days is inside the time it takes to notice
# one. The cost is nothing at this size: backup storage up to the provisioned
# 64 GB is free and this database is a few hundred megabytes.
backup_retention_days = 35

# ---------------------------------------------------------------------------
# The application.
# ---------------------------------------------------------------------------

# TWO REPLICAS, SO A REVISION RESTART IS NOT AN OUTAGE.
#
# Staging runs one with a written reason: a post-deploy health gate measuring a
# cold start is meaningless. That reasoning does not survive contact with paying
# customers. With one replica, every liveness restart, every node the platform
# moves the app off, and every deploy is a gap in service. With two, the ingress
# has somewhere to send the request.
#
# Two is also what makes the replicas-below-minimum alert able to say anything:
# on a single replica app that alert and the availability probe are the same
# event.
min_replicas = 2
max_replicas = 6

# Ten, stated rather than inherited, because the number only means anything
# next to the server it runs against.
#
#   D2ds_v4 answers max_connections = 859, less 5 reserved and 10 superuser
#   reserved, so an ordinary role gets 844.
#   (max_replicas 6 + one rollback revision at min_replicas 2) x 10 = 80,
#   plus 4 for the jobs and break-glass = 84 against 844.
#
# Staging runs 5 against the same pipeline because its B1ms hands out 35. The
# defect that took staging down is present here too and has simply not been
# reached: deploy/cd/deploy.sh used to leave every superseded revision active,
# and each one keeps min_replicas running. At two replicas a deploy, this
# server absorbs roughly forty deploys' worth of abandoned revisions before it
# runs out. deploy.sh now reaps them and checks the measured arithmetic.
pool_max = 10

# Two years of events rather than one. The events table is partitioned by month
# and dropping a partition is not reversible, so this is the number that decides
# how far back an incident review or a customer's question can reach. A year is
# short for a service whose audit chain is part of what it sells.
event_retention_months = 24

# Ninety days of diagnostics rather than thirty. The questions asked of Log
# Analytics during an incident are asked within days; the ones asked of it after
# a security report are asked months later, and thirty days answers those with
# silence. Two gigabytes a month at 0.12 a gigabyte-month is 0.24.
log_retention_days = 90

# Above the 353 projection with room for a bad month, and low enough that a
# runaway is caught in days rather than on an invoice. The budget notifies at
# 50, 80 and 100 percent on both actual and forecast spend.
monthly_budget_usd = 450

# ---------------------------------------------------------------------------
# Identity and access.
# ---------------------------------------------------------------------------

# OPEN. Anybody with a GitHub account may sign in.
#
# Null, and null is not the same value as the empty list: an empty list renders
# AF_SIGNIN_ALLOWLIST="" and the application reads that as "set, and names
# nobody", which closes the plane to everyone. See the module's app.tf, which is
# careful about exactly this. Terraform will not let a plan be produced without
# a value here at all, so opening the door is still a decision somebody wrote
# down rather than one they forgot.
#
# It named two people until this change, alongside a waitlist form that stored
# an address on a host with no way to mail anybody back. That was a closed
# product with a queue nobody could be taken off. What replaces it is a sign-up
# anybody can complete, defended by the things that make an open door safe
# rather than by a list: GitHub has to report the address as verified before a
# user row is written, every sign-in endpoint is rate limited by address in
# src/limits.ts, and a refusal says the same sentence whoever asks, so the form
# cannot be used to find out who has an account.
#
# Closing it again is one line: a list of logins here and an apply.
signin_allowlist = null

# Everybody who signs in gets their own organization on the free plan, owned by
# them, whose quotas and cost caps are enforced against it. Without this a new
# person authenticates and lands in nothing, which is the state this deployment
# was in for every visitor who was not one of the two names above.
self_serve_signup = true

# Where a refused account is sent. Never rendered while the allowlist is null,
# because nobody is refused; set anyway so that closing signups later is one
# decision rather than two. The contact page reaches a person, which is what
# somebody turned away actually needs.
signup_url = "https://antifailure.dev/contact"

# SET ONLY AFTER THE PRODUCTION GITHUB APP EXISTS. SETTING IT EARLY FAILS.
#
# Production needs its OWN App, not staging's: the webhook secret and the
# private key are the credentials that let a delivery write rows, so sharing
# them means a staging compromise writes into production's tenants. Installation
# ids differ per App and github_installations keys on them.
#
# GitHub mints the private key once and shows it once, so Terraform can neither
# create the App's two secrets nor recreate them. A person puts both in the
# vault and the module addresses them by id.
#
# SETTING THIS BEFORE THOSE SECRETS EXIST NOW FAILS AT APPLY RATHER THAN AT
# PLAN. That is a deliberate trade, and on THIS plane it costs nothing, because
# the check it replaces has never once run here.
#
# The module used to read both secrets with a `data "azurerm_key_vault_secret"`,
# which asserted they existed. Asserting existence requires READING, and the
# identity that runs the production plan holds Contributor on the resource group
# and nothing at all on this vault. So the read failed on PERMISSION before it
# could ever report on EXISTENCE. What was given up on production is an
# intention this deployment's own permission model has never permitted, not a
# check that worked.
#
# DO NOT RESTORE THE DATA SOURCE TO GET THE CHECK BACK. It does not come back.
# The wall is the grant, and the grant has no per secret scope: it would open
# every secret in this vault, this one included, to an identity that a pull
# request can reach and whose workflow a pull request can edit in the same
# commit. keyvault.tf carries the whole argument.
#
# STAGING IS THE HONEST COST, and it is worth stating because it is easy to
# assume otherwise. staging.tfvars sets github_app_id too, both environments
# share this module, and staging's plan identity CAN read staging's vault. So
# staging did have a working plan time existence check and this removes it. A
# missing secret there now surfaces at apply with Azure naming the secret, which
# is a later moment than plan and not a dangerous one, on the environment where
# somebody is experimenting anyway.
#
# The checklist in the production guide still has the steps in the order that
# works.
#
# App 4775259, slug `antifailure`, installed on the antifailure organization as
# installation 157834739. The OAuth App that signs people in is a separate
# registration and its client id and secret are the seeded vault entries, not
# this value: an App id is not a credential and unlocks nothing, which is why it
# sits here rather than arriving as a TF_VAR_.
#
# The App this names is CONFIGURED AND SERVING. Read off the running container
# rather than remembered: afcpprod-app carries AF_GITHUB_APP_ID=4775259 and both
# vault secrets exist. Emptying this line does not remove the App; it removes
# all three environment variables from the container app on the next apply, and
# with them the installation webhook, which is the only path by which an
# organization comes into being. That failure presents as a customer signing in
# to an empty screen, so it reads as a product bug rather than a configuration
# one. See ci.tf for the commit that emptied it and for what a plan gate can and
# cannot catch.
github_app_id = "4775259"

# One identity applies this stack, and it is the same person as staging, so the
# grant is pinned rather than following whoever is calling. See staging.tfvars
# for why the module defaults this off.
assign_deployer_secret_officer = true
deployer_principal_id          = "3537595b-8059-4839-9cd8-04325c824291"

# WITHOUT THIS, CONTINUOUS DEPLOYMENT CANNOT REACH PRODUCTION AT ALL.
#
# The object id of af-infra-ci, the Entra application GitHub Actions federates
# into. cd.yml's production job updates the container app's image, starts the
# bootstrap job and shifts ingress traffic; every one of those is a write and
# the identity holds nothing on this group until this line grants it.
#
# An object id is not a secret. It identifies a principal and unlocks nothing,
# which is why it sits here beside deployer_principal_id rather than arriving as
# an environment variable. That placement is deliberate and is the difference
# between this grant and staging's: a value passed as TF_VAR_ by the plan job
# and NOT by the person who runs apply produces a plan that says "1 to add" on
# every pull request forever, for a resource nobody ever creates. That is
# exactly what ci_principal_id does today, and a plan that is never empty is one
# people stop reading.
#
# The grant this line describes is live in Azure and recorded in
# control-plane-production.tfstate. Deleting the line does not revoke it; it
# turns it into an orphan that the next apply removes. See ci.tf for the commit
# that did exactly that and for the gate that now refuses it.
cd_principal_id = "f99916dc-1e11-4305-8e03-1e116a1e93e1"

# ---------------------------------------------------------------------------
# Alerting.
# ---------------------------------------------------------------------------

# ON HERE AND OFF ON STAGING, DELIBERATELY.
#
# Staging is where a bad deploy is supposed to be caught, so it breaks on
# purpose several times a week. A page for that is a page somebody learns to
# ignore, and it is the same page production sends.
#
# The receivers are NOT in this file. Pass TF_VAR_alert_emails and, if you want
# an SMS, TF_VAR_alert_sms_country_code and TF_VAR_alert_sms_number. Enabling
# alerting with no receiver fails at plan: an action group with no receivers
# creates cleanly, attaches to every rule, reports healthy, and tells nobody.
#
# What it costs, which is mostly one line: nine metric alert rules at 0.10 a
# month each, and an availability test billed per execution per location. Three
# locations every five minutes is 26,280 executions a month at 0.00056, so
# 14.72. Cutting to two locations would halve it and would also mean one
# region's network problem could reach the two-failure threshold on its own.
alerting_enabled = true

# ---------------------------------------------------------------------------
# The operator portal.
# ---------------------------------------------------------------------------

# ON. Twenty three operator routes and twenty two sections shipped in v1.1.0
# and nothing on this plane could reach any of them: the switch generates the
# credential and wires AF_ADMIN_DATABASE_URL, and with it unset the portal
# answers "this installation has no operator database credential configured"
# on every request.
#
# The connection arithmetic is a precondition in the module rather than a hope,
# because createAdminPool runs inside the serving process and a pool that fits
# on its own and not beside the application is a 503 at the next peak. This
# database is GP_Standard_D2ds_v4 with 844 usable connections and the portal at
# the default admin_pool_max takes the peak to 116, so it fits with room that is
# not close.
#
# The credential is generated by the apply and written to the vault. Nobody
# types it and no workflow holds it. It is a SEPARATE role from the one the
# application uses, holding BYPASSRLS, which is what lets one operator read
# across tenants and is exactly why it is not the application's role.
#
# Turning this on does not create an operator. admin_users rows are written
# only by a route that needs an operator session, so a fresh database has
# nobody who can create the first one. `af-control-plane-backup
# bootstrap-operator` is what makes the portal reachable by a person, it reads
# the password from the environment or standard input and never from an
# argument, and it is a step somebody runs after this applies.
operator_portal_enabled = true

# EVERY hostname the marketing site is served on, for the three endpoints a
# browser calls cross origin: the contact form that writes a lead, the careers
# form that writes an application, and the analytics beacon. Unset refuses every
# one of them rather than reflecting whatever Origin arrives, which is the right
# default and the wrong answer for the plane that antifailure.dev actually talks
# to. Unset here presents to a visitor as a network error on the contact form
# with no explanation anywhere.
#
# BOTH HOSTNAMES, AND THIS LINE HELD ONLY THE APEX. antifailure.dev and
# www.antifailure.dev are two custom domains on the same Azure Static Web App,
# both Ready, both answering 200 for every page, and Static Web Apps cannot
# redirect one to the other because a route rule matches on PATH and its schema
# carries no hostname condition. So everybody who typed www, followed an old
# link, or was handed the www page by a search engine had the beacon, the
# contact form and the careers form refused 403, and nothing on the page said
# why. Reported from a phone on the live site while every check was green,
# because every check asked the apex.
#
# tools/site/hostnames.txt is the same set written once. `just check-origins`
# refuses when this value, that file and the domains actually bound to af-site
# disagree.
site_origin = "https://antifailure.dev,https://www.antifailure.dev"

# ---------------------------------------------------------------------------
# The acquisition dashboard, and who it belongs to.
# ---------------------------------------------------------------------------

# WITHOUT THIS, THE PAGE CAN ONLY REFUSE. routers/analytics.ts compares the
# caller's organization slug against this value, and an empty value refuses
# EVERYBODY with PRECONDITION_FAILED naming the variable. That is the state
# this plane was in while the console showed the page to every customer, so the
# only thing anybody ever saw there was an error about a variable they could
# not set.
#
# The slug rather than the identifier, because a slug is what an operator can
# write here without first querying their own database. It is compared against
# the row rather than against the session, so renaming the organization takes
# effect on the next request rather than at a sign-in that may never come.
#
# `antifailure` is the organization operating this control plane. It is the
# same row every tenant here is a peer of, which is why the console never
# receives this value: naming the operator to a customer is a fact about
# somebody else. The session carries a boolean instead.
analytics_operator_org = "antifailure"

# Recording, which is a separate question from reading. Off means the page
# still renders and says, in the provenance line, that every number on it is
# zero because nothing is being counted, rather than presenting an empty funnel
# as a bad week.
analytics_enabled = true

# ---------------------------------------------------------------------------
# Taking payment.
# ---------------------------------------------------------------------------

# ONE SWITCH, AND IT IS THIS LINE. An empty stripe_price_team is billing off
# for the whole installation: modules/control-plane/keyvault.tf references no
# Stripe secret, app.tf sets none of the three environment variables, and the
# process says "billing is off" at start-up. Setting it turns all of that on
# together, which is deliberate. A plane that had an API key and no price, or a
# price and no webhook secret, would be one that can start a checkout it cannot
# finish, and the first person to find that would be a customer holding a
# receipt for something they did not get.
#
# THE VALUE HERE IS NOT A SECRET AND THE TWO THAT ARE DO NOT LIVE IN THIS FILE.
# A price identifier is sent to a browser to start a checkout, so it belongs in
# a tracked file. The API key and the webhook signing secret are addressed by
# their versionless vault IDs and read by the Container App identity at deploy
# time. Terraform never sees either value, no plan reads them, and the pull
# request plan job holds no Key Vault data role at all.
#
# WHICH MEANS THE ORDER MATTERS, AND GETTING IT WRONG BREAKS A DEPLOY RATHER
# THAN FAILING A PLAN. Azure resolves those two references when it creates the
# revision. If the secrets are not in afcpprod-kv-centralus before the apply,
# the plan still succeeds and the revision fails to start. Put both secrets in
# the vault first. docs/src/content/docs/self-hosting/production.md has the
# commands, and they pass the value on standard input so it never reaches a
# shell history or a process listing.
#
# Team is a flat 500 USD per month for the organization rather than per seat,
# which is why checkout sends quantity exactly 1. There is deliberately no
# enterprise price: that plan is arranged with a person, and checkout refuses
# it by name rather than reaching Stripe with an empty identifier.
stripe_price_team = "price_1UBSGCIfNGpUWtp7OVO2YbsY"

# NOT SET, AND THAT IS THE DECISION RATHER THAN THE DEFAULT. hosted_required_plan
# would gate the product behind a paid plan, and modules/control-plane/app.tf
# refuses it unless billing is on, so switching billing on is the moment it
# becomes settable. It stays empty: turning payment on so somebody CAN buy is a
# different act from requiring everybody already here to buy, and the second one
# locks out every organization on this plane the moment it applies.
#
# operator_sets_plan is likewise unset and cannot be set now. Granting a plan by
# hand on a plane that sells the same plan is refused at plan time, and the
# process exits at start-up on the combination.

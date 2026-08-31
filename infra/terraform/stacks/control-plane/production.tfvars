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

# The same two people. This is not a public sign-up and the enterprise SSO path
# is a separate feature; adding somebody is an edit here and an apply, which is
# deliberate, because an allowlist that can be edited in a portal is one nobody
# can review.
signin_allowlist = ["virsanghavi", "maksymrajszewski"]

# EMPTY UNTIL THE PRODUCTION GITHUB APP EXISTS, AND SETTING IT EARLY FAILS.
#
# Production needs its OWN App, not staging's: the webhook secret and the
# private key are the credentials that let a delivery write rows, so sharing
# them means a staging compromise writes into production's tenants. Installation
# ids differ per App and github_installations keys on them.
#
# The module reads the App's two secrets from Key Vault with a data source,
# because GitHub mints the private key once and Terraform can neither create nor
# recreate it. So setting this id before those secrets are in the production
# vault fails at PLAN, which is the correct order and not a bug. The checklist
# in the production guide has the steps in the order that works.
github_app_id = ""

# One identity applies this stack, and it is the same person as staging, so the
# grant is pinned rather than following whoever is calling. See staging.tfvars
# for why the module defaults this off.
assign_deployer_secret_officer = true
deployer_principal_id          = "3537595b-8059-4839-9cd8-04325c824291"

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

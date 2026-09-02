---
title: Runbooks
description: The alerts that exist, what each one means, and the page to open when one of them wakes you.
sidebar:
  # Distinct from the title, because the group this page sits in is also called
  # Runbooks and the sidebar showed the same word twice, once as a heading and
  # once as its first item. The label says what the page answers.
  label: Which alert is which
  order: 9
---

Eleven alert rules watch the hosted control plane. Each one names its runbook in
its own description, so the page arrives in the email and the SMS rather than
having to be found. This is the index of those pages.

They are created by `infra/terraform/modules/alerting` and they are **off by
default**. Production turns them on. Staging does not, and that is deliberate:
staging is where a bad deploy is supposed to be caught, so it breaks on purpose
several times a week. A page for that is a page somebody learns to ignore, and
it is the same page production sends.

## What fires, and where to go

| Alert | Severity | Runbook |
| --- | --- | --- |
| `unreachable` | 0 | [The control plane is unreachable](/docs/self-hosting/runbooks/availability) |
| `database-unreachable` | 0 | [The database is not answering](/docs/self-hosting/runbooks/database-unreachable) |
| `server-errors` | 1 | [Server errors](/docs/self-hosting/runbooks/server-errors) |
| `restart-loop` | 1 | [Revision health](/docs/self-hosting/runbooks/revision-health) |
| `bootstrap-job-failed` | 1 | [A job failed](/docs/self-hosting/runbooks/job-failed) |
| `maintenance-job-failed` | 1 | [A job failed](/docs/self-hosting/runbooks/job-failed) |
| `replicas-below-minimum` | 2 | [Revision health](/docs/self-hosting/runbooks/revision-health) |
| `database-storage` | 2 | [Database storage](/docs/self-hosting/runbooks/database-storage) |
| `database-connections` | 2 | [Database connections](/docs/self-hosting/runbooks/database-connections) |
| `database-cpu` | 3 | [Database CPU](/docs/self-hosting/runbooks/database-cpu) |
| `certificate-expiring` | 3 | [The certificate](/docs/self-hosting/runbooks/certificate) |

Each name is prefixed with the stack's own, so the production rule for the first
row is `afcpprod-unreachable`.

Severity 0 means the service is down for customers. Severity 1 means it is
failing and probably visible. Severity 2 and 3 are warnings with hours or days
in them, and neither should be looked at before the sun is up.

One more control lives outside Azure and pages through GitHub instead: [the
vulnerability scan](/docs/self-hosting/runbooks/security-workflow).

## Who is told

One action group, with an email receiver and an optional SMS receiver. The
addresses are not in this repository. They are passed as `TF_VAR_alert_emails`,
`TF_VAR_alert_sms_country_code` and `TF_VAR_alert_sms_number`, because a plan
runs on every pull request into a step summary that is world readable, and an
address in a variable file leaves through a diff.

Enabling alerting with no receiver fails at plan. An action group with no
receivers creates cleanly, attaches to every rule, reports healthy, and delivers
nothing to anybody. That is worse than no alerting, because it looks like
alerting.

## What is not watched, and why

**The engine.** Nothing here watches a customer's own continuous integration.
The engine runs in their infrastructure and reports through ingestion, and its
own alert rules are in `observability/alerts/antifailure.rules.yml` for anybody
running Prometheus.

**The application's own counters.** `GET /metrics` exposes what the process
counted itself, and Azure Monitor cannot read it. The
[operations page](/docs/self-hosting/operations) is the guide to those, and it
is the page to open second on any incident that starts here.

**Anything outside Azure.** The availability test runs from Microsoft managed
agents in other regions. That is outside this stack, its group, its region and
its network, and it is not outside Azure. A failure large enough to take the
prober and the service together reports nothing at all.

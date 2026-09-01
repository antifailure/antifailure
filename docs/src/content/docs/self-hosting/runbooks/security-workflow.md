---
title: The vulnerability scan stopped protecting the repository
description: The daily Security workflow failed, or it stopped running and nobody noticed.
sidebar:
  order: 18
---

**Not an Azure alert.** This one arrives as a red check on `main` and a GitHub
issue titled "The vulnerability scan is not protecting this repository".

`.github/workflows/security.yml` runs `govulncheck` daily at 07:00 UTC.
`.github/workflows/security-watch.yml` watches it and is what opened the issue.

## The two failures it covers, which are different

**The scan ran and failed.** `govulncheck` found a known vulnerability that is
reachable from this code, or an entry in `.govulncheck.yaml` expired or stopped
matching anything. The scan's own job log names which.

**The scan did not run.** GitHub disables scheduled workflows in a repository
with no activity for sixty days, and does so without saying anything. A
repository whose scan silently stopped looks exactly like a repository with no
vulnerabilities. The watchdog fails when the newest completed **scheduled** run
is more than 26 hours old, which catches this.

Scheduled runs only, and that is the part that is easy to get wrong. The scan
also runs on every pull request, so counting runs of any kind would let a busy
afternoon make a dead schedule look fresh.

The issue body says which of the two happened.

## If the scan failed

Open the run the issue links to and read the finding. The policy is not "no
vulnerabilities": it is that anything reachable is matched by an entry in
`.govulncheck.yaml` saying why it cannot hurt us and when that judgement
expires. `tools/vulncheck` enforces both halves, so an entry that has expired,
or that no longer matches anything, fails the scan on its own.

Three honest outcomes, in order of preference: upgrade the dependency, prove the
path is unreachable and record it with an expiry, or accept it deliberately with
a date to look again.

## If the scan stopped running

Check the workflow's state, and re-enable it:

```sh
gh workflow list --all
gh workflow enable security.yml
gh workflow run security.yml
```

Then look at what the gap was. A repository that has had no push for two months
is dormant, and the right response is to run the scan by hand before picking the
work back up rather than to trust the last green run.

## Why this is not in Azure with the others

Azure Monitor cannot see GitHub Actions, and both ways of teaching it fail on
something specific.

A Log Analytics query over a heartbeat the workflow writes would work, but the
legacy Data Collector API that lets a workflow write one is deprecated with a
retirement date, and the current Logs Ingestion API needs a data collection
endpoint, a data collection rule and a custom table that the `azurerm` provider
cannot create at all.

A custom metric pushed with the OIDC identity this repository already has would
cover the failure half. It would not cover the silence half: a metric alert on a
series that stops being emitted does not fire, and `azurerm_monitor_metric_alert`
exposes no setting for how missing data is treated. A dead man's switch that
does not notice death is the failure this control exists to prevent.

So the watchdog runs where the thing it watches runs.

## What it still cannot see

The watchdog is a scheduled workflow too, so sixty days of inactivity disables
it alongside the scan. It therefore also runs on every push to `main`: a
repository being pushed to is one whose scans are being checked. A repository
that is neither pushed to nor scanned is dormant, and this page is what to read
when it wakes up.

## What not to do

**Do not close the issue to make it go away.** The watchdog closes it itself on
the first healthy scheduled scan, and closing it by hand means the next failure
opens a second one rather than commenting on the first.

**Do not add an exemption without an expiry.** `tools/vulncheck` refuses one,
and the reason is that an exemption with no date is a decision nobody will ever
revisit.

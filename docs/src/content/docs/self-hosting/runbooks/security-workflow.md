---
title: The vulnerability scan stopped protecting the repository
description: The daily Security workflow failed, or it stopped running and nobody noticed.
sidebar:
  order: 19
---

**Not an Azure alert.** This one arrives as a red check on `main` and a GitHub
issue titled "The vulnerability scan is not protecting this repository".

`.github/workflows/security.yml` runs `govulncheck` daily at 07:00 UTC.
`.github/workflows/security-watch.yml` watches it and is what opened the issue.

## The three failures it covers, which are different

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

**The scan was cancelled before it started.** This one is new, it has happened,
and it looks exactly like the case above from the outside. A scheduled run that
a concurrency group cancels never reaches a job, so it completes in seconds with
a `cancelled` conclusion and nothing in its log. The watchdog skips a cancelled
run rather than reading it as a failure, correctly, so the newest run it counts
is yesterday's and it ages out at 26 hours. On 2026-09-05 the schedule fired at
11:11:27Z, 21 seconds after a merge to `main`, and was cancelled 17 seconds
later with zero jobs.

The issue body says which of the three happened, except that it cannot tell the
second from the third: both read as a scan that is too old. The next section
tells them apart in one command.

## If the scan failed

Open the run the issue links to and read the finding. The policy is not "no
vulnerabilities": it is that anything reachable is matched by an entry in
`.govulncheck.yaml` saying why it cannot hurt us and when that judgement
expires. `tools/vulncheck` enforces both halves, so an entry that has expired,
or that no longer matches anything, fails the scan on its own.

Three honest outcomes, in order of preference: upgrade the dependency, prove the
path is unreachable and record it with an expiry, or accept it deliberately with
a date to look again.

## If the scan is too old, find out which of the two reasons it is

**Look at the scheduled runs before touching anything.** This is the step that
was missing, and without it the section below sends you to re-enable a workflow
that was never disabled.

```sh
gh api "repos/antifailure/antifailure/actions/workflows/security.yml/runs?event=schedule&per_page=10" \
  --jq '.workflow_runs[] | "\(.created_at)  \(.status)/\(.conclusion)  \(.id)"'
```

A gap with **no rows at all** in it is the schedule not firing, which is the
next section. A row that is there and says `cancelled` is the third case. This
is what that looked like on 2026-09-05, before it was remedied:

```
2026-09-05T11:11:27Z  completed/cancelled  33962658928
2026-09-04T12:02:16Z  completed/success    33870767538
```

**That run now reads `completed/success`, because re-running it is what this
section tells you to do and somebody did.** The example is kept as it was rather
than refreshed, because a runbook whose worked example shows the healthy state
teaches nothing about the sick one. Do not expect that id to reproduce the row
above.

Confirm the diagnosis by asking whether the run reached a job. A concurrency
cancellation reaches none:

```sh
gh api repos/antifailure/antifailure/actions/runs/<id>/jobs --jq '.jobs | length'
```

Zero, and a `created_at` to `updated_at` gap of seconds rather than minutes,
means the run was superseded before it began. Against 33962658928 that command
answers `2` today, for the same reason: the second attempt ran the jobs. Ask it
of the run your own issue names, not of this one. **Re-run it.** The remedy is
that run, not a new one, because only a run of the `schedule` event counts:

```sh
gh run rerun <id>
```

The second attempt keeps `event: schedule`, so the watchdog sees a fresh
completed scheduled run and goes green. `gh workflow run security.yml` does not
help here; it produces a `workflow_dispatch` run, which the watchdog
deliberately ignores for the same reason it ignores pull request runs.

Then ask why it was cancelled, because a fix may already be in the tree and not
yet in effect. `security.yml` gives the schedule its own concurrency group so
that activity on `main` cannot reach it. A scheduled run STILL cancelled after
that is a real regression in the group expression. A scheduled run cancelled
*before* that change reached `main` is not: on 2026-09-05 the fix landed at
12:34:27Z and the cancelled run had fired at 11:11:27Z, 83 minutes earlier.
Compare the fix's commit time against the run's, rather than assuming the guard
was live.

## If the scan stopped running

Only after the section above shows a gap with no scheduled rows in it. Check the
workflow's state, and re-enable it:

```sh
gh workflow list --all
gh workflow enable security.yml
gh workflow run security.yml
```

`gh workflow list --all` reporting `active` means this is NOT the case you have,
and the section above is where to look.

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

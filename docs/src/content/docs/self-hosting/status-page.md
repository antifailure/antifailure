---
title: Status page
description: The cheapest honest way to tell customers something is wrong, and why the signal has to come from outside the thing it reports on.
sidebar:
  order: 7
---

## The one property that decides the design

**The check has to come from somewhere other than the thing it checks.** A
status page hosted on the control plane's own Container App, reading the
control plane's own `/metrics`, cannot report a total outage of the control
plane. The process that would say "I am down" is the process that is down.

Whatever hosts the check and whatever hosts the page both have to survive an
outage of the thing being watched.

## What this rules in and out

A synthetic external monitor checking the public origin from somewhere else
satisfies the property. A hosted uptime or status page product is one answer:
point it at `https://app.antifailure.dev/readyz` and read its `ready` field.
The other, built here, is a scheduled check on GitHub's compute with the page
hosted off Azure, so an Azure-wide event that took out the control plane would
not take out the thing reporting on it. It needs only `curl`, `jq`, and a place
to push a branch.

## What is watched, and why each one separately

`deploy/status/targets.json` names components, each one able to fail while the
others are fine.

| Component | Checked | Why it is its own line |
| --- | --- | --- |
| Control plane API | `app.antifailure.dev/readyz` | What the engine posts reports to and what a customer signs in against. |
| Console | `app.antifailure.dev/` | Served by the same process, from a static export copied into the image. An image whose console directory is empty answers every page with a 503 while `/readyz` stays green. |
| Website | `antifailure.dev/` | The marketing site. |
| Documentation | `antifailure.dev/docs` | Every error the engine prints ends in a link to a page here. A publish that drops the subtree breaks all of them. |
| CLI installer | `antifailure.dev/install.sh` | What `curl` is piped from. It is placed by the site assembly. |
| Site API | `antifailure.dev/api` | A managed function, not a static file. It can be present and refuse every request. |
| Control plane, staging | `app.dev.antifailure.dev/readyz` | Where `main` lands first. Listed as pre-production, because it is not a customer surface and should never be read as one. |

The first two share a process and the next four share a Static Web App, so an
outage of one will often show as an outage of its neighbours.

## What a check asserts

The control plane checks read `/readyz`, the same endpoint as
[`deploy/cd/health-gate.sh`](/docs/self-hosting/azure#upgrade-and-rollback-the-manual-path).
`/health` is a static literal that answers even when the database cannot. A
`200` carrying `"ready": false` is a failure here.

The static checks assert a marker in the body as well as the `200`. The markers
are build output paths and route names rather than copy, so a prose edit is not
a false outage.

## What the page shows

In order: any open incident first, then every component with its current status
and its last ninety days, then the response times behind those checks, then the
incident history day by day.

Each component states its status as a **word** as well as a colour:
`Operational`, `Degraded Performance`, `Partial Outage`, `Major Outage`, and
the two most status pages have no word for and quietly render as green,
`No Recent Data` when the probe has stopped arriving and `No Data` when a
component has never been checked.

Every status word carries the age of the check that earned it, on the same
line: `Operational  checked 21 minutes ago`. GitHub delivers this five minute
cron every three to six hours in practice. The page also says once, where the
list starts, that Operational means the most recent check passed and not that a
component is up right now.

A component whose last reading is older than three times the interval the probe
has actually been keeping reads `No Recent Data`, not `Operational`.

The amber and the red in the day strip are 0.7 apart in OKLab under
deuteranopia and the green and the red are 4.0 apart. So a day containing any
failure is also capped in near black and sized by the share of that day's
checks that failed, and the neutral for a day with no readings is achromatic.

Under System metrics is the only other thing measured: how long each check
took. There is no CPU, no queue depth and no throughput. The window selector is
three radio inputs and a stylesheet, with no script.

## What the page refuses to say

Every number on it is computed from the record. There is no configured target
and no typed figure.

- **The percentages are the share of checks that passed**, and the page says
  so in those words rather than calling it uptime. Between two checks it knows
  nothing, and an outage shorter than the gap can pass unrecorded.
- **A ninety day figure is only called that once the record reaches back
  ninety days.** Before then the page says how much record there is, on the
  section heading and again on every row.
- **Nothing rounds up.** A percentage is floored, so only an unbroken run of
  passing checks can print `100%`.
- **A day with no readings is drawn in the neutral**, never in green, and is
  never counted as a day that was up.
- **A gap in the readings is a gap in the line.** An isolated reading is drawn
  as a dot rather than joined to one hours away.
- **The observed interval is printed, not the schedule.** The workflow asks
  for a check every five minutes. GitHub drops scheduled runs under load and
  delivers considerably fewer, so the page measures the gaps between the
  readings it actually has.

Nothing on the page animates. There is no live indicator.

## Subscribe

The Subscribe control is an Atom feed at `feed.xml`, generated from the same
data by `deploy/status/feed.jq`.

Two kinds of entry. One per incident update, so a subscriber sees each note as
it is written rather than one entry that silently changes. And one per run of
consecutive failed checks detected in the readings. A detected entry says so in
its own text and carries when the run started, when it last failed, and whether
a later check has passed.

## Incidents

Incidents and scheduled maintenance are one JSON file each under
`deploy/status/incidents/`, on `main`. Add a file, open a pull request, merge
it, and the next probe publishes it.

They live on `main` rather than on the `status-data` branch the probe writes,
and the reason is not tidiness. A note written during an outage is the highest
stakes prose this project publishes, and it is written by a tired person at an
unsociable hour. On `main` it gets a diff, a review and a history. On
`status-data` it would be a hand edit of an orphan branch a machine pushes to
every few minutes, where the likely outcome of a mistake is a force push over
the probe's own record. The cost is that an incident reaches the page on the
next probe rather than instantly, and the alerting stack, not this page, is
what wakes anybody.

`deploy/status/incidents/README.md` carries the fields. The shape is a flat
object with no generator and no schema registry.

The `validate` job in `.github/workflows/status.yml` checks every file on any
pull request touching `deploy/status`, including that each component an
incident names exists. The renderer never fails on a bad file: it reports it by
name on the page and renders the rest.

## What is built

- `deploy/status/targets.json` names the components and what to assert about
  each.
- `deploy/status/probe.sh` checks every one of them and prints one reading per
  line. It never fails the run on a component being down.
- `deploy/status/render.sh` folds a run's readings into two records and
  renders the page. `history.json` holds recent raw readings, bounded by age
  and by count. `daily.json` holds one rollup per component per UTC day, and
  is what the ninety day strip is drawn from, so the page can see further back
  than the raw readings it keeps.
- `deploy/status/page.jq` is the page: the layout, the wording and the
  stylesheet, with every value escaped on the way out.
- `deploy/status/feed.jq` is the Atom feed behind the Subscribe control.
- `deploy/status/render_test.sh` runs the renderer over the states this page
  will actually be in, including the ones nobody builds: no history, one
  reading, a gap, a component never probed, a probe that stopped, a malformed
  reading, an outage, a recovery, and incidents open, closed, scheduled and
  unreadable.
- `.github/workflows/status.yml` runs the probe on a schedule and pushes the
  result to a branch named `status-data`, deliberately not `main`. A commit to
  `main` every five minutes would fire `cd.yml`'s staging deploy every five
  minutes. That is a second reason this lives apart from the branch that ships
  code, on top of the first reason: the page's own history should not pile up
  in the commit log of the product it is watching.

The page is self contained. No font file, no stylesheet, no script, no image
and no request of any kind leaves the document. The type is the reader's system
stack with the site's type scale and tracking applied over it, and every colour
is copied by value from the console's palette.

## The step left for a person

**Turn on Pages.** Settings > Pages > Build and deployment > Deploy from a
branch > branch `status-data`, folder `/ (root)` > Save. The page appears at
`https://<owner>.github.io/<repository>/` within a minute or two of the next
probe. That address needs nothing else: the page carries its own stylesheet
and asks for no other file, so serving it under a path prefix changes nothing
about how it renders. Until this is done the workflow still runs, still writes
`status-data`, and the record is still readable with `git log` or by cloning
that branch. There is simply no public URL.

For the Antifailure deployment itself this is **done**: Pages is enabled, https
is enforced, and the page is live at <https://antifailure.github.io/antifailure/>.

`status-data` is an orphan branch inside this repository, with no common
ancestor with `main`, rewritten by `status.yml` on every probe.

**Optionally, point a subdomain at it.** This one is still open for the
Antifailure deployment. A `CNAME` for `status` in the `antifailure.dev` zone, targeting `antifailure.github.io`, plus the same name
entered under Settings > Pages > Custom domain, which writes a `CNAME` file
into `status-data`. The probe only ever stages `history.json`, `daily.json`
and `index.html`, so that file survives every push it makes.

Read the order of those two the way the first one is written: **enable Pages
first and publish the `github.io` address, rather than waiting for the
subdomain.** The subdomain is the nicer link and it is the weaker one. The
`antifailure.dev` zone is Azure DNS, so resolving `status.antifailure.dev`
puts a piece of Azure back in the path to the page whose entire purpose is to
be readable when Azure is having a bad day. It is a much smaller dependency
than hosting would be, and cached resolutions soften it further, but it is not
nothing, and `antifailure.github.io` has none of it. Publish both and give the
`github.io` address as the fallback in the incident note.

The subdomain must not be a route on `antifailure.dev` itself. That hostname is
the Static Web App, so the page and the site it reports on would share an Azure
region. The footer of every `antifailure.dev` page links the status page under
Connect, and `antifailure.dev/status` is a 301 to the `github.io` address.

## What this is not

**It is not the pager.** The alerting stack behind
[the alert rules](/docs/self-hosting/operations#what-the-alerts-mean) is what
wakes a person. This page is what a customer reads, and it has no opinion about
whether one organization's own repository is failing.

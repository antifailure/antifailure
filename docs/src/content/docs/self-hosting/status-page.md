---
title: Status page
description: The cheapest honest way to tell customers something is wrong, and why the signal has to come from outside the thing it reports on.
sidebar:
  order: 3.5
---

Customers of an availability product ask for a status page before they ask for
almost anything else. The wrong status page is worse than none. A page that
says "all systems operational" during an outage has not failed to inform
anyone. It has actively told them something false, at the exact moment they
are checking because they suspect it is not true.

## The one property that decides the design

**The check has to come from somewhere other than the thing it checks.** A
status page hosted on the control plane's own Container App, reading the
control plane's own `/metrics`, cannot report a total outage of the control
plane. The process that would say "I am down" is the process that is down.
This is not a hypothetical. `/health` answered `200` for thirteen minutes with
no database schema behind it, once, already, in this project. That is
described on the [operations page](/docs/self-hosting/operations/). A status
page built the same way would repeat that failure in front of customers,
instead of in a log nobody reads yet.

The corollary: whatever hosts the check and whatever hosts the page both have
to survive an outage of the thing being watched. They do not need to survive
an outage of *everything*. A status page cannot promise more resilience than
exists to give it.

## What this rules in and out

A synthetic external monitor, checking the public origin from somewhere else,
satisfies the property by construction. Two shapes of it exist.

**A hosted uptime or status page product** is a legitimate answer. Several
have a workable free tier for one monitor. For a team that already uses one
for something else, it is probably the right one: someone else's
infrastructure runs the check and hosts the page. The only work is pointing it
at `https://app.antifailure.dev/readyz` and reading its `ready` field. It does
add a real, if usually small, ongoing dependency, and often a cost once more
than one monitor or one page is needed.

**A scheduled check on infrastructure the project already trusts for
something else** is the other answer, with the page hosted apart from Azure.
This is the one built here. GitHub already holds this repository, runs CI and
CD, and issues this project OIDC credentials. Adding a status probe to it is
not a new vendor. It is the existing one doing one more scheduled thing. The
check runs on GitHub's compute, not Azure's, so an Azure-wide event that took
out the control plane would not also take out the thing reporting on it. Free
at this scale, and it needed nothing this repository did not already have:
`curl`, `jq`, and a place to push a branch.

This project is small enough that the second answer costs less to build than
it costs to evaluate the first. That is why it is what exists today. A team
that already pays for a monitoring product should point it at the same
`/readyz` endpoint instead of adopting this one. The two are not exclusive,
and nothing here assumes this is the only way to watch this system.

## What is watched, and why each one separately

A status page whose granularity is "the whole company" cannot answer the only
question anybody brings to it, which is whether the thing they use is
affected. So `deploy/status/targets.json` names components, and each one is
there because it can fail while the others are fine.

| Component | Checked | Why it is its own line |
| --- | --- | --- |
| Control plane API | `app.antifailure.dev/readyz` | What the engine posts reports to and what a customer signs in against. |
| Console | `app.antifailure.dev/` | Served by the same process, from a static export copied into the image. An image whose console directory is empty answers every page with a 503 while `/readyz` stays green. |
| Website | `antifailure.dev/` | The marketing site. |
| Documentation | `antifailure.dev/docs` | Every error the engine prints ends in a link to a page here. A publish that drops the subtree breaks all of them. |
| CLI installer | `antifailure.dev/install.sh` | What `curl` is piped from. It is placed by the site assembly, and two copies of that assembly had already drifted to the point that neither placed it. |
| Waitlist API | `antifailure.dev/api` | A managed function, not a static file. It can be present and refuse every request, and did, for two days, behind a green deploy each time. |
| Control plane, staging | `app.dev.antifailure.dev/readyz` | Where `main` lands first. Listed as pre-production, because it is not a customer surface and should never be read as one. |

The first two share a process and the next four share a Static Web App, so an
outage of one will often show as an outage of its neighbours. They are still
separate lines, because each of the failures in the right hand column has
happened to exactly one of them.

## What a check asserts

The control plane checks read `/readyz`, the same endpoint and the same
reasoning as
[`deploy/cd/health-gate.sh`](/docs/self-hosting/azure/#upgrade-and-rollback-the-manual-path).
`/health` is a static literal that answers even when the database cannot. A
status page built on it would report an outage as healthy, the same way a
liveness probe would. A `200` carrying `"ready": false` is a failure here,
which is the distinction that endpoint exists to make.

The static checks assert a marker in the body as well as the `200`. Every
surface in the table above has already been published broken behind a `200`,
so a check that reads only the status line would have called those healthy.
The markers are build output paths and route names rather than copy, because a
marker that tracks a headline turns a prose edit into a false outage, and a
false outage is the one thing this page must never publish.

## What the page shows

Plain, dense and in the order a person needs it: any open incident first, then
every component with its current status and its last ninety days, then the
response times behind those checks, then the incident history day by day. No
card inside a card and nothing decorative, because somebody reading this is
trying to find one fact quickly while something else is going wrong.

Each component states its status as a **word** as well as a colour:
`Operational`, `Degraded Performance`, `Partial Outage`, `Major Outage`, and
the two most status pages have no word for and quietly render as green,
`No Recent Data` when the probe has stopped arriving and `No Data` when a
component has never been checked.

Every status word carries the age of the check that earned it, on the same
line and at the same weight: `Operational  checked 21 minutes ago`. That
pairing is the point. GitHub delivers this five minute cron every three to six
hours in practice, so a status word standing alone would be a weaker claim
than a reader takes it for, and a reader can only discount it if the age is in
front of them rather than in a paragraph at the foot of the page. The page
also says once, where the list starts, that Operational means the most recent
check passed and not that a component is up right now.

Past a threshold the word itself changes. A component whose last reading is
older than three times the interval the probe has actually been keeping reads
`No Recent Data`, not `Operational`, because at that point the page has
stopped knowing rather than started disagreeing.

The word is not politeness. The amber and the red in the day strip are 0.7
apart in OKLab under deuteranopia and the green and the red are 4.0 apart,
which is to say all three bars are one bar to a red-green colour blind reader
and on a greyscale printout. So a day containing any failure is also capped in
near black and sized by the share of that day's checks that failed, and the
neutral for a day with no readings is achromatic, which is the one thing no
form of colour blindness can confuse with the other three.

Under System metrics is the only thing this design measures besides pass and
fail: how long each check took. There is no CPU, no queue depth and no
throughput, because nothing here observes any of those and a chart of a number
nobody measured is the worst thing a status page can contain. The window
selector is three radio inputs and a stylesheet, with no script at all, since
a page that has to render from a cold cache during an outage cannot depend on
JavaScript arriving.

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
  never counted as a day that was up. The strip ships almost entirely neutral
  and that is the honest picture of a record that has just started.
- **A gap in the readings is a gap in the line.** An isolated reading is drawn
  as a dot rather than joined to one hours away, because a line across a gap
  is a line through data that does not exist.
- **The observed interval is printed, not the schedule.** The workflow asks
  for a check every five minutes. GitHub drops scheduled runs under load and
  delivers considerably fewer, so the page measures the gaps between the
  readings it actually has.

Nothing on the page animates. There is deliberately no live indicator: a
pulsing dot says nothing a timestamp does not say better, and it says it
forever.

## Subscribe

The Subscribe control is an Atom feed at `feed.xml`, generated from the same
data by `deploy/status/feed.jq`.

It is a feed rather than a mailing list because there is no mailing list here,
and a Subscribe button that does nothing is worse than no button: it tells a
customer they will be told, and then does not tell them. A feed costs almost
nothing, works in every reader, and is what a person watching a vendor's
status actually wants.

Two kinds of entry, and both are real. One per incident update, so a
subscriber sees each note as it is written rather than one entry that silently
changes. And one per run of consecutive failed checks detected in the
readings, because without those the feed would be empty until somebody hand
wrote an incident, and the most common real outage is the one nobody had time
to write up. A detected entry says so in its own text and carries only what
the readings support: when the run started, when it last failed, and whether a
later check has passed.

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
object with no generator and no schema registry, because the failure to design
against is not a missing feature, it is a habit nobody keeps: an incident
history that stays empty because writing one is hard is a lie of omission the
moment something has gone wrong.

Two things guard it. The `validate` job in `.github/workflows/status.yml`
checks every file on any pull request touching `deploy/status`, including that
each component an incident names actually exists, which is the typo that would
otherwise attach an incident to nothing at all. And the renderer never fails
on a bad file: it reports it by name on the page and renders the rest, because
a probe has to keep publishing whatever else is wrong.

## What is built

- `deploy/status/targets.json` names the components and what to assert about
  each.
- `deploy/status/probe.sh` checks every one of them and prints one reading per
  line. It never fails the run on a component being down, because a component
  that does not answer is a status to report rather than a reason to stop
  reporting it.
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
and no request of any kind leaves the document, because the one moment it has
to render correctly is the moment something else is broken. That rules out the
site's own web fonts, so the type is the reader's system stack with the site's
type scale and tracking applied over it, and every colour is copied by value
from the console's palette.

## The two steps left for a person

Neither of these can be done from inside this repository, the same way nothing
in `deploy.yml` can set the static site's publish token. Both are one person's
action, once, and everything either one depends on is already built and
running.

**Turn on Pages.** Settings > Pages > Build and deployment > Deploy from a
branch > branch `status-data`, folder `/ (root)` > Save. The page appears at
`https://antifailure.github.io/antifailure/` within a minute or two of the
next probe. That address needs nothing else: the page carries its own
stylesheet and asks for no other file, so serving it under a path prefix
changes nothing about how it renders. Until this is done the workflow still
runs, still writes `status-data`, and the record is still readable with
`git log` or by cloning that branch. There is simply no public URL.

**Optionally, point a subdomain at it.** A `CNAME` for `status` in the
`antifailure.dev` zone, targeting `antifailure.github.io`, plus the same name
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

What the subdomain must not be is a route on `antifailure.dev` itself. That
hostname is the Static Web App, so serving this page from it would put the
page and the site it reports on in the same Azure region, and one event would
take both down together. That is the exact failure this whole design avoids.

## What this is not

**It is not the pager.** The alerting stack behind
[the alert rules](/docs/self-hosting/operations/#what-the-alerts-mean) is what
wakes a person. This page is what a customer reads. They watch the same
system from different distances, and neither substitutes for the other. A
fast burn alert can page someone before a single failed check has accumulated
enough history to move the page's bars. The page also has no opinion about
whether one organization's own repository is failing, which is exactly the
distinction the alerts are built to make and this page is not.

**It is not real time.** The gap between checks is the cost of running on a
free scheduled trigger rather than a dedicated always-on watcher. It is an
honest gap: the page never claims to know about anything more recent than its
last check, it prints when that was, and it prints how far apart the checks
have actually been arriving.

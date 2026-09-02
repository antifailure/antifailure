# Changelog

One section per released tag. `tools/relnotes` reads the section matching the
tag being published and the release workflow puts it in the release notes, so a
tag with no section here, or with an empty one, does not publish at all.
Releases before v1.0.0 predate this file and their notes are on the GitHub
releases page.

## v1.0.0

The first stable release, and the first since v0.1.1 on 26 August 2026.

No commit count here on purpose. One was written down, and it was 577 with 197
landings over four days, and it had drifted to 812 and 238 over seven before
anybody looked. A figure in an unreleased section counts a tree that is still
moving, so it is wrong from the moment it is written until the tag freezes it,
and nothing checks it: `just figurecheck` reads the documentation and this file
is not documentation.

### What 1.0 means, and what it does not

A major version is a promise about what will keep working, so here is the
promise, named surface by surface rather than as a blanket claim about an API.
The standing version is at antifailure.dev/docs/reference/stability.

Stable, and breaking any of these costs a major version:

- **The manifest.** A manifest declaring `version: 1` keeps working. Keys may be
  added and existing ones may gain new accepted values; a key will not be
  removed, renamed, or given a different meaning. The promise runs backwards,
  not forwards: an older manifest works on a newer `af`, and a manifest using a
  key added in a later 1.x does not work on an earlier one, because the parser
  refuses a key it does not know rather than ignoring it.
- **The command line.** The commands, their flags, and their exit codes. A
  command will not be removed or renamed, and a flag will not change what it
  means. New commands and new flags are minor releases.
- **`--output json`.** The documented fields of every command's JSON. Fields may
  be added, so parse for the fields you want rather than refusing unknown ones.
  A documented field will not be removed or change type.
- **The provider interfaces.** `engine/pkg/provider` and the `engine/pkg/schema`
  types they carry. A provider is meant to be written outside this repository
  and each interface ships with a conformance suite, so it is an integration
  surface and is treated as one.
- **The error codes.** A code in the error reference keeps its meaning. Codes
  are the stable identifier for a refusal; the sentence beside one is not.

Explicitly not stable, and free to change in a minor release:

- The Helm chart's values and the Terraform module's variables. The chart is
  versioned separately and is at 0.1.1 for that reason.
- The control plane's HTTP API, which the console and the engine speak to each
  other and which is not a published integration surface.
- Every Go package except the two named above, and everything under
  `engine/internal`, which is unimportable on purpose.
- Lint rule names and their findings, which move as the rules improve. The
  stable identifier for a finding is its rule name within a release.
- The event stream's set of types. Types are added as features land.

### What moves when this tag is pushed

Pushing `v1.0.0` publishes the control plane image as
`ghcr.io/antifailure/control-plane:v1.0.0` and **moves
`ghcr.io/antifailure/control-plane:latest` onto it.** `latest` resolves to the
v0.1.1 digest until then. If you self host and pull `latest`, this tag changes
what your next pull gets, on your infrastructure, at a time we chose rather than
one you did. Pin `v1.0.0`, or pin the digest that tag resolves to, if that
matters to you. A tag can be moved and a digest cannot.

Nothing else moves on its own. No deployment is triggered by this tag, and no
existing environment is upgraded.

### Supply chain

This release is the first whose workflow runs the SPDX bill of materials and the
cosign keyless signing. Both were written after v0.1.1 was tagged and no
published artifact has ever carried them, so this run is the first execution of
either and not a track record. You can settle for yourself whether they ran: a
release that ran them carries `checksums.txt.sigstore.json` and
`sbom.spdx.json`, and v0.1.0 and v0.1.1 carry neither. The verification commands
are at the top of these notes.

### Installing

`install.sh` is unchanged. It asks the GitHub releases API for the newest
release rather than carrying a version of its own, verifies the checksum of what
it downloaded, and installs `af` into `~/.local/bin`. `AF_VERSION=v0.1.1
./install.sh` still installs an older one.

### Behaviour you may depend on that changed

Read this section before upgrading. Everything here changes what an existing
manifest or pipeline does.

- A run holding a workflow verdict the engine cannot read is now `blocked`. It
  used to report the whole run as passed and exit zero while the table beside
  that line printed the same workflow as unverified.
- A run that tried to reach a host the manifest does not mention no longer
  reports `pass`. The request was always refused; the attempt reached the pull
  request comment and changed nothing about the verdict.
- `af ci` runs teardown before it writes the report, so a run that left a
  resource behind says so and, by default, does not ship.
- A migration that holds an exclusive lock past `policy.migration_lock.fail_ms`
  now fails the check and names the table. The rehearsal has sampled `pg_locks`
  since phase 3 and none of it reached a pull request before now.
- Every route to the hosted control plane says the same thing about it. The
  `/signin` and `/signup` tabs both read "Join the waitlist" and both
  descriptions said there was no hosted control plane, which a crawler, a
  bookmark and a shared link all carried while the page underneath offered a
  working GitHub button. The buttons leading there said four different things
  depending on where you found them. Each route now carries its own name, the
  titles and descriptions match the page they open, and the home page states
  that the control plane is invitation only and the engine is not.
- `load.source` no longer accepts `datadog` or `newrelic`. Both were in the
  schema, so a manifest could set them, and both refused when a run reached
  them. An unrecognised source is now refused by name at validation time, with
  the sources that do work.
- An installation licensed for `policy_enforcement` now actually refuses an
  environment that violates the organization policy. `policyenforce.Hook` was
  written and tested and no binary ever constructed one, so the feature refused
  nothing and its compliance report said no policy was configured.
- The egress sidecar enforces the same decision on all three of its request
  paths. A plain HTTP request through the explicit proxy port, which is what
  every client reading `http_proxy` sends, used to skip the live credential
  tripwire, forward the application's own credential in `sandbox` mode, and
  refuse `capture`, `mock` and `synth` instead of serving them.
- The example GitHub Actions workflow runs `af change` first and gates `af ci`
  on its output, so a pull request that touches nothing any check exercises no
  longer gets an environment. It checks out with `fetch-depth: 0`, because a one
  commit deep clone has no merge base to diff against.
- The documentation now runs the control plane image as
  `ghcr.io/antifailure/control-plane:latest` rather than naming a version. The
  tag is moved by the push of a `v*` tag and never by a build off `main`.
- A run in which no workflow reached a verdict about the application exits `9`
  rather than `0`. A real failure still exits `8`, so a pipeline reading the
  number can tell "your change broke something" from "nothing was tested". Both
  of this repository's own answers were the old shape: the control plane's check
  reported that six workflows could not be carried through and went green every
  time. A project with no workflows yet can set `policy.workflows_unverified` to
  `warn` and have that choice recorded in its manifest rather than assumed from
  silence.
- `github.fork_policy` refuses something for the first time. It was validated,
  defaulted, printed by `af explain` and read by nothing, so `af up` on a fork's
  pull request went to the Docker daemon under a default whose own description
  says a maintainer has to add a label first. `af ci`, `af up`, `af test` and
  `af load run` now refuse with `AF-GH-003`, before an environment is named and
  before the daemon is touched. The setting is read from the base branch rather
  than from the checkout, because on a fork's pull request the checked out
  manifest is the fork's own copy. `github.comment: false` is consulted too,
  where turning comments off used to leave them on, and the example workflow
  subscribes to `labeled`, because adding the label is an event.
- A misspelt value in the `github` block is now an error. `fork_policy: nevr`,
  `mode: sideways` and `teardown_on: [closed, merged]` all loaded without a
  word, and everything downstream reads an unrecognised fork policy as `label`,
  so a typo in a security control chose the weaker behaviour silently.
- `load.safe_routes` and `load.unsafe_routes` entries carrying an HTTP method
  match what they were written to match. Normalisation turned `DELETE /*` into
  `/DELETE /*`, so every example in the load documentation was inert. A safe
  list failing that way is loud, because a run that may send nothing refuses
  everything; an unsafe list failing that way is silent, so a manifest with
  `unsafe_routes: ["DELETE /**"]` was sending the deletes its author wrote that
  entry to prevent. A run whose unsafe list now matches will send less traffic
  than it did.
- The documented `safe_routes` and `unsafe_routes` examples use `**` rather than
  `*`. A single star covers exactly one path segment, so `DELETE /*` never
  covered `DELETE /orders/42`, and a delete almost always carries an id. The
  matcher itself is unchanged, because making a single star span segments would
  change what every deployed manifest already means.
- `af ci --load` enforces `load.error_rate`. It read both thresholds out of the
  manifest and passed one of them on, and `Breaches` builds no error rate breach
  from a zero limit, so a change that failed 100 percent of requests under load
  merged green while `af load run` on the same manifest exited non zero. `af ci`
  also says now when a p95 threshold was in force and no route had a baseline to
  compare against, and how many routes the safe list withheld.
- A workflow that touched a response invented by a model reports `unverified`
  rather than `passed`. The promise was made in five places, including the
  product page, and was kept nowhere: the sidecar wrote `synthesized` on the
  decision, `local.Decision` had no field for it, and the runner's
  `synthesized-response` cause had a mapping, a test and no producer. A
  synthesized call is made by the application and never appears in anything a
  browser can see, so the verdict is decided in the engine now. A failure is
  never downgraded.
- `af oracle -o json` wrote the comparison to a file called `json`, and
  `af support bundle -o json` wrote a zip archive to one. Each declared a local
  `--output` with an `-o` shorthand, which silently wins over the persistent
  flag that means text or json everywhere else, so the JSON branch both commands
  carried was unreachable. The local flags are `af oracle --report` and
  `af support bundle --archive` now, and neither takes a shorthand.
- A golden records the project it was made for, and an environment branches only
  a golden whose record matches its own. Expect one golden refresh per project
  the first time a command runs after upgrading, because no existing golden
  carries the record.
- `af workload` is hidden from `af --help`. It is what a hosted control plane
  calls on your behalf; the commands a person runs are `af load run`,
  `af load scenario`, `af test` and `af explore`. Its flags are documented under
  Workloads instead.
- The dispatch workflow template calls `af workload run` rather than assembling
  flags in a shell case statement, and its `command` input keeps its verbs. `up`,
  `down`, `agents` and `load` work on an older copy of the file; `scenario` and
  `explore` need this one. An input the case statement had no flag for used to
  be dropped without a word and is now refused by name.
- An exploration run through `af workload run --kind exploration` no longer
  fails the job when a goal is not reached. `docs/concepts/exploration` has
  always promised that an exploration cannot fail your build, and the promise
  held for `af explore` and broke for the path the console drives. A run that
  measured nothing at all is still blocked and still exits non zero.

### Added

**Before an environment exists.** `af change` reads the diff of a pull request
and says which checks will exercise what it touched, opening no environment and
touching no database. Every changed path is classified by a rule that names it,
and a path no rule recognises selects every check rather than none. It never
grades the change: there is no score and no risk word, because both would be a
judgement made from a file listing. `change.rules` teaches it a layout the built
in rules do not predict.

**Migration safety.** The migration rehearsal now carries 17 DDL lint rules. The
one that matters most is `lock_timeout`: a migration that waits for a lock
queues every subsequent query on that table behind its own lock request, so a
four millisecond `ALTER TABLE` behind one long running transaction stops all
writes for as long as that transaction runs. The rest cover `SET NOT NULL` on an
existing column, a `CHECK` added without `NOT VALID`, `ADD CONSTRAINT ... UNIQUE`
building its index in place, a backfill in the same transaction as its schema
change, and `DROP INDEX`, `REINDEX`, `VACUUM FULL`, `CLUSTER`, `DROP TABLE` and
`TRUNCATE`. Each names the lock mode, the real row count of the table on the
branch, and the multi deploy sequence that avoids the problem.

`af insights` also runs the previous release against the migrated branch and
reports whether its workflows still pass, which is the invariant a rolling
deploy depends on. A failure is confirmed against a second branch with the
migrations left off, so a workflow that fails either way is reported as
unverified rather than blamed on the change. Configured by
`insights.rolling_compatibility`, which defaults to running only when the
migrations take something away.

**`af oracle`.** Runs a change beside the version it replaces: a second
environment from a baseline revision, the same golden branched for both, the
same requests in the same order, and every difference in what came back and in
what ended up in the database. Responses and database contents are compared
completely; events, outbound effects, traces and query plans are not compared at
all, and everything the comparison declined to look at is printed on every run.

**`af explore`.** Sends agents at a goal with no declared workflow. They read
each page through the accessibility tree and report where the application cost
somebody effort without failing: a control that did nothing, a page with nothing
left to try, a route that loops back, an element with no accessible name, a step
slower than the goal allows. Every choice comes from the goal's seed, so each
result carries the command that replays it. `af explore --emit-workflow` prints
the `workflows:` block that turns a discovery into a check.

**`af fidelity`.** An inventory of what an environment reproduces and what it
does not, across the services, the data, the third party hosts, the personas,
the runtime and the traffic. Nothing is estimated: a component whose state could
not be determined is reported as unmeasured, excluded from the headline and
named with the reason, and when nothing could be measured there is no score
rather than nought percent. `fidelity.require` names dimensions that must be
fully reproduced.

**Load.** `af load scenario` runs declared journeys: an ordered list of requests
with waits between them, parallel blocks so the second submit arrives while the
first is in flight, and assertions over what came back. `load.source: otel` reads
the traffic mix out of an OpenTelemetry trace export on disk, and because a span
carries a duration the shape arrives with production's own p95 per route, which
is the baseline `p95_increase` compares against and which nothing could provide
before. A scenario assertion reports what it measured as well as what it
concluded: `af load scenario -o json` carries `measure`, `scope`, `threshold`
and `observed` on every assertion result, because a dashboard cannot chart
"served a p95 of 240ms, over 200ms" without parsing English. An assertion whose
requests were never sent reports no observation at all rather than zero.

**A `warn` verdict.** A run can come back with a real finding about the change
that does not fail the check. A new `policy` block decides which class of
finding warns and which fails, with `ignore` for the ones a project does not
want. `blocked` keeps its meaning exactly: the runner could not evaluate this,
it exits zero, and it never counts against the change.

**Cost control.** An environment now has a lifetime, and `af env reap` destroys
the ones that have outlived it. It refuses to pull an environment out from under
a run that is using it.

**On a pull request.** Antifailure runs on a pull request and reports there
itself. One check run per commit, named `Antifailure`, so a branch protection
rule can require it, and the name is stable on purpose because changing it would
silently un-require the check on every repository that named it. Seven states,
and blocked and unverified are not passes: GitHub's `neutral` conclusion reads as
nothing to say and lets a required check pass, so a pull request whose agents
never ran would have merged behind a green tick. One comment per pull request,
edited in place, whose first line carries the commit it is about, because a
stale result the reader cannot detect is worse than no result. There is no
repository secret to paste: the job proves who it is with a GitHub Actions
workflow identity token and exchanges it for a credential scoped to one commit
and one run, expiring within the hour. A fork's commit gets no credential until
a maintainer adds the `antifailure:allow` label, and the next push withdraws it,
because the maintainer approved code they read. The whole surface is fenced
against the orderings GitHub does not promise: the run event arriving before the
pull request event, a job reporting before its check exists, a push during a
run, a close during a run, a reopen during a teardown, and the same delivery
twice.

**A coding agent can rehearse a change and cannot soften one.** `af mcp` serves
four tools over the Model Context Protocol: rehearse this branch's pending
migrations against a throwaway branch of a sanitized copy of production, inspect
what the environment is allowed to reach and what it actually reached, and read
or cancel a run that is still going. It is a thin frontend over the same
orchestrator `af ci` and `af insights` drive, so a tool call and a pull request
check cannot disagree about the same change. The division of authority is a
property of the schemas rather than a request: no tool takes an argument that
disables sanitization, widens the egress policy, lowers a threshold, names a
database or skips the rehearsal, and a field no schema declares is refused
rather than ignored. Verdicts are PASS, FAIL and INCONCLUSIVE, and INCONCLUSIVE
is never a quieter PASS. Statement text never reaches a result, because the
candidate branch is input written by whoever opened the pull request.

**Hosted workloads.** `af workload` runs a hosted workload definition through the
command that names it: an observed load mix through `af load run`, an HTTP
scenario through `af load scenario`, a browser workflow through `af test`, and an
exploration through `af explore`. Every result carries the plain command that
reproduces it, and a knob the plain command has no flag for is refused rather
than dropped, because honouring it would be a promise the run cannot keep. A
hosted run now claims itself, says when it started, says once a minute that it is
still going, and reports what it measured, where before this the engine emitted
nothing and a run started from the console was recorded as abandoned at its
deadline whatever it actually did. A cancel pressed in the console reaches a run
that is already going and stops it, arriving on the heartbeat the engine is
already making rather than on a poll of its own. `af workload teardown` says what
was actually removed and what is still standing, `af workload promote` compiles
an exploration into a workflow definition and lists what the compilation could
not carry over, and `af workload compare` differences two results of the same
kind and states what it cannot control.

**`af start`.** Says where you are on the first run and names the one command
that moves you forward. The first run is nine commands long, any of them can be
interrupted, and coming back used to mean reconstructing the state from memory:
`af init` refuses a repository that already has a manifest, `af up` on a running
environment is a no-op nobody recognises as one, and neither says where you
actually are. It derives every answer from the machine rather than from a record
of what it last did, so tearing an environment down by hand or switching
branches moves the answer with you, and it runs nothing and writes nothing,
which is the only way it can be honest. Each step reports one of four states and
never collapses one into another: done, not yet, blocked, and not checked.

**A run in GitHub Actions reports itself.** The engine's control plane sink took
its credential from `AF_CONTROL_PLANE_TOKEN` and nothing in any workflow this
project ships has ever set it, so on a runner the sink was never built at all:
the events saying an environment is coming up, is ready, or has been torn down
went to the local log and no further, and the dashboard stayed empty for every
run anybody had. A job now proves what it is with the identity GitHub signs for
it and trades that for a short lived credential, so a workflow needs
`permissions: id-token: write` and no repository secret.
`AF_CONTROL_PLANE_TOKEN` still works and still wins when it is set, because a
developer's machine and a self hosted engine have no runner to vouch for them.
The credential is short lived, so the engine renews it when a batch is refused
and re-sends that batch rather than losing it, and events wait on disk rather
than being dropped. Three things that used to read as authentication failures
now say what to do: GitHub declining to mint an identity for a fork's pull
request, a control plane too old to offer the exchange, and a repository the
control plane has not been told about.

**Two more gates over what the documentation claims.** `reference/api.md` is
checked against the routes the server registers, in both directions: every
documentation URL on one of this product's own hosts has to match a route the
server registers or a page the console serves, and every route the server
serves has to be covered by a pattern the page names. It was the only reference
page with no machine coverage and it is the one that drifted. Separately, every
environment variable the engine names at a user is now checked against the
documentation, which is what `af license install` needed: it tells a paying
customer to set two variables and points them at a page that named neither.

**`af ci --report-json`.** The same run written as JSON, for something that has
to act on the report rather than display it. `--report` stays Markdown for a
person, and `-o json` is not that flag: it is the whole terminal's format, so a
continuous integration step showing progress to somebody watching the job would
have had to give up one or the other.

**Hosted control plane.** Billing through Stripe: customers, subscriptions,
invoices, a checkout session, the customer portal, and the webhook that moves
`organizations.plan`, which is what `PLAN_QUOTAS` and `checkQuota` have always
been pointed at and never been able to reach. Billing is off unless every one of
its four variables is set, and a partly configured one is reported as off with
the missing names. Every billing table has row level security enabled and
forced, and signatures are checked over the raw body before anything is parsed.

The console can act rather than only report: an egress rule waits for approval
instead of enforcing the moment a member proposes it, create environment, run
agents and run load dispatch your own workflow in your own repository, runtimes
can be registered and removed, and the plan can be read and set. A break glass
command sets a role directly in the database for when the GitHub App is gone and
nobody inside the organization can act, writing an audit entry carrying the
reason and refusing to leave an organization with no owner. The first member of
an organization becomes its owner when GitHub confirms they administer it.

**Running an organization without emailing anybody.** Settings holds the display
name, the billing contact that decides where invoices go, every live session
with a way to sign any of them out, a complete copy of everything this control
plane holds, and the way to delete the organization. Members gains invitations,
so a finance person or a contractor who is not in your GitHub organization can
join through a link. Deleting an organization is a state machine rather than a
cascade and runs in one order: what is running is torn down and nothing new can
be started, the Stripe subscription is cancelled at the end of the period you
have paid for, nothing else happens until that period ends, credentials and the
GitHub App installation are revoked, a complete export is produced and you are
given a link to it, and only then is anything deleted. Every step is recorded as
it happens, so a deletion that is interrupted picks up where it stopped, and it
can be called off at any point before it finishes. Closing your own account
erases your name, address, identity and avatar, removes your memberships and
signs you out everywhere. It is called closing rather than deleting because the
audit log is a hash chain and keeps what you did under the name you had at the
time. Five new permissions carry all of it, and `account.close` belongs to every
role, because leaving is about you rather than about the organization.

**A plan gate, for a hosted service that needs one.**
`AF_HOSTED_REQUIRED_PLAN=enterprise` refuses every operational procedure until
Stripe grants that plan. It is unset everywhere except Antifailure's own hosted
service, so self hosting is unchanged, and setting it while billing is off stops
the process at startup rather than serving a deployment that refuses every
request and offers no way to pay. The exits are the part worth stating plainly:
a lapsed plan still permits billing, exporting the organization's data, deleting
the organization, closing an account, and listing and revoking sessions. That
last one is a security action rather than a convenience, because a credential
can leak while a subscription has lapsed and a paywall in front of session
revocation would leave somebody unable to contain it.

**An operator who can see every tenant, and the wall that keeps the application
out of it.** Answering "why did this account's run fail" has needed somebody who
can look at another organization's rows, and nothing in the schema allowed it,
so in practice it would have happened through a shared password at a psql
prompt where nothing is recorded. `antifailure_admin` is that access made
explicit: a separate database role with BYPASSRLS that reads widely and writes
narrowly, holding INSERT and SELECT on the audit log and never UPDATE, so an
operator cannot rewrite the record of what operators did. The application cannot
be granted its way in, because reaching the role means opening a connection with
a password the application process is not given, and a test asserts that nothing
has been granted membership and that SET ROLE into it is refused. Impersonation
is recorded on the session row, where the code that resolves a session on every
request cannot miss it, and a check constraint makes the rules structural: the
four columns are all or nothing, a blank reason is not a reason, and the row
must carry the sequence number of the audit entry that authorised it, so an
impersonated session that was never audited cannot be represented at all.
Support notes are not tenant data, and the application role holds no grant on
that table.

**Operations.** A status page checked from GitHub Actions rather than from the
control plane, so an outage cannot silence the page reporting it. It watches
seven components across production, the public site and staging, each one its
own line because each one has its own way of failing while its neighbours are
fine, and every static check asserts a marker in the body as well as the 200,
because each of the failures this project has actually had would have read as
healthy to a check that looked only at the status line. Every figure is computed
from the record: a percentage is described as the share of checks that passed
rather than as uptime, a ninety day figure is only called that once the record
reaches back ninety days, and a day with no readings is drawn in the neutral and
never counted as a day that was up. No state reaches a reader as colour alone.
Subscribe is an Atom feed generated from the same data, because a button that
did nothing would have been worse than no button. A disaster
recovery drill on a weekly schedule that reports the recovery time it measured
and holds it against a budget. Eleven Azure Monitor rules and an action group
where there were none, each naming its runbook in the notification it sends. An
on call page, a manual rollback procedure, and a rotation runbook for every
secret in the control plane's Key Vault.

**The public site.** antifailure.dev, its documentation, and the four legal
documents a security review asks for by name: a data processing agreement, the
subprocessor list, a statement that there is no service level agreement, and
retention and deletion commitments. Every subprocessor was established by
reading the code that talks to the vendor. No lawyer has read any of it and
every page says so on its face. About and Contact are built on the same page
components as the other company pages. Contact names only routes that were
checked against the live repository: private vulnerability reporting, the issue
chooser, Discussions, and the waitlist. There is no postal address, no telephone
number and no email, because the domain has no mail exchanger and its SPF policy
authorizes no sender, so an email route on the page would be a channel that
silently swallowed whatever was sent to it.

**Published machine readable artifacts.** `https://antifailure.dev/openapi.json`
and `https://antifailure.dev/errors.v1.json` are served at the apex, so an agent
that guesses at either address finds it. The OpenAPI document is generated from
the router, validated before it can be committed and pinned to the revision that
built it, rather than proxied from the production control plane at request time:
the site deploys on every push to main and the hosted control plane moves on a
release promotion, so a proxy would have served the pre-promotion document while
the site's own documentation described the new one. The deploy checks both
documents byte for byte against what the run built, because a stale document is
a perfectly healthy 200.

**A published changelog.** `https://antifailure.dev/changelog`, built from the
entries in `.changes/` that this repository has been writing since its first
week and that nothing had ever rendered. The entries marked internal stay in the
repository and are never published: they are real changes with nothing a user of
Antifailure could observe, and a changelog full of them teaches a reader that
the changelog is not about them. A date is the day an entry landed on the main
branch, read from the commit that brought it there rather than typed, and
nothing is backfilled, so `v0.1.0` and `v0.1.1` carry no entries and the page
says why rather than filling them in with work that plausibly shipped in them.
A change to anything a user can see is refused by CI unless it says what
changed, with a `Changelog-None:` trailer carrying a reason for the genuine
exception, which leaves the reason in the history where the next person finds
it.

### Fixed

- AF-EE-004 and AF-EE-010 are on the errors reference page. Both ship and both
  were marked as reserved for a feature this version does not have, so somebody
  refused by single sign on or by organization policy searched the reference for
  the code they had just been shown and found nothing.
- A golden refresh says what it is doing on the event stream. Six `mask.*` types
  were in the catalog and on the reference page and nothing emitted one, so
  dashboard mode drew an empty pane through the longest part of a refresh. A
  failed verification now puts every finding on the stream rather than only the
  first one the refusal names.
- The waitlist form on antifailure.dev dropped every address for two days,
  because the deploy published a site with no API and the platform removed the
  managed function a hand deploy had put there. The deploy publishes `api/` now
  and refuses to finish green unless the endpoint answers.
- Every security header the site's own configuration declares was being thrown
  away before publishing. The site now serves the two year HSTS with preload
  rather than the platform's 126 day default, a permissions policy, a cross
  origin opener policy, and immutable cache headers on hashed assets, and
  `/product/crowdi` redirects instead of answering 200.
- The production runbook prescribed a GitHub App permission and four webhook
  events nothing in this repository uses, and was missing the one the console's
  dispatch verbs need.
- The documentation shipped two of the eight head tags it was written to carry,
  on all 76 pages. `just docscheck` is the gate that would have caught it.
- The console was built one page at a time and the seams showed: four heading
  sizes, four primary buttons, four radii where there is a scale of three. That
  is one vocabulary now. Its tertiary grey measured 3.2:1 and is 4.6:1. Every
  list stacks into one record per row below `sm` instead of hiding the two
  columns a reader came for behind a horizontal scroll, rows are reachable by
  keyboard, and nothing pulses.
- Documentation code blocks were painted at 1.12:1 against the page and had
  neither a visible surface nor a visible edge. Every control on a phone is now
  at least 44px and no page scrolls sideways at 375px.
- `af explore` could not produce a report on any machine. The engine marshals
  the runner's job document from Go, where a nil slice becomes `null`, and the
  exploration path never sets `workflows`, so the runner read
  `doc.workflows.length` before it looked at the goals and every run exited
  `AF-AGT-003` with a TypeError and no output. Nothing anywhere drove the
  runner's entry point: both halves had green suites and the document between
  them was never sent by a test.
- Pressing control C twice did nothing. Both binaries called `WithSignals`,
  whose comment said in the present tense that a second interrupt forces an exit
  with the journal intact, and both discarded the return value that said one had
  arrived. `cli.Run` waits on it now, and stopping there is safe because every
  resource is journaled before it is created, so `af down` still knows what to
  remove.
- `af init` wrote a header saying every value came from a file, and directly
  beneath it two personas and a `sign-up` workflow that came from nothing. The
  guesses are disclosed where they appear now, and the disclosure is seeded from
  what the draft holds before any question is asked, so a value nothing asked
  about can still be disclosed. They are still written, because nothing in a
  repository proves the absence of authentication and dropping them would trade
  a loud failure for a silent wrong answer.
- The refusal a new user is most likely to meet carried no code. `af test` on a
  freshly initialised repository stopped with "no users table could be found, so
  there is nowhere to create a persona", with no `AF-` number, no next step and
  no link, one command after a refusal that has all three. It is `AF-DB-022`
  now, and it names all three ways out rather than two. A persona whose `login`
  is `none` no longer requires a users table at all, which is why
  `examples/go-api` could not be run until now.
- Three error remedies named a command that does not exist, and the most durable
  of them was written to a file on disk by `af init` rather than printed once.
  There is no `af down --all`; the command that removes every environment on the
  machine is `af env prune --older-than 0`. The test that existed for exactly
  this class was green over all three, because its pattern only matched a
  command at the start of a line and every remedy on the generated errors
  reference sits in a sentence. It reads inline code spans now,
  and a third sweep parses the engine's own string constants through
  `go/parser`.
- `ListGoldens` reported every golden as verified, including the ones that were
  not, so `af up` branched an unverified golden exactly as if it had been
  checked, with nothing printed anywhere. `RefreshGolden` recorded the honest
  value and the read overwrote it. The verified state is read from the
  attestation label now, which is only ever written by a verifier that ran and
  returned.
- Seven fields the egress sidecar records on every request reached no surface at
  all. `substituted` is the one that matters, because it answers the question
  the sandbox exists to answer and without it a row that reached a real service
  and a row that reached a sandbox were the same row. `af net log` and
  `af net log -o json` now carry `substituted`, `host_only`, `via`, `duration`,
  `seq`, `waited_ms` and `limit`, and it is reported as `false` rather than
  absent when no swap happened, because a key that disappears makes "the
  credential was not swapped" and "this build cannot report swaps" the same
  document. Two paired tests fail on a fact recorded with nowhere to go and on a
  published key nothing assigns.
- The runner's dependencies were resolved fresh on every machine. Release
  archives shipped `runner/package.json` and no lockfile, established by
  downloading the published v0.1.1 archive and listing it, and `af runner
  install` ran `npm install`, so `playwright`'s `^1.49.0` became whatever the
  registry served that day and two people installing one release drove their
  tests with two different browsers. Archives carry the lockfile now,
  `af runner install` runs `npm ci` when one is present, `af runner check`
  reports an unpinned tree as a warning rather than as the same ok a pinned one
  gets, and `tools/relpack` runs the real release build and asserts what is
  inside the archive.
- The README told you to install Antifailure and run `af init` and never
  mentioned `af runner install`, so the one dependency a reader had to know
  about was the one the front page left out, and the quickstart stopped at a
  running environment without ever showing a verdict. Both cover the whole path
  now and `tools/walkthrough` walks it.
- `examples/github-workflow.yml`, the file the documentation tells you to copy,
  ran `af ci --output report.md` after that flag had been renamed to `--report`,
  so the copied workflow exited 2 on "the output format is not recognised"
  before `af ci` did any work and nothing brought an environment up. The
  generated command reference moved with the rename and the example did not, so
  the two descriptions of one command disagreed and only the one nobody copies
  was right. It also never set `AF_CONTROL_PLANE_TOKEN`, so a repository
  following it sent no engine events at all and the console's environment list
  was fed by nothing. The gate that checks documented commands reads the
  workflow files under `examples/` and `.github/workflows/` now, and parses each
  invocation through the same validation the binary runs, so a flag that exists
  and refuses its value is a finding rather than a pass.
- The manifest reference says which of `github.mode`, `github.comment`,
  `github.fork_policy` and `github.teardown_on` anything reads, and why the
  hosted control plane never reads your manifest. A test fails if one of those
  fields gains a reader without the table being corrected, because somebody
  wiring a setting up and leaving a page that says it does nothing is the more
  dangerous half.
- Runs, Environments and Audit showed the first page of a list and presented it
  as the whole list. All three routes paginate and each page read none of the
  cursor, so an organization with 200 runs saw 50 in a table that looked
  complete, which is worse than a screen that looks broken because the reader
  acts on it. Each list pages now, and its footer says which of the two things
  is true.
- Refreshing a screen in the console blanked it to a skeleton, and a refresh
  that failed replaced correct data with a full page error. A reload keeps what
  is on screen now, reports that it is refreshing, and puts a failure in a strip
  above the content rather than over it. Both hooks carry a request sequence, so
  of two reloads in flight the older answer cannot overwrite the newer.
- Every loading skeleton in the console was zero pixels wide on a phone. A
  percentage width inside a shrink to fit box has no basis to resolve against,
  so every bar in `TableSkeleton`, on every page that uses it, rendered as a
  stack of empty boxes at 390px while measuring 54.8 to 168.2px at 1280px.
- Every route in the console had the same document title. All ten rendered
  `Antifailure` while their headings read Environments, Runs, Audit and the
  rest, so every tab, every history entry and every tab search result was the
  identical word in a tool people keep open in several tabs while a run goes.
- Following a link into the console while signed out lost the page you were
  going to, so every deep link this product publishes, including the environment
  link in a pull request comment, was a link to the front door. Both ways in
  carry the return path now, query string included.
- The console's teardown button set a column and nothing anywhere read it. The
  containers kept running while the console said they were gone, which is worse
  than the button not existing because somebody who saw "torn down" stopped
  looking. Teardown is a durable request with a lease, an attempt count and an
  acknowledgement now, and the row moves only when the runtime confirms it.
  Where there is no route into the machine at all, the request is given up on
  and says so, naming `af down`, rather than reporting a cleanup that never
  happened.
- The console's environment, agent and load controls put GitHub's raw JSON on
  the screen when a dispatch was refused, beside a sentence naming three
  possible causes at once. A refusal now says which one it is and carries its
  own remedy, and the check runs when a repository is chosen rather than when
  the button is pressed, so a missing permission is visible before the form is
  filled in. The permission behaviour was documented backwards: a missing
  `actions: write` is a 403 rather than the 404 the documentation claimed.
- Accepting new permissions on the GitHub App left the control plane using the
  token it had already minted, which still carried the old scopes, so the App
  went on refusing writes it had just been granted for the rest of that token's
  hour. `InstallationTokens.forget` had existed since the App client was written
  and had no callers anywhere in the tree. An `installation` delivery drops the
  cached token now, for every action rather than for one, and a call GitHub
  answers 401 drops it and retries once.
- Three ways an organization could exist that nobody could ever enter, each of
  them rendering the empty state that means nobody has installed the App to
  somebody whose App is installed. Signing in and installing are two events with
  no guaranteed order and only one order worked, and the flow the product
  recommends produces the other one. An App installed on a personal account
  created an organization keyed on the holder's login, which `/user/orgs` never
  returns. And `/user/orgs` was read one page deep, where it defaults to thirty,
  so the list deciding which organizations somebody may enter was truncated
  rather than shortened.
- A hosted run that did not pass says why on the line a person reads first. A
  load run that breached a threshold reported an empty detail, so the reason
  lived only in the threshold rows. Two siblings had the same gap: an
  exploration that missed a goal said nothing, and a failing scenario or
  workflow whose own reason was empty rendered as a name, a colon and nothing.
- The control plane answers every event it stored and could not apply with a
  sentence saying why, and the engine threw all of them away: the wire type had
  no field for the note, so the decoder dropped it, and the only caller of
  `Send` discarded the whole result. That is the one channel that explains why a
  run reported and the console still shows nothing. Those sentences reach the
  job log now, bounded and without duplicates.
- An unexpected control plane failure told the caller to find their request in
  the logs and gave them nothing to find it with: no identifier in the body,
  none on the response, and deliberately no query, no parameters and no payload
  in the log. Every request carries an `x-request-id` now, the 500 body repeats
  it, and the log line carries it beside the error's class and the driver's
  code, while the statement and its parameters still reach neither the caller
  nor the log.
- The control plane's OpenAPI document described an API a generated client could
  not call. Every query said its input was optional, including the routes that
  answer 400 without one; every mutation demanded a body shaped exactly `{}`
  even where the route reads no input; one `Error` shape stood for three
  different bodies and validated none of them, so a client parsing the readiness
  503 read `undefined` and reported the service healthy; and the event type was
  published as a closed enum while the server deliberately accepts and stores a
  type it has never seen. All of it is taken from the validators the router
  executes now, and a test drives real requests through the real HTTP boundary
  and checks each answer against the schema declared for that status.
- The runtimes table on Environments, the approval queue on Network and the plan
  comparison showed no column headings on a phone, while eleven of the fourteen
  tables beside them did. At 390px the approval queue read as six bare values on
  the screen where somebody approves an egress rule.
- Every documentation address the product prints or publishes names the URL the
  site actually serves rather than a spelling it answers with a 301. That
  affected the `More` link under every error code, which is the address the
  engine prints when something has already gone wrong for you, the documentation
  pages' own canonical tags, and every URL in the sitemap.
- The copy button on every terminal code block in the documentation sat outside
  the block, hanging off the bottom right corner over the paragraph beneath and
  taking its tooltip with it. The install command on the quickstart
  was the first one anybody saw.
- Every h2 in the documentation pushed its anchor link onto a line of its own,
  67px below the heading, on every page. Cascade layers are why the override
  that caused it was silent: Starlight ships its CSS in a layer and this site's
  stylesheet is unlayered, so a plain selector beats a layered rule whatever its
  specificity and reading the diff gives the wrong answer.
- The markdown twin of every page, which is what an answer engine reads instead
  of 300KB of markup, dropped every table cell and every definition list. The
  masking table on the safe state page and the manifest walkthrough on the
  overview page are the most specific claims either page makes, and an assistant
  could see that the page discusses masking and could not see a single rule.
- The home page was built for a phone and for a wide desktop and rendered as
  neither in between, because every layout decision on it switched at one
  breakpoint. At 1100px, which is a laptop rather than an edge case, two of the
  hero's five service cards sat outside the viewport with no affordance at all,
  the art behind the headline ended in a torn horizontal edge lying across body
  text, and every section heading was sliced in half by a rail pinned at the
  wrong offset. No page on the site scrolls horizontally at any width now,
  checked across every exported route at ten widths against the static export
  rather than against the development server, which serves none of the hero art
  and would have measured a torn edge as clean.
- The assistant panel on the home page was a drawing of an application that a
  keyboard could walk into: an unlabelled reply box with no focus ring that was
  tab stop 51 of 119, a Send button rendered 9px square, and window chrome under
  7px. The parts that only depicted an assistant are drawn rather than operated
  now, and what stays interactive is the part that is a real demonstration.
- Every interactive target in the footer measures at least 44 by 44 at every
  width, and on a desktop the footer renders as it did before. Its navigation
  links could not take the treatment the other targets took, because
  they sit flush and each 44px box would have overlapped its neighbour by 15px
  with the later sibling painting on top, which a screenshot would not show and
  a measurement would have called a pass. The rhythm grows under a coarse
  pointer instead.
- The breadcrumb trail failed the contrast floor on every page of the site that
  renders one, at 3.85:1 on the paper ground, 4.13:1 on white and 3.55:1 on the
  sage bands. The scale already held a passing grey one step away.
- The three claims under Isolated Twin on the home page, the ones that say the
  twin has no route out, were set in a grey measuring 3.85:1 and stepped down to
  14px below 1024, missing two floors this project sets for itself on one line.
- The trust band at the foot of the home page was a flat saturated olive that
  nothing else on the site used, and its grey headline, its two captions and its
  attribution all measured 4.10:1. It is the same pale sage every other section
  band uses now, which puts those three at 5.10:1.
- The status page's component rows had two different heights on a phone and
  nothing about a component decided which it got, down a list whose whole job is
  to be scanned in one pass. Its Day, Week and Month control was the only one on
  the page between the 24px floor and the 44px its subscribe button already had.
- The published retention numbers are read from one place and compared to the
  Terraform that sets them by a test. Seven legal claims were found false in one
  night and every one of them was true when it was written, so the fix is a gate
  rather than seven edits: a data subject was told their data is gone after
  fourteen days of point in time recovery while production runs thirty five, and
  the privacy and subprocessor pages said there is no billing and that nothing
  can send mail, which stopped being true when the billing and sign in link work
  landed.
- The runs list, the verdicts view, the goldens quota, the masking attestation
  table and the compliance pack's masking control were blank for every real
  customer. All five read `golden_versions`, `runs` and `verdicts`, and the only
  writers of those three tables anywhere in the repository were the test harness
  and the staging seeder, which fills all of them, so every one of these looked
  correct in development and had nothing to show in production. The compliance
  pack was the worst of it, reporting that the check ran and found nothing for an
  organization producing a signed attestation every night. The engine emitted the
  events, the sink mapped them and ingest accepted the types, so nothing anywhere
  reported a problem; the projections were simply never written.
- Four documents told a self hoster to point GitHub at a URL this product does
  not serve. `AF_GITHUB_REDIRECT_URI` was documented as `/auth/callback` in the
  getting started path, the self hosting page, the Azure page and the README, and
  the route is `/auth/github/callback`, which is what the product's own Terraform
  configures. The value goes straight to GitHub and nothing validates its path at
  startup, so the failure landed at the end of the first sign in, after the
  operator had registered an OAuth App carrying the same wrong URL.
- Four places where the documentation described a command the product does not
  have, including the operations runbook telling an on call engineer in bold not
  to run `af down --all`, which is not a flag, and a nine line `af provider list`
  session whose every line was wrong.
- The enterprise licensing page told a customer to install a license with a
  command that stores nothing. Neither binary installs anything: the enterprise
  entry point reads `AF_LICENSE_KEY` and `AF_ORG` from the environment and that
  is the whole mechanism, and neither variable appeared on any published page.
- The API reference named eight routes fewer than the server serves. Three whole
  families were absent: both webhook endpoints, the two model proxy endpoints,
  and the four console provider routes. The model proxy is the mechanism two
  guides describe in full, so the page listing the surface omitted the thing
  those pages tell you to point your client library at. It also claimed
  everything below was authenticated apart from a counted number of exceptions,
  which the webhooks break in a way counting cannot fix: they accept a signature
  rather than a credential.
- The documentation home was the only page in the documentation with no way to
  see what else exists. It carried Starlight's marketing template, which turns
  the sidebar off, and below 800px it had no menu button either, so on a phone
  the page a new reader lands on had no navigation at all.
- A third of the documentation sidebar was ordered by file name rather than by
  anybody's decision. Starlight breaks a tie on `sidebar.order` with the slug,
  and 27 of the 78 ordered pages shared a number with a sibling, so "Watching a
  run" split the pair of runtime pages, "Provider limits" split the three
  provider pages, and "On call" came before "Standing up production".
- Two sidebar groups were labelled with a raw lowercase directory name among
  nine sentence case labels, and one of them contained a page whose own title was
  the same word in a different casing.
- The documentation offered no way to sign up on a tablet. Between 800px and
  1023px no page carried a Log in link, a Sign up button or a link to the
  repository: the header hid all three at one breakpoint and the mobile menu
  holding their only other copies exists only below another, so for 224 pixels
  of viewport width they were hidden with nowhere to go. iPad portrait is 810 to
  834 pixels.
- The documentation build warned on every run that `/404` was declared twice,
  because Starlight injects one at the same pattern as the site's own. Astro's
  message says a route collision becomes a hard error in a later version. The
  site's own page wins, and it earns it: somebody who lands there arrived from a
  path printed at the end of an engine error message rather than from a link, so
  it names the references they were most likely reaching for.
- The hero shader on the marketing site ran on the CPU on any machine whose GPU
  driver is blocklisted. The guard was binary, and a blocklisted driver is
  neither case: the browser grants a context backed by a software rasteriser
  instead of refusing one, so nothing threw and a full bleed fragment shader
  animated on the CPU for as long as the page was open. That is the normal state
  of a cheap or out of support Chromebook, and it was reported as the site
  crashing them.
- `af net log` left `via` blank on tunnelled CONNECT, which is most of the
  traffic. Three of the proxy's four recording paths said how the request
  reached it and the tunnel every HTTPS request opens said nothing, so the field
  was empty on the majority of records while being populated on the minority. An
  empty field that looks like a value is worse than a missing one, because it
  reads as data rather than as an omission.
- An organization deletion claimed its step after doing the step's work, so a
  second caller did all of it before being told it had lost. Every step's write
  was supposed to carry a `WHERE <its timestamp> IS NULL` so two callers arriving
  at once do not both act, and that was true of the bookkeeping row and false of
  the three statements that do the work. It was not reachable as data loss,
  because each statement locks what it touches and re-evaluates after the winner
  commits, and the ordering is now what the comment above it always claimed.
- The cross platform lint could not typecheck one test file on Windows. It
  carried a `runtime.GOOS` skip, which reads as though the platform had been
  handled and cannot help, because the skip runs at run time and the missing
  symbol is a compile error, so the whole package failed to typecheck.

- `af explore` runs. It never had. The engine built the runner's job document
  with a nil Go slice for `workflows`, which marshals as `null`, and the runner
  read `doc.workflows.length` with no guard, so every exploration on every
  application died with a TypeError before the browser opened, and everything
  downstream of it including `--emit-workflow` had therefore never run either.
  Both sides compiled and both typechecked; they disagreed only on the wire.
  The engine now sends `[]`, strict on the write, and the runner tolerates a
  null or absent list, tolerant on the read.
- Expired sessions are deleted. The sweeper had removed zero rows, on every
  instance, for as long as it existed. It ran on a connection with no tenant
  and every policy on that table keys on a declared value, so its DELETE
  matched nothing and reported success, because a statement that matches
  nothing does not raise. Housekeeping now has a role of its own, entered for
  one transaction, in which a cutoff passed in can only narrow the sweep and
  never widen it.
- The sign-in and sign-up pages no longer advertise a markdown file that
  answered 404. Every page carries a `text/markdown` alternate pointing at its
  own address, and the generator deliberately skips anything marked `noindex`.
  Those two pages are the only ones the site marks `noindex`, so they were the
  only two promising a file the build never wrote. The check now asserts that
  every twin a page advertises is a file the build actually produced.
- `af runner install` finds the runner from anywhere inside a checkout rather
  than only from its root. It searched the working directory and its parent,
  which assumed those were the same place. Run from a subdirectory it reported
  that no runner source existed while standing inside a checkout that had one,
  and told the reader to install a runner they already had.

### Security

- The release archive had no `af` in it. Both callers of
  `tools/release/build.sh` pass the output directories relatively and the
  script builds the binary from `engine/`, in a subshell, so the linker's `-o`
  resolved against `engine/` rather than against the directory the script had
  just made. The binary landed outside the staged tree, the archive was
  assembled without it, and the archive, its checksum and the exit code were
  all exactly what a working build produces. `tools/relpack` exists to assert
  what is inside the archive and could not see it, because it passed absolute
  paths, which is the one shape neither caller uses and the one shape that
  makes the defect impossible. It builds from inside the tree with a relative
  path now, and was watched failing on the unfixed script before it was
  believed.
- Release archives are reproducible and proven to be. Two builds of one commit
  produced four different archives every time, because `tar` takes each entry's
  timestamp from the filesystem and `gzip` writes another into its own header.
  The old check compared `bin/af`, which always matched. `tools/reltar` writes
  the archives now and the check builds twice and compares the archive.
- The bill of materials described nothing. It was generated from a directory of
  `.tar.gz` files, which the generator does not open, so every release would
  have carried a valid SPDX document listing one package instead of the 363 in
  the binaries. `tools/sbomcheck` now requires it to record the SHA256 of every
  binary that ships.
- Signing could not have worked at all. The pinned installer supplies cosign v3,
  where `sign-blob` refuses to run without `--bundle`. Releases sign a bundle
  now, and the workflow verifies both signatures and requires a copy with one
  byte changed to be rejected before it publishes anything.
- The egress sidecar refuses to open a loopback, link local, private or carrier
  grade address on the environment's behalf unless a rule names it, which closes
  the instance metadata endpoint under `default: allow`. It answers non-address
  DNS queries for external names itself, takes a transparent connection's port
  from the listener rather than from the client's `Host` header, and enforces
  `egress.allow_ipv6`, which nothing read.
- `npm audit` runs against every lockfile beside govulncheck, on every pull
  request and every morning. govulncheck reads Go modules and stops there, and
  every `npm ci` in CI passes `--no-audit`, so seven lockfiles, one of which
  builds the control plane, had no advisory check at all.
- `SECURITY.md` stops claiming three things the evidence does not support, and
  the disclosure section now says what happens when a target is missed and that
  there is no bug bounty.
- `af up` branched another project's golden. A golden pool is shared on purpose,
  and selection filtered on the masking rules digest, which says how a golden was
  masked and not whose it is, so every project on a machine with no
  `masking.yaml` hashed to the same value and drew from one pool. Reproduced
  with two ordinary Express repositories: the second declared no database,
  printed in its own generated manifest that branches would start empty, and
  `af up` brought up a database holding the first project's tables and its rows.
  A golden now records the project it was made for and an environment branches
  only a golden whose record equals its own, `af golden pull` refuses a
  published golden made for another project before restoring anything,
  `af golden gc` collects only this project's versions where it used to delete
  another repository's, and every run says where its data came from.
- A sandbox rule whose credential was never configured sent the application's
  own credential to the provider. Substitution only happens when a value exists
  for the name the rule refers to, and when none did the sidecar forwarded
  whatever the application sent, indistinguishable in every other column from a
  working sandbox call. `inspect_egress_firewall` reports it as
  `sandbox_credential_not_substituted` and it always fails the check, and the run
  report states the substituted count either way and names the hosts a request
  reached carrying the application's own credential. Stating it either way is
  deliberate: a line that appears only when something is wrong teaches a reader
  nothing by its absence. There is
  deliberately no way to turn it down: a threshold expresses how much of
  something a project will tolerate, and there is no tolerable quantity of a
  live credential leaving an environment that is running unreviewed code against
  a copy of production data.
- A suspended organization could still be issued a callback credential. The
  suspension was read at `/v1/events` and nowhere else, so the control plane
  minted a working credential for an organization it had stopped and then
  refused the report that credential was minted for. Nothing crossed a tenant
  boundary and no events were accepted, which is why it went unnoticed: the
  outcome was correct and only the explanation was wrong, and a customer went
  looking at their continuous integration when the answer was their billing
  state. The refusal happens where the credential is minted now, naming the
  suspension and the recorded reason.
- On macOS every secret this product stores went through a child process's
  argv, where any other user on the machine could read it in `ps`. A control
  plane bearer token was read that way on this project's own machine, by
  accident, by somebody looking at something else. `af login`, `af model set`,
  `af secret set` and `af provider` all reach it. The product had already decided
  this matters: `af model set` refuses a `--key` flag and reads without echo for
  exactly these reasons, and then handed the key to a function that put it on a
  command line one process deeper. The value goes in on stdin now, and every
  call to `security` is bounded by a timeout, because a keychain that blocks
  rather than failing has no upper bound and the fallback to a file cannot fire,
  since a hang is not an error. Linux was already correct and Windows starts no
  child process at all.
- `github.fork_policy` was sold as a security control and refused nothing. The
  schema said the default requires a maintainer to add `antifailure:allow`
  first, the pull request guide said nothing runs until they do, `af explain`
  printed that forks never run, and `af up` on a fork's pull request answered
  with an environment name and went to the Docker daemon. What customers
  actually had was GitHub's own default, which withholds secrets from a fork's
  job on a GitHub hosted runner: real, and not this control, and it does nothing
  on the self hosted runner that is the ordinary shape here. The refusal is real
  now, it reads the policy from the base branch rather than from the fork's own
  checkout, and `af ci` writes a report saying the check did not run rather than
  exiting non zero, because a fork waiting on a maintainer is not a finding
  about the change.
- A verified GitHub delivery is handled exactly once, however many times it
  arrives. The HMAC over the raw body says a delivery is genuine and says
  nothing about it being new, so a delivery captured off the wire, or replayed
  out of GitHub's own redelivery log, verified exactly as well the thousandth
  time as the first, and every handler downstream of that endpoint writes
  something. Each delivery is claimed by its identifier before it is handled and
  stamped after, a copy arriving while the first is still being handled is
  answered 503 with a `Retry-After` rather than a success it has not earned, a
  handler that throws gives its claim back, and a delivery with no identifier is
  refused rather than handled unfenced.
- `install.sh` verified the download on the happy path and passed on every
  unhappy one. A `checksums.txt` that did not download printed a warning and
  installed anyway, an archive not named inside one printed nothing at all and
  installed anyway, and a machine with no hashing tool printed a warning and
  installed anyway. Only a mismatch stopped. Each of those refuses now, naming
  what was missing, and `openssl` is accepted as a third hashing tool so that
  refusing costs almost no machine anything. Placement was worse than a fail
  open: the placement step was an AND-OR list, which `set -e` does not apply to,
  so an archive assembled without `af` in it printed a `cp` error, then printed
  that it had installed, wrote the PATH line and exited 0.

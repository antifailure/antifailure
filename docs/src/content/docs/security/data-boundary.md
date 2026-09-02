---
title: The trust boundary
description: What the engine sends a control plane, field by field, what never leaves the machine at all, and the five places the boundary is thinner than the marketing says.
sidebar:
  order: 2
---

The product is sold on one claim: production data stays inside your boundary,
and a control plane receives evidence rather than records. This page is the
code behind that claim, written so a reviewer can check it instead of accepting
it. Every assertion names the file it came from.

It holds on the paths that matter most and it does not hold everywhere. Five
places carry more than the word evidence suggests, and they are named in
[Where the claim is thinner than it sounds](#where-the-claim-is-thinner-than-it-sounds)
rather than left for a reviewer to find.

## The picture

```
┌─ YOUR BOUNDARY ────────────┐
│ your laptop, your own CI   │
│ runner, or your cluster    │
│                            │
│ production database        │
│   │ read by af, from here  │
│   ▼                        │
│ af, the engine             │
│   │ mask, then verify      │
│   │ against one type list  │
│   ▼                        │
│ the golden: a masked image │
│ on this machine's Docker   │
│ daemon, and nowhere else   │
│   │ branch                 │
│   ▼                        │
│ the twin: your services,   │
│ on a network that has no   │
│ route to the internet      │
│   │                        │
│   ▼                        │
│ af-proxy: the only way out │
│   │                        │
└──┬─────────────────────────┘
   │ nothing dials in. every
   │ arrow starts here, over
   │ HTTPS with a bearer token
   │
   │ 1 events
   │ 2 the check report
   │ 3 model prompts, opt in
   ▼
┌─ VENDOR BOUNDARY ──────────┐
│                            │
│ the control plane, which   │
│ is app.antifailure.dev     │
│ unless you run your own    │
│   │                        │
└──┬─────────────────────────┘
   │ 4 a workflow_dispatch: a
   │   request to GitHub, not
   ▼   a connection to you
┌─ GITHUB ───────────────────┐
│                            │
│ which starts af again      │
│ inside your boundary, at   │
│ the top of this diagram    │
│                            │
└────────────────────────────┘
```

## What crosses, and what proves it

A verdict per category, and the file to read. Everything below the table is the
detail behind a row.

| What | Crosses? | Where to check |
| --- | --- | --- |
| Production rows | No | `engine/internal/db/pgcopy/pgcopy.go` |
| The golden | No | `engine/internal/db/docker/docker.go` |
| Credentials | No | `engine/internal/redact/redact.go` |
| Artifacts | No | `engine/internal/workload/result.go` |
| Table and column names | Yes | `engine/internal/env/golden.go` |
| Rows per table | Yes | `engine/internal/env/golden.go` |
| Repository and branch | Yes | `engine/internal/env/identity.go` |
| Build output | Yes | `engine/internal/build/docker.go` |
| Route paths | Yes | `engine/internal/workload/result.go` |
| The reproduce command | Yes | `engine/internal/workload/result.go` |
| Twin database rows | Yes, five | `engine/internal/report/report.go` |
| Unmasked production values | Possible | `engine/internal/masking/rules.go` |
| Twin page text | Opt in | `runner/src/model.ts` |
| Traces | Opt in | `engine/internal/telemetry/otel.go` |
| Analytics, crash reports | None | no client exists |

## What never leaves the machine

Four of those rows say no, and each is a different mechanism rather than the
same promise repeated.

**Production rows.** The engine connects to production from the machine it runs
on, copies it, and masks the copy. Nothing may read the copy until a scan has
read it back and found nothing, because `verifyDatabase` refuses to publish a
golden whose report is not clean. An unpublished golden cannot be branched, so
no environment can hold one.

Read the scope of that scan rather than the word clean, because it is narrower
than it sounds in two directions. It samples rows, up to a per-column limit the
attestation records beside the result. And it reads only the columns whose
`information_schema` type is one of six: `text`, `character varying`,
`character`, `json`, `jsonb` and `xml`.

The masking default reads the same six. That is the same list in two files, so
the two steps are not two controls. See
[masking and its check are the same instrument](#masking-and-its-check-are-the-same-instrument).

**The golden itself.** It is an image committed to the local Docker daemon.
Nothing pushes it. The provider's own words for an empty listing are that there
are no golden versions "on this daemon", which is the whole scope of where one
ever is.

**Connection strings and credentials.** Every connection string is registered
with the redactor at the moment it is obtained, in `branchFrom`, rather than
wherever somebody remembered to. A model key you keep on your own machine is
never sent to a control plane, and a control plane token is read only from the
environment: `TokenFromEnvironment` is deliberately the only source, so that
there is no path by which this code could write one into a file in your
repository.

**Artifacts.** The engine uploads nothing. A trace, a screenshot or a video is
recorded with its path, its size and its hash, and with an availability of
`runner_local`, which the type's own comment explains is there so a console
cannot render a path as a link and send somebody to a file that was never
theirs to open.

## The direction of every connection

Reviewers ask this first, so it is first.

The engine dials out and nothing dials in. `controlplane.New` refuses to build
against any address that is not `https`, other than `localhost`, so a token is
never sent in the clear. It authenticates with a bearer token in an
`authorization` header, not a client certificate, so this is ordinary TLS
rather than mutual TLS. Worth knowing before somebody writes down the stronger
of the two.

The control plane's own comment on the ingestion endpoint says what it assumes:
the events arrive from "developer machines and CI runners that the control plane
cannot reach and does not trust". That is the architecture rather than a
courtesy. `web/apps/api/src/ingest.ts` treats duplicates, reordering and bursts
as the normal case because a sender it cannot reach cannot be asked to behave.

Two things look like inbound paths and are not.

**Starting a hosted run.** A button in the console does not run anything. It
asks GitHub to dispatch a workflow in your own repository, on your own runners,
and GitHub reads the trigger declaration from your default branch. The control
plane needs `actions: write` on a GitHub App installation to do it and nothing
else. `examples/github-workflow.yml` is the file that has to be there, and
`web/apps/api/src/auth/github.ts` enumerates every way GitHub refuses.

**Cancelling one.** There is no command channel. A running engine heartbeats
once a minute, and the answer to that heartbeat carries whether somebody has
pressed cancel. The pause between pressing and stopping is that minute, and it
is the price of having no inbound socket. `engine/internal/controlplane/workloads.go`
says so at the type.

Even the run identifier travels this direction. A dispatch cannot carry an
undeclared input, so the engine claims the run waiting for its environment
rather than being told which one it is.

## What travels on the event stream

Attaching the control plane sink is the whole of the decision, and it is made in
one place. `engine/internal/telemetry/telemetry.go` calls `bus.AddSink` with the
control plane sink and no filter. Every event the engine emits is therefore
offered to it.

That has a consequence worth stating plainly. `engine/internal/controlplane/sink.go`
maps the engine's event names onto the control plane's, and an unmapped type is
sent unchanged so that an older control plane can ingest a newer engine. What
crosses is bounded by what the engine emits, not by the list the control plane
publishes.

Field by field, for the events the engine actually emits today:

* **Environment lifecycle.** The repository as `owner/name`, the branch, the
  pull request number, and the lifetime the manifest declares. The preview URL
  is carried as text and is stored as text. Nothing in the control plane fetches
  it.
* **Goldens.** The version identifier, whether it was verified, a digest of the
  masking rules, the size in bytes, when it was made, and the signed attestation.
  The attestation carries counts and a signature, and its report carries no
  findings, because a golden whose scan is not clean is refused before it can be
  published at all.
* **Masking.** A plan event carries counts of tables and columns. A progress
  event carries one table's name and its row count. A finding carries the
  detector, the schema-qualified table, and the column. The value is not on the
  event.
* **Builds.** One event per line of build output. The line is redacted where it
  is read, in `engine/internal/build/docker.go`, and redacted again on the way
  to the wire.
* **Egress decisions.** The host and the mode.
* **Workload results.** The result document, minus its largest field. `native`,
  the engine's own untranslated result, is deleted before the payload is built,
  because the control plane declined to store it. What is left is the
  measurements, the per-route numbers, the threshold verdicts, the evidence
  locators, and the command that reproduces the run.

## The check report is a second channel

The event stream is what the engine did. The report is what it concluded, and it
travels separately.

The `--report-json` flag on `af ci` writes the run document, and `--report`
writes the same run as the Markdown comment a person reads. The workflow trades the job's
identity for a credential good for one commit and posts both to `/v1/pr/report`.
The control plane reads the JSON for its counts, the environment name, the URL
and the duration, and keeps none of the rest. It does store the Markdown,
truncated, on the generation row, and it publishes it as a check run and a
comment on your pull request.

That Markdown is the one place records cross. See
[Where the claim is thinner than it sounds](#where-the-claim-is-thinner-than-it-sounds).

## Redaction is a credential control, not a privacy control

Everything that leaves passes through one function. `scrub` in
`engine/internal/controlplane/client.go` walks every payload string in a batch,
and its comment says why it lives there rather than at the call sites: both the
live path and the spooled path go through `Send`, and a call site somebody
forgot is how a secret reaches a log.

What it removes is credentials. `engine/internal/redact/redact.go` runs two
kinds of rule: patterns for shapes that are recognisable without knowing the
value, and exact matches for values the secrets subsystem actually loaded, in
plain, base64 and percent-encoded forms.

It does not remove personal data and it does not claim to. A name in a build
log is a name in a build log. The control that keeps personal data out of the
twin is masking, which happens before anything reads the copy. A scan reads the
masked copy back before a golden may be branched, and that scan is a check on
the masking rather than a second, independent one.

## Where the twin runs

On the machine that ran `af`. Locally that is Docker on your laptop; in CI it is
Docker on your own runner; the Kubernetes runtime uses the context in your own
kubeconfig.

The services sit on a network created with Docker's `internal` flag, which
`engine/internal/runtime/local/network.go` picks deliberately: turning off IP
masquerading looks equivalent and is not, because Docker Desktop translates the
traffic again at the virtual machine's gateway. That was measured rather than
assumed, and the test that measures it is in the same package.

If you use a hosted database provider, the branch is created in your own account
with your own API key. `engine/internal/db/neon/neon.go` requires the key and
has no other source for one.

## The engine is not inside the egress policy

A reviewer reading [Egress](/docs/concepts/egress) will ask whether
`default: block` stops the engine reporting. It does not, and the reason is
structural rather than an exemption.

The policy governs traffic through the sidecar. The sidecar is reachable because
the services are on a network with no other route out. `af` is not on that
network. It is a process on the host, so its call to a control plane is not a
request the policy ever sees.

Say it the other way round and it is the same fact. Nothing you write in the
manifest turns the control plane sink off. Not setting a token does.

## Model calls

With no key set, the planner is deterministic and nothing is sent anywhere. That
is the default and `runner/src/model.ts` returns no configuration when neither
key is present.

With a key, there are two arrangements and they have different boundaries.

**Your key, from your machine.** The runner calls the provider directly. The
prompt is the workflow description, the page address and title, the field and
control names, and up to 4,000 characters of the page's visible text. The raw
HTML is never in it, which `runner/src/model.ts` states as a design property
rather than an accident.

**A key you store with the control plane.** Point `ANTHROPIC_BASE_URL` at the
control plane and the call goes through it, which is how a spend cap becomes a
cap on anything. The key never leaves that process and the plaintext exists for
one outbound request. `web/apps/api/src/providers/proxy.ts` logs no body. It
does handle one, and that is the point of naming this path: the prompt described
above passes through vendor-operated memory on its way to the model provider.

A `synth` rule takes the same route from the sidecar, and what it carries is one
outbound request line and a bounded piece of its body.

## Telemetry, analytics and crash reporting

There is none, and the sweep rather than the assurance is the evidence.

Searching the whole repository for PostHog, Sentry, Plausible, Google Analytics,
Mixpanel, Amplitude, Datadog and Bugsnag returns test fixtures, an egress rule
example, and a published vendor page saying they are absent. There is no client for
any of them. The engine has no version check and no update ping: the only
external address in the command line code is a control plane, and the only other
addresses are documentation links printed inside error messages.

OpenTelemetry tracing is off unless `OTEL_EXPORTER_OTLP_ENDPOINT` or its traces
variant is set. `engine/internal/telemetry/otel.go` returns a no-op tracer
otherwise, and when it is on the collector is one you named and one you run.
Span attributes are built in a single function from events that are already
redacted, and a test asserts that no other package in the engine imports the
tracing API.

## Where the claim is thinner than it sounds

Five things, stated because a reviewer will find them. The last one is a current
limitation of the product rather than a property of the boundary, and it is here
because it changes what the first one can carry.

**Rows from the twin reach the control plane.** An invariant holds when its
statement returns no rows, so the rows are the evidence, and
`engine/internal/invariant/invariant.go` keeps up to five of them.
`engine/internal/report/report.go` renders them as a Markdown table in the check
comment, and `web/apps/api/src/github/lifecycle.ts` stores that Markdown on the
generation row. The JSON alongside it is read for counts and dropped, so the
rows persist in the comment and only there.

Those rows come from the branch and not from production, and the branch is
masked, which is the whole reason a golden is scanned before it may be branched.
They are still row values, so "evidence, not records" is not an accurate
description of this path.

Read this together with
[masking and its check are the same instrument](#masking-and-its-check-are-the-same-instrument),
because the two compound. A column whose type is outside the six is copied into
the branch unchanged, so a statement that selects it puts a real production
value into the comment. That is the one place in this system where a production
value can leave the customer boundary, and it takes a violated invariant that
selects such a column to get there.

**Schema is not a secret in this design.** Table names, column names and row
counts cross on ordinary masking events. For most buyers that is uninteresting.
For a buyer whose schema is itself confidential it is the answer to a question
they were about to ask, so it belongs here rather than in a footnote.

**Build output crosses in full.** Every line, one event each. It is redacted for
credentials at two writers and for nothing else. A build that prints a customer
identifier prints it into the event stream.

**The set of what crosses is open by construction.** An unmapped event type is
forwarded rather than dropped. That is deliberate and it is documented, and it
means a future event carrying more than these does so without any gate objecting
that the boundary moved.

### Masking and its check are the same instrument

The masking default is fail closed: a column no rule names is emptied rather
than copied, because a column nobody has classified is not one anybody has
confirmed is safe. The verification scan then reads the golden back and refuses
to publish it if a detector finds anything. Two controls, one behind the other.

They are not two controls. Both decide what to look at from
`information_schema.columns.data_type`, and both accept the same six values:

```
text  character varying  character  json  jsonb  xml
```

`looksSensitive` in `engine/internal/masking/rules.go` is one copy of that list
and the query in `engine/internal/verify/scan.go` is the other. A column whose
type is outside it is not emptied by the default and is not read by the scan.

The silent part is the third consequence. `Assign` sets `Unmatched` only inside
the branch that has already passed the type test, so such a column is not
emptied, not scanned, and not listed among the unclassified columns that
`af mask plan` asks you about. It is copied, and nothing says so.

A rule that matches on a column's name still fires, and most of them say
nothing about type, so a column called `email` is masked whatever it is declared
as. Two of the shipped defaults are the exception: the ones for `name` and for
`*_key` require the type to be exactly `text`, and a `citext` column called
`name` matches neither them nor the type default. The exposure is a column whose
name no rule matches, or matches only through one of those two, and whose type
is not one of the six. `citext` is the sharpest case, because `information_schema` reports it
as `USER-DEFINED` and it is the ordinary Postgres type for an email address or a
username. An array of text reports `ARRAY`, and `bytea` and `inet` report
themselves.

This is not being changed today, and the reason is worth stating rather than
hiding. Widening the list is not an additive change: a column that is copied
today would start being emptied, which changes `rules_digest`, invalidates every
existing golden, and can break an environment that expects that column to hold a
value. It is a decision with a migration attached, not a patch.

Until it changes, treat a masked golden as covering the six types above, and
name any other column that holds something you care about in `masking.yaml`
explicitly, where the rule matches on name and the type never comes into it.

## What this page does not prove

Four limits, so that nobody quotes this for more than it says.

It is a reading of the source at one commit. It says what the software does. It
says nothing about how any particular deployment is configured, what the hosted
instance retains, or for how long. For the hosted instance, the
[published vendor list](https://antifailure.dev/subprocessors) is the
companion document and it is built the same way, from the code that talks to
each vendor.

Exact redaction covers values the engine loaded. A credential that the secrets
subsystem never saw is caught only if it matches a pattern rule, and the pattern
rules cover known provider shapes rather than everything.

Clean is a statement about what the scan read. It read a bounded number of rows
per column, and it read only the six types named above. It is strong evidence
that a rule missed nothing in a text column and it is not a proof about the
whole database.

The engine does not check itself for the property this page describes. Nothing
compares what a release sends against the list here, so keeping it true is a
review discipline rather than a gate. The two sentences above and the shared
type list were each found by reading the code, and each of them was green in
every check at the time.

Related: [egress](/docs/concepts/egress),
[masking](/docs/concepts/masking),
[verification](/docs/concepts/verification),
[what a control plane adds](/docs/getting-started/hosted),
[releases and how to verify one](/docs/security/releases).

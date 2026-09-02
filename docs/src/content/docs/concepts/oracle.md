---
title: The differential oracle
description: Run a change beside the version it replaces, on the same data, and report what the two did differently.
sidebar:
  order: 16
---

A test says whether the application does what you told it to. The oracle says
what this change did that the last version did not, which is a different
question and usually the one being asked in a review.

It brings a second environment up from a baseline revision, branches the same
golden for both so they start from identical rows, sends both the same requests
in the same order, and reports every difference in what came back and in what
ended up in the database.

```
af oracle
af oracle --baseline v2.4.0
af oracle --keep --report oracle.md
```

## What is compared, and what is not

Responses and database contents. Not events, not outbound effects, not traces,
not query plans.

That is a decision rather than an omission. Two comparisons done completely are
worth more than six done shallowly, because the first check that reports a
difference which is not one is the last check anybody looks at. When the other
four arrive they will arrive finished.

What is compared:

| | |
| --- | --- |
| Status code | Exactly, and by class. A status that falls into an error class outranks one that moves inside its class. |
| Response headers | Every header except a default list of the ones no two runs agree on. The list is printed on every run. |
| JSON bodies | Structurally, by path. Key order in the document is not a difference; a field that appeared, disappeared, changed type or changed value is. |
| Other bodies | By content. The report says the two differ and where, and does not attempt a text diff. |
| Database contents | Every table, row by row, matched on the primary key, with each column compared. |
| Table structure | Columns added, dropped, or retyped between the two sides. |

## The baseline

`oracle.baseline` decides which revision the comparison is against, and the two
values answer different questions.

`merge_base`, the default, is the commit this branch and the base branch share.
It answers "what does this branch change", and it does not move when somebody
else lands a commit on the base branch halfway through a review.

`ref` is a revision named outright: a branch, a tag, or a commit. It answers
"what changes when this ships", which is what a release gate wants.

There is no value for "the revision currently deployed", because the engine
cannot know what that is. A deployment pipeline does, and it passes the commit:

```
af oracle --baseline "$DEPLOYED_SHA"
```

With no `base_ref` set, the comparison tries `origin/HEAD`, then `origin/main`,
then `origin/master`, then `main`, then `master`, and the report says which one
it used.

## Two environments, one golden

The two versions cannot share an environment. They want the same ports, the same
service names and the same database.

They do share a golden. The candidate comes up first and the baseline is pinned
to whatever golden version the candidate branched, so a scheduled refresh
landing between the two cannot separate them. That matters more than it sounds:
if the two sides start from different rows, every row in the report is noise and
the comparison says nothing.

Only the images are built from the baseline checkout. The manifest, the egress
policy, the personas, the ports and the secrets all come from the candidate's
manifest. If the baseline's own manifest were used, a manifest change in the
pull request would move the application and the harness at once, and no
difference in the report could be attributed to either.

The candidate environment is left running whether or not `af oracle` brought it
up. The baseline is torn down unless `--keep` says otherwise.

## The probes

Both versions have to receive the same bytes in the same order, so the plan is
written down rather than discovered:

```yaml
oracle:
  probes:
    - name: list-customers
      method: GET
      path: /customers
    - name: place-an-order
      method: POST
      path: /orders
      headers:
        content-type: application/json
      body: '{"customer_id": 1, "total_cents": 2599}'
```

Each probe goes to the baseline and then immediately to the candidate, rather
than the whole plan to one side and then the whole plan to the other. Any value
that comes from the clock is much more likely to agree when the two requests are
milliseconds apart, and a probe that depends on an earlier probe's write sees
the same state on both sides at the same point in the sequence.

Requests are sent one at a time. Concurrency would make the order of the two
databases' writes depend on scheduling, and then the identifier a row got would
depend on scheduling too.

The agents that drive a workflow are not used here. They decide their next step
from what is on the screen, so two runs of one workflow send two different
request sequences, and a comparison of those compares the agent with itself.

## Non-determinism

A byte comparison of two responses reports a different `Date`, a different
session cookie, a different request identifier and a different generated
timestamp on every single request. So values are normalised before they are
compared, and every normaliser is narrow on purpose.

| Source | What happens |
| --- | --- |
| Clocks | Two strings that both parse as a timestamp and are within an hour of each other are equal. Further apart, they are reported. One side a timestamp and the other not is reported. |
| Random identifiers | Two strings that are both UUIDs are equal. |
| Sequence identifiers | Compared exactly, deliberately. See below. |
| Floating point | Numbers are equal within a relative tolerance of 1e-9, so representation noise is not news. |
| Session cookies, request ids | `Set-Cookie`, `ETag`, `Date`, `X-Request-Id` and nine others are not compared. The full list is printed on every run. |
| Ordering of writes | Requests are sent one at a time, and rows are matched on the primary key, so storage order is never a difference. |

The hour is configurable, and every run says how wide a gap the timestamp
normaliser actually absorbed. A gap of four milliseconds is the harness; a gap
of fifty minutes is worth a look.

What is not normalised is as considered as what is.

**Sequence identifiers are compared exactly.** Both databases branch one golden
and receive the same requests in the same order, so the sequences have to agree.
A sequence at 41 on one side and 42 on the other means the candidate wrote a row
the baseline did not, which is the most useful thing this comparison can tell
anybody. Normalising identifiers away would have thrown it out.

**A numeric epoch is compared exactly.** Deciding that a number is a clock from
the name of the field it sits under would silently ignore an expiry that moved
by a day. When a number under a name like `expires_at` differs, the report says
so and prints the line that would ignore it.

**An opaque token that is neither a UUID nor a timestamp is compared exactly.**
There is no shape to recognise, and this is not a place to guess. Ignore it by
path.

Everything the comparison declined to look at is printed, defaults included,
assembled while comparing rather than described in a document. An oracle that
silently ignores a field is worse than one that reports it, because the field it
ignored is where the bug was.

## What counts as a difference worth reporting

Findings are ranked, and the ranking is directional. A candidate that stops
returning a field, stops writing a row, or turns a served request into an error
has lost something the baseline had, and that is rarely intended. A candidate
that returns an extra field or writes an extra row is what a feature branch does
all day.

**Critical.** A request the baseline served and the candidate did not answer at
all. A status that fell into an error class. A row the baseline wrote and the
candidate did not. A body declared JSON that no longer parses.

**Major.** A status that moved inside its class. A field the baseline returned
and the candidate does not. A value that changed JSON type. An array that lost
elements. A media type that changed. A row whose columns disagree. A table or a
column the baseline has and the candidate does not.

**Minor.** A field or a row the candidate added. A scalar value that changed. An
array reordered with the same members. A compared header that changed. A status
that left an error class.

`oracle.fail_on` decides which of those fails the command, and defaults to
`critical`. A pull request exists to change behaviour, so failing on any
difference at all would fail every branch and teach everybody to pass the flag
that turns it off.

## Database contents

The two branches are compared by their contents rather than by the statements
that produced them.

Logical decoding needs a replication slot and an output plugin installed in the
database, and audit triggers need schema changes on every table in a database
that is supposed to have production's shape. Both also answer a question nobody
asked, which is which statements ran. What a review needs to know is what a row
holds.

Two snapshots are taken on each side, one before any request and one after. A
row that already differed before either version served a request is the
migrations' doing; a row that differs only afterwards is the application's.
The report labels each finding with which it was.

Tables are read inside a read only repeatable read transaction, so Postgres
refuses a write rather than this code promising not to make one, and every table
is read at one instant.

A table with more rows than `oracle.database.max_rows`, ten thousand by default,
is reported as not compared, with its approximate size. It is never silently
skipped: a report that omits a table reads exactly like a report that found
nothing wrong in it.

A table with no primary key has its rows matched on their whole content, so an
update reads as one row removed and one row added. Without a key there is no
fact about which row on one side corresponds to which row on the other.

## Ignoring a field

`oracle.ignore.fields` takes the subset of JSONPath people actually write:

```yaml
oracle:
  ignore:
    headers: [x-served-by]
    fields:
      - $.payment_intent
      - $.orders[*].reference
      - $..updated_at
```

`$.field` selects one field, `$.list[0]` one element, `$.list[*]` every element,
`$..name` that name at any depth, and `$.object.*` every field of one object. A
pattern that does not parse is refused when the manifest is validated, rather
than matching nothing quietly.

A path applies to a response body and to a table row alike. A row's path is
`$.<column>`, so `$..updated_at` written once covers the response field and the
column behind it.

Paths in the report are written in the same syntax, so one can be copied out of
a report and pasted into the manifest.

## Limits

These are real and are not going to be discovered by surprise.

- **An insert and a delete inside one request are invisible**, because the
  comparison is of contents and the net effect is nothing.
- **A background worker that writes a different number of rows on two runs**
  will report a difference that is not the change. Exclude its tables.
- **Non-JSON bodies are compared by content, not by structure.** A probe pointed
  at an HTML page will report a difference for a CSRF token. Point probes at
  endpoints that return JSON.
- **A new service in the candidate's manifest fails the baseline build**, since
  the baseline checkout has no source for it. That is a change the comparison
  cannot make, and it says so rather than comparing what is left.
- **The comparison costs a second environment.** On a copy-on-write database
  provider the second branch is nearly free and the second build is usually a
  cache hit; the containers are not.

## Configuration

```yaml
oracle:
  enabled: true
  baseline: merge_base        # or ref
  base_ref: origin/main
  fail_on: critical           # none, minor, major, or critical
  compare_timestamps: false   # true compares timestamp strings exactly
  compare_uuids: false        # true compares UUIDs exactly
  probes:
    - name: list-customers
      path: /customers
  ignore:
    headers: []
    fields: []
  database:
    enabled: true
    tables: []                # empty compares every table
    exclude: []
    max_rows: 10000
```

The block is absent by default. The comparison doubles the environments a run
costs and it needs a probe plan somebody wrote, so it does not happen unless a
manifest asks for it. A block that is present with `enabled: false` is a
different answer from no block at all: it is a probe plan somebody kept and a
check they turned off, and `af oracle` says so and exits zero.

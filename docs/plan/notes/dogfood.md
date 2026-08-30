# Dogfooding the control plane

Antifailure runs against Antifailure's own control plane. This note describes
the pipeline, what it has found, and what is not finished.

**State: built, not yet proven green ten times.** The pipeline, the harness and
the workflow all exist. Twenty-four defects are found and twenty-three are fixed
with regression tests. What has not happened is the streak: no ten consecutive
green runs exist, and none is claimed. The honest accounting is at the bottom.

## Why this and not a test suite

Every other check in this repository proves a part works. This one asks whether
the product is usable by somebody who has to use it, which is a question no
unit test asks. The way to find that out is to be that somebody: the control
plane gets a preview environment from a masked copy of its own database, agents
sign in and use it, and whatever that surfaces is a bug we fix rather than a
report we file about somebody else.

The rule that makes it worth doing: **nothing in the manifest may be a special
case for us.** `antifailure.yaml` at the repository root is an ordinary
manifest. There is no private syntax, no flag that only works here, nothing
configured outside the file. When a thing was awkward to express, that was the
product being awkward, and the fix went into the product. Most of the findings
below are exactly that.

## The pipeline

```
af golden refresh    copy the staging control plane, mask it, verify it, publish
af up                build both services, branch the golden, seal the network
af test              agents sign in and use the five screens
af insights          rehearse the pending migrations against the branch
af down              every resource it made, gone
```

Four files carry it, and all four are the ones a customer writes:

| File | What it says |
| --- | --- |
| `antifailure.yaml` | two services, the database, the egress policy, two personas, six workflows, four invariants |
| `masking.yaml` | the columns that need a particular transform, and the columns that must survive |
| `deploy/docker/app.Dockerfile` | the web application, as a standalone server |
| `deploy/docker/control-plane.Dockerfile` | the API, with the framework deliberately excluded |

Two more carry it in CI, and those are ours:

| File | What it does |
| --- | --- |
| `.github/workflows/dogfood.yml` | a pull request job against the control plane, a nightly against the corpus with a load smoke |
| `tools/dogfood` | runs `af ci`, budgets each step, records the run, and checks nothing was left behind |

### What the workflow runs

`.github/workflows/dogfood.yml` has three jobs and is separate from `ci.yml` on
purpose. `ci.yml` is the gate: it decides whether a change may merge, it runs
on every push, and it must never be slow. This one needs a container runtime, a
database to copy and twenty minutes, and what it produces is a report about the
product rather than a verdict on the diff.

**On a pull request**, one job against the control plane. A Postgres service
holds the staging database, `npm run seed --workspace @antifailure/db` migrates
and fills it deterministically, `af mask plan` refuses any column no rule
classifies, `af golden refresh` copies and masks and verifies and publishes,
and then `tools/dogfood` runs `af ci`. Verification is not a flag: an
unverified version cannot be branched, so a golden that reaches the next step
is one whose attestation was signed.

The staging database is synthesised rather than restored. A control plane that
copied a real customer's database into a public runner would be the single
worst thing this repository could do, and what the masking rules are about is
the schema's shape rather than anybody's rows.

**On a schedule**, the corpus, with a load smoke. Both jobs set
`AF_MODEL_MODE=replay`, so the spend is zero and stays zero: a cassette miss
refuses rather than calling a model or quietly falling back to the
deterministic planner. The one job in this repository that spends money on a
model is the nightly smoke that already existed.

**The comment job** is the only one in the file that can write, and it is
skipped entirely for a pull request from a fork, because a token that can post
a comment must never be handed to code somebody else pushed. It edits its own
previous comment rather than adding another, found by a marker, so a branch
pushed six times carries one comment that is current instead of six that
disagree.

Every action is pinned to a commit rather than a tag, every job has a timeout,
and both jobs assert afterwards that nothing carrying the engine's label is
still running. No gate was removed, no threshold lowered, no permission
widened: the workflow's default is `contents: read` and the one job that needs
more declares it for itself.

### The harness, and what it deliberately does not do

`tools/dogfood` runs `af ci` and almost nothing else. That is the point: `af ci`
is the command a customer wires into their own workflow, and a pipeline that
did anything differently would be one that works here and nowhere else. What it
adds is the three things a customer does not need and a maintainer does.

**A budget per step**, from the environment's own event stream rather than from
the prose. `af ci` prints section headings and no clock, so a budget built on
its output would be a budget on the whole command, and the one slow step inside
it would stay invisible. Every event carries a timestamp from the injected
clock, so a phase is the interval between the event that opens it and the last
event that closes it: `env.creating` to `env.ready`, `agent.started` to
`agent.finished`, `env.destroying` to `env.destroyed`. Last rather than first,
because two services build inside one `up` and six workflows run inside one
`test`, and what somebody waits for ends when the last of them finishes.

Every budget is a measurement doubled, and carries the reason for its number
next to it. A budget that fires on a normal day is a budget somebody deletes,
and a deleted budget catches nothing. A test asserts that every budget names a
step something actually produces, for the same reason `gatecheck` refuses a
stale exemption: a guard for a thing that does not happen reads in review as a
guard that is being enforced.

**A record per run**, as JSON. Ten green runs is a claim about ten files, not
about ten memories.

**A leak check afterwards.** `af ci` tears down its own environment, and this
asks the runtime whether it did. "The leak this product exists to prevent" is a
claim worth failing over rather than repeating.

It is deliberately outside `just gate`, with the reason recorded in
`exemptFromGate` where `gatecheck` can see it: its input is a staging database
and a container runtime, not the tree, so a green run here cannot promise a
green run there. Its own behaviour is a function of the tree, and
`go test ./tools/dogfood` covers that inside `just test-tools`, which `gate`
runs.

### Signing in, which decided most of the design

The control plane authenticates with GitHub, and a preview environment has no
route to github.com by design. That is not a problem to work around; it is the
product working. But an application nobody can sign into cannot be exercised by
anything, so the control plane grew a second way in: a link sent to an address
that already belongs to a member.

That path is the whole pipeline in miniature. The manifest sets
`api.resend.com` to `capture`, so the sidecar answers the mail provider with
its own success shape and records the message instead of sending it. The agent
asks for a link, reads it out of the inbox, follows it, and lands signed in.
Nothing is delivered to anybody, no key is configured, and the run proves that
an isolated deployment can be signed into — which is a real customer's problem
as well as ours.

Masking then replaces every address with a synthetic one at `example.test`,
which is correct and leaves nobody to sign in as. `deploy/docker/personas.mjs`
adds two known identities after the branch is made. It refuses to run outside
an environment the engine created, because it creates an account a known
address can sign in as.

### Recorded model answers

A model reading the page is the only part of a run that is not deterministic,
so a check that asks one on every pull request can change its answer with
nothing in the repository changing. `AF_MODEL_CASSETTE` records every prompt
and answer once; every run afterwards replays from disk, reaches no network,
and costs nothing.

A replay that finds no recording **refuses**. It does not call the model and it
does not fall back to the deterministic planner: the workflow is reported
`blocked`, which is a statement about the recording rather than about the
application. A cassette that quietly reached the network would spend money
nightly and nobody would notice; one that quietly degraded would keep passing
while the recording rotted.

## What it found

Twenty-four findings, twenty-three fixed with a regression test. Two came from
writing the manifest, four from the golden refresh, three from the web
application, six from wiring the pipeline itself, and the rest from running the
pieces against each other. Every one of them was invisible in the files, and
five of them are the same shape: a thing that was written, tested, documented,
and never called.

### Product bugs

**1. A golden could not be made of a database that uses row-level security.**
`pg_restore` stops at `CREATE POLICY ... TO "antifailure_app"` with `role does
not exist`. Roles are cluster-wide objects and pg_dump does not carry them, and
`--no-owner --no-privileges` correctly drops ownership and grants without
touching the role a policy names. So the product could not copy a database
using the pattern its own documentation recommends, and its own control plane
was one. Fixed: `pgcopy` now creates every role the source's policies name, as
`NOLOGIN` with no attributes and no memberships, because the only job it has is
to be a name a policy can resolve.

**2. An unclassified free-text column was copied verbatim.**
`transforms.md` says nullify "is the default for unclassified free text", the
shipped example's README says the same, and the planner left such a column with
no transform at all — which `BuildPlan` skips. A `notes` column nobody had
written a rule for went into every environment unchanged. It was survivable
because the plan reported the column and the verification scan reads every
column back, but a report is read once and a default runs every time. Fixed to
fail closed, with the generated-column and not-null cases handled separately:
the first cannot be written at all and the second cannot hold null, and
assigning either one a transform turns one unclassified column into a plan that
refuses to run.

**3. There was no way to empty a `NOT NULL` JSON column.**
`jsonb NOT NULL DEFAULT '{}'` is how most schemas hold a payload or a detail
blob, every one of those is free-form, and `nullify` is refused on it. The only
options were to copy the column or to make the whole plan unrunnable. Added
`empty_json`, which writes an empty object or array of the same kind, and made
it the fail-closed default for unclassified JSON.

**4. Masking rules did not follow a table to its partitions.**
`information_schema` reports a partitioned parent and each of its partitions as
a `BASE TABLE`, so the catalogue held both, and they hold the same rows. Two
consequences: those rows were masked twice, and because a rule names a table
and `events` is not `events_2026_08`, the parent got the rules somebody wrote
while each partition got the fail-closed default. Partitions sort after the
parent, so they ran last and won — a column explicitly marked `preserve` came
out emptied, silently. Found by refreshing a golden of the control plane, whose
`events` table is partitioned by month. Fixed by excluding partitions from the
catalogue, which is also half the masking time: 19 tables instead of 23, 28
seconds instead of 55.

**5. A sha256 digest was reported as a payment card.**
Verification refused to publish because one digest in eleven hundred contained
a thirteen-digit run that started with a real issuer prefix and passed the Luhn
check by chance. A digest is sixty-four hex characters, so long digit runs
separated by the letters `a` to `f` are ordinary. The finding read
`artifacts.sha256 holds payment-card`, which is both wrong and unactionable,
and a scanner that produces those is a scanner somebody turns off. Fixed: a
digit run glued to a letter is part of a token rather than a number anybody
wrote. What is given up is a card number written with a letter hard against it,
which is not how anybody writes one.

**6. Nothing ever analysed a restored database.**
`pg_restore` loads rows and does not analyse, and masking then rewrites most of
the columns it did load, so a golden arrived with no planner statistics at all
and every branch inherited a blind planner. Postgres does not fail on that; it
plans as if every table were empty. Three things downstream were measuring the
wrong database: `af insights` compares query plans between main and a branch to
find the index somebody stopped using, and every plan it read was a sequential
scan chosen because the planner could not see; `af load` measured a p95 against
those plans; and migration-rehearsal timings were timings of a plan production
would never choose. Fixed: `pgcopy.Analyze` after the copy and again after
masking.

**7. Sign-in failed outright behind two proxies.**
`x-forwarded-for` was written straight into the `inet` column on `sessions.ip`.
Behind one proxy that is an address; behind two it is `1.2.3.4, 5.6.7.8`, which
is a list, and the INSERT throws. The OAuth callback answered 500 and nobody
could sign in through that path at all. A direct request is the same bug from
the other side: the rate limiter's bucket key for a request with no header is
the literal string `unknown`, which is a fine bucket key and not an address.
Fixed with a separate `clientAddress` that produces a value for a column rather
than a key for a bucket, and a regression test that signs in through five
forwarded-header shapes.

**8. `bigint` columns were published as strings.**
`artifacts.size_bytes` and `audit_entries.seq` are `bigint`, and the driver
hands a bigint over as a string so a value beyond 2^53 is not quietly rounded.
That is the right default and the wrong shape to publish: `seq` is the cursor
the audit log pages by, so a client that sends back what it was given sends a
string where the input schema wants a number. Fixed at the API boundary, where
the shape is declared.

**9. A `::jsonb` cast double-encoded the value.**
postgres.js applies its own JSON serializer when it sees a `::jsonb` cast in
the query, so a caller that had already stringified stored `["a"]` as the jsonb
*string* `"[\"a\"]"`. `jsonb_typeof` said `string`, and the run page rendered a
paragraph of escaped quotes where five numbered steps belonged. drizzle's
template does not do this, which is why the audit log was fine and the seeded
rows were not. Fixed at the writer, plus a tolerant reader at the boundary,
because the next writer is an engine on somebody else's machine and one badly
encoded field must degrade one field rather than a page.

**10. A Dockerfile excluded by `.dockerignore` produced the wrong error.**
The manifest validator checks the file exists and it does. The build sends a
filtered copy of the tree to the daemon and names the Dockerfile *inside* it,
so a path the ignore file excludes is simply absent, and the daemon answers
`cannot locate specified Dockerfile` about a file anybody can see. `docker
build -f` does not have this problem, which is what makes it confusing: `-f`
reads from the host and never consults the ignore file. Added `AF-BLD-011`,
which names the file and says to add `!` for it.

**11. A static content security policy blanked the entire application.**
The App Router streams a page's content as inline scripts that replace the
placeholder, so `script-src 'self'` does not degrade the page, it deletes it.
The browser says only `Connection closed`. Fixed with a per-request nonce in
middleware, which is the version that is both correct and secure;
`'unsafe-inline'` is the version with the hole in it.

**12. An exception inside an excluded directory never applied.**
`.dockerignore` reads `deploy` and then `!deploy/docker/app.Dockerfile`, the
last matching rule wins, and `Excluded` implemented that correctly. It was
never asked: the walk pruned the excluded directory whole, so nothing inside it
was ever visited and the exception could not run. Every `!` rule naming a file
inside an excluded directory was silently doing nothing, which includes the
`!deploy/docker/bootstrap.mjs` line that was already in this repository. Fixed
by pruning only a directory that no negated rule could reach into, which keeps
the fast path for `node_modules` and is generous where it is unsure: being
wrong costs a walk of an empty subtree, and being wrong the other way drops a
file somebody asked for.

**13. The build sent six hundred megabytes nobody asked for, on every build.**
The finding that explained the thirty minute `app: building`. `.dockerignore`
named the six sibling directories its author thought of and not the seventh, so
`www` and its `node_modules` went into the build context: **598 MiB in 15,497
files**, of which 15,330 were the marketing site the image never opens. The
walk and the tar alone took 23 seconds, and the whole of it was streamed to the
daemon, hashed, and held in memory twice, on every build.

Nothing said so. `Context.Excluded` exists, and its comment calls it "the
number worth printing when a build is slower than somebody expected", and it
was printed nowhere. The refusal at 2 GiB did not fire, because the mistake was
not large enough to be refused, only large enough to make a forty second build
take half an hour. The type's own doc comment describes the failure it then
allowed: "should fail with a message naming the file, not stream for six
minutes and then fail inside the daemon."

Fixed three ways. `af up` now says the size, the file count and the directory
that dominates a context over 256 MiB, because the fix is one line in
`.dockerignore` and that line needs a name. `Context.Tar` returns a reader over
the archive rather than `strings.NewReader(string(...))`, which was a full copy
of the archive on every call. The archive is assembled into a buffer sized from
the walk instead of one that doubles twenty times. And `.dockerignore` gained
the missing line: **598 MiB in 15,497 files became 1.1 MiB in 105, and 23.3
seconds became 17 milliseconds.**

**14. Every build used the deprecated legacy builder.**
`internal/build` called `ImageBuild` without `Version`, so the daemon used
builder v1. Docker says of it: "The legacy builder is deprecated and will be
removed in a future release." Measured here, `FROM alpine` plus one `RUN echo`
took **50 seconds** on the legacy builder and **16 seconds** on BuildKit,
roughly 25 seconds of pure per step overhead against a 34 step Dockerfile. A
Dockerfile using `RUN --mount=type=cache`, which is ordinary modern syntax,
could not build at all, and stages that could run in parallel did not.

Fixed, and the fix was not the one line it looks like. The daemon accepts a
BuildKit build through the same endpoint with no session, which was verified
against this client rather than assumed. What is not one line is the response:
BuildKit answers with its own status stream carrying base64 protobuf traces,
not the `{"stream":"..."}` documents the parser read, so setting the version
alone produces a build log with nothing in it, and a build log is the only
thing that explains a failed build.

`internal/build/buildkit.go` decodes that stream. By hand, rather than by
importing BuildKit: pulling grpc, containerd and their graph into a binary
customers run in their own network is a large increase in the surface somebody
has to trust for a decoder that reads four fields, and a released protobuf
never renumbers a field, so a reader that skips what it does not recognise
keeps working. The field numbers were read off a real daemon and the captured
streams are checked in as fixtures.

Two things that only a real stream would have shown. `aux` is not one type: a
status update carries a base64 string and the final `moby.image.id` document
carries an object, so declaring it as a string makes the decoder fail on the
last document and abandon the stream, turning **every successful build** into
an error with a truncated log. And a vertex is re-sent on every state change,
so printing its name each time makes a forty step build into a log that is
mostly the same forty lines. Both are now tests.

The builder is negotiated once at construction from the daemon's own ping,
falls back to the legacy builder when there is no BuildKit, honours
`DOCKER_BUILDKIT=0` because that is the escape hatch every Docker user already
knows, and **says which one it used**: a silent fall back to the slow builder
is a regression that looks exactly like a slow machine.

**15. A migration command is handed the database under a name no image expects.**
`af up` reached the migration step and stopped: `AF_MIGRATION_DATABASE_URL is
not set. The bootstrap needs it.` The engine injects one variable, `DATABASE_URL`,
and it means two different things depending on which container reads it: an
elevated URL in a migration command and an unprivileged one in the service.
The published control plane image is built for a deployment that sets both
names explicitly, so it could not run under `af up` at all.

The engine's model is coherent and nothing about it is documented, which is the
actual defect: nothing in the reference says a `migrate` command receives a
different `DATABASE_URL` than the service beside it. Fixed on our side by
having the image accept `DATABASE_URL` as a fallback for both names, with the
reason written where the fallback is, so the published artifact runs unchanged
inside a preview. **The docs gap is open.**

**16. The event sequence is per command, not per environment.**
Visible only once the event log below existed. `Event.Seq` is documented as "a
monotonic counter per environment, so a consumer can order events and notice a
gap", and it restarts at 1 for every command: one run of `af up` reached seq
127 and the `af down` after it started again at 1, in the same file, for the
same environment. Ordering by sequence is wrong and a gap cannot be detected.
`tools/dogfood` orders by timestamp instead, which works and is not what the
field promises. **Open.**

**17. Two complete event sinks had no callers.**
`events.FileSink` and `events.JSONSink` are both fully written, both tested,
both documented down to rotation limits and what happens when the disk fills,
and `grep` finds zero constructions of either outside their own file. So the
engine emitted a typed, sequenced, redacted event for everything it did and the
only consumer was a terminal UI: no machine readable record of a run existed
anywhere, `af ci` attached no sink at all, and a CI job could only scrape prose.
This is the dead code shape exactly — the parts are all there and nothing calls
them. Fixed: every command that opens an environment now writes
`.antifailure/events/<env>.ndjson`, which is what `tools/dogfood` reads its per
step timings out of.

**18. The pull request comment could not show that the masking was verified.**
`report.Run.Verification` is rendered by the report package and assigned by
nothing outside a test, so the section that tells a reviewer the data was
proved masked was unreachable. That is the product's central promise and it was
a field nobody filled in. `af ci` ran no insights either, although it is the
command whose entire purpose is the pull request check. Fixed: `af ci` reads
the golden's stored attestation, checks the signature — this is a different
process from the one that signed it, which is the whole reason the signature
exists — and renders the result, plus a new insights section that distinguishes
"looked and found nothing" from "could not look".

### Configuration that is read by nothing

**19. `build.context` and `database.seed` were validated and read by nothing.**
Both in the manifest schema, both validated, both explained by `af explain`,
and neither reached any code. A user setting `build.context` got the whole
repository as the context and no warning; a user setting `database.seed` got no
seeding and no message saying so. Silently ignoring configuration is worse than
not offering it, because the person who set it believes something happened.

Both implemented. `build.context` narrows the walk, and paths elsewhere in the
manifest keep meaning what they say: they are translated into the context, and
a Dockerfile outside it is refused with `AF-BLD-012` rather than reaching the
daemon as "cannot locate specified Dockerfile" about a file anybody can see.
`database.seed` runs once per refresh with `DATABASE_URL` set, filling the
golden every branch is then copied from, which is what makes it the counterpart
of `source_url_env` rather than a cost paid per environment. Changing the seed
changes the golden's identity, so editing it does not leave environments
branching stale data. A seed that fails stops the refresh with `AF-DB-009` and
the script's own last line, because a golden published from a failed seed is an
empty database that looks like a working one.

### Test and tooling defects

**20. `runner/` had no lockfile, and `.gitignore` was why.**
`just deps` runs `npm ci --prefix runner`, which cannot work without one, and
CI ran `npm install` with `cache-dependency-path: runner/package.json`, so the
cache key was a file full of ranges and two runs of the same commit could
resolve different trees. Generating the lockfile was not the fix: `runner/
.gitignore` listed `package-lock.json`, so the generated file was invisible to
git and to a fresh checkout. Changing CI to `npm ci` without noticing that
would have turned a quiet defect into a red build on the first clone, which is
the only reason it was found — `git status` showed the file as neither tracked
nor untracked. Fixed: the ignore line is gone, the lockfile is committed,
`npm ci` succeeds in `runner/`, and CI keys its cache on the lockfile.

**22. The harness blamed a run for another terminal's containers.**
Found by running `tools/dogfood` for the first time, which is the right way for
this one to be found. Its leak check asked the runtime for everything carrying
`dev.antifailure.managed` and reported twelve leaked resources, all of which
belonged to a test suite running in a second terminal. On a hosted runner the
two questions have the same answer, because a job owns its machine; on a laptop
they do not. A check that blames a run for somebody else's containers is a
check people learn to ignore, and an ignored leak check is worse than none,
because the leak is the thing this product sells against. Fixed: the check is
scoped to `dev.antifailure.env` for the environment the run's own event stream
names, and a run that produced no environment reports nothing rather than
everything. Same run, same tool: it also reported a missing event log as a
defect after `af ci` had correctly refused a directory with no manifest, which
is the same false positive one level up.

**24. `af ci -o` meant something different from `-o` everywhere else.**
A local `--output` on `af ci` shadowed the persistent one, so `-o` meant "a
file to write" there and "text or json" on every other command. `af ci -o json`
wrote the pull request comment to a file called `json`, silently, and the one
command written for CI had no machine readable output at all. Fixed by renaming
it to `--report`, which frees `-o` to mean here what it means everywhere; the
example workflow and the generated reference moved with it.

**23. Events from a command's second session were written into a closed file.**
Found by the first real `tools/dogfood` run, which reported that nothing had
recorded the environment being torn down while `af ci` printed "torn down, 1
resources removed" two lines above. A sink is attached to each session as that
session opens and closed with it, which is right for a dashboard because a
command has one session. `af ci` has two: its teardown is deferred and runs
after the lifecycle that failed. The second session's events went into a
buffered writer over a closed file, and `Close` returned early because the file
handle was already nil, so they were dropped with no error anywhere. The log
recorded a run failing and never recorded it being cleaned up, which is the one
line somebody reading that log most needs. Fixed: the sink reopens on delivery
rather than refusing, with a test that closes it between two events.

**21. The masking suite shared one container name and one port.**
So a run that failed before its cleanup poisoned the next with `port is already
allocated` and then `container name already in use`. The second failure hides
the first and points at the runtime rather than at the test that broke, which
happened twice while dogfooding. Fixed: the container name is derived from
`t.Name()`, and the port is taken from the kernel by binding `:0` and reading
back what was assigned, because any number this code picks can be taken between
the picking and the binding.

## About every number in this note

They were all taken on one laptop running two agents against one Docker daemon,
and at the time of the build measurements the load average was 39 on 8 cores.
That is five times oversubscribed, and it is enough to make `npm ci` of 63
packages take five minutes, which it did. The justfile's own `gate` recipe
warns about exactly this before it runs, because a timing gate on a busy
machine fails while nothing is wrong.

So the builder comparison is honest about what it measures and what it does
not. `FROM alpine` plus one `RUN echo`, 50 seconds against 16, is a real
measurement of per step overhead and is the reason finding 14 exists. What
cannot be claimed from this machine is a wall clock figure for `af up`: the
control plane's build here is dominated by a host that is drowning, not by the
builder, and any number quoted from it would be a number about this laptop.

The place to take that measurement is a hosted runner with one job on it, which
is what `.github/workflows/dogfood.yml` is for.

## What one real run looks like

`tools/dogfood` against the control plane, on the machine this was written on:

```
dogfood: pr run on 46a22102, 30m32s
  doctor                 6s  ok
  ci                 30m23s  FAILED (1)
  database branch       20s  ok

2 things to classify as a product bug, a CI defect, or a docs gap:
  - The run emitted no env.destroyed event, so nothing recorded that the
    environment was torn down.
  - ci took 1823s against a budget of 1500s.

not green
```

Not green, and every part of that is the harness working. `doctor` and `ci` are
timed directly; `database branch` at 20 seconds is derived from the event
stream, between `db.branching` and `db.branched`, which is a measurement
nothing in the prose carries. The budget fired with the reason for its number
attached. The record is on disk as JSON with the event counts in it. The leak
check came back empty, correctly, because `af ci` tore the environment down on
its own. And the first finding is finding 23 above: a real defect, in the log
the harness had just started reading, found on its first run.

The build is the whole 30 minutes, and that run predates the builder fix. What
it shows about the harness is still the point, and `af ci` did exactly what it
promises when a build does not finish: it stopped at its own timeout, wrote a
report saying "Nothing ran" with a blocked rather than a failed verdict, and
tore the environment down. Blocked is the right answer. Nothing about the
change was tested, and saying so is more useful than a red mark that would have
meant something else.

### What the build log looks like now

The reason finding 14 is worth its own section is that this is what a BuildKit
build produces through the decoder, taken from a real `af up`:

```
  #12 added 63 packages, and audited 68 packages in 5m
  #12 DONE
  #15 > next build
  #15    ▲ Next.js 15.5.24
  #15  ✓ Compiled successfully in 2.1min
  #15 Route (app)                                 Size  First Load JS
  #15 ┌ ƒ /                                      145 B         103 kB
  #15 ├ ƒ /audit                                 145 B         103 kB
  #15 └ ƒ /signout                               145 B         103 kB
  #15 DONE
  #14 DONE
   #4 CACHED
```

Numbered by step, output attributed to the step that wrote it, `DONE` and
`CACHED` where they belong. Before the decoder this was empty, which is the
whole argument for not treating the version flag as a one line change.

## What is not finished

Stated plainly, because a note that implies more than was built is worse than
no note.

| Asked for | State |
| --- | --- |
| Preview environment of the control plane on every pull request | Manifest, masking rules and both images written. `af golden refresh` proven end to end. `af up` builds both images, issues a certificate, starts the proxy and reaches migrations; not yet observed green through to a ready environment on one machine. |
| Masking rules written first, attestation required | Proven. `masking.yaml` classifies the schema, `af mask plan` refuses an unclassified column, and an unverified golden cannot be branched. |
| `af test` driving the six workflows | Workflows written; no agent has run against the control plane. |
| `af insights` on control-plane migrations | Configured, and now rendered into the pull request comment. Not yet run against the control plane. |
| The standard pull request comment | `af ci` writes it, and now fills in the masking and insights sections that could not be rendered before. Posting is wired in `dogfood.yml` and has never posted. |
| `.github/workflows/dogfood.yml` | Written. Pull request job, nightly job, comment job. **Never run.** |
| Nightly against the full corpus, with a load smoke | Written, in the same file. **Never run.** |
| Recorded-model mode so the spend stays at zero | Built, 11 tests, and set in both jobs. A replay that misses refuses rather than calling the model. |
| An issue per finding, labelled `dogfood`, classified | Filed. Seven open issues and a tracking issue, [#17](https://github.com/antifailure/antifailure/issues/17) through [#24](https://github.com/antifailure/antifailure/issues/24). |
| Fix product bugs with a regression test that would have caught it | Seventeen of twenty-one, each with its test. |
| **Ten consecutive green runs** | **Not run. No streak exists and none is claimed.** |
| **Total CI time before and after** | **Not measured.** The workflow has never executed, so there is no "after" and measuring the "before" alone would be a number with nothing to compare it to. |

### Why the streak is not here

Not a blocked path. Two things ate it.

The defects, which is the honest half. Twenty-one of them, and the engine ones
were serial: a golden could not be made until roles were restored, could not be
planned until partitions were followed, could not be verified until a digest
stopped reading as a payment card, could not be branched until the right golden
was chosen. Each one had to be fixed before the next step could run at all.
That is the pipeline working exactly as intended and it is slow the first time
through.

And the machine, which is the half worth saying out loud rather than rounding
off. Every measurement in this note was taken on one laptop running two agents
against one Docker daemon. The daemon's image store corrupted mid-session
(`NotFound: snapshot ... does not exist`) and every build hung until it was
pruned. A second agent refreshed goldens into the same store, which is how
finding 13's sibling was found and is not a condition CI has. And the legacy
builder's twenty-five seconds a step, against a 34 step Dockerfile, means one
`af up` with a warm cache still costs a quarter of an hour here. A hosted
runner has one agent, a clean daemon, and a cold cache; it is a better place to
run this than the machine it was written on, which is the argument for the
workflow file rather than an excuse for it.

### The next three things

1. **Push the branch and let `dogfood.yml` run.** Everything upstream of it is
   written and the remaining unknowns are the ones only a real run answers:
   whether the seeded staging database is shaped enough for the masking rules,
   whether six workflows pass in recorded mode, and what the budgets should
   actually be. The budgets in `tools/dogfood` are measurements doubled from
   this laptop, and the first green run should replace them with numbers from
   the runner.
2. **[#17](https://github.com/antifailure/antifailure/issues/17), the builder.**
   It is the largest single number in the pipeline and it is deprecated, so it
   is both the cheapest win and a deadline. The measurement is in the issue and
   the work is decoding BuildKit's status stream without losing the build log.
3. **Then the streak.** Ten runs, ten records under `dogfood-record.json`, and
   the burn-down recorded here as it goes. A streak claimed before ten records
   exist would be exactly the kind of thing this note was written to stop.

# Dogfooding the control plane

Antifailure runs against Antifailure's own control plane. This note describes
the pipeline, what it has found, and what is not finished.

**State: the loop works; the streak does not exist yet.** `af up` brings the
control plane up from a masked and verified copy of its own database in about
three and a half minutes, and agents drive it: sign-in works end to end through
an email the environment captured from itself. That was measured on a laptop,
against a second web service the manifest no longer declares. On a hosted
runner, against the console the API now serves itself, every workflow came back
blocked until the three sign-in defects below were fixed.
Thirty-six defects were found getting there and thirty-five are fixed with a
regression test. What has not happened is ten consecutive green runs, and none
is claimed. The honest accounting is at the bottom.

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

**On a schedule**, the corpus, with a load smoke. Neither job sets a model key,
and no job in this repository does, so the spend is zero because there is
nothing to spend it with: the agents run on the deterministic planner. Both
jobs used to set `AF_MODEL_MODE=replay` and to claim that a cassette miss
refuses rather than falling back to that planner. Nothing has ever read
`AF_MODEL_MODE`, the variables the runner reads are `AF_MODEL_CASSETTE` and
`AF_MODEL_CASSETTE_MODE`, no recording exists at any revision to replay, and
the nightly smoke that was said to spend money on a model is not in this
repository either. Three claims and a spend control, none of them real. The
variable is gone.

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
next to it. Which machine the measurement came from matters and the code said
the wrong thing about it for a while: the comment claimed hosted runner
numbers, and they are laptop numbers, taken when no dogfood run had finished on
a runner at all. They are provisional until one has, and the doubling is what
makes them provisional rather than wrong. The `up` budget was the one that
would have fired: ten minutes, for a step whose console build alone measured
860 seconds cold on this machine.

The job's own timeout is larger than the budgets inside it, which is not
bookkeeping. The harness fails a step that runs over and writes a report saying
which one and by how much; a runner timeout kills the job and writes nothing.
A budget that cannot fire before the timeout is a budget replaced by a blank.

A budget that fires on a normal day is a budget somebody deletes, and a deleted
budget catches nothing. A test asserts that every budget names a step something
actually produces, for the same reason `gatecheck` refuses a stale exemption: a
guard for a thing that does not happen reads in review as a guard that is being
enforced.

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

Forty-six findings, forty-five fixed with a regression test. Two came from
writing the manifest, four from the golden refresh, three from the web
application, six from wiring the pipeline itself, thirteen from merging main and
from the first five runs in CI, and the rest from running the pieces against each
other. Every one of them was invisible in the files, and five of them are the
same shape: a thing that was written, tested, documented, and never called.

The last thirteen are worth separating out, because they came from the two things
this exercise had not yet done. Merging a branch that had been open for a while
found a manifest whose invariants could never fail and two migrations sharing a
number. Pushing found the rest: the first CI run failed on the first build, on
a builder that works on every machine here and no runner, and then answered the
pull request with silence. The second run got past the seeder and
found that the product's own masking command could not be run before `af up`,
and blamed a golden that was never involved. None of the seven was reachable
from a laptop.

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
branching stale data. A seed that fails stops the refresh with `AF-DB-013` and
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
generated reference moved with it. **The example workflow did not.** It was
still running `af ci --output report.md` on `origin/main` until 2026-09-01, when
extending `just docexamples` to read `examples/*.yml` found it. And the class
was never closed, only this instance of it: `af oracle` still defines the same
local `-o`.

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

**25. A golden's metadata did not survive being read back.**
The one that made two earlier fixes inert, and it was found by running them
rather than by testing them. `ListGoldens` rebuilt every version from its image
tag alone, so `RulesHash` came back empty and `Attestation` came back missing,
however carefully a refresh had filled them in. `af golden list` had been
printing an empty `RULES` column the whole time.

Two checks read those fields and neither could work. The rule that a branch may
only be made from a golden produced under this manifest's masking rules
compared against `""` and let everything through, which is how bringing the
control plane up branched a golden the masking test suite had published sixteen
minutes earlier and came up with an empty database. And the section of the pull
request comment that tells a reviewer the data was proved masked reads the
attestation off the version, so it could never render.

Fixed by putting both on the image at commit time, where the tag already lives:
a golden's metadata has to outlive the process that made it, and the image is
the only part of it that does. The attestation is base64 encoded because a
label holds a string and a signed JSON document carries quotes and newlines
through a `LABEL` instruction. The test asserts the round trip through
`ListGoldens` rather than through the struct a refresh returns, because that is
the difference the bug lived in.

**26. The control plane could not start inside a preview of itself.**
`af up` reached the point of starting the API and got `AF_DATABASE_URL is not
set. The control plane needs it to start.` The server reads the name a
deployment sets; the engine injects `DATABASE_URL`. Same shape as finding 15,
one layer up, and the argument is the same: the whole point of a preview being
made of the real artifact is that the artifact is unchanged, so an image that
can only start under one orchestrator's variable names is an image a preview
cannot run. Both names are now accepted.

**29. A health check timeout took down a healthy application.**
`app.Dockerfile` gave the liveness check three seconds against a server
rendered page. On a loaded machine that page answered in 4.4 seconds, correctly
and with a 200, and the check failed seventeen times in a row until Docker
called the container unhealthy. The line above the check already says its own
rule: asking for something a healthy application cannot answer "would report a
healthy application as unhealthy". Three seconds was that, for a different
reason. Now ten seconds, five retries, and a start period long enough for a
cold Next.js server on a contended host, in both images.

**27. `af up` told you to open the API, and the agents drove it.**
The finding that blocked every workflow, and the one that most justifies doing
this at all: it is invisible in every file involved and obvious the moment an
agent tries.

`Env.URL` returns the address of the first web service, and the list was in
start order. The control plane's web application declares `depends_on: [api]`,
so the API starts first and became the environment's address. `af up` printed
it. `af test` opened it with a browser, looked for the email field on the sign
in page, and found an API. All six workflows blocked, four minutes each, on a
Playwright timeout about a label:

```
locator.fill: Timeout 10000ms exceeded.
  waiting for getByLabel(/^(email|email address|e-mail|username or email)$/i)
```

Nothing about that message points at the cause. The login page's label is
correct, bound, and accessible; the agent was looking at the wrong service.
Any manifest whose user facing application depends on a backend has this, which
is most of them.

The `Status` path had the same bug by a different route: it sorts services
alphabetically, and `api` sorts before `app`.

Fixed in both. Running services are put back into the order the manifest
declares them, because start order is an implementation detail of bringing
things up and alphabetical order is an accident of naming. The manifest is
where the author says which service the product is, and every reader of that
list wants that answer: the status table, the pull request comment, and the
address a person is told to open.

Worth saying about what did work: `af test` called all six of these **blocked**
rather than failed, which is exactly the five verdict design doing its job. Six
red workflows would have said the change was broken. Six blocked ones said the
runner could not carry them through, which was true.

**28. Nothing told a service its own address, so every emailed link was dead.**
Found one layer past finding 27, once agents could actually reach the login
page. The agent filled in the email, the sign in link was sent, the sidecar
captured it, the agent read it out of the inbox, and then navigated to
`http://localhost:3100/auth/email/callback?token=...` and got
`ERR_CONNECTION_REFUSED`. Four minutes into a workflow.

3100 is the port inside the container. The address the environment answers on
is allocated at run time, so no value written in a manifest can be right, and
the engine injected `AF_ENV_ID` and `AF_SERVICE` and nothing about where the
environment could be reached. Every application that builds an absolute URL has
this: a sign in link, an OAuth redirect, a webhook callback registered with a
provider. It is not a niche shape; it is most products.

Fixed by reserving the host port for every web service before anything starts,
rather than allocating it when the ingress is created, and injecting two
things. `AF_PUBLIC_URL` is this service's own address, with `PUBLIC_URL` and
`BASE_URL` alongside it because most frameworks already read one of those and
an application that does needs no change at all. `AF_ENV_URL` is the
environment's address, which is a different question and the one an emailed
link needs: the service that sends the mail is usually not the application the
link lands on, and here the API mails it while the web application serves the
page.

**30. A signed out visitor could not reach the sign-in page from anywhere.**
The best finding of the lot, and the agents found it the way a person would:
by opening a page and reporting that there was nothing on it to press.

`redirect()` in the Next.js App Router works by throwing an exception the
framework recognises on its way out of the render. Every page here wraps its
data fetching in a try/catch so that an unreachable API becomes a readable
message rather than a stack trace, and that catch swallowed the redirect. So
asking for any page while signed out produced a full page error reading **"The
control plane did not answer. Error: NEXT_REDIRECT"** instead of the sign-in
page, and there was no route back in from anywhere in the application.

Nothing in the code looks wrong. `requireActor` is correct, the redirect is
outside its own try, and the catch that eats it is on the other side of a
function boundary in a different file. It compiles, it typechecks, and every
unit test passes, because the pages that have the bug are the ones nothing
renders in a test.

Fixed with a predicate in its own module, `isNavigation`, rethrown from every
catch in every page. Its own module because `next/navigation` cannot be
resolved outside the bundler and a predicate this important has to be testable
without one. The test that keeps it fixed reads the pages themselves and
asserts that a file with N catch blocks has N rethrows, because the next catch
somebody adds would reintroduce this and it is invisible until a signed out
person opens the page.

**31. A port that was free when probed and taken when bound killed the run.**
The allocator asks the kernel whether it can listen on a port before handing it
out, which is the best a separate process can do and is not a lock: anything
else on the machine can take it in the gap before the daemon binds. On a laptop
running two of these at once that gap is hit regularly, and the whole command
died with a message about a port number. The database provider now retries with
a different port, three times, matching narrowly on the daemon's own wording so
that a real networking failure is not retried and hidden.

**32. A typecheck is not a build, and CI never built the web application.**
Caught by the fix for finding 30 shipping a broken image. An import written as
`../lib/guard` from a page two directories deep **typechecks clean**, because
tsconfig resolves it through `baseUrl`, and fails in webpack with "Module not
found". `npx tsc --noEmit` was green; `next build` was not; the container would
not start.

`ci.yml` typechecked `packages/db`, `packages/policy` and `apps/api`, and named
no web application at all. So the pages a person looks at were never compiled by
anything before this.

That finding outlived the application it was found in. While this branch was
open, main landed its own console at `console/`, a static export the control
plane serves from its own process on its own origin, and that is the one the
published image ships. Two consoles is one too many, so the one written here is
deleted and the preview runs the shipped image alone.

I then claimed the survivor had the same gap, and it does not. I should record
that plainly, because asserting a missing check without looking is the failure
this whole document is about. main's `www` job already builds `console/` and
then asserts that every route the control plane routes to was actually
exported, which is a stricter check than the one I was about to add and catches
a case I had not thought of: a stray server-only import stops a page exporting,
and the route starts answering with the 404 page while nothing else notices.

What is genuinely missing is smaller and still worth closing. `just gate` says
at the top of the file that green here means green there, and the console was
in neither the web workspace's typecheck loop nor the runner's, so a type error
in it passed the local gate and failed the www job twenty minutes later.
`just typecheck` builds it now. CI needed nothing.

**46. A workflow that does not start is invisible to every gate.**
An edit meant to raise a job's `timeout-minutes` deleted its `runs-on` line.
GitHub refused the workflow, produced no job, no log and no failing step, and
reported the run under the file's path rather than its name because it never
got far enough to read one. A red check with nothing inside it.

Twenty-nine gates had nothing to say. The file was valid YAML the whole time,
which is why a parse check would not have caught it either: what was wrong was
the workflow schema.

I reached for `actionlint` and that was the wrong answer, for a reason this
repository had already written down. `just gate` has to work on a plane, and a
gate that begins with `go install` does not. main got there first and better:
`TestEveryJobSaysWhereItRuns` checks the one piece of the Actions schema that
produced this, in the tree, with no dependency and a positive control. That is
the version that survives the merge. Mine is gone.

The edit was mine, and it is one of three times in this session that a string
replacement took a line I did not mean it to take. One moved a job's steps into
another job. One deleted the whole `typecheck` recipe from the justfile while
removing the gate above it, which `gatecheck` caught within the minute by
noticing CI ran a typecheck the justfile no longer did. None was a thinking
error; all three were an editing technique that does not check what it removed,
and the gates caught two of the three.

**45. A compiled binary, committed for the third time.**
`go build ./tools/dogfood` writes `./dogfood` into whatever directory it ran
in, and `git add -A` committed 4.2MB of arm64 Mach-O to a repository that ships
Linux binaries. `tools/gatecheck` caught it, which is what it is for.

The interesting failure is not the binary. `.gitignore` names each tool's
command, the comment above the list says this has happened twice before, and
the list named the tools that existed when somebody wrote it. Eleven tools were
added since and nobody thought about that file while adding them. Both earlier
occurrences were fixed by adding one line, which is why there was a third.

A list maintained by remembering is wrong by default, so the completeness of
this one is checked now rather than remembered:
`TestEveryToolsBinaryNameIsIgnored` fails when a directory under `tools/` has
no line in `.gitignore` and no recorded reason for not having one. `docs` has
one, being a tool and the documentation site at once, which is the collision
the file's own comment warns about. Verified by removing `/dogfood` and
watching it fail.

**44. A good error message is not a working environment.**
With the masking rules fixed, the run reached `af golden refresh` and stopped:
the Postgres service is 17 and the runner ships a pg_dump 16, which refuses
outright to read a server newer than itself.

The engine handled this exactly right. It detected the mismatch, refused by
name, and printed the apt command that fixes it, per platform. That is the
behaviour this repository asks for everywhere, and the pipeline failed anyway,
because a message telling somebody what to install does not install it. The
workflow now does what the message says, in both jobs, and asserts the binary
is where the engine looks for it rather than trusting the package.

The same step already existed twice in ci.yml. It is here a third time rather
than shared, because a composite action for six lines is the worse trade, and
because the run that found this was the first one to get far enough to copy
anything at all.

**43. The transform reference contradicted itself about uniqueness, on one page.**
The table of transforms is generated from the registry and its Unique column is
correct. The paragraph two screens below it is written by hand, and it listed
`string_fpe` among the transforms that preserve uniqueness. The registry says
`PreservesUniqueness() bool { return false }`.

It reads like it should be true, which is why it survived: `string_fpe` keeps a
value's length and character classes, and two different inputs of the same shape
can land on the same output. The generated half of the page was right the whole
time, sitting directly above the wrong half, because one is regenerated and the
other is not. Found by going to the page to choose a transform for a unique
column and getting a candidate the engine would have refused.

**42. Two changes were each correct and the pair was not.**
main added single sign-on and SCIM. This branch wrote the masking rules. Neither
touched the other, and forty-two columns across eleven new tables arrived with
nobody having decided what happens to them.

Most of that is fine by design: an unclassified text column is emptied, which is
safe. One was not. `sso_connections.idp_entity_id` is unique, so emptying it
would set every row to the same value and fail on the second, leaving the table
half masked, and the engine refused the plan rather than finding that out
halfway through a run. Fixing it surfaced a second: `sso_domains.domain` is
unique too, and the `company` transform does not preserve uniqueness either.

Both are now keyed hashes, which keep uniqueness and equality and carry nothing
back to a customer, and that is the right answer for a value matched exactly
rather than read. The SCIM person columns are classified as the people they are,
because emptying them is safe and useless: a members page rendering a column of
blanks proves nothing about whether it renders a directory. Every column left to
the default is now listed by name in masking.yaml with its reason.

Nobody forgot anything here. That is the finding.

**41. The masking rules had never been checked against the schema.**
There was no point in the pipeline where the rules met the current schema until
this ran. `af mask plan` was in the workflow to do exactly that, and it had
never once succeeded, so the check that would have caught 42 was itself
untested.

**40. `af mask plan` could not be run before `af up`, and said the wrong thing about why.**
The first thing anybody does with masking is ask what the rules would do. In a
fresh checkout that failed, because the command connects to the environment's
own branch and no environment exists yet.

What it printed was worse than the refusal. A branch asked for by environment
carries no golden name, and the not-found path named that empty value in a
message whose placeholder is the golden version, so the output read `The golden
version  no longer exists` with nothing between the spaces, and the next step
sent the reader to `af golden list` about a golden that was never involved.
Three things wrong in one sentence: the wrong error, a blank where a name goes,
and advice for a different problem.

The schema a plan is about is the source's schema, which the golden is a copy
of, so the plan now reads the source when there is no branch and says which
database it read from. That is not a lesser answer; it is the same answer one
step earlier, which is where somebody fixing a masking rule wants it. With
neither a branch nor a source, the refusal says there is no branch and to run
`af up`, which is what AF-DB-014 exists for.

Proven against the seeded control plane: 93 columns across 26 tables, read from
the source, exit 0.

**39. The dogfood pipeline ran `af mask plan` where it could not work.**
The step is right to be where it is. Its whole argument is that a column nobody
classified should fail before a golden is published, when the fix is one line in
masking.yaml rather than after. But it ran the command at the one point in the
pipeline where the command refused, and the refusal was the one above. The step
did not move; the command was fixed to work there.

**38. The comment job answered a failed run with silence.**
The job downloads the report the dogfood job uploads. A run that fails before
`af ci` writes anything uploads no artifact at all, and `download-artifact` is
a hard error on a missing one, so the job died on its first step. The guard for
exactly this case, `[ -f report/dogfood-report.md ] || exit 0`, was the step
below and was never reached.

Silence is the one answer this must never give, because a reviewer who sees no
comment cannot tell whether the check passed, failed, or never ran. The
download tolerates a miss now, and a run with no report posts the fact that it
has no report, with a link to what happened.

**37. The Dockerfile copied a manifest that no longer existed.**
Deleting the second web application left one `COPY web/apps/app/package.json`
behind, and the image build failed on it in thirteen seconds. The workspace
lockfile still carried the application's whole dependency tree too, seventeen
hundred lines of a framework and a compiler that nothing installs any more.
Both are gone, and the comment beside the COPY block, which explained the
scoping in terms of that application, now explains it in terms of what is
actually there.

**36. Two migrations both numbered 0012.**
main added device authorization as 0012 and this branch added email sign-in as
0012, an hour apart. Whichever merged second would have applied SECOND on a
database that took both and FIRST on a database that only ever saw it, so two
installations would have run the same two migrations in different orders and
nothing would have said so.

main also wrote the test that catches it, on its own branch, naming this exact
collision. So the merge produced the defect and imported its own detector in
the same commit, and the detector won. Renumbered to 0017.

**35. BuildKit works on every developer machine here and no CI runner.**
The first CI run failed on the first build with `no active sessions`, from
`docker.io/library/alpine:3.20`, before a single instruction ran.

The daemon's `/build` endpoint runs BuildKit against a session, which the
client is expected to open first: it is how registry credentials, secrets and
SSH sockets reach the builder. The Docker CLI opens one. This engine does not,
because opening one means the buildkit session packages, which pull a
dependency graph an order of magnitude larger than everything the engine uses
to talk to Docker, including a Kubernetes client that would then have to be
kept in step with the one the cluster runtime already pins. Adding it upgraded
thirty transitive Kubernetes packages, which is not a thing to do inside a
merge.

The negotiation was not wrong so much as asking slightly the wrong question.
The ping advertises BuildKit as the default builder, which is true, and carries
no field that says "and you will need a session". Docker Desktop does not
require one and a plain Linux dockerd does, which is the worst possible split:
every machine this was written on is fine and every machine it has to run on is
not, so the suite was green locally and red on the first push.

A build that fails this way is now repeated on the legacy builder and says so.
The first attempt costs resolving one FROM line, which is the zero seconds it
failed in, and what somebody gets is a slower build rather than no build. The
session is the better answer and it is a change of its own, on a branch where a
Kubernetes upgrade can be reviewed as the thing it is.

**34. Four invariants in this repository's own manifest could never fail.**
Every one of them was written as `SELECT count(*) ... WHERE ...`. An invariant
holds when its statement returns no rows, and a bare count returns one row
saying zero, so all four reported clean against any database at all, including
one where the join they check is broken. They were the evidence that masking
had not severed a relationship, and they were not evidence.

`af explain` refused the manifest and named all four with the fix. Nothing here
found this by reading it; the product found it in the product's own file, which
is the entire argument for this exercise. They now return the offending rows.

**33. Editing the manifest rebuilt every image.**
An image's tag is derived from the build context's digest, and the manifest was
in the context. So changing a workflow expectation or an egress rule changed
the digest, and `af up` asked the daemon for an image that had never been
built. The layers still cache and most of the work is skipped, and it is still
a rebuild nobody asked for, on the file a person edits most often while getting
a manifest right. Neither image reads it, so it is excluded now.

The general shape is worth keeping in mind: deriving the cache key from the
whole context is correct and conservative, and it means anything in the context
that no Dockerfile reads is a cache miss waiting to happen.

**21. The masking suite shared one container name and one port.**
So a run that failed before its cleanup poisoned the next with `port is already
allocated` and then `container name already in use`. The second failure hides
the first and points at the runtime rather than at the test that broke, which
happened twice while dogfooding. Fixed: the container name is derived from
`t.Name()`, and the port is taken from the kernel by binding `:0` and reading
back what was assigned, because any number this code picks can be taken between
the picking and the binding.

## Where the gate suite is structurally blind

Worth stating on its own, because it is the argument for finishing this rather
than a footnote to it. Almost every finding above was invisible to twenty-nine
gates that were all passing.

- Two consoles doing the same job. No gate notices architectural duplication.
  Both compiled, both were tested, both were correct.
- `masking.yaml` drifting from a schema main had changed. Catching it needed
  the product, `af mask plan`, and that check had never once run successfully.
- Four invariants written as `SELECT count(*)` that could never fail. Every one
  of them parsed, and the file they were in was in no gate at all.
- The transforms page contradicting itself, generated table against
  hand-written prose, with a comment in the generator explaining that the prose
  was deliberately left alone.

The pattern is one thing. The suite is strong on mechanical invariants, where
one artifact is checked against a rule, and blind to **two things that are each
individually valid and jointly wrong**. Nobody wrote a bug in any of those four.
Somebody wrote a console and somebody else wrote a console; somebody added SSO
tables and somebody else wrote masking rules; somebody generated a table and
somebody else wrote a paragraph under it. Each half passed review because each
half was right.

That is exactly the gap this pipeline exists to close, and it closes it in the
only way that works: by running the whole product against a real database and
seeing what the combination does, rather than by adding a thirtieth rule about
a file.

Three of the four are now also caught mechanically, which is worth doing where
it is cheap and is not a substitute for the above:

| Was blind to | Now caught by | Would it have fired |
| --- | --- | --- |
| A manifest whose invariants can never fail | `just examples` and ci.yml validate this repository's own `antifailure.yaml`, not only the examples' | Yes, verified by reintroducing the `count(*)` and watching AF-MAN-002 name the line |
| Prose contradicting the table generated above it | `TestTheProseAgreesWithTheRegistryAboutUniqueness` reads the hand-written half and checks every transform it classifies against the registry | Yes, and it found a second one on its first run: the prose was wrong about `int_fpe` as well as `string_fpe`, and I had corrected only the one I noticed by hand |
| Masking rules drifting from the schema | `af mask plan` in the pipeline, which now runs | Yes, this is finding 42 |

The fourth, architectural duplication, has no cheap mechanical form and is left
to the thing that actually found it: a person asking which of the two the
preview should run.

The second row is the one to keep in mind. That gate was written to close a gap
I had already fixed by hand, and it immediately failed on a case I had missed
in the same paragraph, in the same session, having looked directly at it. A
check positioned to compare two artifacts finds things a person comparing them
does not.

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

Against the control plane, after the fixes above:

```
Bringing up antifailure-hosted-loop-c7189a
  branching the database from gv_20260829163911_710c39a7
  app: built in 16s
  api: built in 22s
  issued an environment certificate so the proxy can read inside TLS
  egress proxy ready with 1 rule, everything else takes the default
  api: running migrations
  api: ready at http://127.0.0.1:46000
  app: ready at http://127.0.0.1:46001

  ok    app                          http://127.0.0.1:46001
  ok    api                          http://127.0.0.1:46000

  Open  http://127.0.0.1:46001
```

Three and a half minutes, from a golden that was copied from the control
plane's own database, masked under `masking.yaml`, and verified: 84 columns
across 23 tables, 11,530 rows sampled. The branch holds 3 organizations, 26
users and 60 environments, none of them anybody's real address.

Then the agents:

```
  ok    a-viewer-cannot-edit-policy  passed in 34.106s
  1 passed, 1 failed, 0 flaky, 0 blocked, 4 unverified
```

One workflow passing is a small number and it is the number that matters,
because of what had to work to get it: an agent asked for a sign-in link with
an email address, the API sent it, the egress sidecar captured the request to
Resend instead of delivering it, the agent read the link out of the captured
message, followed it to an address the engine had allocated minutes earlier,
landed signed in as the viewer, opened the network policy page, and confirmed
that this role may not edit it. Every one of those steps was broken at some
point today, and each break is a finding above.

The `unverified` results are the product refusing to lie. Without a model key
the runner reads the accessibility tree and compares it against the manifest's
expectations, and an expectation written as a sentence about the product can be
neither confirmed nor contradicted by anything on screen. It says exactly that:

> Nothing on the page contradicts what was expected, and nothing confirms it
> either, so this run proved nothing. Set a model key so the runner can read
> the page, or write an expectation whose words appear on it.

That is the right answer and it is more useful than a pass would have been. The
expectations have since been rewritten to name the pages' own labels, which is
the zero-spend path to a verifiable result; a model key still improves on it
rather than being required.

## What is not finished

Stated plainly, because a note that implies more than was built is worse than
no note.

| Asked for | State |
| --- | --- |
| Preview environment of the control plane on every pull request | **Proven.** `af up` brings both services up in about three and a half minutes from a masked, verified copy of the control plane's own database. |
| Masking rules written first, attestation required | **Proven.** `af mask plan` refuses an unclassified column, and a golden that does not verify cannot be branched. |
| `af test` driving the workflows | **Proven that agents run.** One workflow passes outright, sign-in works end to end through a captured email. The rest need either a model key or the rewritten expectations to return a verdict rather than `unverified`. |
| `af insights` on control-plane migrations | Wired into `af ci`'s report and not yet run against the control plane. |
| The standard pull request comment | `af ci` writes it, and now fills in the masking and insights sections that were unreachable. It has posted, on two pull requests. |
| `.github/workflows/dogfood.yml` | Written: pull request job, nightly job, comment job. The pull request job and the comment job have run and been green, in about six and a half minutes. The nightly job is `schedule` only and has still never fired. |
| Nightly against the corpus, with a load smoke | Written, in the same file. **Never run.** No scheduled run has fired. |
| Recorded-model mode so the spend stays at zero | Built, 11 tests. The runs above spent nothing at all, because no key was set. |
| An issue per finding, labelled and classified | Filed. [#17](https://github.com/antifailure/antifailure/issues/17) through [#24](https://github.com/antifailure/antifailure/issues/24); six closed with their fixes. |
| Fix product bugs with a regression test that would have caught it | Thirty-two of thirty-three. |
| **Ten consecutive green runs** | **No streak exists and none is claimed.** Two runs have been green and neither counts toward one: both reported all six workflows `blocked`, so the record says green about a run that carried nothing through. |
| **Total CI time before and after** | **Not measured.** The workflow's own runs are 6m29s and 6m49s, of which `af ci` is about four minutes. What is not measured is the effect on total CI time, because Dogfood runs beside `ci.yml` rather than inside it. |

### Why the streak is not here

Not a blocked path, and not the defects either any more. The machine.

Every measurement in this note was taken on one laptop running two agents
against one Docker daemon, at a load average of 33 to 40 on eight cores. At
that load `npm ci` of 63 packages takes five minutes, a container answering
200s in 4.4 seconds fails a three second health check, a port that is free when
probed is taken when bound, and the daemon's image store corrupted twice badly
enough that every build hung until it was pruned. Three separate test packages
hit their own timeouts blocked on daemon calls, always with the same stack.

A hosted runner has one job on it, a clean daemon, and no second agent. That is
a better place to run this than the machine it was written on, which is the
argument for the workflow file rather than an excuse for it.

### The next three things

1. **Done, and it answered the unknown.** `dogfood.yml` has run green twice
   on a hosted runner, and both runs reported all six workflows blocked with
   a ten second timeout waiting for an email field. The runner navigated to
   `/login`, which the console's static export does not have, so the agents
   were driving its 404 page. That is fixed, along with two more defects
   underneath it that each would have blocked all six on their own. The
   budgets in `tools/dogfood` still want replacing with runner numbers.
2. **Get the six workflows to a verdict.** The rewritten expectations are the
   zero-spend half; a recorded cassette for the corpus is the other.
3. **Then the streak.** Ten runs, ten records on disk, and the burn-down
   recorded here as it goes. A streak claimed before ten records exist would
   be exactly the thing this note was written to prevent.

# Open questions

Questions the agent could not answer from the specification, the code, or the
repository, recorded with the interpretation it proceeded under. A question
with an answer is kept rather than deleted, because the reasoning is the useful
part.

## Open

### Q1. Legal name of the copyright holder

The MIT license header reads `Copyright (c) 2026 Antifailure`. If there is an
incorporated entity, that name belongs there instead, and in
`ee/LICENSE.md`, the enterprise license issuance tool, and the SBOM.

**Proceeding under:** the product name. This is a one line change in three
files whenever the entity name is known.

### Q2. Contributor License Agreement

The licensing amendment left this deliberately open. PostHog uses a CLA; the
plan's decision 2 specifies the Developer Certificate of Origin with no CLA,
and the repository currently implements DCO only.

A CLA is only needed if contributions might ever be relicensed, for example to
move a community feature into `ee/`, or to relicense the project wholesale. It
has a real cost: some contributors will not sign one, and the friction shows up
on first time contributions, which are the ones most sensitive to friction.

**Proceeding under:** DCO, no CLA. Reversing this later is possible but
requires contacting every prior contributor, so it is worth an explicit answer
before the contributor count grows.

### Q3. Crowdi-Agents code reuse

The owner granted access to `maksymrajszewski/Crowdi-Agents` as possible
material for the agent runner, matching decision 8 in the plan.

**Finding:** the repository has no `LICENSE` file and its `package.json` sets
`"private": true`, which means all rights reserved by default. Copying any of
it into this repository, which is MIT and public, would create a licensing
defect. Separately, the architectures diverge: Crowdi's browser controller is
selector driven and coupled to Supabase, BullMQ, and its own schema, while this
plan specifies a selector free accessibility tree harness that the engine
spawns over a socket API.

**Proceeding under:** a clean implementation. Ideas were carried, code was not:
human like typing cadence, deliberate misclick and mouse jitter for realism,
friction detection as a first class signal, browser pooling, and persona
diversity all appear in the runner design and all were reimplemented.

**Needed to change this:** the copyright holders of Crowdi-Agents relicensing
it, or the relevant files, under MIT, recorded in that repository.

### Q4. Azure provisioning is not done

Part B assumes four resource groups, registered providers, approved quotas,
Terraform state storage, two Key Vaults, a delegated DNS zone, and an Entra app
registration with OIDC federation. None of these exist yet. The subscription is
authenticated and reachable.

**Proceeding under:** everything that does not need Azure is built and proved
first. Terraform is written and validated but not applied. `tools/preflight`
reports each missing item with the exact command that provisions it, so this
becomes a single sitting of work rather than a blocker discovered piecemeal.

### Q5. Third party test accounts are not provisioned

Neon, Supabase with branching, the Stripe sandbox, Datadog, New Relic, Doppler,
1Password, and the Entra test tenant are all listed in the access matrix and
none exist.

**Proceeding under:** each provider is implemented against its documented API
with a fake that enforces the same validation rules, and runs the full
conformance suite against that fake. The provider is marked `written` rather
than `proven` in `STATUS.md` until it has run against the real service. The
distinction is load bearing and is never blurred.

### Q6. Corpus applications are not vendored

Appendix E.5 proposes eight real applications as submodules. Each needs a
license check before vendoring, and several are large.

**Proceeding under:** a purpose built fixture application in
`corpus/fixture-app` that deliberately contains every edge case the catalogs in
C.12 list, plus the seeded synthetic data generator. It is faster to iterate
against, it can be made to fail on purpose, and it does not depend on an
upstream project's release schedule. The real applications are added on top,
not instead.

### Q7. Go cannot measure the branch coverage C.5 asks for

C.5 sets "100 percent branch coverage" on `masking`, `subset`, `verify`,
`policy`, `journal`, `redact`, `secrets` and `webhook`, and G4 makes it a gate.
Go does not measure branch coverage. `go test -cover` instruments statements,
so `if err != nil { return err }` counts as covered when the error path never
ran, and `a && b` counts once however many ways it was reached. The number the
Go toolchain can produce is not the number the plan names.

The options are a third party branch-coverage tool, which for Go means
rewriting the instrumentation and none of the candidates are widely used, or
holding those packages at 100 percent STATEMENT coverage and being explicit
that it is a weaker bar, or dropping the requirement.

**Proceeding under:** 100 percent statement coverage for those packages, with
`tools/coverage/thresholds.yaml` saying in as many words that it is standing in
for the branch requirement rather than satisfying it. A gate that measures
something real and says what it measures is worth more than one that claims a
number nothing computes, which is the state this replaced.

### Q8. G3 asks for goleak in every package that starts goroutines, and five do not have it

G3 reads "`goleak` verifies no goroutine leaks in every package that starts
goroutines". Nine packages contain a real `go` statement outside their tests,
and five of them call `goleak` nowhere: `cmd/af-proxy`, `conformance`,
`internal/insights`, `internal/load` and `internal/subset`. The other four,
`internal/cli`, `internal/controlplane`, `internal/events` and `internal/hud`,
already verify.

Counted with a parser rather than a grep. The obvious `grep -l 'go '` says
nineteen packages, because it matches prose: "spans go anywhere" and "events
that go to the control plane" are comments, not goroutines. The wrong number
was written here first and is recorded because the correction is the useful
part.

This is not a formality. `goleak` in `internal/hud` found a goroutine leaked
per dashboard, from a cancellation watcher receiving on a nil `Done` channel,
which is exactly the shape that costs a long running `af up` its memory.

**Proceeding under:** added package by package, each with the leak it found or
a statement that it found none. Adding all five at once and reaching for
`goleak.IgnoreTopFunction` on whatever turned red would produce a gate that
reports nothing, which is worse than the gap.

Four are done and clean, in CI as well as locally: `cmd/af-proxy`,
`conformance`, `internal/load` and `internal/subset`.

`internal/insights` is the open one. It passes on a workstation and fails in
CI, with two `net/http` `persistConn` `readLoop` and `writeLoop` pairs still in
IO wait at exit. The stacks carry no frame from this repository; they are idle
keep-alive connections held by an `http.Transport`. What has been audited and
is not the cause: every `dockerutil.Client()` in the package and its tests is
closed, including on the error paths, and the one streaming response it reads,
`ContainerLogs` in `lastLines`, goes through `dockerutil.Discard`, which drains
before closing. That leaves either a leak below the audited code or the known
race where `CloseIdleConnections` returns before those goroutines unwind.

It is left failing-open rather than silenced. `goleak.IgnoreTopFunction` on
`net/http` would make it green and simultaneously blind it to the leak class
most worth catching in a package that talks to the daemon, which is the same
trade this question exists to refuse.

### Q9. A golden image disappears between RefreshGolden and the next Branch

`TestConformance/Branch_IsIsolatedFromTheGolden` in `internal/db/docker` fails
intermittently, on CI more often than locally, and it is now a required check
so it blocks real pull requests.

    db.go:656: Branch(gv_20260829030220_7969f160, env_conformance00005):
    AF-DB-004: The golden version gv_20260829030220_7969f160 no longer exists.

Line 656 is the FIRST `Branch`, so the window is narrow: the image is gone
between `RefreshGolden` returning it and the `ImageInspect` at the top of
`Branch`, with nothing of the suite's own running in between.

An earlier fix gave every golden a unique id, and it is live: the `7969f160`
above is a per-refresh sha256 rather than the constant that used to truncate to
the same eight characters. Identifier collision is fixed and this is a second
cause.

What has been ruled out, so nobody pays for it twice:

- `sweepCandidates` cannot be it. It lists containers labelled `candidate` and
  removes containers; it never touches an image. (It does carry a separate bug:
  `parseErr != nil || created.Before(cutoff)` removes a candidate whose created
  label will not parse whatever its age, which can kill another process's
  in-flight refresh. Worth fixing, but it produces a different failure.)
- The behaviours are not parallel. There is no `t.Parallel` anywhere in
  `internal/db/docker`, so nothing in this suite runs beside itself.
- The conformance selftest is not a second writer. `conformance/db_selftest_test.go`
  runs the same suite against an in-memory fake, so it commits no images.
- `DestroyGolden` refuses a version a branch still references, and the harness
  registers its cleanup on the subtest, so the suite's own teardown cannot run
  in this window.

What is left, in the order worth trying: another test package creating goldens
on the same daemon while this one runs, since Go runs packages in parallel and
the suite's own leak check already carries a comment about exactly that; a
daemon race where a freshly committed image is not immediately inspectable; and
`ImageRemove` with `PruneChildren: true` reaching a sibling committed from the
same base image.

**Answered, partly.** The identifier was still colliding, along an axis the
first repair did not consider.

The rules hash was `sha256(t.Name() + "#" + refreshCount)`. That is unique
within one process and identical across processes: every package running this
suite calls its entry point `TestConformance`, so `internal/db/docker`,
`internal/db/dblab`, `internal/db/neon` and `internal/db/supabase` all produce
the behaviour name `TestConformance/Branch_IsIsolatedFromTheGolden` and
therefore the same eight characters, on every run. Go runs test packages in
parallel, so "unique within one process" was never the property needed.

The evidence had been in the failures the whole time and I read past it twice:
the failure on 2026-08-29 at 03:02 and the one at 10:11 quote the SAME hash,
`7969f160`, differing only in the timestamp. A hash that repeats across hours
and runs is derived, not unique.

It is four random bytes now, which is what `internal/masking`'s
`uniqueRulesHash` already did for the same reason. Randomness cannot collide by
construction, where both derivations collided along an axis nobody thought of.

Not claimed as proven. The suite is green against a real daemon after the
change, but this flake is intermittent and one green run is not evidence. What
IS established is that the collision was real and is now impossible; if the
failure returns, the cause is something else and the ruled-out list above still
stands.

Still open underneath it: `provider.NewGoldenVersionID` resolves to the second,
so two refreshes in one second still depend on their hashes differing. That is
production code, not test code, and two refreshes in a second is not something
only a test does.

**The flake survived the fix.** On 2026-08-29 at 11:12, after the random hash
was on main, the same behaviour failed again:

    Branch(gv_20260829111232_ebaff111, env_conformance00005):
    AF-DB-004: The golden version gv_20260829111232_ebaff111 no longer exists.

`ebaff111` is random, not the repeated `7969f160`, so the fix is live and the
identifier collision really is gone. It was simply not the cause of this. The
commit that made the change said in as many words that one green run of an
intermittent failure is not evidence and that if the failure returned the cause
was elsewhere. It returned; the cause is elsewhere.

That leaves the ruled-out list above plus one entry: identifier collision, now
impossible by construction rather than merely unlikely. The remaining
candidates, in the order worth trying, are a daemon race where a freshly
committed image is not immediately inspectable, and `ImageRemove` with
`PruneChildren: true` reaching an image committed from the same base. Both want
instrumentation inside `RefreshGolden` and `Branch` rather than more reading:
log the image id at commit, list it back immediately, and log again at the
`ImageInspect` that fails.

### Q10. Six call sites raise AF-DB-004 without the field its message names

`fill` renders a placeholder it has no value for by printing it verbatim, so an
error raised without every field its template names reaches a reader with
`{version}` in it.

AF-DB-004's message is "The golden version {version} no longer exists." Six
sites raise it with an `env` field and no `version`: `internal/env/insights.go`
twice, and `internal/env/golden.go` four times. Every one of those prints the
literal `{version}` to somebody whose environment has just failed.

Found while adding the available-versions listing to the same code, which is
why that listing is wrapped onto the error rather than added as a catalog
field: adding `{available}` to the template would have given those six a second
placeholder to leak.

**Proceeding under:** recorded, not fixed. The repair is either passing the
right field at each site or making `fill` refuse a template it cannot complete,
and the second is the better one: a placeholder that reaches a user is always a
bug, so the catalog should not be able to produce one. That is a change to how
every error in the product renders and wants its own pass rather than being
tacked onto this one.

## Answered

*(none yet)*

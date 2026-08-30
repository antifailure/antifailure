# Merge queue

The ten pull requests open against `main` on 2026-08-28, the order they will be
merged in, and what happened to each. Written before the first merge so that the
plan can be checked against the outcome rather than reconstructed from it.

## Two things the brief assumed that this repository does not have

Both are recorded here rather than worked around silently, because the
instruction that depends on them cannot be carried out as written.

**There is no `web/apps/app` and no `web/packages/ui`.** `web/apps/` contains
`api` only, and `web/packages/` contains `db` and `policy` only. The paths the
UI preference rule names do not exist in the tree.

**No open pull request touches the front end.** The cofounder's work
(`maksymrajszewski`, a collaborator, three commits, most recently PR #13) lives
entirely under `www/`: the marketing site, its components, its theme. Zero of
the ten open pull requests modify a single file under `www/`. The `web/apps/api`
and `web/packages/db` files that PRs #6 and #7 do touch are a Fastify API server
and SQL migrations, not user interface and not his code.

The consequence: the UI preference rule has no pull request to apply to in this
queue. It is not being relaxed and it is not being reinterpreted onto `www/` by
implication. It simply does not fire. If a later pull request does reach `www/`
or a real UI package, the rule stands as written and his choices win.

**CODEOWNERS cannot express the rule.** `.github/CODEOWNERS` assigns every path
to `@VirSanghavi`; `maksymrajszewski` appears nowhere in it, and there is no
entry for `www/`. So "request his review through the CODEOWNERS flow" has no
mechanism behind it today: a pull request touching his code would auto request
nobody. Recorded as a follow up rather than fixed inside a merge, because
changing code ownership is an ownership decision, not a merge mechanic.

## Inventory

All ten are authored by `VirSanghavi`, all target `main`, none are drafts, and
all ten were opened between 2026-08-27 03:30Z and 2026-08-28 01:05Z. Age below
is measured from 2026-08-28 02:45Z.

| PR | Branch | Age | Files | +/- | Class | CI |
|----|--------|-----|-------|-----|-------|-----|
| #5  | `auth-adapters` | 22h | 40 | +6108 / -49 | foundation, schema | all green |
| #2  | `subsetting` | 23h | 32 | +6497 / -431 | foundation, engine | all green |
| #3  | `supabase-provider` | 23h | 13 | +3531 / -21 | engine | all green |
| #8  | `dblab-provider` | 22h | 14 | +3513 / -4 | engine | all green |
| #9  | `kube-runtime` | 21h | 36 | +6591 / -83 | engine | all green |
| #11 | `insights` | 21h | 30 | +5785 / -128 | engine, workflow | all green |
| #7  | `resilience` | 22h | 45 | +9058 / -70 | engine, control plane | all green |
| #10 | `ee-secrets` | 21h | 57 | +10074 / -43 | engine, ee, workflow | all green |
| #12 | `infra-live` | 2h | 26 | +1582 / -126 | infrastructure | **1 failing** |
| #6  | `ee-sso` | 22h | 54 | +11421 / -47 | control plane, ee, workflow | all green |

Every one of the ten is phase work, agent authored, carrying a spec number in
its title. **None is a dependency update.** PRs #7 and #9 do modify
`engine/go.mod`, but as a side effect of feature work, not as a bump.

### What each one is

- **#5 `auth-adapters` (3.6)**: personas, so the agent signs in as somebody who
  exists. Changes `schemas/manifest.v1.json` and `engine/pkg/schema/manifest.go`.
  The only manifest schema change in the queue, which is why it goes first.
- **#2 `subsetting` (3.5, 3.10)**: subsetting and the golden lifecycle. Changes
  `engine/pkg/provider/db.go`, the database provider interface that #3 and #8
  implement against.
- **#3 `supabase-provider` (3.9)**: the Supabase database provider.
- **#8 `dblab-provider` (3.9)**: the Database Lab Engine provider.
- **#9 `kube-runtime` (Lane 4)**: a runtime conformance suite proved able to
  fail, plus the Kubernetes runtime. Changes `engine/pkg/provider/runtime.go`.
- **#11 `insights` (3.11)**: makes the three manifest configured insights real.
- **#7 `resilience` (14.6, 14.8, 14.10)**: observability, chaos, and a disaster
  recovery drill. Touches `engine/`, `observability/`, and `web/apps/api`.
- **#10 `ee-secrets` (13.8, 13.12)**: enterprise secret stores, the last two
  keyrings, compliance packs that can say no.
- **#12 `infra-live`**: the Azure control plane, and the difference between a
  clean plan and a working apply. Pure `infra/terraform` plus `tools/azguard`.
- **#6 `ee-sso` (13.2, 13.3)**: single sign on and SCIM, and the route extension
  point they needed. Touches `web/apps/api`, `web/packages/db`, `ee/web`.

## Contention: what will actually conflict

Every pull request modifies `docs/plan/STATUS.md`, so every rebase after the
first conflicts there. Each adds its own lines; resolution takes the pull
request's own entry and keeps main's accumulated ones.

Six pull requests (#2, #5, #8, #9, #10, #11) modify the error catalog trio:
`engine/internal/errors/catalog.yaml`, `engine/internal/errors/codes.gen.go`,
and `docs/src/content/docs/reference/errors.md`. Only the first is hand written.
The other two are generated by `just generate`, and per the rules they are
regenerated after each rebase rather than hand merged. `just _generated` then
proves the result is current.

Seven pull requests (#2, #3, #5, #7, #8, #9, #10) modify
`engine/internal/env/env.go`, the provider selection core. These are genuine
hand merges and the reason the order below front loads the interface changes.

Narrower collisions: `justfile` (#6, #10), `.github/workflows/ci.yml` (#6, #11),
`engine/go.mod` (#7, #9), `engine/internal/proxyimage/sources.gen.go` (#5, #9,
generated), `engine/internal/env/provider_selection_test.go` (#3, #8, #9), and
`web/apps/api/src/server.ts`, `src/limits.ts`, `package.json` (#6, #7).

## Planned merge order

Foundation and schema, then engine, then infrastructure, then control plane.
Each pull request is rebased onto the `main` that the previous merge produced,
so every rebase sees a stable base and the interface changes land before their
consumers.

1. **#5 `auth-adapters`**: the manifest schema moves first, alone.
2. **#2 `subsetting`**: the database provider interface, before its two
   implementations.
3. **#3 `supabase-provider`**: first consumer of that interface.
4. **#8 `dblab-provider`**: second consumer. It shares
   `provider_selection_test.go` with #3, so it follows rather than races it.
5. **#9 `kube-runtime`**: the runtime provider interface and conformance suite.
6. **#11 `insights`**: engine work on top of the settled manifest schema.
7. **#7 `resilience`**: engine plus observability. Lands before #6 so that the
   shared `web/apps/api` files are resolved once, in #6's rebase.
8. **#10 `ee-secrets`**: enterprise engine surface. Carries a `justfile` edit
   that #6 will rebase over.
9. **#12 `infra-live`**: infrastructure, isolated from all of the above.
10. **#6 `ee-sso`**: control plane and `ee/web`, last, so it absorbs the
    `justfile`, `ci.yml`, and `web/apps/api` changes rather than being absorbed.

## Gates each pull request must clear before it merges

Non negotiable, in this order, after the rebase and before the merge:

- `just gate` locally, all 26 gates green.
- `gitleaks detect` and `trufflehog git` over the branch's **full history**, not
  just its diff.
- Every new or modified test run **20 times with `-race`**.
- The gate check green in CI on the rebased head.
- `just leaks` after any merge touching runtime or providers.

No test is skipped, weakened, deleted, or quarantined to get a merge through. A
pull request that cannot pass without that stays open, gets `needs-human`, and
the reason is recorded below.

## Known blocker before we start

**#12 `infra-live`** fails the `known vulnerabilities` gate: `GO-2026-5970`, an
infinite loop on invalid input in `golang.org/x/text`, reachable and not
accepted in `.govulncheck.yaml`. `golang.org/x/text` is pinned at `v0.41.0` in
`engine/go.mod` and `examples/go-api/go.mod`. `.govulncheck.yaml` says the bar
for an exception is "this vulnerability cannot be reached through the way we use
this dependency", not "upgrading is inconvenient", and that if the fix exists,
take the fix. So the fix is a dependency bump, not a new allow entry. Adding one
would be weakening a gate to get a merge through, which the rules forbid.

## Secret scanning: done, and clean

Both scanners were run over the **full history of all ten branches** plus `main`
as a baseline, before any merge. Result: **no real credential on any branch.**

`gitleaks` reports 11 findings on `main` itself, so a raw count proves nothing;
what matters is what each branch adds on top of that baseline. Nine of the ten
branches add zero. `auth-adapters` adds three, and all three are synthetic:

- `runner/test/totp.test.ts` and `engine/internal/personas/totp_test.go` carry
  `GEZDGNBVGY3TQOJQ`, which is base32 for `12345678901234567890`, the seed
  published in RFC 6238 Appendix B. It is a public test vector.
- `engine/internal/personas/api_test.go` carries `sk_test_thisisthesecret`, in a
  test whose entire purpose is proving the admin token never reaches a URL.

The 11 baseline findings on `main` are the same shape: fake keys inside
`internal/redact`, `internal/verify` and `internal/secrets` test corpora, which
is what those packages exist to detect.

`trufflehog` found **zero verified secrets on every branch**, including `main`.
Its unverified findings are all the `Postgres` detector firing on connection
strings in `_test.go` files. `resilience` adds four (telemetry redaction tests,
which contain strings like `postgres://app:never-registered-either@db:5432/app`
precisely so the redactor can be proved to catch them) and `ee-secrets` adds
three (keyring round trip tests using `postgres://u:p@ss w0rd!"$&@host:5432/db`
to prove awkward characters survive the store). Both are fixtures doing their
job.

One methodology note worth keeping: trufflehog silently scanned **nothing** on
the first attempt. Pointed at a bare mirror it exits 0, prints no findings, and
reports `"chunks":0` in a stderr line nobody reads. A zero from it is only
meaningful next to a non zero chunk count, which is why the counts above are
recorded. The real runs cover roughly 8,500 to 9,000 chunks and 95 MB each.

## The environment cannot currently run `just gate`

`main` itself does not pass `just gate` on this workstation, and the code is not
the reason. Two full runs failed at the first gate, `generated files are
current`, when `engine/internal/masking` hit the Go test binary's 10 minute
timeout. Run on its own, the same test passes:

```
ok  github.com/antifailure/antifailure/engine/internal/masking  78.839s
```

79 seconds alone, more than 600 under the gate. That is contention, not a
regression, and the justfile predicts it in its own preamble: it measures load
before running and warns that "timing gates can fail here while nothing is
wrong". Load average on this 8 core machine sat between 40 and 59 throughout,
driven by processes unrelated to this repository.

Part of it was this repository's own mess, and that part is fixed. A leaked
`k3d` conformance cluster had been running for four hours and was burning 403
percent CPU, four of the eight cores, on its own. A `postgres` container from a
disaster recovery test had been up for 14 hours. Eight `alpine` containers sat
in `Created` state, never started and never removed. All of them are gone and
`just leaks` now reports `containers: 0, networks: 0`. Clearing them was not
enough: load stayed above 50 without them.

**A test that creates eight containers and never removes them is a real defect**
in whatever suite created them, and it is recorded below as a follow up rather
than fixed inside somebody else's merge.

## Outcome

Nothing has been merged. The inventory, the merge order, and the secret scans
are complete; the gate that every merge depends on cannot be trusted on this
machine until it is quieter, and no pull request will be merged on a gate result
that this document cannot stand behind.

## Follow ups to open

1. **CODEOWNERS does not name the cofounder.** No entry for `maksymrajszewski`
   and no entry for `www/`, so a pull request touching his marketing site
   requests review from nobody. The UI preference rule has no teeth without it.
2. **A suite leaks containers.** Eight `alpine:3.20` containers labelled
   `dev.antifailure.managed` were left in `Created` state, plus a `k3d` cluster
   and a stray `postgres`. `just leaks` catches them after the fact; something
   should not be creating them.
3. **`GO-2026-5970` in `golang.org/x/text`.** Reachable, unaccepted, and failing
   `known vulnerabilities` on #12. Needs the dependency bumped in `engine/go.mod`
   and `examples/go-api/go.mod`.
4. **No open pull request carries a `.changes/` fragment**, though CONTRIBUTING
   requires one of every pull request and the pull request template has a
   checkbox for it. Ten fragments are missing.

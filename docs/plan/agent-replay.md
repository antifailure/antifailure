# Antifailure agent replay: overnight implementation plan

Date: 2026-09-28. Execution authorized by the project owner. Target: tested changes on `antifailure/antifailure` main, through the repository's protected merge path.

## Delivery contract

Deliver the PRD's local Phase 1 vertical slice. A TypeScript agent records an opted-in incident. A developer saves it with a verified Postgres golden and explicit outcome assertions, reproduces the original failure, tests another revision in an independent environment, and retains the case for CI. Missing evidence produces `INCONCLUSIVE`. A failed outcome produces `FAIL` only after the control reproduces. `PASS` requires complete evidence and confirmed teardown.

This authorizes engineering implementation, not a claim of customer validation. The planted demonstration proves the implementation. Phase 0 demand and a real customer's historical coverage remain unvalidated. Hosted storage, a console dashboard, Python, browser/queue restoration, PITR, live-model experiments, and automatic production fixes are outside this release.

## Source review and integration constraints

Reviewed main: `51e431f1d1067babe6d0fd85ea632cf228699a56`, newer than the PRD's source revision. Recheck main and open work immediately before integrating.

| Existing source | Use in the implementation |
| --- | --- |
| `README.md`, `CONTRIBUTING.md`, `justfile` | Setup, attribution, generated files, tests, release gates and `just merge` |
| `engine/internal/env/env.go` | Environment lifecycle, unique branch identities, build roots, pinned goldens, resource journals |
| `engine/internal/env/oracle.go` | Isolated revision archives and independent state branches; do not invoke its candidate-first lifecycle |
| `engine/internal/oracle/` | Database capture and comparison when the declared tables and requests support it |
| `engine/internal/env/golden.go` | Verified golden lookup, project identity, lifetime and retention |
| `engine/internal/runtime/local/` | Contained services and externally observed egress evidence |
| `engine/internal/journal/` | Idempotent teardown and crash recovery |
| `runner/src/cassette.ts` | Preserve existing runner behavior; production-agent capture has a separate versioned contract |
| `engine/internal/telemetry/` | Existing redaction and trace correlation conventions |
| `engine/internal/cli/`, `engine/internal/mcp/` | Register reachable interfaces and preserve output/exit conventions |

The open security PR [588](https://github.com/antifailure/antifailure/pull/588) overlaps credential checks and complete egress reads. Inspect its final state before changing shared files. Do not merge unrelated work or assume its protections already exist on main. The new strict replay policy must prevent external access independently.

Searches covered open and closed issues/PRs, current source paths and recent history. Existing load scenarios, runner cassettes and PostgreSQL crash replay are separate capabilities, not the SDK requested here.

## Architecture decisions

1. **Explicit Node/TypeScript integration.** Wrap an agent run and named model, tool and HTTP boundaries. No monkey-patching claim. Propagate context across async work. Preserve the host application's results and exceptions when recording fails. Return an incomplete-capture signal through a diagnostic hook and retained metadata where the writer remains available.
2. **Local immutable artifacts.** Write redacted bounded payloads before publishing manifests. Use content digests, exclusive/atomic publication, schema checks, idempotent run identifiers and per-record decoding. Keep drafts distinct from ready scenarios. One corrupt record must leave siblings inspectable.
3. **A pinned experiment.** Freeze source revision, application harness, input, golden identity, dependency records, assertion definitions and clock contract. A candidate revision belongs to a replay attempt. User-supplied assertions and policy come from the approved scenario, never the candidate's response.
4. **Strict replay only.** Match full request identities, including system instructions, model settings and tool versions. Consume repeated requests in recorded order. Reject ambiguous concurrency, missing entries, changed requests and unsupported boundaries. Never call a production fallback. Report the first divergence even if application code catches a cassette error.
5. **Run customer code in contained application services.** The local engine sends a bounded replay request to an explicitly enabled SDK endpoint. The SDK supplies cassette-backed observations and a declared clock to the agent. Real application database operations execute against the branch. The control plane and host process never import customer code.
6. **Independent control and candidate.** Bring up the original revision first, confirm its specified wrong outcome, then evaluate the candidate in its own branch of the same golden. Use a unique attempt identifier, independent cassette cursors and journaled environments. Capture before/after database facts and proxy observations. Always tear down both sides, including failed startup.
7. **Conservative fidelity.** A pinned current golden is approximate historical state. Record its age and identity. Unsupported clock use, missing identities, absent content or unobserved dependencies prevent stronger claims. No `incident-equivalent` label without proof for every outcome-relevant boundary.
8. **CLI and machine-readable evidence first.** Provide incident inspection/save, replay, eval execution and recovery. JSON and text read the same result. Reuse native CLI formatting; no new web UI is needed for Phase 1. Expose bounded inspection/replay through MCP where repository surface parity requires it.

Alternatives: adapting the existing runner cassette would confuse customer-agent capture with Antifailure's own agent. Building a hosted trace store would expand scope before the replay path works. Extending the oracle by changing its lifecycle would risk existing users' expectations. A separate replay coordinator reusing its primitives is the selected approach.

## Work packages and completion evidence

| Order | Work | Completion evidence |
| --- | --- | --- |
| 0 | Verify current main, prior work, instructions, toolchain and Docker; save this plan and a compact decision record | Exact source SHA, available runtime, overlapping work identified |
| 1 | Define versioned incident, dependency, scenario, attempt and SDK protocol; implement validators and bounded local artifact store | Round-trip fixtures shared between TypeScript and Go; malformed siblings survive; partial writes never become ready |
| 2 | Implement SDK capture, privacy boundary, async context, keyed hashes, opt-in content, strict cassettes and replay handler | Actual wrapped agent captures and replays; recording failure leaves host behavior intact; redaction failures mark incompleteness |
| 3 | Wire save/inspect, pin checks and immutable scenario creation | Real CLI imports SDK capture, identifies missing records, saves only validated scenarios, lists damaged records by name |
| 4 | Implement baseline-first coordinator, frozen harness, revision builds, independent branches, effects/database evidence and teardown | Real Node service and Postgres reproduce the planted bug and verify the fix, with no public egress |
| 5 | Add eval suite, report history and recovery; wire public surfaces | Real CLI runs multiple saved cases; replay can be inspected/recovered; missing evidence has nonzero exit |
| 6 | Run adversarial and ordering matrix; trace every public capability to its caller | Behavioral tests below, explicit unused-symbol pass, positive and negative controls |
| 7 | Documentation, examples, generated outputs, changelog and repository checks | Fresh package install and documented demo work; relevant checks and required CI pass |
| 8 | Land through `just merge`; watch checks and deployment for the merged SHA | Main commit, CI URLs/conclusions, deployment outcome and scope-specific smoke evidence |

Every package is wired into an executable path before calling it complete. Tests that merely search for function names or strings do not satisfy any behavioral acceptance criterion.

## Required behavioral matrix

| Scenario | Required observable outcome |
| --- | --- |
| Metadata-only capture | No prompt, completion, user input, tool body or raw credential in persisted/exported bytes; diagnosis remains possible |
| Opted-in capture | Only allowed bounded fields retained; denied fields redacted before persistence |
| Redactor or disk failure | Host agent preserves result/error; capture visibly incomplete |
| Async tools, nested operations, parallel runs | Parent/run correlation stays correct; no cross-run cassette or context leakage |
| Checkpoint before trace / trace before checkpoint / concurrent | Same valid scenario once both prerequisites are durable |
| Checkpoint without trace / trace without checkpoint | Named missing dependency; never replay-ready |
| Response before save / save before response / response absent | Reconciliation or explicit expiry; no dangling ready manifest |
| Store succeeds and acknowledgment is lost | Retrying the same idempotency key creates one logical incident |
| Interrupted blob/manifest publication | Prior valid state remains readable; retry completes or reports a named conflict |
| One malformed incident among good incidents | Good incidents remain listed; bad incident is named |
| Changed system prompt/model/settings/tool schema | Cassette miss, first divergence and `INCONCLUSIVE`; zero fallback calls |
| Identical requests repeated | Correct recorded occurrence consumed; exhaustion cannot reuse an earlier response |
| Baseline reproduces specified bug, candidate correct | `PASS` only after evidence and both teardowns are confirmed |
| Baseline unexpectedly correct | Candidate verdict withheld as `INCONCLUSIVE` |
| Candidate repeats bug | `FAIL`, with the failed assertion named |
| Candidate fails differently | Asserted outcome evaluated only if experiment remains valid; infrastructure/evidence errors are `INCONCLUSIVE` |
| Missing golden/blob, hash mismatch, incompatible schema, identity gap | Specific `INCONCLUSIVE`, recovery action, no unsafe startup |
| Catch and suppress cassette error inside agent | Coordinator still sees incomplete evidence and refuses `PASS` |
| Direct HTTP, redirects, raw sockets, model/payment/email attempts | Network containment prevents production access; refused effects are visible |
| Candidate tries to modify policy/assertions | Frozen engine-side experiment remains authoritative |
| Baseline writes / candidate writes | Neither side changes the other side or golden |
| Two concurrent replays | Unique environments, database state, cassette cursors and journals |
| Crash before/during teardown or after report write | Recovery removes resources; report cannot retain an unearned `PASS` |
| Teardown provider failure | Named pending resources and `INCONCLUSIVE`; retry is idempotent |
| Saved case with deliberately reintroduced bug | Nonzero CI result and `FAIL` |
| Saved case with removed required cassette entry | Nonzero CI result and `INCONCLUSIVE` |

## Verification and shipping procedure

Use the repository-pinned Go and Node versions. Run targeted Go tests with the race detector, SDK tests/typecheck/package-install smoke, parser fuzz/property tests, and the real Docker/Postgres integration test. Do not use a Docker skip as evidence of end-to-end success. Inspect real text/JSON output, including narrow terminal formatting and error recovery instructions.

Run applicable generated-file, error-catalog, public-surface, parity, dead-code, documentation, changelog, attribution and formatting gates. Run the full repository gate and diagnose failures; compare failures outside the change against unchanged main before attributing them. Preserve logs without customer data or secrets.

Main currently requires: engine, control plane, edition boundary, enterprise, runner, www, known vulnerabilities, no credentials in the tree, and commits are attributed to their author. Fetch protection again before merging. Commit with the configured real identity and DCO sign-off. Use the repository PR template and `just merge`, never bypass protection or force-push main.

After each push, watch checks for that exact SHA. Fix relevant failures and repeat. After merge, watch main CI and CD for the merged SHA, then inspect the deployed artifact and appropriate smoke path. A source merge does not imply a tagged CLI release or an npm publication; report those separately and do not publish a package or create a release tag as an incidental step.

An overnight target is a scheduling objective, not a reason to waive a gate. If a hard external prerequisite cannot be met, preserve the completed work and its evidence and report the exact blocker. Do not report shipped while checks, containment, teardown or deployment are unknown.

## Evidence to retain

Final commit and PR links; exact test commands and summaries; baseline/fixed/regressed/missing-evidence reports; environment identifiers and zero-resource teardown inventory; capture privacy scan; cassette hit/miss counts; capture overhead and byte counts measured on the synthetic demo; main CI and deployment conclusions. Mark customer reproduction rate and production overhead as unmeasured.

Reference constraints: [OpenTelemetry JavaScript instrumentation](https://opentelemetry.io/docs/languages/js/instrumentation/) for context integration; [PostgreSQL PITR](https://www.postgresql.org/docs/17/continuous-archiving.html) for the historical-recovery boundary. This MVP pins a verified golden and implements no PITR adapter.

# Status

The honest answer to "does it do X yet". Every component carries one of three
states and nothing else:

| State | Means |
| --- | --- |
| **proven** | The code exists, its tests pass, and the behavior has been exercised end to end against the real thing. |
| **written** | The code exists and passes its tests against a fake that enforces the real service's validation rules. It has never talked to the real service. |
| **planned** | Specified, not built. |

The distinction between proven and written is load bearing. A provider that has
only ever spoken to a fake is written, no matter how good the fake is.

This file is updated in the same pull request as the code it describes.

## What works today

```
af up          builds every service, branches a Postgres golden, seals the
               network, and prints where the app is
af down        removes every container, network, and database branch it made
af status      what is running for this branch, and where
af net policy   the effective egress policy, in the order that decides
af net explain  what would happen to one request, and which rule decides it
af doctor      ten checks, each with a remediation
af init        reads a repository, writes a manifest, explains what it assumed
af explain     shows the effective configuration with every default resolved
af version     version, commit, edition, platform
```

Everything else in the command tree exists and returns AF-RUN-001.

Proved end to end on a Node repository with no Dockerfile: a generated
build, a Postgres branch reachable at `db:5432` from inside the environment,
the app serving on localhost, and nothing left on the daemon after `af down`.

### What the containment is, exactly

Today an environment has **no route to the internet at all**. That is not the
per host policy the manifest describes; the policy engine decides correctly and
nothing enforces it per host yet, because the proxy sidecar is Phase 5.

The seal itself is real and measured. The first implementation disabled IP
masquerading, which looks like it removes a container's route out and does not:
on Docker Desktop the traffic is translated again by the virtual machine's own
gateway, and a supposedly sealed service still reached 1.1.1.1. What works is an
internal network, with a small forwarder per web service publishing it back to
the host. There is a test for the seal and a negative control beside it, so the
seal cannot pass because the probe was broken.

## Phase 1. Foundation

| Sub-phase | State | Notes |
| --- | --- | --- |
| 1.1 Repository and governance | proven | Governance files, templates, CODEOWNERS, ADRs 0001 and 0002. |
| 1.2 Toolchain pinning and task runner | planned | |
| 1.3 Schemas and code generation | proven | `schemas/manifest.v1.json` is the source of truth; Go types mirror it. TypeScript generation lands with the runner. |
| 1.4 Continuous integration and gates | planned | |
| 1.5 Release pipeline | planned | |
| 1.6 Security baseline | planned | |
| 1.7 Documentation site skeleton | planned | |
| 1.8 Azure foundation (Terraform) | planned | Isolation boundary documented in `infra/ISOLATION.md`. Blocked on Q4. |
| 1.9 Test infrastructure and fakes | planned | |
| 1.10 Events, logging, and redaction | proven | 100 percent coverage on redaction, 454 ns/op with no allocations. |
| 1.11 Local state store and journal | proven | Crash injection at every step, plus a property test over random interleavings. |

## Phase 2. Engine core and CLI

| Sub-phase | State | Notes |
| --- | --- | --- |
| 2.1 CLI framework | proven | Whole tree present; unimplemented commands return AF-RUN-001 and exit 2. |
| 2.2 Manifest loader and validator | proven | Fuzzed. Unknown keys are errors with a line and a suggestion. |
| 2.3 Detection engine | proven | Twelve analyzers. Deterministic, bounded, fuzzed, never executes repository code. |
| 2.4 `af init` | proven | Validates its own output before writing. |
| 2.5 Secrets subsystem | planned | `secrets.Value` exists and is proven. Sources and adapters are next. |
| 2.6 `af doctor` | proven | |
| 2.7 HUD | planned | |
| 2.8 Event sinks | proven | NDJSON with rotation, JSON, memory, and a replay reader. |

## Supporting packages

| Package | State | Coverage |
| --- | --- | --- |
| `internal/clock` | proven | 88 percent |
| `internal/secrets` | proven | 100 percent |
| `internal/redact` | proven | 100 percent |
| `internal/errors` | proven | 100 percent |
| `internal/events` | proven | 94 percent |
| `internal/journal` | proven | 95 percent |
| `internal/state` | proven | 83 percent |
| `internal/lock` | proven | 78 percent |
| `internal/manifest` | proven | 87 percent |
| `internal/detect` | proven | 81 percent |
| `internal/cli` | proven | 73 percent |

## Phase 3. Postgres data layer

| Sub-phase | State | Notes |
| --- | --- | --- |
| 3.1 Provider interface and conformance suite | proven | 23 behaviors in `engine/conformance`. A behavior a provider cannot support is skipped explicitly, naming the capability. |
| 3.2 Docker provider | proven | 21 behaviors pass against a real daemon, 2 skip with named reasons, zero resources left behind across repeated runs. |
| 3.3 Masking engine | partial | The 22 transforms and the key hierarchy are proven at 95 percent. The rules model, classifier, SQL compiler, and resumable executor are next. |
| 3.4 Verification scanner | partial | The 9 detectors are proven at 94 percent. The streaming table scan and the signed attestation are next. |
| 3.5 Subsetting | planned | |
| 3.6 Authentication adapters | planned | |
| 3.7 to 3.9 Neon, Supabase, DBLab | planned | Blocked on Q5: no accounts provisioned. |
| 3.10 Golden lifecycle | planned | |
| 3.11 Postgres Insights | planned | |

## Supporting packages, phase 3

| Package | State | Coverage |
| --- | --- | --- |
| `internal/masking` | proven | 95 percent |
| `internal/verify` | proven | 94 percent |
| `pkg/provider` | proven | interface only |
| `engine/conformance` | proven | 23 behaviors |
| `internal/db/docker` | proven | full suite green against a real daemon |

## Phase 4. Build and runtime

| Component | State | Notes |
| --- | --- | --- |
| `internal/dockerutil` | proven | one label scheme for every component that creates Docker resources; filters match on the label being present, not its value, so a resource an older release stamped differently is still found |
| `internal/build` context | proven | deterministic tar, content digest as the cache key, symlinks out of the root dropped |
| `internal/build` ignore | proven | `.dockerignore` implemented here rather than added as a dependency |
| `internal/build` buildpacks | proven | Go, Node, Python, Ruby; the first three are built against a real daemon in CI, so a generated Dockerfile that does not build fails the suite |
| `internal/build` docker | proven | image tag derived from context, Dockerfile, target, and args; build output redacted at the writer |
| `internal/runtime/local` | proven | 12 behaviors against a real daemon, seal measured with a negative control |
| `internal/env` | proven | lock, state, journal, database, build, runtime, in the one order that works |
| `internal/policy` | proven | 100 percent, 48ns per decision, zero allocations |

## Phase 5. Egress control

| Sub-phase | State | Notes |
| --- | --- | --- |
| Policy decision function | proven | `internal/policy`, and `af net policy` and `af net explain` run off it |
| Proxy sidecar | planned | until it lands, an environment has no route out at all rather than a per host one |
| Capture, mock, sandbox, synth | planned | |
| Inbox and webhooks | planned | |

## Phases 6 to 14

Not started.
## Where to pick up

In order of what unblocks the most:

1. The proxy sidecar, so that egress becomes the per host policy the manifest
   already describes and `af net explain` already answers about. The decision
   function is done and proven; what is missing is the thing that consults it
   on a real connection. This is also what turns the ingress forwarder into
   the component it was designed to become.
2. The masking rules model, SQL compiler, and resumable executor, so that
   `af mask plan` and `af mask apply` work against a real database. The
   transforms and the key hierarchy are done; what is missing is reading the
   Postgres catalog, compiling chunked UPDATE statements in dependency order,
   and checkpointing per chunk.
3. `internal/verify`'s streaming table scan and signed attestation, so that a
   golden can be marked verified for real rather than by the Docker provider's
   current assumption that a committed image was verified.
4. `af logs`, which is a small amount of work over what the runtime already
   does and is the first thing anybody asks for when a service does not start.
5. The agent runner, Phase 6, which is where bring your own model key lands.

Three notes for whoever picks this up. The conformance suite is not yet tested
against a deliberately buggy provider, so it is not yet proved that every
subtest can fail; that is worth doing before a second provider is written
against it. And the Docker provider reports every committed image as verified,
which is true by construction today because the commit is the last step of a
refresh, but will stop being true once goldens can be imported. And `af up`
creates an empty golden when no source database is configured, so the schema
arrives with the migrations rather than from production; masking and
verification run and trivially pass on no rows, which is the honest answer
rather than a skipped step.

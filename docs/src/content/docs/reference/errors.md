---
title: Error reference
description: Every error Antifailure can return, what causes it, and what to do about it.
sidebar:
  order: 6
---

Every user facing error carries a code of the form `AF-<AREA>-<NNN>`.
This page is generated from `engine/internal/errors/catalog.yaml`, so it
cannot fall behind the code: a code with no entry here fails the build, and
an entry that nothing returns fails it too.

## Exit codes

Scripts can branch on these. They are stable.

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | A generic failure. The message says what. |
| `2` | The command was used incorrectly. |
| `3` | Configuration is wrong or incomplete. |
| `4` | Authentication or authorization failed. |
| `5` | A provider failed. Often retryable. |
| `6` | A policy denied the operation. |
| `7` | Verification failed. Masking or an invariant. |
| `8` | A test failed. Agent verdicts or load thresholds. |
| `9` | Interrupted, and teardown completed cleanly. |
| `10` | Interrupted, and resources are still recorded. Run `af down` again. |

30 further codes are reserved for features this version does not have. They are in `engine/internal/errors/catalog.yaml` and are left out here because this page is for looking up an error you have actually seen.

## Agents

### AF-AGT-001

The agent runner could not be started: {detail}

**What to do.** Run 'af doctor' to check that the runner and its browsers are installed.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/agents](/docs/concepts/agents/) |

### AF-AGT-002

Workflow {workflow} exhausted its budget of {budget} before completing.

**What to do.** Raise the budget for {workflow} in the manifest, or split it into smaller workflows.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/workflows](/docs/guides/workflows/) |

### AF-AGT-003

The agent runner produced no readable output: {detail}

**What to do.** This is the runner's own failure and not the application's; the output above is what it printed.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/agents](/docs/concepts/agents/) |

### AF-AGT-004

The agent runner could not be found: {detail}

**What to do.** Install it with 'af runner install', or point at a checkout with --runner.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/agents](/docs/concepts/agents/) |

### AF-AGT-010

Invariant {invariant} did not finish within {timeout}.

**What to do.** Make the invariant cheaper; it runs after every workflow and must be a quick read.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/invariants](/docs/guides/invariants/) |

### AF-AGT-011

Invariant {invariant} is not read only.

**What to do.** Rewrite it as a single SELECT; invariants run inside a read only transaction.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/invariants](/docs/guides/invariants/) |

### AF-AGT-012

Invariant {invariant} does not hold: {detail}

**What to do.** The rows the statement returned are the violation. Run it against the branch to see them all.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/invariants](/docs/guides/invariants/) |

### AF-AGT-020

There is nothing to explore: {detail}

**What to do.** Add a goal under explore in the manifest, and set explore.enabled to true.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/exploration](/docs/concepts/exploration/) |

### AF-AGT-021

No goal named {goal} is declared under explore.

**What to do.** Run 'af explain' to see the goals this manifest declares, then check the spelling.

| | |
| --- | --- |
| Exit code | `2` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/exploration](/docs/concepts/exploration/) |

## Build

### AF-BLD-001

The build for service {service} failed after {duration}.

**What to do.** Read the build log above; the first error line names the step that failed.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/build](/docs/guides/build/) |

### AF-BLD-002

The Dockerfile for {service} is not valid at line {line}: {detail}

**What to do.** Fix the line and run 'af up' again.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/build](/docs/guides/build/) |

### AF-BLD-003

The build context for {service} is {size}, above the {limit} limit.

**What to do.** Add large directories to .dockerignore; the build does not need them.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/build](/docs/guides/build/) |

### AF-BLD-004

The build context for {service} holds more than {count} files; {path} is where the count was reached.

**What to do.** Add the generated directories to .dockerignore; a build context should hold source, not output.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/build](/docs/guides/build/) |

### AF-BLD-010

No build strategy could be detected for {service}.

**What to do.** Add a Dockerfile to {path}, or set services.{service}.build to a strategy the reference lists.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/build](/docs/guides/build/) |

## Control plane

### AF-CPL-001

No control plane token is configured.

**What to do.** Create an engine token in the control plane, then set AF_CONTROL_PLANE_TOKEN. Everything except this command works without one.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [self-hosting/control-plane](/docs/self-hosting/control-plane/) |

### AF-CPL-002

The control plane has no environment called {env}.

**What to do.** Check the identifier with 'af env list', or confirm the engine that created it was sending events to this control plane.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [self-hosting/control-plane](/docs/self-hosting/control-plane/) |

## Database

### AF-DB-002

The source database at {host} could not be reached.

**What to do.** Check that the host is reachable from this machine and that the connection string names the right port.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [providers/overview](/docs/providers/overview/) |

### AF-DB-003

The source database is Postgres {found}, and this provider supports {supported}.

**What to do.** Use a provider that supports Postgres {found}, or upgrade the source.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [providers/overview](/docs/providers/overview/) |

### AF-DB-004

The golden version {version} no longer exists.

**What to do.** Run 'af golden list' to see the available versions, then 'af up --golden <version>'.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/goldens](/docs/concepts/goldens/) |

### AF-DB-005

The golden version {version} is still referenced by {count} environments and cannot be collected.

**What to do.** Run 'af down' on those environments first, or leave the version in place.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/goldens](/docs/concepts/goldens/) |

### AF-DB-006

The provider's concurrent branch limit ({limit}) is reached.

**What to do.** Run 'af down' on unused environments or raise the limit in the provider settings.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [providers/limits](/docs/providers/limits/) |

### AF-DB-008

The database provider {provider} at {endpoint} rejected the configured credential.

**What to do.** Check the value of the variable named by database.api_key_env; the provider answered 401, so the credential reached it and was refused rather than being missing.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [providers/overview](/docs/providers/overview/) |

### AF-DB-009

The Database Lab Engine at {endpoint} has no snapshot to build a golden from: {detail}

**What to do.** Wait for the engine's own data retrieval to finish, then refresh again; its progress is at GET /instance/retrieval.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [providers/dblab](/docs/providers/dblab/) |

### AF-DB-011

The subset could not be taken: {detail}

**What to do.** Run 'af explain' to see the effective subset block, and check that the seed table and its predicate name columns this database has.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/subsetting](/docs/concepts/subsetting/) |

### AF-DB-020

Personas cannot be provisioned because {provider} creates users only through its own API, and no sandbox tenant is configured.

**What to do.** Point auth.url or auth.domain at a sandbox, development or staging tenant and set auth.sandbox: true, so that personas are never created in production.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/personas](/docs/guides/personas/) |

### AF-DB-021

{provider} rejected the admin token used to create personas.

**What to do.** Check that the variable named by auth.token_env holds a key for the sandbox tenant with permission to create users.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/personas](/docs/guides/personas/) |

### AF-DB-030

Migrations failed on the branch: {detail}

**What to do.** The rehearsal names the statement that failed and times the ones before it. Fix the migration and push again: a migration that fails on a branch with production's shape is one that would have failed in production.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/insights](/docs/concepts/insights/) |

### AF-DB-031

The migration finding {rule} fails this project's policy: {detail}

**What to do.** The report above names the table and the statement. Fix the migration, or lower the rule to 'warn' in the manifest's policy block.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/verdicts](/docs/concepts/verdicts/) |

## Detection

### AF-DET-001

No application could be detected in {path}.

**What to do.** Declare your services by hand in antifailure.yaml; the manifest reference lists the minimum fields.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/detection](/docs/concepts/detection/) |

## Enterprise

### AF-EE-004

The license covers {seats} seats and they are all in use.

**What to do.** Remove an inactive member, or contact licensing@antifailure.dev to add seats. No existing member was removed.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [enterprise/licensing](/docs/enterprise/licensing/) |

### AF-EE-010

Organization policy {policy} refuses this environment: {detail}

**What to do.** Ask an organization administrator to review {policy}, or bring the repository into compliance.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [enterprise/policy](/docs/enterprise/policy/) |

## Infrastructure

### AF-INF-002

The provider rate limited this operation and asked to wait {retry_after}.

**What to do.** The engine retries automatically. If this persists, lower the concurrency in the manifest.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [providers/limits](/docs/providers/limits/) |

## Load

### AF-LOD-010

Load could not be generated: {detail}

**What to do.** Bring the environment up with 'af up', and check the load section of the manifest.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/load](/docs/concepts/load/) |

### AF-LOD-011

Load exceeded {count} thresholds the manifest sets.

**What to do.** The breaches are listed above, worst first. Raise the threshold or fix the regression.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/load](/docs/concepts/load/) |

## Manifest

### AF-MAN-001

No antifailure.yaml was found in {path} or any parent directory.

**What to do.** Run 'af init' in the repository root to create one.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

### AF-MAN-002

The manifest at {path} is not valid: {detail}

**What to do.** Fix the reported line, then run 'af doctor' to revalidate.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

### AF-MAN-003

The manifest at {path} declares schema version {found}, which this build does not understand.

**What to do.** Upgrade with 'af version -check' and install the release that supports version {found}.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

### AF-MAN-004

'af init' needs a terminal to ask questions, and this session has none.

**What to do.** Pass --non-interactive together with the flags listed by 'af init --help'.

| | |
| --- | --- |
| Exit code | `2` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/cli#af-init](/docs/reference/cli/#af-init) |

### AF-MAN-005

The manifest at {path} is larger than the {limit} limit.

**What to do.** Split the configuration or remove generated content; a manifest describes services, it does not contain them.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

## Masking and verification

### AF-MSK-001

The golden {version} has no valid verification attestation and cannot be branched.

**What to do.** Run 'af golden verify {version}'; a golden is branchable only once verification has passed.

| | |
| --- | --- |
| Exit code | `7` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/verification](/docs/concepts/verification/) |

### AF-MSK-002

Verification found data matching {detector} in {table}.{column}.

**What to do.** Add a masking rule for {table}.{column} and refresh the golden. The value itself is never printed.

| | |
| --- | --- |
| Exit code | `7` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/verification](/docs/concepts/verification/) |

### AF-MSK-010

Masking could not run: {detail}

**What to do.** Run 'af mask plan' to see what was decided for each column.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/masking](/docs/concepts/masking/) |

## Egress

### AF-NET-001

The request to {host} was blocked by rule {rule}.

**What to do.** Add an egress rule for {host} with the mode you intend, or leave it blocked.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/egress](/docs/concepts/egress/) |

### AF-NET-002

{request} is not a request that can be explained: {detail}

**What to do.** Pass a method and a URL, as in 'af net explain GET https://api.stripe.com/v1/charges'.

| | |
| --- | --- |
| Exit code | `2` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/cli#af-net-explain](/docs/reference/cli/#af-net-explain) |

### AF-NET-010

No mock matched {method} {path} on {host}.

**What to do.** A fixture skeleton was written to {suggestion}. Fill it in and run again.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/mocking](/docs/guides/mocking/) |

### AF-NET-011

No message matching {match} arrived within {timeout}.

**What to do.** Check 'af net log' to see whether the request was refused, and 'af inbox list' for what did arrive.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/inbox](/docs/guides/inbox/) |

### AF-NET-012

The webhook could not be delivered to {service}: {detail}

**What to do.** Run 'af status' to check the service is up, and check the path against the manifest's webhook_path.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/webhooks](/docs/guides/webhooks/) |

### AF-NET-013

The environment tried to reach {hosts}, which nothing in the manifest mentions.

**What to do.** Add an egress rule for it with the mode you intend, or set policy.egress_surprise to 'warn' to let the attempt through the check.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/verdicts](/docs/concepts/verdicts/) |

## Differential oracle

### AF-ORC-001

The manifest declares no oracle block, so there is nothing to compare.

**What to do.** Add an oracle block with at least one probe; the manifest reference has the shape.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-002

The oracle is on and declares no requests to send.

**What to do.** Add at least one entry under oracle.probes. Both versions have to receive the same requests in the same order, so the plan is written down rather than discovered.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-003

The baseline revision could not be resolved: {detail}

**What to do.** Set oracle.base_ref to a branch, tag, or commit this checkout can see, and fetch it if it is a remote ref.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-004

The baseline and the candidate are both {commit}, so there is nothing to compare.

**What to do.** Commit the change, or point oracle.base_ref at the revision you meant to compare against.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-005

The baseline revision {commit} could not be checked out: {detail}

**What to do.** Check that the commit is present in this clone; a shallow clone often is not deep enough to reach it.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-006

There is no web service to send requests to in the {side} environment.

**What to do.** Declare a service of kind web in the manifest; the oracle compares HTTP responses and needs somewhere to send them.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-007

The baseline environment did not come up: {detail}

**What to do.** Bring the baseline revision up on its own with 'af up' from a checkout of it to see the build or migration failure in full.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-008

The {side} branch could not be read for comparison: {detail}

**What to do.** Check the branch is reachable, or turn the contents comparison off with oracle.database.enabled: false to compare responses alone.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-009

The golden version {version} the comparison pinned is no longer present or no longer verified.

**What to do.** Run the comparison again; both sides branch one golden and the one the candidate used has gone.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

### AF-ORC-010

The candidate behaves differently from the baseline: {detail}

**What to do.** Read the differences above. Each one is either the change you meant to make or a regression; raise oracle.fail_on if this class of difference is expected.

| | |
| --- | --- |
| Exit code | `8` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/oracle](/docs/concepts/oracle/) |

## Runtime

### AF-RUN-001

The command '{command}' is not available in this version.

**What to do.** See the roadmap for when it lands; 'af version' reports the version you are running.

| | |
| --- | --- |
| Exit code | `2` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/cli](/docs/reference/cli/) |

### AF-RUN-002

The Docker daemon at {endpoint} could not be reached.

**What to do.** Start Docker and run 'af doctor' to confirm; on macOS that is Docker Desktop.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-003

Another Antifailure process holds the lock for this branch (process {pid}, since {since}).

**What to do.** Wait for it to finish, or stop it and run 'af down' to clean up.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/journal](/docs/concepts/journal/) |

### AF-RUN-004

Service {service} did not become ready within {timeout}.

**What to do.** The last log lines are above. Check the health path {health} and the port the service binds.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-005

Service {service} exited with code {code} during startup.

**What to do.** The last log lines are above. Run 'af logs {service}' for the full output.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-009

No free port was found in the range {range} to publish the environment on.

**What to do.** Free a port in that range, or set runtime.port_from in the manifest to a range that is clear.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-010

Writing to {path} failed because the disk is full; {needed} is required.

**What to do.** Free space on the volume holding {path}, then run the command again.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-020

Docker has no room left for the environment: {detail}

**What to do.** Run 'docker system prune' or raise the disk limit in Docker's settings.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-030

The environment could not be torn down completely; {count} resources are still recorded.

**What to do.** Run 'af down' again once the provider is reachable; the journal remembers what is left.

| | |
| --- | --- |
| Exit code | `10` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/journal](/docs/concepts/journal/) |

### AF-RUN-040

The environment could not be placed: {detail}

**What to do.** Run 'af doctor' to check the runtime, then 'af down' to clear anything left behind.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-041

The services depend on each other in a cycle: {cycle}

**What to do.** Remove one of the depends_on entries; a cycle has no order that can start.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

### AF-RUN-042

Service {service} depends on {missing}, which the manifest does not declare.

**What to do.** Add a service called {missing}, or correct the depends_on entry.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

### AF-RUN-043

This cluster is not containing the environment: {detail}

**What to do.** Use a cluster whose CNI enforces NetworkPolicy rather than only accepting it, then run 'af up' again.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/kubernetes-runtime](/docs/guides/kubernetes-runtime/) |

### AF-RUN-044

This runtime cannot do that: {detail}

**What to do.** Use the runtime that supports it, or run the command against an environment placed by one that does.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/kubernetes-runtime](/docs/guides/kubernetes-runtime/) |

### AF-RUN-045

{kind} {name} was not created by this runtime, so it was not removed.

**What to do.** Remove it yourself if you meant to, or use an environment id this runtime placed. 'af env list' shows the ones it owns.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/kubernetes-runtime](/docs/guides/kubernetes-runtime/) |

## Secrets

### AF-SEC-001

The variables {names} are declared in the manifest but were not found in any configured source.

**What to do.** Add them to one of the searched sources: {sources}.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/secrets](/docs/guides/secrets/) |

### AF-SEC-002

The credential for {source} was rejected after one refresh: {detail}

**What to do.** Rotate the credential and store the new value where {source} reads it. A rejection that survives a refresh is a credential that was revoked or was never right, so retrying will not help.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/secrets](/docs/guides/secrets/) |

### AF-SEC-003

The value supplied for {name} carries a live credential prefix, and {name} is configured for sandbox use.

**What to do.** Point {name} at a sandbox credential; the environment must never hold a live key.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/secrets](/docs/guides/secrets/) |

### AF-SEC-004

The encrypted local store has no passphrase: no system keyring answered and AF_SECRET_PASSPHRASE is not set.

**What to do.** Set AF_SECRET_PASSPHRASE, or store the passphrase in the system keyring on a platform that has one. There is deliberately no default: a store encrypted with a passphrase everybody knows only looks encrypted.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/secrets](/docs/guides/secrets/) |

### AF-SEC-010

The environment certificate could not be created: {detail}

**What to do.** Run 'af doctor' to check the runtime, then bring the environment up again.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/egress](/docs/concepts/egress/) |


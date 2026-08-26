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

### AF-BLD-010

No build strategy could be detected for {service}.

**What to do.** Add a Dockerfile to {path}, or set services.{service}.build to a strategy the reference lists.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/build](/docs/guides/build/) |

## Control plane

### AF-CP-001

The control plane at {url} could not be reached.

**What to do.** Antifailure works without it. Unset control_plane.url to run fully locally.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [self-hosting/control-plane](/docs/self-hosting/control-plane/) |

### AF-CP-002

The control plane rejected this engine's token.

**What to do.** Run 'af login' to obtain a new token.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [self-hosting/control-plane](/docs/self-hosting/control-plane/) |

## Database

### AF-DB-001

The database provider {provider} is not registered in this build.

**What to do.** Set database.provider to one of: {available}.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [providers/overview](/docs/providers/overview/) |

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

### AF-DB-007

Extension {extension} is required by the golden and is not available on the target.

**What to do.** Install {extension} on the target, or remove its use from the schema before refreshing.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [providers/overview](/docs/providers/overview/) |

### AF-DB-010

The storage pool has {available} free and the operation needs {needed}.

**What to do.** Run 'af golden gc' to reclaim unreferenced versions, or grow the pool.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/goldens](/docs/concepts/goldens/) |

### AF-DB-020

Personas cannot be provisioned because {provider} creates users only through its own API.

**What to do.** Configure SANDBOX mode for {provider} so that personas are created in its sandbox tenant.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/personas](/docs/guides/personas/) |

### AF-DB-030

Migrations failed on the branch: {detail}

**What to do.** Read the migration log at {location}, fix the migration, and push again.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/insights](/docs/concepts/insights/) |

## Detection

### AF-DET-001

No application could be detected in {path}.

**What to do.** Declare your services by hand in antifailure.yaml; the manifest reference lists the minimum fields.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/detection](/docs/concepts/detection/) |

### AF-DET-002

Detection stopped after {budget} with partial results.

**What to do.** Add large directories to .gitignore or .dockerignore, or pass --detect-timeout to raise the budget.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/detection](/docs/concepts/detection/) |

### AF-DET-003

Services {first} and {second} both claim port {port}.

**What to do.** Give one of them a different port in antifailure.yaml.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/detection](/docs/concepts/detection/) |

## Enterprise

### AF-EE-001

The enterprise license could not be verified.

**What to do.** Reinstall the license with 'af license install'; the token may have been truncated in transit.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [enterprise/licensing](/docs/enterprise/licensing/) |

### AF-EE-002

The system clock is {skew} behind the last time this license was seen.

**What to do.** Correct the system clock. Enterprise features resume once it passes the recorded time.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [enterprise/licensing](/docs/enterprise/licensing/) |

### AF-EE-003

This license was issued for organization {issued_for} and this instance is {actual}.

**What to do.** Install the license issued for {actual}.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [enterprise/licensing](/docs/enterprise/licensing/) |

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

## GitHub

### AF-GH-001

The webhook signature did not verify.

**What to do.** Confirm that the webhook secret stored for this installation matches the one configured in GitHub.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/github](/docs/guides/github/) |

### AF-GH-002

The GitHub API rejected the request: {detail}

**What to do.** Check the App's permissions against the list on the GitHub integration page.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/github](/docs/guides/github/) |

## Infrastructure

### AF-INF-001

The cloud API returned a quota error for {quota} in {region}.

**What to do.** Request more {quota} in {region}, then run the command again.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [self-hosting/azure](/docs/self-hosting/azure/) |

### AF-INF-002

The provider rate limited this operation and asked to wait {retry_after}.

**What to do.** The engine retries automatically. If this persists, lower the concurrency in the manifest.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [providers/limits](/docs/providers/limits/) |

## Load

### AF-LOD-001

The load target {target} is not an environment this engine created.

**What to do.** Point the load run at an environment from 'af status'. Load is never generated against an external host.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/load](/docs/concepts/load/) |

### AF-LOD-002

The load run was aborted after the error rate exceeded {threshold} for {duration}.

**What to do.** The service is failing under this load. The report above shows the first failing endpoint.

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
| More | [reference/cli/af_init](/docs/reference/cli/af_init/) |

### AF-MAN-005

The manifest at {path} is larger than the {limit} limit.

**What to do.** Split the configuration or remove generated content; a manifest describes services, it does not contain them.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/manifest](/docs/reference/manifest/) |

### AF-MAN-006

The path {path} in the manifest resolves outside the repository.

**What to do.** Use a path relative to the repository root, with no leading slash and no parent directory segments.

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

### AF-MSK-003

The masking rule for {table}.{column} names a column that does not exist in the schema.

**What to do.** Remove the rule or correct the name; 'af mask plan' lists the columns it found.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/masking](/docs/concepts/masking/) |

### AF-MSK-004

Masking would violate the check constraint {constraint} on {table}.{column}.

**What to do.** Choose a format preserving transform for {table}.{column} that satisfies {constraint}.

| | |
| --- | --- |
| Exit code | `7` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/transforms](/docs/reference/transforms/) |

### AF-MSK-005

Masking is only permitted on a golden candidate, and {target} is a source database.

**What to do.** Run masking against a golden candidate; the engine never rewrites a source.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/masking](/docs/concepts/masking/) |

### AF-MSK-007

The transform on {table}.{column} produced duplicate values under the unique constraint {constraint}.

**What to do.** Use a transform that preserves uniqueness, such as email or uuid_remap, for {table}.{column}.

| | |
| --- | --- |
| Exit code | `7` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [reference/transforms](/docs/reference/transforms/) |

### AF-MSK-008

The columns {columns} hold free text and have no masking rule.

**What to do.** Give each column a rule, or allowlist it explicitly if it is known to hold no personal data.

| | |
| --- | --- |
| Exit code | `7` |
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

### AF-NET-004

A request to {host} carried a live credential in the {header} header and was blocked.

**What to do.** Replace the credential with a sandbox key; an environment must never hold a live one.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/egress](/docs/concepts/egress/) |

### AF-NET-005

The sandbox credential for {host} was rejected: {detail}

**What to do.** Check the sandbox key's permissions at the provider.

| | |
| --- | --- |
| Exit code | `4` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/sandbox](/docs/guides/sandbox/) |

### AF-NET-010

No mock matched {method} {path} on {host}.

**What to do.** A fixture skeleton was written to {suggestion}. Fill it in and run again.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/mocking](/docs/guides/mocking/) |

### AF-NET-020

{host} rejected the environment certificate, which usually means the client pins its own.

**What to do.** Set the host to ALLOW so that its traffic is not intercepted, or disable pinning in the client for previews.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/egress](/docs/concepts/egress/) |

### AF-NET-021

{host} resolves only to IPv6 and the environment has IPv6 disabled.

**What to do.** Set egress.allow_ipv6 for this environment, or use a host with an IPv4 address.

| | |
| --- | --- |
| Exit code | `6` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/egress](/docs/concepts/egress/) |

### AF-NET-030

The synthesis model returned no usable response for {method} {path}.

**What to do.** SYNTH is an escape hatch, not a pass. Write a fixture for this endpoint instead.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/synth](/docs/guides/synth/) |

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

### AF-RUN-010

Writing to {path} failed because the disk is full; {needed} is required.

**What to do.** Free space on the volume holding {path}, then run the command again.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [guides/local-runtime](/docs/guides/local-runtime/) |

### AF-RUN-011

The local state database at {path} is corrupt.

**What to do.** A backup was written to {backup}. The database was rebuilt; run 'af down --all' to reconcile any resources it no longer tracks.

| | |
| --- | --- |
| Exit code | `1` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [concepts/journal](/docs/concepts/journal/) |

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

## Scheduling

### AF-SCH-001

No runtime satisfies the placement requirement {requirement}.

**What to do.** Register a runtime that meets it, or relax the requirement in the placement rules.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [enterprise/runtimes](/docs/enterprise/runtimes/) |

### AF-SCH-002

The organization is at its concurrent environment limit ({limit}); this run is queued at position {position}.

**What to do.** It will start automatically. Tear down an unused environment to start sooner.

| | |
| --- | --- |
| Exit code | `5` |
| Retryable | Yes. The engine retries automatically where it can. |
| More | [concepts/scheduling](/docs/concepts/scheduling/) |

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

The credential for {source} was rejected after one refresh.

**What to do.** Rotate the credential and store the new value where {source} reads it.

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

No operating system keyring is available and no fallback passphrase is set.

**What to do.** Set AF_KEYRING_PASSPHRASE to use the encrypted file fallback, or install a Secret Service provider.

| | |
| --- | --- |
| Exit code | `3` |
| Retryable | No. Retrying the same operation unchanged will fail the same way. |
| More | [guides/secrets](/docs/guides/secrets/) |


---
title: Google Cloud SQL
description: Branching a Cloud SQL for PostgreSQL instance with a fast clone, the request shape that decides whether it is fast, and the one question this provider could not settle.
sidebar:
  order: 8
---

Cloud SQL can clone an instance. When the clone is a **fast clone** it is
created from an Instant Snapshot, which Google documents as a metadata only
operation, so the size of the data does not affect how long it takes.

That is the reason this provider exists, and the sentence that has to travel
with it is longer than usual.

## Cloud SQL has two clone workflows and the call site does not name them

There is also a **standard clone**, which takes a full backup and provisions a
new instance from it. Its duration scales with the size of the database, and for
a large one it is measured in hours rather than minutes.

Cloud SQL chooses between the two **from the shape of the request**, silently,
and returns the same operation either way. There is no field in the response
that says which you got. So a provider that asks for a clone and reports flat
branch time is making a claim it has not checked.

Three things force the standard workflow:

- **Naming a zone at all.** Not naming a different zone: Google states that
  re-specifying even the source's own zone falls back to the standard workflow.
  The fast path requires the field to be absent.
- **Asking for a point in time.** A clone carrying a recovery timestamp is
  restored rather than snapshotted.
- **Disk properties that do not match the source**, meaning the disk type, the
  encryption and the block size.

The first is the trap, and it is worth saying plainly: the request that pins a
branch beside its golden, which is the careful looking thing to do, is exactly
the request that stops being a fast clone.

This provider does not ask for a clone and hope. The type it builds the request
from has **no field** for a zone or a point in time, so asking for the slow path
does not compile, and two separate tests hold that: one asserts on the
marshalled JSON that those keys are absent rather than empty, and one counts
every clone the provider causes and requires none of them to be classified
standard by Google's own rule.

## What it looks like

```yaml
database:
  provider: cloudsql
  project: acme-production
  api_key_env: AF_CLOUDSQL_BRANCH_KEY
```

`project` is the Cloud SQL **instance** that goldens are cloned from. The
connection name `project:region:instance` is accepted too and the instance is
taken from it.

Nothing connects to production. The copy is made by the control plane and the
masking runs against the copy, so no credential in this configuration reaches
the source instance over a connection.

| Variable | What it is |
| ---: | --- |
| `AF_CLOUDSQL_PROJECT` | The Google Cloud project holding the instances |
| `AF_CLOUDSQL_REGION` | The region the source instance lives in |
| `AF_CLOUDSQL_BRANCH_KEY` | The key every clone's password is derived from |
| `AF_CLOUDSQL_STOP_GOLDENS` | `1` to stop a published golden's compute. Read the section below first |
| `AF_CLOUDSQL_TIER` | Overrides the machine tier. Empty keeps the source's, which is what keeps a clone fast |
| `AF_CLOUDSQL_TLS_MODE` | The `sslmode` of the connection strings. Defaults to `require` |

The branch key is **not** the source instance's password. A distinct password is
derived from it for every clone, so a preview environment never holds
production's database credential. That matters more here than it sounds: Google
documents that a clone carries the source's users and passwords, so without the
derived password every branch would be reachable with production's.

## Goldens cost compute here, and Aurora's trick does not exist

The Aurora provider publishes a golden by deleting its writer instance and
keeping the volume, because an Aurora cluster's storage exists whether or not an
instance is attached and is still clonable. A published Aurora golden costs
storage and no compute.

**Cloud SQL has no such thing.** An instance is compute and storage together and
there is no clonable object underneath it. The closest shape available is an
instance whose activation policy is `NEVER`, which stops the compute and keeps
the disk.

Whether Cloud SQL will fast clone an instance that is stopped is **not
established**. Google's clone documentation does not address a stopped source in
either direction, and this provider will not assume the permissive answer about
somebody's bill or somebody's outage. So the default keeps goldens running,
which costs compute per retained golden and is known to work, and
`AF_CLOUDSQL_STOP_GOLDENS=1` opts in to the cheaper behaviour with that unknown
attached. Settling it takes one clone of one stopped instance in one project.

## What is not here

**Reset.** Cloud SQL has no rewind that returns an instance to an earlier state
without creating a new one. Restoring a backup onto an existing instance goes
through the same provisioning as a clone and takes the instance offline while it
runs, so calling that Reset would publish a capability whose cost is nothing
like what the name implies. The conformance suite skips the behaviour by name.

**IAM database authentication.** Cloud SQL supports it for PostgreSQL, it would
be the better credential, and it is not implemented.

## What has been proved, and what has not

The provider's own suite drives a fake Cloud SQL Admin API with a real Postgres
behind it, so the behaviours that are claims about bytes are checked against
bytes. Every request shape is what the Admin API documents.

**No part of this has been run against Google.** There is no project behind the
test suite and there is not meant to be. The suite does not assert a real
service, so the service owned conformance verdicts report as unproven rather
than as passed, which is the honest reading of a run whose storage is a local
Postgres.

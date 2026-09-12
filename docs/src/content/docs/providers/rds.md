---
title: Amazon RDS for PostgreSQL
description: Restoring an RDS for PostgreSQL snapshot for each environment, why that takes minutes and grows with the database, and what has and has not been measured.
sidebar:
  order: 10
---

Plain RDS has no clone. A branch here is a **snapshot restore**: RDS
provisions a new instance and hydrates a new volume from a DB snapshot, and
the volume is every byte of the database. So branch time grows with the size
of the data, this provider declares that it does **not** branch copy on
write, and its branch time is minutes rather than seconds.

That is said first on purpose. RDS for PostgreSQL is where most enterprise
Postgres on AWS lives, so this is the row a buyer is most likely to be reading
about themselves. If your production runs on Aurora PostgreSQL, the
[`aurora`](/docs/providers/aurora) provider clones instead of copying, and
this provider refuses to be pointed at an Aurora cluster rather than quietly
becoming the slow way to do the fast thing.

This provider is in the enterprise edition. Reaching a production RDS instance
needs an IAM role somebody with an organization grants, which is the line the
editions are drawn on.

```yaml
database:
  provider: rds
  project: acme-production
  api_key_env: AF_RDS_BRANCH_KEY
```

`project` is the RDS for PostgreSQL **DB instance identifier** that goldens are
built from. It is not a cluster identifier and not an endpoint hostname.

Nothing connects to production. A golden starts as a snapshot RDS takes of the
instance you named, so the data never crosses a network this tool is on and no
credential for the production database is read.

## How a golden and a branch are made

A golden is a manual DB snapshot, built in five steps:

1. Snapshot the source instance.
2. Restore that snapshot into a candidate instance.
3. Rotate the candidate's master password and close every login it inherited.
4. Mask the candidate, verify it, and close any login the masking created.
5. Snapshot the candidate. That snapshot is the golden. The candidate and the
   first snapshot are then deleted.

A published golden therefore costs snapshot storage and no compute, which is
the one place this mechanism is cheaper than a clone.

A branch is an instance restored from a golden snapshot, with its master
password rotated and its inherited logins closed before anything is handed a
connection string.

## What it creates in your account

| Name | What it is |
| --- | --- |
| `af-g-<digest>` | A golden: a manual DB snapshot of a masked, verified candidate. |
| `af-b-<environment>-<digest>` | One environment's branch: an instance restored from a golden. |
| `af-c-<digest>` | A candidate instance, which exists only while a refresh runs. |
| `af-t-<digest>` | The first snapshot of a refresh, which exists only while it runs. |

Every resource carries an `antifailure` tag and a digest of the source
instance's ARN, and every destructive path reads both before it deletes
anything. An instance whose name matches the scheme and whose tags do not is
left alone, not adopted and not deleted. A second source instance in the same
account never lists, adopts or deletes the first one's resources.

A candidate or first snapshot left behind by a killed refresh is removed by
the next refresh once it is six hours old.

## Credentials

Two things are read, both through the engine's own resolution chain rather than
out of the process environment, so every credential this provider uses is
declared and appears in the same audit trail as the rest.

`AWS_REGION` says which region the instance is in. An instance in `eu-west-1`
does not exist in `us-east-1`, and asking the wrong region answers that the
instance is not there.

`AF_RDS_BRANCH_KEY`, or whatever `api_key_env` names, is **not the source
instance's password**. A restored instance inherits the master credential of
the snapshot it came from, which is production's. This provider rotates every
restored instance's master password before anything connects, to a keyed hash
of that variable and the instance's own identifier. The value is
deterministic, so a later command rebuilds a connection string without a
password having been stored anywhere. It is distinct per instance, so a
preview's credential opens that preview and nothing else. Any high entropy
string will do, and changing it changes every branch's password.

Rotating the master password is not the whole of it. A restore carries every
other login production had, each with its production password, and a password
change ends no session that already authenticated. So before a golden is
masked, and again before it is published, the provider disables every other
login role in the restored instance's own catalog, clears its password, and
ends its sessions along with any other session of the administrator. The two
roles AWS reserves, `rdsadmin` and `rdsrepladmin`, are left alone. A login the
administrator cannot disable stops publication rather than surviving into it.

The AWS credentials themselves come from the environment, an ECS or EKS Pod
Identity credential endpoint, or an EC2 instance role, in that order, and
version 2 of the instance metadata service only.

The IAM actions needed are `rds:CreateDBSnapshot` and `rds:DescribeDBInstances`
on the source instance, and `rds:RestoreDBInstanceFromDBSnapshot`,
`rds:ModifyDBInstance`, `rds:AddTagsToResource`, `rds:DescribeDBSnapshots`,
`rds:DeleteDBInstance` and `rds:DeleteDBSnapshot` on what it creates.

## Where a branch runs, and how it is reached

A restored instance is placed in the source instance's own DB subnet group and
VPC security groups, read from the source rather than configured. Left to its
defaults, RDS would place it in the account's default VPC, which is reachable
from somewhere production is not. It is never publicly accessible, it takes no
backups of its own, and IAM database authentication is off, so the derived
password is the only way in.

`AF_RDS_SSLMODE` defaults to `verify-full`, which checks the certificate chain
and the hostname, and nothing weaker is accepted. The provider carries AWS's
published RDS root bundles for the commercial and GovCloud partitions, pinned
by digest in its tests, and uses the one for the source's partition. The engine
installs the same public bundle inside service and migration containers.
`disable` is accepted only for a loopback endpoint, which is the test fixture
and nothing else.

## What this provider will not do

**It will not branch from an Aurora cluster.** A snapshot restore of Aurora
works and copies every byte, where a clone would not. It refuses at startup and
names the `aurora` provider instead.

**It does not implement `reset`.** RDS has no restore in place. Deleting the
instance and restoring again is exactly what the reset capability is defined
not to be, so it is declared false and the conformance suite skips it by name.

**It does not take a subset.** A candidate is a restore of the source, so there
is nothing empty to load a slice into, and a manifest asking for a subset is
refused.

**It does not implement pooled connection strings or IAM database
authentication.** RDS Proxy is a separate resource this provider does not
create, and IAM authentication is turned off rather than half supported.

## Air gapped installations

**RDS is refused under `AF_AIR_GAPPED`, deliberately.** Restoring a snapshot
needs the RDS API, which an air gapped network by definition cannot reach. The
refusal happens before the environment is created and names the manifest line.
Every request the provider makes also goes through the air gap guard, so a
path that reached it anyway could not dial out.

## What the tests prove, and what they do not

**No AWS account was available to anybody who wrote this provider.** Nothing on
this page has been run against AWS.

The conformance suite runs against a fake RDS control plane on localhost backed
by a real Postgres, in `ee/engine/db/rds/fakerds`. Every line of the provider
runs, and the claims about bytes are checked against bytes: a golden is masked
before it is verified and published only if verification passed, a branch
holds the golden's rows and is isolated from the golden and from other
branches, and nothing leaks across a run. The fake recomputes every request's
signature and refuses one that does not match.

The request shapes follow AWS's published RDS service model, including two
details that are easy to get wrong and invisible to a fake written from the same
assumption: tags are sent as `Tags.Tag.N`, which is what the official SDK sends,
and an instance's subnet group is read as the structure AWS returns rather than
as a string.

The verified connection path runs a real PostgreSQL TLS handshake through the
driver the provider uses, against a certificate authority the test generates,
and refuses a wrong hostname and a wrong signer. No connection has met a
certificate RDS issued.

**Live AWS timing is unmeasured.** The benchmark prints `UNMEASURED` for every
wall clock cell rather than carrying a number from somewhere else, and the
comparison table says the same. What it does measure is the provider's own
work: the control plane calls a branch makes are identical at twenty gibibytes
and at a tebibyte, and a branch opens the database exactly once, to close the
logins the restore inherited.

Copy on write is reported as `UNPROVEN`. The conformance suite decides it with
a stopwatch, and over this fake a restore is a local
`CREATE DATABASE ... TEMPLATE`, which copies files, so the declaration of false
would pass for a reason that has nothing to do with RDS. The suite withholds
the verdict instead, and deciding it needs a run against a real account.

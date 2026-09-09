---
title: Amazon Aurora
description: Cloning an Aurora PostgreSQL cluster for each environment, what it costs, and the half of the speed claim that is not the clone.
sidebar:
  order: 7
---

Aurora can clone a cluster. The clone shares the source's storage volume and
diverges a page at a time as either side writes, so making one moves no data
and takes about as long for a terabyte as for a hundred rows.

That is the whole reason this provider exists, and it comes with a second
sentence that belongs beside it rather than in a footnote.

**The storage is there in seconds and nobody can connect to storage.** A clone
has no instances. A preview environment needs one, and provisioning a writer
takes minutes. The flat part of this is real and it is the storage; the wall
clock to an open connection is dominated by an instance coming up, which is
also flat in the size of the database and is measured in minutes. This provider
declares an expected branch latency in minutes for that reason, and the
benchmark in `benchmarks/` publishes the two halves separately.

This provider is in the enterprise edition. Reaching a production Aurora
cluster needs an IAM role somebody with an organization grants, which is the
line the editions are drawn on.

```yaml
database:
  provider: aurora
  project: acme-production
  api_key_env: AF_AURORA_BRANCH_KEY
```

`project` is the Aurora PostgreSQL **DB cluster identifier** that goldens are
cloned from. It is not an instance identifier and not an endpoint hostname, and
a value that names one of those is refused with a sentence saying so rather
than reported as a cluster that does not exist.

There is no `source_url_env`, and that is the difference between this provider
and every other one here. Nothing connects to production. The copy is made by
the storage layer from the cluster you named, so the data never crosses a
network this tool is on and no credential for the production database is ever
held, read, or asked for.

## What it creates in your account

| Name | What it is |
| --- | --- |
| `af-g-<digest>` | A golden: a clone of the source, masked, verified, and then left with no instance attached. |
| `af-b-<digest>` | One environment's branch: a clone of a golden, with one writer instance. |

Every cluster carries an `antifailure` tag, and that tag is what the provider
reads before it deletes anything. A cluster whose name matches the scheme and
whose tag does not is left alone, not adopted and not deleted. Names collide,
and a provider that trusted the prefix would eventually destroy a cluster it
never created.

**A published golden keeps its writer instance, and that costs you money.** The
obvious saving is to delete it: a cluster's volume exists whether or not an
instance is attached, cloning is a cluster level operation, and a golden that
cost storage and no compute would make keeping several of them cheap. It ought
to work. Nobody who wrote this provider has an Aurora account, the only thing
here that could say whether it does is a fake this repository also wrote, and a
fake agreeing with the assumption that produced it is not evidence. An untested
cost saving that silently breaks branching is worse than the standing cost, so
the instance stays until somebody with an account has run it. If that is you,
the measurement is worth more to us than the saving is to you.

## Credentials

Two things are read, both through the engine's own resolution chain rather than
out of the process environment, so every credential this provider uses is
declared and appears in the same audit trail as the rest.

`AWS_REGION` says which region the cluster is in. A cluster in `eu-west-1` does
not exist in `us-east-1`, and asking the wrong region answers that the cluster
is not there, which is a confusing way to learn about a typo.

**The source cluster's own password is never read.** Not at startup, not during
a refresh, not to connect to a clone, not anywhere. That is the sentence to
check first if you are reviewing this for security, and the rest of this section
is how it is true.

`AF_AURORA_BRANCH_KEY`, or whatever `api_key_env` names, is **not the source
cluster's password**. A clone inherits the master credential of the cluster it
came from, so a provider that did nothing here would hand production's database
password to every preview environment. This one rotates each clone's master
password before anything connects, to a keyed hash of that variable and the
clone's own identifier. Three things follow. The value is deterministic, so a
later command rebuilds a connection string without anything having stored a
password. It is distinct per cluster, so a preview's credential opens the
preview and nothing else. And the source cluster's own password is never read
and never needed. Any high entropy string will do, and changing it changes
every branch's password.

The AWS credentials themselves come from the environment, an ECS or EKS Pod
Identity credential endpoint, or an EC2 instance role, in that order, and
version 2 of the instance metadata service only. A profile in `~/.aws` and a
web identity token file are not read, and a run that finds nothing says which
places it looked in rather than only that it found nothing.

The IAM actions needed are `rds:RestoreDBClusterToPointInTime` on the source
cluster, and `rds:CreateDBInstance`, `rds:ModifyDBCluster`,
`rds:AddTagsToResource`, `rds:DescribeDBClusters`, `rds:DescribeDBInstances`,
`rds:DeleteDBInstance` and `rds:DeleteDBCluster` on the clones.

## What this provider will not do

**It will not fall back to a snapshot restore.** If the cluster you name is not
Aurora PostgreSQL, the provider refuses at startup and says which provider
handles that engine instead. A snapshot restore would work and would copy every
byte, and a flat cost quietly becoming a linear one is worse than a refusal,
because nobody measures a thing that still appears to work.

**It does not implement `reset`.** Aurora's only rewind is Backtrack and that
is Aurora MySQL. Destroying the clone and cloning again would work, and it is
exactly what the reset capability is defined not to be, so the capability is
declared false and the conformance suite skips that behaviour by name.

**It does not implement pooled connection strings.** The pooled endpoint on RDS
is a proxy, which is a separate resource with its own identity and its own
subnet group, and this provider does not create one. Handing back the direct
string under a second name would be a pool that is not one.

**It does not implement IAM database authentication.** Aurora supports it, it
would be the better credential, and it is not here. It is named because a
capability that is named and not built is worse than one that is absent.

## Air gapped installations

**Aurora is refused under `AF_AIR_GAPPED`, deliberately.** The permitted
providers there are `docker`, `dblab` and `pgurl`, all three of which the
operator hosts or supplies. Cloning an Aurora cluster needs `rds.amazonaws.com`,
which an air gapped network by definition cannot reach, so permitting it would
produce an environment that failed at its first API call rather than at
validation.

The refusal happens before the environment is created and it names the manifest
line, which is the difference that matters: the same installation used to get
three minutes into an `af up` and fail on a refused connection, and one of those
tells you what to change while the other tells you the network is broken.

## What the tests prove, and what they do not

The conformance suite runs against a fake RDS control plane on localhost backed
by a real Postgres, in `ee/engine/db/aurora/fakerds`. No test needs an AWS
account and none should.

That proves the provider's logic: that a clone is requested copy on write and
never any other way, that a golden is masked before it is verified and
published only if verification passed, that a branch holds the golden's rows
and is isolated from the golden and from other branches, and that nothing
leaks across a whole run. It also proves the requests are signed correctly for
the region and service they are sent to, because the fake recomputes the
signature and refuses one that does not match.

It does not prove that AWS accepts those requests, and it cannot produce a wall
clock for a real clone. The benchmark says `UNMEASURED` in those cells rather
than carrying a number from somewhere else.

Copy on write itself is therefore reported as `UNPROVEN` rather than as a pass.
The conformance suite decides that claim with a stopwatch, and over a fake
control plane on one local Postgres the only way to hand back a branch carrying
the golden's data is `CREATE DATABASE ... TEMPLATE`, which copies files. A
stopwatch pointed at that is timing Postgres, so the suite withholds the verdict
instead of publishing either answer. `UNPROVEN` is not a pass and the run prints
it as its own line. Deciding it needs a run against a real Aurora, and the same
suite produces a measured verdict there without changing.

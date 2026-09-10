---
title: Azure Database for PostgreSQL
description: Branching a Flexible Server with a point in time restore, why this provider does not claim copy on write, and the three things Azure does not carry across a restore.
sidebar:
  order: 9
---

A branch here is a **point in time restore** of an Azure Database for PostgreSQL
Flexible Server. It needs no dump and no reload, and it produces a server
carrying the golden's rows without anything reading them over a connection. It
is the only mechanism Azure offers that does.

## This provider does not claim copy on write, and that is deliberate

The Aurora and Cloud SQL providers report copy on write branching. **This one
reports that it does not.**

Microsoft documents a restore as creating a **new server**, and describes the
restored server as an independent copy: the physical files are restored from the
snapshot backups to the new server's data location, and a recovery process then
replays write ahead log files to bring it to a consistent state. Nothing in
Microsoft's documentation says the restored server shares storage with its
source.

The temptation to claim otherwise is real, because Microsoft also writes that
"the data restore operation from a snapshot doesn't depend on the size of data",
which reads exactly like a copy on write sentence. The same paragraph continues
that the recovery timing "might vary, depending on the previous backup of the
requested date and time and the number of logs to process", and gives the
overall recovery as **a few minutes up to a few hours**.

So one half of the operation is flat in the size of the data and the other half
is not flat in anything you control. Quoting the first half and declaring copy
on write would be quoting the fast part of a number whose slow part is the one
you wait through.

Declaring it false is not a way of dodging the question. The conformance suite
requires the **opposite** proof of a provider that declares false: that branch
time does grow with the size of the database. The honest declaration is the one
that leaves the behaviour testable.

## Three things Azure does not carry across a restore

Each of these is an outage or an exposure if a provider assumes otherwise, and
each is handled here.

**Firewall rules are not copied.** Microsoft lists applying them as a post
restore task. A branch created and left alone is a server nobody can connect to,
and the failure arrives as a connection timeout that mentions no firewall at all.
This provider creates the rule, and it refuses to start without a range to put
in it rather than defaulting to the whole internet.

**The administrator credential is copied.** A restored server keeps the source's
administrator login, so without an explicit reset every preview environment
would be reachable with production's database credential. A distinct password is
derived for every restore.

**Public and private access cannot be crossed.** A server on a virtual network
restores only to a virtual network, and one on public access only to public
access. This provider opens a branch with a firewall rule, which exists only on
the public side, so it refuses a private source **before** provisioning rather
than after.

Server parameters are not copied either. A source tuned for production comes
back at the defaults, which is worth knowing and is not something this provider
tries to fix for you.

## What it looks like

```yaml
database:
  provider: azurepg
  project: acme-production
  api_key_env: AF_AZUREPG_BRANCH_KEY
```

`project` is the **flexible server** goldens are restored from. The fully
qualified domain name is accepted too and the server name is taken from it.

| Variable | What it is |
| ---: | --- |
| `AF_AZUREPG_SUBSCRIPTION` | The subscription holding the servers |
| `AF_AZUREPG_RESOURCE_GROUP` | The resource group the servers live in |
| `AF_AZUREPG_BRANCH_KEY` | The key every restore's administrator password is derived from |
| `AF_AZUREPG_ALLOW_CIDR` | The range the created firewall rule admits. Required |
| `AF_AZUREPG_LOCATION` | The region. A restore lands in its source's region |
| `AF_AZUREPG_TLS_MODE` | The `sslmode` of the connection strings. Defaults to `require` |

`AF_AZUREPG_ALLOW_CIDR` has no default on purpose. A default of `0.0.0.0/0`
would make every branch work immediately and would open a copy of production to
the whole internet.

## Deleting a server deletes its backups

Microsoft states this plainly, and it is why every destructive path here reads an
`antifailure` resource tag before acting rather than trusting a name. A customer
whose own server happens to be called `af-b-something` must not lose it to our
garbage collection, and on Azure there is nothing to restore from afterwards.

## What is not here

**Reset.** A restore creates a new server rather than returning an existing one
to an earlier state, which Microsoft states directly: a restore "always creates a
new database server with the name that you provide. It doesn't overwrite the
existing database server." There is no operation matching the capability, so the
conformance suite skips the behaviour by name.

**Microsoft Entra database authentication.** Flexible Server supports it, it
would be the better credential, and it is not implemented.

## What has been proved, and what has not

The provider's own suite drives a fake Resource Manager with a real Postgres
behind it, and the fake models all three of the things Azure does not carry
across a restore, so a provider that forgot one fails there rather than in your
subscription.

**No part of this has been run against Azure.** There is no subscription behind
the test suite and there is not meant to be. The suite does not assert a real
service, so the service owned conformance verdicts report as unproven.

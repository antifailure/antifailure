---
title: Azure
description: The Azure services an environment answers for, the ones it refuses, and what each costs.
sidebar:
  order: 23
---

An application in an environment reaches Azure Blob Storage, Queue Storage and
Table Storage without knowing it. It builds
`https://youraccount.blob.core.windows.net` the way it does in production, that
name resolves to the environment's sidecar, the sidecar terminates TLS with a
certificate authority the environment already trusts, and Azurite answers.

There is no endpoint override, no `BlobEndpoint=` in a connection string that
only exists in tests, and no client construction that differs from the one that
ships. That is the whole point of this page. The emulator is a commodity;
reaching it without changing the application is not.

## The surface

This table is **the** surface. A host outside it is not routed to an emulator:
it falls through to the environment's egress policy, whose default is block, so
it is refused rather than answered. A silent wrong answer from an emulator is
worse than a refusal, because it will be believed.

| Service | Host | Emulator | Answered by |
| --- | --- | --- | --- |
| Azure Blob Storage | `*.blob.core.windows.net` | `azure-blob` | Azurite |
| Azure Queue Storage | `*.queue.core.windows.net` | `azure-queue` | Azurite |
| Azure Table Storage | `*.table.core.windows.net` | `azure-table` | Azurite |

Azurite is published by Microsoft and is MIT licensed. It is the only one of
the three Microsoft Azure emulators that is open source, and that difference
decides more of this page than weight does.

## Why three emulators for one image

Azurite is a single container that listens on **three ports**: blob on 10000,
queue on 10001 and table on 10002. An emulator declaration carries one port, so
blob, queue and table are registered separately against the same image.

The shape turned out better than the constraint that produced it. An
environment starts the emulators its egress rules name, so a manifest that
touches blob only starts one container and pays for one, and a refusal is
written per service: a request to Azure Files is refused with Files named,
rather than with "Azure" named.

## The account name travels in the host

Azure puts the storage account in the first label of the hostname, so
`youraccount.blob.core.windows.net` carries the account the way a virtual
hosted S3 URL carries the bucket. The sidecar rewrites the destination and
**preserves the Host header**, and Azurite reads the account out of that header
when the host is a name rather than an address.

So nothing needs to tell Azurite which account it is serving through a flag.
`AZURITE_ACCOUNTS` is deliberately not set, because the account an environment
needs is whichever one its substituted credential names, and that is a property
of the manifest rather than of this build.

The credential is substituted, not passed through. A request signed with a key
the environment recognises as live is refused before it leaves, and the
emulator has no route out in any case: it attaches to the environment's inner
network, which is created `internal`, so having nowhere to send a credential is
a property of the network rather than a promise.

## What it costs per environment

Measured on 2026-09-08 on an Apple Silicon machine, 8 core, with Docker
Desktop holding 7.654 GiB, **at load averages between 24 and 28**, because the
machine was running other work at the same time. The load is published with the
numbers rather than left out: the memory figures are stable under it and the
start times are not, and saying which is which is worth more than a best case.

Image sizes are compressed download bytes for the `linux/arm64` member, read
from the registry.

| Container | Image | Resident | Started |
| --- | --- | --- | --- |
| `azure-blob` | 108.9 MiB | 69.9 MiB | bound all three ports |
| `azure-queue` | the same image, nothing more on disk | 66.7 MiB | bound all three ports |
| `azure-table` | the same image, nothing more on disk | 67.1 MiB | bound all three ports |
| **all three** | **108.9 MiB once** | **203.8 MiB** | |

One Azurite measured alone was 83.5 MiB, so the marginal cost of the second and
third is about 67 MiB each. An environment that names only blob pays 69.9 MiB
and one image.

That is the whole cost of Azure Blob, Queue and Table in an environment: **one
image and about 200 MiB of memory**, or a third of that for one service.

### What Service Bus would have cost, which is why it is not here

| Container | Image | Result |
| --- | --- | --- |
| `servicebus-emulator` | 81.7 MiB | halted: `SQL Health Check failed` |
| `mssql/server` companion | 595.9 MiB, AMD64 only | **killed after 707 seconds, never ready** |

**677.6 MiB of image before either process answers anything**, against 108.9 MiB
for all of Azurite. The SQL Server companion was given a 3 GB allocation of its
own and was killed by the memory limit after 707 seconds, having reached TLS
initialisation and no further; it never printed `SQL Server is now ready for
client connections`, and the Service Bus emulator beside it then failed its SQL
health check and halted. Under emulated AMD64 on ARM64 this is what the pair
does on a developer laptop.

The load average was 25 to 26 throughout, on a shared machine, so this does not
prove SQL Server cannot start here. It does mean the pair is in a different
class of weight from Azurite by roughly an order of magnitude in image bytes and
more than that in memory, and that a developer with an Apple Silicon machine
who names Service Bus in a manifest would be waiting on an emulated SQL Server
rather than testing their application. Opt in is the right answer even before
the EULA below.

### Cosmos DB

`cosmosdb/linux/azure-cosmos-emulator:vnext-preview` has a real ARM64 build and
needs no companion. It is **645.6 MiB of image**, six times Azurite, and held
**89.9 MiB resident**. Its readiness was NOT measured: the predicate used to
watch for it matched the word `ready` inside its own retry line
`readiness check still waiting for Postgres startup`, so the 97 seconds it
reported is not a start time and is not published as one. Its own health line
still read `PostgreSQL=FAIL, Gateway=FAIL, Explorer=FAIL` at that point.

## Not answered, and why

Naming a service and not building it is worse than leaving it out, so these
are named here rather than discovered in a failure.

### Azure Service Bus

Microsoft publishes an emulator for it and this build does not start one, for
two measured reasons and one that is not about cost at all.

**Its wire protocol may not reach an emulator at all.** Every Azure Service Bus
SDK defaults to AMQP 1.0 on port 5672, which is not HTTP. A rule in emulate
mode makes the sidecar terminate the connection and read an HTTP request out of
it, so an AMQP connection would be dropped rather than forwarded. Service Bus
also speaks HTTP on 5300, and that path would work.

This is the reason that would matter most, and it is **NOT CONFIRMED BY
EXPERIMENT**. It was reasoned from `policy.inspectMode` and the sidecar's
`http.ReadRequest` by the lane that owns the routing, and neither that lane nor
this one has driven an AMQP client at an emulate rule. It is recorded here
because a reader deciding whether to wait for Service Bus deserves to know the
strongest argument against it exists, and recorded as unconfirmed because it
has not been run.

**It cannot be declared yet.** `mcr.microsoft.com/azure-messaging/servicebus-emulator`
requires a SQL Server container beside it, which it dials on startup, and an
emulator declaration carries one image. Nothing here can express a companion.

**It refuses to start until somebody accepts a EULA.** Started bare, with no
environment set, it exits with code 133 in 47 seconds and says so: the
Service Bus emulator EULA has to be accepted through `ACCEPT_EULA=Y`. That is
an acceptance a user makes, not one a build makes on their behalf, which is a
better reason for it to be opt in than any number on this page.

**Its companion has no ARM64 build.** `mcr.microsoft.com/mssql/server:2022-latest`
is a single AMD64 manifest with no ARM64 member, so on an Apple Silicon machine
Service Bus drags an emulated AMD64 SQL Server into every environment that
names it. Measured above: 677.6 MiB of image, and the SQL Server never became
ready in 707 seconds with 3 GB of its own.

### Azure Cosmos DB

The Linux emulator is a single image with a real ARM64 build and no EULA gate,
and its cost is above. It is not registered here because nothing in the
conformance suite proves it, and a service in the surface that nothing proves is
a claim rather than a capability. At 645.6 MiB it is also six times Azurite, so
if it lands it lands opt in.

### Everything else Azure runs

Azure Files (`*.file.core.windows.net`), Data Lake Storage Gen2
(`*.dfs.core.windows.net`), Key Vault (`*.vault.azure.net`), Event Hubs and the
management plane are outside the surface. So are the sovereign cloud suffixes
`core.chinacloudapi.cn` and `core.usgovcloudapi.net`, which are different hosts
and are refused rather than answered.

Two of these are worth calling out because a reader is likely to expect them to
be covered by something that is. A **Service Bus queue is not a Queue Storage
queue**, and the **Cosmos DB Table API** is not Table Storage: it speaks the
same protocol on `table.cosmos.azure.com` but its partitioning and throughput
behaviour is what a Cosmos user is testing, and Azurite is not that.

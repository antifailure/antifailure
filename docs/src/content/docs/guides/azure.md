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

## Not answered, and why

Naming a service and not building it is worse than leaving it out, so these
are named here rather than discovered in a failure.

### Azure Service Bus

Microsoft publishes an emulator for it and this build does not start one, for
two measured reasons and one that is not about cost at all.

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
names it. The measured cost is below.

### Azure Cosmos DB

The Linux emulator is a single image with a real ARM64 build and no EULA gate,
and its cost is below. It is not registered here yet because nothing in the
conformance suite proves it, and a service in the surface that nothing proves
is a claim rather than a capability.

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

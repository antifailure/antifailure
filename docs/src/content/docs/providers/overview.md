---
title: Extension points
description: The five things a build can add without forking the engine, what ships for each, and which edition each one belongs to.
sidebar:
  order: 1
---

An environment is assembled out of parts, and five of those parts are things
somebody outside this repository can supply. This page is the map of all five.
Each has its own page under Providers with the detail, the capabilities and
the refusals.

| Extension point | What it supplies | What ships | Where the detail is |
| --- | --- | --- | --- |
| Database provider | The environment's primary Postgres, and the branch of the golden it runs on | `docker`, `neon`, `supabase`, `dblab`, `pgurl` | [Database providers](/docs/providers/databases) |
| Datastore provider | Every other store the manifest declares, and what its stance does to the contents | `clickhouse` | [Datastore providers](/docs/providers/datastores) |
| Runtime | Where the containers actually run | `local`, `kubernetes` | [Runtimes](/docs/providers/runtimes) |
| Golden store | Where a golden's dump and its attestation live | `local`, `s3`, `azure_blob`, `gcs` | [Golden stores](/docs/providers/stores) |
| Emulator | A third party API answered inside the environment | nothing built in | [Emulators](/docs/providers/emulators) |

The interfaces are in `engine/pkg/extension`, which is a public package for
exactly this reason: an interface declared in an internal package is one a
build outside the module cannot name, let alone implement.

## The three rules that hold for all five

**A registration adds a choice and can never replace one.** The engine
consults its own built in providers first and the registry afterwards. So a
registration under a built in name would never be used, and it is refused at
validation rather than ignored. The alternative is a build somebody believes
overrides the Docker provider and which silently does not.

**A name in the manifest that this build does not have is refused, and the
refusal lists what there is.** It is never substituted. Falling back to
`docker` would hand somebody an empty preview with no reason for it, and a
datastore quietly starting empty is how somebody ends up trusting a blank
ClickHouse. The refusal names registered providers too, so a misspelling is
answered rather than merely rejected.

**A capability is a promise a suite checks.** Every point declares what it can
do, and the conformance suite runs a behaviour only where it was declared and
skips it BY NAME where it was not. Declaring a capability you do not have
makes the suite run a behaviour it should have skipped, which fails, which is
the intended outcome.

## Which edition an extension point belongs to

One rule decides it, and it is about who the value is for rather than about
how hard the code was:

> A provider goes in the enterprise edition when it needs an ORGANIZATION to
> exist. One developer with their own account and their own card gets MIT, in
> the engine, next to `supabase`.

What follows from it:

- **Anything with an MIT peer in the engine stays MIT.** The `s3` and
  `azure_blob` golden stores are MIT, so `gcs` is, and it lives in
  `engine/internal/golden` beside them rather than in `ee/`.
- **All emulation is MIT**, and **all datastore support is MIT**. Neither is
  an upsell. They are what makes `af up` work for ordinary software.
- What is licensed sits above them: cross account goldens, residency
  placement, federated identity, running more than one runtime at once, and
  the managed database providers that need an IAM role somebody in an
  organization has to grant.

The community edition is the whole product minus `ee/`. An expired licence
leaves you with it rather than with nothing.

## The matrix

Every provider this build has, what it actually does underneath, and what it
declares. Capabilities are read from the provider's own `Capabilities()`
rather than described here twice, so the column is the value the conformance
suite tests against.

### Database providers

| Provider | Mechanism | Branch shares storage | Reset in place | Pooled endpoint | Subsetting | Edition |
| --- | --- | --- | --- | --- | --- | --- |
| `docker` | A container per branch on the local daemon, from an image with the golden committed into it | yes, the daemon's storage driver | yes | no | yes | MIT |
| `neon` | A Neon branch of the golden branch | yes | yes | yes | no | MIT |
| `supabase` | A Supabase branch, which is a whole separate project, with the golden copied in | no | yes | yes | no | MIT |
| `dblab` | A ZFS clone handed out by a Database Lab Engine you run | yes | yes | no | no | MIT |
| `pgurl` | A `CREATE DATABASE ... TEMPLATE` on any Postgres you can reach | no | yes | no | yes | MIT |

`neon` and `dblab` are the two where a branch is a copy on write clone of a
full size copy of production, which is the whole reason to choose either.
`docker` declares the same capability for a different reason and it is worth
knowing which: a branch there is a container over the golden image's shared
layers, so nothing is copied when one is made, and the time in that provider
goes into building the image rather than into branching it.

The [database providers](/docs/providers/databases) page still describes
`docker` branch time as growing with the database. That sentence predates this
matrix and is not something this page measured either way; the `benchmarks/`
report is where a number for it would come from, and there is not one yet.

### Datastore providers

| Provider | Engine | Mechanism | Holds a golden | Branch shares storage | Edition |
| --- | --- | --- | --- | --- | --- |
| `clickhouse` | `clickhouse` | `ATTACH PARTITION FROM` against a local server the engine starts | yes | usually, and it depends on the server's storage policy rather than on this provider | MIT |

### Runtimes

| Runtime | Mechanism | Reachable from the machine that ran `af` | Logs | Can attach a local database container | Edition |
| --- | --- | --- | --- | --- | --- |
| `local` | Containers on the local Docker daemon, with a port forwarder per web service | yes | yes | yes | MIT |
| `kubernetes` | A Deployment, Service and Ingress per web service | only with a domain to publish under | yes | no | MIT |

Running more than one runtime from one control plane is the `multi_runtime`
licensed feature. Running either one on its own is not.

### Golden stores

| Store | Mechanism | Credential | Edition |
| --- | --- | --- | --- |
| `local` | A directory, written beside and renamed into place | none | MIT |
| `s3` | The S3 REST API, signed with Signature Version 4 written here | `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` | MIT |
| `azure_blob` | The Blob REST API | a container shared access signature carried in the URL | MIT |
| `gcs` | The Cloud Storage JSON API | a service account key, or the metadata server | MIT |

`s3` also addresses Cloudflare R2, MinIO, Backblaze B2, DigitalOcean Spaces
and Wasabi. What is proved about each is on the [golden
stores](/docs/providers/stores) page, including which of them is proved end to
end and which are proved only to be addressed correctly.

### Emulators

Nothing is built in, and that is deliberate rather than unfinished.
Antifailure does not write emulators: LocalStack, Azurite and the vendors' own
emulators exist and carry years of fidelity work a hand written replacement
would not have. What the engine adds is that the application needs no endpoint
override to reach one. See [Emulators](/docs/providers/emulators).

## Writing one

[Writing a provider](/docs/contributing/provider-authoring) has the
registration, which is four lines around `engine/pkg/afcli`, and the
conformance suite each point runs.

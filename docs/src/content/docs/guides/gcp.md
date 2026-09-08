---
title: Google Cloud in an environment
description: Which Google Cloud services an environment answers for, which emulator answers each one, which of those Google actually ships, and what is refused.
sidebar:
  order: 24
---

## Google does not ship a Cloud Storage emulator

Read this first, because finding it out from a failing test is worse than
reading it here.

Google publishes emulators for five of its services: Pub/Sub, Firestore,
Datastore, Bigtable and Spanner. It publishes **none for Cloud Storage**. There
is no `gcloud emulators storage`, there never has been, and the gap is old
enough that the community filled it.

So Cloud Storage in an environment is answered by
[fake-gcs-server](https://github.com/fsouza/fake-gcs-server), which is
Francisco Souza's project, is BSD 2-Clause licensed, and **has no affiliation
with Google**. It is a good emulator and it is not Google's. Every table on
this page says which of the two you are looking at, in a column, because the
distinction changes what a passing test is worth: a Pub/Sub test that passes
here passed against the code Google ships to its own customers for local
development, and a Cloud Storage test that passes here passed against a third
party reimplementation of a published API.

Nothing on this page presents fake-gcs-server as Google's, and if you find a
sentence that reads that way, it is a defect.

## The surface

**The table is the surface.** A Google host that is not in it is not routed to
an emulator at all. It falls through to the environment's egress policy, whose
default is block, so it is refused rather than answered. That is deliberate and
it is the expensive half of this feature: a silent wrong answer from an
emulator is worse than a refusal, because a refusal sends you to look at the
rule and a wrong answer gets believed.

| Service | Hosts answered | Emulator | Shipped by | Transport |
| --- | --- | --- | --- | --- |
| Cloud Storage | `storage.googleapis.com`, `*.storage.googleapis.com` | fake-gcs-server | **Third party** | REST over HTTP/1.1 |
| Cloud Pub/Sub | `pubsub.googleapis.com` | Google Cloud CLI | Google | gRPC over HTTP/2 |
| Cloud Firestore | `firestore.googleapis.com` | Google Cloud CLI | Google | gRPC over HTTP/2 |
| Cloud Datastore | `datastore.googleapis.com` | Google Cloud CLI | Google | gRPC over HTTP/2 |
| Cloud Bigtable | `bigtable.googleapis.com`, `bigtableadmin.googleapis.com` | Google Cloud CLI | Google | gRPC over HTTP/2 |
| Cloud Spanner | `spanner.googleapis.com` | Cloud Spanner Emulator | Google | gRPC over HTTP/2 |

Six services and **six containers**, which is the first thing that differs from
the AWS surface. LocalStack answers nine AWS services on one gateway port, so
an AWS environment starts one emulator. Google ships nothing of that shape, so
a Google environment starts one container per service it asks for, and the cost
is a sum rather than a constant. The sums are measured further down.

Bigtable answers for two hostnames rather than one on purpose. Creating a table
is an admin call against `bigtableadmin.googleapis.com`, so a surface holding
only the data plane fails at the first setup step of every test with an error
naming the wrong service, and the person reading it goes looking at their row
writes.

## What is refused, and why each one

| Refused | Why |
| --- | --- |
| `oauth2.googleapis.com`, `accounts.google.com`, `iamcredentials.googleapis.com`, `sts.googleapis.com` | The credential path. No emulator here implements Google's token endpoint, and answering it with a fabricated token would be this project writing an emulator for the one service where a wrong answer is a security claim. See "Credentials" below. |
| `www.googleapis.com` | It serves the Cloud Storage JSON API and dozens of other Google APIs on the same name. Routing it to a storage emulator would answer for every other API on that host with a storage 404. |
| `storage.<location>.rep.googleapis.com` | The regional and dual region Cloud Storage endpoints. fake-gcs-server matches on the Host header against exactly one public host, so routing a second spelling here produces a 404 from inside the emulated surface, which reads as a missing object rather than as an unsupported endpoint. |
| `<location>-pubsub.googleapis.com` | The Pub/Sub regional endpoints. The emulator has no notion of a region, so answering for a regional spelling would emulate a property it does not have. |
| `secretmanager.googleapis.com`, `cloudtasks.googleapis.com`, `bigquery.googleapis.com`, `run.googleapis.com`, `cloudfunctions.googleapis.com`, `logging.googleapis.com`, `compute.googleapis.com` and every other Google API | Outside the surface. No emulator, so a refusal. |
| `metadata.google.internal` and `169.254.169.254` | Link local, and refused by the sidecar's destination guard before any rule is consulted. See "Containment" below. |

Firestore's emulator is the `gcloud` one and not the Firebase Local Emulator
Suite. Security rules, indexes, Firebase Authentication, the Realtime Database
and Hosting are a different program and are not here. Datastore in Firestore
mode is served by Firestore and is emulated by the Firestore emulator, not by
the Datastore one; they are two containers for that reason.

## What the emulators do not implement

An emulator that is trusted where it is wrong is worse than no emulator, so
each project's own stated gaps are repeated here rather than left to be found.

- **Cloud Spanner.** Google documents that the emulator does not check whether
  a statement is partitionable, so a partitioned DML statement or a
  `partitionQuery` can pass here and fail in production with a
  non-partitionable statement error. It also has no query plans in `PLAN` or
  `PROFILE` mode, no `ANALYZE`, no audit logging and no monitoring. **A twin is
  not a substitute for a plan review on Spanner.**
- **Cloud Bigtable.** The emulator holds one unnamed instance in memory.
  Replication, app profiles and instance or cluster administration are not
  emulated, and the admin API answers for table level calls only.
- **Cloud Storage.** fake-gcs-server does not validate signed URL query
  parameters at all: neither the signature nor the expiry is checked. A test
  that proves a signed URL works here has proved that the URL was formed, not
  that it was signed correctly.
- **Cloud Firestore.** No security rules and no composite index enforcement, so
  a query that the emulator answers can be refused in production for want of an
  index.

## The claim, and the one number that carries it

The point of answering these hosts inside the environment is that **the
application is not changed to reach them**. No `apiEndpoint`, no `baseUrl`, no
`STORAGE_EMULATOR_HOST`, no `PUBSUB_EMULATOR_HOST`, no client construction that
exists only in tests. Every name resolves to the sidecar, the sidecar
terminates TLS with a certificate authority the environment already trusts, and
it answers for `storage.googleapis.com` itself. The unmodified production code
path runs against the emulator.

Google's client libraries do read `STORAGE_EMULATOR_HOST`, `PUBSUB_EMULATOR_HOST`,
`FIRESTORE_EMULATOR_HOST`, `DATASTORE_EMULATOR_HOST`, `BIGTABLE_EMULATOR_HOST`
and `SPANNER_EMULATOR_HOST`, and pointing a client at an emulator with one of
them is the ordinary way to do this. **Antifailure does not set any of them, and
setting one would make the claim meaningless**: those variables change how the
client library builds its endpoint and, for several of them, switch off
authentication as well, so the code under test stops being the code that ships.
If you ever see one of those variables in an environment this tool built, that
is a bug in this tool and not a shortcut.

**Today that claim holds for one of the six services, and even that one has a
gap in front of it.** Both halves are measured below rather than reasoned
about.

### What was run

A stand in for the sidecar's inspected path, built to match
`engine/cmd/af-proxy/mitm.go` in the places that decide the answer: it answers
`CONNECT`, terminates TLS with an authority carrying the same extensions the
engine's own authority sets in `engine/internal/envcert/envcert.go`, sets no
ALPN, and then reads HTTP/1.1 requests out of the terminated connection. The
clients are the vendor's own, unmodified, reached through `HTTPS_PROXY` and a
trusted authority and nothing else.

### Cloud Storage: both languages complete every call, with no override

| Client | Version | Create bucket | Upload | Download | List |
| --- | --- | --- | --- | --- | --- |
| `@google-cloud/storage` | 8.0.1 | 502 ms | 208 ms | 24 ms | 28 ms |
| `google-cloud-storage`, Python | 3.13.1 | 126 ms | 18 ms | 7 ms | 10 ms |

Both preserved the Host header as `storage.googleapis.com` on every request,
which is what lets fake-gcs-server route them at all, and both sent
`Authorization: Bearer`. No `apiEndpoint`, no `STORAGE_EMULATOR_HOST` and no
client option was set in either.

### The credential path is a real gap, and it is not the same in two languages

Before its first storage call, each client exchanged its service account key
for an access token. **The host it exchanged at is outside the surface, and it
is a different host in each language.**

| Client | Token request observed |
| --- | --- |
| `@google-cloud/storage` 8.0.1 | `POST www.googleapis.com/oauth2/v4/token` |
| `google-cloud-storage` 3.13.1, Python | `POST oauth2.googleapis.com/token` |

Python takes that host from the `token_uri` in the key file, so an environment
that supplies the key controls it. **Node does not.** `gtoken`, which
`google-auth-library` uses for a service account key, holds
`https://www.googleapis.com/oauth2/v4/token` as a constant and ignores
`token_uri`, so nothing an environment supplies can move it.

No emulator on this page implements Google's token endpoint, and this project
does not write emulators, least of all for the one surface where a wrong
answer is a security claim. So both hosts stay refused, and **Cloud Storage
with a service account key does not run end to end inside an environment
today**. That is a gap in the credential path rather than in the storage
surface. It is the Google shaped version of the reason the AWS surface answers
for STS, and it is stated here because a user meeting it as a failure would go
looking at their bucket.

### The other five: gRPC does not survive an HTTP/1.1 proxy

Three runs of the same unmodified `@google-cloud/pubsub` 6.0.1 client against
the same stand in, with one thing changed each time.

| The stand in | What the client got | Wall clock |
| --- | --- | --- |
| Reads HTTP/1.1, no ALPN. This is what af-proxy does. | `14 UNAVAILABLE`, after 9 connection attempts | 80.5 s, which is the client's own 60 s deadline plus its retries |
| Reads HTTP/1.1, offers ALPN `h2` | `14 UNAVAILABLE` | 77.0 s |
| Forwards the terminated socket to an HTTP/2 backend | `12 UNIMPLEMENTED`, which is the backend answering | **2.6 s**, of which 0.46 s is the call the client itself timed |

The proxy's own log says why. On both HTTP/1.1 runs it recorded
`Parse Error: Pause on PRI/Upgrade`, which is an HTTP/1.1 parser meeting the
HTTP/2 connection preface. So the failure is not in the TLS handshake, which
succeeds, and not in ALPN, which changes nothing on its own. It is the first
frame after the handshake.

The third row is the useful one. With the socket forwarded to an HTTP/2
backend instead of parsed, the unmodified client reached the server in 456
milliseconds by its own clock and came back with a real gRPC status. **The transport, the
proxy, the certificate and the credentials all work for a gRPC client with
zero endpoint overrides.** The only thing missing is that the sidecar reads
HTTP/1.1 where it would have to forward HTTP/2. That is one property of one
file, and it is what stands between this surface and five of its six services.

Worth recording beside it: the gRPC clients made **no token request at all**.
Google's gRPC client stack signs a self signed JWT locally, so the credential
gap above is specific to the REST client and does not apply to the other five.

## What it costs per environment

Six services and six containers, so the cost is a sum. These are the
compressed download sizes read from each registry's own manifest, per
architecture, for the digests this build pins.

| Image | linux/arm64 | linux/amd64 | Answers |
| --- | --- | --- | --- |
| `fsouza/fake-gcs-server` 1.56.1 | 24.2 MB | 25.4 MB | Cloud Storage |
| `google-cloud-cli` 583.0.0-emulators | 356.4 MB | 447.2 MB | Pub/Sub, Firestore, Datastore, Bigtable |
| `cloud-spanner-emulator` 1.5.57 | **none published** | 71.2 MB | Spanner |

Three images and not six, because the four `gcloud` emulators are four
containers of one image and its layers are pulled once. A manifest asking for
all six pulls about 452 MB on arm64 and 544 MB on amd64, and then runs six
processes, four of which are JVMs.

**The Spanner emulator publishes no arm64 image.** Its manifest is a single
`linux/amd64` image rather than a multi architecture index, so on an Apple
Silicon machine it runs under emulation. That is stated rather than hidden
because it is the one entry here whose start time and memory will not resemble
anything a reader measures on a Linux runner.

The **memory and start time per running container are NOT measured**, and the
reason is not that they were skipped. The Docker daemon on the machine this
was written on stopped scheduling containers under memory pressure while
another twenty three lanes were running, and publishing an estimate in a table
of measurements is the thing this project refuses to do. The command that
produces them is `just benchmark-emulators`, it needs a working daemon, and
the number belongs here when somebody runs it.

## Containment, checked against Google's documentation rather than assumed

Emulator containers attach to the environment's **inner** network only, which
Docker creates with `internal: true`. So an emulator having no route out is a
property of the network rather than a promise made by this page, and the number
of ways out of it is zero.

Two things about Google Cloud are worth stating here, because a containment
argument carried over from another cloud gets them wrong.

- **The metadata server cannot be closed with a firewall rule.** Google's own
  VPC firewall documentation says of the metadata server at `169.254.169.254`
  and `fd20:ce::254`: "This server is essential to the operation of the
  instance, so the instance can access it regardless of any firewall rules that
  you configure." That is a stronger statement than the equivalent one on AWS,
  and it holds for IPv6 as well, which reasoning carried over from AWS misses
  entirely.
- **On Google Cloud the metadata server is also the resolver.** A VM's
  `resolv.conf` names the metadata server as its nameserver, and Google
  documents that a lookup that no private zone answers is then looked for in a
  public zone. So on a Compute Engine VM, DNS resolution and the identity
  endpoint are the same unfilterable address, and closing one closes the other.

Neither of those changes what an environment does, because an environment's
emulators sit on an internal Docker network with no route to a metadata server
of any kind, and the sidecar refuses a link local destination before consulting
any rule. They are recorded because they are the facts that would decide the
question if Antifailure ever ran an environment on a Compute Engine VM
directly, and because the answer is not the same as the AWS one.

## Licences

Every image is pinned by digest and every licence is recorded in
`THIRD_PARTY_NOTICES.md`, generated from the same declaration the engine starts
the container from, so a bumped digest cannot leave a stale licence behind.

| Emulator | Licence | Holder |
| --- | --- | --- |
| fake-gcs-server | BSD 2-Clause License | Francisco Souza. **Not affiliated with Google.** |
| Google Cloud CLI | Apache License 2.0 | Google LLC |
| Cloud Spanner Emulator | Apache License 2.0 | Google LLC |

The Google Cloud CLI's licence was read from `/google-cloud-sdk/LICENSE` inside
the image rather than from a page about installing it. Its second clause is
worth knowing: using the CLI against a Google Cloud product is additionally
governed by that product's own terms. Nothing here reaches a Google Cloud
product, because the emulator has no route out.

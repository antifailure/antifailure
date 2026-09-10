---
title: Golden stores
description: Where a golden's dump and its attestation live, the four stores that ship, and exactly what is proved about the services that speak the S3 API.
sidebar:
  order: 10
---

A golden store is where a golden's dump and its attestation live when they
live somewhere other than the machine that made them.

The reason to have one at all: a golden made on a laptop cannot be branched by
a runner, and a fleet that refreshes production once per runner is a fleet that
reads production once per runner. One machine refreshes and publishes; the rest
pull what it published.

```yaml
database:
  golden:
    storage: gcs                      # or local, s3, azure_blob
    storage_url: $AF_GOLDEN_STORE_URL
```

The attestation travels beside the dump and is checked before the dump is used.
That ordering is the point: a dump on its own is a database somebody could have
put anything in, and the signed statement of what the verification scan found
is what makes it a golden rather than a file.

## What ships

| Store | `storage_url` | Credential | Comes from |
| --- | --- | --- | --- |
| `local` | a directory, or `file:///path` | none | the filesystem |
| `s3` | `s3://bucket/prefix`, or `https://host/bucket/prefix` for a server that is not AWS | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, optionally `AWS_SESSION_TOKEN` and `AWS_REGION` | the environment |
| `azure_blob` | the container's https URL with a shared access signature | the signature, in the URL | the environment |
| `gcs` | `gs://bucket/prefix`, or `https://host/bucket/prefix` for a server that is not Google | `GOOGLE_APPLICATION_CREDENTIALS`, or the metadata server | the environment |

All four are MIT and all four are in the engine. The editions rule says
anything with an MIT peer in the engine stays MIT, and these are each other's
peers.

## The credential never lives in the manifest

A `storage_url` written as `$VARIABLE` or `${VARIABLE}` is read from the
environment. That is what lets a container shared access signature or a bucket
URL with a credential in it stay out of a file that is committed. It is the
same rule `source_url_env` follows, in the form a URL can carry.

The `s3` and `gcs` stores go further and take no credential from the URL at
all. They read it from the environment by the names the vendor's own tools
already use, so a machine already set up for the AWS CLI or for `gcloud` needs
nothing else.

A message about a URL never prints its credential back out. A shared access
signature is a query string and a bucket URL can carry a user info section, so
both are stripped before a URL reaches an error.

## `local`

A directory, and the right answer more often than it sounds: a shared runner
with a volume, a CI cache, an NFS mount. Objects are written beside their final
name and renamed into place, so a reader never sees a half written dump and a
crash leaves a temporary file rather than a truncated one wearing the real name.

## `s3`, and the five other services that speak it

Signature Version 4 is implemented in this repository rather than taken from
the AWS SDK, for the same reason the Blob store speaks REST: three operations
against a stable, fully specified protocol are not worth a dependency tree in a
binary otherwise built from a handful of libraries.

A `s3://bucket/prefix` URL addresses AWS virtual hosted, as
`bucket.s3.<region>.amazonaws.com`. A full `https://host/bucket/prefix` URL
addresses a server that is not AWS PATH STYLE, because a bucket prefixed onto
an endpoint that is an address, or onto a regional host that does not serve
wildcard subdomains, is a hostname that does not resolve.

| Service | `storage_url` | `AWS_REGION` |
| --- | --- | --- |
| Amazon S3 | `s3://your-bucket/goldens` | your region |
| Cloudflare R2 | `https://<account>.r2.cloudflarestorage.com/your-bucket/goldens` | `auto` |
| MinIO | `http://<minio-host>:9000/your-bucket/goldens` | `us-east-1` |
| Backblaze B2 | `https://s3.<region>.backblazeb2.com/your-bucket/goldens` | that region, such as `us-west-004` |
| DigitalOcean Spaces | `https://<region>.digitaloceanspaces.com/your-bucket/goldens` | that region, such as `nyc3` |
| Wasabi | `https://s3.<region>.wasabisys.com/your-bucket/goldens` | that region, such as `us-east-2` |

**Set `AWS_REGION`.** Signature Version 4 pins the region into the credential
scope, so a request signed for `us-east-1` against a bucket in `us-west-004` is
refused, and it is refused with a 403 that reads exactly like a wrong secret
key. The default when the variable is unset is `us-east-1`, which is right for
AWS in that region and for MinIO and is wrong for the rest.

### What is proved, and what is not

This matters more than the table. "Works with R2, B2, Spaces and Wasabi" is
the kind of sentence that turns out to be wrong, so here is the split:

- **MinIO is proved end to end**, by a suite that runs the four operations
  against a real MinIO. It is the store's own signing that is under test there:
  a wrong signature is indistinguishable from a right one until a server
  rejects it, and a fixture cannot reject anything.
- **The other four are proved to be ADDRESSED correctly and are not proved to
  answer.** A test asserts, for each of them, the host the request goes to, the
  path style addressing, and a credential scope naming that vendor's region.
  What it cannot assert is that Cloudflare, Backblaze, DigitalOcean and Wasabi
  accept the result, because that needs an account with each and no test in
  this repository may require a cloud account.

If one of the four does not work for you, that is a bug worth reporting rather
than a limitation to work around. The protocol is the same one MinIO answers.

## `azure_blob`

The `storage_url` is the CONTAINER's URL carrying a shared access signature,
which is what the portal and the CLI both produce. Nothing here ever sees an
account key. Scope the signature to one container with read, write, delete and
list, give it an expiry, and put the whole URL in the environment variable the
manifest names.

A 403 from this store is almost always the signature: expired, scoped to the
wrong container, or missing one of the four permissions. The message says so,
because a bare 403 sends somebody to look at their network.

## `gcs`

The Cloud Storage JSON API, spoken directly for the same reason as the other
two. Two ways to get a token, matching where this actually runs:

- **The metadata server**, which is what a Cloud Run service, a GKE workload
  and a Compute Engine instance all have, and which needs no key material at
  all. This is the better path wherever it exists.
- **A service account key**, signed here into an RS256 assertion and exchanged
  for an access token. This is what a CI runner outside Google has. Point
  `GOOGLE_APPLICATION_CREDENTIALS` at the key file, or put the document itself
  in `GOOGLE_APPLICATION_CREDENTIALS_JSON`.

The key is parsed when the store is opened, so a key that is not a key is
reported before anything depends on the answer. The metadata server is NOT
probed then: off Google that name does not resolve, and paying a second for
that on every command would be a second on every command. A `gs://` URL with no
credential anywhere therefore opens and then refuses at the first request,
naming the variable that fixes it.

An endpoint that is not Google's with no credential configured sends no
`Authorization` header at all. That is what lets a Cloud Storage emulator be
reached with no Google account anywhere. A `gs://` URL never gets that
treatment: an unauthenticated request to Google is a 401, and refusing with the
variable named beats a 401 twenty minutes into a refresh.

The service account needs `storage.objects` on the bucket. A 401 from this
store is the token and a 403 is the grant, and the message distinguishes them,
because they have different fixes and the same digit count.

### There is no official Cloud Storage emulator

Google ships emulators for Pub/Sub, Firestore, Datastore, Bigtable and Spanner,
and none for Cloud Storage. `fsouza/fake-gcs-server` is the de facto choice and
is community maintained. The suite for this store runs against it, and what
that proves is the four operations against the JSON API. It does not prove
authentication, because that server verifies none. The two token paths are
covered separately, against a server the test stands up, which is as close as a
machine with no Google account gets.

## Writing one

Implement `extension.GoldenStore`, which opens an `extension.ObjectStore` with
`Name`, `Put`, `Get`, `List` and `Delete`.

Two details decide whether it works rather than nearly works:

- **Return `extension.ErrObjectNotFound` for an object that is not there.** A
  store outside this module cannot name the engine's own sentinel, so a store
  that returns some other error turns every "no golden published yet" into "the
  store is broken". They are the same HTTP status on more than one service.
- **Removing what is not there must succeed.** Teardown retries, and a retry
  that fails on the work it already did is a teardown that never finishes.

`local`, `azure_blob`, `s3` and `gcs` are reserved names and a registration
under one of them is refused at validation rather than accepted and then never
consulted, because the built in stores are looked up first.

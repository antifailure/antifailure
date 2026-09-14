---
title: Emulators
description: How a third party API is answered inside an environment, why Antifailure writes none of them, and what a declaration has to carry.
sidebar:
  order: 13
---

An emulator is a third party API answered inside the environment: an S3, a
queue, a pub/sub topic, a blob store. It is the fifth extension point and the
only one with nothing built in, which is deliberate rather than unfinished.

## Antifailure does not write emulators

No hand written S3, no hand written SQS, no blob store core, no queue core. If
a future change proposes one, this paragraph is the answer.

LocalStack, Azurite, the Microsoft Service Bus and Cosmos emulators, the
`gcloud` emulators and `fake-gcs-server` exist, are mature, and carry years of
fidelity work. S3 alone has a decade of edge cases in it. A hand written
replacement would be worse on day one and probably for two years, and nobody
buys this product because its S3 emulator is good.

**What this engine adds is the part people hate about those emulators.** Using
LocalStack normally means changing the application: an endpoint override, an
`AWS_ENDPOINT_URL`, a client construction that only exists in tests. That makes
the test prove less, because the code under test is not the code that ships.
Here none of that is needed. Every name resolves to the environment's sidecar,
the sidecar terminates TLS with a certificate authority the environment already
trusts, and it answers for `s3.amazonaws.com` itself. The unmodified production
code path runs against the emulator. The emulator is a commodity; making it
invisible is not.

How a request actually gets there is the egress subsystem's job and the mode in
the manifest decides it. See [egress](/docs/concepts/egress), which is the page
that says what each mode does with a request.

## What a declaration carries

```go
type Emulator interface {
    Name() string                    // what an egress rule names it by
    Hosts() []string                 // the hostnames it answers for
    Container() EmulatorContainer    // the image, the port, the environment
}
```

Two things are refused at validation rather than accepted, and both were
refusals somebody wanted later:

- **An emulator that answers for no hosts is refused.** No request could ever
  reach it, so a registration with an empty host list is a registration that
  does nothing, and doing nothing quietly is what this whole extension system
  is built to avoid.
- **An image pinned by a tag rather than by a digest is refused.** An emulator
  is the thing answering for a production API. A tag that moves changes what an
  environment was tested against with nothing in the repository changing, and
  then the run that passes yesterday and fails today has no diff to blame.
  `@sha256:` or it does not register.

Two emulators registered under one name, or one registered with no name at all,
are refused for the same reason every other extension point refuses them.

## Costs that are named rather than hidden

Each of these is a real cost of using somebody else's emulator, and the rule is
that they are stated rather than discovered:

- **Coverage belongs to whoever integrates one.** The covered surface is
  recorded and anything outside it is refused with the provider's own error
  shape. A silent wrong answer from an emulator is worse than a refusal,
  because it will be trusted.
- **Weight.** The Azure Service Bus emulator wants an MSSQL container beside
  it. That is measured and said out loud rather than absorbed.
- **Licensing and supply chain.** Every image is pinned by digest, recorded in
  `THIRD_PARTY_NOTICES.md`, and given the same no egress treatment as any other
  container in the environment.
- **There is no official Cloud Storage emulator.** Google ships them for
  Pub/Sub, Firestore, Datastore, Bigtable and Spanner and none for Cloud
  Storage, so `fsouza/fake-gcs-server` is the de facto choice and is community
  maintained. Stated plainly here rather than left for somebody to find.

## A bad emulator is not tolerated either

The commodity argument runs both ways. Not writing emulators does not mean
putting up with a wrong one. Where an integration is wrong in a way that
matters, the fix is upstream or a documented refusal. It is not a fork, and it
is not a locally patched image that nobody else can reproduce.

## Writing one

Implement `extension.Emulator` and register it with `AddEmulator`. Give it the
hostnames the vendor's own SDK resolves, pin the image by digest, and declare
what it covers.

Nothing is reserved here, because no emulator is built into this binary. A
registration can shadow nothing.

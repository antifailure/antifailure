# added

Reaching an emulator meant changing the application, so what was verified was a
client nobody deploys.

LocalStack, Azurite and the vendors' own emulators carry years of fidelity work
nobody here is going to reproduce, and every one of them is reached the same
way: an endpoint override, a base URL that only has a value under test, a client
constructed one way for tests and another for production. The change is small
enough that everybody makes it and large enough that it moves the code under
test off the path that ships.

A new egress mode removes the change. A rule set to `emulate` names a registered
emulator, and the sidecar answers for the provider's own hostname with a
certificate the environment already trusts, then forwards the request to a
container beside the services. Your SDK is configured for production and stays
that way.

The mode names a registration rather than an image or an address, so a manifest
cannot decide what answers for `s3.amazonaws.com`. A name this build has not
registered refuses the environment before it starts rather than falling through
to `block`, because a rule that silently does nothing is how somebody comes to
believe an environment was tested against S3. The emulator container joins the
environment's inner network and nothing else, so it has no route to the internet
at all.

Two headers are deliberately preserved rather than rewritten, and the reason is
in `docs/concepts/egress.md`. The `Host` header carries the bucket name in
virtual hosted S3 addressing, so rewriting it would rename every bucket. The
`Authorization` header cannot leak to anything from a network with no route out,
and replacing it without re-signing would produce a signature that disagrees
with its own request. The sidecar records the access key id, never the secret,
and the live credential tripwire still refuses a key that works against
production.

`conformance.RunEmulator` checks the nine promises the engine makes about an
emulator, none of which is about whether the emulator implements S3 correctly.
It ships with a broken emulator and a self test that requires each break to turn
exactly one behaviour red, including the one that matters most: an emulator that
refuses everything passes every other check and is useless.

The container a registration declares carries three fields that LocalStack did
not need and the other two clouds do. `Command` decides which emulator you get,
because Google ships four of them inside one Cloud CLI image whose entrypoint is
the CLI. `Companions` are the containers an emulator does not work without, such
as the MSSQL instance Azure's Service Bus emulator refuses to start without, and
they join the inner network on exactly the emulator's terms so they have no route
out either. `Maintainer` records who stands behind an image, declared rather than
inferred from the registry it sits in, because the de facto GCS emulator is
community maintained and Google ships none at all.

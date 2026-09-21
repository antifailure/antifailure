# added

The emulators started empty, so a bucket production has existed nowhere in the
twin.

Every emulator an environment starts is configured to keep nothing: LocalStack
with PERSISTENCE off, Azurite fresh, and the storage emulator with its backend
in memory. That is deliberate, because a twin that inherited the last twin's
buckets would be reproducible only by accident. Nothing filled the gap it
leaves, so an application that reads its own bucket, queue, table, stream,
parameter or secret on startup met an emulator that had none of them, and the
failure it reported was a missing bucket, which is a true sentence about the
twin and a false one about production.

`af up` now creates the cloud resources production's infrastructure as code
declares, inside the emulators, before any service starts. The requests go
through the environment's own sidecar at the provider's own hostname, so what
is proved is the route the application has rather than the existence of a
bucket somewhere: a host the egress policy does not route is reported refused,
exactly as the application would be refused at it.

What an emulator cannot hold is NAMED rather than passed over. Every attribute
a declaration carries is accounted for by subtraction, so an attribute nobody
thought about is reported rather than dropped: a lifecycle rule that LocalStack
stores and never runs, a bucket location the storage emulator answers
US-CENTRAL1 for whatever is asked, a SecureString parameter created as a plain
String because the surface answers for no key service. A secret and a parameter
hold a placeholder and are reported as substituted, never reproduced, because
production's value must not be copied into a container a third party image
runs. A resource is called reproduced only after it has been read back out of
the emulator, and a run in which everything reproduced prints no caveat at all.

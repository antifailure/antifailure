# added

The AWS surface an environment will answer for, declared, published and proved
against the vendor's own SDK with zero endpoint overrides.

Using an emulator normally means changing the program: an endpoint override, an
AWS_ENDPOINT_URL, a client constructed one way under test and another way in
production. Whatever that run proves, it proves it about code nobody ships. The
part worth building is not the emulator, it is removing that change, and this
release lands the half of it that can be measured.

Nine services are declared and published as the surface: S3 including virtual
hosted bucket addressing, SQS, SNS, DynamoDB, Kinesis, EventBridge, Secrets
Manager, Systems Manager Parameter Store, and STS. STS is there because
credential chains call it before the first real request, so a surface without it
fails at startup with an error naming the wrong thing.

The proof is a suite that runs on every build. The AWS SDK for Go is built from
the default configuration chain, told nothing, and it reaches LocalStack anyway.
The AWS SDK for JavaScript reads no proxy variable at all, so it is driven the
other way, through DNS on an internal Docker network, which is the mechanism
that does not depend on a library choosing to honour a variable. Neither
application names an endpoint. An AWS host outside the surface is refused rather
than answered, with the same 403 the sidecar already writes for a blocked host,
because a wrong answer from an emulator is worse than a refusal: it will be
trusted.

Read docs/guides/aws.md for the surface and the measurements behind it. It
opens by saying which half of this is built, and that is worth reading first:
egress.rules[].mode has no emulate value yet, so a manifest cannot ask for this
and no environment routes to it today. What ships here is the declaration, the
registration, the generated notices, the published surface, and the suite that
holds all of it to the SDKs. A test fails when the guide and the code disagree.

One fact worth reading before planning around this: LocalStack archived its
community edition in March 2026 and the current image refuses to start without
an auth token. The image is pinned by digest to the final community build, which
starts offline and needs no account, and an organization with a licence can
register the supported image under the same name from its own build.

# added

An environment can answer AWS calls itself, and the application says nothing
about it.

Using an emulator normally means changing the program: an endpoint override, an
AWS_ENDPOINT_URL, a client constructed one way under test and another way in
production. Whatever that run proves, it proves it about code nobody ships. An
environment now routes nine AWS services to LocalStack behind the names the SDK
already resolves, and the suite that proves it is written the way an application
is written: the AWS SDK for Go is built from the default configuration chain,
told nothing, and it reaches the emulator anyway. Zero endpoint overrides, zero
lines of application change.

The nine are S3, including virtual hosted bucket addressing, SQS, SNS, DynamoDB,
Kinesis, EventBridge, Secrets Manager, Systems Manager Parameter Store, and STS.
STS is there because credential chains call it before the first real request, so
a surface without it fails at startup with an error naming the wrong thing.

The list is the surface, and an AWS host outside it is refused rather than
answered, in AWS's own XML error shape, because a wrong answer from an emulator
is worse than a refusal: it will be trusted. The guide at docs/guides/aws.md is
the surface, and a test fails when the guide and the code disagree.

One fact worth reading before planning around this: LocalStack archived its
community edition in March 2026 and the current image refuses to start without
an auth token. The image is pinned by digest to the final community build, which
starts offline and needs no account, and an organization with a licence can
register the supported image under the same name from its own build.

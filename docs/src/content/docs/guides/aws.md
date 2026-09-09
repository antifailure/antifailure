---
title: AWS
description: The AWS surface an environment answers for itself, and the surface it refuses.
sidebar:
  order: 22
---

An environment can answer AWS calls itself, with **no endpoint override in the
application**. Every name resolves to the sidecar, the sidecar terminates TLS
with the certificate authority the environment already trusts, and it answers
for `s3.amazonaws.com` itself. The code that runs is the code that ships: no
`AWS_ENDPOINT_URL`, no client constructed differently in tests, no branch on an
environment variable.

The emulator behind it is [LocalStack](https://github.com/localstack/localstack).
Antifailure does not write emulators. S3 alone has a decade of edge cases in it,
a hand written replacement would be worse on day one and probably for two years,
and nobody buys this product because its S3 emulator is good. What is worth
building is the part people hate about using an emulator, which is changing the
application to reach it.

## The surface

**This table is the surface.** An AWS host that is not in it is not routed to
the emulator: it falls through to the environment's egress policy, whose default
is `block`, and it is refused. That is deliberate. A silent wrong answer from an
emulator is worse than a refusal, because the wrong answer will be trusted.

| Service | Hosts answered | Proved by |
| --- | --- | --- |
| Amazon S3 | `s3.amazonaws.com`, `s3.*.amazonaws.com`, `*.s3.amazonaws.com`, `*.s3.*.amazonaws.com` | CreateBucket, PutObject and GetObject, in both addressing styles |
| Amazon SQS | `sqs.*.amazonaws.com` | CreateQueue, SendMessage and ReceiveMessage |
| Amazon SNS | `sns.*.amazonaws.com` | CreateTopic and Publish |
| Amazon DynamoDB | `dynamodb.*.amazonaws.com`, `streams.dynamodb.*.amazonaws.com` | CreateTable, PutItem and GetItem |
| Amazon Kinesis | `kinesis.*.amazonaws.com` | CreateStream and PutRecord |
| Amazon EventBridge | `events.*.amazonaws.com` | PutRule and PutEvents |
| AWS Secrets Manager | `secretsmanager.*.amazonaws.com` | CreateSecret and GetSecretValue |
| AWS Systems Manager Parameter Store | `ssm.*.amazonaws.com` | PutParameter and GetParameter |
| AWS STS | `sts.amazonaws.com`, `sts.*.amazonaws.com` | GetCallerIdentity and AssumeRole |

A star stands for one whole label, so `sqs.*.amazonaws.com` is every region and
`*.s3.*.amazonaws.com` is a virtual hosted bucket in every region. The leading
star covers one label or more, which is what makes a bucket whose name contains
a dot reachable.

The "proved by" column is not decoration. Each of those calls is made by the
vendor's own SDK against a running emulator in this repository's own test suite.
A service listed with nothing proving it is a claim, and a claim in a table
somebody trusts is the failure this table exists to avoid.

### STS is in the surface on purpose

Most AWS SDKs resolve credentials before the first real call, and several
credential chains call `sts.amazonaws.com` to do it. An emulated surface without
STS fails at startup, with an error naming the credential chain rather than the
service anybody was trying to reach, and the person reading it goes looking at
S3.

## What is outside it, and why

| Not answered | Why |
| --- | --- |
| AWS Lambda, ECS, EKS, Batch and Step Functions | LocalStack runs these by starting further containers through the Docker socket. An environment does not hand a container the Docker socket, so this is refused rather than half answered. |
| Amazon RDS, Aurora, ElastiCache and OpenSearch | A datastore is not emulated. Postgres is branched from a golden, and a second store is declared in the manifest with a stance. An emulator with an empty schema in it is a worse answer than either. |
| Amazon SES and SESv2 | Mail is captured into the environment's [inbox](/docs/guides/inbox/), where an agent can read it and no real address receives anything. An emulator would swallow it instead. |
| Amazon API Gateway, CloudFormation, IAM, CloudWatch and everything else AWS runs | Outside the surface, and refused by the egress policy rather than answered. |
| S3 dualstack, transfer acceleration and S3 Express One Zone | Further spellings of the S3 endpoint that resolve under different names. They reach nothing, and the refusal says no rule matches rather than naming S3. |

### Where the refusal actually happens

The refusal is in the ROUTING, and it is worth being precise about that rather
than claiming a second wall that does not exist.

The container is started with `SERVICES` listing the nine and
`STRICT_SERVICE_LOADING` set. Measured against the pinned digest on 2026-09-08,
that leaves 23 of the 35 services LocalStack knows about reporting `disabled`
and twelve reporting `available`: the nine above, DynamoDB Streams which the
surface routes, and KMS and Lambda, which load because services in the list
depend on them. A GET to `/2015-03-31/functions` with a Lambda `Host` header is
then answered `200 {"Functions": []}` by the container.

That is exactly the silent wrong answer a declared surface exists to prevent,
and what prevents it is that `lambda.*.amazonaws.com` is not a host any covered
service claims. Nothing routes the request to the emulator, so the environment's
egress policy decides it, and the default is `block`. The container allowlist is
a smaller attack surface and a shorter start, not the refusal.

## How the application reaches it, which is DNS and not a proxy variable

**Zero endpoint overrides is achieved by DNS interception, not by proxy
configuration.** It is worth reading that sentence twice if you were planning
around the proxy variables, because one of the two SDKs below ignores them
completely.

An environment reaches the sidecar two ways. The proxy variables are the weaker
one: a library is free to ignore them, and the AWS SDK for JavaScript ignores
them entirely, so `HTTPS_PROXY` does nothing for a Node application. The one
that always holds is the network. Every external name resolves to the sidecar,
the sidecar terminates TLS with a certificate authority the environment already
trusts, and a client that reads no variable at all still arrives there. A
service that somehow bypassed both has nowhere to send the packet, because the
inner network has no route out.

The suite that proves this drives both paths on purpose. The AWS SDK for Go is
driven through the proxy variables, and the AWS SDK for JavaScript is driven
through DNS, on an internal Docker network with a router answering on 443 and
one name mapped per hostname. Neither application names an endpoint.

## What the sidecar rewrites, and what it does not

**The destination is rewritten. The `Host` header and the `Authorization` header
are preserved.** Both of those are facts about the protocols rather than
preferences:

- Virtual hosted S3 addressing carries the bucket name in the `Host` header, and
  that is where LocalStack reads it from. Rewriting `Host` destroys the bucket
  name and breaks the case this guide is loudest about.
- SigV4 signs the `Host` header. Rewriting `Authorization` without re-signing
  produces a signature that disagrees with its own request, which is fragile
  against any emulator that parses the key id.

The credential cannot escape regardless of what the header holds, and that is a
property of the network rather than a promise: the emulator is attached to the
environment's inner network only, which Docker creates with `internal` set, so
it has no route out. The sidecar refuses a request signed with a key that
[livekey](/docs/concepts/egress/) recognises as a live one, so a real `AKIA` key does
not reach the emulator either.

## The LocalStack image, and a fact worth reading before you plan around it

**LocalStack's Community edition was archived in March 2026.** The project moved
to a single "LocalStack for AWS" image which requires an auth token, and the
final community build is published as the `community-archive` tag. The image
this build starts is that final community build, pinned by digest:

```
localstack/localstack@sha256:6b6172cfceb04b4fbc35097a55f717c365a35fafa572be49f7341771cf9023ed
```

It is pinned by digest rather than by tag because an emulator is the thing
answering for production's API, and a tag that moves changes what an environment
was tested against with nothing in this repository changing. A tag is refused by
the registry's validation.

What that means in practice:

- Running the suite needs **no LocalStack account and no token**. The archived
  community image starts offline and answers for the nine services above.
- The archived image does not gain new AWS behaviour. When AWS changes an API in
  a way the archive predates, this surface is what it is, and the gap register
  is where that is recorded rather than discovered.
- An organisation with a LocalStack licence can point the environment at the
  supported image instead, by registering an emulator named `aws` from a build
  of their own through `extension.AddEmulator`. The registry refuses two
  emulators under one name, so that is a replacement rather than a shadow.

LocalStack is licensed under the Apache License 2.0 and is recorded in
`THIRD_PARTY_NOTICES.md`, which is generated from the same declaration the
engine starts the container from.

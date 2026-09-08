---
title: Air gapped
description: An installation that reaches nothing outside your own network, with the list of what it refuses and the count from a real run.
sidebar:
  order: 9
---

An air gapped installation reaches nothing outside your own network. Not the
licence server, because there is not one. Not a telemetry endpoint, not a
release check, not a model provider, not Docker Hub, and not the third party
APIs your application calls.

This page is deliberately specific about what that means, because a partial air
gap described as complete is worse than no feature at all. The buyer who needs
this is the buyer who cannot tolerate being wrong about it.

## Turning it on

```sh
export AF_LICENSE_KEY=...
export AF_ORG=acme
export AF_AIR_GAPPED=1
export AF_AIR_GAPPED_ALLOW='registry.example.com:5000,10.4.0.0/16,vault.example.com'
af up
```

`AF_AIR_GAPPED_ALLOW` is your own network, and it is empty by default. Each
entry is a hostname, a hostname and port, an IP address or a CIDR. A bare
hostname permits every port on it; an entry that names a port permits that port
and no other.

**A private range is not permitted implicitly.** An internal registry on
`10.0.0.0/8` is reachable because you named it, not because the range looked
harmless. A flat corporate network would otherwise widen the air gap for
everybody on it, silently.

**Loopback and unix sockets are always permitted.** The sidecar, a local
Postgres and the Docker daemon are addressed there, and an installation that
could not reach them could not run at all.

**An entry that is not an address stops the binary.** `https://registry.example.com/v2/`
is refused rather than ignored, because an allow list with a typo in it is one
that is quietly narrower than you believe, and you find that out at three in the
morning.

## What happens without the licence

`AF_AIR_GAPPED` set on an installation whose licence does not include
`air_gapped` **does not start**. It does not warn and carry on unsealed.

The failure this feature exists to prevent is an installation that believes it
is air gapped and makes one call it did not expect, and the belief is the part
that does the damage. An operator who asked for an air gap and got an open
network plus a line on standard error is in a worse position than one who got an
error and fixed it.

For the same reason there is no way to unseal a running process. Everywhere else
in this product a licence is asked per call, so that a lapse degrades a feature
rather than requiring a restart. This one is the opposite on purpose: the
expensive direction of the mistake is not "the air gap stopped working", it is
"the machine in the secure facility started talking to the internet because a
purchase order was slow".

## What it refuses

Every outbound client in the engine dials through one guard. Sealed, each of
these is refused unless the address is in your allow list, and each refusal is
recorded with the site that made it.

| What | Where it would have gone |
| --- | --- |
| the release check | `api.github.com`, and the release download `af update` fetches |
| the telemetry exporter | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| the model key probe | your model provider, from `af model test` and from the MCP server |
| the workflow oracle | the two deployments `af oracle` compares |
| the identity provider seeding | Clerk, Auth0, WorkOS |
| the control plane client | the control plane |
| the control plane identity discovery | the control plane's OIDC endpoint |
| the device authorization login | the control plane |
| the load generator | the application under test |
| the S3 golden store | AWS |
| the Azure Blob golden store | Azure |
| the Neon control API | `console.neon.tech` |
| the Supabase management API | `api.supabase.com` |
| the Database Lab API | your DBLab server |
| the ClickHouse HTTP interface | your ClickHouse server |
| the service readiness probe | the environment, over loopback |
| the webhook delivery | a service in the environment |
| the doctor reachability check | whatever it was asked about |
| the enterprise secret store | AWS, GCP, Azure or Vault |
| the runtime conformance suite | the internet, on purpose, which is why it is here |
| the container image pull | the registry the image reference names |
| the container image build | Docker Hub, for the sidecar's base image |

Three of those are worth naming separately.

**Name resolution.** `af doctor` resolves a host without dialing it, and a
resolver query is an outbound packet carrying exactly the name an air gapped
installation was not supposed to be interested in. It does not look like a
connection, which is why it is the one that gets missed. The guard also refuses
a hostname **before** resolving it, so a refused connection does not put the
name on the wire on its way to being refused.

**Container images.** A pull happens in the Docker daemon, over a socket the
guard never sees, so it is checked against the registry the reference names
before the daemon is asked. Both callers look for the image locally first, so an
installation that loaded its images from a tarball or an internal registry runs
untouched. What is refused is the silent reach for Docker Hub.

**The sidecar image.** Its Dockerfile begins `FROM golang:1.25-alpine`, so
building it on demand is a pull from Docker Hub on the path of every `af up`.
Under an air gap it is refused outright rather than pointed somewhere else:
publish the image to your own registry and load it, and the build is never
reached.

## What your application may do

The largest outbound path in a preview environment is not the engine, it is the
application. Egress rules decide that, and an air gapped installation refuses an
environment whose rules would leave your network, **before** it is created,
naming every rule.

| Mode | Air gapped |
| --- | --- |
| `block` | permitted, the request is refused inside the environment |
| `capture` | permitted, the message is recorded and the provider's success shape returned |
| `mock` | permitted, answered from a pack in your repository |
| `allow` | **refused**, it forwards the request to the real host |
| `sandbox` | **refused**, it substitutes a test credential and still forwards to the real host |
| `synth` | **refused**, it asks a model provider to invent the response |

The same applies to `egress.default`, which is the mode every host no rule names
gets. A manifest with `default: allow` and no rules at all reaches the whole
internet, and it is refused for exactly that.

`sandbox` is the one people are surprised by. Substituting a test credential
does not stop the connection being made or the request leaving; it changes what
the request carries.

The environment is refused rather than quietly downgraded. An environment
switched from `allow` to `block` behind your back would come up, go green, and
report that it tested a code path it never reached.

## What is not covered, and why

Three things sit outside the guard, and it is better to read them here than to
discover them.

**Building your application's image.** `docker build` runs in the daemon and in
BuildKit, and what it fetches is a base image and whatever your package manager
resolves. None of that passes through this process. Governing it is the daemon's
job: build on a machine whose registry mirror and package mirror are internal,
or use `build.strategy: image` and supply a prebuilt image, which an air gapped
installation usually already does.

**The Postgres connection.** Connections made by the database drivers go to the
URL you supply. If that URL names a hosted provider, the connection leaves your
network and the guard does not sit on it. The cloud providers' own control APIs,
which is how a Neon or Supabase branch is created in the first place, **are**
guarded and are refused, so the ordinary way of reaching one of those is already
closed.

**The Kubernetes runtime.** `af` talks to whatever cluster your kubeconfig names.
That is your cluster by definition, and the guard does not sit on the client.

**The Docker daemon.** `af` talks to the daemon `DOCKER_HOST` names, which is a
unix socket on the machine by default and is permitted for that reason. Pointing
it at a remote daemon over TCP is a connection the guard does not sit on.

## Proving it

The count that matters is not a list of call sites, it is what a real run does.
`engine/internal/runtime/local` carries a test that seals the guard and then
performs a complete lifecycle, bringing an environment up on real Docker,
serving a request through it, and tearing it down. It asserts that the ledger
contains **zero refusals**, and separately that the ledger contains the readiness
probe, because zero refusals out of zero observations is not a measurement.

`engine/pkg/airgap` carries a second test that walks the source of both modules
looking for an outbound client that does not go through the guard. It has its
own test that it can say no, pointed at a fixture that reaches the network six
different ways, because a walk that silently skipped every path would report a
clean repository in exactly the same words. And a third test compares the table
above against the guard's own source in both directions, so a site added without
a row here, or a row here naming a refusal that does not happen, is a failure
rather than a slow drift.

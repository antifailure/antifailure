---
title: The local runtime
description: How an environment runs on your machine, and what the failures mean.
sidebar:
  order: 2
---

Locally, an environment is a set of containers on two Docker networks: an inner
one the services share, and an outer one only the egress proxy can reach. A
service has no route to the internet except through the proxy, which is what
makes the policy an enforced boundary rather than a configuration file.

```
        ┌──────────── inner network ────────────┐
        │  web    worker    cron    database    │
        └──────────────────┬────────────────────┘
                           │ (the only way out)
                     egress proxy
                           │
                    ┌──────┴──────┐
                  outer network / internet
```

Everything is labelled with the environment id, so teardown of one environment
can never touch another's.

## The daemon

```
AF-RUN-002 The Docker daemon at unix:///var/run/docker.sock could not be
reached.
```

`af doctor` checks this and everything else about the machine before you need
it, and names the command that fixes each thing it finds.

The daemon has to speak Docker API 1.40 or later, which is Docker Engine 19.03
and every release since. The floor belongs to the Docker client library the
engine is built with rather than to a policy of ours: below it the client
refuses to negotiate a version and sends its requests unversioned, and what an
older daemon does with those is not something any release has been checked
against. `af doctor` reads the daemon's API version and fails its Docker check
below the floor, naming the version it found, so the mismatch is reported
before an environment is attempted rather than halfway through one.

## The egress sidecar image

The first thing `af up` needs is the egress sidecar's image, and a release
publishes it to `ghcr.io/antifailure/af-proxy` for `linux/amd64` and
`linux/arm64`. On a machine that has never run `af`, the engine fetches it,
which is one small image, and says so:

```
fetching the egress proxy ghcr.io/antifailure/af-proxy:<digest> (once per version)
```

The tag is a digest of the sidecar's own source, not a version number, so a
build of `af` from a commit that changed the sidecar has a digest no release
published. That build compiles the image instead, from the source the binary
carries, and prints each step as it goes, including the pull of the Go base
image the compile starts from. A line every fifteen seconds says how long the
step has run, out of how long it may, and what the daemon last reported, so a
stalled download and a slow compile no longer look the same.

Each attempt is bounded: two minutes to fetch and ten to compile. A step that
runs out of time stops with `AF-RUN-048`, naming what it was doing and the last
thing the daemon said. On a slow machine, allow more for both:

```
AF_PROXY_IMAGE_TIMEOUT=25m af up
```

To take the image from a registry you run instead, name it:

```
AF_PROXY_IMAGE=registry.example.com/antifailure/af-proxy:<digest> af up
```

A named image is fetched and never replaced by a compile, because naming one
usually means this machine should not be reaching Docker Hub. Whatever it is
called, the image has to say it is this sidecar: every sidecar image carries a
`dev.antifailure.proxy-sources` label naming the digest of the source it was
built from, and one whose label does not match the source this `af` carries is
refused rather than run. An image `af` compiled carries the label too, so
pushing it into your own registry works.

A service that publishes a port is reached through a small forwarder on your
loopback, and the forwarder is this same sidecar image started in forward mode.
So publishing a port fetches and builds nothing beyond the sidecar itself: no
second image, no base image, and no package download.

## A service that never becomes ready

```
AF-RUN-004 Service web did not become ready within 180s.
```

Readiness is an HTTP request to `health_path`, defaulting to `/`. Any status
counts, including 500: readiness means the process is listening and routing,
not that the application is healthy. A service answering 500 has started, and
reporting it as never having started would send you to the runtime instead of
to your own handler.

The usual cause is binding to `127.0.0.1` inside the container, which makes the
service unreachable from anywhere including the check. Bind to `0.0.0.0`. `PORT`
is set in the environment for you.

For a slow start, raise it:

```yaml
services:
  - name: web
    health_path: /healthz
    health_timeout: 300s
```

## An emulator that never starts listening

```
AF-RUN-049 The probe emulator started but never accepted a connection at af-emu-probe:8080 within 3m0s, so the environment was torn down.
```

An emulator is a third party container, and starting one is not the same thing
as being able to talk to it. The daemon reports a container started the moment
its first process is running, while the server inside binds its port some time
after that: measured on this machine, the Google emulators take between 17.7 and
51.5 seconds to accept their first connection, and LocalStack spends its own
seconds loading providers. So `af up` starts the emulators, starts the sidecar,
and then dials each emulator from inside the environment until it answers, before
any of your services are created.

That dial is the reason for this wait. Without it an application that calls out
the instant it starts reaches the sidecar, the sidecar forwards to a port nothing
has bound yet, and the application reads `502 Bad Gateway` from its own SDK. That
502 is the same status the sidecar returns for an emulator the environment is not
running at all, so the symptom pointed at the manifest while the cause was the
clock.

Each emulator has three minutes. An environment whose emulator never binds is
torn down rather than left standing, because every call it would answer is a 502
and that is the misleading symptom this wait exists to remove. For an emulator
that genuinely needs longer, say so:

```
AF_EMULATOR_READY_TIMEOUT=6m af up
```

A container that exits instead of binding is usually a command the image does not
have or a companion container the emulator refuses to start without. `af logs`
does not carry an emulator's output, and `docker logs af-emu-<name>-<env>` does.

## A service that exits immediately

```
AF-RUN-005 Service web exited with code 1 during startup.
```

The last lines of its output come with the error. `af logs web` has the rest.
The most common causes are a missing environment variable and a command that is
correct for your shell but not for the image's.

## Ports

```
AF-RUN-009 No free port was found in the range 46000-47999 to publish the
environment on.
```

Usually environments that were never torn down. `af env list` shows them and
`af env prune` lists the ones older than a day and removes nothing, and
`af env prune --yes` removes what it listed.

Databases are published from 43000 and services from 46000. `af doctor` probes
twenty ports of each range and says how many are free. `AF_PORT_RANGE_START`
moves both together: set it to the first port of a range that is free, and
services are published 3000 above it. It belongs in your shell or your runner's configuration rather than in
the manifest, because a machine is what runs out of ports and two people sharing
one repository need different answers.

```
AF_PORT_RANGE_START=51000 af up
```

A port that is free when Antifailure reserves it can be taken by something else
before the daemon binds it. That is retried on a fresh port rather than
reported, so the address `af up` prints is the one that was bound, which is not
always the one a service was told at startup: an application that builds
absolute URLs from `AF_PUBLIC_URL` or `AF_ENV_URL` may name the port it lost.
Bringing the environment up again after freeing the port gives every container
the same answer.

## Networks

```
AF-RUN-052 The environment's network could not be created, because Docker has
no address range left to give it: Docker has handed out every address range it
is allowed to. The daemon holds 30 networks, and 14 of them are Antifailure
networks with no container attached
```

Every environment gets two networks, and every network takes one address range
from a fixed set Docker hands out. The defaults hold about thirty one, and
Docker counts every network on the machine against them, whoever made it. The
usual cause is environments whose run was killed before its teardown: their
networks stay behind with nothing attached, each still holding a range.

`af env prune --orphaned` lists exactly those, the environments that hold
networks with nothing attached and nothing running, and removes nothing.
`af env prune --orphaned --yes` removes what it listed. An environment counts
only once nothing has been created in it for an hour, so one being brought up
right now is never taken, and a network without the Antifailure label is never
considered at all. `af doctor` counts them in its leftover environments check.

If the message counts few networks of ours, the daemon is full of another
tool's. `docker network ls` names them, and widening `default-address-pools` in
Docker's daemon settings makes room for more.

## Size

```
AF-RUN-047 This runtime cannot place the sizes the manifest asks for: service
"clickhouse" asks for 32Gi of memory per instance and the roomiest node has
7Gi free, so one instance of it cannot be placed at all
```

`resources.cpu` and `resources.memory` become the daemon's own cpu and memory
constraint. There is no scheduler here to reserve anything, so the single value
the manifest carries is applied as the cap alone: a container gets that share
of the machine under contention and no more, and one over its memory cap is
killed rather than allowed to take the machine down with it. That is the half
of the promise this runtime can keep, and it is the half that matters on a
laptop, where the failure being reproduced is one environment starving another.

The check runs before the network is created, so an environment this machine
cannot hold leaves nothing behind for `af down` to find.

**What it does not account for.** Docker reserves nothing. A container with no
memory limit, which is most of them and every container this machine was
already running, is not holding anything the daemon can subtract, so the
comparison is against the whole machine rather than against what is free. This
refuses an environment that could never fit and it does not refuse the eleventh
environment on a machine that holds ten. The cluster check does better, because
a cluster scheduler has the fact this one does not: what every pod asked for.

The daemon's memory is the Docker VM's, not the machine's. A laptop with plenty
of memory whose VM was given a quarter of it has a quarter here, and `docker
info` is where that number comes from.

## Disk

```
AF-RUN-010 Writing to /Users/you/.antifailure failed because the disk is full;
the state directory is required.
AF-RUN-020 Docker has no room left for the environment: no space left on device
```

`af golden gc` reclaims goldens nothing branched from, which is usually the
larger number with the Docker provider, since each one is an image. `docker
system prune` handles what belongs to Docker rather than to Antifailure.

## Two runs at once

```
AF-RUN-003 Another Antifailure process holds the lock for this branch (process
4821, since 12:04).
```

Two `af up` runs on one branch would race on the same names and both fail in
ways neither explains, so the second waits. If the first died without releasing
it, `af down` cleans up.

Related: [the journal](/docs/concepts/journal), [egress](/docs/concepts/egress),
[building](/docs/guides/build).

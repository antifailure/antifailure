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

## A service with no port

A service that publishes no port has nothing the runtime can poll from outside,
and most services in a real stack are like that: a compose file leaves a port
unpublished because only other services reach it. So readiness has three
answers rather than two.

| Answer | Means |
| --- | --- |
| proved | A check ran and passed: the port answered, or the health command exited zero. |
| unproved | The service is running and there was nothing to check. |
| failed | A check did not pass in time, or the container exited. |

An unproved service is not a failure, and `af up` does not stop on one. It is
not evidence either, and `af up` and `af status` say so on its line instead of
printing a tick. Before it is reported, the runtime watches the container for
five seconds, which catches a process that exits on a refused outbound call at
startup. Just before `af up` returns it looks at every service again, so one
that passed its own check and died while later ones were starting is reported
as failed rather than as up.

To prove a service with no port, give it a command that exits zero once it is
ready. It runs inside the container through `/bin/sh -c`, so an image with no
shell cannot use one.

```yaml
services:
  - name: db
    kind: worker
    health_command: pg_isready -U postgres
```

A command is also the right check for a store that accepts connections before
it is usable. Postgres answers on its port while its init scripts are still
running, and a connection cannot tell that apart from finished. A service may
declare `health_path` or `health_command`, not both.

```
AF-RUN-050 Service db did not pass its health command within 180s.
```

The command is run for every instance of the service, so with `replicas: 3`
each of the three has to pass it.

One limit, stated: this runtime checks readiness while `af up` runs. A service
that passes and later starts failing its check while its process keeps running
is not noticed until something asks it for work. The Kubernetes runtime has no
such limit, because the cluster runs the check for as long as the pod lives.

## Mounting files, directories and volumes

A service built from a prebuilt image holds none of your repository, so a
configuration file it reads at startup needs a way in. `mounts` gives it one.

```yaml
services:
  - name: keeper
    kind: worker
    mounts:
      - path: clickhouse/keeper_config.xml
        at: /etc/clickhouse-keeper/keeper_config.xml
      - path: clickhouse/init
        at: /docker-entrypoint-initdb.d
      - volume: coordination
        at: /var/lib/clickhouse-coordination
```

A `path` is a file or directory in the repository. It is **copied** into the
container before the process starts. It is never bound to the machine. That
choice is deliberate:

- The service cannot write back into your working tree, which is the source of
  the next build.
- The daemon needs no share of your filesystem, so a remote or virtual machine
  daemon behaves the same as a local one.
- The copy is taken once, when the environment comes up. Editing the file
  afterwards does not reach a running container, and deleting it cannot break
  one. Run `af up` again to pick up a change.

A path that leaves the repository is refused, and so is one that leaves it
through a symbolic link. A missing path is refused before anything starts,
because a service whose configuration did not arrive starts on its image's
defaults and would report itself running. One mount may carry 16 MiB across at
most 2000 files.

A `volume` is a named volume this environment owns. It is the one writable
surface a mount creates. It keeps what the service writes across a restart of
that service, several services may share one, and `af down` removes it with the
rest of the environment. It is never a path to your machine.

```
AF-RUN-048 A mount on service keeper could not be read: clickhouse/keeper_config.xml: no such file or directory
```

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
2.0 GiB is required.
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

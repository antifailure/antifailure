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
`af env prune --older-than 24h` removes the old ones after printing what it
would do.

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

Related: [the journal](/docs/concepts/journal/), [egress](/docs/concepts/egress/),
[building](/docs/guides/build/).

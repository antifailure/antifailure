---
title: Runtimes
description: Where an environment's containers actually run, what each runtime declares it can do, and why a runtime says no rather than reporting an address that does not resolve.
sidebar:
  order: 10
---

A runtime is where an environment's containers actually run. Everything above
it, the manifest, the golden, the masking, the sidecar, the fidelity report, is
the same whichever one is chosen.

```yaml
runtime:
  provider: local   # or kubernetes
```

## What ships

| Runtime | An environment is | Detail |
| --- | --- | --- |
| `local` | A network on the local Docker daemon, one container per service, plus a port forwarder per web service | [The local runtime](/docs/guides/local-runtime) |
| `kubernetes` | A namespace, with a Deployment and a Service per service and an Ingress per web service | [The Kubernetes runtime](/docs/guides/kubernetes-runtime) |

Both are MIT and both are in the engine. Running one is not an enterprise
feature. Running SEVERAL from one control plane, and placing an environment on
the right one, is the `multi_runtime` licensed feature, because deciding which
pool an environment belongs in is a question only an organization has:
residency, an isolated pool for regulated repositories, a pool with more
memory. See [multiple runtimes](/docs/enterprise/runtimes).

## What a runtime declares

Three capabilities, and each of them exists because the honest answer is
sometimes no.

| Capability | `local` | `kubernetes` |
| --- | --- | --- |
| Reachable from the machine that ran `af` | yes | only with a `domain` to publish under |
| Logs | yes | yes |
| Can attach a database container from the local daemon | yes | no |

**Reachability is not a formality.** The Kubernetes runtime declares it only
when a domain is configured, because without one there is no Ingress and no
address a caller could reach, and declaring otherwise would mean `af up`
printing a URL that resolves to nothing.

**Attaching a local database is the one that decides your database provider.** A
database container on the machine that ran `af` is not reachable from a
cluster, so on Kubernetes the database has to be one the environment can
already reach: `neon`, `supabase`, `dblab` or `pgurl` pointed at a server the
cluster can route to. A runtime that declared this true when it was not would
make the engine attach a branch no pod can connect to.

## Containment is the runtime's job

Whichever runtime is chosen, an environment reaches nothing it was not given.
The egress policy, the sidecar that terminates TLS with a certificate the
environment already trusts, and the network rules that make the sidecar the
only way out are all built by the runtime. A runtime that cannot enforce that
is not a runtime this engine will ship, whatever else it can do. See
[egress](/docs/concepts/egress).

## Writing one

Implement `provider.Runtime` and run the suite:

```go
func TestMyRuntime(t *testing.T) {
    conformance.RunRuntime(t, factory, conformance.RuntimeOptions{})
}
```

The runtime suite ships with a deliberately BROKEN fake and a self test that
proves the suite fails against it, one behaviour at a time. That is the
standard every extension point here is held to, and it is not a formality: a
conformance suite nobody has proved can fail is a suite that proves nothing,
and a declared behaviour means nothing until somebody has watched it say no.

A behaviour a runtime cannot support is skipped EXPLICITLY, naming the missing
capability. A silent skip is how an implementation ends up claiming
conformance it does not have, and the skip line is what a reviewer reads.

`local` and `kubernetes` are reserved names. A registration under one of them
is refused at validation rather than accepted and then never consulted, because
the built in runtimes are looked up first.

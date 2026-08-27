---
title: The Kubernetes runtime
description: How an environment runs on a cluster, why it refuses some clusters, and what the failures mean.
sidebar:
  order: 3
---

An environment on Kubernetes is a namespace. Everything in it belongs to that
namespace and to nothing else, which is what makes teardown a single delete and
what makes two environments of one repository unable to reach each other.

Set it in the manifest:

```yaml
runtime:
  provider: kubernetes
  kubeconfig_context: my-cluster
  namespace_prefix: af-env-
  domain: preview.example.com
```

Only `provider` is required. Without `kubeconfig_context` the current context is
used, which is worth stating plainly: the difference between a throwaway cluster
and a production one is usually a context name nobody checked.

## What goes into the namespace

One Deployment and one Service per service in the manifest, so a manifest that
says `http://worker:8080` means it. One Deployment and Service for the egress
sidecar. A Secret holding the sidecar's configuration. Five NetworkPolicies. An
Ingress per web service, when a domain is set.

A service that declares `migrate` gets a Job that has to finish first. It is
never retried, because one clear failure reads better than six minutes of a Job
that is neither running nor finished, and because a half applied migration is
worse than a refused one.

## Containment

The guarantee is the one the local runtime makes, reached differently.

Every namespace gets a NetworkPolicy that denies all traffic in both
directions. On top of that, a service may reach exactly one thing: the
environment's own sidecar, on the proxy port and on DNS. Services may reach each
other, because that is what a manifest means when one service names another, and
the rule that permits it selects pods rather than namespaces, so it can never
match anything outside.

Every pod resolves names through the sidecar and through nothing else. The
sidecar answers with its own address for anything outside the environment and
forwards anything inside it to the cluster's resolver. So a client that ignores
its proxy variables, which Node does entirely and many SDKs do by accident, is
still decided: the name resolves to the sidecar, and the packet has nowhere else
to go.

The sidecar is the only pod with a route off the cluster, and even it does not
get an unqualified one. Its egress excludes the link local range, which carries
the instance metadata endpoint and with it the node's own cloud credentials, and
the private ranges, which carry the cluster's control plane and whatever else is
on the operator's network.

No pod gets a service account token. A pod that can talk to the API server can
delete the policy that is containing it.

## Why it refuses some clusters

A NetworkPolicy is a request to whatever CNI the cluster runs, and a CNI is
free to accept the object and enforce nothing. The API gives you no signal
either way: the policy is stored, it reads back correctly, and `kubectl get
networkpolicy` lists it whether or not a single packet is being dropped.

On such a cluster every object here is created successfully, every status reads
green, and every environment can reach the internet, the metadata endpoint and
each other. There is no error anywhere. The policy exists; it is decorative.

So before any service image runs, the runtime starts one pod under exactly the
rules a service runs under and has it try to get out four ways: a direct TCP
connection to a public address, a UDP query straight to a public resolver, the
metadata endpoint, and the cluster's own API server. If any of them works, the
environment does not start and you get **AF-RUN-043**.

That check also fails when it cannot answer, and that is deliberate. A probe
that could not run tells you nothing about whether the cluster contains
anything, and an unanswered question about a security control is not a pass.

Use a cluster whose CNI enforces NetworkPolicy. This page deliberately does not
give you the list of which ones do, because that answer changes with their
releases and a list in a document ages into a confident lie. The probe is the
authority: it asks the cluster in front of it rather than the cluster a document
remembers, and it asks before every environment. The one this runtime has been
proved against is k3s, in the k3d cluster the conformance run below used.

If you get **AF-RUN-043**, read it as a statement about the cluster and not
about the runtime. The message names which of the four routes got out. No
service image ran and no sidecar started, but the namespace and its policies
were created before the probe, which is the point of doing it in that order, so
`af down` on that environment is still what removes them.

## Images

The engine builds service images, and the egress sidecar, on a container daemon
on the machine that ran `af`. A cluster's nodes cannot see that daemon. An image
that exists, that built successfully, that is right there in `docker images`, is
an image the cluster reports as `ErrImagePull` several minutes later.

There are two honest answers and the runtime supports both.

For a k3d or kind cluster, images are copied from the local daemon into the
nodes. This is detected from the kubeconfig context name, which is the only mark
those tools leave, so it happens for `k3d-*` and `kind-*` contexts and for
nothing else.

For any other cluster, the images have to be somewhere the nodes can pull from.
Publish the sidecar image and name it:

```
export AF_PROXY_IMAGE=registry.example.com/antifailure/proxy:<tag>
```

## Preview URLs

With `domain` set, each web service gets an Ingress at
`<environment>-<service>.<domain>` and an extra policy letting the ingress
controller in. Without a domain, no Ingress is created and the runtime reports
that it has no ingress, so `af up` prints no URL rather than one that resolves
to nothing.

## Readiness, and one real difference

A service with no `health_path` is ready when its port accepts a connection,
which is what the local runtime does and is as much as can be asked without
inventing a protocol the application does not speak.

A service that declares one is polled, and here the two runtimes differ.
Locally, any HTTP status counts as ready, including a 500, because readiness
there means the process is listening and routing. Kubernetes decides readiness
itself and treats 4xx and 5xx as not ready. So a service whose declared health
path answers 500 comes up locally and does not come up here.

Declaring a health path is a statement that the path reports health, so this is
the more defensible of the two behaviours, but it is a real difference and it
belongs in front of you rather than in a support conversation.

## What this runtime does not do yet

Stated here rather than discovered later.

`af net log`, `af inbox` and `af webhook trigger` do not work against a cluster.
They read what the sidecar decided and captured, and reaching a sidecar in a pod
needs a port forward that is not built yet. They fail with **AF-RUN-044** naming
the runtime, rather than quietly reporting on this machine's containers, which
is what the engine did before the runtime selection was made to apply
everywhere.

A database provider whose branches are containers on your machine cannot be used
with this runtime: the cluster cannot route to them. That combination is refused
at `af up` with **AF-RUN-044** rather than handed to services as a connection
string that will never resolve. Use a database the environment can already
reach.

Cron services are placed as ordinary Deployments rather than CronJobs, and the
manifest's `replicas` and `resources` are not applied. Neither value reaches any
runtime today: they are dropped between the manifest and the runtime contract,
so honouring them here alone would mean one runtime enforcing a cap the other
silently ignores.

## Teardown

`af down` deletes the namespace and waits for it to be gone. Reporting success
while it is still terminating would make the next `af up` fail with a message
about a terminating namespace, which is a confusing way to learn that the last
teardown had not finished.

A namespace that will not finish terminating is almost always a finalizer
waiting on something, so the finalizers are named in the message.

It deletes only what it created, and the label decides that rather than the
name. A namespace name is derived from an environment id, so a cluster that
already had a namespace by that name would otherwise lose it and everything in
it. Every namespace this runtime makes carries `dev.antifailure.managed=true`,
set in the same call that creates the object, so one of ours without the label
cannot exist. One with the name and without the label is somebody else's, and
`af down` refuses it with **AF-RUN-045** rather than removing it.

`af up` refuses the same namespace for a sharper reason. Placing an environment
in it would not simply add objects: the first policy applied denies all traffic
in both directions, so whatever was already running in there would stop talking
to anything, with no error on either side. Refusing to start is the only
outcome that leaves the cluster as it was. A namespace this runtime made is
reused normally, which is what makes `af up` idempotent.

## Conformance

This runtime is held to the same suite the local one is, and the containment
behaviours in that suite are not skippable by any capability a runtime can
declare. A runtime that could declare its way out of them would be a supported
way to ship one that lets environments reach the internet.

It has passed: 31 behaviours green against a real k3s cluster in one run, with
one skipped because no domain was configured and there was therefore no ingress
to reach, which the suite names rather than passing over.

The skip is worth understanding before you rely on it. A behaviour a runtime
cannot support is skipped by name, so the output tells you which guarantee this
runtime did not make on that run. Nothing about containment can be skipped that
way.

To reproduce it, this is the cluster the passing run used: k3s v1.35.5 under
k3d v5.9.0, with no ingress controller, which is why the ingress behaviour is
the one that skips. The disable is written out rather than left to k3d's
default so that the cluster is the same one whatever your k3d does.

```
k3d cluster create af-conformance --k3s-arg "--disable=traefik@server:0"
AF_KUBE_CONTEXT=k3d-af-conformance go test ./engine/internal/runtime/k8s/ -run TestConformance -timeout 60m
k3d cluster delete af-conformance
```

The test skips unless `AF_KUBE_CONTEXT` names a cluster. It creates namespaces,
deletes namespaces, and runs pods that try to reach the internet, so it is never
run against a cluster by accident.

Give it the hour. The run that passed took 27m50s on a loaded machine, and most
of that is waiting for real pods to schedule rather than anything this runtime
computes. The default `go test` timeout of ten minutes will kill it partway and
leave namespaces behind.

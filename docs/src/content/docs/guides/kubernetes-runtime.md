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

Every customer-code pod also has a trusted startup gate, including migrations
and stance jobs. It uses the engine's own image, not a shell or networking tool
from the application image. Application code cannot start until that pod has
connected to its sidecar and repeatedly observed the escape routes denied.

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
A release publishes the sidecar image to `ghcr.io/antifailure/af-proxy`, tagged
with the digest of the sidecar source that release carries. Name it, or your
own copy of it:

```
export AF_PROXY_IMAGE=registry.example.com/antifailure/proxy:<tag>
```

`docker image ls antifailure/proxy` on a machine that has run `af up` shows
the digest this build of `af` carries.

## Preview URLs

With `domain` set, each web service gets an Ingress at
`<environment>-<service>.<domain>` and an extra policy letting the ingress
controller in. Without a domain, no Ingress is created and the runtime reports
that it has no ingress, so `af up` prints no URL rather than one that resolves
to nothing.

Pod readiness alone does not mean the ingress controller has updated its
backend list. The runtime also waits for the published health URL to stop
returning missing-route or gateway-unavailable responses. If the root path
deliberately returns 404 or 503, configure a `health_path` that reports
readiness. Redirects are not followed, so the probe does not sign in or visit
an external authentication service.

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

Cron services are placed as ordinary Deployments rather than CronJobs.

The manifest's `replicas` becomes the Deployment's replica count, so
`replicas: 3` is three pods behind the Service every other service resolves,
and kube-proxy spreads connections across them. Readiness waits for all three:
a service reported ready is not one whose third pod is still being scheduled.
The egress sidecar is always a single pod whatever any service asks for,
because it is the environment's only resolver and its only route out, and a
second one would split the record of what was refused across two decision logs.

The manifest's `resources` becomes the container's `ResourceRequirements`, and
the request and the limit are the SAME figure, which puts the pod in the
Guaranteed quality of service class. A dimension the manifest did not name is
left out of both maps rather than set to zero: a zero request is a request for
nothing and a zero limit is a limit of nothing, so a service that named no size
produces the identical Deployment it produced before the key was honoured.

The gap between a small request and a larger limit is where a node is
oversubscribed. Every pod is placed against its request and may then grow into
its limit, so a node that fits ten environments on paper runs eleven and the
eleventh takes memory from the others. The symptom is a workflow that reads as
flaky, and a twin whose failures belong to the machine rather than to the
change under test is worth less than no twin.

`af up` checks the sizes against the cluster BEFORE it creates anything, and
refuses with **AF-RUN-047** naming the shortfall. Without that check a request
larger than any node is accepted by the API server and the pod sits `Pending`
with an event nobody is watching, so `af up` waits out the readiness timeout
and reports a service that did not start. The free figure is each schedulable
node's allocatable minus the requests of the pods already on it, which is the
quantity the scheduler itself places against; allocatable alone would accept an
environment onto a full cluster. Cordoned and not ready nodes are left out,
because a node that still reports its allocatable and can hold nothing makes
the cluster look larger than it is.

Two necessary conditions, neither sufficient: every instance has to fit on some
single node, and the total has to fit in what is free across all of them. A set
that passes both can still fail to pack, and the scheduler remains the
authority on that. What is refused here is only the cases where no packing
exists at all, which are the ones a person cannot diagnose from a `Pending`
pod.

A cluster that will not let `af` list its nodes or its pods is one this cannot
check. It says so on the progress channel and lets the environment through,
rather than reporting nothing and passing: refusing to start because a
permission is narrow would break every cluster where `af` has namespace scoped
access and nothing more.

`af status` reports the applied size off the pod the cluster is running rather
than off the spec that was sent, because a runtime that echoed the request back
would agree with the manifest whether or not anything was applied.

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
behaviours in that suite cannot be skipped by anything: not by a capability a
runtime declares, and not by the knob that trims a slow local run. A runtime
that could declare its way out of them would be a supported way to ship one
that lets environments reach the internet, and a knob that skips them is the
same hole with a friendlier name.

Historical counts do not establish the current runtime's guarantees. Earlier
runs exposed a startup window: NetworkPolicy was programmed after a new pod
started, and its first UDP lookup escaped. A namespace-level probe could not
close that window for pods created later.

The startup gate now runs in each pod before customer code. The separate
`TestImmediateStartupCannotBypassContainment` checks the application's first
network command, with an uncontrolled positive check proving that the UDP
receiver answers. A complete proof requires both this test and every current
shared conformance behaviour, without skips. The isolated workflow retains
the individual test events, rather than turning a successful process exit into
a conformance claim.

Response-based probes do not prove the absence of every one-way packet. Use a
CNI that implements NetworkPolicy; the gates test observable paths and refuse
an incomplete answer rather than certify arbitrary CNI implementations.

The skip is worth understanding before you rely on it. A behaviour a runtime
cannot support is skipped by name, so the output tells you which guarantee this
runtime did not make on that run. Nothing about containment can be skipped that
way.

Run the isolated Kubernetes conformance workflow, or use the disposable
cluster command on a machine dedicated to this test:

```
just k8s-conformance
```

The command pins the cluster image, enables ingress on loopback, runs the full
roster plus immediate-startup proof, and deletes its cluster. Existing clusters
are refused. The ordinary ten-minute Go timeout is too short for this run;
the command supplies its own bounded timeout and checks every recorded verdict.

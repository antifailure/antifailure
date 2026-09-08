---
title: Multiple runtimes
description: Placing an environment on the right pool when there is more than one.
sidebar:
  order: 5
---

*More than one placement target requires an enterprise license with the
`multi_runtime` feature. One target needs no license.*

With one runtime there is nothing to decide. With several, an environment has to
go somewhere, and where is a policy question: a region for data residency, a
pool with more memory for a heavy repository, an isolated pool for repositories
that handle regulated data.

```yaml
runtime:
  provider: kubernetes
  domain: preview.example.com
  requires:
    region: eu-west-1
  targets:
    - name: frankfurt
      kubeconfig_context: eu-prod
      domain: eu.preview.example.com
      tags:
        region: eu-west-1
    - name: virginia
      kubeconfig_context: us-prod
      domain: us.preview.example.com
      tags:
        region: us-east-1
```

That repository is placed in Frankfurt. Every command that has to find the
environment afterwards works it out the same way, from the same file.

```
AF-SCH-001 No runtime satisfies the placement requirement region=eu-west-2.
  Next: Declare a target under runtime.targets carrying that tag, or relax
  runtime.requires. Nothing was created.
```

## Why it refuses rather than falls back

Placing an EU repository's environment in a US pool because the EU pool was full
is the kind of helpfulness that ends a compliance audit badly. A requirement
that can be silently ignored is not a requirement.

The same reasoning is why a requirement nothing can satisfy is refused when the
manifest is read rather than at dispatch. The requirement and the targets are in
one file,
so the contradiction is decidable before anything runs, and the person looking at
it is the person who wrote both lines. A scheduler in a cluster reporting the
same thing an hour later is reporting it to somebody who cannot fix it.

## Requirements and tags

Attributes, matched by equality against what each target declares: region,
instance class, isolation level, whatever your organization decides matters.
Every requirement must be met; empty requires means any target will do, and the
first one listed wins.

**The tags are declared in the manifest, not discovered from the cluster.** A
kubeconfig context is a name on somebody's laptop and it does not say which
region the cluster is in. Writing the claim in the repository puts it under
review next to the requirement that reads it, and it is one fewer thing that can
be changed by anyone with access to a cluster.

## What placement does not decide

**Capacity and health are not inputs.** The engine places one environment from a
command line and holds no capacity ledger, so it has nothing to report for either
and does not invent one. `engine/internal/scheduler` carries the fair share
round, the aging that stops a nightly job starving behind pull requests, the per
organization limit and the queue position for the day a control plane dispatches
batches; the engine calls the same function with the one run it has, so the
decision on a laptop is made by the code that will make it in a cluster rather
than by a second implementation that agrees until it does not.

The consequence worth stating plainly: **this does not fail over.** A target that
is unreachable is an error, not a reason to place somewhere else. Placement is a
pure function of the manifest, and it has to be, because `af up`, `af status`,
`af logs` and `af down` each decide independently. A placement that varied with a
cluster's health would have `af status` asking the wrong cluster and reporting
that your environment does not exist.

## Residency

A target's `region` tag is what fills the region an organization policy's
`allowed_regions` rule compares against. Before targets existed nothing in the
product knew where an environment ran, so that rule had no value to read. A
target that carries a region can be refused by a residency policy; one that does
not carry a region cannot be, and the policy says so rather than passing.

See [policy](/docs/enterprise/policy).

## The community edition

Two runtimes, both built. `runtime.provider` names `local` or `kubernetes`, and
any other name is refused with a message that lists what this build has rather
than quietly substituting one. The Kubernetes runtime builds a Deployment, a
Service and an Ingress per web service and has been selectable the whole time.

One target is community too. It decides nothing: it labels the single runtime
you already had so a residency policy has something to read, and charging for a
label would be charging for the community edition.

What the enterprise edition adds is not a third runtime. It is the choice between
several at once: the requirements, the tags and the refusal described above.

## Why there is no ECS runtime

`runtime.provider: ecs` is registered in the enterprise binary and it refuses,
every time, with a report rather than an error. The reason is worth reading
before assuming it is a gap somebody will close next release.

A runtime is allowed to exist here only if it can prove an environment has no
way out, and the Kubernetes runtime proves it the only way a proof works: it
creates the `NetworkPolicy` objects itself, then runs one pod under exactly the
rules a service runs under and has it try to escape before any application image
starts. If any attempt gets out the environment does not start and you get
**AF-RUN-043**. That is not caution. Several container network plugins accept a
`NetworkPolicy` object and enforce nothing, every status reads green, and the
only thing that can tell the two apart is a packet.

ECS on Fargate cannot be given the same treatment, for two reasons that are
properties of the platform rather than of this implementation.

**The image pull runs inside the boundary.** A kubelet pulls on the node, so a
pod can be denied every egress rule and still start. On Fargate platform version
1.4.0 the ECR login, the image pull and the log push all flow over the task's own
network interface, under the task's own security group, and AWS states that a
Fargate task must have a route to the registry to pull an image. So a security
group that denies egress does not produce a contained environment. It produces a
task that never starts, and the endpoints that let it start are themselves
reachable addresses.

**The enforcement cannot be observed without an account.** Whether a cluster
enforces a `NetworkPolicy` is answerable on a laptop in ninety seconds. Whether
AWS enforces a route table is a fact about an account, and no test in this
repository is permitted to need one.

So what ships is the enumeration and the predicate over it, and the refusal
carries both. Thirteen distinct egress paths out of a Fargate task, of which
**ten are closed by the configuration this runtime would generate, one is open,
and two are not decided by either the configuration or AWS's own
documentation**. Every closed verdict carries the grade of evidence behind it,
and the report separates the closures computed from a JSON document from any
recorded by an attempt made inside a running task, because those are different
claims.

The one that is open is the task metadata endpoint, which is on by default for
every Fargate task on platform version 1.4.0 or later with no documented way to
turn it off.

The two that are unproven are named rather than rounded up, because an unproven
verdict is not a weaker closed one. The instance metadata service at
`169.254.169.254` is the sharper of them: the Fargate launch type removes the
documented credential source, because EC2 instance profiles are not available to
containers in Fargate tasks, but AWS does not state that the address stops
answering, and those are different claims. The local Amazon Time Sync Service at
`169.254.169.123` is the other. Settling either needs one request from one
running task, which is a request from a task in somebody's AWS account, so
nothing here moves them on an absence of evidence.

Two paths that a reader of an earlier draft of this page would have found in the
open column are closed, and how they closed is the reusable part. The interface
endpoints the image pull needs and the S3 gateway endpoint the layers come from
cannot be disconnected without giving up the ability to start an environment at
all. But reaching ECR is not the same question as reaching **any** repository,
any log group and any bucket in the region, and only the second is an
exfiltration path. A VPC endpoint policy naming this environment's own
repository, log group and bucket closes the second while leaving the first, so
the weaker half of the question was the one being answered.

One finding is worth carrying away even if you never run on ECS. **On AWS the
security group is irrelevant to a DNS query.** The VPC user guide states that
traffic to and from the Amazon DNS server cannot be filtered with network ACLs
or security groups, and that resolver answers recursive queries for public names
from anywhere in the VPC. A DNS question is chosen by whoever asks it, so that
is a data channel out that no security group audit will ever show you. Closing
it takes a Route 53 Resolver DNS Firewall rule group whose last rule blocks every
domain and which does not fail open. A containment argument carried over from
Kubernetes gets this one wrong, because there the same attempt is closed by the
policy that closes everything else.

**A DNS Firewall rule group is read by priority from the lowest number up, and
that is where the second finding is.** A group holding `ALLOW` on every domain
at priority 5 and `BLOCK` on every domain at priority 1000 blocks nothing at all,
because `ALLOW` permits the request to go through and the lower priority is
consulted first. It reads as configured in a console screenshot. The check
refuses that shape, along with a terminal rule moved off the end by priority, two
rules sharing the last priority, which AWS refuses to create anyway, and a domain
with a star anywhere but the front, which a DNS Firewall domain list cannot hold.

The rule group's allow list is generated from the manifest's own egress
catalogue rather than from a list of AWS names kept beside it. A host the
manifest declares `allow` or `sandbox` is one the sidecar forwards to for real
and therefore has to resolve; a host declared `block`, `mock`, `capture` or
`synth` is answered locally and must not resolve, because a name the sidecar
answers resolving publicly is a route around the decision the manifest made
about it. A declared host that cannot be expressed as a DNS Firewall domain is
named in the refusal rather than dropped or widened.

**The honest summary is that Antifailure runs on EKS and not on raw ECS.** Use
`runtime.provider: kubernetes` against an EKS cluster, where the probe runs.

The report is generated for a real network rather than for a made up one, so
that the security group rules and route table entries it judges are the ones
your account would get. Six variables describe that network, and they are
variables rather than manifest fields because a VPC identifier is a property of
one company's account and does not belong in a file that gets forked:
`AF_ECS_REGION`, `AF_ECS_CLUSTER`, `AF_ECS_VPC_ID`, `AF_ECS_VPC_CIDR`,
`AF_ECS_SUBNET_IDS` and `AF_ECS_SUBNET_CIDRS`, the last two comma separated and
in matching order. Setting them changes the report and does not change the
answer, and the refusal you get without them says so before you go and build a
VPC to find out. A seventh, `AF_ECS_PROBE_IMAGE`, is optional and names the
image the containment probe container would run; with it unset the plan carries
no probe container and the instance metadata path says so.

Related: [scheduling](/docs/concepts/scheduling), [manifest reference](/docs/reference/manifest#placement), [licensing](/docs/enterprise/licensing).

---
title: Why there is no Azure Container Apps runtime
description: What a containment claim on Container Apps would have to say, and why Microsoft's own documentation refuses it.
sidebar:
  order: 22
---

On Azure, the runtime to use is `kubernetes` on AKS. Antifailure does not run
on raw Azure Container Apps, and
`runtime.provider: aca` in a manifest exits with the reason rather than with a
list of the runtimes that do exist.

This page is that reason. It is here rather than in a release note because
"Container Apps is not supported" reads like a roadmap item, and this is not
one. Two of the three things a containment claim on Container Apps would have
to say are contradicted by Microsoft's own documentation.

## The rule this is measured against

A runtime earns its place here by proving containment before it runs anything.
The Kubernetes runtime creates its own NetworkPolicy objects and then starts one
pod under them that tries to escape four ways, refusing the environment with
`AF-RUN-043` when any attempt succeeds. The order matters: a NetworkPolicy is a
request to whatever network plugin the cluster runs, several plugins accept the
object and enforce nothing, and only a packet can tell those apart.

A runtime that assumes containment instead of proving it is worth less than no
runtime at all, because from the outside the two look identical.

## An environment cannot be built with the door closed

Microsoft's firewall guidance for Container Apps lists four names under the
scenario "All scenarios":

- `mcr.microsoft.com` and `*.data.mcr.microsoft.com`, for Microsoft Artifact
  Registry
- `packages.aks.azure.com` and `acs-mirror.azureedge.net`, which the underlying
  Azure Kubernetes Service cluster uses to download and install its Kubernetes
  and container network interface binaries

The same guidance offers the private endpoint escape for your own registry and
your own key vault, and offers nothing like it for those four. So the subnet
must have a route to the public internet before an environment exists at all.

**This is worse on Container Apps than on AWS Fargate, and it is worth being
precise about why.** On Fargate the registry, the image layers and the log push
are all reachable through endpoints inside the VPC, so a task can start in a
subnet with no route out. On Container Apps two of the four required names are
public content delivery endpoints for the platform's own cluster binaries, and
no configuration reaches them. "A network security group that denies" is not a
tighter setting here. It is a configuration that builds nothing.

## Internal governs ingress, not egress

An internal environment has no public endpoint and its virtual IP is an internal
load balancer address. That is a real property and it is worth having. It is not
containment.

Microsoft's billing note for a virtual network integrated environment reads "One
standard static public IP for egress if you're using an internal or external
environment, plus one standard static public IP for ingress if you're using an
external environment". An internal environment is provisioned with a public
egress address. Reading `internal` as "no outbound path" is the specific mistake
this page exists to prevent.

## The resolver, which is different on every cloud

A name lookup is a data channel. The question in a DNS query is chosen by
whatever sends it, so a resolver that answers recursively is an upload with a
packet budget. Every containment argument has to say what happens to it, and the
answer is different on all three clouds:

- On **Kubernetes** a NetworkPolicy closes the resolver along with everything
  else, which is why an argument carried over from Kubernetes is wrong
  everywhere else.
- On **AWS** the resolver cannot be filtered at all. The VPC user guide states
  that you cannot filter traffic to or from the Amazon DNS server using network
  ACLs or security groups, so only a Route 53 Resolver DNS Firewall rule group
  closes it.
- On **Azure** a network security group **can** filter it, through the
  `AzurePlatformDNS` service tag. And then the Container Apps page says: "Don't
  explicitly deny the Azure DNS address 168.63.129.16 in the outgoing NSG rules.
  If you do, your Container Apps environment doesn't function."

Azure DNS is also "a virtual IP of the host node and as such it isn't subject to
user defined routes", so a firewall the default route points at never sees the
query.

Two clouds, the same verdict, opposite mechanisms. That is why the enumeration
behind this page reports three values and not two.

## What is shipped instead

`runtime.provider: aca` reaches an enumeration of twelve egress paths out of a
Container Apps replica, each with a verdict of `closed`, `open` or `unproven`,
and a refusal carrying the whole report. **Eight are closed by the configuration
the runtime would generate, three are open, and one is unproven.**

`unproven` is never counted as `closed`. The instance metadata address at
`169.254.169.254` is the example: the generated configuration writes the one
rule Azure documents for it, an outbound deny to the `AzurePlatformIMDS` service
tag, and the verdict is still `unproven`, because no Container Apps page states
that the address answers a replica and no page states that it does not. Those
are different claims, and settling it needs one request from one running
replica.

Every verdict is computed from configuration that has never been applied to an
Azure subscription. A `closed` verdict means the generated configuration removes
the path. It does not mean Azure was seen enforcing it.

## What to use

Use `runtime.provider: kubernetes` against AKS. It makes the same containment
argument there as on any other cluster: a probe tries to get out before any
application image starts, and a cluster that does not enforce the policy is
refused. What has not happened is a run on AKS. The Kubernetes runtime has been
run against k3s and nowhere else, it is recorded as `written` rather than
`proven`, and its own page says why, including the window after a pod starts
that the probe does not close. AKS is the recommendation because it is where
most organisations on Azure already run containers, not because it was measured.

See [The Kubernetes runtime](/docs/guides/kubernetes-runtime).

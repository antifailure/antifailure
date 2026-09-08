# added

A manifest naming the Cloud Run runtime was answered by a list of two names.

`runtime.provider: cloudrun` reached the engine's generic refusal, which says
this build has local and kubernetes and stops there. That answer is true and it
is useless: somebody writing that line is asking whether their environments can
run on Cloud Run, and a list of the runtimes that already exist does not tell
them why not.

The plan for this lane described the containment as VPC egress for all traffic
through a connector, a firewall that denies, and metadata reachable only to the
sidecar. Two of those three lines are wrong, and the third is wrong in the
direction that flatters the claim. Google's page on deploying multiple
containers states that "All containers within an instance share the same
network namespace", the container contract states that Cloud Run writes an
/etc/hosts entry mapping container names to 127.0.0.1 so containers reach each
other on localhost, and one paragraph of the container contract says the
opposite in passing, that containers run under network namespaces "isolating
them from each other". The mechanism settles it: communication over localhost is
what a shared network namespace is. So there is no arrangement in which the
sidecar reaches the metadata server and the application container does not.
Cloud Run has no way
to run a service with no identity at all, and the metadata server hands an OAuth
access token for that identity to anything inside the container, on an address
that Google states VPC firewall rules do not apply to. That path is closed on
Fargate, where a task definition may carry no task role, and it cannot be closed
here.

The runtime is registered and it refuses, carrying the enumeration as the
reason: ten egress paths out of a Cloud Run instance, six closed by the
generated configuration, three open and one that neither the configuration nor
Google's documentation decides. Seven of ten for an environment of exactly one
service, which is a shape Fargate cannot reach at all.

Cloud Run is the tighter of the two clouds in the place that decided the ECS
lane. Google states that container images "are not pulled from their container
repository when a new Cloud Run instance is started" and the platform captures
the log streams itself, so an environment here starts under a deny all egress
rule, where a Fargate task under the same rule never starts and holds three
paths open by necessity.

Every verdict is computed from a configuration that has never been applied to a
Google Cloud project, and the report says so in a constant that cannot be
paraphrased away.

# added

`runtime.provider: ecs` is now answered by a containment report instead of by a
list of the two runtimes that exist. It still refuses to start an environment,
and the refusal is the feature: it enumerates thirteen distinct ways out of an
ECS task on Fargate, names the ten the generated configuration closes, the one
that is open and the two that neither the configuration nor AWS's documentation
decides, and says which verdicts are computed from a configuration nobody has
applied to an AWS account.

An unproven verdict is not a weaker closed one and is never counted as one. The
instance metadata service and the local time sync service are the two, both on
link local addresses that no route table or security group reaches, and settling
either takes one request from one running task in somebody's own account.

Three things the enumeration found are worth knowing even if you never run on
ECS. A security group cannot deny egress on Fargate, because the image pull runs
over the task's own interface under that same security group, so denying egress
produces a task that never starts. On AWS a security group cannot filter a DNS
query at all: the Amazon provided resolver answers recursive lookups for public
names from anywhere in the VPC and the VPC user guide states plainly that neither
security groups nor network ACLs can filter traffic to it, so closing that path
takes a Route 53 Resolver DNS Firewall rule group. And reaching ECR is a
different question from reaching every repository, log group and bucket in the
region: the first cannot be closed without giving up the ability to start an
environment, the second can, by a VPC endpoint policy, and only the second is an
exfiltration path.

The honest summary is unchanged and is now written down: Antifailure runs on EKS
and not on raw ECS.

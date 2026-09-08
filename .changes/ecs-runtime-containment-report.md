# added

`runtime.provider: ecs` is now answered by a containment report instead of by a
list of the two runtimes that exist. It still refuses to start an environment,
and the refusal is the feature: it enumerates thirteen distinct ways out of an
ECS task on Fargate, names the eight the generated configuration closes and the
five it does not, and says which verdicts are computed from a configuration
nobody has applied to an AWS account.

Two things the enumeration found are worth knowing even if you never run on ECS.
A security group cannot deny egress on Fargate, because the image pull runs over
the task's own interface under that same security group, so denying egress
produces a task that never starts. And on AWS a security group cannot filter a
DNS query at all: the Amazon provided resolver answers recursive lookups for
public names from anywhere in the VPC and the VPC user guide states plainly that
neither security groups nor network ACLs can filter traffic to it, so closing
that path takes a Route 53 Resolver DNS Firewall rule that blocks every domain.

The honest summary is unchanged and is now written down: Antifailure runs on EKS
and not on raw ECS.

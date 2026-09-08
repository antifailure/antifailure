# security

A Route 53 Resolver DNS Firewall rule group that let every query through was
reported as closing the resolver.

AWS processes a rule group by order of priority starting from the lowest, and
defines ALLOW as permitting the request to go through and ALERT as permitting it
and sending metrics and logs. So a group holding ALLOW on every domain at
priority 5 and BLOCK on every domain at priority 1000 blocks nothing at all, and
the check called it closed because every assertion it made about the terminal
rule was true. It reads as configured in a console screenshot. The check now
refuses that shape, a terminal rule moved off the end by priority, two rules
sharing the last priority, which AWS refuses to create, and a domain with a star
anywhere but the front, which a DNS Firewall domain list cannot hold. Each has a
mutation aimed at it.

The failure mode of that association was a bool and the AWS field takes three
values. The third takes the answer from a setting no plan carries, so a plan
holding it has not decided whether a query the firewall cannot evaluate is
permitted, and a bool rounded that to whichever answer the zero value gave. It
is the AWS value now, and that case reports unproven rather than either answer.

The ECS runtime's egress report went from eight of thirteen paths closed to ten.
The two that moved were open because a Fargate task must reach the registry to
start, which answers the weaker half of the question: reaching ECR cannot be
closed and reaching any repository, any log group and any bucket in the region
can, by a VPC endpoint policy, and only the second is an exfiltration path. The
generated ECR policy would also have denied the login, because
ecr:GetAuthorizationToken takes no resource and was named against a repository.

The generated firewall would have broken the environment it protects. Its allow
list held four hardcoded AWS names, so a manifest declaring a host the sidecar
forwards to would have had that host fail to resolve. It reads the manifest's
egress catalogue now, and a declared host it cannot express as a rule is named
in the refusal rather than dropped or widened.

Instance metadata still cannot be closed on Fargate, and it can now be answered.
A verdict carries the grade of evidence behind it, so a closure computed from a
JSON document is counted apart from one recorded by an attempt made inside a
task in the customer's own account. No recorded attempt leaves the path
unproven, and nothing moves it on an absence.

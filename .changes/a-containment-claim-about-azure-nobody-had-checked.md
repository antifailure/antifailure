# added

The plan's row for Azure Container Apps described the containment as "an
internal environment, an NSG that denies, no public ingress except the declared
web service". Two of those three are wrong against Microsoft's own
documentation, and the third is not the protection it sounds like.

An internal environment governs INGRESS. Microsoft bills a virtual network
integrated environment for "one standard static public IP for egress if you're
using an internal or external environment", so an internal environment is
provisioned with a public egress address and reaches the internet unless a route
sends it somewhere else first.

A network security group that denies cannot exist here either. The Container
Apps firewall guidance requires `mcr.microsoft.com`,
`*.data.mcr.microsoft.com`, `packages.aks.azure.com` and
`acs-mirror.azureedge.net` under the scenario "All scenarios", and offers the
private endpoint escape only for your own registry and key vault, never for
those four. That is worse than the AWS answer rather than the same one: on
Fargate the registry, layer and log dependencies are all reachable through
endpoints inside the VPC, so a task can start in a subnet with no route out,
and a Container Apps replica cannot. Microsoft states the rest plainly:
"Don't explicitly deny the Azure DNS address 168.63.129.16 in the outgoing NSG
rules. If you do, your Container Apps environment doesn't function."

So `runtime.provider: aca` now reaches a package that enumerates twelve egress
paths out of a replica and refuses. Eight are closed by the generated
configuration, three are open and named, and one is unproven and is never
counted as closed. A manifest naming the runtime exits with the whole report
rather than with a list of the two runtimes that do exist.

Every verdict is computed from a configuration that has never been applied to an
Azure subscription, and the report says so in a constant that cannot be
paraphrased away.

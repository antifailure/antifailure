// Package aca is the Azure Container Apps runtime, and like its ECS peer it is
// a containment proof rather than a placer of containers.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The Kubernetes runtime earns its existence by creating NetworkPolicy objects
// and then running one pod under them that tries to escape four ways before any
// application image starts, refusing with AF-RUN-043 when any attempt succeeds.
// The escape probe is the price of admission for a runtime here, and Container
// Apps cannot pay it for two reasons that are properties of Azure rather than
// of this repository.
//
// The first is that a Container Apps environment cannot be built without a
// route to the public internet. Microsoft's own firewall guidance requires
// mcr.microsoft.com, *.data.mcr.microsoft.com, packages.aks.azure.com and
// acs-mirror.azureedge.net to be reachable in every scenario, and offers the
// private endpoint escape only for the customer's own registry and key vault,
// never for those four. That is strictly worse than ECS on the same question:
// on AWS the ECR, logs and S3 dependencies are all satisfiable
// by endpoints inside the VPC, so a Fargate task can start with no route out at
// all, while a Container Apps replica cannot.
//
// The second is that the enforcement is a fact about an Azure subscription. A
// network security group, a route table and a firewall policy are objects Azure
// accepts, and whether they contain anything can only be settled by a packet
// from a running replica. The plan this repository works from forbids a test
// that needs a cloud account, so the predicate here is written over the
// configuration an environment would be given rather than over an account.
//
// Read egress.go first. The enumeration there is the deliverable and the
// runtime's refusal in runtime.go is a consequence of it.
//
// Every Azure behaviour asserted in this package was read from Microsoft Learn
// on 2026-09-07 and the page is named at the point it is used. None of it is
// from memory, and none of it is carried over from the AWS lane: the two clouds
// answer the resolver question differently and for different reasons, and a
// containment argument that assumed otherwise would be wrong on the sharpest
// path in the file.
package aca

// Plan is the whole Azure configuration one environment would be given.
//
// One flat value rather than a client talking to Azure, for the reason the ECS
// plan gives: a predicate over a value can be evaluated by a test, printed in a
// refusal, checked into a golden file and mutated one field at a time, while a
// predicate over a subscription can only be evaluated in a subscription
// somebody pays for. A repository whose containment proof lives there has no
// containment proof on the day the credentials expire.
//
// Every field is a field of a real Azure resource or request body. Nothing is a
// summary or a boolean standing in for a shape.
type Plan struct {
	// Region is the Azure region, which appears in the regional service tags
	// the predicate has to recognise.
	Region string
	// ResourceGroup holds the environment's own resources. The platform
	// creates a second, managed group beside it that this plan does not
	// describe and must not modify.
	ResourceGroup string
	// EnvironmentType is WorkloadProfiles or ConsumptionOnly, and it decides
	// more about containment than any other single field.
	//
	// A Consumption only environment supports no user defined routes, no
	// egress through a NAT gateway and no other custom egress, and Microsoft's
	// own network security group table for it requires an outbound allow to
	// AzureCloud on 443, which is every Azure datacentre public address, and
	// an outbound allow to * on UDP 123. An environment that has to permit
	// those two rules is not contained in any sense worth the word.
	EnvironmentType string
	// Environment is the managed environment resource.
	Environment ManagedEnvironment
	// App is the container app that runs one service.
	App ContainerApp
	// Network is everything the packets can and cannot do.
	Network NetworkPlan
}

// The two environment types, spelled as this package spells them.
const (
	// WorkloadProfiles is the default environment type and the only one that
	// supports user defined routes.
	WorkloadProfiles = "WorkloadProfiles"
	// ConsumptionOnly is the legacy environment type.
	ConsumptionOnly = "ConsumptionOnly"
)

// ManagedEnvironment is the Microsoft.App/managedEnvironments resource, cut
// down to the fields that decide whether anything can get out.
type ManagedEnvironment struct {
	// Name is the environment name.
	Name string
	// VnetConfiguration places the environment in a virtual network the
	// customer owns, which is the only shape in which a network security
	// group applies to it at all.
	VnetConfiguration VnetConfiguration
	// AppLogsConfiguration is where the platform ships container logs. Its
	// destination is the field that decides whether the environment needs a
	// route to Azure Monitor, and "none" is a supported value.
	AppLogsConfiguration AppLogsConfiguration
	// PublicNetworkAccess is Enabled or Disabled. An internal environment can
	// only be Disabled, and Microsoft documents that the setting cannot be
	// changed afterwards.
	PublicNetworkAccess string
	// WorkloadProfileNames are the profiles declared on the environment. It is
	// here so that a plan naming a profile in a Consumption only environment
	// is visibly inconsistent rather than quietly accepted.
	WorkloadProfileNames []string
}

// VnetConfiguration is the environment's virtual network integration.
type VnetConfiguration struct {
	// Internal is true when the environment has no public endpoint and its
	// virtual IP is an internal load balancer address.
	//
	// It governs INGRESS and nothing else, which is the row line this lane
	// found wrong. Microsoft's own billing note for a virtual network
	// integrated environment reads "One standard static public IP for egress
	// if you're using an internal or external environment", so an internal
	// environment is provisioned with a public egress address and reaches the
	// internet unless a route sends it somewhere else first.
	Internal bool
	// InfrastructureSubnetID is the dedicated subnet. Container Apps requires
	// a subnet used by nothing else, so it is the unit of isolation between
	// two environments and there is no smaller one.
	InfrastructureSubnetID string
}

// AppLogsConfiguration is the environment's log destination.
type AppLogsConfiguration struct {
	// Destination is "none", "log-analytics" or "azure-monitor". Empty is
	// treated as none. Anything else obliges the plan to permit outbound
	// traffic to the AzureMonitor service tag, which is a route out of the
	// environment carrying whatever the application wrote to its log.
	Destination string
}

// ContainerApp is the Microsoft.App/containerApps resource.
type ContainerApp struct {
	// Name is the app name.
	Name string
	// Identity is the managed identity block, and it must be None.
	//
	// A container app with an identity exposes IDENTITY_ENDPOINT and
	// IDENTITY_HEADER to the code in the container and Microsoft documents the
	// endpoint as available "from within the app with a standard HTTP GET
	// request". This is Azure's answer to 169.254.170.2 on Fargate, with one
	// difference that matters: on Fargate the endpoint is always there and the
	// only defence is an empty task role, while here the whole identity can be
	// absent or scoped away.
	Identity Identity
	// IdentitySettings scope an identity to a lifecycle phase. A setting of
	// None makes an identity unavailable to every container, which is the
	// documented way to hold a registry pull identity that the application
	// code can never use.
	IdentitySettings []IdentitySetting
	// Registries are the registries the app pulls from.
	Registries []Registry
	// Ingress is the inbound configuration.
	Ingress Ingress
	// Containers are the images and their names.
	Containers []Container
}

// Identity is the app's managed identity block.
type Identity struct {
	// Type is None, SystemAssigned, UserAssigned, or the comma joined pair.
	Type string
	// UserAssignedIdentityIDs are the resource ids of user assigned
	// identities.
	UserAssignedIdentityIDs []string
}

// IdentitySetting scopes one identity to a phase of the app's life.
type IdentitySetting struct {
	// Identity is "system" or a user assigned identity's resource id.
	Identity string
	// Lifecycle is Init, Main, All or None.
	Lifecycle string
}

// Registry is one registry the app pulls from.
type Registry struct {
	// Server is the login server, such as example.azurecr.io.
	Server string
	// Identity is the identity used for the pull, empty when the pull uses a
	// credential instead.
	Identity string
}

// Ingress is the app's inbound configuration.
type Ingress struct {
	// External admits traffic from outside the environment. In an internal
	// environment "outside" still means the virtual network rather than the
	// internet, because the environment has no public endpoint.
	External bool
	// TargetPort is the container port.
	TargetPort int
	// Transport is auto, http, http2 or tcp.
	Transport string
}

// Container is one container in the app.
type Container struct {
	// Name is the container name.
	Name string
	// Image is the image reference.
	Image string
}

// NetworkPlan is the virtual network, the subnet, the security group, the route
// table, the firewall and the private endpoints, which together are whatever
// containment there is.
type NetworkPlan struct {
	// VirtualNetwork is the network the environment's subnet lives in.
	VirtualNetwork VirtualNetwork
	// Subnet is the dedicated subnet the environment is placed in.
	Subnet Subnet
	// NSG is the network security group on that subnet. Container Apps has no
	// per app or per replica security group, so the subnet is the finest
	// granularity available and one subnet per environment is the only way to
	// give two environments different rules.
	NSG NetworkSecurityGroup
	// RouteTable is the table associated with the subnet.
	RouteTable RouteTable
	// Firewall is the Azure Firewall the default route points at. It is a
	// pointer because a plan without one is a plan whose only egress control
	// is the security group, and the predicate has to be able to say that.
	Firewall *Firewall
	// PrivateEndpoints are the private endpoints in the virtual network that
	// let the environment reach an Azure service without a public address.
	PrivateEndpoints []PrivateEndpoint
}

// VirtualNetwork is the network's own attributes.
type VirtualNetwork struct {
	// Name is the network name.
	Name string
	// AddressPrefixes are its ranges.
	AddressPrefixes []string
	// Peerings are virtual network peerings. A peering is a route to another
	// network that never touches the internet, so an egress check written as
	// "no route to the internet" passes a network peered with production.
	Peerings []Peering
}

// Peering is one virtual network peering.
type Peering struct {
	// Name is the peering name.
	Name string
	// RemoteVirtualNetwork is the network on the other side.
	RemoteVirtualNetwork string
	// AllowForwardedTraffic widens it further.
	AllowForwardedTraffic bool
}

// Subnet is the environment's dedicated subnet.
type Subnet struct {
	// Name is the subnet name.
	Name string
	// AddressPrefix is its range. Container Apps requires at least a /27 for a
	// workload profile environment and at least a /23 for a Consumption only
	// one, and the size cannot be changed after the environment exists.
	AddressPrefix string
	// Delegation is the service the subnet is delegated to. A workload profile
	// environment requires Microsoft.App/environments and a Consumption only
	// environment requires no delegation at all, so the field is one of the
	// few places the two types disagree in the resource rather than in prose.
	Delegation string
	// ServiceEndpoints are virtual network service endpoints on the subnet.
	//
	// Each one is a route Azure adds to the subnet for an entire service. It
	// is not a route to one storage account, it is a route to the Storage
	// service, and a user defined route to a firewall does not obviously win
	// against it. An empty list is the only value this plan accepts.
	ServiceEndpoints []string
	// NATGatewayID attaches a NAT gateway, which gives the subnet a static
	// public address for outbound traffic and is therefore a route out.
	NATGatewayID string
}

// NetworkSecurityGroup is the group on the environment's subnet.
type NetworkSecurityGroup struct {
	// Name is the group name.
	Name string
	// Outbound and Inbound are the security rules, in no particular order.
	// Priority decides evaluation, not position, which is why every rule
	// carries one.
	Outbound []SecurityRule
	Inbound  []SecurityRule
}

// SecurityRule is one network security group rule.
//
// The destination is a string because Azure accepts three different kinds of
// value there and the difference is the whole subject: a CIDR, a single
// address, or a service tag naming a set of prefixes Microsoft maintains. A
// predicate that parsed only CIDRs would silently ignore every service tag rule
// in the plan, which is where all the interesting reach lives.
type SecurityRule struct {
	// Name is the rule name and it appears in the report.
	Name string
	// Priority orders the rules. Lower is evaluated first, and the first match
	// decides, so a low priority Allow beats a high priority Deny.
	Priority int
	// Access is Allow or Deny.
	Access string
	// Protocol is Tcp, Udp, Icmp or "*".
	Protocol string
	// SourceAddressPrefix and DestinationAddressPrefix are CIDRs, single
	// addresses, or service tags.
	SourceAddressPrefix      string
	DestinationAddressPrefix string
	// DestinationPortRange is a port, a range such as "30000-32767", or "*".
	DestinationPortRange string
}

// Access values.
const (
	Allow = "Allow"
	Deny  = "Deny"
)

// RouteTable is the table associated with the environment's subnet.
type RouteTable struct {
	// Name is the table name.
	Name string
	// DisableBGPRoutePropagation stops routes learned from a virtual network
	// gateway being added to this table. Microsoft's own walkthrough for
	// Container Apps sets it, and leaving it unset means the corporate network
	// can add a route to this environment without touching this plan.
	DisableBGPRoutePropagation bool
	// Routes are the entries.
	Routes []Route
}

// Route is one route table entry.
type Route struct {
	// Name is the route name.
	Name string
	// AddressPrefix is the destination range.
	AddressPrefix string
	// NextHopType is VirtualAppliance, Internet, VirtualNetworkGateway,
	// VnetLocal or None. Azure spells these exactly, so the predicate compares
	// them rather than guessing at a shape.
	NextHopType string
	// NextHopIPAddress is set only for VirtualAppliance.
	NextHopIPAddress string
}

// Next hop types, spelled as Azure spells them.
const (
	NextHopVirtualAppliance      = "VirtualAppliance"
	NextHopInternet              = "Internet"
	NextHopVirtualNetworkGateway = "VirtualNetworkGateway"
	NextHopVnetLocal             = "VnetLocal"
	NextHopNone                  = "None"
)

// Firewall is the Azure Firewall the default route points at.
type Firewall struct {
	// Name is the firewall name.
	Name string
	// PrivateIP is the address a route's next hop names.
	PrivateIP string
	// ApplicationRules filter by name. For HTTPS without TLS inspection the
	// match is on the name the client offered, which is worth saying out loud
	// in the report rather than implying that a name rule is an address rule.
	ApplicationRules []ApplicationRule
	// NetworkRules filter by address, port and service tag.
	NetworkRules []NetworkRule
}

// ApplicationRule is one Azure Firewall application rule.
type ApplicationRule struct {
	// Name is the rule name.
	Name string
	// Protocols are entries such as "https:443".
	Protocols []string
	// TargetFQDNs are the names allowed. "*" is every name.
	TargetFQDNs []string
	// Action is Allow or Deny.
	Action string
}

// NetworkRule is one Azure Firewall network rule.
type NetworkRule struct {
	// Name is the rule name.
	Name string
	// DestinationAddresses are CIDRs, addresses or service tags.
	DestinationAddresses []string
	// DestinationPorts are ports or ranges.
	DestinationPorts []string
	// Action is Allow or Deny.
	Action string
}

// PrivateEndpoint is one private endpoint in the virtual network.
//
// It is the mechanism that makes the registry path closable on Azure, and the
// same page that offers it offers nothing like it for the platform's own image
// sources. Microsoft's Container Apps firewall page states that a registry
// configured with private endpoints needs no security group rule at all, and
// the only other private endpoint it names is for a key vault. Microsoft
// Artifact Registry and the two AKS binary mirrors appear on that page as names
// to allow through a firewall and nowhere as a resource an endpoint can front,
// which is a statement about the guidance rather than a proof about Azure, and
// it is the honest version of the claim.
type PrivateEndpoint struct {
	// Name is the endpoint name.
	Name string
	// Service is the resource type it fronts, such as
	// Microsoft.ContainerRegistry/registries.
	Service string
	// Target is the specific resource, such as a registry's login server.
	Target string
	// SubnetName is the subnet the endpoint's network interface lives in.
	SubnetName string
	// PrivateDNSZone is the zone that has to resolve the service's public name
	// to the endpoint's private address. Without it the name still resolves to
	// the public address and the endpoint is decorative.
	PrivateDNSZone string
}

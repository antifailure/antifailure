package aca

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Name is the value runtime.provider takes in a manifest to reach this package.
const Name = "aca"

// Provider is the registered runtime provider for Azure Container Apps.
//
// It never returns a runtime, and that is the deliverable rather than a stage
// on the way to one. Read Open.
type Provider struct {
	// Getenv reads the network the environment would be placed in. It is a
	// field rather than a call to os.Getenv so that a test can drive the
	// generator without setting process wide state, which is the mistake that
	// makes one test's environment leak into another's.
	Getenv func(string) string
}

// The environment variables that describe the subscription's network.
//
// Environment variables rather than manifest fields, deliberately. A virtual
// network name, a subnet range and a firewall address are properties of an
// organisation's Azure subscription, they differ per installation, and putting
// them in antifailure.yaml would mean a repository's manifest carried one
// company's network layout into every fork of it.
const (
	EnvRegion        = "AF_ACA_REGION"
	EnvResourceGroup = "AF_ACA_RESOURCE_GROUP"
	EnvVNet          = "AF_ACA_VNET_NAME"
	EnvVNetCIDR      = "AF_ACA_VNET_CIDR"
	EnvSubnetCIDR    = "AF_ACA_SUBNET_CIDR"
	EnvRegistry      = "AF_ACA_REGISTRY"
	EnvFirewallIP    = "AF_ACA_FIREWALL_IP"
)

// NewProvider builds the provider with the process environment.
func NewProvider() *Provider { return &Provider{Getenv: os.Getenv} }

// Name is the manifest value.
func (p *Provider) Name() string { return Name }

// Open refuses, every time, and returns the containment report as the reason.
//
// This is the whole lane and it needs the argument written where somebody
// deleting it will read it.
//
// The plan this repository works from carries a standing risk: the containment
// claim must not soften to make a cloud runtime possible, and if a runtime
// cannot be proved contained then the honest answer is that Antifailure runs on
// AKS and not on raw Container Apps. The Kubernetes runtime is allowed to exist
// because it creates the NetworkPolicy objects itself and then runs one pod
// under them that tries to escape four ways before any application image
// starts. Two properties make that possible and Container Apps has neither.
//
// The first is that a Container Apps environment cannot exist without a route
// to the public internet. Microsoft's firewall guidance requires
// mcr.microsoft.com, *.data.mcr.microsoft.com, packages.aks.azure.com and
// acs-mirror.azureedge.net in every scenario, and no private endpoint exists
// for any of them. This is worse than the ECS answer rather than the same one:
// on Fargate the registry, layer and log dependencies are all reachable through
// endpoints inside the VPC, so a task can start in a subnet with no route out,
// and a Container Apps replica cannot. An internal environment does not change
// this, because internal governs ingress: Microsoft bills a virtual network
// integrated environment for "one standard static public IP for egress if
// you're using an internal or external environment".
//
// The second is that the enforcement can be observed cheaply on Kubernetes and
// not here. A kind cluster on a laptop will say in ninety seconds whether its
// CNI enforces a NetworkPolicy. Whether Azure enforces a security group is a
// fact about a subscription, and no test in this repository is allowed to need
// one.
//
// So this package publishes the enumeration, generates the configuration,
// proves the predicate over that configuration in continuous integration, and
// refuses. A runtime that came up and could not prove it had no egress would be
// worse than no runtime at all, because from the outside it looks exactly like
// one that can.
func (p *Provider) Open(_ context.Context, cfg extension.RuntimeConfig) (provider.Runtime, error) {
	in, missing := p.inputs()
	if len(missing) > 0 {
		return nil, fmt.Errorf("the aca runtime is not available, and this installation has not "+
			"described a network for it either: %s are unset. Even fully described it would "+
			"refuse, for the reason af prints when they are set. Use runtime.provider "+
			"kubernetes against AKS, which is proved contained by a probe that runs before any "+
			"application image", strings.Join(missing, ", "))
	}
	report := Evaluate(Generate(in, environmentID(cfg)))

	var b strings.Builder
	b.WriteString("the aca runtime refuses to start an environment, on purpose.\n\n")
	b.WriteString(report.String())
	b.WriteString("\nThe Kubernetes runtime is allowed to exist because it creates its own " +
		"NetworkPolicy objects and then runs a pod under them that tries to escape before any " +
		"application image starts. A Container Apps environment cannot be created without " +
		"reaching mcr.microsoft.com, *.data.mcr.microsoft.com, packages.aks.azure.com and " +
		"acs-mirror.azureedge.net, none of which has a private endpoint, and Microsoft " +
		"documents that denying Azure DNS at 168.63.129.16 stops the environment from " +
		"functioning. Whether Azure enforces the rest is a fact about a subscription that no " +
		"test in this repository is allowed to need. Use runtime.provider kubernetes against " +
		"AKS.\n")
	return nil, fmt.Errorf("%s", b.String())
}

// inputs reads the network description and reports what is missing.
func (p *Provider) inputs() (Inputs, []string) {
	get := p.Getenv
	if get == nil {
		get = os.Getenv
	}
	in := Inputs{
		Region:        strings.TrimSpace(get(EnvRegion)),
		ResourceGroup: strings.TrimSpace(get(EnvResourceGroup)),
		VNetName:      strings.TrimSpace(get(EnvVNet)),
		VNetCIDR:      strings.TrimSpace(get(EnvVNetCIDR)),
		SubnetCIDR:    strings.TrimSpace(get(EnvSubnetCIDR)),
		Registry:      strings.TrimSpace(get(EnvRegistry)),
		FirewallIP:    strings.TrimSpace(get(EnvFirewallIP)),
	}
	var missing []string
	for _, pair := range []struct {
		name  string
		empty bool
	}{
		{EnvRegion, in.Region == ""},
		{EnvResourceGroup, in.ResourceGroup == ""},
		{EnvVNet, in.VNetName == ""},
		{EnvVNetCIDR, in.VNetCIDR == ""},
		{EnvSubnetCIDR, in.SubnetCIDR == ""},
		{EnvRegistry, in.Registry == ""},
		{EnvFirewallIP, in.FirewallIP == ""},
	} {
		if pair.empty {
			missing = append(missing, pair.name)
		}
	}
	return in, missing
}

// environmentID is a stable name for the environment the plan is generated for.
//
// The runtime config carries no environment id, because a runtime is opened
// once and creates many environments. The generated plan is therefore for a
// named example rather than for a specific environment, and calling it that in
// the identifier is better than picking one and implying the plan is about it.
func environmentID(cfg extension.RuntimeConfig) string {
	if ns := strings.TrimSpace(cfg.Runtime.NamespacePrefix); ns != "" {
		return ns + "example"
	}
	return "af-example"
}

// Inputs are the subscription facts a plan is generated from.
type Inputs struct {
	Region        string
	ResourceGroup string
	VNetName      string
	VNetCIDR      string
	SubnetCIDR    string
	Registry      string
	FirewallIP    string
}

// Rule priorities, named so that a reader can see the order without counting.
//
// Azure evaluates lowest first and the first match wins, so the deny rules have
// to sit above every allow numerically and the catch all has to sit last of
// all. Getting this backwards is the classic Azure security group defect: a
// deny at 4096 under an allow at 100 never runs.
const (
	priorityAllowArtifactRegistry = 100
	priorityAllowFrontDoor        = 110
	priorityAllowOwnSubnet        = 120
	priorityAllowAzureDNS         = 130
	priorityDenyInstanceMetadata  = 3000
	priorityDenyInternet          = 4000

	priorityAllowLoadBalancerProbe = 100
	priorityAllowOwnSubnetInbound  = 110
	priorityDenyInternetInbound    = 4000
)

// Generate builds the configuration one environment would be given.
//
// This is the function the predicate is a predicate about. It is deterministic,
// takes no clock and no randomness, so the plan for a given subscription and
// environment is the same every time and a golden over it is a real regression
// test rather than a snapshot of one run.
//
// Everything it emits is the tightest shape the paths in egress.go allow. Where
// a path cannot be closed, this function does not pretend: it still permits the
// artifact registry and Azure DNS, because a plan that omitted them would score
// better and build nothing.
func Generate(in Inputs, envID string) Plan {
	subnetName := envID
	nsgName := "nsg-" + envID

	return Plan{
		Region:        in.Region,
		ResourceGroup: in.ResourceGroup,
		// Workload profiles rather than the legacy type, and it is the single
		// most consequential field in the plan. A Consumption only environment
		// supports no user defined route, so there is no firewall to send
		// 0.0.0.0/0 to, and Microsoft's own table for it requires an outbound
		// allow to AzureCloud on 443 and to every address on UDP 123.
		EnvironmentType: WorkloadProfiles,
		Environment: ManagedEnvironment{
			Name: envID,
			VnetConfiguration: VnetConfiguration{
				// Internal, which closes public ingress and nothing else. The
				// environment still has a public address for egress and for
				// the platform's own management traffic.
				Internal:               true,
				InfrastructureSubnetID: subnetName,
			},
			// None, so the platform ships no container logs out of the
			// environment. af reads them from the environment instead, and the
			// cost of that choice is printed in the report rather than hidden.
			AppLogsConfiguration: AppLogsConfiguration{Destination: "none"},
			PublicNetworkAccess:  "Disabled",
			WorkloadProfileNames: []string{"Consumption"},
		},
		App: ContainerApp{
			Name: envID + "-web",
			// None, and it is the single most important field in the file.
			// IDENTITY_ENDPOINT is only set when an identity exists, so the
			// cheapest way to make it vend nothing is for there to be nothing
			// to vend. A twin of production has no business holding
			// production's Entra ID permissions in the first place.
			Identity: Identity{Type: "None"},
			Ingress: Ingress{
				External:   false,
				TargetPort: 8080,
				Transport:  "auto",
			},
			Registries: []Registry{{Server: in.Registry}},
		},
		Network: NetworkPlan{
			VirtualNetwork: VirtualNetwork{
				Name:            in.VNetName,
				AddressPrefixes: []string{in.VNetCIDR},
			},
			Subnet: Subnet{
				Name:          subnetName,
				AddressPrefix: in.SubnetCIDR,
				// Required for a workload profile environment, and forbidden
				// for the legacy one. It is emitted rather than left empty so
				// that a plan whose environment type was changed underneath it
				// is visibly inconsistent.
				Delegation: "Microsoft.App/environments",
				// Empty on purpose. A service endpoint is a route to a whole
				// Azure service added to the subnet rather than to the route
				// table, and the private endpoint below does the same job for
				// one resource.
				ServiceEndpoints: nil,
				NATGatewayID:     "",
			},
			NSG: NetworkSecurityGroup{
				Name: nsgName,
				Outbound: []SecurityRule{
					{
						Name:                     "allow-artifact-registry",
						Priority:                 priorityAllowArtifactRegistry,
						Access:                   Allow,
						Protocol:                 "Tcp",
						SourceAddressPrefix:      in.SubnetCIDR,
						DestinationAddressPrefix: "MicrosoftContainerRegistry",
						DestinationPortRange:     "443",
					},
					{
						Name:                     "allow-artifact-registry-front-door",
						Priority:                 priorityAllowFrontDoor,
						Access:                   Allow,
						Protocol:                 "Tcp",
						SourceAddressPrefix:      in.SubnetCIDR,
						DestinationAddressPrefix: "AzureFrontDoor.FirstParty",
						DestinationPortRange:     "443",
					},
					{
						Name:                     "allow-within-this-environment",
						Priority:                 priorityAllowOwnSubnet,
						Access:                   Allow,
						Protocol:                 "*",
						SourceAddressPrefix:      in.SubnetCIDR,
						DestinationAddressPrefix: in.SubnetCIDR,
						DestinationPortRange:     "*",
					},
					{
						// Written out rather than left to the platform, so
						// that the report can say the plan permits it on
						// purpose. Denying it is documented to stop the
						// environment working, and the resolver is not
						// filtered by the firewall either.
						Name:                     "allow-azure-dns",
						Priority:                 priorityAllowAzureDNS,
						Access:                   Allow,
						Protocol:                 "*",
						SourceAddressPrefix:      in.SubnetCIDR,
						DestinationAddressPrefix: AzureDNSAddress,
						DestinationPortRange:     "53",
					},
					{
						// The one mechanism Azure documents for the metadata
						// address. It is written even though the verdict stays
						// unproven, because writing the documented control and
						// still refusing to call the path closed is the whole
						// difference between a check and a claim.
						Name:                     "deny-instance-metadata",
						Priority:                 priorityDenyInstanceMetadata,
						Access:                   Deny,
						Protocol:                 "*",
						SourceAddressPrefix:      in.SubnetCIDR,
						DestinationAddressPrefix: "AzurePlatformIMDS",
						DestinationPortRange:     "*",
					},
					{
						Name:                     "deny-internet",
						Priority:                 priorityDenyInternet,
						Access:                   Deny,
						Protocol:                 "*",
						SourceAddressPrefix:      "*",
						DestinationAddressPrefix: "Internet",
						DestinationPortRange:     "*",
					},
				},
				Inbound: []SecurityRule{
					{
						Name:                     "allow-load-balancer-probe",
						Priority:                 priorityAllowLoadBalancerProbe,
						Access:                   Allow,
						Protocol:                 "Tcp",
						SourceAddressPrefix:      "AzureLoadBalancer",
						DestinationAddressPrefix: in.SubnetCIDR,
						DestinationPortRange:     "30000-32767",
					},
					{
						Name:                     "allow-within-this-environment",
						Priority:                 priorityAllowOwnSubnetInbound,
						Access:                   Allow,
						Protocol:                 "*",
						SourceAddressPrefix:      in.SubnetCIDR,
						DestinationAddressPrefix: in.SubnetCIDR,
						DestinationPortRange:     "*",
					},
					{
						Name:                     "deny-internet",
						Priority:                 priorityDenyInternetInbound,
						Access:                   Deny,
						Protocol:                 "*",
						SourceAddressPrefix:      "Internet",
						DestinationAddressPrefix: "*",
						DestinationPortRange:     "*",
					},
				},
			},
			RouteTable: RouteTable{
				Name: "rt-" + envID,
				// Set, following Microsoft's own Container Apps walkthrough.
				// Left unset, the corporate network can add a route into this
				// subnet without anything in this plan changing.
				DisableBGPRoutePropagation: true,
				Routes: []Route{
					{
						Name:             "everything-to-the-firewall",
						AddressPrefix:    "0.0.0.0/0",
						NextHopType:      NextHopVirtualAppliance,
						NextHopIPAddress: in.FirewallIP,
					},
				},
			},
			Firewall: &Firewall{
				Name:      "fw-" + envID,
				PrivateIP: in.FirewallIP,
				ApplicationRules: []ApplicationRule{
					{
						Name:        "the-four-names-an-environment-cannot-be-built-without",
						Protocols:   []string{"https:443"},
						TargetFQDNs: platformFQDNs(),
						Action:      Allow,
					},
				},
			},
			PrivateEndpoints: []PrivateEndpoint{
				{
					Name:           "pe-" + envID + "-registry",
					Service:        "Microsoft.ContainerRegistry/registries",
					Target:         in.Registry,
					SubnetName:     subnetName,
					PrivateDNSZone: "privatelink.azurecr.io",
				},
			},
		},
	}
}

// platformFQDNs is what the firewall lets through, which is only what
// Microsoft's own guidance marks as required in every scenario.
//
// Two of the four are the artifact registry and two are the mirrors the
// underlying cluster downloads its Kubernetes and network interface binaries
// from. That second pair is the part people are surprised by, and it is the
// reason a Container Apps environment cannot be built behind a closed door.
func platformFQDNs() []string {
	out := []string{
		"mcr.microsoft.com",
		"*.data.mcr.microsoft.com",
		"packages.aks.azure.com",
		"acs-mirror.azureedge.net",
	}
	sort.Strings(out)
	return out
}

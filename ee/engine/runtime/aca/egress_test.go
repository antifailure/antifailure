package aca_test

// The containment proof for the Azure Container Apps runtime.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/runtime/aca"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// reference is the subscription description every plan in these tests is built
// from. One value, so that a test which changes the plan is visibly changing it
// rather than quietly using a different subscription.
func reference() aca.Inputs {
	return aca.Inputs{
		Region:        "eastus",
		ResourceGroup: "antifailure",
		VNetName:      "vnet-antifailure",
		VNetCIDR:      "10.30.0.0/16",
		SubnetCIDR:    "10.30.1.0/27",
		Registry:      "antifailure.azurecr.io",
		FirewallIP:    "10.30.255.4",
	}
}

func referencePlan() aca.Plan { return aca.Generate(reference(), "af-example") }

// expected is the verdict every path gets on the generated plan.
//
// Written out in full rather than computed, because the point of the table is
// to be a thing a person can disagree with. A test asserting "the report
// matches Evaluate" would pass for any predicate at all.
var expected = map[string]aca.Verdict{
	"an-arbitrary-public-address":                          aca.Closed,
	"the-platform-images-with-no-private-endpoint":         aca.Open,
	"azure-dns-at-168-63-129-16":                           aca.Open,
	"the-instance-metadata-address":                        aca.Unproven,
	"the-managed-identity-token-endpoint":                  aca.Closed,
	"your-registry-and-its-layer-blobs":                    aca.Closed,
	"the-platform-log-sink":                                aca.Closed,
	"a-consumption-only-environments-control-plane-tunnel": aca.Closed,
	"a-neighbouring-environments-subnet":                   aca.Closed,
	"a-peering-or-a-gateway-route":                         aca.Closed,
	"a-virtual-network-service-endpoint":                   aca.Closed,
	"the-container-console-and-the-log-stream":             aca.Open,
}

// TestTheGeneratedPlanGetsTheVerdictItShould is the baseline every mutation
// below is measured against.
func TestTheGeneratedPlanGetsTheVerdictItShould(t *testing.T) {
	report := aca.Evaluate(referencePlan())
	require.Equal(t, len(expected), report.Total,
		"the enumeration and this table have drifted apart")
	for _, v := range report.Verdicts {
		want, ok := expected[v.Path.ID]
		require.True(t, ok, "path %q is not in the table", v.Path.ID)
		require.Equal(t, want, v.Verdict, "path %q said %q because: %s",
			v.Path.ID, v.Verdict, v.Detail)
		require.NotEmpty(t, v.Detail, "path %q gave a verdict with no reason", v.Path.ID)
	}
}

// TestTheNumber is the figure this lane owes, asserted rather than described.
//
// It is deliberately not 12 of 12. Two of the paths cannot be closed without
// stopping the environment from working, one is not described by any network
// configuration at all, and one cannot be decided without a subscription, so a
// green here at 12 would mean somebody had shortened the enumeration.
func TestTheNumber(t *testing.T) {
	report := aca.Evaluate(referencePlan())
	require.Equal(t, 12, report.Total, "egress paths enumerated")
	require.Equal(t, 8, report.Closed, "closed by the generated configuration")
	require.Equal(t, 3, report.Open, "open, and named")
	require.Equal(t, 1, report.Unproven, "not decided by the configuration or the documentation")
	require.False(t, report.Contained(), "this plan must not report itself contained")
	require.Len(t, report.NotClosed(), 4)
}

// mutation is one break in the generated plan and the paths it should move.
type mutation struct {
	// path is the path whose check the break is aimed at.
	path string
	// what describes the break, for the failure message.
	what string
	// apply breaks the plan.
	apply func(*aca.Plan)
	// alsoOpens are paths that legitimately move as well, named so that a
	// break which moves everything cannot pass by moving its target too.
	alsoOpens []string
}

// mutations is the mutation test, run rather than reported.
//
// Every path this package calls Closed has at least one entry, because a
// verdict that cannot become Open is not a check. The repository's rule is one
// break per assertion, and the alsoOpens field is what enforces it: a break has
// to move its own path and the named ones and nothing else, so a predicate that
// returned Open for everything fails on the first row.
func mutations() []mutation {
	return []mutation{
		{
			path: "an-arbitrary-public-address",
			what: "no default route to the firewall",
			apply: func(p *aca.Plan) {
				p.Network.RouteTable.Routes = nil
			},
		},
		{
			path: "an-arbitrary-public-address",
			what: "an outbound rule permitting the Internet service tag",
			apply: func(p *aca.Plan) {
				p.Network.NSG.Outbound = append(p.Network.NSG.Outbound, aca.SecurityRule{
					Name: "allow-internet", Priority: 200, Access: aca.Allow, Protocol: "Tcp",
					SourceAddressPrefix: "10.30.1.0/27", DestinationAddressPrefix: "Internet",
					DestinationPortRange: "443",
				})
			},
		},
		{
			path: "an-arbitrary-public-address",
			what: "a firewall application rule allowing every name",
			apply: func(p *aca.Plan) {
				p.Network.Firewall.ApplicationRules = append(p.Network.Firewall.ApplicationRules,
					aca.ApplicationRule{Name: "allow-everything", Protocols: []string{"https:443"},
						TargetFQDNs: []string{"*"}, Action: aca.Allow})
			},
		},
		{
			path: "an-arbitrary-public-address",
			what: "no firewall at all",
			apply: func(p *aca.Plan) {
				p.Network.Firewall = nil
			},
			// With no firewall the plan's default route points at a virtual
			// appliance that is not this environment's firewall, which is
			// exactly what the peering path exists to notice.
			alsoOpens: []string{"a-peering-or-a-gateway-route"},
		},
		{
			path: "an-arbitrary-public-address",
			what: "the catch all outbound deny rule removed",
			apply: func(p *aca.Plan) {
				var kept []aca.SecurityRule
				for _, r := range p.Network.NSG.Outbound {
					if r.Name != "deny-internet" {
						kept = append(kept, r)
					}
				}
				p.Network.NSG.Outbound = kept
			},
		},
		{
			path: "an-arbitrary-public-address",
			what: "a NAT gateway on the subnet",
			apply: func(p *aca.Plan) {
				p.Network.Subnet.NATGatewayID = "natgw-egress"
			},
		},
		{
			path: "the-managed-identity-token-endpoint",
			what: "a system assigned identity, which sets IDENTITY_ENDPOINT",
			apply: func(p *aca.Plan) {
				p.App.Identity = aca.Identity{Type: "SystemAssigned"}
			},
		},
		{
			path: "the-managed-identity-token-endpoint",
			what: "a user assigned identity with no lifecycle of None",
			apply: func(p *aca.Plan) {
				p.App.Identity = aca.Identity{
					Type:                    "UserAssigned",
					UserAssignedIdentityIDs: []string{"/subscriptions/x/identities/puller"},
				}
			},
		},
		{
			path: "your-registry-and-its-layer-blobs",
			what: "an outbound rule naming the AzureContainerRegistry service tag",
			apply: func(p *aca.Plan) {
				p.Network.NSG.Outbound = append(p.Network.NSG.Outbound, aca.SecurityRule{
					Name: "allow-registry", Priority: 210, Access: aca.Allow, Protocol: "Tcp",
					SourceAddressPrefix:      "10.30.1.0/27",
					DestinationAddressPrefix: "AzureContainerRegistry",
					DestinationPortRange:     "443",
				})
			},
		},
		{
			path: "your-registry-and-its-layer-blobs",
			what: "a private endpoint with no private DNS zone, so the public name still resolves publicly",
			apply: func(p *aca.Plan) {
				p.Network.PrivateEndpoints[0].PrivateDNSZone = ""
			},
		},
		{
			path: "the-platform-log-sink",
			what: "a log analytics destination on the environment",
			apply: func(p *aca.Plan) {
				p.Environment.AppLogsConfiguration.Destination = "log-analytics"
			},
		},
		{
			path: "the-platform-log-sink",
			what: "an outbound rule naming the AzureMonitor service tag",
			apply: func(p *aca.Plan) {
				p.Network.NSG.Outbound = append(p.Network.NSG.Outbound, aca.SecurityRule{
					Name: "allow-monitor", Priority: 220, Access: aca.Allow, Protocol: "Tcp",
					SourceAddressPrefix:      "10.30.1.0/27",
					DestinationAddressPrefix: "AzureMonitor",
					DestinationPortRange:     "443",
				})
			},
		},
		{
			path: "a-consumption-only-environments-control-plane-tunnel",
			what: "the legacy Consumption only environment type",
			apply: func(p *aca.Plan) {
				p.EnvironmentType = aca.ConsumptionOnly
			},
		},
		{
			path: "a-consumption-only-environments-control-plane-tunnel",
			what: "the regional AzureCloud rule that the tunnel needs",
			apply: func(p *aca.Plan) {
				p.Network.NSG.Outbound = append(p.Network.NSG.Outbound, aca.SecurityRule{
					Name: "allow-cluster-tunnel", Priority: 230, Access: aca.Allow, Protocol: "Udp",
					SourceAddressPrefix:      "10.30.1.0/27",
					DestinationAddressPrefix: "AzureCloud.eastus",
					DestinationPortRange:     "1194",
				})
			},
			// Every Azure datacentre address in a region is an address this
			// plan does not name, so the first path is right to notice it too.
			alsoOpens: []string{"an-arbitrary-public-address"},
		},
		{
			path: "a-neighbouring-environments-subnet",
			what: "an outbound rule naming the whole virtual network rather than this subnet",
			apply: func(p *aca.Plan) {
				p.Network.NSG.Outbound = append(p.Network.NSG.Outbound, aca.SecurityRule{
					Name: "allow-vnet", Priority: 240, Access: aca.Allow, Protocol: "*",
					SourceAddressPrefix:      "10.30.1.0/27",
					DestinationAddressPrefix: "10.30.0.0/16",
					DestinationPortRange:     "*",
				})
			},
		},
		{
			path: "a-neighbouring-environments-subnet",
			what: "an inbound rule admitting the whole virtual network",
			apply: func(p *aca.Plan) {
				p.Network.NSG.Inbound = append(p.Network.NSG.Inbound, aca.SecurityRule{
					Name: "allow-vnet-in", Priority: 250, Access: aca.Allow, Protocol: "Tcp",
					SourceAddressPrefix:      "10.30.0.0/16",
					DestinationAddressPrefix: "10.30.1.0/27",
					DestinationPortRange:     "443",
				})
			},
		},
		{
			path: "a-peering-or-a-gateway-route",
			what: "a peering with the production network",
			apply: func(p *aca.Plan) {
				p.Network.VirtualNetwork.Peerings = append(p.Network.VirtualNetwork.Peerings,
					aca.Peering{Name: "to-production", RemoteVirtualNetwork: "vnet-production"})
			},
		},
		{
			path: "a-peering-or-a-gateway-route",
			what: "gateway route propagation left on",
			apply: func(p *aca.Plan) {
				p.Network.RouteTable.DisableBGPRoutePropagation = false
			},
		},
		{
			path: "a-peering-or-a-gateway-route",
			what: "a route to a virtual network gateway",
			apply: func(p *aca.Plan) {
				p.Network.RouteTable.Routes = append(p.Network.RouteTable.Routes, aca.Route{
					Name: "to-the-office", AddressPrefix: "10.90.0.0/16",
					NextHopType: aca.NextHopVirtualNetworkGateway,
				})
			},
		},
		{
			path: "a-virtual-network-service-endpoint",
			what: "a Microsoft.Storage service endpoint on the subnet",
			apply: func(p *aca.Plan) {
				p.Network.Subnet.ServiceEndpoints = []string{"Microsoft.Storage"}
			},
		},
	}
}

// TestEveryClosureCanSayNo breaks the thing each closed path depends on and
// requires the verdict to change.
//
// This is the mutation test, run in continuous integration rather than
// performed once by hand and written up. A predicate whose Closed verdicts
// survive their own mutation is a check that cannot say no, which this
// repository has repeatedly found to be worse than no check at all.
func TestEveryClosureCanSayNo(t *testing.T) {
	for _, m := range mutations() {
		t.Run(m.path+"/"+m.what, func(t *testing.T) {
			require.Equal(t, aca.Closed, expected[m.path],
				"a mutation is only meaningful against a path the plan closes")

			broken := referencePlan()
			m.apply(&broken)
			after := verdictsByID(aca.Evaluate(broken))

			require.Equal(t, aca.Open, after[m.path],
				"breaking %s left %s reporting %q, so that check cannot fail",
				m.what, m.path, after[m.path])

			moved := map[string]bool{m.path: true}
			for _, id := range m.alsoOpens {
				moved[id] = true
				require.Equal(t, aca.Open, after[id],
					"%s was declared to move as well and did not", id)
			}
			for id, want := range expected {
				if moved[id] {
					continue
				}
				require.Equal(t, want, after[id],
					"breaking %s also moved %s, which it was not declared to", m.what, id)
			}
		})
	}
}

// TestEveryClosedPathHasAMutation stops a path from being added with a check
// nothing ever breaks.
//
// Without it the suite above measures whatever it happens to cover, and the way
// a check that cannot say no gets into a repository is by arriving after the
// test that would have caught it.
func TestEveryClosedPathHasAMutation(t *testing.T) {
	covered := map[string]bool{}
	for _, m := range mutations() {
		covered[m.path] = true
	}
	for id, want := range expected {
		if want != aca.Closed {
			continue
		}
		require.True(t, covered[id],
			"%s is reported closed and no mutation proves that check can fail", id)
	}
}

// TestEveryPathIsDescribed requires the prose, because the enumeration is the
// deliverable and an id with no reason attached is not evidence of anything.
func TestEveryPathIsDescribed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range aca.Paths() {
		require.NotEmpty(t, p.ID)
		require.False(t, seen[p.ID], "duplicate path id %q", p.ID)
		seen[p.ID] = true
		require.NotEmpty(t, p.Name, "%s has no name", p.ID)
		require.NotEmpty(t, p.Why, "%s does not say why it is a real route", p.ID)
		require.NotEmpty(t, p.ClosedBy, "%s does not say what closes it", p.ID)
		require.NotNil(t, p.Check, "%s has no check", p.ID)
	}
}

// TestTheReportCarriesItsCaveat is the one assertion that guards the honesty of
// every number this package produces.
func TestTheReportCarriesItsCaveat(t *testing.T) {
	out := aca.Evaluate(referencePlan()).String()
	require.Contains(t, out, "8 of 12 egress paths")
	require.Contains(t, out, aca.Caveat)
	require.Contains(t, out, "not that Azure was seen enforcing it")
}

// TestNoEmDashInTheProse holds the repository's writing rule on the strings
// themselves rather than on the source, the way the engine's own four
// assertions do, because this package is prose as much as it is code and a file
// scanner does not read a composed report.
func TestNoEmDashInTheProse(t *testing.T) {
	report := aca.Evaluate(referencePlan())
	require.NotContains(t, report.String(), "—")
	for _, p := range aca.Paths() {
		for _, s := range []string{p.Name, p.Why, p.ClosedBy} {
			require.NotContains(t, s, "—", "%s", p.ID)
			require.NotContains(t, s, "--", "%s", p.ID)
		}
	}
	require.NotContains(t, aca.Caveat, "—")
	require.NotContains(t, aca.Caveat, "--")
}

// TestTheProviderRefusesAndSaysWhy is the end to end assertion: a manifest
// naming this runtime reaches Open and gets a refusal carrying the report.
func TestTheProviderRefusesAndSaysWhy(t *testing.T) {
	env := map[string]string{
		aca.EnvRegion: "eastus", aca.EnvResourceGroup: "antifailure",
		aca.EnvVNet: "vnet-antifailure", aca.EnvVNetCIDR: "10.30.0.0/16",
		aca.EnvSubnetCIDR: "10.30.1.0/27", aca.EnvRegistry: "antifailure.azurecr.io",
		aca.EnvFirewallIP: "10.30.255.4",
	}
	p := &aca.Provider{Getenv: func(k string) string { return env[k] }}
	require.Equal(t, "aca", p.Name())

	rt, err := p.Open(context.Background(), extension.RuntimeConfig{})
	require.Nil(t, rt, "a refusing provider must return no runtime at all")
	require.Error(t, err)
	require.Contains(t, err.Error(), "refuses to start an environment, on purpose")
	require.Contains(t, err.Error(), "8 of 12 egress paths")
	require.Contains(t, err.Error(), "the-instance-metadata-address")
	require.Contains(t, err.Error(), "azure-dns-at-168-63-129-16")
	require.Contains(t, err.Error(), "runtime.provider kubernetes")
	require.NotContains(t, err.Error(), "—")
}

// TestTheProviderSaysWhichVariablesAreMissing keeps the two refusals apart. An
// installation that has described no network should be told that, not handed a
// containment report about an empty plan.
func TestTheProviderSaysWhichVariablesAreMissing(t *testing.T) {
	p := &aca.Provider{Getenv: func(string) string { return "" }}
	rt, err := p.Open(context.Background(), extension.RuntimeConfig{})
	require.Nil(t, rt)
	require.Error(t, err)
	require.Contains(t, err.Error(), aca.EnvVNet)
	require.Contains(t, err.Error(), aca.EnvFirewallIP)
	require.Contains(t, err.Error(), "Even fully described it would refuse")
}

// TestTheGeneratedPlanBuildsSomething guards the direction the score could be
// gamed in.
//
// Two of the three open paths close instantly if the plan simply stops
// permitting the artifact registry and Azure DNS, and the resulting environment
// would not be created at all. So the score going up must not be achievable by
// deleting the reason it is down.
func TestTheGeneratedPlanBuildsSomething(t *testing.T) {
	plan := referencePlan()

	var dests []string
	for _, r := range plan.Network.NSG.Outbound {
		if r.Access == aca.Allow {
			dests = append(dests, r.DestinationAddressPrefix)
		}
	}
	require.Contains(t, dests, "MicrosoftContainerRegistry",
		"nothing pulls the platform's own system containers without this")
	require.Contains(t, dests, "AzureFrontDoor.FirstParty",
		"Microsoft names this as a dependency of the artifact registry tag")
	require.Contains(t, dests, aca.AzureDNSAddress,
		"Microsoft documents that denying Azure DNS stops the environment functioning")

	require.NotNil(t, plan.Network.Firewall)
	var names []string
	for _, r := range plan.Network.Firewall.ApplicationRules {
		names = append(names, r.TargetFQDNs...)
	}
	for _, required := range []string{
		"mcr.microsoft.com", "*.data.mcr.microsoft.com",
		"packages.aks.azure.com", "acs-mirror.azureedge.net",
	} {
		require.Contains(t, names, required,
			"Microsoft lists this under the scenario All scenarios, so an environment "+
				"without it is not created")
	}

	require.Len(t, plan.Network.PrivateEndpoints, 1,
		"the registry closure is a private endpoint and deleting it does not improve the score")
	require.NotEmpty(t, plan.Network.PrivateEndpoints[0].PrivateDNSZone,
		"a private endpoint with no zone leaves the public name resolving publicly")
}

// TestTheEnvironmentTypeIsTheOneThatCanBeRouted guards the field that decides
// more about containment than any other, and the delegation that has to agree
// with it.
func TestTheEnvironmentTypeIsTheOneThatCanBeRouted(t *testing.T) {
	plan := referencePlan()
	require.Equal(t, aca.WorkloadProfiles, plan.EnvironmentType,
		"a Consumption only environment supports no user defined route, so there is no "+
			"firewall for 0.0.0.0/0 to reach")
	require.Equal(t, "Microsoft.App/environments", plan.Network.Subnet.Delegation,
		"a workload profile environment requires this delegation and the legacy type forbids it")
	require.True(t, plan.Environment.VnetConfiguration.Internal)
	require.Equal(t, "Disabled", plan.Environment.PublicNetworkAccess)
	require.False(t, plan.App.Ingress.External)
}

// TestTheInstanceMetadataVerdictIsNotRoundedUp is its own test because it is
// the path the lane was warned about by name and the failure mode is a
// plausible sentence rather than a wrong boolean.
//
// The plan writes the one rule Azure documents for the address. That is more
// than the ECS lane could write and it is still not evidence, so the verdict
// has to stay unproven with the rule present AND with it absent, and the two
// reasons have to differ so that a reader can tell which case they are in.
func TestTheInstanceMetadataVerdictIsNotRoundedUp(t *testing.T) {
	const id = "the-instance-metadata-address"

	withRule := aca.Evaluate(referencePlan())
	require.Equal(t, aca.Unproven, verdictsByID(withRule)[id],
		"writing the documented rule does not make the path closed")
	present := detailByID(withRule)[id]
	require.Contains(t, present, "AzurePlatformIMDS")
	require.Contains(t, strings.ToLower(present), "needs a subscription")

	plan := referencePlan()
	var kept []aca.SecurityRule
	for _, r := range plan.Network.NSG.Outbound {
		if r.Name != "deny-instance-metadata" {
			kept = append(kept, r)
		}
	}
	plan.Network.NSG.Outbound = kept

	withoutRule := aca.Evaluate(plan)
	require.Equal(t, aca.Unproven, verdictsByID(withoutRule)[id],
		"removing the rule does not make the path open either, because no page says the "+
			"address answers")
	absent := detailByID(withoutRule)[id]
	require.NotEqual(t, present, absent,
		"the two cases must be distinguishable or the verdict says nothing")
	require.Contains(t, absent, "not even in the plan")
}

// TestTheAzureDNSPathCanOnlyBeClosedByBreakingTheEnvironment is the assertion
// that keeps this lane from inheriting the AWS answer.
//
// On AWS the resolver cannot be filtered by a security group at all and only a
// DNS Firewall rule group closes it. On Azure a security group reaches it
// through the AzurePlatformDNS service tag, so the check must be able to return
// Closed, and the generated plan must still not do it, because Microsoft
// documents that an environment with that rule does not function.
func TestTheAzureDNSPathCanOnlyBeClosedByBreakingTheEnvironment(t *testing.T) {
	const id = "azure-dns-at-168-63-129-16"

	base := aca.Evaluate(referencePlan())
	require.Equal(t, aca.Open, verdictsByID(base)[id])
	require.Contains(t, detailByID(base)[id], "isn't subject to user defined routes",
		"the reason the firewall is irrelevant here has to be in the report")

	plan := referencePlan()
	plan.Network.NSG.Outbound = append(plan.Network.NSG.Outbound, aca.SecurityRule{
		Name: "deny-azure-dns", Priority: 3100, Access: aca.Deny, Protocol: "*",
		SourceAddressPrefix: "10.30.1.0/27", DestinationAddressPrefix: "AzurePlatformDNS",
		DestinationPortRange: "*",
	})
	closed := aca.Evaluate(plan)
	require.Equal(t, aca.Closed, verdictsByID(closed)[id],
		"a security group CAN reach this path on Azure, unlike on AWS, and the check has to "+
			"be able to say so")
	require.Contains(t, detailByID(closed)[id], "doesn't function",
		"a closed verdict here has to carry the consequence Microsoft documents")
}

// TestTheChecksReadPriorityRatherThanPosition guards a defect that would not
// show up in any other test here.
//
// Azure evaluates security rules by priority and the first match wins. A check
// that read the slice in the order it was built would agree with Azure by
// accident on this plan and disagree on a plan somebody wrote by hand, so the
// verdicts must survive the slices being reversed.
func TestTheChecksReadPriorityRatherThanPosition(t *testing.T) {
	plan := referencePlan()
	plan.Network.NSG.Outbound = reversed(plan.Network.NSG.Outbound)
	plan.Network.NSG.Inbound = reversed(plan.Network.NSG.Inbound)

	after := verdictsByID(aca.Evaluate(plan))
	for id, want := range expected {
		require.Equal(t, want, after[id],
			"%s changed when the rules were reordered, so the check reads position", id)
	}
}

// TestTheReportNamesRulesInThePriorityAzureReadsThem is the assertion the test
// above cannot make, and it exists because a mutation showed that.
//
// Reversing the slices leaves every verdict where it was, and it would do that
// for a build with no sort in it at all: every check in this file is a
// quantifier over the whole rule set rather than a walk that stops at the first
// match, so position cannot reach a verdict. Disabling the sort in byPriority
// leaves the entire package green, which makes that line something no test
// could say no about. What the sort actually buys is the order of the
// enumeration a person reads after a refusal, so that is what this pins: two
// rules that open the same path, handed over in the order Azure would evaluate
// them second, are still reported first things first.
func TestTheReportNamesRulesInThePriorityAzureReadsThem(t *testing.T) {
	const id = "an-arbitrary-public-address"

	plan := referencePlan()
	// Appended late before early, so that slice order and priority order
	// disagree. Both destinations are public, so both are reasons this path is
	// open and both have to appear.
	plan.Network.NSG.Outbound = append(plan.Network.NSG.Outbound,
		aca.SecurityRule{
			Name: "allow-late", Priority: 2900, Access: aca.Allow, Protocol: "*",
			SourceAddressPrefix: "10.30.1.0/27", DestinationAddressPrefix: "203.0.113.0/24",
			DestinationPortRange: "*",
		},
		aca.SecurityRule{
			Name: "allow-early", Priority: 2800, Access: aca.Allow, Protocol: "*",
			SourceAddressPrefix: "10.30.1.0/27", DestinationAddressPrefix: "198.51.100.0/24",
			DestinationPortRange: "*",
		})

	report := aca.Evaluate(plan)
	require.Equal(t, aca.Open, verdictsByID(report)[id],
		"two allow rules to public ranges have to open this path")

	detail := detailByID(report)[id]
	require.Contains(t, detail, `"allow-early"`, "the rule at priority 2800 is not named")
	require.Contains(t, detail, `"allow-late"`, "the rule at priority 2900 is not named")
	require.Less(t,
		strings.Index(detail, `"allow-early"`), strings.Index(detail, `"allow-late"`),
		"the reasons are listed in slice order, so a reader is sent to priority 2900 first")
}

func reversed(rules []aca.SecurityRule) []aca.SecurityRule {
	out := make([]aca.SecurityRule, 0, len(rules))
	for i := len(rules) - 1; i >= 0; i-- {
		out = append(out, rules[i])
	}
	return out
}

func verdictsByID(r aca.Report) map[string]aca.Verdict {
	out := map[string]aca.Verdict{}
	for _, v := range r.Verdicts {
		out[v.Path.ID] = v.Verdict
	}
	return out
}

func detailByID(r aca.Report) map[string]string {
	out := map[string]string{}
	for _, v := range r.Verdicts {
		out[v.Path.ID] = v.Detail
	}
	return out
}

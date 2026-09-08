package aca

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Verdict is what the predicate can say about one egress path.
//
// Three values rather than two, and the third is the point. A containment claim
// written with a boolean has nowhere to put "I looked and the configuration
// does not decide this", so it rounds that case to whichever answer the author
// was hoping for. Every instrument in this repository that turned out to be
// lying had that shape: a check that could not say "I could not look" reported
// a pass instead.
type Verdict string

const (
	// Closed means the generated configuration removes this path. It does NOT
	// mean a packet was observed failing to get out. See Report.Caveat.
	Closed Verdict = "closed"
	// Open means the path exists in the generated configuration, either
	// because Azure provides no way to remove it or because removing it would
	// stop the environment from working.
	Open Verdict = "open"
	// Unproven means the configuration does not decide it and neither does the
	// documentation. It is not a weaker Closed and it must never be counted as
	// one.
	Unproven Verdict = "unproven"
)

// AzureDNSAddress is the virtual public address Azure answers DNS, DHCP, the
// load balancer health probe and the guest agent on.
//
// It is a public address that is not the internet, so it gets a named constant
// rather than being caught by a range check. Every check in this file that
// walks security group rules has to skip it deliberately, because a plan that
// permits it is not thereby permitting the internet and a plan that denies it
// is not thereby contained.
const AzureDNSAddress = "168.63.129.16"

// Path is one distinct way out of a Container Apps replica.
//
// Distinct means a different route, not a different phrasing of one route. Two
// entries that the same single change would close are one path, because a count
// of paths is only worth publishing if closing one of them is real work. That
// rule is why the managed identity token endpoint and the Microsoft Entra ID
// egress it requires are one entry here rather than two: removing the identity
// removes both.
type Path struct {
	// ID is stable and appears in the refusal message and in the report.
	ID string
	// Name is one line a person can read.
	Name string
	// Why records that this is a route somebody has actually used, which is
	// the bar for being in this list at all. A theoretical path inflates the
	// denominator and makes the ratio look worse than the product is, which is
	// its own kind of dishonesty.
	Why string
	// ClosedBy names the mechanism, or says plainly that nothing closes it.
	ClosedBy string
	// Check decides the path against one plan and says why.
	Check func(Plan) (Verdict, string)
}

// Paths is every egress path out of an Azure Container Apps replica that this
// lane could enumerate, in a stable order.
//
// The order is the order a person should read them: the internet, then the
// platform's own obligations, then the two link local style endpoints, then the
// dependencies the environment chooses, then the routes that reach sideways,
// then the channel that does not use the network at all.
func Paths() []Path {
	return []Path{
		{
			ID:   "an-arbitrary-public-address",
			Name: "a TCP connection to any public address the plan does not name",
			Why: "It is the obvious answer to interception by name, and it is what every " +
				"exfiltration tool does first. A Container Apps environment starts with a " +
				"route to it: Microsoft bills an internal environment for \"one standard " +
				"static public IP for egress\", so internal governs ingress and leaves " +
				"outbound alone.",
			ClosedBy: "a route sending 0.0.0.0/0 to Azure Firewall, a firewall policy with no " +
				"allow rule naming every name, a security group whose last outbound rule " +
				"denies the Internet tag, no allow rule naming a public range, and no NAT " +
				"gateway on the subnet",
			Check: checkArbitraryPublicAddress,
		},
		{
			ID:   "the-platform-images-with-no-private-endpoint",
			Name: "Microsoft Artifact Registry and the AKS binary mirrors, which every environment must reach",
			Why: "Microsoft's Container Apps firewall guidance lists mcr.microsoft.com, " +
				"*.data.mcr.microsoft.com, packages.aks.azure.com and acs-mirror.azureedge.net " +
				"under the scenario \"All scenarios\", and none of the four has a private " +
				"endpoint offering. This is where Container Apps is strictly worse than " +
				"Fargate: an ECS task's registry, log and layer dependencies are all " +
				"reachable through endpoints inside the VPC, so the task can start with no " +
				"route to the internet, and a Container Apps replica cannot.",
			ClosedBy: "nothing, without giving up the ability to run an environment at all. It " +
				"is narrowed by firewall application rules naming those four and by security " +
				"group rules naming the MicrosoftContainerRegistry and AzureFrontDoor service " +
				"tags rather than a range",
			Check: checkPlatformImages,
		},
		{
			ID:   "azure-dns-at-168-63-129-16",
			Name: "a DNS query to Azure DNS, whose payload is whatever the sender puts in it",
			Why: "A name lookup is a data channel: the question is chosen by the sender, so a " +
				"resolver that answers recursively is an upload. This is the path a " +
				"Kubernetes intuition gets wrong and it is ALSO the path an AWS answer gets " +
				"wrong, in the opposite direction. On AWS the resolver cannot be filtered by " +
				"a security group at all. On Azure it can: the AzurePlatformDNS service tag " +
				"exists precisely so that an outbound deny rule reaches it. The Container " +
				"Apps firewall page then closes the door anyway, in one sentence: \"Don't " +
				"explicitly deny the Azure DNS address 168.63.129.16 in the outgoing NSG " +
				"rules. If you do, your Container Apps environment doesn't function.\"",
			ClosedBy: "an outbound security group rule denying the AzurePlatformDNS service " +
				"tag, which Microsoft documents as stopping the environment from working. " +
				"Nothing else reaches it: the address is a virtual IP of the host node and " +
				"Microsoft states it \"isn't subject to user defined routes\", so the " +
				"firewall the default route points at never sees the query",
			Check: checkAzureDNS,
		},
		{
			ID:   "the-instance-metadata-address",
			Name: "169.254.169.254, which on a virtual machine hands out the instance identity and its tokens",
			Why: "It is not on the internet and it is the highest value target in the list, " +
				"because it turns any request forgery in the application into cloud " +
				"credentials. The AzurePlatformIMDS service tag exists so that an outbound " +
				"deny rule can reach it, and this plan writes that rule.",
			ClosedBy: "not established. The Container Apps documentation never mentions the " +
				"address in either direction: it is absent from the outbound requirements, " +
				"absent from the firewall tables, and absent from the managed identity page, " +
				"which routes tokens through IDENTITY_ENDPOINT instead. So the rule is " +
				"written and the verdict is still unproven",
			Check: checkInstanceMetadata,
		},
		{
			ID:   "the-managed-identity-token-endpoint",
			Name: "IDENTITY_ENDPOINT, which hands a Microsoft Entra ID token to anything in the container",
			Why: "Microsoft documents it as \"available from within the app with a standard " +
				"HTTP GET request\", with IDENTITY_HEADER as the only thing between it and a " +
				"server side request forgery. It is Azure's answer to 169.254.170.2 on " +
				"Fargate, and the Entra ID egress it obliges is the same path rather than a " +
				"second one, because removing the identity removes both.",
			ClosedBy: "an identity type of None on the app, or an identity settings entry with " +
				"a lifecycle of None for every identity the app declares. This is the one " +
				"place Azure is better than the Fargate equivalent, where the endpoint cannot " +
				"be turned off and an empty task role is the only defence",
			Check: checkManagedIdentity,
		},
		{
			ID:   "your-registry-and-its-layer-blobs",
			Name: "the application image's own registry and the blob storage its layers live in",
			Why: "The environment has to pull the application image from somewhere, and the " +
				"public path to a registry is a path to every repository in it plus, through " +
				"*.blob.core.windows.net, a great deal of storage that is not yours. The " +
				"Container Apps firewall page gives the way out: a registry configured with " +
				"private endpoints needs no security group rule at all.",
			ClosedBy: "a private endpoint on the registry with a private DNS zone that resolves " +
				"its public name to the endpoint, and no security group rule naming the " +
				"AzureContainerRegistry or Storage service tags",
			Check: checkRegistry,
		},
		{
			ID:   "the-platform-log-sink",
			Name: "the environment's log destination, which carries whatever the application wrote",
			Why: "A log line is chosen by the application, so a log shipper is an outbound " +
				"channel with a formatting convention. Microsoft marks the AzureMonitor rule " +
				"\"required only when you're using Azure Monitor\", which means it is a " +
				"decision rather than an obligation, and a twin of production has no business " +
				"shipping its masked data anywhere.",
			ClosedBy: "an app logs destination of none on the environment and no security group " +
				"rule naming the AzureMonitor service tag. The cost is real and is stated " +
				"rather than hidden: the platform then ships no container logs anywhere",
			Check: checkLogSink,
		},
		{
			ID:   "a-consumption-only-environments-control-plane-tunnel",
			Name: "the AKS control plane tunnel and the open NTP rule a Consumption only environment forces",
			Why: "Microsoft's security group table for a Consumption only environment requires " +
				"an outbound allow to AzureCloud on TCP 443, described as \"a way to allow " +
				"all FQDN-based outbound dependencies that don't have a static IP\", an allow " +
				"to AzureCloud in the region on UDP 1194 and TCP 9000 for the tunnel to the " +
				"underlying cluster's control plane, and an allow to * on UDP 123 for NTP. " +
				"Those are every Azure datacentre address and every address on earth, in an " +
				"environment that also supports no user defined routes.",
			ClosedBy: "choosing a workload profile environment, which is the default, and " +
				"emitting none of those four rules",
			Check: checkConsumptionOnlyTunnel,
		},
		{
			ID:   "a-neighbouring-environments-subnet",
			Name: "another environment's replicas, which share the virtual network but not the subnet",
			Why: "Container Apps has no per app or per replica security group, so the subnet " +
				"is the finest granularity there is and a rule naming the virtual network " +
				"rather than the subnet reaches every other environment in it. The Kubernetes " +
				"suite has a behaviour for exactly this and it is the most important one it " +
				"has.",
			ClosedBy: "one dedicated subnet and one security group per environment, outbound " +
				"rules naming no private range wider than this environment's own subnet, and " +
				"inbound rules admitting only this subnet and the load balancer probe",
			Check: checkNeighbouringEnvironment,
		},
		{
			ID:   "a-peering-or-a-gateway-route",
			Name: "a route to the corporate network, which never touches the internet and is not covered by an internet check",
			Why: "A containment check written as \"no route to the internet\" passes a virtual " +
				"network peered with the production one. That is worse than reaching the " +
				"internet, because the thing on the other side is the database this " +
				"environment is a twin of. Microsoft's own Container Apps walkthrough sets " +
				"gateway route propagation to No on the route table, which is an " +
				"acknowledgement that the corporate network can otherwise add a route here " +
				"without touching this plan.",
			ClosedBy: "no peering on the virtual network, gateway route propagation disabled, " +
				"no route with a next hop of VirtualNetworkGateway, and no virtual appliance " +
				"route pointing anywhere but this environment's firewall",
			Check: checkPeeringOrGateway,
		},
		{
			ID:   "a-virtual-network-service-endpoint",
			Name: "a service endpoint on the subnet, which is a route to an entire Azure service",
			Why: "Microsoft states that implementing a service endpoint means \"Azure adds a " +
				"route to a virtual network subnet for the service\", and the address " +
				"prefixes in that route are the whole service tag. It is not a route to your " +
				"storage account, it is a route to Storage, and it is added to the subnet " +
				"rather than to the plan's own route table, so a reader auditing the route " +
				"table does not see it.",
			ClosedBy: "no service endpoints on the subnet. A private endpoint does the same job " +
				"scoped to one resource and is what this plan uses instead",
			Check: checkServiceEndpoint,
		},
		{
			ID:   "the-container-console-and-the-log-stream",
			Name: "az containerapp exec, which is an interactive shell that never touches the subnet",
			Why: "It is the one path on this list that a person opens deliberately, while " +
				"debugging, and then forgets. Microsoft documents the console and the log " +
				"stream as reached through the Azure control plane and a second generated URL " +
				"under azurecontainerapps.dev, not through the environment's own address, so " +
				"no security group rule, route or firewall policy in this plan describes it.",
			ClosedBy: "nothing in this plan. The container console page documents no switch " +
				"that turns it off for an app or an environment, and the only control this " +
				"lane could identify is Azure role based access control, which is not part of " +
				"the network configuration and is not generated here",
			Check: checkContainerConsole,
		},
	}
}

// PathVerdict is one path with what the predicate decided about one plan.
type PathVerdict struct {
	Path    Path
	Verdict Verdict
	Detail  string
}

// Report is the whole predicate applied to one plan.
type Report struct {
	// Verdicts are in Paths order.
	Verdicts []PathVerdict
	// Closed, Open and Unproven count the verdicts. They are separate fields
	// rather than one ratio because the ratio that matters is closed over
	// total, and burying an unproven inside a numerator is the exact
	// arithmetic this repository already caught its own fidelity instrument
	// doing.
	Closed   int
	Open     int
	Unproven int
	// Total is len(Verdicts).
	Total int
}

// Caveat is the sentence that has to travel with every number this package
// produces, and it is a constant so that it cannot be paraphrased away.
//
// A Closed verdict is a statement about a JSON document. Whether Azure enforces
// that document is a statement about a subscription, and nothing in this
// repository has ever observed it.
const Caveat = "Every verdict here is computed from the configuration this runtime would " +
	"generate. None of it has been applied to an Azure subscription and no packet has been " +
	"observed failing to leave a replica. A closed verdict means the generated configuration " +
	"removes the path, not that Azure was seen enforcing it."

// Evaluate applies every path's check to a plan.
func Evaluate(plan Plan) Report {
	paths := Paths()
	report := Report{Verdicts: make([]PathVerdict, 0, len(paths)), Total: len(paths)}
	for _, path := range paths {
		verdict, detail := path.Check(plan)
		report.Verdicts = append(report.Verdicts, PathVerdict{Path: path, Verdict: verdict, Detail: detail})
		switch verdict {
		case Closed:
			report.Closed++
		case Open:
			report.Open++
		default:
			report.Unproven++
		}
	}
	return report
}

// Contained reports whether every enumerated path is closed.
//
// It is false for the plans this package generates, and that is not a bug to be
// fixed later. Two of the paths cannot be closed on Container Apps without
// stopping the environment from working, one cannot be closed by any network
// configuration at all, and one cannot be decided without a subscription. A
// true here would mean the enumeration had been shortened rather than that the
// containment had improved.
func (r Report) Contained() bool { return r.Closed == r.Total }

// NotClosed lists the paths that are not closed, in Paths order, for the
// refusal message.
func (r Report) NotClosed() []PathVerdict {
	var out []PathVerdict
	for _, v := range r.Verdicts {
		if v.Verdict != Closed {
			out = append(out, v)
		}
	}
	return out
}

// String renders the report the way the refusal prints it.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d egress paths out of an Azure Container Apps replica are closed by "+
		"the generated configuration (%d open, %d unproven).\n", r.Closed, r.Total, r.Open, r.Unproven)
	for _, v := range r.Verdicts {
		fmt.Fprintf(&b, "  %-9s %s\n             %s\n", v.Verdict, v.Path.ID, v.Detail)
	}
	b.WriteString("\n" + Caveat + "\n")
	return b.String()
}

// checkArbitraryPublicAddress requires every mechanism the plan relies on to be
// present, not merely one of them.
//
// All of them rather than any one, and that choice needs defending because
// several are sufficient alone. The predicate is a statement about the
// configuration this runtime generates, not about the minimum configuration
// that would happen to work: a plan that has lost one of its closures is no
// longer the plan the proof covers, and reporting it closed would mean the
// check cannot fail when the generator regresses. A check that cannot say no is
// worse than no check.
func checkArbitraryPublicAddress(p Plan) (Verdict, string) {
	var missing []string
	for _, rule := range outbound(p) {
		if rule.Access != Allow {
			continue
		}
		if broadDestination(rule.DestinationAddressPrefix) {
			missing = append(missing, fmt.Sprintf("outbound rule %q permits %s to %s",
				rule.Name, describePorts(rule), rule.DestinationAddressPrefix))
		}
	}
	if !hasDenyAllOutbound(p.Network.NSG) {
		missing = append(missing, "no outbound rule denies the Internet service tag, so a "+
			"destination no rule names is permitted by Azure's own default outbound rule")
	}
	if !defaultRouteToFirewall(p) {
		missing = append(missing, "the route table sends no 0.0.0.0/0 to this environment's "+
			"firewall, so traffic the security group permits leaves through the environment's "+
			"own public egress address")
	}
	if p.Network.Subnet.NATGatewayID != "" {
		missing = append(missing, fmt.Sprintf("the subnet has NAT gateway %s, which gives it a "+
			"static public address for outbound traffic", p.Network.Subnet.NATGatewayID))
	}
	if fw := p.Network.Firewall; fw == nil {
		missing = append(missing, "the plan has no firewall, so nothing filters by name")
	} else {
		for _, rule := range fw.ApplicationRules {
			if !strings.EqualFold(rule.Action, Allow) {
				continue
			}
			for _, fqdn := range rule.TargetFQDNs {
				if strings.TrimSpace(fqdn) == "*" {
					missing = append(missing, fmt.Sprintf("firewall application rule %q allows "+
						"every name", rule.Name))
				}
			}
		}
		for _, rule := range fw.NetworkRules {
			if !strings.EqualFold(rule.Action, Allow) {
				continue
			}
			for _, dest := range rule.DestinationAddresses {
				if broadDestination(dest) {
					missing = append(missing, fmt.Sprintf("firewall network rule %q allows %s",
						rule.Name, dest))
				}
			}
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "0.0.0.0/0 goes to the firewall, the firewall names no wildcard, the last " +
		"outbound rule denies the Internet tag, no allow rule names a public range, and the " +
		"subnet has no NAT gateway"
}

// checkPlatformImages reports the reach the design is obliged to open, and how
// narrow it is, which is the only thing that can change.
func checkPlatformImages(p Plan) (Verdict, string) {
	var tags, names, wildcards []string
	for _, rule := range outbound(p) {
		if rule.Access != Allow {
			continue
		}
		if platformImageTag(rule.DestinationAddressPrefix) {
			tags = append(tags, rule.DestinationAddressPrefix)
		}
	}
	if fw := p.Network.Firewall; fw != nil {
		for _, rule := range fw.ApplicationRules {
			if !strings.EqualFold(rule.Action, Allow) {
				continue
			}
			for _, fqdn := range rule.TargetFQDNs {
				fqdn = strings.TrimSpace(fqdn)
				if fqdn == "" || fqdn == "*" {
					continue
				}
				names = append(names, fqdn)
				if strings.HasPrefix(fqdn, "*.") {
					wildcards = append(wildcards, fqdn)
				}
			}
		}
	}
	if len(tags) == 0 && len(names) == 0 {
		return Closed, "the plan permits neither the artifact registry service tags nor any " +
			"name. Note that a Container Apps environment cannot be created without reaching " +
			"mcr.microsoft.com, *.data.mcr.microsoft.com, packages.aks.azure.com and " +
			"acs-mirror.azureedge.net, so a plan that closes this path builds no environment"
	}
	sort.Strings(tags)
	sort.Strings(names)
	detail := fmt.Sprintf("the replica's subnet reaches %s by service tag and %s by name, "+
		"because Microsoft requires all four artifact and mirror names in every scenario and "+
		"none of them has a private endpoint",
		joinOrNone(tags), joinOrNone(names))
	if len(wildcards) > 0 {
		sort.Strings(wildcards)
		detail += fmt.Sprintf("; %s are wildcards, and an Azure Firewall application rule "+
			"without TLS inspection matches the name the client offered rather than the one "+
			"it connects to", strings.Join(wildcards, ", "))
	}
	return Open, detail
}

// checkAzureDNS is the path an AWS answer gets wrong in the opposite direction,
// and saying which direction is the useful part.
func checkAzureDNS(p Plan) (Verdict, string) {
	const context = "a query here is not filtered by anything else in this plan: Microsoft " +
		"states the address is a virtual IP of the host node and \"isn't subject to user " +
		"defined routes\", so the firewall the default route names never sees it. "

	for _, rule := range outbound(p) {
		if rule.Access == Deny && strings.EqualFold(rule.DestinationAddressPrefix, "AzurePlatformDNS") {
			return Closed, context + fmt.Sprintf("outbound rule %q denies the AzurePlatformDNS "+
				"service tag, which is the one documented mechanism that reaches it. Microsoft "+
				"also documents the consequence: \"Don't explicitly deny the Azure DNS address "+
				"168.63.129.16 in the outgoing NSG rules. If you do, your Container Apps "+
				"environment doesn't function.\" So this verdict describes a plan that closes "+
				"the path and builds nothing", rule.Name)
		}
	}
	for _, rule := range outbound(p) {
		if rule.Access == Allow && strings.HasPrefix(rule.DestinationAddressPrefix, AzureDNSAddress) {
			return Open, context + fmt.Sprintf("outbound rule %q permits %s to %s and no rule "+
				"denies the AzurePlatformDNS service tag, so the resolver answers and the "+
				"question in each query is chosen by the sender", rule.Name,
				describePorts(rule), rule.DestinationAddressPrefix)
		}
	}
	return Open, context + "no rule denies the AzurePlatformDNS service tag, and Microsoft " +
		"states that \"By default DNS communication isn't subject to the configured network " +
		"security groups unless targeted using the AzurePlatformDNS service tag\", so the " +
		"resolver answers whatever else the plan says"
}

// checkInstanceMetadata returns Unproven whatever the plan says, and that is
// the honest answer rather than a placeholder.
//
// The plan writes the one rule Azure documents for the path, an outbound deny
// to the AzurePlatformIMDS service tag. That is more than the ECS lane could
// write, where no field of a task definition reaches the address at all. It is
// still not evidence, because the Container Apps documentation never mentions
// the address in either direction: it is absent from the outbound requirements,
// from both firewall tables and from the managed identity page, which sends
// tokens through IDENTITY_ENDPOINT instead. Whether a replica's network
// namespace is even on the path this rule filters is a fact about a running
// replica, and rounding "the rule is written" up to "the path is closed" is
// exactly how a containment claim softens into a plausible sentence.
func checkInstanceMetadata(p Plan) (Verdict, string) {
	for _, rule := range outbound(p) {
		if rule.Access == Deny && strings.EqualFold(rule.DestinationAddressPrefix, "AzurePlatformIMDS") {
			return Unproven, fmt.Sprintf("outbound rule %q denies the AzurePlatformIMDS service "+
				"tag, which is the only mechanism Azure documents for this address. No "+
				"Container Apps page states that the rule applies to a replica, or that the "+
				"address answers, or that it does not. Settling this needs one request from "+
				"one running replica, which needs a subscription", rule.Name)
		}
	}
	return Unproven, "no rule denies the AzurePlatformIMDS service tag, so the one mechanism " +
		"Azure documents for this address is not even in the plan. That still does not make " +
		"the path open, because no Container Apps page states that the address answers a " +
		"replica. Settling this needs one request from one running replica, which needs a " +
		"subscription"
}

// checkManagedIdentity is closed by an absence, which is the cheapest and most
// durable kind of closure there is.
func checkManagedIdentity(p Plan) (Verdict, string) {
	declared := declaredIdentities(p.App)
	if len(declared) == 0 {
		return Closed, "the app declares no managed identity, so IDENTITY_ENDPOINT and " +
			"IDENTITY_HEADER are not set and there is nothing for the endpoint to vend. The " +
			"environment consequently needs no outbound rule for Microsoft Entra ID either, " +
			"which is the same closure and not a second one"
	}
	lifecycle := map[string]string{}
	for _, s := range p.App.IdentitySettings {
		lifecycle[strings.ToLower(strings.TrimSpace(s.Identity))] = strings.ToLower(strings.TrimSpace(s.Lifecycle))
	}
	var reachable []string
	for _, id := range declared {
		if lifecycle[strings.ToLower(id)] != "none" {
			reachable = append(reachable, id)
		}
	}
	if len(reachable) == 0 {
		return Closed, fmt.Sprintf("the app declares %s and every one of them carries an "+
			"identity settings lifecycle of None, which Microsoft documents as \"Not available "+
			"to any containers\"", strings.Join(declared, ", "))
	}
	sort.Strings(reachable)
	return Open, fmt.Sprintf("the app declares %s with no lifecycle of None, so IDENTITY_ENDPOINT "+
		"vends a Microsoft Entra ID token to anything in the container that reads "+
		"IDENTITY_HEADER", strings.Join(reachable, ", "))
}

// checkRegistry is the one path where Azure gives a closure that AWS does not.
func checkRegistry(p Plan) (Verdict, string) {
	var public []string
	for _, rule := range outbound(p) {
		if rule.Access != Allow {
			continue
		}
		dest := rule.DestinationAddressPrefix
		if strings.EqualFold(dest, "AzureContainerRegistry") || strings.HasPrefix(dest, "Storage") {
			public = append(public, fmt.Sprintf("outbound rule %q permits %s to %s",
				rule.Name, describePorts(rule), dest))
		}
	}
	if fw := p.Network.Firewall; fw != nil {
		for _, rule := range fw.ApplicationRules {
			if !strings.EqualFold(rule.Action, Allow) {
				continue
			}
			for _, fqdn := range rule.TargetFQDNs {
				if registryName(fqdn) {
					public = append(public, fmt.Sprintf("firewall application rule %q allows %s",
						rule.Name, fqdn))
				}
			}
		}
	}
	if len(public) > 0 {
		return Open, strings.Join(public, "; ") + "; a public registry endpoint is a path to " +
			"every repository the caller can authenticate to, and the blob names beside it are " +
			"a great deal of storage that is not yours"
	}
	for _, pe := range p.Network.PrivateEndpoints {
		if !strings.Contains(strings.ToLower(pe.Service), "containerregistry") {
			continue
		}
		if strings.TrimSpace(pe.PrivateDNSZone) == "" {
			return Open, fmt.Sprintf("private endpoint %q fronts %s with no private DNS zone, "+
				"so the registry's public name still resolves to its public address and the "+
				"endpoint is decorative", pe.Name, pe.Target)
		}
		return Closed, fmt.Sprintf("private endpoint %q resolves %s through %s to an address "+
			"inside this virtual network, and no rule names the AzureContainerRegistry or "+
			"Storage service tags. Microsoft states that a registry configured with private "+
			"endpoints needs no security group rule at all", pe.Name, pe.Target, pe.PrivateDNSZone)
	}
	return Closed, "the plan names no registry, by tag, by name or by private endpoint. Note " +
		"that an application image has to come from somewhere, so a plan that closes this path " +
		"this way runs no application"
}

// checkLogSink treats the log destination as egress, because it is.
func checkLogSink(p Plan) (Verdict, string) {
	var missing []string
	if dest := strings.ToLower(strings.TrimSpace(p.Environment.AppLogsConfiguration.Destination)); dest != "" && dest != "none" {
		missing = append(missing, fmt.Sprintf("the environment ships container logs to %q, and "+
			"a log line's content is chosen by the application", dest))
	}
	for _, rule := range outbound(p) {
		if rule.Access == Allow && strings.EqualFold(rule.DestinationAddressPrefix, "AzureMonitor") {
			missing = append(missing, fmt.Sprintf("outbound rule %q permits %s to AzureMonitor",
				rule.Name, describePorts(rule)))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "the environment's app logs destination is none and no rule names the " +
		"AzureMonitor service tag. The cost is stated rather than hidden: the platform then " +
		"ships no container logs anywhere, and af logs reads them from the environment instead"
}

// checkConsumptionOnlyTunnel is decided by one field and by the rules that
// field obliges, and it is worth its own path because the field is a choice
// somebody makes once and never revisits.
func checkConsumptionOnlyTunnel(p Plan) (Verdict, string) {
	var missing []string
	if strings.EqualFold(p.EnvironmentType, ConsumptionOnly) {
		missing = append(missing, "the environment type is Consumption only, which Microsoft's "+
			"own security group table requires to permit AzureCloud on TCP 443, AzureCloud in "+
			"the region on UDP 1194 and TCP 9000 for the tunnel to the underlying cluster's "+
			"control plane, and * on UDP 123 for NTP, in an environment that supports no user "+
			"defined routes")
	}
	for _, rule := range outbound(p) {
		if rule.Access != Allow {
			continue
		}
		dest := rule.DestinationAddressPrefix
		if strings.EqualFold(dest, "AzureCloud") || strings.HasPrefix(dest, "AzureCloud.") {
			missing = append(missing, fmt.Sprintf("outbound rule %q permits %s to %s, which is "+
				"every Azure datacentre address in scope", rule.Name, describePorts(rule), dest))
		}
		udp := strings.EqualFold(rule.Protocol, "Udp") || rule.Protocol == "*" || rule.Protocol == ""
		if udp && ruleCoversPort(rule, 123) && broadDestination(dest) {
			missing = append(missing, fmt.Sprintf("outbound rule %q permits NTP to %s, which is "+
				"the rule a Consumption only environment obliges and which reaches every "+
				"address on earth", rule.Name, dest))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "the environment type is a workload profile environment and no rule names " +
		"AzureCloud or permits NTP outbound"
}

// checkNeighbouringEnvironment is the isolation promise, and it is written
// against the subnet because Container Apps has nothing finer.
func checkNeighbouringEnvironment(p Plan) (Verdict, string) {
	subnet := strings.TrimSpace(p.Network.Subnet.AddressPrefix)
	var missing []string
	for _, rule := range outbound(p) {
		if rule.Access != Allow {
			continue
		}
		dest := strings.TrimSpace(rule.DestinationAddressPrefix)
		if dest == "" || !isCIDROrAddress(dest) || dest == AzureDNSAddress {
			continue
		}
		if reachesPublicIPv4(dest) {
			continue
		}
		if !withinPrefix(dest, subnet) {
			missing = append(missing, fmt.Sprintf("outbound rule %q permits %s to %s, which is "+
				"a private range outside this environment's own subnet %s",
				rule.Name, describePorts(rule), dest, subnet))
		}
	}
	for _, rule := range inbound(p) {
		if rule.Access != Allow {
			continue
		}
		src := strings.TrimSpace(rule.SourceAddressPrefix)
		if strings.EqualFold(src, "AzureLoadBalancer") {
			continue
		}
		if isCIDROrAddress(src) && withinPrefix(src, subnet) {
			continue
		}
		missing = append(missing, fmt.Sprintf("inbound rule %q admits %s, which is neither this "+
			"environment's own subnet nor the load balancer probe", rule.Name, src))
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, fmt.Sprintf("every outbound rule naming a private range names %s or narrower, "+
		"and inbound admits only that subnet and the AzureLoadBalancer probe. Reaching the "+
		"declared web service from outside the subnet needs an inbound rule this plan does not "+
		"write, and writing one for the whole virtual network opens this path", subnet)
}

// checkPeeringOrGateway is the path a naive internet check misses entirely.
func checkPeeringOrGateway(p Plan) (Verdict, string) {
	var found []string
	for _, peering := range p.Network.VirtualNetwork.Peerings {
		found = append(found, fmt.Sprintf("peering %q reaches %s", peering.Name,
			peering.RemoteVirtualNetwork))
	}
	if !p.Network.RouteTable.DisableBGPRoutePropagation {
		found = append(found, "the route table propagates gateway routes, so a route learned "+
			"from a virtual network gateway is added to this subnet without touching this plan")
	}
	firewallIP := ""
	if p.Network.Firewall != nil {
		firewallIP = strings.TrimSpace(p.Network.Firewall.PrivateIP)
	}
	for _, r := range p.Network.RouteTable.Routes {
		switch r.NextHopType {
		case NextHopVirtualNetworkGateway:
			found = append(found, fmt.Sprintf("route %q sends %s to a virtual network gateway",
				r.Name, r.AddressPrefix))
		case NextHopVirtualAppliance:
			if firewallIP == "" || strings.TrimSpace(r.NextHopIPAddress) != firewallIP {
				found = append(found, fmt.Sprintf("route %q sends %s to virtual appliance %s, "+
					"which is not this environment's firewall", r.Name, r.AddressPrefix,
					r.NextHopIPAddress))
			}
		}
	}
	if len(found) > 0 {
		return Open, "the plan reaches a network that is not the internet and not this " +
			"environment: " + strings.Join(found, "; ")
	}
	return Closed, "the virtual network has no peering, the route table does not propagate " +
		"gateway routes, and every virtual appliance route points at this environment's firewall"
}

// checkServiceEndpoint is separate from the route table because a service
// endpoint's route is added to the subnet rather than written in the table, so
// a reader auditing the table does not see it.
func checkServiceEndpoint(p Plan) (Verdict, string) {
	if endpoints := p.Network.Subnet.ServiceEndpoints; len(endpoints) > 0 {
		sorted := append([]string(nil), endpoints...)
		sort.Strings(sorted)
		return Open, fmt.Sprintf("the subnet carries the service endpoints %s, and Azure adds a "+
			"route to the subnet for each one whose prefixes are the whole service tag rather "+
			"than one resource", strings.Join(sorted, ", "))
	}
	return Closed, "the subnet carries no service endpoints, so the only Azure services it " +
		"reaches privately are the ones a private endpoint names one resource at a time"
}

// checkContainerConsole can only ever return Open, and a check with one
// possible answer is usually a bug. This one is a finding.
//
// It is written as a check rather than as a comment so that it appears in the
// report, is counted in the denominator and is printed in the refusal. A path
// nothing in the plan can close is exactly the path a summary leaves out.
func checkContainerConsole(p Plan) (Verdict, string) {
	return Open, fmt.Sprintf("app %q exposes the container console and the log stream through "+
		"the Azure control plane and a generated URL under azurecontainerapps.dev, neither of "+
		"which is the environment's own address, so nothing in this plan's security group, "+
		"route table or firewall policy describes the channel. The container console page "+
		"documents no switch that turns it off", p.App.Name)
}

// outbound returns the plan's outbound rules in priority order, which is the
// order Azure evaluates them and therefore the order a person reading a failure
// has to read them in. Position in the slice means nothing to Azure and the
// checks must not let it mean anything here either.
func outbound(p Plan) []SecurityRule { return byPriority(p.Network.NSG.Outbound) }

// inbound returns the plan's inbound rules in priority order.
func inbound(p Plan) []SecurityRule { return byPriority(p.Network.NSG.Inbound) }

// byPriority sorts a copy of the rules by priority.
func byPriority(rules []SecurityRule) []SecurityRule {
	out := append([]SecurityRule(nil), rules...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// hasDenyAllOutbound reports whether the group's last word is a deny that
// covers a destination no earlier rule named.
func hasDenyAllOutbound(g NetworkSecurityGroup) bool {
	for _, rule := range byPriority(g.Outbound) {
		if rule.Access != Deny {
			continue
		}
		dest := strings.TrimSpace(rule.DestinationAddressPrefix)
		if dest != "*" && !strings.EqualFold(dest, "Internet") {
			continue
		}
		if rule.Protocol != "*" && rule.Protocol != "" {
			continue
		}
		if port := strings.TrimSpace(rule.DestinationPortRange); port != "*" && port != "" {
			continue
		}
		return true
	}
	return false
}

// defaultRouteToFirewall reports whether 0.0.0.0/0 goes to this environment's
// own firewall.
func defaultRouteToFirewall(p Plan) bool {
	fw := p.Network.Firewall
	if fw == nil || strings.TrimSpace(fw.PrivateIP) == "" {
		return false
	}
	for _, r := range p.Network.RouteTable.Routes {
		if strings.TrimSpace(r.AddressPrefix) != "0.0.0.0/0" {
			continue
		}
		if r.NextHopType != NextHopVirtualAppliance {
			continue
		}
		if strings.TrimSpace(r.NextHopIPAddress) == strings.TrimSpace(fw.PrivateIP) {
			return true
		}
	}
	return false
}

// declaredIdentities lists the identities the app declares, as the identity
// settings block names them.
func declaredIdentities(app ContainerApp) []string {
	kind := strings.ToLower(app.Identity.Type)
	var out []string
	if strings.Contains(kind, "systemassigned") {
		out = append(out, "system")
	}
	out = append(out, app.Identity.UserAssignedIdentityIDs...)
	return out
}

// broadDestination reports whether a security rule destination reaches an
// address nobody named.
//
// It fails towards broad on purpose. A value this function cannot classify is
// reported as reaching the internet, because the alternative is that a
// malformed rule silently counts as containment, and this file exists to stop a
// containment claim resting on something nobody looked at.
func broadDestination(dest string) bool {
	dest = strings.TrimSpace(dest)
	switch {
	case dest == "":
		return false
	case dest == "*", dest == "0.0.0.0/0", dest == "::/0":
		return true
	case strings.EqualFold(dest, "Internet"):
		return true
	case strings.EqualFold(dest, "AzureCloud"), strings.HasPrefix(dest, "AzureCloud."):
		return true
	case dest == AzureDNSAddress, dest == AzureDNSAddress+"/32":
		// A named platform address with a path of its own. Permitting it is
		// not permitting the internet and denying it is not containment.
		return false
	case !isCIDROrAddress(dest):
		// A service tag naming one Azure service. Whether reaching that
		// service is acceptable is the subject of another path, not this one.
		return false
	default:
		return reachesPublicIPv4(dest)
	}
}

// platformImageTag reports whether a destination is one of the artifact
// registry service tags the Container Apps outbound table requires.
func platformImageTag(dest string) bool {
	dest = strings.TrimSpace(dest)
	if strings.EqualFold(dest, "MicrosoftContainerRegistry") {
		return true
	}
	// Microsoft spells the Front Door dependency with a dot on the security
	// group page and without one on the firewall page. Both appear in real
	// configurations and both mean the same tag.
	return strings.EqualFold(dest, "AzureFrontDoor.FirstParty") ||
		strings.EqualFold(dest, "AzureFrontDoorFirstParty")
}

// registryName reports whether a firewall target name is a registry or the blob
// storage a registry serves layers from.
func registryName(fqdn string) bool {
	fqdn = strings.ToLower(strings.TrimSpace(fqdn))
	switch {
	case strings.Contains(fqdn, "azurecr.io"), strings.Contains(fqdn, "azurecr.cn"):
		return true
	case strings.Contains(fqdn, "blob.core.windows.net"):
		return true
	case strings.Contains(fqdn, "registry-1.docker.io"), strings.Contains(fqdn, "hub.docker.com"):
		return true
	default:
		return false
	}
}

// isCIDROrAddress reports whether a destination is an address literal rather
// than a service tag.
func isCIDROrAddress(dest string) bool {
	dest = strings.TrimSpace(dest)
	if _, err := netip.ParsePrefix(dest); err == nil {
		return true
	}
	_, err := netip.ParseAddr(dest)
	return err == nil
}

// privateRanges are the ranges a rule may name without reaching the internet.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
}

// reachesPublicIPv4 reports whether a CIDR or address contains anything outside
// the private ranges. It fails towards public, for the reason on
// broadDestination.
func reachesPublicIPv4(dest string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(dest))
	if err != nil {
		addr, addrErr := netip.ParseAddr(strings.TrimSpace(dest))
		if addrErr != nil || !addr.Is4() {
			return true
		}
		prefix = netip.PrefixFrom(addr, 32)
	}
	if !prefix.Addr().Is4() {
		return true
	}
	for _, private := range privateRanges {
		if private.Contains(prefix.Addr()) && private.Bits() <= prefix.Bits() {
			return false
		}
	}
	return true
}

// withinPrefix reports whether a destination sits inside a range.
func withinPrefix(dest, outer string) bool {
	out, err := netip.ParsePrefix(strings.TrimSpace(outer))
	if err != nil {
		return false
	}
	inner, err := netip.ParsePrefix(strings.TrimSpace(dest))
	if err != nil {
		addr, addrErr := netip.ParseAddr(strings.TrimSpace(dest))
		if addrErr != nil {
			return false
		}
		inner = netip.PrefixFrom(addr, addr.BitLen())
	}
	return out.Contains(inner.Addr()) && out.Bits() <= inner.Bits()
}

// ruleCoversPort reports whether a rule permits a port. Azure writes a port
// range as "a", "a-b" or "*".
func ruleCoversPort(rule SecurityRule, port int) bool {
	spec := strings.TrimSpace(rule.DestinationPortRange)
	if spec == "*" || spec == "" {
		return true
	}
	low, high, found := strings.Cut(spec, "-")
	from, err := strconv.Atoi(strings.TrimSpace(low))
	if err != nil {
		return false
	}
	to := from
	if found {
		to, err = strconv.Atoi(strings.TrimSpace(high))
		if err != nil {
			return false
		}
	}
	return from <= port && port <= to
}

// describePorts renders a rule's protocol and ports for a message.
func describePorts(rule SecurityRule) string {
	protocol := rule.Protocol
	if protocol == "" || protocol == "*" {
		protocol = "every protocol"
	}
	spec := strings.TrimSpace(rule.DestinationPortRange)
	if spec == "" || spec == "*" {
		return protocol + " on every port"
	}
	return protocol + " on " + spec
}

// joinOrNone renders a list for a message, saying nothing rather than printing
// an empty string.
func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "nothing"
	}
	return strings.Join(values, ", ")
}

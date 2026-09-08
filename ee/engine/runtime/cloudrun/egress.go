package cloudrun

import (
	"fmt"
	"net/netip"
	"strings"
)

// Verdict is what the predicate can say about one egress path.
//
// Three values rather than two, and the third is the point of the file. A
// containment claim written with a boolean has nowhere to put "I looked and
// neither the configuration nor the documentation decides this", so it rounds
// that case to whichever answer the author was hoping for. Every instrument in
// this repository that turned out to be lying had exactly that shape.
type Verdict string

const (
	// Closed means the generated configuration removes this path. It does NOT
	// mean a packet was observed failing to get out. See Caveat.
	Closed Verdict = "closed"
	// Open means the path exists in the generated configuration, either
	// because Google provides no way to remove it or because removing it would
	// stop the environment from working.
	Open Verdict = "open"
	// Unproven means the configuration does not decide it and neither does the
	// documentation. It is not a weaker Closed and it must never be counted as
	// one.
	Unproven Verdict = "unproven"
)

// Path is one distinct way out of a Cloud Run instance.
//
// Distinct means a different route, not a different phrasing of one route. Two
// entries that the same single change would close are one path, because a
// count of paths is only worth publishing if closing one of them is real work.
//
// That rule is why this list is shorter than the ECS list, and the reason is
// worth stating rather than leaving as an implication that this lane looked
// less hard. On AWS a query to a public resolver and a connection to a public
// address are separate entries, because a security group carries a rule per
// port range and a plan can close one and leave the other. Here both are
// closed by the same object, a deny all egress rule at every destination, so
// they are one path. On AWS the link local time service is its own entry
// because AWS documents an NTP service without saying where it is; here Google
// documents NTP as one of the four things the metadata server provides, so it
// is part of the metadata path rather than beside it. And ECS Exec has no
// Cloud Run equivalent for a service, so there is no entry for it. A shorter
// denominator earned by three checked differences is worth more than a longer
// one padded with entries that are the same route twice.
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
	// Check evaluates the path against a plan and returns the verdict with the
	// detail that justifies it. The detail is never empty, because a verdict
	// with no reason is a verdict nobody can check.
	Check func(Plan) (Verdict, string)
}

// Paths is every egress path out of a Cloud Run instance that this lane could
// enumerate, in a stable order.
//
// The order is the order a person should read them: the two that look like the
// internet, then the two link local answers Google gives whatever the firewall
// says, then the destinations the design itself has to allow, then the
// neighbours, then the two that carry data without the application opening a
// socket at all.
func Paths() []Path {
	return []Path{
		{
			ID:   "any-public-destination",
			Name: "a packet to any public address, including a DNS query to a resolver of the sender's choosing",
			Why: "It is the first thing every exfiltration tool tries and it needs no name lookup " +
				"and no credential. It is one path rather than two because on Cloud Run the same " +
				"single object refuses both: a deny all egress rule matches every destination and " +
				"every protocol, so a query to a public resolver on port 53 and a connection to a " +
				"public address on port 443 cannot be closed separately.",
			ClosedBy: "the vpc egress setting all-traffic on every service, so the packets are in " +
				"the network at all, plus a deny all egress firewall rule, no allow rule at a " +
				"lower priority naming a public range other than the restricted Google range, no " +
				"Cloud NAT gateway on the subnet, and no route to the internet gateway wider than " +
				"that same range",
			Check: checkPublicDestination,
		},
		{
			ID:   "public-ipv6-on-a-dual-stack-subnet",
			Name: "the same connection over IPv6, which a firewall rule written in IPv4 does not cover",
			Why: "A firewall rule names destination ranges, and a range is of one IP version. A " +
				"network audited in IPv4 and read as contained is wide open over IPv6 the moment " +
				"the subnet is dual stack. Google states that dual stack subnets 'let your Cloud " +
				"Run resources send IPv4 and IPv6 traffic to a VPC network with Direct VPC " +
				"egress', that an existing IPv4 only subnet can be changed into one, and that a " +
				"subnet's IPv6 access type may be external, which is a route to the internet " +
				"that an IPv4 reading of this network calls contained.",
			ClosedBy: "an IPv4 only subnet with no internal IPv6 range, and a deny all egress rule " +
				"for every IPv6 destination as well as every IPv4 one",
			Check: checkPublicIPv6,
		},
		{
			ID:   "the-instance-metadata-server",
			Name: "169.254.169.254, which hands an OAuth access token for the service identity to anything in the container",
			Why: "It is the highest value target in the list because it turns any request forgery " +
				"in the application into a Google credential. The container contract lists the " +
				"path that 'Generates an OAuth2 access token for the service account of this " +
				"Cloud Run resource'. The firewall documentation states that Google 'always " +
				"allows communication between a VM instance and its corresponding metadata " +
				"server at 169.254.169.254', and lists packets to and from that server under " +
				"always allowed traffic, where it says 'For VM instances, VPC firewall rules and " +
				"hierarchical firewall policies do not apply'. That sentence is scoped to VM " +
				"instances and Google publishes no equivalent about Cloud Run instances in " +
				"either direction, so the closure named below is nothing rather than a rule " +
				"somebody forgot to write. The same address also serves DHCP, NTP and DNS, which " +
				"is why the time service is part of this path rather than beside it.",
			ClosedBy: "nothing, and it is a dependency rather than only a leak: the documented way " +
				"for one Cloud Run service to call another is to fetch an ID token from this " +
				"same endpoint with the target's URL as the audience, so an environment of more " +
				"than one service needs it answering. This is the sharpest difference from the " +
				"ECS plan beside it, and it " +
				"runs the other way: a Fargate task definition may carry no task role, so " +
				"169.254.170.2 vends nothing, while Cloud Run has no way to run a service with no " +
				"identity at all. The narrowing available is a dedicated service account holding " +
				"no role bindings, which leaves the endpoint answering and the token valid",
			Check: checkMetadataServer,
		},
		{
			ID:   "the-resolvers-recursion",
			Name: "a recursive query through the network's own resolver, whose question is chosen by the sender",
			Why: "A name lookup is a data channel. The question is written by the client and it " +
				"reaches whoever is authoritative for the name, so a resolver that recurses is an " +
				"upload that no egress rule sees. Cloud DNS name resolution order ends by " +
				"following the start of authority record 'to query publicly available zones', and " +
				"the Cloud Run networking page states that when VPC egress is configured 'All DNS " +
				"queries are sent to the DNS server configured for the VPC network associated " +
				"with your VPC network egress setup'. This is the path a containment argument " +
				"carried over from Kubernetes gets wrong, because there a NetworkPolicy closes " +
				"the resolver along with everything else.",
			ClosedBy: "an outbound Cloud DNS server policy whose alternative name servers are all " +
				"inside this environment's subnet, which Google documents as sending ALL queries " +
				"there. A response policy cannot do it: a response policy rule answers a name " +
				"differently and Google documents no block action and no rule matching every " +
				"name, and only one response policy may be attached to a network",
			Check: checkResolverRecursion,
		},
		{
			ID:   "the-restricted-google-api-range",
			Name: "199.36.153.4/30, the one public range the plan has to allow once an environment has more than one service",
			Why: "Cloud Run services reach each other over their own https URLs, and the Direct " +
				"VPC page states that services and jobs do not support direct VPC ingress, so an " +
				"instance has no address of its own inside the network to be called at. Google's " +
				"own instructions for " +
				"Cloud Run under VPC Service Controls resolve both googleapis.com and run.app to " +
				"this range. Allowing it is therefore the same act as allowing every Google API " +
				"the metadata server's token can sign for, and a token that can write to Cloud " +
				"Storage is an upload.",
			ClosedBy: "an environment of exactly one service, which needs no allow rule at all. " +
				"That is possible here and is not possible on Fargate, where the image pull and " +
				"the log push force the equivalent allowance open. For an environment of more " +
				"than one, this plan does not close it, and a candidate exists that this lane " +
				"did not build: Google lists an internal Application Load Balancer both as a " +
				"way to reach a Cloud Run service 'through an internal IP address in your VPC " +
				"network' and as an allowed source under the internal ingress setting, so one " +
				"load balancer per service would put a service to service call on a private " +
				"address instead of into this range. What this lane checked is that the " +
				"mechanism is documented, not that the objects it needs can all be scoped to " +
				"one environment, so it is named here rather than generated and asserted. A VPC " +
				"Service Controls perimeter only narrows which Google services answer, and it " +
				"is an organization level resource this runtime cannot create",
			Check: checkRestrictedGoogleAPIRange,
		},
		{
			ID:   "a-neighbouring-environments-service",
			Name: "another environment's Cloud Run service, reached through exactly the same address as this environment's own services",
			Why: "Every environment in this design shares a network, and once the restricted range " +
				"is allowed, one environment's call to its own worker and its call to a " +
				"neighbour's web service are the same packets to the same address. Nothing at the " +
				"network layer separates them. What separates them is IAM, and the Kubernetes " +
				"suite has a behaviour for exactly this case because it is the one that matters " +
				"most: the thing on the other side is a twin of the same production system.",
			ClosedBy: "no allUsers invoker binding on any service, an ingress setting other than " +
				"all on every service, and an environment identity holding an invoker binding on " +
				"this environment's services and no others. Every environment is generated by the " +
				"same function, so a property of this plan is a property of the neighbour",
			Check: checkNeighbouringService,
		},
		{
			ID:   "a-neighbouring-environments-address-in-a-shared-subnet",
			Name: "a private address belonging to another environment, allowed by a rule that names a range wider than this environment's subnet",
			Why: "Direct VPC egress gives every instance an address out of the subnet, and Cloud " +
				"Run reserves them in blocks of sixteen, so environments that share a subnet are " +
				"interleaved inside one range. A rule written once against a private range that " +
				"was convenient is how one environment reaches a peer environment's datastore " +
				"while every rule in it still reads as correct.",
			ClosedBy: "one subnet per environment and allow rules naming only that subnet's range, " +
				"so no rule in the plan describes an address the environment does not own",
			Check: checkNeighbouringAddress,
		},
		{
			ID:   "a-route-out-of-the-network",
			Name: "the corporate network, through peering, a VPN tunnel, an interconnect attachment or a connectivity hub",
			Why: "A containment check written as no internet gateway and no NAT passes a network " +
				"peered with the production network. That is worse than reaching the internet, " +
				"because the thing on the other side is the database this environment is a twin " +
				"of, and the traffic never touches a public address on the way.",
			ClosedBy: "a network carrying no route whose next hop is a peering connection, a VPN " +
				"tunnel, an interconnect attachment or a connectivity hub",
			Check: checkRouteOutOfTheNetwork,
		},
		{
			ID:   "the-platforms-own-capture-of-stdout-and-stderr",
			Name: "everything the application prints, which leaves the environment with no socket, no rule and no route",
			Why: "This is the path that makes the whole product's claim narrower than it sounds. " +
				"A twin holds masked production data, and a single log line carrying a row " +
				"leaves the environment into the project's logs, where the audience is everyone " +
				"with log access rather than everyone with environment access. Google states " +
				"that logs are picked up automatically from standard output, standard error, " +
				"any file under /var/log and syslog, so it is not only the streams: a file " +
				"written in the container is published as well. It is a route people use on " +
				"purpose, to get data out of a sandbox, and it is also the accident that " +
				"happens on the first debugging afternoon.",
			ClosedBy: "nothing in the Cloud Run configuration. The platform captures the streams " +
				"and no field in this plan turns that off. A Cloud Logging exclusion filter is a " +
				"control on the destination rather than on the path, and it is not this " +
				"runtime's to create",
			Check: checkPlatformLogCapture,
		},
		{
			ID:   "a-declared-cloud-storage-or-nfs-volume",
			Name: "a bucket or a file share mounted into the container, which is a write channel the application never opens a socket for",
			Why: "Cloud Run supports mounting a Cloud Storage bucket and an NFS share as volumes, " +
				"and a mounted bucket is the shortest path out of an environment there is: a " +
				"file copy. It belongs in this list because it is a supported field somebody " +
				"adds for a legitimate reason and it survives every egress rule in the plan.",
			ClosedBy: "a generated plan that declares no Cloud Storage and no NFS volume on any " +
				"service",
			Check: checkVolumeMounts,
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
// The difference between what this package proves and what it would need a
// Google Cloud project to prove is the whole honesty of the runtime. A closed
// verdict is a statement about a set of resource definitions. Whether Google
// enforces them is a statement about a project, and nothing in this repository
// has ever observed it.
const Caveat = "Every verdict here is computed from the configuration this runtime would " +
	"generate. None of it has been applied to a Google Cloud project and no packet has been " +
	"observed failing to leave an instance. A closed verdict means the generated " +
	"configuration removes the path, not that Google was seen enforcing it."

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
// It is false for every plan this package generates, and that is not a bug to
// be fixed later. Three of the paths cannot be closed on Cloud Run at all and
// one cannot be decided without a project, so a true here would mean the
// enumeration had been shortened rather than that the containment had
// improved.
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
	fmt.Fprintf(&b, "%d of %d egress paths out of a Cloud Run instance are closed by the "+
		"generated configuration (%d open, %d unproven).\n", r.Closed, r.Total, r.Open, r.Unproven)
	for _, v := range r.Verdicts {
		fmt.Fprintf(&b, "  %-9s %s\n             %s\n", v.Verdict, v.Path.ID, v.Detail)
	}
	b.WriteString("\n" + Caveat + "\n")
	return b.String()
}

// checkPublicDestination requires every one of the five closures, not whichever
// one happens to be present.
//
// Five rather than any one of them, and that choice needs defending because
// several are sufficient alone. The predicate is a statement about the
// configuration this runtime generates, not about the minimum configuration
// that would happen to work: a plan that has lost one of its closures is no
// longer the plan the proof covers, and reporting it closed would mean the
// check cannot fail when the generator regresses. A check that cannot say no
// is worse than no check.
func checkPublicDestination(p Plan) (Verdict, string) {
	var missing []string

	for _, s := range p.Services {
		if s.VPCEgress != EgressAllTraffic {
			missing = append(missing, fmt.Sprintf(
				"service %q has the vpc egress setting %q, and on anything but %q Google routes "+
					"requests to public destinations straight to the internet, where no rule in "+
					"this network sees them",
				s.Name, s.VPCEgress, EgressAllTraffic))
		}
		if s.Network == "" || s.Subnet == "" {
			missing = append(missing, fmt.Sprintf(
				"service %q names network %q and subnet %q, and without both there is no network "+
					"in the path at all", s.Name, s.Network, s.Subnet))
		}
	}

	deny := denyAllEgressRule(p, "0.0.0.0/0")
	if deny == nil {
		missing = append(missing, "no egress firewall rule denies every IPv4 destination, and the "+
			"implied egress rule lets an instance send traffic to any destination")
	}

	if deny != nil {
		for _, rule := range p.Network.FirewallRules {
			if !isEgressAllow(rule) || rule.Priority >= deny.Priority {
				continue
			}
			for _, dest := range rule.DestinationRanges {
				if isPublicRange(dest) && !withinRestrictedVIP(dest) {
					missing = append(missing, fmt.Sprintf(
						"egress rule %q allows %s at priority %d, ahead of the deny rule at %d",
						rule.Name, dest, rule.Priority, deny.Priority))
				}
			}
		}
	}

	for _, r := range p.Network.Routes {
		if r.NextHopKind != NextHopInternetGateway {
			continue
		}
		if !withinRestrictedVIP(r.DestRange) {
			missing = append(missing, fmt.Sprintf(
				"route %q sends %s to the internet gateway, which is wider than the restricted "+
					"Google range %s", r.Name, r.DestRange, RestrictedVIPRange))
		}
	}

	for _, nat := range p.Network.NATGateways {
		for _, s := range nat.Subnets {
			if s == p.Network.Subnet.Name {
				missing = append(missing, fmt.Sprintf(
					"Cloud NAT gateway %q translates for subnet %q, which gives an instance with "+
						"no external address a way to the internet", nat.Name, s))
			}
		}
	}

	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, fmt.Sprintf(
		"every service sends all traffic to the network, egress rule %q denies every IPv4 "+
			"destination at priority %d, no allow rule ahead of it names a public range other "+
			"than %s, no Cloud NAT gateway serves subnet %q and no internet gateway route is "+
			"wider than that range",
		deny.Name, deny.Priority, RestrictedVIPRange, p.Network.Subnet.Name)
}

// checkPublicIPv6 is separate from the IPv4 check because a firewall rule's
// destination ranges are of one IP version, so the two are closed by different
// objects and one of them regresses on its own.
func checkPublicIPv6(p Plan) (Verdict, string) {
	var missing []string
	if p.Network.Subnet.StackType != StackTypeIPv4Only {
		missing = append(missing, fmt.Sprintf(
			"subnet %q has stack type %q rather than %q",
			p.Network.Subnet.Name, p.Network.Subnet.StackType, StackTypeIPv4Only))
	}
	if p.Network.Subnet.IPv6Range != "" {
		missing = append(missing, fmt.Sprintf(
			"subnet %q carries the internal IPv6 range %s",
			p.Network.Subnet.Name, p.Network.Subnet.IPv6Range))
	}
	if denyAllEgressRule(p, "::/0") == nil {
		missing = append(missing, "no egress firewall rule denies every IPv6 destination, so a "+
			"subnet that becomes dual stack later is contained in IPv4 only")
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, fmt.Sprintf(
		"subnet %q is %s with no internal IPv6 range, and an egress rule denies every IPv6 "+
			"destination as well, so the IPv4 reading of this network is the whole reading",
		p.Network.Subnet.Name, StackTypeIPv4Only)
}

// checkMetadataServer always answers Open, and the detail is where the work is.
//
// Always, because no field in this plan and no field in the Cloud Run API
// removes the endpoint. The check still reads the plan, because the difference
// between an environment running as the project's Compute Engine default
// service account and one running as a dedicated account with no bindings is
// the difference between a token that can act on the whole project and a token
// that can act on nothing, and a report that said only "open" would hide it.
func checkMetadataServer(p Plan) (Verdict, string) {
	if p.Identity.Email == "" {
		return Open, "no service account is named, so Cloud Run runs this environment as the " +
			"project's Compute Engine default service account, and the metadata server vends " +
			"that identity's access token to anything inside the container"
	}
	if isDefaultComputeAccount(p.Identity.Email) {
		return Open, fmt.Sprintf(
			"the environment runs as %s, the project's Compute Engine default service account, "+
				"and the metadata server vends its access token to anything inside the container",
			p.Identity.Email)
	}
	roles := make([]string, 0, len(p.Identity.Bindings))
	for _, b := range p.Identity.Bindings {
		roles = append(roles, b.Role+" on "+b.Resource)
	}
	held := "no role bindings at all"
	if len(roles) > 0 {
		held = strings.Join(roles, " and ")
	}
	return Open, fmt.Sprintf(
		"the endpoint answers and cannot be turned off, and it vends an access token for %s, "+
			"which holds %s. Nothing in this plan closes it: Google documents no way to make the "+
			"endpoint unreachable from a container and Cloud Run cannot run a service with no "+
			"identity, so the token is narrowed rather than absent", p.Identity.Email, held)
}

// checkResolverRecursion is the path this lane was warned about by name, and it
// is the one that cannot reach Closed.
//
// The best verdict available is Unproven, and the reason is a gap in Google's
// documentation rather than a gap in the plan. The mechanism that sends every
// query to a name server the operator chooses is an outbound server policy, and
// Google describes what it does like this: "When a VM uses its metadata server
// 169.254.169.254 as its name server, and when you have specified alternative
// name servers for a VPC network, Cloud DNS sends all queries to the
// alternative name servers". The precondition in that sentence is a VM. Google
// does not document what resolver address a Cloud Run instance uses, and the
// Cloud Run page says only that queries go to "the DNS server configured for
// the VPC network associated with your VPC network egress setup", which is
// consistent with the policy applying and does not say that it does.
//
// So this returns Unproven when the policy is right and Open when it is not,
// and it never returns Closed. That asymmetry is deliberate: the check can
// still fail, which is what makes it a check, and it cannot pass, which is what
// keeps the number honest.
func checkResolverRecursion(p Plan) (Verdict, string) {
	policy := p.Network.DNS.OutboundServerPolicy
	if policy == nil {
		return Open, "the network has no outbound server policy, so a name that matches no rule " +
			"is resolved by following the start of authority record out to the public zones, and " +
			"the question is written by whatever is running in the container"
	}
	if policy.Network != p.Network.Name {
		return Open, fmt.Sprintf(
			"outbound server policy %q applies to network %q rather than %q, so it filters "+
				"nothing here while reading as configured",
			policy.Name, policy.Network, p.Network.Name)
	}
	if len(policy.AlternativeNameServers) == 0 {
		return Open, fmt.Sprintf(
			"outbound server policy %q names no alternative name server, so resolution falls "+
				"through to the public zones", policy.Name)
	}
	for _, ns := range policy.AlternativeNameServers {
		addr, err := netip.ParseAddr(ns)
		if err != nil {
			return Open, fmt.Sprintf(
				"alternative name server %q is not an address this check can place, so it cannot "+
					"be shown to be inside the environment", ns)
		}
		if !addrInRange(addr, p.Network.Subnet.IPv4Range) {
			return Open, fmt.Sprintf(
				"alternative name server %s is outside subnet %s, so Cloud DNS classifies it as "+
					"reachable on the internet and sends every query there, which makes the "+
					"policy the exfiltration channel rather than the fix",
				ns, p.Network.Subnet.IPv4Range)
		}
	}
	if !policy.PrivateRouting {
		return Open, fmt.Sprintf(
			"outbound server policy %q does not force private routing, so the classification of "+
				"its name servers is inferred from their addresses and a privately reused "+
				"external address would be queried over the internet", policy.Name)
	}
	return Unproven, fmt.Sprintf(
		"outbound server policy %q sends every query to %s inside subnet %s, which Google "+
			"documents as receiving ALL queries. That sentence states its precondition as a VM "+
			"using 169.254.169.254 as its name server, and Google does not document what "+
			"resolver a Cloud Run instance uses, so the configuration does not settle it. One "+
			"query from one running instance settles it, and that needs an account",
		policy.Name, strings.Join(policy.AlternativeNameServers, ", "), p.Network.Subnet.IPv4Range)
}

// checkRestrictedGoogleAPIRange is the path whose verdict depends on the shape
// of the environment rather than on a field somebody set wrong.
func checkRestrictedGoogleAPIRange(p Plan) (Verdict, string) {
	var allowing []string
	for _, rule := range p.Network.FirewallRules {
		if !isEgressAllow(rule) {
			continue
		}
		for _, dest := range rule.DestinationRanges {
			if withinRestrictedVIP(dest) {
				allowing = append(allowing, fmt.Sprintf("%s at priority %d", rule.Name, rule.Priority))
			}
		}
	}
	if len(allowing) == 0 {
		return Closed, fmt.Sprintf(
			"no rule allows %s, so the Google APIs the metadata server's token could sign for "+
				"are unreachable. An environment of %d service needs no service to service call, "+
				"the image was imported at deploy time rather than pulled at instance start, and "+
				"the platform captures the log streams, so nothing else in the environment needs "+
				"this range either", RestrictedVIPRange, len(p.Services))
	}
	perimeter := "no VPC Service Controls perimeter is named in this plan, and a perimeter is an " +
		"organization level resource this runtime cannot create for itself"
	if p.Perimeter != nil {
		perimeter = fmt.Sprintf(
			"perimeter %q narrows which Google services answer, and narrowing a destination is "+
				"not closing a channel", p.Perimeter.Name)
	}
	return Open, fmt.Sprintf(
		"%s allows %s, which this environment of %d services needs because it reaches its own "+
			"second service through that service's https URL and both googleapis.com and "+
			"run.app resolve into that range. An internal Application Load Balancer per service "+
			"is the documented candidate for closing it and this plan does not generate one. %s",
		strings.Join(allowing, " and "), RestrictedVIPRange, len(p.Services), perimeter)
}

// checkNeighbouringService reads this plan and reports about the neighbour,
// which is sound only because every environment is generated by Generate. That
// is the assumption the check rests on and it is written down here rather than
// left in somebody's head.
func checkNeighbouringService(p Plan) (Verdict, string) {
	var missing []string
	own := map[string]bool{}
	for _, s := range p.Services {
		own[serviceResource(p, s.Name)] = true
	}
	// The identity has to belong to this environment and not to the
	// installation, and this is the check that says so.
	//
	// It is here rather than on the metadata path because it decides a
	// different question. One service account shared by every environment
	// leaves the metadata path exactly as open as it already is, and it
	// destroys this one: an invoker binding granted so that a web service can
	// call its own worker is then held by the principal every other
	// environment also runs as, so every environment can call every other
	// environment's services and each binding reads as correct on its own.
	if local, _, ok := strings.Cut(p.Identity.Email, "@"); !ok || local != p.Network.Subnet.Name {
		missing = append(missing, fmt.Sprintf(
			"the environment runs as %q, which is not this environment's own identity %q, so "+
				"every environment shares one principal and a binding granted to one is held "+
				"by all of them", p.Identity.Email, p.Network.Subnet.Name))
	}
	for _, s := range p.Services {
		if s.AllowUnauthenticated {
			missing = append(missing, fmt.Sprintf(
				"service %q is invokable by allUsers, so every environment on this network may "+
					"call it and so may the internet", s.Name))
		}
		if s.Ingress == IngressAll || s.Ingress == "" {
			missing = append(missing, fmt.Sprintf(
				"service %q has ingress %q, which the ingress page describes as allowing any "+
					"resource on the internet to reach it", s.Name, s.Ingress))
		}
	}
	for _, b := range p.Identity.Bindings {
		if !strings.Contains(b.Role, "invoker") {
			continue
		}
		if !own[b.Resource] {
			missing = append(missing, fmt.Sprintf(
				"the environment identity holds %s on %s, which is not one of its own services",
				b.Role, b.Resource))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, fmt.Sprintf(
		"no service accepts unauthenticated callers, every service has an ingress setting other "+
			"than %q, and %s, which is this environment's own identity and no other "+
			"environment's, holds invoker bindings on its own %d services and on nothing else. "+
			"The separation is IAM rather than the network, because at the network layer a call "+
			"to a neighbour and a call to this environment's own worker are the same packets to "+
			"the same address", IngressAll, p.Identity.Email, len(p.Services))
}

// checkNeighbouringAddress is about destinations inside the network, where the
// closure is the width of a rule rather than the absence of a route.
func checkNeighbouringAddress(p Plan) (Verdict, string) {
	var missing []string
	for _, rule := range p.Network.FirewallRules {
		if !isEgressAllow(rule) {
			continue
		}
		for _, dest := range rule.DestinationRanges {
			if isPublicRange(dest) {
				continue
			}
			if !rangeWithin(dest, p.Network.Subnet.IPv4Range) {
				missing = append(missing, fmt.Sprintf(
					"egress rule %q allows the private range %s, which is wider than this "+
						"environment's subnet %s and therefore describes addresses the "+
						"environment does not own", rule.Name, dest, p.Network.Subnet.IPv4Range))
			}
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, fmt.Sprintf(
		"every private destination this plan allows is inside subnet %s, which holds this "+
			"environment's instances and nothing else",
		p.Network.Subnet.IPv4Range)
}

// checkRouteOutOfTheNetwork reads next hop kinds, because the kind is the part
// that decides whether the packet leaves for a network somebody else runs.
func checkRouteOutOfTheNetwork(p Plan) (Verdict, string) {
	var found []string
	for _, r := range p.Network.Routes {
		switch r.NextHopKind {
		case NextHopPeering, NextHopVPNTunnel, NextHopInterconnect, NextHopNCCHub:
			found = append(found, fmt.Sprintf("route %q sends %s to %s", r.Name, r.DestRange, r.NextHopKind))
		}
	}
	if len(found) > 0 {
		return Open, strings.Join(found, "; ")
	}
	return Closed, "the network carries no route to a peering connection, a VPN tunnel, an " +
		"interconnect attachment or a connectivity hub, so there is no path to a network this " +
		"environment is a twin of"
}

// checkPlatformLogCapture always answers Open, and it is in this list because
// leaving it out would be the dishonest choice rather than the tidy one.
func checkPlatformLogCapture(p Plan) (Verdict, string) {
	names := make([]string, 0, len(p.Services))
	for _, s := range p.Services {
		names = append(names, s.Name)
	}
	return Open, fmt.Sprintf(
		"the platform captures the standard output, the standard error, anything under /var/log "+
			"and syslog from %s, and sends it to the project's logs. No field in this plan turns "+
			"that off, no rule in this network is on the path, and a masked row printed once has "+
			"left the environment", strings.Join(names, ", "))
}

// checkVolumeMounts is closed by construction, which is exactly why it needs a
// mutation aimed at it: a check over a field nothing ever sets is a check that
// has never been shown to fail.
func checkVolumeMounts(p Plan) (Verdict, string) {
	var found []string
	for _, s := range p.Services {
		for _, v := range s.Volumes {
			if v.Type == VolumeCloudStorage || v.Type == VolumeNFS {
				found = append(found, fmt.Sprintf(
					"service %q mounts the %s volume %q at %s", s.Name, v.Type, v.Name, v.Target))
			}
		}
	}
	if len(found) > 0 {
		return Open, strings.Join(found, "; ")
	}
	return Closed, "no service declares a Cloud Storage or NFS volume, so there is no destination " +
		"outside the environment that a file copy reaches"
}

// The values Cloud Run and Compute Engine accept in the fields this package
// reads. They are constants because a check comparing against a literal typed
// twice is a check that passes on the day one of them is misspelled.
const (
	// EgressAllTraffic is the vpc egress setting that puts every packet in the
	// network.
	EgressAllTraffic = "all-traffic"
	// EgressPrivateRangesOnly is the default, under which requests to public
	// destinations are routed straight to the internet.
	EgressPrivateRangesOnly = "private-ranges-only"
	// IngressAll is the default ingress setting, which the ingress page
	// describes as allowing any resource on the internet to reach the service.
	IngressAll = "all"
	// IngressInternal restricts ingress to the network and to the perimeter.
	IngressInternal = "internal"
	// StackTypeIPv4Only is a subnet with no IPv6.
	StackTypeIPv4Only = "IPV4_ONLY"
	// StackTypeDual is a subnet that also carries IPv6.
	StackTypeDual = "IPV4_IPV6"
	// VolumeCloudStorage and VolumeNFS are the two volume types that reach
	// outside the environment.
	VolumeCloudStorage = "cloud-storage"
	VolumeNFS          = "nfs"
	// RoleInvoker is the role that lets one identity call a Cloud Run service.
	RoleInvoker = "roles/run.invoker"
)

// Next hop kinds, which are the part of a route that decides where a packet
// goes when it leaves.
const (
	NextHopInternetGateway = "default-internet-gateway"
	NextHopPeering         = "peering"
	NextHopVPNTunnel       = "vpn-tunnel"
	NextHopInterconnect    = "interconnect-attachment"
	NextHopNCCHub          = "ncc-hub"
)

// denyAllEgressRule finds the lowest priority egress deny rule covering the
// given every address range, or nil.
func denyAllEgressRule(p Plan, everything string) *FirewallRule {
	var best *FirewallRule
	for i, rule := range p.Network.FirewallRules {
		if rule.Direction != "EGRESS" || rule.Action != "deny" {
			continue
		}
		if !coversEverything(rule, everything) {
			continue
		}
		if best == nil || rule.Priority < best.Priority {
			best = &p.Network.FirewallRules[i]
		}
	}
	return best
}

// coversEverything reports whether a rule names the given every address range
// for every protocol.
func coversEverything(rule FirewallRule, everything string) bool {
	named := false
	for _, dest := range rule.DestinationRanges {
		if dest == everything {
			named = true
		}
	}
	if !named {
		return false
	}
	for _, proto := range rule.Protocols {
		if proto == "all" {
			return true
		}
	}
	return false
}

// isEgressAllow reports whether a rule permits outbound traffic.
func isEgressAllow(rule FirewallRule) bool {
	return rule.Direction == "EGRESS" && rule.Action == "allow"
}

// isPublicRange reports whether a destination range is outside every private
// range.
//
// Containment rather than overlap, and the difference is the whole
// correctness of the function. 0.0.0.0/0 OVERLAPS every private range, so an
// overlap test calls the widest possible destination private and reports the
// most open plan there is as contained. A range is internal only when the
// whole of it is inside one internal range.
//
// An unparseable range is treated as public, because a check that shrugs at
// input it cannot read is a check that says yes when it means "I could not
// look".
func isPublicRange(cidr string) bool {
	if _, err := netip.ParsePrefix(cidr); err != nil {
		return true
	}
	for _, internal := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC 1918
		"100.64.0.0/10",  // RFC 6598, which Google lists beside RFC 1918
		"127.0.0.0/8",    // loopback
		"169.254.0.0/16", // link local, including the metadata server
		"fc00::/7",       // unique local addresses
		"fe80::/10",      // link local IPv6
		"::1/128",        // IPv6 loopback
	} {
		if rangeWithin(cidr, internal) {
			return false
		}
	}
	return true
}

// withinRestrictedVIP reports whether a range is inside the restricted Google
// range.
func withinRestrictedVIP(cidr string) bool { return rangeWithin(cidr, RestrictedVIPRange) }

// rangeWithin reports whether inner is contained in outer.
func rangeWithin(inner, outer string) bool {
	in, err := netip.ParsePrefix(inner)
	if err != nil {
		return false
	}
	out, err := netip.ParsePrefix(outer)
	if err != nil {
		return false
	}
	if in.Bits() < out.Bits() {
		return false
	}
	return out.Contains(in.Addr())
}

// addrInRange reports whether an address is inside a range.
func addrInRange(addr netip.Addr, cidr string) bool {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return false
	}
	return prefix.Contains(addr)
}

// isDefaultComputeAccount reports whether an address is the project's Compute
// Engine default service account, which is what Cloud Run uses when a
// deployment names none and which carries whatever that account was granted
// when the project was created.
func isDefaultComputeAccount(email string) bool {
	return strings.HasSuffix(email, "-compute@developer.gserviceaccount.com")
}

// serviceResource is the resource name an invoker binding is held on.
func serviceResource(p Plan, service string) string {
	return fmt.Sprintf("projects/%s/locations/%s/services/%s", p.Project, p.Region, service)
}

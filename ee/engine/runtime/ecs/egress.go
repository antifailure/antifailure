// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.

package ecs

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Verdict is what the predicate can say about one egress path.
//
// Three values rather than two, and the third is the point of the file. A
// containment claim written with a boolean has nowhere to put "I looked and the
// configuration does not decide this", so it rounds that case to whichever
// answer the author was hoping for. Every instrument in this repository that
// turned out to be lying had exactly that shape: a check that could not say "I
// could not look" reported a pass instead.
type Verdict string

const (
	// Closed means the generated configuration removes this path. It does NOT
	// mean a packet was observed failing to get out. See Report.Caveat.
	Closed Verdict = "closed"
	// Open means the path exists in the generated configuration, either
	// because AWS provides no way to remove it or because removing it would
	// stop the environment from starting.
	Open Verdict = "open"
	// Unproven means the configuration does not decide it and neither does the
	// documentation. It is not a weaker Closed and it must never be counted as
	// one.
	Unproven Verdict = "unproven"
)

// Path is one distinct way out of an ECS task on Fargate.
//
// Distinct means a different route, not a different phrasing of one route. Two
// entries that the same single change would close are one path, because a
// count of paths is only worth publishing if closing one of them is real work.
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

// Paths is every egress path out of an ECS task on Fargate that this lane
// could enumerate, in a stable order.
//
// The order is the order a person should read them: the three that look like
// the internet, then the resolver, then the two link local endpoints, then the
// routes the design itself has to open, then the ones somebody turns on.
//
// Every AWS behaviour asserted below was read from AWS documentation on
// 2026-09-07 and the page is named in the text. None of it is from memory,
// because the whole lane is a containment claim and a containment claim built
// on a recollection of a cloud provider's defaults is worth nothing.
func Paths() []Path {
	return []Path{
		{
			ID:   "public-ipv4-through-a-gateway",
			Name: "a TCP connection to a public IPv4 address, with no name lookup at all",
			Why: "It is the obvious answer to interception by DNS, and it is what every " +
				"exfiltration tool does first. It needs no resolver and it leaves no query.",
			ClosedBy: "no route to an internet gateway or a NAT gateway, no public address " +
				"on the task ENI, and no security group egress rule naming a public range",
			Check: checkPublicIPv4,
		},
		{
			ID:   "public-ipv6-through-an-egress-only-gateway",
			Name: "the same connection over IPv6, which IPv4 security group rules do not cover",
			Why: "A security group carries IPv4 and IPv6 rules separately, so a group audited " +
				"in IPv4 and read as contained can be wide open over IPv6 through an egress " +
				"only internet gateway, which exists precisely to give private subnets " +
				"outbound IPv6.",
			ClosedBy: "no IPv6 range on the VPC or its subnets, no egress only gateway route, " +
				"and no IPv6 egress rule",
			Check: checkPublicIPv6,
		},
		{
			ID:   "public-resolver-over-udp",
			Name: "a DNS query straight to a public resolver, whose payload is whatever the client puts in it",
			Why: "A name lookup is a data channel. The question in a DNS query is chosen by " +
				"the sender, so a resolver that answers recursively is an unlogged upload " +
				"with a 1024 packet per second budget, and a firewall that allows port 53 " +
				"because it is only DNS is allowing exactly that.",
			ClosedBy: "no route out and no egress rule reaching port 53 on a public range",
			Check:    checkPublicResolver,
		},
		{
			ID:   "the-amazon-provided-resolver",
			Name: "the same query to the Route 53 Resolver at 169.254.169.253 or the VPC range plus two",
			Why: "This is the sharpest one on AWS and it is the one a Kubernetes intuition " +
				"gets wrong. The Amazon DNS server resolves public names recursively for " +
				"anything in the VPC, and the VPC user guide states that you cannot filter " +
				"traffic to or from it using network ACLs or security groups. So the whole " +
				"security group is irrelevant to this path.",
			ClosedBy: "a Route 53 Resolver DNS Firewall rule group associated with this VPC " +
				"whose last rule blocks every domain and which does not fail open, or " +
				"enableDnsSupport turned off on the VPC",
			Check: checkAmazonResolver,
		},
		{
			ID:   "the-ec2-instance-metadata-service",
			Name: "169.254.169.254, which on an EC2 container instance hands out the instance role's credentials",
			Why: "It is not on the internet and it is the highest value target in the list, " +
				"because it turns any request forgery in the application into cloud " +
				"credentials. The ECS documentation says containers on EC2 container " +
				"instances can reach instance metadata and IAM role credentials, and names " +
				"the defence as ECS_AWSVPC_BLOCK_IMDS, which is an ECS container agent " +
				"variable set on the instance.",
			ClosedBy: "nothing a task definition can say. The Fargate launch type removes the " +
				"documented credential source, because EC2 instance profiles are not " +
				"available to containers in Fargate tasks, but AWS does not document " +
				"whether the address answers, so the configuration does not settle it",
			Check: checkInstanceMetadata,
		},
		{
			ID:   "the-task-role-credentials-endpoint",
			Name: "169.254.170.2, which vends the task role's credentials to anything in the container",
			Why: "It is the Fargate equivalent of the instance metadata service and it is " +
				"always there. Any code in the container reads AWS_CONTAINER_CREDENTIALS_" +
				"RELATIVE_URI and gets a signing key for whatever the task role permits.",
			ClosedBy: "no taskRoleArn in the task definition, so the endpoint has nothing to " +
				"vend. The endpoint itself cannot be turned off",
			Check: checkTaskRoleCredentials,
		},
		{
			ID:   "the-task-metadata-endpoint",
			Name: "169.254.170.2/v4, on by default and with no documented way to turn it off",
			Why: "It discloses the task ARN, the cluster name, the ENI address and container " +
				"statistics to anything in the container. It is not a route to the internet " +
				"and it vends no credentials once the task role is gone, but it is an " +
				"endpoint the environment did not ask for and cannot remove.",
			ClosedBy: "nothing. The ECS documentation states the task metadata endpoint is on " +
				"by default for every Fargate task on platform version 1.4.0 or later",
			Check: checkTaskMetadata,
		},
		{
			ID:   "the-interface-endpoints-the-image-pull-needs",
			Name: "the ECR and CloudWatch Logs interface endpoints, which the task must reach in order to start",
			Why: "On Fargate platform version 1.4.0 the ECR login, the image pull and the log " +
				"push all flow over the TASK ENI, under this security group, and the " +
				"documentation says plainly that a Fargate task must have a route to the " +
				"registry to pull an image. There is no equivalent of a kubelet pulling the " +
				"image outside the pod's policy, so a task whose security group denies " +
				"everything never starts.",
			ClosedBy: "nothing, without giving up the ability to run an environment at all. " +
				"It is narrowed by an endpoint policy per endpoint and by egress rules " +
				"naming the endpoints' own security group rather than an address range",
			Check: checkInterfaceEndpoints,
		},
		{
			ID:   "the-s3-gateway-endpoint-the-layers-come-from",
			Name: "the S3 gateway endpoint, reached through a route table prefix list rather than an ENI",
			Why: "ECR stores image layers in S3, so the pull needs S3 as well as ECR. A " +
				"gateway endpoint is a route, and an unrestricted one is a path to every " +
				"bucket in the region, including the ones that accept anonymous writes.",
			ClosedBy: "nothing, for the same reason as the interface endpoints. It is narrowed " +
				"by an endpoint policy",
			Check: checkS3Gateway,
		},
		{
			ID:   "the-ecs-exec-channel",
			Name: "ECS Exec, which is an interactive shell inbound and an unmetered channel outbound",
			Why: "It is the one path on this list that a person opens deliberately, while " +
				"debugging, and then leaves open. It runs over AWS Systems Manager rather " +
				"than over anything the egress policy describes.",
			ClosedBy: "enableExecuteCommand false and no ssmmessages endpoint in the plan",
			Check:    checkExecuteCommand,
		},
		{
			ID:   "a-peering-transit-or-private-gateway-route",
			Name: "a route to the corporate network, which never touches the internet and is not covered by an internet check",
			Why: "A containment check written as no internet gateway and no NAT gateway passes " +
				"a VPC that is peered with the production VPC. That is worse than reaching " +
				"the internet, because the thing on the other side is the database this " +
				"environment is a twin of.",
			ClosedBy: "a route table holding only the local route and the gateway endpoint " +
				"prefix lists",
			Check: checkPrivateGatewayRoutes,
		},
		{
			ID:   "a-neighbouring-environment",
			Name: "another environment's tasks in the same VPC",
			Why: "Every environment in this design shares a VPC, and a security group written " +
				"once and reused is how an environment reaches a peer environment's " +
				"datastore while every rule in it still reads as correct. The Kubernetes " +
				"suite has a behaviour for exactly this and it is the slowest and most " +
				"important one it has.",
			ClosedBy: "one security group per environment, egress rules naming only that group " +
				"or an endpoint's group, and no rule naming a range wider than this " +
				"environment's own subnets",
			Check: checkNeighbouringEnvironment,
		},
		{
			ID:   "the-local-amazon-time-sync-service",
			Name: "the link local NTP service, which shares the link local packet budget with the resolver and metadata",
			Why: "The VPC DNS quota page counts Route 53 Resolver queries, instance metadata " +
				"requests and Amazon Time Service NTP requests against one 1024 packet per " +
				"second link local budget, which is documentary evidence that a link local " +
				"NTP service is reachable from inside the network.",
			ClosedBy: "not established. The EC2 page for the local Amazon Time Sync Service " +
				"does not state the link local address and does not say whether security " +
				"groups filter it, so this lane could not settle the path and does not " +
				"assert either answer",
			Check: checkTimeSync,
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
	// total, and burying an unproven inside a numerator is the exact arithmetic
	// this repository already caught its own fidelity instrument doing.
	Closed   int
	Open     int
	Unproven int
	// Total is len(Verdicts).
	Total int
}

// Caveat is the sentence that has to travel with every number this package
// produces, and it is a constant so that it cannot be paraphrased away.
//
// It is here because the difference between what this package proves and what
// it would need an AWS account to prove is the whole honesty of the runtime. A
// Closed verdict is a statement about a JSON document. Whether AWS enforces
// that document is a statement about an account, and nothing in this repository
// has ever observed it.
const Caveat = "Every verdict here is computed from the configuration this runtime would " +
	"generate. None of it has been applied to an AWS account and no packet has been " +
	"observed failing to leave a task. A closed verdict means the generated " +
	"configuration removes the path, not that AWS was seen enforcing it."

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
// fixed later. Three of the paths cannot be closed on ECS at all and two cannot
// be decided without an account, so a true here would mean the enumeration had
// been shortened rather than that the containment had improved.
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
	fmt.Fprintf(&b, "%d of %d egress paths out of an ECS task on Fargate are closed by the "+
		"generated configuration (%d open, %d unproven).\n", r.Closed, r.Total, r.Open, r.Unproven)
	for _, v := range r.Verdicts {
		fmt.Fprintf(&b, "  %-9s %s\n             %s\n", v.Verdict, v.Path.ID, v.Detail)
	}
	b.WriteString("\n" + Caveat + "\n")
	return b.String()
}

// checkPublicIPv4 requires that all three of the mechanisms the plan relies on
// are present.
//
// All three rather than any one of them, and that choice needs defending
// because a single one of them is sufficient on its own. The predicate is a
// statement about the configuration this runtime generates, not about the
// minimum configuration that would happen to work: a plan that has lost one of
// its three closures is no longer the plan the proof covers, and reporting it
// closed would mean the check cannot fail when the generator regresses. A check
// that cannot say no is worse than no check.
func checkPublicIPv4(p Plan) (Verdict, string) {
	var missing []string
	for _, r := range p.Network.RouteTable.Routes {
		if kind := gatewayKind(r.Target); kind == "an internet gateway" || kind == "a NAT gateway" {
			missing = append(missing, fmt.Sprintf("the route table sends %s to %s (%s)",
				r.Destination, r.Target, kind))
		}
	}
	if p.Network.RouteTable.AssignPublicIP != AssignPublicIPDisabled {
		missing = append(missing, fmt.Sprintf("assignPublicIp is %q and must be %q",
			p.Network.RouteTable.AssignPublicIP, AssignPublicIPDisabled))
	}
	for _, rule := range p.Network.SecurityGroup.Egress {
		if rule.CIDRv4 != "" && reachesPublicIPv4(rule.CIDRv4) {
			missing = append(missing, fmt.Sprintf("a security group egress rule permits %s to %s",
				describePorts(rule), rule.CIDRv4))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "no internet gateway or NAT gateway route, assignPublicIp DISABLED, and no " +
		"egress rule naming a range outside RFC 1918, RFC 6598 and the link local block"
}

// checkPublicIPv6 is separate from checkPublicIPv4 because a security group
// holds the two rule sets separately and an audit written in one does not see
// the other.
func checkPublicIPv6(p Plan) (Verdict, string) {
	var missing []string
	if p.Network.VPC.IPv6CIDR != "" {
		missing = append(missing, fmt.Sprintf("the VPC carries the IPv6 range %s", p.Network.VPC.IPv6CIDR))
	}
	for _, s := range p.Network.Subnets {
		if s.IPv6CIDR != "" {
			missing = append(missing, fmt.Sprintf("subnet %s carries the IPv6 range %s", s.ID, s.IPv6CIDR))
		}
	}
	for _, r := range p.Network.RouteTable.Routes {
		if gatewayKind(r.Target) == "an egress only internet gateway" || strings.Contains(r.Destination, "::/0") {
			missing = append(missing, fmt.Sprintf("the route table sends %s to %s", r.Destination, r.Target))
		}
	}
	for _, rule := range p.Network.SecurityGroup.Egress {
		if rule.CIDRv6 != "" {
			missing = append(missing, fmt.Sprintf("a security group egress rule permits %s to %s",
				describePorts(rule), rule.CIDRv6))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "no IPv6 range on the VPC or its subnets, no egress only internet gateway " +
		"route, and no IPv6 egress rule"
}

// checkPublicResolver looks for port 53 reaching a public range specifically,
// as well as for the route, because allowing DNS out is the exception people
// grant without thinking of it as egress.
func checkPublicResolver(p Plan) (Verdict, string) {
	var missing []string
	for _, r := range p.Network.RouteTable.Routes {
		if kind := gatewayKind(r.Target); kind == "an internet gateway" || kind == "a NAT gateway" {
			missing = append(missing, fmt.Sprintf("the route table sends %s to %s, which a "+
				"query to a public resolver would take", r.Destination, r.Target))
		}
	}
	for _, rule := range p.Network.SecurityGroup.Egress {
		if !ruleCoversPort(rule, 53) {
			continue
		}
		if rule.CIDRv4 != "" && reachesPublicIPv4(rule.CIDRv4) {
			missing = append(missing, fmt.Sprintf("a security group egress rule permits %s to %s, "+
				"which reaches a public resolver", describePorts(rule), rule.CIDRv4))
		}
		if rule.CIDRv6 != "" {
			missing = append(missing, fmt.Sprintf("a security group egress rule permits %s to %s",
				describePorts(rule), rule.CIDRv6))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "no route out and no egress rule that reaches port 53 on a range outside the VPC"
}

// checkAmazonResolver is the one check where the security group is irrelevant,
// and saying so is the useful part.
func checkAmazonResolver(p Plan) (Verdict, string) {
	const irrelevant = "security groups and network ACLs cannot filter this path, so nothing in " +
		"the security group above is evidence about it: "

	if !p.Network.VPC.EnableDNSSupport {
		return Closed, irrelevant + "enableDnsSupport is off on the VPC, so queries to the " +
			"Amazon provided DNS server do not succeed"
	}
	fw := p.Network.DNSFirewall
	if fw == nil {
		return Open, irrelevant + "enableDnsSupport is on and no Route 53 Resolver DNS Firewall " +
			"rule group is associated with this VPC, so the resolver answers recursive queries " +
			"for any public name"
	}
	var missing []string
	if fw.AssociatedVPCID != p.Network.VPC.ID {
		missing = append(missing, fmt.Sprintf("the rule group is associated with %q and this "+
			"plan's VPC is %q", fw.AssociatedVPCID, p.Network.VPC.ID))
	}
	if fw.FailOpen {
		missing = append(missing, "the association fails open, so a rule the firewall cannot "+
			"reach permits the query")
	}
	if last, ok := lastRule(fw.Rules); !ok {
		missing = append(missing, "the rule group has no rules")
	} else if !isBlockEverything(last) {
		missing = append(missing, fmt.Sprintf("the last rule by priority is %s on %s and must "+
			"be BLOCK on *", last.Action, strings.Join(last.Domains, ", ")))
	}
	if len(missing) > 0 {
		return Open, irrelevant + strings.Join(missing, "; ")
	}
	return Closed, irrelevant + "a DNS Firewall rule group associated with this VPC blocks every " +
		"domain in its last rule and does not fail open"
}

// checkInstanceMetadata is the path the lane was warned about by name, and the
// answer it produces is deliberately not Closed.
//
// The Fargate launch type removes the documented credential source: the ECS
// task IAM role page states that EC2 instance profiles are not available to
// containers in Fargate tasks. What it does not state, anywhere this lane could
// find, is that 169.254.169.254 does not answer inside a Fargate task. Those
// are different claims. The first is about what credentials exist and the
// second is about what the address does, and rounding the second up to the
// first is precisely how a containment claim softens into a plausible sentence.
//
// So Fargate is Unproven and EC2 is Open, and neither is Closed, because no
// field of a task definition or a security group closes a link local address.
func checkInstanceMetadata(p Plan) (Verdict, string) {
	if !strings.EqualFold(p.LaunchType, "FARGATE") {
		return Open, fmt.Sprintf("the launch type is %q. On a container instance the "+
			"documented defence is the ECS_AWSVPC_BLOCK_IMDS agent variable, which is set on "+
			"the instance and which no task definition can set, so this plan cannot close it",
			p.LaunchType)
	}
	return Unproven, "the launch type is FARGATE, so no EC2 instance profile exists for the " +
		"endpoint to vend, but AWS does not document whether 169.254.169.254 answers inside a " +
		"Fargate task and a link local address is not filtered by the security group. Settling " +
		"this needs one request from one running task, which needs an account"
}

// checkTaskRoleCredentials is closed by an absence, which is the cheapest and
// most durable kind of closure in this file.
func checkTaskRoleCredentials(p Plan) (Verdict, string) {
	if arn := strings.TrimSpace(p.TaskDefinition.TaskRoleARN); arn != "" {
		return Open, fmt.Sprintf("the task definition sets taskRoleArn to %q, and 169.254.170.2 "+
			"vends that role's credentials to anything in the container that reads "+
			"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", arn)
	}
	return Closed, "the task definition sets no taskRoleArn, so the credentials endpoint has " +
		"nothing to vend. The execution role is not reachable from the container: ECS documents " +
		"that execution role permissions are not accessed by the application"
}

// checkTaskMetadata can only ever return Open, and a check with one possible
// answer is usually a bug. This one is a finding.
//
// It is written as a check rather than as a comment so that it appears in the
// report, is counted in the denominator, and is printed in the refusal. A path
// nothing can close is exactly the path a summary leaves out.
func checkTaskMetadata(p Plan) (Verdict, string) {
	return Open, fmt.Sprintf("platform version %q. The task metadata endpoint is on by default "+
		"for every Fargate task on platform version 1.4.0 or later and AWS documents no way to "+
		"turn it off. It discloses the task ARN, the cluster, the ENI address and container "+
		"statistics. With no task role it vends no credentials", p.PlatformVersion)
}

// checkInterfaceEndpoints reports the endpoints the design is obliged to open,
// and reports how narrow they are, which is the only thing that can change.
func checkInterfaceEndpoints(p Plan) (Verdict, string) {
	var reachable, unrestricted []string
	for _, e := range p.Network.Endpoints {
		if !strings.EqualFold(e.Type, "Interface") {
			continue
		}
		reachable = append(reachable, e.Service)
		if strings.TrimSpace(e.PolicyDocument) == "" {
			unrestricted = append(unrestricted, e.Service)
		}
	}
	if len(reachable) == 0 {
		return Closed, "the plan holds no interface endpoints. Note that a Fargate task in a " +
			"subnet with no route out and no ECR endpoint cannot pull an image, so a plan that " +
			"closes this path cannot start an environment"
	}
	sort.Strings(reachable)
	detail := fmt.Sprintf("the task can reach %s, because on Fargate 1.4.0 the image pull runs "+
		"over the task ENI under this security group", strings.Join(reachable, ", "))
	if len(unrestricted) > 0 {
		sort.Strings(unrestricted)
		return Open, detail + fmt.Sprintf("; and %s carry no endpoint policy, so the "+
			"narrowing is absent as well", strings.Join(unrestricted, ", "))
	}
	return Open, detail + "; each carries an endpoint policy and is reached through an egress " +
		"rule naming the endpoints' security group rather than an address range"
}

// checkS3Gateway is separate from the interface endpoints because a gateway
// endpoint is a route table entry rather than an ENI, so it is not governed by
// the same rules and is not found by the same audit.
func checkS3Gateway(p Plan) (Verdict, string) {
	for _, e := range p.Network.Endpoints {
		if !strings.EqualFold(e.Type, "Gateway") {
			continue
		}
		if strings.TrimSpace(e.PolicyDocument) == "" {
			return Open, fmt.Sprintf("%s is a gateway endpoint with no endpoint policy, which "+
				"is a route to every bucket in the region", e.Service)
		}
		return Open, fmt.Sprintf("%s is reachable, because ECR stores image layers in S3 and "+
			"the pull needs both. It carries an endpoint policy, which is the only narrowing "+
			"available to a gateway endpoint", e.Service)
	}
	return Closed, "the plan holds no gateway endpoint. An ECR pull needs one, so a plan that " +
		"closes this path cannot start an environment"
}

// checkExecuteCommand looks at the task definition and at the endpoints,
// because the flag alone is not the path: an ssmmessages endpoint left in the
// VPC is the route the flag would use, and it outlives the flag.
func checkExecuteCommand(p Plan) (Verdict, string) {
	var missing []string
	if p.TaskDefinition.EnableExecuteCommand {
		missing = append(missing, "enableExecuteCommand is true")
	}
	for _, e := range p.Network.Endpoints {
		if strings.Contains(e.Service, "ssmmessages") || strings.Contains(e.Service, ".ssm") {
			missing = append(missing, fmt.Sprintf("%s is reachable, which is the channel ECS "+
				"Exec runs over", e.Service))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "enableExecuteCommand is false and no Systems Manager endpoint is reachable"
}

// checkPrivateGatewayRoutes is the path a naive internet check misses entirely.
func checkPrivateGatewayRoutes(p Plan) (Verdict, string) {
	var found []string
	for _, r := range p.Network.RouteTable.Routes {
		switch gatewayKind(r.Target) {
		case "a VPC peering connection", "a transit gateway", "a virtual private gateway",
			"a local gateway", "a carrier gateway":
			found = append(found, fmt.Sprintf("%s to %s (%s)", r.Destination, r.Target,
				gatewayKind(r.Target)))
		}
	}
	if len(found) > 0 {
		return Open, "the route table reaches a network that is not the internet and not this " +
			"VPC: " + strings.Join(found, "; ")
	}
	return Closed, "the route table holds only the local route and gateway endpoint prefix lists"
}

// checkNeighbouringEnvironment is the isolation promise, and it is written
// against the security group rather than against the subnet because every
// environment in this design shares a VPC and therefore shares a range.
func checkNeighbouringEnvironment(p Plan) (Verdict, string) {
	own := map[string]bool{p.Network.SecurityGroup.ID: true}
	for _, e := range p.Network.Endpoints {
		if e.SecurityGroupID != "" {
			own[e.SecurityGroupID] = true
		}
	}
	var missing []string
	for _, rule := range p.Network.SecurityGroup.Egress {
		if rule.PeerSecurityGroupID != "" && !own[rule.PeerSecurityGroupID] {
			missing = append(missing, fmt.Sprintf("an egress rule names security group %s, "+
				"which is neither this environment's group nor an endpoint's",
				rule.PeerSecurityGroupID))
		}
		if rule.CIDRv4 == "" {
			continue
		}
		if !withinSubnets(rule.CIDRv4, p.Network.Subnets) {
			missing = append(missing, fmt.Sprintf("an egress rule permits %s to %s, which is "+
				"wider than this environment's own subnets", describePorts(rule), rule.CIDRv4))
		}
	}
	for _, rule := range p.Network.SecurityGroup.Ingress {
		if rule.PeerSecurityGroupID != "" && !own[rule.PeerSecurityGroupID] {
			missing = append(missing, fmt.Sprintf("an ingress rule admits security group %s, "+
				"which is neither this environment's group nor an endpoint's",
				rule.PeerSecurityGroupID))
		}
	}
	if len(missing) > 0 {
		return Open, strings.Join(missing, "; ")
	}
	return Closed, "every rule names this environment's own security group or an endpoint's, " +
		"and no rule names a range wider than this environment's subnets"
}

// checkTimeSync returns Unproven whatever the plan says, and that is the
// honest answer rather than a placeholder.
//
// The VPC DNS quota page counts NTP requests against the same link local packet
// budget as the resolver and instance metadata, which is documentary evidence
// that a link local NTP service exists inside the network. The EC2 page for the
// local Amazon Time Sync Service does not give the address and says nothing
// about filtering. One page implies the path and no page decides it, so the
// verdict says so and the path stays in the denominator.
func checkTimeSync(Plan) (Verdict, string) {
	return Unproven, "the VPC DNS quota page counts Amazon Time Service NTP requests against " +
		"the same 1024 packet per second link local budget as the resolver and instance " +
		"metadata, so a link local NTP service is reachable from inside the VPC. No AWS page " +
		"this lane read states whether a security group filters it, and this lane does not " +
		"assert an answer it could not source"
}

// gatewayKind names an AWS target by its id prefix, because in AWS the prefix
// is the type and reading it is not a heuristic.
func gatewayKind(target string) string {
	switch {
	case strings.HasPrefix(target, "igw-"):
		return "an internet gateway"
	case strings.HasPrefix(target, "nat-"):
		return "a NAT gateway"
	case strings.HasPrefix(target, "eigw-"):
		return "an egress only internet gateway"
	case strings.HasPrefix(target, "pcx-"):
		return "a VPC peering connection"
	case strings.HasPrefix(target, "tgw-"):
		return "a transit gateway"
	case strings.HasPrefix(target, "vgw-"):
		return "a virtual private gateway"
	case strings.HasPrefix(target, "lgw-"):
		return "a local gateway"
	case strings.HasPrefix(target, "cagw-"):
		return "a carrier gateway"
	case strings.HasPrefix(target, "vpce-"):
		return "a VPC endpoint"
	case target == "local":
		return "the VPC itself"
	default:
		return "an unrecognised target"
	}
}

// privateRanges are the ranges a rule may name without reaching the internet.
//
// Link local is in the list because a rule naming it does not reach a public
// address, NOT because link local is safe. The two link local endpoints have
// their own paths above, and they are not filtered by security groups at all,
// so a security group check has nothing to say about them either way.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
}

// reachesPublicIPv4 reports whether a CIDR contains any address outside the
// private ranges.
//
// It fails towards public on purpose. A CIDR this function cannot parse is
// reported as reaching the internet, because the alternative is that a
// malformed rule silently counts as containment, and the whole file exists to
// stop a containment claim resting on something nobody looked at.
func reachesPublicIPv4(cidr string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !prefix.Addr().Is4() {
		return true
	}
	for _, private := range privateRanges {
		if private.Overlaps(prefix) && private.Bits() <= prefix.Bits() &&
			private.Contains(prefix.Addr()) {
			return false
		}
	}
	return true
}

// withinSubnets reports whether a CIDR is inside one of the plan's own subnets.
func withinSubnets(cidr string, subnets []Subnet) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	for _, s := range subnets {
		sub, err := netip.ParsePrefix(strings.TrimSpace(s.IPv4CIDR))
		if err != nil {
			continue
		}
		if sub.Contains(prefix.Addr()) && sub.Bits() <= prefix.Bits() {
			return true
		}
	}
	return false
}

// ruleCoversPort reports whether a rule permits a port. Protocol "-1" is every
// protocol and every port, which AWS represents with a port range of zero.
func ruleCoversPort(rule Rule, port int) bool {
	if rule.Protocol == "-1" {
		return true
	}
	return rule.FromPort <= port && port <= rule.ToPort
}

// describePorts renders a rule's protocol and ports for a message.
func describePorts(rule Rule) string {
	if rule.Protocol == "-1" {
		return "every protocol and port"
	}
	if rule.FromPort == rule.ToPort {
		return fmt.Sprintf("%s/%d", rule.Protocol, rule.FromPort)
	}
	return fmt.Sprintf("%s/%d to %d", rule.Protocol, rule.FromPort, rule.ToPort)
}

// lastRule returns the rule with the highest priority number, which is the one
// DNS Firewall evaluates last and therefore the one that decides a query no
// earlier rule matched.
func lastRule(rules []DNSFirewallRule) (DNSFirewallRule, bool) {
	if len(rules) == 0 {
		return DNSFirewallRule{}, false
	}
	last := rules[0]
	for _, r := range rules[1:] {
		if r.Priority > last.Priority {
			last = r
		}
	}
	return last, true
}

// isBlockEverything reports whether a rule blocks every domain.
func isBlockEverything(rule DNSFirewallRule) bool {
	if !strings.EqualFold(rule.Action, "BLOCK") {
		return false
	}
	for _, d := range rule.Domains {
		if strings.TrimSpace(d) == "*" {
			return true
		}
	}
	return false
}

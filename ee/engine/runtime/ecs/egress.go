package ecs

// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.

import (
	"encoding/json"
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

// Grade is what KIND of evidence produced a verdict, and it is in the type
// rather than only in the caveat on purpose.
//
// Report.Caveat says in prose that a closed verdict is a statement about a JSON
// document. That was true of every verdict this package could produce when the
// caveat was written, and it stopped being true the moment a probe inside a
// running task could record what it saw. Two closed verdicts of different
// grades are different claims, and a sentence at the bottom of a report is the
// wrong place to keep a distinction that a caller might want to act on: a
// person deciding whether to trust an environment should be able to ask which
// of the closures were observed, not read for it.
type Grade string

const (
	// FromDocument means a predicate read the generated configuration. It is a
	// statement about a JSON document and nothing has applied it to an account.
	FromDocument Grade = "document"
	// FromObservation means a probe inside a running task in the customer's own
	// account made the attempt and this is what it saw. It is a statement about
	// a packet.
	FromObservation Grade = "observation"
	// NotEstablished is the grade of every unproven verdict. Nothing looked, so
	// there is no evidence of either kind, and it is a value rather than an
	// empty string so that a report cannot carry a verdict whose grade nobody
	// filled in.
	NotEstablished Grade = "none"
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
	// Observe interprets a recorded probe result for this path, and it is nil
	// for every path the configuration decides.
	//
	// It is nil for those on purpose rather than for want of writing: a path a
	// route table already answers does not need a second instrument, and a
	// second instrument that could disagree with the first about the same fact
	// is a way to be confidently wrong rather than a way to be more sure. Only
	// the two link local addresses have one, because they are the two the
	// configuration cannot decide at all.
	//
	// It is consulted ONLY when an observation for this path exists. With none,
	// Check decides, and Check returns Unproven for both of them, which is the
	// absence rule: no probe result means unproven, forever.
	Observe func(Observation) (Verdict, string)
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
				"whether the address answers, so the configuration does not settle it. " +
				"One attempt from one running task does, and the generated task definition " +
				"carries the probe container that makes it",
			Check:   checkInstanceMetadata,
			Observe: observeLinkLocal,
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
			Name: "any ECR repository or log group in the region, through the endpoints the image pull must reach",
			Why: "On Fargate platform version 1.4.0 the ECR login, the image pull and the log " +
				"push all flow over the TASK ENI, under this security group, and the " +
				"documentation says plainly that a Fargate task must have a route to the " +
				"registry to pull an image. There is no equivalent of a kubelet pulling the " +
				"image outside the pod's policy, so a task whose security group denies " +
				"everything never starts.",
			ClosedBy: "a VPC endpoint policy per endpoint, naming this environment's own " +
				"repository and log group and no wildcard resource or action, plus egress " +
				"rules naming the endpoints' own security group rather than an address " +
				"range. The connection to the endpoint cannot be closed without giving up " +
				"the ability to start an environment, and the route to any other " +
				"repository, log group or service through it can be, which is the half " +
				"that is an exfiltration path",
			Check: checkInterfaceEndpoints,
		},
		{
			ID:   "the-s3-gateway-endpoint-the-layers-come-from",
			Name: "any S3 bucket in the region, through the gateway endpoint the image layers come from",
			Why: "ECR stores image layers in S3, so the pull needs S3 as well as ECR. A " +
				"gateway endpoint is a route, and an unrestricted one is a path to every " +
				"bucket in the region, including the ones that accept anonymous writes.",
			ClosedBy: "a gateway endpoint policy naming the layer bucket with a read only " +
				"action. The route itself stays, for the same reason as the interface " +
				"endpoints, and an endpoint policy is the only narrowing a gateway " +
				"endpoint has",
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
			ClosedBy: "not established by any configuration. The EC2 page for the local " +
				"Amazon Time Sync Service gives the IPv4 endpoint as 169.254.169.123 and " +
				"says a link local address restricts the traffic to within the VPC, and it " +
				"nowhere says whether a security group filters it. So the address is known " +
				"and the filtering is not. An attempt would settle it and this lane " +
				"could not build one: the attempt is NTP over UDP and no tool in a " +
				"minimal container image makes it",
			Check: checkTimeSync,
		},
	}
}

// PathVerdict is one path with what the predicate decided about one plan.
type PathVerdict struct {
	Path    Path
	Verdict Verdict
	// Grade says what kind of evidence produced Verdict. Unproven is always
	// NotEstablished and NotEstablished is always Unproven, which is asserted
	// rather than assumed.
	Grade  Grade
	Detail string
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
	// ClosedByDocument and ClosedByObservation split Closed by grade, and they
	// sum to it.
	//
	// They are published separately because the two are not the same claim and
	// a single Closed count invites them to be read as though they were. One
	// says the configuration this runtime generates removes the path. The other
	// says a task in an account tried it and got nowhere. The second is the
	// stronger evidence and the harder to get, and a report that hid which was
	// which would let the easy one borrow the credibility of the hard one.
	ClosedByDocument    int
	ClosedByObservation int
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
const Caveat = "A verdict marked [document] is computed from the configuration this runtime " +
	"would generate. It says the generated configuration removes the path, not that AWS was " +
	"seen enforcing it, and nothing here has been applied to an AWS account. A verdict marked " +
	"[observation] is a recorded attempt from a task inside the account that ran the " +
	"environment, which is a statement about a packet rather than about a JSON document. Those " +
	"are different grades of evidence and this report counts them separately. A path with " +
	"neither stays unproven: no absence of evidence closes anything here."

// Evaluate applies every path's check to a plan, with no observed evidence.
//
// It is EvaluateWith against an empty set rather than a separate code path, so
// that the no evidence case cannot drift away from the case the tests exercise.
func Evaluate(plan Plan) Report { return EvaluateWith(plan, nil) }

// EvaluateWith applies every path's check to a plan and lets recorded
// observations answer the paths the plan cannot.
//
// The order is deliberate and it is the whole safety property. Check runs
// FIRST and always, so the configuration's own answer is the floor. An
// observation can only speak for a path that declared an Observe, and only when
// one was actually recorded for it. There is no branch in which a missing
// observation improves a verdict, which is what makes "it must never flip to
// closed on absence of evidence" a property of the shape rather than a rule
// somebody has to remember.
func EvaluateWith(plan Plan, observed Observations) Report {
	paths := Paths()
	report := Report{Verdicts: make([]PathVerdict, 0, len(paths)), Total: len(paths)}
	for _, path := range paths {
		verdict, detail := path.Check(plan)
		grade := FromDocument
		if verdict == Unproven {
			grade = NotEstablished
		}
		if path.Observe != nil {
			if obs, ok := observed.For(path.ID); ok {
				verdict, detail = path.Observe(obs)
				grade = FromObservation
				if verdict == Unproven {
					grade = NotEstablished
				}
			}
		}
		report.Verdicts = append(report.Verdicts,
			PathVerdict{Path: path, Verdict: verdict, Grade: grade, Detail: detail})
		switch verdict {
		case Closed:
			report.Closed++
			if grade == FromObservation {
				report.ClosedByObservation++
			} else {
				report.ClosedByDocument++
			}
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
	fmt.Fprintf(&b, "%d of %d egress paths out of an ECS task on Fargate are closed "+
		"(%d open, %d unproven).\n", r.Closed, r.Total, r.Open, r.Unproven)
	fmt.Fprintf(&b, "Of those %d, %d are closed by the generated configuration and %d by an "+
		"attempt observed from a running task.\n",
		r.Closed, r.ClosedByDocument, r.ClosedByObservation)
	for _, v := range r.Verdicts {
		fmt.Fprintf(&b, "  %-9s %-12s %s\n                            %s\n",
			v.Verdict, "["+string(v.Grade)+"]", v.Path.ID, v.Detail)
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
//
// It is also the check this lane found could not say no to the two shapes that
// matter most. A rule group is evaluated in priority order starting from the
// lowest, so "the last rule blocks everything" is necessary and nowhere near
// sufficient: an ALLOW or ALERT rule matching every domain at a lower priority
// answers every query before the terminal rule is ever reached, and the old
// predicate called that group closed. AWS defines ALLOW as "permit the request
// to go through" and ALERT as "permit the request and send metrics and logs to
// Cloud Watch", so both of them let the query out and only BLOCK disallows it.
// A rule group with ALLOW on * at priority 5 and BLOCK on * at priority 1000
// blocks nothing at all, and it reads as configured in a console screenshot.
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
	deferred := false
	switch fw.FailOpen {
	case FailClosed:
	case FailOpenEnabled:
		missing = append(missing, "FirewallFailOpen is ENABLED, so a query DNS Firewall cannot "+
			"evaluate is permitted rather than refused and the containment is best effort")
	case FailOpenDeferred:
		deferred = true
	default:
		missing = append(missing, fmt.Sprintf("FirewallFailOpen is %q, which is not one of the "+
			"three values AWS accepts, so the failure mode of this association is not described "+
			"by this plan", fw.FailOpen))
	}
	missing = append(missing, ruleGroupFaults(fw.Rules)...)

	// A definite fault outranks an undecided failure mode. Knowing the path is
	// open is a stronger statement than not knowing what happens when the
	// firewall breaks, and reporting unproven here would hide a fault behind an
	// uncertainty.
	if len(missing) > 0 {
		return Open, irrelevant + strings.Join(missing, "; ")
	}
	if deferred {
		return Unproven, irrelevant + "the rule group's rules are correct, and its " +
			"FirewallFailOpen is USE_LOCAL_RESOURCE_SETTING, which takes the failure mode from a " +
			"setting this plan does not carry. So whether a query DNS Firewall cannot evaluate is " +
			"permitted is not decided here, and a two valued reading of that field would round " +
			"the unknown case to whichever answer suited"
	}
	return Closed, irrelevant + "a DNS Firewall rule group associated with this VPC blocks every " +
		"domain in its last rule by priority, no earlier rule permits every domain, every " +
		"priority is unique, and FirewallFailOpen is DISABLED"
}

// ruleGroupFaults lists everything wrong with a rule group's rules.
//
// It returns every fault rather than the first, because the report is read by
// somebody fixing the group and a check that stops at the first problem makes
// them run it once per problem.
func ruleGroupFaults(rules []DNSFirewallRule) []string {
	if len(rules) == 0 {
		return []string{"the rule group has no rules"}
	}
	var faults []string

	// Unique priorities, because AWS requires them and because two rules
	// sharing one leave the order the group is evaluated in undefined by
	// anything in this plan. A rule group AWS refuses to create is not a
	// containment either.
	counts := map[int]int{}
	for _, r := range rules {
		counts[r.Priority]++
	}
	var dups []int
	for priority, n := range counts {
		if n > 1 {
			dups = append(dups, priority)
		}
	}
	sort.Ints(dups)
	for _, priority := range dups {
		faults = append(faults, fmt.Sprintf("%d rules share priority %d, and AWS requires a "+
			"unique priority for each rule in a rule group", counts[priority], priority))
	}

	ordered := append([]DNSFirewallRule(nil), rules...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Priority < ordered[j].Priority })

	for _, r := range ordered {
		switch strings.ToUpper(strings.TrimSpace(r.Action)) {
		case "ALLOW", "BLOCK", "ALERT":
		default:
			faults = append(faults, fmt.Sprintf("the rule at priority %d has action %q, which is "+
				"not one of ALLOW, BLOCK or ALERT", r.Priority, r.Action))
		}
		for _, d := range r.Domains {
			if !expressibleDomain(d) {
				faults = append(faults, fmt.Sprintf("the rule at priority %d matches %q, and a "+
					"DNS Firewall domain specification may only start with a star, so this rule "+
					"group cannot be created", r.Priority, d))
			}
		}
	}

	last := ordered[len(ordered)-1]
	if !isBlockEverything(last) {
		faults = append(faults, fmt.Sprintf("the last rule by priority is %s on %s and must be "+
			"BLOCK on *", last.Action, strings.Join(last.Domains, ", ")))
	}
	for _, r := range ordered[:len(ordered)-1] {
		if matchesEveryDomain(r) && permitsTheQuery(r) {
			faults = append(faults, fmt.Sprintf("the rule at priority %d is %s on * and is "+
				"evaluated before the terminal rule, so every query is permitted and the "+
				"terminal BLOCK is never reached", r.Priority, strings.ToUpper(r.Action)))
		}
	}
	return faults
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
	base := "the launch type is FARGATE, so no EC2 instance profile exists for the endpoint to " +
		"vend, but AWS does not document whether 169.254.169.254 answers inside a Fargate task " +
		"and a link local address is not filtered by the security group. Settling this needs " +
		"one request from one running task, and no probe result has been recorded for this " +
		"environment. "
	for _, c := range p.TaskDefinition.Containers {
		if c.Name == "af-containment-probe" {
			return Unproven, base + fmt.Sprintf("The task definition carries the probe container "+
				"%q on image %q, which makes the request on the first run in an account and "+
				"records what it saw", c.Name, c.Image)
		}
	}
	return Unproven, base + "The task definition carries no probe container either, because this " +
		"installation named no probe image, so as generated this plan has no way to answer the " +
		"question even once it runs"
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

// checkInterfaceEndpoints answers the question the path is actually about,
// which is not the one the first version of it answered.
//
// "The task can reach ECR" and "the task can reach ANY ECR repository and ANY
// log group in the region" are very different claims, and only the second is an
// exfiltration path. The first cannot be closed: on Fargate 1.4.0 the ECR
// login, the image pull and the log push all run over the task ENI under this
// security group, and AWS states that a Fargate task must have a route to the
// registry to pull an image, so a plan that closes the connectivity closes the
// product. The second can be closed, by an endpoint policy, and calling the
// whole path open because the first half cannot be shut was reporting the
// weaker fact and hiding the stronger one.
//
// So this returns Closed when the reachable set is exactly the endpoints the
// pull and the log push need and every one of them carries a policy naming
// concrete resources, and Open otherwise, naming which endpoint and why.
func checkInterfaceEndpoints(p Plan) (Verdict, string) {
	needed := map[string]bool{
		fmt.Sprintf("com.amazonaws.%s.ecr.api", p.Region): true,
		fmt.Sprintf("com.amazonaws.%s.ecr.dkr", p.Region): true,
		fmt.Sprintf("com.amazonaws.%s.logs", p.Region):    true,
	}
	var reachable, faults []string
	for _, e := range p.Network.Endpoints {
		if !strings.EqualFold(e.Type, "Interface") {
			continue
		}
		reachable = append(reachable, e.Service)
		if !needed[e.Service] {
			faults = append(faults, fmt.Sprintf("%s is an interface endpoint the image pull and "+
				"the log push do not need, and every endpoint in the VPC is a service the task "+
				"can open a connection to", e.Service))
			continue
		}
		if why, ok := policyNamesConcreteResources(e.PolicyDocument, p.TaskDefinition.Family); !ok {
			faults = append(faults, fmt.Sprintf("%s: %s", e.Service, why))
		}
	}
	if len(reachable) == 0 {
		return Closed, "the plan holds no interface endpoints. Note that a Fargate task in a " +
			"subnet with no route out and no ECR endpoint cannot pull an image, so a plan that " +
			"closes this path this way cannot start an environment"
	}
	sort.Strings(reachable)
	if len(faults) > 0 {
		sort.Strings(faults)
		return Open, fmt.Sprintf("the task can reach %s, and %s", strings.Join(reachable, ", "),
			strings.Join(faults, "; "))
	}
	return Closed, fmt.Sprintf("the task can open TCP 443 to %s and to nothing else, because on "+
		"Fargate 1.4.0 the image pull runs over the task ENI and that connectivity is the price "+
		"of an environment starting at all. Each endpoint carries a policy naming this "+
		"environment's own repository and log group, so no other repository, log group or "+
		"service in the region is reachable through them, and the egress rule names the "+
		"endpoints' security group rather than an address range. What is closed is the route to "+
		"an arbitrary destination, not the connection itself", strings.Join(reachable, ", "))
}

// policyNamesConcreteResources reports whether a VPC endpoint policy narrows the
// endpoint to named resources rather than to everything the service holds.
//
// An endpoint with no policy permits every action in that service that the
// caller has credentials for. A policy naming Resource "*" is the same thing
// written down. The bar here is that every Allow statement names at least one
// resource with a literal prefix belonging to this environment, and no
// statement grants every action, because an endpoint scoped to one repository
// with ecr:* on it is still a place to push a layer.
//
// It fails towards open, the way reachesPublicIPv4 does. A policy this function
// cannot parse is reported as unnarrowed, because the alternative is that a
// malformed document silently counts as containment.
func policyNamesConcreteResources(document, envName string) (string, bool) {
	body := strings.TrimSpace(document)
	if body == "" {
		return "it carries no endpoint policy, so it permits every action in that service that " +
			"the caller has credentials for", false
	}
	var doc iamPolicy
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Sprintf("its endpoint policy could not be read as a policy document (%v), and "+
			"a document nobody could parse is not a narrowing", err), false
	}
	if len(doc.Statement) == 0 {
		return "its endpoint policy holds no statements, which permits nothing and narrows " +
			"nothing", false
	}
	for _, st := range doc.Statement {
		if !strings.EqualFold(strings.TrimSpace(st.Effect), "Allow") {
			continue
		}
		if len(st.Resource) == 0 {
			return "its endpoint policy allows an action with no Resource at all", false
		}
		for _, action := range st.Action {
			a := strings.TrimSpace(action)
			if a == "*" || strings.HasSuffix(a, ":*") {
				return fmt.Sprintf("its endpoint policy allows %q, which is every action in the "+
					"service on whatever it names", action), false
			}
		}
		if onlyResourcelessActions(st.Action) {
			// The one carve out, and it is narrow on purpose. A handful of AWS
			// actions take no resource at all, so IAM requires Resource "*" for
			// them and a policy that named a repository instead would deny the
			// call. ecr:GetAuthorizationToken is the one this plan needs, and
			// without it the login fails and no image is pulled. It is not a
			// destination: it returns a token scoped to the caller's own
			// account rather than reaching anything. So a statement whose
			// actions are ALL in that set may name a wildcard resource, and one
			// that slips any other action into the same statement may not.
			continue
		}
		named := false
		for _, resource := range st.Resource {
			section, ok := arnResourceSection(resource)
			if !ok || section == "*" || strings.HasPrefix(section, "*") {
				return fmt.Sprintf("its endpoint policy allows %q, which reaches every resource "+
					"the service holds", resource), false
			}
			if envName != "" && strings.Contains(resource, envName) {
				named = true
			}
		}
		if envName != "" && !named {
			return fmt.Sprintf("its endpoint policy names %s, and none of them belongs to %s, so "+
				"it narrows the endpoint to somebody else's resources rather than to this "+
				"environment's", strings.Join(st.Resource, ", "), envName), false
		}
	}
	return "", true
}

// resourcelessActions are the actions that take no resource, so that a policy
// naming them has to name a wildcard resource and naming one is not a widening.
//
// It is a fixed set rather than a pattern, and it holds exactly what this
// runtime's own plan needs. A pattern here would be a way for the next action
// somebody adds to inherit the exception without anybody deciding it should.
var resourcelessActions = map[string]bool{
	"ecr:GetAuthorizationToken": true,
}

// onlyResourcelessActions reports whether every action in a statement is one.
//
// Empty is false: a statement with no actions at all has not earned the
// exception, it has just failed to say anything.
func onlyResourcelessActions(actions []string) bool {
	if len(actions) == 0 {
		return false
	}
	for _, a := range actions {
		if !resourcelessActions[strings.TrimSpace(a)] {
			return false
		}
	}
	return true
}

// arnResourceSection returns the part of an ARN after the account field, which
// is the part that says WHICH repository, bucket or log group.
//
// An ARN is arn:partition:service:region:account:resource and the resource
// section is everything after the fifth colon, so a bare "*" is not an ARN at
// all and is reported as such rather than treated as a resource name that
// happens to be a star.
func arnResourceSection(arn string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return "", false
	}
	return parts[5], true
}

// iamPolicy is the part of a policy document this predicate reads.
type iamPolicy struct {
	Statement []iamStatement `json:"Statement"`
}

// iamStatement is one statement.
type iamStatement struct {
	Effect   string     `json:"Effect"`
	Action   stringList `json:"Action"`
	Resource stringList `json:"Resource"`
}

// stringList decodes an IAM field that is a string OR an array of strings.
//
// It exists because getting that cardinality wrong is a whole class of bug in
// this repository's history: a to one field decoded as a list throws, and a
// throw while decoding a list discards the entire list. Here the consequence
// would be quieter and worse, because a policy that failed to parse is reported
// as unnarrowed, so a decoder that could not read AWS's own single string form
// would report every correctly scoped endpoint as open and nobody would look
// twice at a containment check being pessimistic.
type stringList []string

// UnmarshalJSON accepts both shapes.
func (l *stringList) UnmarshalJSON(raw []byte) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// checkS3Gateway is separate from the interface endpoints because a gateway
// endpoint is a route table entry rather than an ENI, so it is not governed by
// the same rules and is not found by the same audit.
//
// It closes on the same argument: ECR serves image layers from S3 and the pull
// needs both, so the connectivity stays, and what an endpoint policy can remove
// is the route to every OTHER bucket in the region, including the ones that
// accept anonymous writes. That removal is the difference between a read of one
// AWS owned layer bucket and an upload target.
func checkS3Gateway(p Plan) (Verdict, string) {
	for _, e := range p.Network.Endpoints {
		if !strings.EqualFold(e.Type, "Gateway") {
			continue
		}
		// The layer bucket is AWS's own and carries no environment name, so
		// the concrete resource check runs with no name to require. What it
		// still refuses is a wildcard bucket and a wildcard action, which are
		// the two ways this endpoint becomes a route to every bucket.
		if why, ok := policyNamesConcreteResources(e.PolicyDocument, ""); !ok {
			return Open, fmt.Sprintf("%s is a gateway endpoint and %s", e.Service, why)
		}
		return Closed, fmt.Sprintf("%s is reachable, because ECR serves image layers from S3 and "+
			"the pull needs both, and its endpoint policy names a concrete bucket with a read "+
			"only action, so the route does not reach any other bucket in the region. An "+
			"endpoint policy is the only narrowing available to a gateway endpoint and this one "+
			"carries it", e.Service)
	}
	return Closed, "the plan holds no gateway endpoint. An ECR pull needs one, so a plan that " +
		"closes this path this way cannot start an environment"
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
// that a link local NTP service exists inside the network, and the EC2 page for
// the local Amazon Time Sync Service gives that service's IPv4 endpoint as
// 169.254.169.123. What no page says is whether a security group filters it,
// and a security group does not filter the resolver at all, so the one nearby
// answer does not transfer.
//
// This lane corrected the earlier text on one point. The address IS documented,
// on the page the time page links to rather than on the time page itself, and
// the correction matters because an unproven path with no address can never be
// answered at all.
//
// It still has no probe, and that is the honest end of it rather than an
// unfinished one. The attempt is an NTP exchange over UDP and nothing in a
// minimal container image makes one, so a probe target here could only ever
// record that it could not try. This path went from unanswerable to answerable
// in principle and no further, and it is reported that way rather than counted
// as progress.
func checkTimeSync(Plan) (Verdict, string) {
	return Unproven, "the VPC DNS quota page counts Amazon Time Service NTP requests against " +
		"the same 1024 packet per second link local budget as the resolver and instance " +
		"metadata, and the EC2 page gives the IPv4 endpoint as 169.254.169.123. No AWS page " +
		"this lane read states whether a security group filters it, and no plan field decides " +
		"it. A probe from one running task settles it and none has been recorded for this " +
		"environment"
}

// observeLinkLocal turns one recorded attempt into a verdict.
//
// It is the only function in this package that can produce a Closed verdict
// about a packet rather than about a document, and every branch of it is
// written so that the absence of information stays absent.
//
// Reachable is the only outcome that is positive evidence, and it opens the
// path. Refused and TimedOut close it, because a link local address with
// nothing behind it is the shape of both. Errored does NOT close it: a probe
// that could not run has not looked, and reading a broken instrument as a pass
// is the single failure this repository keeps finding in its own checks.
func observeLinkLocal(obs Observation) (Verdict, string) {
	where := fmt.Sprintf("a probe inside a running task in environment %q dialled %s over %s",
		obs.EnvironmentID, obs.Address, obs.Network)
	if obs.ProbeVersion != ProbeVersion {
		return Unproven, fmt.Sprintf("%s, and recorded the result as %q, which this build does "+
			"not know how to read. A result from a probe whose behaviour is not this one is not "+
			"evidence about this one", where, obs.ProbeVersion)
	}
	when := obs.ObservedAt.UTC().Format("2006-01-02T15:04:05Z")
	switch obs.Outcome {
	case Reachable:
		return Open, fmt.Sprintf("%s at %s and something answered. This is an observed packet "+
			"rather than a reading of a document: %s", where, when, obs.Detail)
	case NoAnswer:
		return Closed, fmt.Sprintf("%s at %s and nothing answered within %s. This is an "+
			"observed packet rather than a reading of a document, and it is the only verdict "+
			"in this report that is about one: %s", where, when, obs.Timeout, obs.Detail)
	default:
		return Unproven, fmt.Sprintf("%s at %s and could not make the attempt at all (%s). A "+
			"probe that did not run says nothing about the path, and this is deliberately not "+
			"read as a refusal", where, when, obs.Detail)
	}
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

// matchesEveryDomain reports whether a rule matches every query.
func matchesEveryDomain(rule DNSFirewallRule) bool {
	for _, d := range rule.Domains {
		if strings.TrimSpace(d) == "*" {
			return true
		}
	}
	return false
}

// permitsTheQuery reports whether a rule's action lets the query out.
//
// ALERT is in here with ALLOW rather than with BLOCK, and that is the point of
// the function existing at all. AWS defines ALERT as "permit the request and
// send metrics and logs to Cloud Watch". It is the value somebody sets while
// tuning a rule group and leaves, and read as a block it turns an open resolver
// into a closed verdict with a graph to look at.
func permitsTheQuery(rule DNSFirewallRule) bool {
	switch strings.ToUpper(strings.TrimSpace(rule.Action)) {
	case "ALLOW", "ALERT":
		return true
	default:
		return false
	}
}

// isBlockEverything reports whether a rule blocks every domain.
func isBlockEverything(rule DNSFirewallRule) bool {
	if !strings.EqualFold(strings.TrimSpace(rule.Action), "BLOCK") {
		return false
	}
	return matchesEveryDomain(rule)
}

// expressibleDomain reports whether AWS would accept a domain specification.
//
// A specification "can optionally start with * (asterisk)" and may otherwise
// hold only letters, digits, hyphens and the period that separates labels. So a
// star in the middle, which the manifest's own host syntax allows and uses, is
// not a pattern a DNS Firewall domain list can hold. The predicate refuses such
// a rule group rather than assuming AWS would take it, because the alternative
// is a plan that reads as containment and cannot be created.
func expressibleDomain(domain string) bool {
	d := strings.TrimSpace(domain)
	if d == "" || len(d) > 255 {
		return false
	}
	if d == "*" {
		return true
	}
	d = strings.TrimPrefix(d, "*")
	for _, r := range d {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

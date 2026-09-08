// Package ecs is the AWS ECS on Fargate runtime, and it is mostly a proof
// about containment rather than a placer of containers.
//
// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.
//
// The Kubernetes runtime earns its existence by creating NetworkPolicy objects
// and then running a pod that tries to escape four ways before any application
// image starts, refusing the environment with AF-RUN-043 when any attempt
// succeeds. That order matters: the policy is a request to whatever CNI the
// cluster runs, several CNIs accept the object and enforce nothing, and the
// only thing that can tell those apart is a packet.
//
// ECS has the same gap in a worse place. A task definition, a security group
// and a route table are all objects AWS accepts, and whether they contain
// anything is a fact about the account they were applied to. So this package is
// built around a predicate over the configuration an environment would be given
// rather than around an API client, because the predicate is the part that can
// be proved in continuous integration on a machine with no AWS account, and the
// enforcement is the part that cannot.
//
// What this package deliberately does NOT do is start an environment. See
// runtime.go for why, and read egress.go first: the enumeration there is the
// deliverable, and the runtime's refusal is a consequence of it.
package ecs

// Plan is the whole AWS configuration one environment would be given.
//
// It is one flat value rather than a client talking to AWS, and that is the
// design decision the rest of the package rests on. A predicate over a value
// can be evaluated by a test, printed in an error message, checked into a
// golden file and mutated one field at a time. A predicate over an account can
// be evaluated in exactly one place, which is an account somebody is paying
// for, and a repository whose containment proof lives there has no containment
// proof at all on the day the credentials expire.
//
// Every field here is a field of a real AWS request or resource. Nothing is a
// summary or a boolean standing in for a shape, because a summary is where the
// thing being asserted stops being the thing being applied.
type Plan struct {
	// Region is the AWS region, which appears in the endpoint service names
	// the plan has to match against.
	Region string
	// Cluster is the ECS cluster the tasks are placed in.
	Cluster string
	// LaunchType must be FARGATE. It is checked rather than assumed because
	// the EC2 launch type puts the task on a container instance whose instance
	// profile credentials are reachable from inside the container, and the
	// only documented defence there is an agent variable on the instance
	// (ECS_AWSVPC_BLOCK_IMDS) that no task definition can set.
	LaunchType string
	// PlatformVersion is the Fargate platform version. Below 1.4.0 a task gets
	// a second, Fargate owned ENI carrying image pulls and secret retrieval
	// which does not appear in VPC flow logs and which no security group in
	// this plan describes, so a plan that does not pin 1.4.0 or later is
	// making a claim about traffic it cannot see.
	PlatformVersion string
	// TaskDefinition is what runs.
	TaskDefinition TaskDefinition
	// Network is everything the packets can and cannot do.
	Network NetworkPlan
}

// TaskDefinition is the ECS task definition, cut down to the fields that decide
// whether anything can get out.
type TaskDefinition struct {
	// Family names the definition.
	Family string
	// NetworkMode must be awsvpc, which is the only mode Fargate supports and
	// the only one that gives the task its own ENI and therefore its own
	// security group.
	NetworkMode string
	// TaskRoleARN is the role whose credentials 169.254.170.2 vends to
	// anything inside the container that asks.
	//
	// It must be EMPTY. That endpoint cannot be turned off on Fargate, so the
	// only way to make it vend nothing is to give it nothing, and an
	// environment that is a twin of production has no business holding
	// production's IAM permissions in the first place.
	TaskRoleARN string
	// ExecutionRoleARN is the role Fargate itself uses to pull the image and
	// push logs. It is NOT vended to the container: the credentials endpoint
	// serves the task role, and the ECS documentation states plainly that
	// execution role permissions "aren't accessed by" the application. It is
	// therefore allowed to be set, and it has to be, because without it no
	// image can be pulled at all.
	ExecutionRoleARN string
	// EnableExecuteCommand is ECS Exec. It must be false. It opens a channel to
	// AWS Systems Manager that is an interactive shell inbound and an
	// unmetered data channel outbound, and it is the one egress path in this
	// list that a person turns on deliberately while debugging and forgets.
	EnableExecuteCommand bool
	// Containers are the images and their environments, in the order they
	// appear in the definition.
	Containers []Container
}

// Container is one container in the task definition.
type Container struct {
	// Name is the container name.
	Name string
	// Image is the image reference.
	Image string
	// Essential marks a container whose exit stops the task.
	Essential bool
}

// NetworkPlan is the VPC, the subnets, the security group, the route table,
// the endpoints and the DNS firewall, which together are the containment.
type NetworkPlan struct {
	// VPC is the network the task's ENI lives in.
	VPC VPC
	// Subnets are the subnets awsVpcConfiguration names.
	Subnets []Subnet
	// SecurityGroup is the single security group on the task ENI. One per
	// environment, because a security group shared between environments is
	// how one environment reaches another's database while every rule in it
	// still reads as correct.
	SecurityGroup SecurityGroup
	// RouteTable is the table associated with every subnet above.
	RouteTable RouteTable
	// Endpoints are the VPC endpoints the task can reach. They exist because
	// on Fargate 1.4.0 the image pull runs over the task ENI, so a task in a
	// subnet with no route out cannot start without them.
	Endpoints []VPCEndpoint
	// DNSFirewall is the Route 53 Resolver DNS Firewall association, which is
	// the ONLY thing in this struct that can close the resolver path. AWS
	// states that traffic to the Amazon DNS server cannot be filtered with
	// security groups or network ACLs, so without this the resolver at
	// 169.254.169.253 answers recursive queries for any public name and the
	// query itself carries whatever the client put in it.
	DNSFirewall *DNSFirewall
}

// VPC is the network's own attributes.
type VPC struct {
	// ID is the VPC id.
	ID string
	// IPv4CIDR is the primary IPv4 range. The Route 53 Resolver also answers
	// at this range's base address plus two, which is why the predicate reads
	// it rather than only checking 169.254.169.253.
	IPv4CIDR string
	// IPv6CIDR is the IPv6 range, and it must be empty. Every security group
	// rule in this plan is written in IPv4, and an IPv6 range with an egress
	// only internet gateway is a full route to the internet that an IPv4
	// reading of the same security group calls contained.
	IPv6CIDR string
	// EnableDNSSupport is whether the Amazon provided resolver answers at all.
	EnableDNSSupport bool
}

// Subnet is one subnet the task may be placed in.
type Subnet struct {
	// ID is the subnet id.
	ID string
	// IPv4CIDR is its range.
	IPv4CIDR string
	// IPv6CIDR must be empty, for the reason on VPC.IPv6CIDR.
	IPv6CIDR string
	// MapPublicIPOnLaunch must be false.
	MapPublicIPOnLaunch bool
}

// AssignPublicIP is the awsVpcConfiguration field of the same name. A task with
// a public address in a subnet routed to an internet gateway is on the internet
// whatever else the plan says, so it is a field of its own rather than a
// property of the subnet.
type AssignPublicIP string

// The two values ECS accepts.
const (
	AssignPublicIPEnabled  AssignPublicIP = "ENABLED"
	AssignPublicIPDisabled AssignPublicIP = "DISABLED"
)

// RouteTable is the route table associated with every subnet in the plan.
type RouteTable struct {
	// ID is the table id.
	ID string
	// Routes are its entries.
	Routes []Route
	// AssignPublicIP is what awsVpcConfiguration asks for. It sits here rather
	// than on Subnet because it is a property of the task placement and not of
	// the subnet, and because putting it beside the routes is where a reader
	// checking "can this reach the internet" will look.
	AssignPublicIP AssignPublicIP
}

// Route is one route table entry.
type Route struct {
	// Destination is a CIDR or a prefix list id.
	Destination string
	// Target is what the traffic is handed to: "local", a VPC endpoint, a
	// gateway. The predicate reads its prefix, because AWS resource id
	// prefixes are the type: igw for an internet gateway, nat for a NAT
	// gateway, eigw for an egress only internet gateway, pcx for a peering
	// connection, tgw for a transit gateway, vgw for a virtual private
	// gateway, vpce for a VPC endpoint.
	Target string
}

// SecurityGroup is the group on the task ENI.
type SecurityGroup struct {
	// ID is the group id.
	ID string
	// Egress are the outbound rules. A security group permits only what its
	// rules name, so an empty list permits nothing outbound. It cannot be
	// empty here, because the image pull runs over this ENI.
	Egress []Rule
	// Ingress are the inbound rules.
	Ingress []Rule
}

// Rule is one security group rule.
type Rule struct {
	// Protocol is tcp, udp, icmp, or "-1" for every protocol.
	Protocol string
	// FromPort and ToPort bound the port range.
	FromPort int
	ToPort   int
	// CIDRv4 is an IPv4 destination, empty when the rule names something else.
	CIDRv4 string
	// CIDRv6 is an IPv6 destination.
	CIDRv6 string
	// PrefixListID is an AWS managed prefix list, which is how a gateway
	// endpoint's address ranges are named in a rule.
	PrefixListID string
	// PeerSecurityGroupID is another security group as the destination, which
	// is how the task is allowed to reach the interface endpoints without
	// naming an address range that will change.
	PeerSecurityGroupID string
}

// VPCEndpoint is one endpoint the environment can reach.
type VPCEndpoint struct {
	// Service is the full service name, such as
	// com.amazonaws.us-east-1.ecr.dkr.
	Service string
	// Type is Interface or Gateway.
	Type string
	// SecurityGroupID is the group on an interface endpoint's own ENIs.
	SecurityGroupID string
	// PolicyDocument is the endpoint policy. An endpoint with no policy allows
	// every action on every resource in that service that the caller has
	// credentials for, which for an anonymous caller is not much and for a
	// caller who found any credentials at all is the whole service.
	PolicyDocument string
	// PrivateDNSEnabled is whether the service's public name resolves to the
	// endpoint inside this VPC.
	PrivateDNSEnabled bool
}

// DNSFirewall is a Route 53 Resolver DNS Firewall rule group associated with
// the VPC.
type DNSFirewall struct {
	// RuleGroupID is the rule group.
	RuleGroupID string
	// AssociatedVPCID must be the plan's VPC. A rule group that exists and is
	// associated with a different VPC filters nothing here, and it is exactly
	// the sort of thing that reads as configured in a console screenshot.
	AssociatedVPCID string
	// FailOpen is the association's failure mode. True means that when DNS
	// Firewall cannot reach a rule, the query is allowed through, which turns
	// the containment into a best effort.
	FailOpen bool
	// Rules are the rule group's rules, and the predicate requires that the
	// last one by priority blocks every domain.
	Rules []DNSFirewallRule
}

// DNSFirewallRule is one rule in the group.
type DNSFirewallRule struct {
	// Priority orders the rules. Lower is evaluated first.
	Priority int
	// Domains are the domain patterns the rule matches. "*" is every domain.
	Domains []string
	// Action is ALLOW, BLOCK, or ALERT. ALERT logs and permits, which is worth
	// naming because it is the value somebody sets while tuning and leaves.
	Action string
}

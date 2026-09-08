// Package cloudrun is the GCP Cloud Run runtime, and like the ecs package
// beside it, it is a proof about containment rather than a placer of
// containers.
//
// Not MIT. This directory is covered by the Antifailure Enterprise License;
// see ee/LICENSE.md.
//
// It was written after the ECS lane and it deliberately does NOT inherit that
// lane's verdicts. Two clouds that both have a link local metadata address do
// not have the same containment problem, and the differences found here are
// large enough in both directions that reusing the AWS answer would have been
// wrong twice over:
//
//   - Cloud Run is EASIER to contain than Fargate in the place that decided the
//     ECS lane. On Fargate the image pull and the log push run over the task's
//     own network interface, so a security group that denies egress starts
//     nothing, and three paths are open by necessity. Cloud Run imports the
//     image at deploy time and Google states that "Container images are not
//     pulled from their container repository when a new Cloud Run instance is
//     started", so the registry is not on the instance's data path at all, and
//     the platform captures stdout and stderr without the container sending
//     anything. An environment here can be given a deny all egress rule and
//     still start.
//   - Cloud Run is HARDER to contain in the place the ECS lane closed. AWS lets
//     a task definition carry no task role, so 169.254.170.2 vends nothing.
//     Cloud Run has no equivalent: a service always runs as a service account,
//     the metadata server always answers, and Google documents the endpoint
//     that turns any request forgery in the application into an OAuth access
//     token. That path is open here and it was closed there.
//
// Every Google behaviour asserted in this package was read from Google's own
// documentation on 2026-09-08 and the page is named in the text. None of it is
// from memory and none of it is carried over from the AWS lane.
//
// What this package deliberately does NOT do is start an environment. Read
// egress.go first, because the enumeration there is the deliverable, and then
// runtime.go for why the refusal follows from it.
package cloudrun

// Plan is the whole Google Cloud configuration one environment would be given.
//
// It is one flat value rather than a client talking to Google, for the reason
// the ecs package gives at the same place: a predicate over a value can be
// evaluated by a test, printed in an error message and mutated one field at a
// time, while a predicate over a project can only be evaluated in a project
// somebody is paying for. A repository whose containment proof lives in an
// account has no containment proof on the day the credentials expire.
//
// Every field is a field of a real Cloud Run, Compute Engine or Cloud DNS
// resource. Nothing is a summary or a boolean standing in for a shape.
type Plan struct {
	// Project is the Google Cloud project the environment is placed in.
	Project string
	// Region is the Cloud Run region.
	Region string
	// Services are the Cloud Run services this environment is made of, in
	// manifest order.
	//
	// A slice rather than a single service because the count decides a verdict.
	// Cloud Run services reach each other over their own https URLs and there
	// is no other supported path: the Direct VPC page states that "Cloud Run
	// services and jobs don't support direct VPC ingress". So an environment
	// with two services has to be allowed to reach the address those URLs
	// resolve to, and an environment with one service does not.
	Services []Service
	// Identity is the service account every service in the environment runs
	// as, with the bindings it holds.
	Identity ServiceAccount
	// Network is the VPC network side of the environment.
	Network NetworkPlan
	// Perimeter is the VPC Service Controls perimeter the project sits in, or
	// nil.
	//
	// It is nil in everything this package generates and the field exists to
	// say so out loud. A perimeter is an organization level resource created
	// against an access policy, it covers a project rather than an
	// environment, and no runtime provider can create one for itself. Google's
	// own Cloud Run instructions treat it as the thing that stops
	// exfiltration through Google APIs, so its absence is part of why the
	// Google API path is reported open rather than narrowed.
	Perimeter *ServicePerimeter
}

// Service is one Cloud Run service, cut down to the fields that decide whether
// anything can get out.
type Service struct {
	// Name is the service name.
	Name string
	// Image is the image reference. It is imported at deploy time rather than
	// pulled at instance start, which is why it appears in no egress path.
	Image string
	// Ingress is the ingress setting, one of "all", "internal" or
	// "internal-and-cloud-load-balancing". The default is "all", and the
	// ingress page states that the default "allow any resource on the internet
	// to reach your Cloud Run resource".
	Ingress string
	// AllowUnauthenticated records an allUsers binding on the invoker role.
	// It must be false: a service anyone may invoke is a service every other
	// environment may invoke.
	AllowUnauthenticated bool
	// VPCEgress is the egress setting, "all-traffic" or "private-ranges-only".
	//
	// It is the single most important field in this file. On the default,
	// Google states that "Requests to public destinations continue to be
	// routed directly to the internet", which means the VPC firewall never
	// sees them and every rule written in it is decoration. Only "all-traffic"
	// puts the packets somewhere a rule can refuse them.
	VPCEgress string
	// Network and Subnet place the service on the VPC with Direct VPC egress.
	// Without both, there is no VPC in the path at all.
	Network string
	Subnet  string
	// NetworkTags are the revision level tags a firewall rule can target.
	NetworkTags []string
	// ServiceAccount is the identity the revision runs as. Empty means the
	// Compute Engine default service account, which the service identity page
	// states is used "If you don't specify a service account".
	ServiceAccount string
	// Volumes are the declared volume mounts. A Cloud Storage or NFS volume is
	// a read and write channel to a destination outside the environment that
	// the application does not have to open a socket to reach.
	Volumes []Volume
}

// Volume is one declared volume mount.
type Volume struct {
	// Name is the volume name.
	Name string
	// Type is the volume type: "cloud-storage", "nfs", "in-memory" or
	// "secret". The first two leave the environment.
	Type string
	// Target is the bucket or the export path.
	Target string
}

// ServiceAccount is the environment's identity and what it may do.
type ServiceAccount struct {
	// Email is the service account address. It must not be the project's
	// Compute Engine default service account, which is what Cloud Run uses
	// when a deployment names none.
	Email string
	// Bindings are the IAM role bindings this identity holds.
	//
	// The list is here rather than left implicit because the metadata server
	// hands this identity's access token to anything inside the container, so
	// the blast radius of that path is exactly this list. An empty list is the
	// closest thing Cloud Run has to the empty task role the ECS plan relies
	// on, and it is not the same thing: the endpoint still answers, the token
	// is still a valid Google credential, and the identity still exists.
	Bindings []Binding
}

// Binding is one IAM role binding held by the environment's identity.
type Binding struct {
	// Role is the role name, such as run.invoker.
	Role string
	// Resource is what the role is held on, in the form this plan uses for
	// Cloud Run services: projects/PROJECT/locations/REGION/services/NAME.
	Resource string
}

// NetworkPlan is the VPC network, its subnet, the firewall, the routes, the
// NAT gateways and the DNS configuration, which together are the containment.
type NetworkPlan struct {
	// Name is the VPC network name.
	Name string
	// Subnet is the subnet Cloud Run allocates instance addresses from. One
	// per environment, because Direct VPC egress gives every service on a
	// subnet an address in it, and a firewall rule written against a shared
	// subnet's range cannot tell one environment from the next.
	Subnet Subnet
	// FirewallRules are the VPC firewall rules. Order does not matter here
	// because every rule carries its own priority, which is what the predicate
	// reads.
	FirewallRules []FirewallRule
	// Routes are the custom routes in the network. Their next hops are how
	// traffic leaves for somewhere that is not the internet: a peering
	// connection, a Cloud VPN tunnel, an interconnect attachment.
	Routes []Route
	// NATGateways are the Cloud NAT gateways configured on the subnet. A NAT
	// gateway is what gives an instance with no external address a way to the
	// internet, so one attached to this subnet is a second route out that a
	// reading of the firewall alone would miss.
	NATGateways []NATGateway
	// DNS is the Cloud DNS configuration attached to this network.
	DNS DNSPlan
}

// Subnet is the subnet the environment's instances get addresses from.
type Subnet struct {
	// Name is the subnet name.
	Name string
	// IPv4Range is its primary range. Direct VPC egress requires a range of
	// at least a /26 and Cloud Run reserves addresses in blocks of 16.
	IPv4Range string
	// StackType is "IPV4_ONLY" or "IPV4_IPV6". A dual stack subnet gives the
	// environment IPv6 egress, and a firewall rule is written per IP version,
	// so a deny rule for every IPv4 destination says nothing about IPv6.
	StackType string
	// IPv6Range is the internal IPv6 range, empty on an IPv4 only subnet.
	IPv6Range string
	// PrivateGoogleAccess is whether instances with no external address can
	// reach Google APIs through the restricted virtual address range.
	PrivateGoogleAccess bool
}

// FirewallRule is one VPC firewall rule.
type FirewallRule struct {
	// Name is the rule name.
	Name string
	// Direction is "EGRESS" or "INGRESS".
	Direction string
	// Action is "allow" or "deny".
	Action string
	// Priority orders the rules. Lower is evaluated first. Google's Cloud Run
	// instructions state that a deny all egress rule must sit at a priority
	// after 1000, because the rules that carry Cloud Run's own traffic to the
	// service are at lower numbers.
	Priority int
	// DestinationRanges are the destinations an egress rule matches.
	DestinationRanges []string
	// TargetTags restricts the rule to instances carrying these network tags.
	// Empty means every instance in the network.
	TargetTags []string
	// Protocols are the protocol and port matches, in the form "tcp:443" or
	// "all".
	Protocols []string
}

// Route is one route in the VPC network.
type Route struct {
	// Name is the route name.
	Name string
	// DestRange is the destination range.
	DestRange string
	// NextHopKind is what the traffic is handed to. The predicate reads this
	// rather than a resource URL because the kind is the part that decides
	// whether the packet leaves: "default-internet-gateway", "peering",
	// "vpn-tunnel", "interconnect-attachment", "ncc-hub", "instance".
	NextHopKind string
}

// NATGateway is one Cloud NAT gateway.
type NATGateway struct {
	// Name is the gateway name.
	Name string
	// Router is the Cloud Router that programs it.
	Router string
	// Subnets are the subnets whose instances it translates for.
	Subnets []string
}

// DNSPlan is the Cloud DNS configuration attached to the network.
//
// It is a struct of its own because name resolution on Google Cloud is not
// decided by the firewall at all. Google states that a VM's queries go to the
// metadata server and that "VPC firewall rules and hierarchical firewall
// policies do not apply" to that address, so whatever the firewall says, the
// question in a DNS query is answered by whatever this struct describes.
type DNSPlan struct {
	// ResponsePolicy is the network's single response policy. Google states
	// that "you can only attach one response policy per network", which is why
	// this is a pointer to one rather than a list.
	ResponsePolicy *ResponsePolicy
	// OutboundServerPolicy names alternative name servers for the network.
	//
	// This is the only mechanism Google documents that sends EVERY query
	// somewhere of the operator's choosing, and it is therefore the only
	// candidate for closing the resolver as a data channel. Whether it reaches
	// a Cloud Run instance's queries is the thing this lane could not settle.
	// See checkResolverRecursion.
	OutboundServerPolicy *OutboundServerPolicy
}

// ResponsePolicy is a Cloud DNS response policy attached to the network.
type ResponsePolicy struct {
	// Name is the policy name.
	Name string
	// Networks are the VPC networks it is attached to. A policy attached to a
	// different network filters nothing here and reads as configured in a
	// console screenshot.
	Networks []string
	// Rules are its rules.
	Rules []ResponsePolicyRule
}

// ResponsePolicyRule is one rule in a response policy.
type ResponsePolicyRule struct {
	// Name is the rule name.
	Name string
	// DNSName is the name or wildcard the rule matches, with the trailing dot
	// Google's examples carry.
	DNSName string
	// LocalData are the records served in place of the real answer. This is
	// the whole of what a response policy can do to a name: it answers
	// differently. There is no block action, which is the difference between
	// this and the rule group the ECS plan relies on.
	LocalData []string
	// Behavior is "bypassResponsePolicy" for a passthru rule, empty
	// otherwise.
	Behavior string
}

// OutboundServerPolicy is a Cloud DNS server policy with alternative name
// servers.
type OutboundServerPolicy struct {
	// Name is the policy name.
	Name string
	// Network is the VPC network it applies to.
	Network string
	// AlternativeNameServers are the addresses queries are sent to. Google
	// classifies an address in a private range as a name server inside the
	// network and routes the query through the network; an address outside
	// one is treated as reachable on the internet and the query is routed
	// there, which would make this field the exfiltration channel rather than
	// the fix.
	AlternativeNameServers []string
	// PrivateRouting forces the private classification regardless of the
	// address.
	PrivateRouting bool
}

// ServicePerimeter is a VPC Service Controls perimeter.
type ServicePerimeter struct {
	// Name is the perimeter name.
	Name string
	// Projects are the projects inside it.
	Projects []string
	// RestrictedServices are the Google services it protects.
	RestrictedServices []string
}

// RestrictedVIPRange is the address range Google publishes for
// restricted.googleapis.com, which is the only Google API endpoint that
// answers inside a network with no route to the internet.
//
// It is a constant because it appears in a route, in a firewall rule, in a
// response policy rule and in three checks, and a range that differs between
// them is a plan that does not do what its author read.
const RestrictedVIPRange = "199.36.153.4/30"

package cloudrun

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Name is the value runtime.provider takes in a manifest to reach this
// package.
const Name = "cloudrun"

// Provider is the registered runtime provider for GCP Cloud Run.
//
// It never returns a runtime, and that is the deliverable rather than a stage
// on the way to one. Read Open.
type Provider struct {
	// Getenv reads the project the environment would be placed in. It is a
	// field rather than a call to os.Getenv so that a test can drive the
	// generator without setting process wide state, which is the mistake that
	// makes one test's environment leak into another's.
	Getenv func(string) string
}

// The environment variables that describe the installation's project and
// network.
//
// Environment variables rather than manifest fields deliberately. A project
// id, a network name and a subnet range are properties of one organization's
// Google Cloud, they differ per installation, and putting them in
// antifailure.yaml would mean a repository's manifest carried one company's
// network layout into every fork of it.
const (
	EnvProject     = "AF_CLOUDRUN_PROJECT"
	EnvRegion      = "AF_CLOUDRUN_REGION"
	EnvNetwork     = "AF_CLOUDRUN_NETWORK"
	EnvSubnetRange = "AF_CLOUDRUN_SUBNET_RANGE"
	EnvIdentity    = "AF_CLOUDRUN_SERVICE_ACCOUNT"
	EnvResolver    = "AF_CLOUDRUN_RESOLVER"
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
// claim must not soften to make a cloud runtime possible. The Kubernetes
// runtime is allowed to exist because it creates its own NetworkPolicy objects
// and then runs a pod under them that tries to escape four ways before any
// application image starts, refusing the environment when any attempt
// succeeds. Cloud Run cannot have that probe, for a different reason than ECS
// could not, and the difference is worth stating because it is the whole
// finding of this lane.
//
// On Fargate the blocker was that the environment could not start under the
// policy: the image pull and the log push run over the task's own interface,
// so a security group that denies egress runs nothing. That is not true here.
// Google states that container images "are not pulled from their container
// repository when a new Cloud Run instance is started", and the platform
// captures the log streams itself, so an environment here starts under a deny
// all egress rule. The generated configuration is genuinely tighter than the
// ECS one, and one path that is permanently open there is closed here.
//
// The blocker here is the other half. Two of the paths that remain open have
// no configuration that closes them and one cannot be decided at all:
//
//  1. The metadata server vends an OAuth access token for the service
//     identity, Cloud Run cannot run a service with no identity, and the
//     firewall documentation states that VPC firewall rules do not apply to
//     that address. The ECS plan closes its equivalent by carrying no task
//     role. There is no such field here, and there is no per container answer
//     either: the plan this lane came from described the metadata server as
//     reachable only to the sidecar, and Google's page on deploying multiple
//     containers states that "All containers within an instance share the same
//     network namespace". One paragraph of the container contract says the
//     opposite in passing, that containers run under network namespaces
//     "isolating them from each other", and the mechanism settles which is
//     operative: the same contract states that Cloud Run writes an /etc/hosts
//     entry mapping container names to 127.0.0.1 so that containers reach each
//     other on localhost, which is what a shared network namespace is.
//  2. Everything the application prints leaves the environment into the
//     project's logs, with no socket, no rule and no route involved.
//  3. Whether the resolver can be pointed somewhere that does not recurse is
//     undecided, because the mechanism Google documents states its
//     precondition as a VM and Cloud Run instances are not VMs.
//
// A probe that tried to escape and failed would say nothing about any of
// those, because the first two are not failures to escape: they are documented
// features working as designed. So the escape probe that earns a runtime its
// existence would come back green on an environment that still has a token
// endpoint and a log pipe in it.
//
// What this package therefore does is publish the enumeration, generate the
// configuration, prove the predicate over that configuration in continuous
// integration on a machine with no Google Cloud project, and refuse.
func (p *Provider) Open(_ context.Context, cfg extension.RuntimeConfig) (provider.Runtime, error) {
	in, missing := p.inputs()
	if len(missing) > 0 {
		return nil, fmt.Errorf("the cloudrun runtime is not available, and this installation has "+
			"not described a project for it either: %s are unset. Even fully described it would "+
			"refuse, for the reason af prints when they are set. Use runtime.provider kubernetes "+
			"against GKE, which is proved contained by a probe that runs before any application "+
			"image", strings.Join(missing, ", "))
	}
	envID := environmentID(cfg)
	report := Evaluate(Generate(in, envID))
	single := Evaluate(Generate(in.withServices("web"), envID))

	var b strings.Builder
	b.WriteString("the cloudrun runtime refuses to start an environment, on purpose.\n\n")
	b.WriteString(report.String())
	fmt.Fprintf(&b, "\nThe report above is for an example environment of %d services. An "+
		"environment of exactly one service closes %d of %d instead, because it needs no service "+
		"to service call and therefore no rule allowing %s.\n",
		len(in.Services), single.Closed, single.Total, RestrictedVIPRange)
	b.WriteString("\nCloud Run is easier to contain than Fargate in the place that decided the " +
		"ecs lane, because the image is imported at deploy time rather than pulled at instance " +
		"start, so an environment here starts under a deny all egress rule. It is harder in the " +
		"place that lane closed: a Fargate task may carry no task role, and Cloud Run has no way " +
		"to run a service with no identity, so the metadata server vends a token whatever else " +
		"this plan says. Two open paths have no field that closes them and one is undecided by " +
		"Google's own documentation. Use runtime.provider kubernetes against GKE, where a " +
		"NetworkPolicy closes the resolver along with everything else and the probe that proves " +
		"it runs before any application image.\n")
	return nil, fmt.Errorf("%s", b.String())
}

// inputs reads the installation description and reports what is missing.
func (p *Provider) inputs() (Inputs, []string) {
	get := p.Getenv
	if get == nil {
		get = os.Getenv
	}
	in := Inputs{
		Project:     strings.TrimSpace(get(EnvProject)),
		Region:      strings.TrimSpace(get(EnvRegion)),
		Network:     strings.TrimSpace(get(EnvNetwork)),
		SubnetRange: strings.TrimSpace(get(EnvSubnetRange)),
		Identity:    strings.TrimSpace(get(EnvIdentity)),
		Resolver:    strings.TrimSpace(get(EnvResolver)),
		// The example the report is generated for. A RuntimeConfig carries the
		// manifest's runtime block and not its services, so the provider
		// cannot know how many services this repository declares. Two is the
		// shape that decides a verdict, so the report is generated for two and
		// the refusal says out loud that one service closes a further path.
		Services: []string{"web", "worker"},
	}
	var missing []string
	for _, pair := range []struct {
		name  string
		empty bool
	}{
		{EnvProject, in.Project == ""},
		{EnvRegion, in.Region == ""},
		{EnvNetwork, in.Network == ""},
		{EnvSubnetRange, in.SubnetRange == ""},
		{EnvIdentity, in.Identity == ""},
		{EnvResolver, in.Resolver == ""},
	} {
		if pair.empty {
			missing = append(missing, pair.name)
		}
	}
	return in, missing
}

// environmentID is a stable name for the environment the plan is generated
// for.
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

// Inputs are the installation facts a plan is generated from.
type Inputs struct {
	// Project is the Google Cloud project.
	Project string
	// Region is the Cloud Run region.
	Region string
	// Network is the VPC network name.
	Network string
	// SubnetRange is the range of THIS environment's own subnet, at least a
	// /26 because that is what Direct VPC egress requires.
	//
	// One range per environment rather than one shared range, and that is a
	// requirement rather than a preference. Google states that Cloud Run
	// instance addresses "are ephemeral, so don't create policies based on
	// individual IPs" and that a policy "must use the IP address range of the
	// entire subnet". So a subnet is the finest destination a rule can name,
	// and two environments in one subnet cannot be separated by any rule that
	// can be written.
	SubnetRange string
	// Identity is the service account the environment runs as. It must not be
	// the project's Compute Engine default account.
	Identity string
	// Resolver is the address, inside SubnetRange, of the name server the
	// network's outbound server policy sends queries to.
	Resolver string
	// Services are the Cloud Run services the environment is made of.
	Services []string
}

// withServices returns the inputs with a different service list, which is how
// the refusal reports the single service case without a second set of
// variables.
func (in Inputs) withServices(names ...string) Inputs {
	out := in
	out.Services = names
	return out
}

// Generate builds the configuration one environment would be given.
//
// This is the function the predicate is a predicate about. It is deterministic
// and it takes no clock and no randomness, so the plan for a given
// installation and environment is the same every time.
//
// Everything it emits is the tightest shape the paths in egress.go allow.
// Where a path cannot be closed, this function does not pretend: it still
// emits the rule that lets an environment of more than one service work, and
// takes the open verdict, because a plan that omitted it would score better
// and run nothing.
func Generate(in Inputs, envID string) Plan {
	tag := "af-" + envID
	subnet := Subnet{
		Name:      tag,
		IPv4Range: in.SubnetRange,
		StackType: StackTypeIPv4Only,
		// Turned on only when something in the environment has to reach a
		// Google API, which on this design is only the service to service
		// call. Private Google Access is what makes the restricted range
		// answer at all, so leaving it off is part of closing that path for a
		// single service environment rather than a cosmetic default.
		PrivateGoogleAccess: len(in.Services) > 1,
	}

	services := make([]Service, 0, len(in.Services))
	for _, name := range in.Services {
		services = append(services, Service{
			Name:  name,
			Image: fmt.Sprintf("%s-docker.pkg.dev/%s/antifailure/%s:%s", in.Region, in.Project, envID, name),
			// Internal rather than the default. The ingress page states that
			// the default lets any resource on the internet reach the service,
			// and an environment that is a twin of production has no business
			// being reachable from there.
			Ingress:              IngressInternal,
			AllowUnauthenticated: false,
			// The single most important field in the file. On the default
			// setting Google routes requests to public destinations straight
			// to the internet, where no rule in this network is on the path,
			// and every firewall rule below would be decoration.
			VPCEgress:      EgressAllTraffic,
			Network:        in.Network,
			Subnet:         subnet.Name,
			NetworkTags:    []string{tag},
			ServiceAccount: in.Identity,
			Volumes:        nil,
		})
	}

	plan := Plan{
		Project:  in.Project,
		Region:   in.Region,
		Services: services,
		Identity: ServiceAccount{Email: in.Identity},
		Network: NetworkPlan{
			Name:   in.Network,
			Subnet: subnet,
			FirewallRules: []FirewallRule{
				{
					// Traffic inside the environment, which is not egress: a
					// service reaching this environment's own database alias
					// never leaves the environment and is not decided against
					// the egress policy.
					Name:              tag + "-allow-own-subnet",
					Direction:         "EGRESS",
					Action:            "allow",
					Priority:          900,
					DestinationRanges: []string{in.SubnetRange},
					TargetTags:        []string{tag},
					Protocols:         []string{"all"},
				},
				{
					// Priority 1100 rather than a lower number, because
					// Google's own Cloud Run instructions state that a deny
					// all egress rule must sit at a priority after 1000 or the
					// traffic that carries requests to the service is blocked
					// as well. A tighter number here would not be tighter
					// containment, it would be an environment that never
					// serves.
					Name:              tag + "-deny-all-ipv4",
					Direction:         "EGRESS",
					Action:            "deny",
					Priority:          1100,
					DestinationRanges: []string{"0.0.0.0/0"},
					TargetTags:        []string{tag},
					Protocols:         []string{"all"},
				},
				{
					// The IPv6 twin, emitted even though the subnet is IPv4
					// only. A rule's destination ranges are of one IP version,
					// so this is the object that keeps the containment true on
					// the day somebody makes the subnet dual stack.
					Name:              tag + "-deny-all-ipv6",
					Direction:         "EGRESS",
					Action:            "deny",
					Priority:          1100,
					DestinationRanges: []string{"::/0"},
					TargetTags:        []string{tag},
					Protocols:         []string{"all"},
				},
			},
			DNS: DNSPlan{
				ResponsePolicy: &ResponsePolicy{
					Name:     tag + "-responses",
					Networks: []string{in.Network},
					Rules: []ResponsePolicyRule{
						{
							Name:      "googleapis",
							DNSName:   "*.googleapis.com.",
							LocalData: restrictedVIPAddresses(),
						},
						{
							Name:      "run-app",
							DNSName:   "*.run.app.",
							LocalData: restrictedVIPAddresses(),
						},
					},
				},
				// Emitted alongside the response policy, and the interaction
				// between the two is a fact about Cloud DNS rather than an
				// oversight here. The name resolution order consults the
				// alternative name server FIRST, and Google warns that using
				// one "disables the resolution of many Cloud DNS features". So
				// the resolver this policy names has to serve the two names
				// above itself, with the same answers, and the response policy
				// is what applies to the network when no outbound policy is
				// attached. Both objects carry the same answer on purpose.
				OutboundServerPolicy: &OutboundServerPolicy{
					Name:                   tag + "-resolver",
					Network:                in.Network,
					AlternativeNameServers: []string{in.Resolver},
					PrivateRouting:         true,
				},
			},
		},
		// Nil, and it is a statement rather than an omission. A perimeter
		// covers a project, it is created against an organization's access
		// policy, and no runtime provider can make one for itself.
		Perimeter: nil,
	}

	// The allowance an environment of more than one service cannot do without,
	// and the route that makes the range answer. Both are absent for a single
	// service environment, which is the only shape of environment this design
	// can fully close that path for.
	if len(in.Services) > 1 {
		plan.Network.FirewallRules = append(plan.Network.FirewallRules, FirewallRule{
			Name:              tag + "-allow-restricted-google",
			Direction:         "EGRESS",
			Action:            "allow",
			Priority:          950,
			DestinationRanges: []string{RestrictedVIPRange},
			TargetTags:        []string{tag},
			Protocols:         []string{"tcp:443"},
		})
		plan.Network.Routes = append(plan.Network.Routes, Route{
			Name:        tag + "-restricted-google",
			DestRange:   RestrictedVIPRange,
			NextHopKind: NextHopInternetGateway,
		})
		for _, s := range services {
			plan.Identity.Bindings = append(plan.Identity.Bindings, Binding{
				Role:     RoleInvoker,
				Resource: serviceResource(plan, s.Name),
			})
		}
	}
	return plan
}

// restrictedVIPAddresses is the four addresses in the restricted range, which
// is what a response policy rule serves in place of the real answer.
func restrictedVIPAddresses() []string {
	return []string{"199.36.153.4", "199.36.153.5", "199.36.153.6", "199.36.153.7"}
}

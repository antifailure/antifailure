// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.

package ecs

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Name is the value runtime.provider takes in a manifest to reach this
// package.
const Name = "ecs"

// Provider is the registered runtime provider for AWS ECS on Fargate.
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

// The environment variables that describe the account's network.
//
// They are environment variables rather than manifest fields deliberately. A
// VPC id, a subnet id and a security group id are properties of an
// organisation's AWS account, they differ per installation, and putting them in
// antifailure.yaml would mean a repository's manifest carried one company's
// network layout into every fork of it.
const (
	EnvRegion     = "AF_ECS_REGION"
	EnvCluster    = "AF_ECS_CLUSTER"
	EnvVPC        = "AF_ECS_VPC_ID"
	EnvVPCCIDR    = "AF_ECS_VPC_CIDR"
	EnvSubnets    = "AF_ECS_SUBNET_IDS"
	EnvSubnetCIDR = "AF_ECS_SUBNET_CIDRS"
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
// The plan this repository works from carries a standing risk in section 10:
// the containment claim must not soften to make a cloud runtime possible, and
// if ECS cannot be proved contained then the honest answer is that Antifailure
// runs on EKS and not on raw ECS. The Kubernetes runtime is allowed to exist
// because it creates the NetworkPolicy objects itself and then runs a pod under
// them that tries to escape four ways before any application image starts. Two
// properties make that possible and ECS on Fargate has neither.
//
// The first is that the image pull happens outside the policy. A kubelet pulls
// on the node, so a pod can be denied every egress rule and still start. On
// Fargate platform version 1.4.0 the ECR login, the image pull and the log push
// all run over the task ENI under the task's own security group, and AWS states
// that a Fargate task must have a route to the registry to pull an image. So a
// security group that denies egress is not a configuration that runs an
// environment, it is a configuration that runs nothing, and the row of the plan
// that describes this lane as "a security group that denies egress" describes
// something that cannot exist.
//
// The second is that the enforcement can be observed cheaply. A kind cluster on
// this machine will tell you in ninety seconds whether its CNI enforces a
// NetworkPolicy. Whether AWS enforces a route table is a fact about an account,
// and the plan's own standing risk says no test may need a cloud account. So
// the escape probe that earns a runtime its existence has never run here.
//
// What this package therefore does is publish the enumeration, generate the
// configuration, prove the predicate over that configuration in continuous
// integration, and refuse. A runtime that came up and could not prove it had no
// egress would be worse than no runtime at all, because from the outside it
// looks exactly like one that can.
func (p *Provider) Open(_ context.Context, cfg extension.RuntimeConfig) (provider.Runtime, error) {
	in, missing := p.inputs()
	if len(missing) > 0 {
		return nil, fmt.Errorf("the ecs runtime is not available, and this installation has not "+
			"described a network for it either: %s are unset. Even fully described it would "+
			"refuse, for the reason af prints when they are set. Use runtime.provider "+
			"kubernetes against EKS, which is proved contained by a probe that runs before "+
			"any application image", strings.Join(missing, ", "))
	}
	report := Evaluate(Generate(in, environmentID(cfg)))

	var b strings.Builder
	b.WriteString("the ecs runtime refuses to start an environment, on purpose.\n\n")
	b.WriteString(report.String())
	b.WriteString("\nThe Kubernetes runtime is allowed to exist because it creates its own " +
		"NetworkPolicy objects and then runs a pod under them that tries to escape before any " +
		"application image starts. On Fargate the image pull runs over the task ENI under the " +
		"task's own security group, so a security group that denies egress starts nothing, and " +
		"whether AWS enforces the rest is a fact about an account that no test in this " +
		"repository is allowed to need. Use runtime.provider kubernetes against EKS.\n")
	return nil, fmt.Errorf("%s", b.String())
}

// inputs reads the network description and reports what is missing.
func (p *Provider) inputs() (Inputs, []string) {
	get := p.Getenv
	if get == nil {
		get = os.Getenv
	}
	in := Inputs{
		Region:      strings.TrimSpace(get(EnvRegion)),
		Cluster:     strings.TrimSpace(get(EnvCluster)),
		VPCID:       strings.TrimSpace(get(EnvVPC)),
		VPCCIDR:     strings.TrimSpace(get(EnvVPCCIDR)),
		SubnetIDs:   splitList(get(EnvSubnets)),
		SubnetCIDRs: splitList(get(EnvSubnetCIDR)),
	}
	var missing []string
	for _, pair := range []struct {
		name  string
		empty bool
	}{
		{EnvRegion, in.Region == ""},
		{EnvCluster, in.Cluster == ""},
		{EnvVPC, in.VPCID == ""},
		{EnvVPCCIDR, in.VPCCIDR == ""},
		{EnvSubnets, len(in.SubnetIDs) == 0},
		{EnvSubnetCIDR, len(in.SubnetCIDRs) == 0},
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

// splitList parses a comma separated environment variable.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Inputs are the account facts a plan is generated from.
type Inputs struct {
	Region      string
	Cluster     string
	VPCID       string
	VPCCIDR     string
	SubnetIDs   []string
	SubnetCIDRs []string
}

// Generate builds the configuration one environment would be given.
//
// This is the function the predicate is a predicate about. It is deterministic
// and it takes no clock and no randomness, so the plan for a given account and
// environment is the same every time and a golden file over it is a real
// regression test rather than a snapshot of one run.
//
// Everything it emits is the tightest shape the paths in egress.go allow. Where
// a path cannot be closed, this function does not pretend: it still emits the
// endpoints the image pull needs, because a plan that omitted them would score
// better and start nothing.
func Generate(in Inputs, envID string) Plan {
	sg := "sg-" + envID
	endpointSG := "sg-" + envID + "-endpoints"

	subnets := make([]Subnet, 0, len(in.SubnetIDs))
	for i, id := range in.SubnetIDs {
		cidr := ""
		if i < len(in.SubnetCIDRs) {
			cidr = in.SubnetCIDRs[i]
		}
		subnets = append(subnets, Subnet{ID: id, IPv4CIDR: cidr})
	}

	endpoints := []VPCEndpoint{
		interfaceEndpoint(in.Region, "ecr.api", endpointSG, envID),
		interfaceEndpoint(in.Region, "ecr.dkr", endpointSG, envID),
		interfaceEndpoint(in.Region, "logs", endpointSG, envID),
		{
			Service:         fmt.Sprintf("com.amazonaws.%s.s3", in.Region),
			Type:            "Gateway",
			PolicyDocument:  s3EndpointPolicy(in.Region),
			SecurityGroupID: "",
		},
	}

	// Egress names the endpoints' security group rather than an address range,
	// because an interface endpoint's ENI addresses are assigned by AWS and a
	// rule written against them is wrong the first time one is replaced.
	egress := []Rule{
		{Protocol: "tcp", FromPort: 443, ToPort: 443, PeerSecurityGroupID: endpointSG},
		{Protocol: "tcp", FromPort: 443, ToPort: 443, PrefixListID: "pl-" + in.Region + "-s3"},
	}
	// And one rule per subnet for traffic inside the environment, which is not
	// egress: a service reaching another service, the sidecar or the database
	// alias never leaves this environment and is not decided against the
	// egress policy.
	for _, s := range subnets {
		if s.IPv4CIDR != "" {
			egress = append(egress, Rule{Protocol: "-1", CIDRv4: s.IPv4CIDR})
		}
	}

	return Plan{
		Region:  in.Region,
		Cluster: in.Cluster,
		// Pinned rather than left to LATEST. Below 1.4.0 a task carries a
		// second Fargate owned ENI for image pulls and secret retrieval that
		// no security group in this plan describes and that does not appear in
		// VPC flow logs, so a plan on an older platform version is making a
		// claim about traffic it cannot see.
		LaunchType:      "FARGATE",
		PlatformVersion: "1.4.0",
		TaskDefinition: TaskDefinition{
			Family:      envID,
			NetworkMode: "awsvpc",
			// Empty, and it is the single most important field in the file.
			// 169.254.170.2 cannot be turned off, so the only way it vends
			// nothing is for there to be nothing to vend.
			TaskRoleARN:          "",
			ExecutionRoleARN:     fmt.Sprintf("arn:aws:iam::*:role/%s-execution", envID),
			EnableExecuteCommand: false,
		},
		Network: NetworkPlan{
			VPC: VPC{
				ID:       in.VPCID,
				IPv4CIDR: in.VPCCIDR,
				IPv6CIDR: "",
				// Left ON, and the DNS firewall closes the resolver path
				// instead. Turning it off would also close it, and it would
				// break the private DNS names of the very interface endpoints
				// the image pull needs, so the environment would stop starting.
				EnableDNSSupport: true,
			},
			Subnets:       subnets,
			SecurityGroup: SecurityGroup{ID: sg, Egress: egress},
			RouteTable: RouteTable{
				ID:             "rtb-" + envID,
				AssignPublicIP: AssignPublicIPDisabled,
				Routes: []Route{
					{Destination: in.VPCCIDR, Target: "local"},
					{Destination: "pl-" + in.Region + "-s3", Target: "vpce-" + envID + "-s3"},
				},
			},
			Endpoints: endpoints,
			DNSFirewall: &DNSFirewall{
				RuleGroupID:     "rslvr-frg-" + envID,
				AssociatedVPCID: in.VPCID,
				FailOpen:        false,
				Rules: []DNSFirewallRule{
					// The allow rules come first by priority and name only what
					// the environment is obliged to resolve. The block rule is
					// last, so a name that matched nothing above is refused
					// rather than resolved.
					{Priority: 10, Action: "ALLOW", Domains: allowedDomains(in.Region)},
					{Priority: 1000, Action: "BLOCK", Domains: []string{"*"}},
				},
			},
		},
	}
}

// interfaceEndpoint builds one interface endpoint with a policy.
func interfaceEndpoint(region, service, sg, envID string) VPCEndpoint {
	return VPCEndpoint{
		Service:           fmt.Sprintf("com.amazonaws.%s.%s", region, service),
		Type:              "Interface",
		SecurityGroupID:   sg,
		PrivateDNSEnabled: true,
		PolicyDocument:    endpointPolicy(service, envID),
	}
}

// endpointPolicy narrows an interface endpoint to the environment's own
// repository or log group.
//
// An endpoint with no policy permits every action in that service that the
// caller has credentials for. The container has no credentials while the task
// role is absent, so today this narrows very little, and it is written anyway
// because the day somebody adds a task role is the day it is the only thing
// standing between this environment and the whole service.
func endpointPolicy(service, envID string) string {
	switch service {
	case "ecr.api", "ecr.dkr":
		return fmt.Sprintf(`{"Statement":[{"Effect":"Allow","Principal":"*","Action":`+
			`["ecr:GetAuthorizationToken","ecr:BatchGetImage","ecr:GetDownloadUrlForLayer"],`+
			`"Resource":"arn:aws:ecr:*:*:repository/%s*"}]}`, envID)
	case "logs":
		return fmt.Sprintf(`{"Statement":[{"Effect":"Allow","Principal":"*","Action":`+
			`["logs:CreateLogStream","logs:PutLogEvents"],`+
			`"Resource":"arn:aws:logs:*:*:log-group:/antifailure/%s:*"}]}`, envID)
	default:
		return `{"Statement":[{"Effect":"Deny","Principal":"*","Action":"*","Resource":"*"}]}`
	}
}

// s3EndpointPolicy narrows the gateway endpoint to the buckets ECR serves
// layers from in this region.
func s3EndpointPolicy(region string) string {
	return fmt.Sprintf(`{"Statement":[{"Effect":"Allow","Principal":"*","Action":`+
		`["s3:GetObject"],"Resource":"arn:aws:s3:::prod-%s-starport-layer-bucket/*"}]}`, region)
}

// allowedDomains is what the DNS firewall lets through, which is only what the
// image pull and the log push have to resolve.
func allowedDomains(region string) []string {
	out := []string{
		fmt.Sprintf("api.ecr.%s.amazonaws.com", region),
		fmt.Sprintf("dkr.ecr.%s.amazonaws.com", region),
		fmt.Sprintf("logs.%s.amazonaws.com", region),
		fmt.Sprintf("prod-%s-starport-layer-bucket.s3.%s.amazonaws.com", region, region),
	}
	sort.Strings(out)
	return out
}

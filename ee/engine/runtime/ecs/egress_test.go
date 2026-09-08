package ecs_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/runtime/ecs"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// reference is the account description the generated plan is built from
// throughout these tests. One value, so that a test that changes the plan is
// visibly changing it rather than quietly using a different account.
func reference() ecs.Inputs {
	return ecs.Inputs{
		Region:      "us-east-1",
		Cluster:     "antifailure",
		VPCID:       "vpc-0af1",
		VPCCIDR:     "10.20.0.0/16",
		SubnetIDs:   []string{"subnet-0a", "subnet-0b"},
		SubnetCIDRs: []string{"10.20.1.0/24", "10.20.2.0/24"},
	}
}

func referencePlan() ecs.Plan { return ecs.Generate(reference(), "af-example") }

// expected is the verdict every path gets on the generated plan.
//
// It is written out in full rather than computed, because the point of the
// table is to be a thing a person disagrees with. A test that asserted "the
// report matches Evaluate" would pass for any predicate at all.
var expected = map[string]ecs.Verdict{
	"public-ipv4-through-a-gateway":              ecs.Closed,
	"public-ipv6-through-an-egress-only-gateway": ecs.Closed,
	"public-resolver-over-udp":                   ecs.Closed,
	"the-amazon-provided-resolver":               ecs.Closed,
	"the-ec2-instance-metadata-service":          ecs.Unproven,
	"the-task-role-credentials-endpoint":         ecs.Closed,
	"the-task-metadata-endpoint":                 ecs.Open,
	"the-interface-endpoints-the-image-pull-needs": ecs.Open,
	"the-s3-gateway-endpoint-the-layers-come-from": ecs.Open,
	"the-ecs-exec-channel":                         ecs.Closed,
	"a-peering-transit-or-private-gateway-route":   ecs.Closed,
	"a-neighbouring-environment":                   ecs.Closed,
	"the-local-amazon-time-sync-service":           ecs.Unproven,
}

// TestTheGeneratedPlanGetsTheVerdictItShould is the baseline every mutation
// below is measured against.
func TestTheGeneratedPlanGetsTheVerdictItShould(t *testing.T) {
	report := ecs.Evaluate(referencePlan())
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
// It is deliberately not 13 of 13. Three of the paths cannot be closed on ECS
// at all and two cannot be decided without an AWS account, so a green here at
// 13 would mean somebody had shortened the enumeration.
func TestTheNumber(t *testing.T) {
	report := ecs.Evaluate(referencePlan())
	require.Equal(t, 13, report.Total, "egress paths enumerated")
	require.Equal(t, 8, report.Closed, "closed by the generated configuration")
	require.Equal(t, 3, report.Open, "open, and named")
	require.Equal(t, 2, report.Unproven, "not decided by the configuration or the documentation")
	require.False(t, report.Contained(), "this plan must not report itself contained")
	require.Len(t, report.NotClosed(), 5)
}

// mutation is one break in the generated plan and the paths it should move.
type mutation struct {
	// path is the path whose check the break is aimed at.
	path string
	// what describes the break, for the failure message.
	what string
	// apply breaks the plan.
	apply func(*ecs.Plan)
	// alsoOpens are paths that legitimately move as well, named so that a
	// break which moves everything cannot pass by moving its target too.
	alsoOpens []string
}

// mutations is the mutation test, run rather than reported.
//
// Every path this package calls Closed has one entry, because a verdict that
// cannot become Open is not a check. The repository's rule is one break per
// assertion, and that is what the alsoOpens field enforces: a break has to move
// its own path and the named ones and nothing else, so a predicate that
// returned Open for everything fails on the first row.
func mutations() []mutation {
	return []mutation{
		{
			path: "public-ipv4-through-a-gateway",
			what: "a default route to an internet gateway",
			apply: func(p *ecs.Plan) {
				p.Network.RouteTable.Routes = append(p.Network.RouteTable.Routes,
					ecs.Route{Destination: "0.0.0.0/0", Target: "igw-0bad"})
			},
			alsoOpens: []string{"public-resolver-over-udp"},
		},
		{
			path: "public-ipv4-through-a-gateway",
			what: "a public address on the task ENI",
			apply: func(p *ecs.Plan) {
				p.Network.RouteTable.AssignPublicIP = ecs.AssignPublicIPEnabled
			},
		},
		{
			path: "public-ipv4-through-a-gateway",
			what: "an egress rule naming a public range",
			apply: func(p *ecs.Plan) {
				p.Network.SecurityGroup.Egress = append(p.Network.SecurityGroup.Egress,
					ecs.Rule{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDRv4: "0.0.0.0/0"})
			},
			// It also opens the resolver path, because a rule covering every
			// address on port 443 covers port 53 only if the range does; this
			// one does not, so the resolver path is untouched. It opens the
			// neighbour path, because 0.0.0.0/0 is wider than the subnets.
			alsoOpens: []string{"a-neighbouring-environment"},
		},
		{
			path: "public-ipv6-through-an-egress-only-gateway",
			what: "an IPv6 range on the VPC",
			apply: func(p *ecs.Plan) { p.Network.VPC.IPv6CIDR = "2600:1f18::/56" },
		},
		{
			path: "public-ipv6-through-an-egress-only-gateway",
			what: "an egress only internet gateway route",
			apply: func(p *ecs.Plan) {
				p.Network.RouteTable.Routes = append(p.Network.RouteTable.Routes,
					ecs.Route{Destination: "::/0", Target: "eigw-0bad"})
			},
		},
		{
			path: "public-resolver-over-udp",
			what: "an egress rule reaching port 53 on a public resolver",
			apply: func(p *ecs.Plan) {
				p.Network.SecurityGroup.Egress = append(p.Network.SecurityGroup.Egress,
					ecs.Rule{Protocol: "udp", FromPort: 53, ToPort: 53, CIDRv4: "1.1.1.1/32"})
			},
			// A public range in an egress rule is also exactly what the first
			// path checks for, and it should be found by both.
			alsoOpens: []string{"public-ipv4-through-a-gateway", "a-neighbouring-environment"},
		},
		{
			path:  "the-amazon-provided-resolver",
			what:  "a DNS firewall association that fails open",
			apply: func(p *ecs.Plan) { p.Network.DNSFirewall.FailOpen = true },
		},
		{
			path: "the-amazon-provided-resolver",
			what: "a last rule that alerts instead of blocking",
			apply: func(p *ecs.Plan) {
				rules := p.Network.DNSFirewall.Rules
				rules[len(rules)-1].Action = "ALERT"
			},
		},
		{
			path: "the-amazon-provided-resolver",
			what: "a rule group associated with a different VPC",
			apply: func(p *ecs.Plan) {
				p.Network.DNSFirewall.AssociatedVPCID = "vpc-somebody-else"
			},
		},
		{
			path:  "the-amazon-provided-resolver",
			what:  "no DNS firewall at all",
			apply: func(p *ecs.Plan) { p.Network.DNSFirewall = nil },
		},
		{
			path: "the-task-role-credentials-endpoint",
			what: "a task role, which 169.254.170.2 then vends",
			apply: func(p *ecs.Plan) {
				p.TaskDefinition.TaskRoleARN = "arn:aws:iam::111122223333:role/app"
			},
		},
		{
			path:  "the-ecs-exec-channel",
			what:  "ECS Exec turned on",
			apply: func(p *ecs.Plan) { p.TaskDefinition.EnableExecuteCommand = true },
		},
		{
			path: "the-ecs-exec-channel",
			what: "a Systems Manager endpoint left in the VPC",
			apply: func(p *ecs.Plan) {
				p.Network.Endpoints = append(p.Network.Endpoints, ecs.VPCEndpoint{
					Service: "com.amazonaws.us-east-1.ssmmessages", Type: "Interface",
					SecurityGroupID: "sg-af-example-endpoints", PolicyDocument: "{}",
				})
			},
		},
		{
			path: "a-peering-transit-or-private-gateway-route",
			what: "a route to a peered VPC, which reaches production without touching the internet",
			apply: func(p *ecs.Plan) {
				p.Network.RouteTable.Routes = append(p.Network.RouteTable.Routes,
					ecs.Route{Destination: "10.90.0.0/16", Target: "pcx-0prod"})
			},
		},
		{
			path: "a-peering-transit-or-private-gateway-route",
			what: "a transit gateway route",
			apply: func(p *ecs.Plan) {
				p.Network.RouteTable.Routes = append(p.Network.RouteTable.Routes,
					ecs.Route{Destination: "10.0.0.0/8", Target: "tgw-0corp"})
			},
		},
		{
			path: "a-neighbouring-environment",
			what: "an egress rule naming another environment's security group",
			apply: func(p *ecs.Plan) {
				p.Network.SecurityGroup.Egress = append(p.Network.SecurityGroup.Egress,
					ecs.Rule{Protocol: "tcp", FromPort: 5432, ToPort: 5432,
						PeerSecurityGroupID: "sg-af-other-environment"})
			},
		},
		{
			path: "a-neighbouring-environment",
			what: "an egress rule covering the whole VPC rather than this environment's subnets",
			apply: func(p *ecs.Plan) {
				p.Network.SecurityGroup.Egress = append(p.Network.SecurityGroup.Egress,
					ecs.Rule{Protocol: "-1", CIDRv4: "10.20.0.0/16"})
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
			require.Equal(t, ecs.Closed, expected[m.path],
				"a mutation is only meaningful against a path the plan closes")

			broken := referencePlan()
			m.apply(&broken)
			after := verdictsByID(ecs.Evaluate(broken))

			require.Equal(t, ecs.Open, after[m.path],
				"breaking %s left %s reporting %q, so that check cannot fail",
				m.what, m.path, after[m.path])

			moved := map[string]bool{m.path: true}
			for _, id := range m.alsoOpens {
				moved[id] = true
				require.Equal(t, ecs.Open, after[id],
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
// Without it, the suite above measures whatever it happens to cover, and the
// way a check that cannot say no gets into a repository is by arriving after
// the test that would have caught it.
func TestEveryClosedPathHasAMutation(t *testing.T) {
	covered := map[string]bool{}
	for _, m := range mutations() {
		covered[m.path] = true
	}
	for id, want := range expected {
		if want != ecs.Closed {
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
	for _, p := range ecs.Paths() {
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
	out := ecs.Evaluate(referencePlan()).String()
	require.Contains(t, out, "8 of 13 egress paths")
	require.Contains(t, out, ecs.Caveat)
	require.Contains(t, out, "not that AWS was seen enforcing it")
}

// TestNoEmDashInTheProse holds the repository's writing rule on the strings
// themselves rather than on the source, the way the engine's own four
// assertions do, because this package is prose as much as it is code and a file
// scanner does not read a composed report.
func TestNoEmDashInTheProse(t *testing.T) {
	report := ecs.Evaluate(referencePlan())
	require.NotContains(t, report.String(), "—")
	for _, p := range ecs.Paths() {
		for _, s := range []string{p.Name, p.Why, p.ClosedBy} {
			require.NotContains(t, s, "—", "%s", p.ID)
			require.NotContains(t, s, "--", "%s", p.ID)
		}
	}
}

// TestTheProviderRefusesAndSaysWhy is the end to end assertion: a manifest
// naming this runtime reaches Open and gets a refusal carrying the report.
func TestTheProviderRefusesAndSaysWhy(t *testing.T) {
	env := map[string]string{
		ecs.EnvRegion: "us-east-1", ecs.EnvCluster: "antifailure",
		ecs.EnvVPC: "vpc-0af1", ecs.EnvVPCCIDR: "10.20.0.0/16",
		ecs.EnvSubnets: "subnet-0a,subnet-0b", ecs.EnvSubnetCIDR: "10.20.1.0/24,10.20.2.0/24",
	}
	p := &ecs.Provider{Getenv: func(k string) string { return env[k] }}
	require.Equal(t, "ecs", p.Name())

	rt, err := p.Open(context.Background(), extension.RuntimeConfig{})
	require.Nil(t, rt, "a refusing provider must return no runtime at all")
	require.Error(t, err)
	require.Contains(t, err.Error(), "refuses to start an environment, on purpose")
	require.Contains(t, err.Error(), "8 of 13 egress paths")
	require.Contains(t, err.Error(), "the-ec2-instance-metadata-service")
	require.Contains(t, err.Error(), "runtime.provider kubernetes")
}

// TestTheProviderSaysWhichVariablesAreMissing keeps the two refusals apart. An
// installation that has described no network should be told that, not handed a
// containment report about an empty plan.
func TestTheProviderSaysWhichVariablesAreMissing(t *testing.T) {
	p := &ecs.Provider{Getenv: func(string) string { return "" }}
	rt, err := p.Open(context.Background(), extension.RuntimeConfig{})
	require.Nil(t, rt)
	require.Error(t, err)
	require.Contains(t, err.Error(), ecs.EnvVPC)
	require.Contains(t, err.Error(), "Even fully described it would refuse")
}

// TestTheGeneratedPlanStartsSomething guards the direction the score could be
// gamed in.
//
// Two of the three open paths close instantly if the plan simply stops emitting
// the endpoints, and the resulting environment would pull no image and run
// nothing. So the score going up must not be achievable by deleting the reason
// it is down.
func TestTheGeneratedPlanStartsSomething(t *testing.T) {
	plan := referencePlan()
	var services []string
	for _, e := range plan.Network.Endpoints {
		services = append(services, e.Service)
	}
	sort.Strings(services)
	require.Contains(t, services, "com.amazonaws.us-east-1.ecr.api")
	require.Contains(t, services, "com.amazonaws.us-east-1.ecr.dkr")
	require.Contains(t, services, "com.amazonaws.us-east-1.s3",
		"ECR serves image layers from S3 and a pull needs both")
	require.NotEmpty(t, plan.TaskDefinition.ExecutionRoleARN,
		"without an execution role nothing can pull an image")
	require.Empty(t, plan.TaskDefinition.TaskRoleARN,
		"the execution role is required and the task role must not be")
}

// TestThePlatformVersionIsPinned guards the one field whose default would make
// the security group a claim about traffic it cannot see.
func TestThePlatformVersionIsPinned(t *testing.T) {
	plan := referencePlan()
	require.Equal(t, "1.4.0", plan.PlatformVersion,
		"below 1.4.0 a task carries a second Fargate owned ENI for image pulls that no rule "+
			"in this plan describes and that VPC flow logs do not show")
	require.Equal(t, "FARGATE", plan.LaunchType)
	require.Equal(t, "awsvpc", plan.TaskDefinition.NetworkMode)
}

// TestTheInstanceMetadataVerdictIsNotRoundedUp is its own test because it is
// the path the lane was warned about by name and the failure mode is a
// plausible sentence rather than a wrong boolean.
func TestTheInstanceMetadataVerdictIsNotRoundedUp(t *testing.T) {
	plan := referencePlan()
	byID := verdictsByID(ecs.Evaluate(plan))
	require.Equal(t, ecs.Unproven, byID["the-ec2-instance-metadata-service"],
		"Fargate removes the documented credential source and does not document that the "+
			"address stops answering. Those are different claims")

	plan.LaunchType = "EC2"
	byID = verdictsByID(ecs.Evaluate(plan))
	require.Equal(t, ecs.Open, byID["the-ec2-instance-metadata-service"],
		"on a container instance the defence is an agent variable no task definition can set")

	detail := detailByID(ecs.Evaluate(referencePlan()))["the-ec2-instance-metadata-service"]
	require.Contains(t, strings.ToLower(detail), "needs an account")
}

func verdictsByID(r ecs.Report) map[string]ecs.Verdict {
	out := map[string]ecs.Verdict{}
	for _, v := range r.Verdicts {
		out[v.Path.ID] = v.Verdict
	}
	return out
}

func detailByID(r ecs.Report) map[string]string {
	out := map[string]string{}
	for _, v := range r.Verdicts {
		out[v.Path.ID] = v.Detail
	}
	return out
}

package cloudrun_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/runtime/cloudrun"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// reference is the installation description the generated plan is built from
// throughout these tests. One value, so that a test which changes the plan is
// visibly changing it rather than quietly using a different project.
func reference() cloudrun.Inputs {
	return cloudrun.Inputs{
		Project:     "antifailure-twins",
		Region:      "us-central1",
		Network:     "af-net",
		SubnetRange: "10.20.1.0/26",
		Identity:    "af-example@antifailure-twins.iam.gserviceaccount.com",
		Resolver:    "10.20.1.10",
		Services:    []string{"web", "worker"},
	}
}

func referencePlan() cloudrun.Plan { return cloudrun.Generate(reference(), "af-example") }

// expected is the verdict every path gets on the generated plan.
//
// It is written out in full rather than computed, because the point of the
// table is to be a thing a person disagrees with. A test that asserted "the
// report matches Evaluate" would pass for any predicate at all.
var expected = map[string]cloudrun.Verdict{
	"any-public-destination":                                 cloudrun.Closed,
	"public-ipv6-on-a-dual-stack-subnet":                     cloudrun.Closed,
	"the-instance-metadata-server":                           cloudrun.Open,
	"the-resolvers-recursion":                                cloudrun.Unproven,
	"the-restricted-google-api-range":                        cloudrun.Open,
	"a-neighbouring-environments-service":                    cloudrun.Closed,
	"a-neighbouring-environments-address-in-a-shared-subnet": cloudrun.Closed,
	"a-route-out-of-the-network":                             cloudrun.Closed,
	"the-platforms-own-capture-of-stdout-and-stderr":         cloudrun.Open,
	"a-declared-cloud-storage-or-nfs-volume":                 cloudrun.Closed,
}

// TestTheGeneratedPlanGetsTheVerdictItShould is the baseline every mutation
// below is measured against.
func TestTheGeneratedPlanGetsTheVerdictItShould(t *testing.T) {
	report := cloudrun.Evaluate(referencePlan())
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
// It is deliberately not 10 of 10. Three of the paths cannot be closed on
// Cloud Run at all and one cannot be decided without a Google Cloud project,
// so a green here at 10 would mean somebody had shortened the enumeration.
func TestTheNumber(t *testing.T) {
	report := cloudrun.Evaluate(referencePlan())
	require.Equal(t, 10, report.Total, "egress paths enumerated")
	require.Equal(t, 6, report.Closed, "closed by the generated configuration")
	require.Equal(t, 3, report.Open, "open, and named")
	require.Equal(t, 1, report.Unproven, "not decided by the configuration or the documentation")
	require.False(t, report.Contained(), "this plan must not report itself contained")
	require.Len(t, report.NotClosed(), 4)
}

// TestASingleServiceEnvironmentClosesOneMore is the second half of the number,
// and it is the one finding in this lane that runs in the product's favour.
//
// On Fargate the equivalent allowance is open by necessity, because the image
// pull runs over the task's own interface. Here it is open only because an
// environment of more than one service has nowhere else to send a service to
// service call, so an environment of one closes it.
func TestASingleServiceEnvironmentClosesOneMore(t *testing.T) {
	in := reference()
	in.Services = []string{"web"}
	report := cloudrun.Evaluate(cloudrun.Generate(in, "af-example"))

	require.Equal(t, 10, report.Total)
	require.Equal(t, 7, report.Closed)
	require.Equal(t, 2, report.Open)
	require.Equal(t, 1, report.Unproven)

	byID := verdictsByID(report)
	require.Equal(t, cloudrun.Closed, byID["the-restricted-google-api-range"],
		"an environment of one service needs no service to service call")
	require.Contains(t, detailByID(report)["the-restricted-google-api-range"],
		"imported at deploy time",
		"the reason this closes here and cannot close on Fargate belongs in the detail")
}

// mutation is one break in the generated plan and the paths it should move.
type mutation struct {
	// path is the path whose check the break is aimed at.
	path string
	// what describes the break, for the failure message.
	what string
	// apply breaks the plan.
	apply func(*cloudrun.Plan)
	// alsoOpens are paths that legitimately move as well, named so that a
	// break which moves everything cannot pass by moving its target too.
	alsoOpens []string
}

// mutations is the mutation test, run rather than reported.
//
// Every path this package does not already call Open has one entry, because a
// verdict that cannot become Open is not a check. That is a wider requirement
// than the ecs package's, which covers only its closed paths: here the
// resolver path is Unproven at best, and an unproven verdict that could never
// become open would be the same dead check wearing a more honest label.
//
// The repository's rule is one break per assertion, and that is what the
// alsoOpens field enforces: a break has to move its own path and the named
// ones and nothing else, so a predicate that returned Open for everything
// fails on the first row.
func mutations() []mutation {
	return []mutation{
		{
			path: "any-public-destination",
			what: "a service left on the default egress setting",
			apply: func(p *cloudrun.Plan) {
				p.Services[1].VPCEgress = cloudrun.EgressPrivateRangesOnly
			},
		},
		{
			path: "any-public-destination",
			what: "no deny all rule for IPv4",
			apply: func(p *cloudrun.Plan) {
				p.Network.FirewallRules = withoutRule(p.Network.FirewallRules, "af-af-example-deny-all-ipv4")
			},
		},
		{
			path: "any-public-destination",
			what: "an allow rule naming every address ahead of the deny",
			apply: func(p *cloudrun.Plan) {
				p.Network.FirewallRules = append(p.Network.FirewallRules, cloudrun.FirewallRule{
					Name:              "af-af-example-oops",
					Direction:         "EGRESS",
					Action:            "allow",
					Priority:          800,
					DestinationRanges: []string{"0.0.0.0/0"},
					Protocols:         []string{"tcp:443"},
				})
			},
		},
		{
			path: "any-public-destination",
			what: "a Cloud NAT gateway on the environment's subnet",
			apply: func(p *cloudrun.Plan) {
				p.Network.NATGateways = append(p.Network.NATGateways, cloudrun.NATGateway{
					Name: "af-nat", Router: "af-router", Subnets: []string{p.Network.Subnet.Name},
				})
			},
		},
		{
			path: "any-public-destination",
			what: "an internet gateway route wider than the restricted range",
			apply: func(p *cloudrun.Plan) {
				p.Network.Routes = append(p.Network.Routes, cloudrun.Route{
					Name: "af-default", DestRange: "0.0.0.0/0",
					NextHopKind: cloudrun.NextHopInternetGateway,
				})
			},
		},
		{
			path:  "public-ipv6-on-a-dual-stack-subnet",
			what:  "a dual stack subnet",
			apply: func(p *cloudrun.Plan) { p.Network.Subnet.StackType = cloudrun.StackTypeDual },
		},
		{
			path:  "public-ipv6-on-a-dual-stack-subnet",
			what:  "an internal IPv6 range on the subnet",
			apply: func(p *cloudrun.Plan) { p.Network.Subnet.IPv6Range = "fd20:1::/64" },
		},
		{
			path: "public-ipv6-on-a-dual-stack-subnet",
			what: "no deny all rule for IPv6",
			apply: func(p *cloudrun.Plan) {
				p.Network.FirewallRules = withoutRule(p.Network.FirewallRules, "af-af-example-deny-all-ipv6")
			},
		},
		{
			path:  "the-resolvers-recursion",
			what:  "no outbound server policy at all",
			apply: func(p *cloudrun.Plan) { p.Network.DNS.OutboundServerPolicy = nil },
		},
		{
			path: "the-resolvers-recursion",
			what: "an alternative name server on the public internet",
			apply: func(p *cloudrun.Plan) {
				p.Network.DNS.OutboundServerPolicy.AlternativeNameServers = []string{"8.8.8.8"}
			},
		},
		{
			path: "the-resolvers-recursion",
			what: "a server policy attached to a different network",
			apply: func(p *cloudrun.Plan) {
				p.Network.DNS.OutboundServerPolicy.Network = "somebody-elses-net"
			},
		},
		{
			path: "the-resolvers-recursion",
			what: "private routing turned off",
			apply: func(p *cloudrun.Plan) {
				p.Network.DNS.OutboundServerPolicy.PrivateRouting = false
			},
		},
		{
			path:  "a-neighbouring-environments-service",
			what:  "a service anyone may invoke",
			apply: func(p *cloudrun.Plan) { p.Services[0].AllowUnauthenticated = true },
		},
		{
			path:  "a-neighbouring-environments-service",
			what:  "the default ingress setting",
			apply: func(p *cloudrun.Plan) { p.Services[0].Ingress = cloudrun.IngressAll },
		},
		{
			path: "a-neighbouring-environments-service",
			what: "an invoker binding on another environment's service",
			apply: func(p *cloudrun.Plan) {
				p.Identity.Bindings = append(p.Identity.Bindings, cloudrun.Binding{
					Role:     cloudrun.RoleInvoker,
					Resource: "projects/antifailure-twins/locations/us-central1/services/af-other-web",
				})
			},
		},
		{
			path: "a-neighbouring-environments-address-in-a-shared-subnet",
			what: "an allow rule naming a private range wider than the subnet",
			apply: func(p *cloudrun.Plan) {
				for i := range p.Network.FirewallRules {
					if p.Network.FirewallRules[i].Name == "af-af-example-allow-own-subnet" {
						p.Network.FirewallRules[i].DestinationRanges = []string{"10.0.0.0/8"}
					}
				}
			},
		},
		{
			path: "a-neighbouring-environments-address-in-a-shared-subnet",
			what: "an allow rule naming the range the subnet sits inside",
			apply: func(p *cloudrun.Plan) {
				for i := range p.Network.FirewallRules {
					if p.Network.FirewallRules[i].Name == "af-af-example-allow-own-subnet" {
						// The /24 that starts at the same address as the
						// environment's /26, which a containment check written
						// as "does the subnet contain this address" reports as
						// inside itself.
						p.Network.FirewallRules[i].DestinationRanges = []string{"10.20.1.0/24"}
					}
				}
			},
		},
		{
			path: "a-route-out-of-the-network",
			what: "a route to a peered network",
			apply: func(p *cloudrun.Plan) {
				p.Network.Routes = append(p.Network.Routes, cloudrun.Route{
					Name: "af-to-production", DestRange: "10.90.0.0/16",
					NextHopKind: cloudrun.NextHopPeering,
				})
			},
		},
		{
			path: "a-declared-cloud-storage-or-nfs-volume",
			what: "a Cloud Storage volume on a service",
			apply: func(p *cloudrun.Plan) {
				p.Services[0].Volumes = append(p.Services[0].Volumes, cloudrun.Volume{
					Name: "exports", Type: cloudrun.VolumeCloudStorage, Target: "gs://somewhere-else",
				})
			},
		},
		{
			path: "a-declared-cloud-storage-or-nfs-volume",
			what: "an NFS volume on a service",
			apply: func(p *cloudrun.Plan) {
				p.Services[1].Volumes = append(p.Services[1].Volumes, cloudrun.Volume{
					Name: "share", Type: cloudrun.VolumeNFS, Target: "10.90.0.4:/exports",
				})
			},
		},
	}
}

// TestBreakingAPathIsNoticed runs every mutation and requires the target to
// move to Open and nothing else to move at all.
func TestBreakingAPathIsNoticed(t *testing.T) {
	for _, m := range mutations() {
		m := m
		t.Run(m.path+": "+m.what, func(t *testing.T) {
			require.NotEqual(t, cloudrun.Open, expected[m.path],
				"a mutation is only meaningful against a path the plan does not already call open")

			broken := referencePlan()
			m.apply(&broken)
			after := verdictsByID(cloudrun.Evaluate(broken))

			require.Equal(t, cloudrun.Open, after[m.path],
				"breaking %s left %s reporting %q, so that check cannot fail",
				m.what, m.path, after[m.path])

			moved := map[string]bool{m.path: true}
			for _, id := range m.alsoOpens {
				moved[id] = true
				require.Equal(t, cloudrun.Open, after[id],
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

// TestEveryPathNotAlreadyOpenHasAMutation stops a path from being added with a
// check nothing ever breaks.
//
// Without it, the suite above measures whatever it happens to cover, and the
// way a check that cannot say no gets into a repository is by arriving after
// the test that would have caught it.
func TestEveryPathNotAlreadyOpenHasAMutation(t *testing.T) {
	covered := map[string]bool{}
	for _, m := range mutations() {
		covered[m.path] = true
	}
	for id, want := range expected {
		if want == cloudrun.Open {
			continue
		}
		require.True(t, covered[id],
			"%s is reported %q and no mutation proves that check can fail", id, want)
	}
}

// TestEveryPathIsDescribed requires the prose, because the enumeration is the
// deliverable and an id with no reason attached is not evidence of anything.
func TestEveryPathIsDescribed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range cloudrun.Paths() {
		require.NotEmpty(t, p.ID)
		require.False(t, seen[p.ID], "duplicate path id %q", p.ID)
		seen[p.ID] = true
		require.NotEmpty(t, p.Name, "%s has no name", p.ID)
		require.NotEmpty(t, p.Why, "%s does not say why it is a real route", p.ID)
		require.NotEmpty(t, p.ClosedBy, "%s does not say what closes it", p.ID)
		require.NotNil(t, p.Check, "%s has no check", p.ID)
	}
}

// TestTheReportCarriesItsCaveat is the one assertion that guards the honesty
// of every number this package produces.
func TestTheReportCarriesItsCaveat(t *testing.T) {
	out := cloudrun.Evaluate(referencePlan()).String()
	require.Contains(t, out, "6 of 10 egress paths")
	require.Contains(t, out, cloudrun.Caveat)
	require.Contains(t, out, "not that Google was seen enforcing it")
}

// TestNoEmDashInTheProse holds the repository's writing rule on the strings
// themselves rather than on the source, the way the engine's own four
// assertions do, because this package is prose as much as it is code and a
// file scanner does not read a composed report.
func TestNoEmDashInTheProse(t *testing.T) {
	report := cloudrun.Evaluate(referencePlan())
	require.NotContains(t, report.String(), "—")
	for _, p := range cloudrun.Paths() {
		for _, s := range []string{p.Name, p.Why, p.ClosedBy} {
			require.NotContains(t, s, "—", "%s", p.ID)
			require.NotContains(t, s, "--", "%s", p.ID)
		}
	}
}

// TestTheProviderRefusesAndSaysWhy is the end to end assertion: a manifest
// naming this runtime reaches Open and gets a refusal carrying the report.
func TestTheProviderRefusesAndSaysWhy(t *testing.T) {
	p := &cloudrun.Provider{Getenv: referenceEnv()}
	require.Equal(t, "cloudrun", p.Name())

	rt, err := p.Open(context.Background(), extension.RuntimeConfig{})
	require.Nil(t, rt, "a refusing provider must return no runtime at all")
	require.Error(t, err)
	require.Contains(t, err.Error(), "refuses to start an environment, on purpose")
	require.Contains(t, err.Error(), "6 of 10 egress paths")
	require.Contains(t, err.Error(), "the-instance-metadata-server")
	require.Contains(t, err.Error(), "closes 7 of 10 instead",
		"the single service number belongs in the refusal, not only in a test")
	require.Contains(t, err.Error(), "runtime.provider kubernetes")
}

// TestTheProviderSaysWhichVariablesAreMissing keeps the two refusals apart. An
// installation that has described no project should be told that, not handed a
// containment report about an empty plan.
func TestTheProviderSaysWhichVariablesAreMissing(t *testing.T) {
	p := &cloudrun.Provider{Getenv: func(string) string { return "" }}
	rt, err := p.Open(context.Background(), extension.RuntimeConfig{})
	require.Nil(t, rt)
	require.Error(t, err)
	require.Contains(t, err.Error(), cloudrun.EnvProject)
	require.Contains(t, err.Error(), cloudrun.EnvResolver)
	require.Contains(t, err.Error(), "Even fully described it would refuse")
}

// TestTheGeneratedPlanRunsSomething guards the direction the score could be
// gamed in.
//
// One of the three open paths closes instantly if the plan simply stops
// emitting the rule that lets one service call another, and the resulting
// environment would be a web service that cannot reach its worker. So the
// score going up must not be achievable by deleting the reason it is down.
func TestTheGeneratedPlanRunsSomething(t *testing.T) {
	plan := referencePlan()
	require.Len(t, plan.Services, 2)

	var allowsRestricted bool
	for _, rule := range plan.Network.FirewallRules {
		for _, dest := range rule.DestinationRanges {
			if dest == cloudrun.RestrictedVIPRange && rule.Action == "allow" {
				allowsRestricted = true
			}
		}
	}
	require.True(t, allowsRestricted,
		"a two service environment reaches its own second service through %s and nothing else, "+
			"so a plan without this rule would score better and run nothing",
		cloudrun.RestrictedVIPRange)
	require.True(t, plan.Network.Subnet.PrivateGoogleAccess,
		"the restricted range does not answer without Private Google Access on the subnet")
	require.Len(t, plan.Identity.Bindings, 2,
		"each service needs an invoker binding for the other to call it")
	for _, s := range plan.Services {
		require.Equal(t, cloudrun.EgressAllTraffic, s.VPCEgress)
		require.NotEmpty(t, s.Network)
		require.NotEmpty(t, s.Subnet)
	}
}

// TestTheDenyRuleSitsAfterPriorityOneThousand guards the one number whose
// tighter looking value would stop the environment serving.
func TestTheDenyRuleSitsAfterPriorityOneThousand(t *testing.T) {
	plan := referencePlan()
	var denies int
	for _, rule := range plan.Network.FirewallRules {
		if rule.Action != "deny" {
			continue
		}
		denies++
		require.Greater(t, rule.Priority, 1000,
			"Google states that a deny all egress rule must sit at a priority after 1000, or the "+
				"traffic that carries requests to the service is blocked as well: rule %q is at %d",
			rule.Name, rule.Priority)
	}
	require.Equal(t, 2, denies, "one deny rule per IP version")
}

// TestTheMetadataVerdictIsNotRoundedUp is its own test because it is the path
// this lane was warned about by name and because it is the one the ECS plan
// closes and this one cannot.
func TestTheMetadataVerdictIsNotRoundedUp(t *testing.T) {
	plan := referencePlan()
	require.NotEmpty(t, plan.Identity.Bindings,
		"the two service plan holds invoker bindings, so this is the narrowed case rather than "+
			"the empty one")

	byID := verdictsByID(cloudrun.Evaluate(plan))
	require.Equal(t, cloudrun.Open, byID["the-instance-metadata-server"],
		"Cloud Run cannot run a service with no identity, so the endpoint vends a real token")

	// And it stays open for the tightest identity available, which is the
	// point: an identity with nothing granted is a narrower token, not a
	// closed path.
	in := reference()
	in.Services = []string{"web"}
	bare := cloudrun.Generate(in, "af-example")
	require.Empty(t, bare.Identity.Bindings)
	byID = verdictsByID(cloudrun.Evaluate(bare))
	require.Equal(t, cloudrun.Open, byID["the-instance-metadata-server"])
	require.Contains(t, detailByID(cloudrun.Evaluate(bare))["the-instance-metadata-server"],
		"no role bindings at all")

	// The default account is the worst case and the detail has to name it,
	// because "open" alone would read the same for both.
	plan.Identity.Email = "410000000000-compute@developer.gserviceaccount.com"
	detail := detailByID(cloudrun.Evaluate(plan))["the-instance-metadata-server"]
	require.Contains(t, detail, "Compute Engine default service account")
}

// TestTheResolverVerdictIsNeverClosed is the assertion that keeps the
// three valued verdict from collapsing into two.
//
// The resolver path cannot reach Closed on any plan, because the mechanism
// Google documents states its precondition as a VM. A future edit that makes
// the check return Closed for a plan that looks tidy would be exactly the
// rounding this lane exists to refuse, so it is asserted over the reference
// plan, over a plan with every field of the policy set as well as it can be,
// and over the broken ones.
func TestTheResolverVerdictIsNeverClosed(t *testing.T) {
	plans := map[string]cloudrun.Plan{"the reference plan": referencePlan()}

	tidy := referencePlan()
	tidy.Perimeter = &cloudrun.ServicePerimeter{
		Name:     "af-perimeter",
		Projects: []string{"antifailure-twins"},
	}
	plans["a plan inside a perimeter"] = tidy

	for _, m := range mutations() {
		broken := referencePlan()
		m.apply(&broken)
		plans[m.path+": "+m.what] = broken
	}

	for name, plan := range plans {
		verdict := verdictsByID(cloudrun.Evaluate(plan))["the-resolvers-recursion"]
		require.NotEqual(t, cloudrun.Closed, verdict,
			"%s reported the resolver closed, and no configuration settles it", name)
	}

	detail := detailByID(cloudrun.Evaluate(referencePlan()))["the-resolvers-recursion"]
	require.Contains(t, detail, "needs an account")
}

// TestTheSubnetBelongsToTheEnvironment guards the assumption the neighbouring
// address check rests on.
//
// That check says a plan is closed when every private destination it allows is
// inside the subnet. That is only containment if the subnet holds one
// environment, and the only thing making it hold one environment is this name.
func TestTheSubnetBelongsToTheEnvironment(t *testing.T) {
	plan := cloudrun.Generate(reference(), "af-nightly-7")
	require.Contains(t, plan.Network.Subnet.Name, "af-nightly-7",
		"two environments in one subnet cannot be separated by any rule that can be written, "+
			"because Google states a policy must use the range of the entire subnet")
	for _, s := range plan.Services {
		require.Equal(t, plan.Network.Subnet.Name, s.Subnet)
		require.Contains(t, s.NetworkTags, "af-af-nightly-7")
	}
}

// TestTheFourEscapeAttemptsAreAnsweredOneByOne is the lane's acceptance
// evidence, kept in the repository rather than only in a report.
//
// The wave this lane belongs to asks for conformance.RunRuntime green and the
// four escape attempts quoted individually as refused. RunRuntime takes a
// factory that produces a runtime and this package produces none, so it cannot
// run at all and no packet has been observed. What can be said without a
// project is which of the four behaviours the generated configuration would
// refuse, and the answer is not four, which is the whole reason this lane ships
// a refusal.
//
// The behaviour names are the ones in engine/conformance/runtime.go, so a
// rename there and a quiet drift here cannot both happen without this test
// noticing that a path id has gone.
func TestTheFourEscapeAttemptsAreAnsweredOneByOne(t *testing.T) {
	byID := verdictsByID(cloudrun.Evaluate(referencePlan()))
	for _, c := range []struct {
		behaviour string
		path      string
		want      cloudrun.Verdict
		note      string
	}{
		{
			behaviour: "Egress_CannotBeBypassedByAddress",
			path:      "any-public-destination",
			want:      cloudrun.Closed,
			note:      "refused by the configuration, and by no packet anybody watched",
		},
		{
			behaviour: "Egress_CannotBeBypassedByUDP",
			path:      "any-public-destination",
			want:      cloudrun.Closed,
			note: "the same rule refuses a query to a public resolver, which is why the two are " +
				"one path here and two on AWS",
		},
		{
			behaviour: "Egress_CannotReachTheMetadataEndpoint",
			path:      "the-instance-metadata-server",
			want:      cloudrun.Open,
			note: "this one cannot be refused on Cloud Run at all, and it is the behaviour the " +
				"Kubernetes runtime earns its existence by refusing",
		},
		{
			behaviour: "Egress_NamesDoNotCrossEnvironments",
			path:      "a-neighbouring-environments-service",
			want:      cloudrun.Closed,
			note: "closed by IAM rather than by resolution: every service name resolves into the " +
				"restricted range, including a neighbour's, and the front end refuses the call " +
				"rather than the name failing to resolve",
		},
	} {
		require.Equal(t, c.want, byID[c.path],
			"%s maps to %s, which said %q. %s", c.behaviour, c.path, byID[c.path], c.note)
	}
	require.NotEqual(t, cloudrun.Closed, byID["the-resolvers-recursion"],
		"there is no conformance behaviour for a query through the network's own resolver, "+
			"because on Kubernetes a NetworkPolicy closes it along with everything else")
}

func referenceEnv() func(string) string {
	env := map[string]string{
		cloudrun.EnvProject:     "antifailure-twins",
		cloudrun.EnvRegion:      "us-central1",
		cloudrun.EnvNetwork:     "af-net",
		cloudrun.EnvSubnetRange: "10.20.1.0/26",
		cloudrun.EnvIdentity:    "af-example@antifailure-twins.iam.gserviceaccount.com",
		cloudrun.EnvResolver:    "10.20.1.10",
	}
	return func(k string) string { return env[k] }
}

func withoutRule(rules []cloudrun.FirewallRule, name string) []cloudrun.FirewallRule {
	out := make([]cloudrun.FirewallRule, 0, len(rules))
	for _, r := range rules {
		if r.Name != name {
			out = append(out, r)
		}
	}
	if len(out) == len(rules) {
		panic("no rule named " + name + ", so this mutation removes nothing: " + strings.Join(ruleNames(rules), ", "))
	}
	return out
}

func ruleNames(rules []cloudrun.FirewallRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Name)
	}
	return out
}

func verdictsByID(r cloudrun.Report) map[string]cloudrun.Verdict {
	out := map[string]cloudrun.Verdict{}
	for _, v := range r.Verdicts {
		out[v.Path.ID] = v.Verdict
	}
	return out
}

func detailByID(r cloudrun.Report) map[string]string {
	out := map[string]string{}
	for _, v := range r.Verdicts {
		out[v.Path.ID] = v.Detail
	}
	return out
}

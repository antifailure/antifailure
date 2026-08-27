package k8s

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// These test the decisions that are made before anything talks to a cluster.
// They are worth having separately from the conformance suite because the
// conformance suite needs a cluster and these do not, so a change that quietly
// opens the environment fails on a laptop with no cluster and in CI, rather
// than only where somebody remembered to run it.

func TestDefaultDenyHasNoRules(t *testing.T) {
	policies := networkPolicies("e1", "af-env-e1", false)
	deny := findPolicy(t, policies, "af-default-deny")

	// This is the single most important assertion in the package. A
	// NetworkPolicy that selects every pod and lists no egress rules denies
	// all egress. The same object with PolicyTypes missing Egress permits all
	// egress, and the two differ by one word in a list. Nothing else here
	// would notice: every other policy would still apply, every object would
	// still be created, and every environment would have a route out.
	require.Empty(t, deny.Spec.PodSelector.MatchLabels,
		"the default deny must select every pod in the namespace")
	require.Empty(t, deny.Spec.PodSelector.MatchExpressions)
	require.ElementsMatch(t,
		[]networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		deny.Spec.PolicyTypes,
		"a policy type left out of this list is a direction that is not denied")
	require.Empty(t, deny.Spec.Egress, "an egress rule here would be an allowance")
	require.Empty(t, deny.Spec.Ingress)
}

func TestOnlyTheSidecarMayLeave(t *testing.T) {
	policies := networkPolicies("e1", "af-env-e1", false)
	egress := findPolicy(t, policies, "af-service-egress-to-proxy")

	require.Len(t, egress.Spec.Egress, 1,
		"a second egress rule for services is a second way out")
	for _, peer := range egress.Spec.Egress[0].To {
		require.Nil(t, peer.IPBlock,
			"a service's egress must name the sidecar by selector and never an address range")
		require.Nil(t, peer.NamespaceSelector,
			"a namespace selector would let a service out of its own namespace")
		require.NotNil(t, peer.PodSelector)
		require.Equal(t, ComponentProxy, peer.PodSelector.MatchLabels[LabelComponent])
	}
}

func TestTheEscapeProbeIsGovernedLikeAService(t *testing.T) {
	policies := networkPolicies("e1", "af-env-e1", false)
	egress := findPolicy(t, policies, "af-service-egress-to-proxy")

	// The probe's verdict is only evidence about services if the probe is
	// under the same rules a service is under. A probe with no egress
	// allowance at all would prove something nobody asked: that a pod with no
	// permissions cannot leave.
	var covered []string
	for _, expr := range egress.Spec.PodSelector.MatchExpressions {
		if expr.Key == LabelComponent {
			covered = expr.Values
		}
	}
	require.ElementsMatch(t, []string{ComponentService, ComponentProbe}, covered)
}

func TestTheSidecarCannotReachTheMetadataEndpointEither(t *testing.T) {
	policies := networkPolicies("e1", "af-env-e1", false)
	proxy := findPolicy(t, policies, "af-proxy-egress")

	var excepted []string
	for _, rule := range proxy.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				excepted = append(excepted, peer.IPBlock.Except...)
			}
		}
	}
	// The sidecar is the only pod with a route out, so this is the last place
	// the metadata endpoint can be blocked. An egress rule naming a host would
	// otherwise be able to reach it, and on a cloud runner that address hands
	// out the node's own credentials.
	require.Contains(t, excepted, "169.254.0.0/16",
		"the link local range carries the instance metadata endpoint")
	for _, private := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		require.Contains(t, excepted, private,
			"the environment must not reach the operator's own network through the sidecar")
	}
}

func TestNoIngressPolicyWithoutADomain(t *testing.T) {
	without := networkPolicies("e1", "af-env-e1", false)
	with := networkPolicies("e1", "af-env-e1", true)
	require.Len(t, with, len(without)+1)
	// Opening a namespace to every other namespace on the cluster for a route
	// that nothing serves would be weakening isolation for nothing.
	for _, p := range without {
		require.NotEqual(t, "af-ingress-controller", p.Name)
	}
}

func TestInternalNamesCoverEveryFormAResolverProduces(t *testing.T) {
	names := internalNames("af-env-e1", []provider.ServiceSpec{{Name: "web"}})

	// A pod's search list turns one name into several, and the sidecar matches
	// what actually arrives. A missing qualified form means the sidecar
	// answers with its own address for a service's own name, so one service
	// calling another reaches the proxy, which decides it against an egress
	// policy that has never heard of it.
	for _, want := range []string{
		"web", "web.af-env-e1", "web.af-env-e1.svc", "web.af-env-e1.svc.cluster.local",
		ProxyName, ProxyName + ".af-env-e1.svc.cluster.local",
	} {
		require.Contains(t, names, want)
	}
}

func TestPodsResolveOnlyThroughTheSidecar(t *testing.T) {
	policy, cfg := podDNS("af-env-e1", "10.43.0.9")
	require.Equal(t, "None", string(policy),
		"anything but None leaves the cluster's own resolver in resolv.conf, "+
			"which is a name lookup that the sidecar never sees")
	require.Equal(t, []string{"10.43.0.9"}, cfg.Nameservers)

	var ndots string
	for _, o := range cfg.Options {
		if o.Name == "ndots" && o.Value != nil {
			ndots = *o.Value
		}
	}
	// With the cluster default of 5, a lookup of example.com is tried as
	// example.com.<namespace>.svc.cluster.local first. That is not a name the
	// sidecar was told is internal, so it answers with its own address, and
	// the connection that follows carries a Host header no egress rule can
	// match. The policy then refuses a host it was configured to allow.
	require.Equal(t, "1", ndots)
}

func TestServicesNeverGetAServiceAccountToken(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{{Name: "web", Port: 8080}}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")

	// A pod that can read a service account token can talk to the API server,
	// and a pod that can talk to the API server can delete the NetworkPolicy
	// that is containing it.
	require.NotNil(t, d.Spec.Template.Spec.AutomountServiceAccountToken)
	require.False(t, *d.Spec.Template.Spec.AutomountServiceAccountToken)
}

func TestSanitizeProducesALegalNamespace(t *testing.T) {
	for input, want := range map[string]string{
		"feature/AF-123_fix": "feature-af-123-fix",
		"---":                "env",
		"":                   "env",
		"Simple":             "simple",
	} {
		got := sanitize(input)
		require.Equal(t, want, got, "input %q", input)
	}
	// Branch names are long and namespaces are not.
	long := sanitize(strings.Repeat("a", 200))
	require.LessOrEqual(t, len(DefaultNamespacePrefix+long), 63)
	require.NotEmpty(t, long)
}

func TestCoveringCIDRSpansEveryNode(t *testing.T) {
	got, err := coveringCIDR([]string{"10.42.0.0/24", "10.42.1.0/24", "10.42.2.0/24"})
	require.NoError(t, err)
	require.Equal(t, "10.42.0.0/22", got)

	// One node is the common case and must not be widened.
	got, err = coveringCIDR([]string{"192.168.5.0/24"})
	require.NoError(t, err)
	require.Equal(t, "192.168.5.0/24", got)

	_, err = coveringCIDR(nil)
	require.Error(t, err, "no pod network is a refusal, not a default")
}

func TestMigrationRunsWithItsOwnConnectionString(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{
		EnvID:                "e1",
		DatabaseURL:          secretValue("postgres://pooled/db"),
		MigrationDatabaseURL: secretValue("postgres://direct/db"),
		Services:             []provider.ServiceSpec{{Name: "web", Migrate: "migrate"}},
	}
	job := r.migrationJob(spec, spec.Services[0], "af-env-e1", "10.43.0.9")
	require.Equal(t, "postgres://direct/db", envValue(t, job.Spec.Template.Spec.Containers[0].Env, "DATABASE_URL"),
		"a migration through a transaction pooler half applies rather than refusing")

	deployment := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")
	require.Equal(t, "postgres://pooled/db", envValue(t, deployment.Spec.Template.Spec.Containers[0].Env, "DATABASE_URL"),
		"the application should use the pool")

	// A migration is never retried: one clear failure beats six minutes of a
	// Job that is neither running nor finished.
	require.NotNil(t, job.Spec.BackoffLimit)
	require.Equal(t, int32(0), *job.Spec.BackoffLimit)
}

func findPolicy(t *testing.T, policies []*networkingv1.NetworkPolicy, name string) *networkingv1.NetworkPolicy {
	t.Helper()
	for _, p := range policies {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no policy called %s; the environment would be missing whatever it does", name)
	return nil
}

func envValue(t *testing.T, env []corev1.EnvVar, name string) string {
	t.Helper()
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func secretValue(s string) secrets.Value { return secrets.New(s) }

func TestTheSidecarCanBindTheDNSPort(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix, proxyRef: "proxy:test"}
	_, deployment, _, err := r.proxyObjects("e1", "af-env-e1", "10.42.0.0/16", "10.43.0.10:53",
		provider.EnvSpec{EnvID: "e1"})
	require.NoError(t, err)

	sc := deployment.Spec.Template.Spec.Containers[0].SecurityContext
	require.NotNil(t, sc)
	require.Contains(t, sc.Capabilities.Drop, corev1.Capability("ALL"))
	// The sidecar answers DNS on port 53 and does not run as root. Without
	// this one capability the container starts, fails to listen, and every
	// name lookup in the environment goes unanswered, which reads as an
	// environment whose services cannot find anything rather than as a
	// permission it was not given.
	require.Contains(t, sc.Capabilities.Add, corev1.Capability("NET_BIND_SERVICE"),
		"a process that is not root cannot bind a port below 1024 without it")
	require.True(t, *sc.RunAsNonRoot)
}

func TestReadinessMirrorsTheLocalRuntime(t *testing.T) {
	// No health path: ready when the port accepts a connection, because
	// asking for more would mean inventing a protocol the application does
	// not speak. This is what the local runtime does.
	probe := readinessProbe(provider.ServiceSpec{Name: "web", Port: 8080})
	require.NotNil(t, probe.TCPSocket)
	require.Nil(t, probe.HTTPGet)

	probe = readinessProbe(provider.ServiceSpec{Name: "web", Port: 8080, HealthPath: "/healthz"})
	require.NotNil(t, probe.HTTPGet)
	require.Equal(t, "/healthz", probe.HTTPGet.Path)
}

func TestAServiceWithNoPortIsNotProbed(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{{Name: "worker", Kind: "worker"}}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")
	// A worker listens on nothing. A probe against a port it does not have
	// would restart it forever.
	require.Nil(t, d.Spec.Template.Spec.Containers[0].ReadinessProbe)
}

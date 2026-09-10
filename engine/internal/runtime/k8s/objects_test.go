package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// These test the decisions that are made before anything talks to a cluster.
// They are worth having separately from the conformance suite because the
// conformance suite needs a cluster and these do not, so a change that quietly
// opens the environment fails on a laptop with no cluster and in CI, rather
// than only where somebody remembered to run it.

func TestDefaultDenyHasNoRules(t *testing.T) {
	policies := networkPolicies("e1", "af-env-e1", false, nil)
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
	policies := networkPolicies("e1", "af-env-e1", false, nil)
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
	policies := networkPolicies("e1", "af-env-e1", false, nil)
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
	policies := networkPolicies("e1", "af-env-e1", false, nil)
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
	without := networkPolicies("e1", "af-env-e1", false, nil)
	with := networkPolicies("e1", "af-env-e1", true, nil)
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
	_, deployment, _, err := r.proxyObjects(context.Background(), "e1", "af-env-e1", "10.42.0.0/16", "10.43.0.10",
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

func TestADeploymentUsesTheOnlyRestartPolicyItMay(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{{Name: "web", Port: 8080}}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")

	// Kubernetes rejects a Deployment whose pod template restarts on anything
	// but Always, and it rejects it at Up. This started out as Never, copied
	// from the local runtime where restarts are deliberately off, which would
	// have failed every single environment with a validation error.
	require.Equal(t, corev1.RestartPolicyAlways, d.Spec.Template.Spec.RestartPolicy)

	// A Job is the opposite: Always is illegal there, and a migration that
	// restarted forever would never be reported as failed.
	spec.Services[0].Migrate = "migrate"
	job := r.migrationJob(spec, spec.Services[0], "af-env-e1", "10.43.0.9")
	require.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)
}

func TestANameserverIsABareAddress(t *testing.T) {
	_, cfg := podDNS("af-env-e1", "10.43.0.10")
	// Kubernetes validates dnsConfig.nameservers as IP addresses and refuses
	// a pod whose nameserver carries a port. The cluster resolver is also
	// given to the sidecar, which forwards to an endpoint and does want one,
	// and handing the same string to both produced a pod that could never be
	// created.
	for _, ns := range cfg.Nameservers {
		require.NotContains(t, ns, ":", "a nameserver may not carry a port")
		require.NotNil(t, netParse(ns), "a nameserver must be an address")
	}
}

func TestTheSidecarIsGivenAnEndpoint(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix, proxyRef: "proxy:test"}
	secret, _, _, err := r.proxyObjects(context.Background(), "e1", "af-env-e1", "10.42.0.0/16", "10.43.0.10",
		provider.EnvSpec{EnvID: "e1"})
	require.NoError(t, err)

	var cfg sidecarConfig
	require.NoError(t, json.Unmarshal(secret.Data[configKey], &cfg))
	require.Equal(t, "10.43.0.10:53", cfg.Resolver,
		"the sidecar dials this, so it needs the port the pods' nameserver must not have")
}

func netParse(s string) net.IP { return net.ParseIP(s) }

func TestAClientThatIgnoresProxyVariablesCanStillReachTheSidecar(t *testing.T) {
	policies := networkPolicies("e1", "af-env-e1", false, nil)
	egress := findPolicy(t, policies, "af-service-egress-to-proxy")

	ports := map[int32]bool{}
	for _, p := range egress.Spec.Egress[0].Ports {
		if p.Port != nil {
			ports[p.Port.IntVal] = true
		}
	}
	// The sidecar listens transparently on 80 and 443 as well as proxying on
	// 3128. A client that ignores its proxy variables resolves the name, gets
	// the sidecar's address, and connects on the ordinary port; without these
	// the policy blocks the exact case the whole design exists for, and it
	// fails in the direction that looks safe, because everything is still
	// contained and a host the policy ALLOWS is unreachable too.
	for _, want := range []int32{ProxyPort, 80, 443, dnsPort} {
		require.True(t, ports[want], "egress to the sidecar must permit port %d", want)
	}

	// And every port the byte stream path listens on, compared against the
	// shared table rather than against a literal.
	//
	// This is the drift gate. The sidecar opens a listener per entry in
	// schema.ByteStreamProtocols and this NetworkPolicy decides whether a
	// packet ever reaches one. A port present in the table and absent here
	// does not fail loudly: a NetworkPolicy that does not permit a port DROPS
	// the packet, so the application hangs until its own connect timeout and
	// the sidecar's decision log stays empty, which reads as an application
	// fault rather than as a policy one. Asserting against a copied list here
	// would make the two lists two lists again.
	for _, proto := range schema.ByteStreamProtocols {
		require.True(t, ports[int32(proto.Port)], //nolint:gosec // every port in the table is below 65536
			"egress to the sidecar must permit port %d, which it listens on for %s",
			proto.Port, proto.Name)
	}
}

// TestAPolicyPermittedPortIsNotAGrant is the cell behind the claim that the
// NetworkPolicy may be wider than the listeners at no cost.
//
// The claim is easy to write and it is narrower than it sounds, so it is worth
// stating exactly what holds it up. Permitting a port here grants nothing
// BECAUSE NOTHING IN THE ENVIRONMENT ANSWERS ON IT, and not because the
// sidecar would refuse a connection that arrived. There is no second layer:
// serveStream evaluates the host and the port and forwards on Allowed with no
// port check anywhere in the path, and the address guard in destination.go
// checks address ranges rather than ports. So the guarantee holds exactly as
// long as the listener set stays narrow, and the moment anything opens a
// listener on one of these ports the permission stops being inert and becomes
// a grant, with nothing in between to refuse it.
//
// That is the invariant this test exists to keep: the ports the policy permits
// and the manifest did not name must have no listener. Anyone widening the
// listener set is also changing what this policy permits, and this is where
// they find out.
func TestAPolicyPermittedPortIsNotAGrant(t *testing.T) {
	// One host, one port, which is what a manifest that means to reach a
	// broker looks like.
	const named = 5671 // AMQP over TLS, and in the table
	rules := []schema.EgressRule{{Host: "broker.internal:5671", Mode: schema.ModeAllow}}

	policies := networkPolicies("e1", "af-env-e1", false, rules)
	egress := findPolicy(t, policies, "af-service-egress-to-proxy")
	permitted := map[int32]bool{}
	for _, p := range egress.Spec.Egress[0].Ports {
		if p.Port != nil {
			permitted[p.Port.IntVal] = true
		}
	}

	served := map[int]bool{}
	for _, proto := range schema.StreamPorts(rules) {
		served[proto.Port] = true
	}

	// The control, and it has to come first. A test whose every assertion is
	// that something is absent passes against a policy that permits nothing
	// and a sidecar that serves nothing, which is the environment this lane
	// was fixing.
	require.True(t, permitted[named],
		"the port the manifest named is not permitted, so nothing below is measuring anything")
	require.True(t, served[named],
		"the port the manifest named has no listener, so the feature does not work at all")

	// The claim. Every other port in the table is permitted by the policy and
	// answered by nothing.
	for _, proto := range schema.ByteStreamProtocols {
		if proto.Port == named {
			continue
		}
		require.True(t, permitted[int32(proto.Port)], //nolint:gosec // every port in the table is below 65536
			"port %d dropped out of the NetworkPolicy, so a connection to it hangs "+
				"rather than being refused", proto.Port)
		require.False(t, served[proto.Port],
			"port %d is permitted by the NetworkPolicy AND the sidecar opens a listener for it, "+
				"which is what turns the permission into a grant: a connection accepted there is "+
				"forwarded on the name in its handshake, and this manifest named only port %d",
			proto.Port, named)
	}
}

// TestAPortOnlyAManifestNamesReachesTheSidecar is the other half of the drift
// gate above, and it is about the ports that are not in the table.
//
// A manifest may name a broker on a port nobody standardised, and stream.go
// promises that writing it down is what makes it reachable. On Docker there is
// no NetworkPolicy and the promise keeps itself. On Kubernetes the sidecar
// opens the listener and the cluster drops the packet on its way there, so the
// application hangs until its own connect timeout and the sidecar's log stays
// empty: the failure looks like the broker is down, and the manifest that was
// supposed to fix it is the thing that is being ignored.
//
// The port here is deliberately one ByteStreamProtocols does not carry, so a
// policy built from the table alone cannot pass this by accident.
func TestAPortOnlyAManifestNamesReachesTheSidecar(t *testing.T) {
	const declared = 15672 // the RabbitMQ management port, and not in the table
	for _, proto := range schema.ByteStreamProtocols {
		require.NotEqual(t, declared, proto.Port,
			"this test is only meaningful for a port the table does not already carry")
	}

	rules := []schema.EgressRule{
		{Host: fmt.Sprintf("broker.internal:%d", declared), Mode: schema.ModeAllow},
	}
	policies := networkPolicies("e1", "af-env-e1", false, rules)
	egress := findPolicy(t, policies, "af-service-egress-to-proxy")

	ports := map[int32]bool{}
	for _, p := range egress.Spec.Egress[0].Ports {
		if p.Port != nil {
			ports[p.Port.IntVal] = true
		}
	}
	require.True(t, ports[declared],
		"a port the manifest names must reach the sidecar, or declaring it changes nothing on a cluster")

	// And the table is still whole. Narrowing this list to the ports the
	// sidecar actually listens on would be the tempting simplification and it
	// is the wrong one: a permitted port nothing listens on answers with a
	// reset at once, while an unpermitted one is dropped and hangs. The
	// cheaper failure is the one worth keeping.
	for _, proto := range schema.ByteStreamProtocols {
		require.True(t, ports[int32(proto.Port)], //nolint:gosec // every port in the table is below 65536
			"a rule naming one port must not narrow the policy to it, and port %d was lost",
			proto.Port)
	}
}

// TestDownRefusesANamespaceItDoesNotOwn is the guard on the one operation here
// that cannot be undone.
//
// Down derives a namespace name from an environment id and deletes it, and
// deleting a namespace takes every object inside it. So the question that
// matters is not "does it delete the right one" but "what does it do when the
// name it computed belongs to somebody else", and the conformance suite cannot
// ask it: the suite's leak check compares the runtime's own Inventory before
// and after, Inventory lists only namespaces labelled managed, and a namespace
// wrongly deleted was never in that list. The assertion is scoped to exclude
// the casualty, so it passes either way.
//
// Both directions are checked, because a refusal that also refuses our own
// namespaces would pass the first half of this and break teardown entirely.
func TestDownRefusesANamespaceItDoesNotOwn(t *testing.T) {
	const envID = "e1"
	name := "af-env-" + envID

	newRuntime := func(labels map[string]string) (*Runtime, kubernetes.Interface) {
		cli := k8sfake.NewSimpleClientset(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		})
		return &Runtime{
			cli:       cli,
			rest:      &rest.Config{Host: "https://cluster.test"},
			prefix:    "af-env-",
			readyWait: time.Second,
		}, cli
	}

	t.Run("a namespace with our name but not our label survives", func(t *testing.T) {
		// The shape a person hits: they had a namespace called af-env-e1
		// before they ever ran af, and an environment id collided with it.
		r, cli := newRuntime(map[string]string{"owner": "somebody-else"})

		_, err := r.Down(context.Background(), envID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "AF-RUN-045")

		_, getErr := cli.CoreV1().Namespaces().Get(
			context.Background(), name, metav1.GetOptions{})
		require.NoError(t, getErr, "the namespace was deleted anyway")
	})

	t.Run("a namespace we labelled is removed", func(t *testing.T) {
		r, cli := newRuntime(labelsFor(envID, "namespace"))

		_, err := r.Down(context.Background(), envID)
		require.NoError(t, err)

		_, getErr := cli.CoreV1().Namespaces().Get(
			context.Background(), name, metav1.GetOptions{})
		require.True(t, apierrors.IsNotFound(getErr),
			"teardown left the namespace it owns behind: %v", getErr)
	})
}

// TestUpRefusesANamespaceItDoesNotOwn is the other half, and the worse half.
//
// Down deleting somebody's namespace is loud: their objects are gone and they
// will notice. Up reusing it is quiet, and it is not merely additive, because
// applyPolicies runs immediately afterwards and its first policy denies all
// traffic in both directions. Everything already running in that namespace
// would stop talking to anything, with no error on either side and no obvious
// cause. So it refuses.
//
// The second case is why this cannot simply refuse every namespace that
// already exists: Up is idempotent, and the second Up of one environment finds
// the namespace the first one made.
func TestUpRefusesANamespaceItDoesNotOwn(t *testing.T) {
	const envID = "e1"
	name := "af-env-" + envID

	newRuntime := func(labels map[string]string) *Runtime {
		return &Runtime{
			cli: k8sfake.NewSimpleClientset(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			}),
			rest:      &rest.Config{Host: "https://cluster.test"},
			prefix:    "af-env-",
			readyWait: time.Second,
		}
	}

	t.Run("somebody else's namespace is not adopted", func(t *testing.T) {
		r := newRuntime(map[string]string{"owner": "somebody-else"})
		err := r.ensureNamespace(context.Background(), envID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "AF-RUN-045")
	})

	t.Run("our own namespace is reused, because Up is idempotent", func(t *testing.T) {
		r := newRuntime(labelsFor(envID, "namespace"))
		require.NoError(t, r.ensureNamespace(context.Background(), envID))
	})
}

// TestJournalledKindsAreTheOnesTheJournalKnows closes the gap the layering
// leaves open.
//
// A runtime is handed a journal callback taking a plain string and does not
// import the journal, which keeps the provider interface free of the engine's
// storage and means nothing in the compiler notices when a runtime invents a
// kind. This one did. It wrote "namespace" while journal.KindNamespace has
// been "k8s.namespace" since before this package existed, and "deployment"
// when there was no constant at all.
//
// That is not cosmetic, because Replay picks a deleter with
// reg.Lookup(rec.Provider, rec.Kind). A deleter correctly registered under
// journal.KindNamespace would not have matched one row this runtime wrote, so
// the compensation path could not have cleaned up a Kubernetes environment
// however carefully it was written.
//
// A test may import what the file under test may not, which is the whole
// reason this can be checked at all.
func TestJournalledKindsAreTheOnesTheJournalKnows(t *testing.T) {
	require.Equal(t, string(journal.KindNamespace), kindNamespace,
		"the namespace kind this runtime writes is not the one the journal names, "+
			"so nothing registered for journal.KindNamespace can ever delete it")
	require.Equal(t, string(journal.KindDeployment), kindDeployment,
		"the deployment kind this runtime writes is not the one the journal names")
}

func TestADeploymentRunsTheInstancesTheManifestAsksFor(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{
		{Name: "roller", Kind: "worker", Replicas: 3},
	}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")

	// The number this was hardcoded to one for. A manifest asking for three
	// instances of a worker got a single pod, and the run went green having
	// proved nothing about the case its author was worried about.
	require.NotNil(t, d.Spec.Replicas)
	require.Equal(t, int32(3), *d.Spec.Replicas)

	// The Service in front of these pods selects on the labels the pod
	// template carries, which is what puts all three behind the one name
	// other services resolve. Without the match the extra pods run and
	// nothing routes to them, which is three instances that behave like one.
	svc := serviceObject(spec.EnvID, "af-env-e1", spec.Services[0])
	for k, v := range svc.Spec.Selector {
		require.Equal(t, v, d.Spec.Template.Labels[k],
			"the service selects %s=%s and the pods do not carry it, so the "+
				"instances would be unreachable by name", k, v)
	}
}

func TestADeploymentThatAsksForNothingRunsOne(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{{Name: "web", Port: 8080}}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")

	// Every manifest written before instance counts existed sends zero here,
	// and zero pods is not what any of them meant.
	require.NotNil(t, d.Spec.Replicas)
	require.Equal(t, int32(1), *d.Spec.Replicas)
}

func TestTheSidecarIsAlwaysASingleton(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix, proxyRef: "af-proxy:test"}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{
		{Name: "roller", Kind: "worker", Replicas: 5},
	}}
	_, deployment, _, err := r.proxyObjects(context.Background(), "e1", "af-env-e1",
		"10.42.0.0/16", "10.43.0.10", spec)
	require.NoError(t, err)

	// The sidecar is the environment's resolver and its only route out, and
	// every service is pointed at one address for it. A second one would
	// answer half the environment's DNS from a second decision log, so the
	// run's record of what it refused would be split across two pods and
	// neither would be the whole story. Nothing about a service asking for
	// five instances may reach it.
	require.NotNil(t, deployment.Spec.Replicas)
	require.Equal(t, int32(1), *deployment.Spec.Replicas)
}

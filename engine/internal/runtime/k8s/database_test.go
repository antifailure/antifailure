package k8s

import (
	"context"
	"encoding/json"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestDatabasePoliciesOnlyOpenFixedTargetsThroughTheSidecar(t *testing.T) {
	routes := []provider.DatabaseRoute{
		{Port: 45000, Upstream: "10.20.30.40:5432"},
		{Port: 45001, Upstream: "[fd12::10]:6543"},
	}
	policies, err := databaseNetworkPolicies("e1", "af-env-e1", routes)
	require.NoError(t, err)
	require.Len(t, policies, 2)
	application := findPolicy(t, policies, "af-database-via-proxy")
	require.Len(t, application.Spec.Egress, 1)
	peer := application.Spec.Egress[0].To
	require.Len(t, peer, 1)
	require.Nil(t, peer[0].IPBlock, "an application must not receive a direct database-network route")
	require.Nil(t, peer[0].NamespaceSelector)
	require.Equal(t, ComponentProxy, peer[0].PodSelector.MatchLabels[LabelComponent])
	require.Equal(t, "e1", peer[0].PodSelector.MatchLabels[LabelEnv])
	var ports []int
	for _, port := range application.Spec.Egress[0].Ports {
		require.Equal(t, corev1.ProtocolTCP, *port.Protocol)
		ports = append(ports, port.Port.IntValue())
	}
	require.ElementsMatch(t, []int{45000, 45001}, ports)
	proxy := findPolicy(t, policies, "af-proxy-database-destinations")
	require.Equal(t, ComponentProxy, proxy.Spec.PodSelector.MatchLabels[LabelComponent])
	require.Equal(t, "e1", proxy.Spec.PodSelector.MatchLabels[LabelEnv])
	require.Len(t, proxy.Spec.Egress, 2)
	for i, expected := range []struct {
		cidr string
		port int
	}{{"10.20.30.40/32", 5432}, {"fd12::10/128", 6543}} {
		rule := proxy.Spec.Egress[i]
		require.Len(t, rule.To, 1)
		require.Equal(t, expected.cidr, rule.To[0].IPBlock.CIDR)
		require.Len(t, rule.Ports, 1)
		require.Equal(t, corev1.ProtocolTCP, *rule.Ports[0].Protocol)
		require.Equal(t, expected.port, rule.Ports[0].Port.IntValue())
	}
	// These additions never remove the existing private-address deny ranges.
	base := findPolicy(t, networkPolicies("e1", "af-env-e1", false, nil), "af-proxy-egress")
	require.Contains(t, base.Spec.Egress[0].To[0].IPBlock.Except, "10.0.0.0/8")
	require.Contains(t, base.Spec.Egress[0].To[0].IPBlock.Except, "169.254.0.0/16")
}

func TestDatabasePoliciesRejectUnpinnedAndMetadataTargets(t *testing.T) {
	for _, upstream := range []string{"db.example.test:5432", "169.254.169.254:80", "[fd00:ec2::254]:80"} {
		policies, err := databaseNetworkPolicies("e1", "af-env-e1", []provider.DatabaseRoute{{Port: 45000, Upstream: upstream}})
		require.Error(t, err)
		require.Nil(t, policies)
	}
}

func TestDatabaseTargetRefusalPrecedesAnyClusterAction(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	runtime := &Runtime{cli: client}
	_, err := runtime.Up(context.Background(), provider.EnvSpec{
		EnvID:          "refused-database",
		DatabaseRoutes: []provider.DatabaseRoute{{Port: 45000, Upstream: "169.254.169.254:80"}},
	})
	require.ErrorContains(t, err, "link-local")
	require.Empty(t, client.Actions(), "unsafe database input created or queried cluster resources")
}

func TestDatabaseRouteReachesProxyConfigurationAndServicePorts(t *testing.T) {
	runtime := &Runtime{prefix: DefaultNamespacePrefix, proxyRef: "proxy:test"}
	routes := []provider.DatabaseRoute{{Port: 45000, Upstream: "10.20.30.40:5432"}}
	secret, deployment, service, err := runtime.proxyObjects(context.Background(), "e1", "af-env-e1", "10.42.0.0/16", "10.96.0.10", provider.EnvSpec{DatabaseRoutes: routes})
	require.NoError(t, err)
	var config sidecarConfig
	require.NoError(t, json.Unmarshal(secret.Data[configKey], &config))
	require.Equal(t, routes, config.DatabaseRoutes)
	var exposed, targeted bool
	for _, port := range deployment.Spec.Template.Spec.Containers[0].Ports {
		if port.ContainerPort == 45000 && port.Protocol == corev1.ProtocolTCP {
			exposed = true
		}
	}
	for _, port := range service.Spec.Ports {
		if port.Port == 45000 && port.TargetPort.IntValue() == 45000 && port.Protocol == corev1.ProtocolTCP {
			targeted = true
		}
	}
	require.True(t, exposed, "the database listener was absent from the proxy container")
	require.True(t, targeted, "the proxy service did not route the database listener")
}

func TestDatabaseTrustMountsWithoutAnHTTPAuthority(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	runtime := &Runtime{cli: client}
	spec := provider.EnvSpec{EnvID: "e1", DatabaseCACertPEM: "AF_FAKE_DATABASE_CA"}
	require.NoError(t, runtime.ensureCASecret(context.Background(), spec, "af-env-e1"))
	secret, err := client.CoreV1().Secrets("af-env-e1").Get(context.Background(), caSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"database-ca.crt": []byte(spec.DatabaseCACertPEM)}, secret.Data)
	service := provider.ServiceSpec{Name: "app", Image: "app:test", Migrate: "migrate"}
	for _, migration := range []bool{false, true} {
		container := containerFor(spec, service, migration)
		require.Len(t, container.VolumeMounts, 1)
		mount := container.VolumeMounts[0]
		require.True(t, mount.ReadOnly)
		require.Equal(t, provider.DatabaseTrustBundlePath, path.Join(mount.MountPath, "database-ca.crt"))
		for _, env := range container.Env {
			require.NotContains(t, []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE"}, env.Name,
				"the database CA was added to HTTP trust")
		}
	}
	deployment := runtime.deploymentFor(spec, service, "af-env-e1", "10.96.0.10")
	require.Len(t, deployment.Spec.Template.Spec.Volumes, 1)
	job := runtime.oneShotJob(spec, "af-env-e1", "10.96.0.10", "migration", "migrate", containerFor(spec, service, true))
	require.Len(t, job.Spec.Template.Spec.Volumes, 1, "the migration pod never received the database CA file")
}

func TestDatabaseAndHTTPTrustRemainDistinct(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	runtime := &Runtime{cli: client, proxyRef: "proxy:test"}
	spec := provider.EnvSpec{
		EnvID: "e1", CACertPEM: "AF_FAKE_HTTP_CA", CAKeyPEM: secrets.New("AF_FAKE_HTTP_KEY"),
		DatabaseCACertPEM: "AF_FAKE_DATABASE_CA",
	}
	require.NoError(t, runtime.ensureCASecret(context.Background(), spec, "af-env-e1"))
	secret, err := client.CoreV1().Secrets("af-env-e1").Get(context.Background(), caSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, []byte(spec.CACertPEM), secret.Data["ca.crt"])
	require.Equal(t, []byte(spec.DatabaseCACertPEM), secret.Data["database-ca.crt"])
	for _, body := range secret.Data {
		require.NotContains(t, string(body), "AF_FAKE_HTTP_KEY", "a public certificate secret acquired a signing key")
	}
	proxySecret, _, _, err := runtime.proxyObjects(context.Background(), "e1", "af-env-e1", "10.42.0.0/16", "10.96.0.10", spec)
	require.NoError(t, err)
	var config sidecarConfig
	require.NoError(t, json.Unmarshal(proxySecret.Data[configKey], &config))
	require.Equal(t, spec.CACertPEM, config.CACert)
	require.NotContains(t, string(proxySecret.Data[configKey]), "AF_FAKE_DATABASE_CA")
}

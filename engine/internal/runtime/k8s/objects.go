package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// configPath is where the sidecar's configuration is mounted.
const configPath = "/etc/antifailure/proxy.json"

// configKey is the key inside the Secret holding it.
const configKey = "proxy.json"

// sidecarConfig mirrors the sidecar's own Config, exactly as the local
// runtime's copy does and for the same reason: the runtime must not depend on
// a main package.
type sidecarConfig struct {
	Egress      schema.Egress     `json:"egress"`
	Subnet      string            `json:"subnet"`
	Internal    []string          `json:"internal"`
	EnvID       string            `json:"env_id"`
	MockPacks   []string          `json:"mock_packs,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Resolver    string            `json:"resolver,omitempty"`
	CACert      string            `json:"ca_cert,omitempty"`
	CAKey       string            `json:"ca_key,omitempty"`
}

// falseRef is a pointer to false, needed by several pod fields.
func falseRef() *bool         { b := false; return &b }
func trueRef() *bool          { b := true; return &b }
func int32Ref(n int32) *int32 { return &n }
func int64Ref(n int64) *int64 { return &n }

// namespaceFor builds the environment's namespace.
func (r *Runtime) namespaceObject(envID string) *corev1.Namespace {
	labels := labelsFor(envID, "namespace")
	if r.ttl > 0 {
		now := r.clock.Now()
		labels[dockerutil.LabelExpires] = strconv.FormatInt(now.Add(r.ttl).UTC().Unix(), 10)
	}
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   r.namespace(envID),
			Labels: labels,
		},
	}
}

// networkPolicies are the whole containment story, as objects.
//
// Read them in order, because the order is the argument. The first denies
// everything in both directions, which is the state an environment would be
// in if the rest failed to apply: unreachable rather than unrestricted. The
// rest add back exactly what an environment needs and nothing else.
func networkPolicies(envID, namespace string, hasIngress bool) []*networkingv1.NetworkPolicy {
	serviceSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{LabelComponent: ComponentService},
	}
	// The escape probe is governed by exactly the rules a service is governed
	// by, which is the only way its verdict means anything. A probe with
	// stricter rules than a service would prove that a pod with no egress
	// allowance cannot escape, which is not the question: the question is
	// whether a pod that IS allowed to reach the sidecar can reach anything
	// else, and that has to be asked of the real rule set.
	//
	// Selecting the probe here is necessary and it is not sufficient, and the
	// missing half cost a real escape. The allowance below names the sidecar
	// with a pod selector, so it is inert until a sidecar exists. Running the
	// probe before the sidecar therefore put it back under default-deny alone,
	// selector or no selector. Up runs it after the sidecar for that reason;
	// moving it earlier silently restores the hole.
	containedSelector := metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{{
			Key:      LabelComponent,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{ComponentService, ComponentProbe},
		}},
	}
	proxySelector := metav1.LabelSelector{
		MatchLabels: map[string]string{LabelComponent: ComponentProxy},
	}
	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	proxyPort := intstr.FromInt32(ProxyPort)
	dns := intstr.FromInt32(dnsPort)
	// The sidecar also listens transparently on 80 and 443, and those are the
	// ports that matter most. A client that reads its proxy variables talks to
	// 3128; a client that ignores them, which is Node and a great many SDKs,
	// resolves the name, gets the sidecar's own address back, and connects to
	// it on the ordinary port. Leaving these out let the policy block the one
	// case the whole design exists for, and it failed in the direction that
	// looks safe: everything was contained, and a host the policy ALLOWED was
	// unreachable too.
	httpPort := intstr.FromInt32(80)
	httpsPort := intstr.FromInt32(443)

	meta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Name: name, Namespace: namespace, Labels: labelsFor(envID, "policy"),
		}
	}

	policies := []*networkingv1.NetworkPolicy{
		{
			// Everything, both directions, denied. An empty rule list is a
			// deny rather than an allow, which is the one piece of
			// NetworkPolicy semantics worth being sure of: this object with
			// its Egress list removed would permit all egress instead.
			ObjectMeta: meta("af-default-deny"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{
					networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress,
				},
			},
		},
		{
			// The only way out of a service: the sidecar. Proxying on 3128 and
			// DNS on 53, and nothing else, so a service cannot even resolve a
			// name except through the thing that logs what it asked for.
			ObjectMeta: meta("af-service-egress-to-proxy"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: containedSelector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To: []networkingv1.NetworkPolicyPeer{{PodSelector: &proxySelector}},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &tcp, Port: &proxyPort},
						{Protocol: &tcp, Port: &httpPort},
						{Protocol: &tcp, Port: &httpsPort},
						{Protocol: &udp, Port: &dns},
						{Protocol: &tcp, Port: &dns},
					},
				}},
			},
		},
		{
			// Services may talk to each other, which is what a manifest means
			// when one service names another. Same namespace only: the peer
			// selector is a pod selector, which never matches outside it.
			ObjectMeta: meta("af-service-to-service"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: serviceSelector,
				PolicyTypes: []networkingv1.PolicyType{
					networkingv1.PolicyTypeEgress, networkingv1.PolicyTypeIngress,
				},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To: []networkingv1.NetworkPolicyPeer{{PodSelector: &serviceSelector}},
				}},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &serviceSelector},
						{PodSelector: &proxySelector},
					},
				}},
			},
		},
		{
			// The sidecar is the only pod with a route out, and even it does
			// not get an unqualified one. The excluded ranges are the ones an
			// environment must never reach whatever its policy says: the link
			// local block, which carries the cloud metadata endpoint and the
			// credentials of the node this pod happens to be on, and the
			// private ranges, which carry the cluster's own control plane and
			// whatever else lives on the operator's network.
			ObjectMeta: meta("af-proxy-egress"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: proxySelector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{
					{
						To: []networkingv1.NetworkPolicyPeer{{
							IPBlock: &networkingv1.IPBlock{
								CIDR: "0.0.0.0/0",
								Except: []string{
									"169.254.0.0/16",
									"10.0.0.0/8",
									"172.16.0.0/12",
									"192.168.0.0/16",
								},
							},
						}},
					},
					{
						// Except the cluster's own resolver, which the sidecar
						// forwards internal names to and which lives on one of
						// the ranges just excluded.
						To: []networkingv1.NetworkPolicyPeer{{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": "kube-system",
								},
							},
						}},
						Ports: []networkingv1.NetworkPolicyPort{
							{Protocol: &udp, Port: &dns},
							{Protocol: &tcp, Port: &dns},
						},
					},
				},
			},
		},
		{
			// Services reach the sidecar, so the sidecar has to accept them.
			ObjectMeta: meta("af-proxy-ingress"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: proxySelector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{
						{PodSelector: &serviceSelector},
						{PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{LabelComponent: ComponentProbe},
						}},
					},
				}},
			},
		},
	}

	if hasIngress {
		// Only when there is an ingress controller to let in. Without a
		// domain there is no ingress, and opening the namespace to every
		// other namespace on the cluster for a route nothing serves would be
		// weakening isolation for nothing.
		policies = append(policies, &networkingv1.NetworkPolicy{
			ObjectMeta: meta("af-ingress-controller"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: serviceSelector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{},
						PodSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{{
								Key:      "app.kubernetes.io/name",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"ingress-nginx", "traefik"},
							}},
						},
					}},
				}},
			},
		})
	}
	return policies
}

// probeSelector is the label a containment probe pod carries.
func probeLabels(envID string) map[string]string {
	l := labelsFor(envID, ComponentProbe)
	return l
}

// internalNames are the names the sidecar must forward rather than answer.
//
// Every form a cluster resolver might see, because a pod's search list turns
// one name into several and the sidecar matches what actually arrives. Missing
// the qualified forms means the sidecar answers with its own address for a
// service's own name, and a service calling another service reaches the proxy,
// which decides it against an egress policy that has never heard of it.
func internalNames(namespace string, services []provider.ServiceSpec) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	names := []string{ProxyName}
	for _, s := range services {
		names = append(names, s.Name)
	}
	for _, n := range names {
		add(n)
		add(n + "." + namespace)
		add(n + "." + namespace + ".svc")
		add(n + "." + namespace + ".svc.cluster.local")
	}
	sort.Strings(out)
	return out
}

// proxyObjects builds the sidecar's Secret, Deployment and Service.
func (r *Runtime) proxyObjects(
	ctx context.Context,
	envID, namespace, subnet, resolver string,
	spec provider.EnvSpec,
) (*corev1.Secret, *appsv1.Deployment, *corev1.Service, error) {
	proxyRef, imageErr := r.proxyImage(ctx)
	if imageErr != nil {
		return nil, nil, nil, imageErr
	}
	egress := schema.Egress{}
	if spec.Egress != nil {
		egress = *spec.Egress
	}
	credentials := map[string]string{}
	for name, value := range spec.SandboxCredentials {
		credentials[name] = value.Reveal()
	}
	cfg := sidecarConfig{
		Egress:    egress,
		Subnet:    subnet,
		Internal:  internalNames(namespace, spec.Services),
		EnvID:     envID,
		MockPacks: spec.MockPacks,
		// The sidecar forwards to an endpoint, so the port goes on here and
		// nowhere else. A pod's nameserver is a bare address.
		Credentials: credentials,
		Resolver:    net.JoinHostPort(resolver, strconv.Itoa(dnsPort)),
		CACert:      spec.CACertPEM,
	}
	if spec.CAKeyPEM.Reveal() != "" {
		cfg.CAKey = spec.CAKeyPEM.Reveal()
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, nil, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "the sidecar configuration could not be encoded")
	}

	// A Secret and not a ConfigMap. It carries the environment's certificate
	// authority private key and every sandbox credential, and a ConfigMap is
	// readable by anything that can read the namespace and is printed in full
	// by kubectl describe.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: ProxyName, Namespace: namespace, Labels: labelsFor(envID, ComponentProxy),
		},
		Data: map[string][]byte{configKey: body},
	}

	labels := labelsFor(envID, ComponentProxy)
	labels[LabelService] = ProxyName

	var env []corev1.EnvVar
	for _, kv := range spec.ModelEnv {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env = append(env, corev1.EnvVar{Name: name, Value: value})
		}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: ProxyName, Namespace: namespace, Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ref(1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				LabelEnv: envID, LabelComponent: ComponentProxy,
			}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: falseRef(),
					Containers: []corev1.Container{{
						Name:  "proxy",
						Image: proxyRef,
						Args:  []string{"-config", configPath},
						Env:   env,
						Ports: []corev1.ContainerPort{
							{ContainerPort: ProxyPort, Protocol: corev1.ProtocolTCP},
							{ContainerPort: dnsPort, Protocol: corev1.ProtocolUDP},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name: "config", MountPath: "/etc/antifailure", ReadOnly: true,
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: falseRef(),
							// The sidecar reads its configuration and writes
							// its decisions to stdout. It opens no file, so
							// there is nothing for a writable root to be for.
							ReadOnlyRootFilesystem: trueRef(),
							RunAsNonRoot:           trueRef(),
							RunAsUser:              int64Ref(65532),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								// Everything dropped except the one thing it
								// cannot work without. The sidecar answers DNS
								// on port 53, and a process that is not root
								// cannot bind a port below 1024 without this.
								// Without it the container starts, fails to
								// listen, and every name lookup in the
								// environment goes unanswered, which reads as
								// an environment whose services cannot find
								// anything rather than as a missing
								// capability.
								Add: []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: ProxyName},
						},
					}},
				},
			},
		},
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: ProxyName, Namespace: namespace, Labels: labelsFor(envID, ComponentProxy),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{LabelEnv: envID, LabelComponent: ComponentProxy},
			Ports: []corev1.ServicePort{
				{Name: "proxy", Port: ProxyPort, Protocol: corev1.ProtocolTCP,
					TargetPort: intstr.FromInt32(ProxyPort)},
				{Name: "dns-udp", Port: dnsPort, Protocol: corev1.ProtocolUDP,
					TargetPort: intstr.FromInt32(dnsPort)},
			},
		},
	}
	return secret, deployment, service, nil
}

// podDNS points a pod at the sidecar and nowhere else.
//
// ndots is 1 rather than the cluster's usual 5, and that number is doing real
// work. With the default, a lookup of example.com is tried as
// example.com.<namespace>.svc.cluster.local first, which is not a name the
// sidecar has been told is internal, so the sidecar answers it with its own
// address and the connection that follows carries a Host header no egress rule
// can match. The policy would then refuse a host it was configured to allow,
// for a reason nothing in the decision log would explain.
func podDNS(namespace, resolverIP string) (corev1.DNSPolicy, *corev1.PodDNSConfig) {
	ndots := "1"
	return corev1.DNSNone, &corev1.PodDNSConfig{
		Nameservers: []string{resolverIP},
		Searches: []string{
			namespace + ".svc.cluster.local",
			"svc.cluster.local",
			"cluster.local",
		},
		Options: []corev1.PodDNSConfigOption{{Name: "ndots", Value: &ndots}},
	}
}

// serviceEnv is the environment one service receives.
func serviceEnv(spec provider.EnvSpec, s provider.ServiceSpec, migration bool) []corev1.EnvVar {
	proxyURL := fmt.Sprintf("http://%s:%d", ProxyName, ProxyPort)
	out := []corev1.EnvVar{
		{Name: "HTTP_PROXY", Value: proxyURL},
		{Name: "HTTPS_PROXY", Value: proxyURL},
		{Name: "http_proxy", Value: proxyURL},
		{Name: "https_proxy", Value: proxyURL},
		// Never the whole cluster. Only this environment's own names bypass
		// the proxy, because everything else is a decision.
		//
		// The service names are listed one by one as well as by suffix, and
		// they have to be: a manifest says http://api:3000, which is a bare
		// name, and a bare name does not match a .svc suffix. Without them
		// every service to service call is sent to the proxy and refused by
		// the egress policy.
		{Name: "NO_PROXY", Value: noProxyFor(spec)},
		{Name: "no_proxy", Value: noProxyFor(spec)},
		{Name: "AF_ENV_ID", Value: spec.EnvID},
		{Name: "AF_SERVICE", Value: s.Name},
	}
	url := spec.DatabaseURL
	if migration && spec.MigrationDatabaseURL.Reveal() != "" {
		// A migration must not run through a transaction pooler: it does not
		// support the session level features migrations use, and the failure
		// is a migration that half applies rather than one that refuses.
		url = spec.MigrationDatabaseURL
	}
	if v := url.Reveal(); v != "" {
		out = append(out, corev1.EnvVar{Name: "DATABASE_URL", Value: v})
	}
	if spec.CACertPEM != "" {
		out = append(out,
			corev1.EnvVar{Name: "NODE_EXTRA_CA_CERTS", Value: caPath},
			corev1.EnvVar{Name: "SSL_CERT_FILE", Value: caPath},
			corev1.EnvVar{Name: "REQUESTS_CA_BUNDLE", Value: caPath},
		)
	}
	names := make([]string, 0, len(s.Env))
	for name := range s.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, corev1.EnvVar{Name: name, Value: s.Env[name].Reveal()})
	}
	return out
}

// noProxyFor lists everything inside the environment that must not be sent to
// the sidecar.
func noProxyFor(spec provider.EnvSpec) string {
	out := []string{"localhost", "127.0.0.1", ProxyName, ".svc", ".cluster.local"}
	for _, s := range spec.Services {
		if s.Name != "" {
			out = append(out, s.Name)
		}
	}
	return strings.Join(out, ",")
}

// caPath is where the environment certificate is mounted, when there is one.
const (
	caSecretName = "af-ca"
	caPath       = "/etc/antifailure/ca.crt"
)

// containerFor builds the container one service runs.
func containerFor(spec provider.EnvSpec, s provider.ServiceSpec, migration bool) corev1.Container {
	c := corev1.Container{
		Name:  "app",
		Image: s.Image,
		Env:   serviceEnv(spec, s, migration),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: falseRef(),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
	command := s.Command
	if migration {
		command = s.Migrate
	}
	if command != "" {
		// The same shape the local runtime uses, and the entrypoint is
		// cleared for the same reason: a migration that inherited an
		// entrypoint starting the server would run the application instead of
		// the migration and report success when it was killed.
		c.Command = []string{"/bin/sh", "-c", command}
	}
	if !migration && s.Port > 0 {
		c.Ports = []corev1.ContainerPort{{ContainerPort: int32(s.Port), Protocol: corev1.ProtocolTCP}}
		c.ReadinessProbe = readinessProbe(s)
	}
	if spec.CACertPEM != "" {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name: "af-ca", MountPath: "/etc/antifailure", ReadOnly: true,
		})
	}
	// The manifest's resources block is deliberately not read here, because
	// it never arrives: provider.ServiceSpec carries no CPU or memory, so the
	// values are dropped between the manifest and every runtime. Honouring
	// them on this runtime alone would make one runtime enforce a cap the
	// other silently ignores, which is worse than neither doing it.
	return c
}

// deploymentFor builds one service's Deployment.
func (r *Runtime) deploymentFor(
	spec provider.EnvSpec, s provider.ServiceSpec, namespace, resolverIP string,
) *appsv1.Deployment {
	labels := labelsFor(spec.EnvID, ComponentService)
	labels[LabelService] = s.Name
	labels[LabelServiceKind] = s.Kind

	dnsPolicy, dnsConfig := podDNS(namespace, resolverIP)
	pod := corev1.PodSpec{
		// A pod that can read a service account token can talk to the API
		// server, and an environment that can talk to the API server can
		// rewrite the policy that contains it.
		AutomountServiceAccountToken: falseRef(),
		DNSPolicy:                    dnsPolicy,
		DNSConfig:                    dnsConfig,
		// Always, because a Deployment may not say anything else: Kubernetes
		// rejects a Deployment whose pod template restarts on any other
		// policy, and the rejection is a validation error on Up rather than
		// anything visible later.
		//
		// The local runtime disables restarts so that a service which crash
		// loops is visible as one instead of being hidden behind a runtime
		// that keeps starting it. That intent survives here for a different
		// reason: a pod that keeps failing lands in CrashLoopBackOff, which
		// Status reports, and its exit code is kept in the container's last
		// termination state rather than being lost on the restart.
		RestartPolicy: corev1.RestartPolicyAlways,
		Containers:    []corev1.Container{containerFor(spec, s, false)},
	}
	if spec.CACertPEM != "" {
		pod.Volumes = append(pod.Volumes, corev1.Volume{
			Name: "af-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: caSecretName},
			},
		})
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name, Namespace: namespace, Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ref(1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				LabelEnv: spec.EnvID, LabelService: s.Name,
			}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       pod,
			},
		},
	}
}

// migrationJob builds the Job that runs a service's migration to completion.
func (r *Runtime) migrationJob(
	spec provider.EnvSpec, s provider.ServiceSpec, namespace, resolverIP string,
) *batchv1.Job {
	labels := labelsFor(spec.EnvID, ComponentService)
	labels[LabelService] = s.Name + "-migrate"

	dnsPolicy, dnsConfig := podDNS(namespace, resolverIP)
	container := containerFor(spec, s, true)
	container.Name = "migrate"

	pod := corev1.PodSpec{
		AutomountServiceAccountToken: falseRef(),
		DNSPolicy:                    dnsPolicy,
		DNSConfig:                    dnsConfig,
		RestartPolicy:                corev1.RestartPolicyNever,
		Containers:                   []corev1.Container{container},
	}
	if spec.CACertPEM != "" {
		pod.Volumes = append(pod.Volumes, corev1.Volume{
			Name: "af-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: caSecretName},
			},
		})
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name + "-migrate", Namespace: namespace, Labels: labels,
		},
		Spec: batchv1.JobSpec{
			// Never retried. A migration that failed has to be reported as
			// failed, and a backoff turns one clear failure into six minutes
			// of a Job that is neither running nor finished.
			BackoffLimit: int32Ref(0),
			Template:     corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: pod},
		},
	}
}

// serviceObject exposes a service inside the namespace, so that a manifest can
// say http://worker:8080 and mean it.
func serviceObject(envID, namespace string, s provider.ServiceSpec) *corev1.Service {
	labels := labelsFor(envID, ComponentService)
	labels[LabelService] = s.Name
	port := s.Port
	if port <= 0 {
		port = 80
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{LabelEnv: envID, LabelService: s.Name},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: int32(port), Protocol: corev1.ProtocolTCP,
				TargetPort: intstr.FromInt32(int32(port)),
			}},
		},
	}
}

// ingressFor publishes a web service under the wildcard domain.
func (r *Runtime) ingressFor(envID, namespace string, s provider.ServiceSpec) *networkingv1.Ingress {
	host := r.hostFor(envID, s.Name)
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name, Namespace: namespace, Labels: labelsFor(envID, ComponentService),
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: s.Name,
									Port: networkingv1.ServiceBackendPort{Number: int32(s.Port)},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if r.ingress != "" {
		ing.Spec.IngressClassName = &r.ingress
	}
	return ing
}

// hostFor is the preview hostname one service is published at.
func (r *Runtime) hostFor(envID, service string) string {
	return fmt.Sprintf("%s-%s.%s", sanitize(envID), sanitize(service), strings.TrimPrefix(r.domain, "*."))
}

// coveringCIDR returns the smallest network containing all of the given ones.
//
// The sidecar is told one network and finds its own address inside it, which
// on Docker is the environment's bridge. A cluster hands out a pod range per
// node, so on a cluster with more than one node there is no single range to
// hand over, and the covering network is the honest answer: it is what every
// pod address on the cluster has in common.
func coveringCIDR(cidrs []string) (string, error) {
	var nets []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil || n.IP.To4() == nil {
			continue
		}
		nets = append(nets, n)
	}
	if len(nets) == 0 {
		return "", fmt.Errorf("no IPv4 pod network")
	}
	base := nets[0].IP.To4()
	ones, bits := nets[0].Mask.Size()
	for _, n := range nets[1:] {
		ip := n.IP.To4()
		o, _ := n.Mask.Size()
		if o < ones {
			ones = o
		}
		// Shorten the prefix until both addresses agree under it.
		for ones > 0 {
			mask := net.CIDRMask(ones, bits)
			if net.IP(maskIP(base, mask)).Equal(net.IP(maskIP(ip, mask))) {
				break
			}
			ones--
		}
	}
	mask := net.CIDRMask(ones, bits)
	return (&net.IPNet{IP: net.IP(maskIP(base, mask)), Mask: mask}).String(), nil
}

func maskIP(ip net.IP, mask net.IPMask) []byte {
	out := make([]byte, len(ip))
	for i := range ip {
		out[i] = ip[i] & mask[i]
	}
	return out
}

// clusterPodCIDR is the network every pod on this cluster has an address on.
func (r *Runtime) clusterPodCIDR(ctx context.Context) (string, error) {
	nodes, err := r.cli.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	var cidrs []string
	for _, n := range nodes.Items {
		if n.Spec.PodCIDR != "" {
			cidrs = append(cidrs, n.Spec.PodCIDR)
		}
		cidrs = append(cidrs, n.Spec.PodCIDRs...)
	}
	covering, err := coveringCIDR(cidrs)
	if err != nil {
		return "", aferrors.Coded(aferrors.AFRUN002, "endpoint", r.rest.Host,
			"detail", "this cluster's nodes do not report a pod network, so the sidecar "+
				"cannot be told which of its addresses to answer on")
	}
	return covering, nil
}

// readinessProbe decides when a service is answering.
//
// The two cases mirror the local runtime's. With no health path declared, a
// service is ready when its port accepts a connection, because asking for more
// would mean inventing a protocol the application does not speak. With one
// declared, it is polled.
//
// The difference from the local runtime is worth knowing, and it is documented
// on the Kubernetes runtime page rather than hidden here. Locally, any HTTP
// status counts as ready, including a 500, because readiness there means the
// process is listening and routing. Kubernetes decides readiness itself and
// treats 4xx and 5xx as not ready. So a service whose declared health path
// answers 500 comes up locally and does not come up here. Declaring a health
// path is a statement that the path reports health, so this is the more
// defensible of the two, but it is a real difference between the runtimes and
// pretending otherwise would be worse than saying it.
func readinessProbe(s provider.ServiceSpec) *corev1.Probe {
	probe := &corev1.Probe{
		PeriodSeconds:    2,
		FailureThreshold: 60,
		TimeoutSeconds:   3,
	}
	if s.HealthPath == "" {
		probe.ProbeHandler = corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(int32(s.Port))},
		}
		return probe
	}
	probe.ProbeHandler = corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: s.HealthPath, Port: intstr.FromInt32(int32(s.Port)),
		},
	}
	return probe
}

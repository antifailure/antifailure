// Package k8s runs an environment as a namespace on a Kubernetes cluster.
//
// The containment story is the same one the local runtime tells and it is
// worth restating in the cluster's vocabulary, because the mechanism is
// different and the guarantee must not be. Every environment gets its own
// namespace with a default deny egress NetworkPolicy, and the only egress that
// policy permits is to the environment's own egress sidecar. Services are told
// to resolve every name through that sidecar, which answers with its own
// address for anything outside the environment and forwards anything inside it
// to the cluster's resolver. So a service that ignores its proxy variables
// still cannot reach the internet, for the same reason it cannot on Docker:
// the packet has nowhere to go.
//
// There is one thing that is true here and is not true of the local runtime,
// and it is the reason this package refuses to bring an environment up until
// it has checked. A NetworkPolicy is a request to the CNI. Several CNIs, kind's
// own kindnet among them, accept a NetworkPolicy object and enforce nothing at
// all, which produces a cluster where every containment test passes while
// nothing is contained. Applying policy and assuming enforcement would turn
// this product's central guarantee into a YAML file nobody validated, so
// Up runs a probe that tries to escape and refuses to continue if it can.
package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Label keys. They mirror the local runtime's so that one leak detector can
// read both and one person can read either without translating.
const (
	LabelManaged     = "dev.antifailure.managed"
	LabelEnv         = "dev.antifailure.env"
	LabelService     = "dev.antifailure.service"
	LabelServiceKind = "dev.antifailure.service-kind"
	LabelComponent   = "dev.antifailure.component"
)

// Component values.
const (
	ComponentService = "service"
	ComponentProxy   = "proxy"
	ComponentProbe   = "probe"
)

// ProxyName is what the sidecar's Deployment and Service are called inside an
// environment's namespace, and ProxyPort is where it proxies.
//
// Fixed, for the same reason the local runtime fixes them: they appear in
// every service's proxy variables and in every decision log line, and a name
// that changed per environment would make two runs impossible to compare.
const (
	ProxyName = "af-proxy"
	ProxyPort = 3128
	dnsPort   = 53
)

// DefaultNamespacePrefix is prepended to an environment id.
//
// A prefix rather than a bare id, because namespaces are a cluster wide
// namespace of their own and an environment called "test" would otherwise be
// one keystroke from somebody's real one.
const DefaultNamespacePrefix = "af-env-"

// DefaultReadyTimeout matches the local runtime's, and for the same reason: it
// is long enough for a framework that compiles on first request and short
// enough that a service which will never answer is reported rather than
// waited on. A cluster adds image pulls and scheduling to that, so it is not
// generous here.
const DefaultReadyTimeout = 5 * time.Minute

// Runtime places environments on a Kubernetes cluster.
type Runtime struct {
	cli       kubernetes.Interface
	rest      *rest.Config
	clock     clock.Clock
	redactor  *redact.Redactor
	prefix    string
	domain    string
	ingress   string
	resolver  string
	images    ImageLoader
	proxyRef  string
	readyWait time.Duration
	// skipContainmentCheck is never set by anything a user can reach.
	//
	// It exists for exactly one test, the one that proves the containment
	// check itself refuses a cluster that does not enforce policy, which
	// cannot run if the check has already refused. There is deliberately no
	// option, environment variable or manifest key that reaches it, because
	// the moment there is one, the first cluster where the check is
	// inconvenient is the cluster where somebody turns it off.
	skipContainmentCheck bool
}

// Options configure the runtime.
type Options struct {
	// Kubeconfig is the path to a kubeconfig file. Empty uses the usual
	// discovery: in-cluster configuration if there is any, then KUBECONFIG,
	// then the file in the home directory.
	Kubeconfig string
	// Context is the kubeconfig context to use. Empty uses the current one.
	//
	// This is runtime.kubeconfig_context in the manifest, and it matters more
	// than it looks: the difference between the right cluster and the
	// production one is usually a context name somebody did not check.
	Context string
	// NamespacePrefix is prepended to the environment id. Empty uses
	// DefaultNamespacePrefix.
	NamespacePrefix string
	// Domain is the wildcard domain preview URLs are published under. Empty
	// means no ingress is created and the runtime says it has none, rather
	// than reporting a URL nothing resolves.
	Domain string
	// IngressClass names the ingress controller. Empty uses the cluster's
	// default ingress class.
	IngressClass string
	// Resolver is the cluster DNS service address internal names are
	// forwarded to. Empty discovers it from the kube-dns service.
	Resolver string
	// ProxyImage is the sidecar image reference. Required: this package does
	// not build images, and the engine that does knows the reference.
	ProxyImage string
	// Images makes an image available to the cluster. Nil means every image
	// is already pullable from where the nodes can reach.
	Images ImageLoader
	// ReadyTimeout bounds a readiness wait. Zero uses the default.
	ReadyTimeout time.Duration

	Clock    clock.Clock
	Redactor *redact.Redactor
}

// New returns a runtime talking to a cluster.
func New(opts Options) (*Runtime, error) {
	cfg, err := restConfig(opts)
	if err != nil {
		return nil, err
	}
	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", cfg.Host)
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Redactor == nil {
		opts.Redactor = redact.New()
	}
	if opts.NamespacePrefix == "" {
		opts.NamespacePrefix = DefaultNamespacePrefix
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = DefaultReadyTimeout
	}
	if opts.Images == nil {
		// A development cluster is the one case where an image can be put
		// where the nodes can see it without a registry, and the only place
		// the tool that made the cluster leaves a mark is the context name.
		// The match is exact rather than a guess, so a real cluster falls
		// through to pulling images the way it always would.
		if loader, ok := LocalClusterLoaderFor(contextName(opts)); ok {
			opts.Images = loader
		} else {
			opts.Images = NoImageLoader{}
		}
	}
	return &Runtime{
		cli: cli, rest: cfg, clock: opts.Clock, redactor: opts.Redactor,
		prefix: opts.NamespacePrefix, domain: opts.Domain,
		ingress: opts.IngressClass, resolver: opts.Resolver,
		images: opts.Images, proxyRef: opts.ProxyImage,
		readyWait: opts.ReadyTimeout,
	}, nil
}

// restConfig resolves how to reach the cluster.
func restConfig(opts Options) (*rest.Config, error) {
	if opts.Kubeconfig == "" && opts.Context == "" {
		// Running inside the cluster is the case where there is no kubeconfig
		// at all, and it is the case a control plane runs in.
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.Kubeconfig != "" {
		rules.ExplicitPath = opts.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: opts.Context}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		detail := "no reachable cluster: " + err.Error()
		if opts.Context != "" {
			detail = fmt.Sprintf("kubeconfig context %q could not be used: %v", opts.Context, err)
		}
		return nil, aferrors.Coded(aferrors.AFRUN002, "endpoint", "kubernetes", "detail", detail)
	}
	return cfg, nil
}

// contextName resolves which kubeconfig context is actually in use, which is
// not always the one named: an empty Context means whichever is current.
func contextName(opts Options) string {
	if opts.Context != "" {
		return opts.Context
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.Kubeconfig != "" {
		rules.ExplicitPath = opts.Kubeconfig
	}
	raw, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{}).RawConfig()
	if err != nil {
		return ""
	}
	return raw.CurrentContext
}

// Name identifies the runtime.
func (r *Runtime) Name() string { return "kubernetes" }

// Capabilities declares what this runtime can do.
func (r *Runtime) Capabilities() provider.RuntimeCaps {
	return provider.RuntimeCaps{
		// Only with a domain to publish under. Without one there is no
		// ingress and no address a caller could reach, and saying otherwise
		// would mean af up printing a URL that resolves to nothing.
		Ingress: r.domain != "",
		Logs:    true,
		// A database container on the machine that ran af is not reachable
		// from a cluster, so the database has to be one the environment can
		// already reach. Declaring otherwise would make the engine attach a
		// branch that no pod can connect to.
		AttachesLocalDatabase: false,
	}
}

// Close releases the runtime's resources. The client holds no connection that
// outlives a request, so there is nothing to release, and saying so is better
// than an empty method that looks unfinished.
func (r *Runtime) Close() error { return nil }

// namespace is the namespace an environment lives in.
func (r *Runtime) namespace(envID string) string {
	return r.prefix + sanitize(envID)
}

// sanitize turns an environment id into something a namespace may be called.
//
// Namespaces are DNS-1123 labels: lower case letters, digits and hyphens, 63
// characters, starting and ending with an alphanumeric. Environment ids are
// derived from branch names, which routinely contain slashes, capitals and
// dots, so a name that is merely prefixed would be rejected by the API server
// with a message about a regular expression rather than about the branch.
func sanitize(id string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(id) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '-':
			b.WriteRune('-')
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "env"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// labels are what every object this runtime creates carries.
func labelsFor(envID, component string) map[string]string {
	return map[string]string{
		LabelManaged:   "true",
		LabelEnv:       envID,
		LabelComponent: component,
	}
}

// managedSelector matches everything this runtime owns.
func managedSelector() metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: LabelManaged + "=true"}
}

// clusterResolver finds the address internal names are forwarded to.
//
// Discovered rather than configured, because it is the one piece of cluster
// specific information that is both required and knowable: every conformant
// cluster has a DNS service, and asking is more reliable than a default that
// is right on most clusters and silently wrong on the rest.
func (r *Runtime) clusterResolver(ctx context.Context) (string, error) {
	if r.resolver != "" {
		return r.resolver, nil
	}
	for _, name := range []string{"kube-dns", "coredns", "rke2-coredns-rke2-coredns"} {
		svc, err := r.cli.CoreV1().Services("kube-system").Get(ctx, name, metav1.GetOptions{})
		if err != nil || svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == corev1.ClusterIPNone {
			continue
		}
		return fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, dnsPort), nil
	}
	return "", aferrors.Coded(aferrors.AFRUN002, "endpoint", "kubernetes",
		"detail", "no DNS service was found in kube-system, so the sidecar has nowhere "+
			"to forward the names inside the environment. Set runtime.resolver if this "+
			"cluster's resolver is somewhere else")
}

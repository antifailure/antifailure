package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Up creates the environment and returns once every service that declares
// readiness has answered.
//
// The order is the containment argument, so it is worth reading as one. The
// namespace and its policies come first, because a pod that started before the
// policy that contains it has already had a window in which it could reach
// anything. The escape probe comes next, before a single service image runs,
// because a cluster whose CNI ignores NetworkPolicy accepts every object here
// and enforces none of them, and there is no later moment at which that would
// be discovered. Only then does the sidecar start, and only then the services.
func (r *Runtime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	if spec.EnvID == "" {
		return provider.Env{}, aferrors.Coded(aferrors.AFRUN040, "detail", "the environment has no id")
	}
	journal := spec.Journal
	if journal == nil {
		journal = func(string, string) error { return nil }
	}
	progress := spec.Progress
	if progress == nil {
		progress = func(string) {}
	}
	namespace := r.namespace(spec.EnvID)
	env := provider.Env{EnvID: spec.EnvID, NetworkID: namespace, CreatedAt: r.clock.Now().UTC()}

	order, err := startOrder(spec.Services)
	if err != nil {
		return env, err
	}

	resolver, err := r.clusterResolver(ctx)
	if err != nil {
		return env, err
	}
	podCIDR, err := r.clusterPodCIDR(ctx)
	if err != nil {
		return env, err
	}

	// Before anything is created, because an image that cannot be pulled
	// fails as a pod stuck in ImagePullBackOff several minutes later, which
	// reads as a slow cluster rather than as a missing image.
	if err := r.ensureImages(ctx, spec, progress); err != nil {
		return env, err
	}

	// The namespace is what is journalled, and almost the only thing, because
	// it is the unit of teardown: Down deletes it and everything inside goes
	// with it. A record naming a NetworkPolicy or a Secret inside it would be
	// a record teardown has no separate use for and the inventory does not
	// report, which is a row nothing can ever act on. The Deployments are
	// journalled as well because the inventory does report them and they are
	// what somebody reads to see what an environment is running.
	if err := journal(kindNamespace, namespace); err != nil {
		return env, err
	}
	if err := r.ensureNamespace(ctx, spec.EnvID); err != nil {
		return env, err
	}
	if err := r.applyPolicies(ctx, spec.EnvID, namespace); err != nil {
		return env, err
	}

	if spec.CACertPEM != "" {
		if err := r.ensureCASecret(ctx, spec, namespace); err != nil {
			return env, err
		}
	}

	proxyIP, err := r.startProxy(ctx, spec, namespace, podCIDR, resolver, journal, progress)
	if err != nil {
		return env, err
	}
	env.ProxyReady = true

	// AFTER the sidecar and before any service image, and the order is the
	// whole value of the check.
	//
	// It ran before the sidecar until a run caught it out. The rule that lets
	// a service out at all is `af-service-egress-to-proxy`, whose peer list is
	// a pod selector for the sidecar, and with no sidecar in the namespace
	// that selector matches nothing. So the probe was governed by default-deny
	// alone: it proved that a pod with NO egress allowance cannot escape,
	// which is a question nobody was asking, while the rule a service actually
	// runs under was never exercised.
	//
	// That is not hypothetical. On k3s the conformance suite caught a service
	// reaching 1.1.1.1 on UDP 53 and getting the real public answer back,
	// minutes after this probe had passed on the same cluster. Moving the
	// probe here is what stopped it.
	//
	// What that proves and what it does not: the escape needed the rule to be
	// present, and it went away once the probe ran with the rule present and
	// looped until nothing got out. So the window is around the rule becoming
	// real for a namespace that has just acquired a sidecar, and this probe
	// now absorbs it before any service exists. Whether the CNI is briefly
	// permissive or permanently loose about the destination on that port is
	// NOT established here, and the difference does not change what to do:
	// the probe has to run under the rule set a service runs under, and it has
	// to keep asking until the answer stops changing.
	//
	// The sidecar having started first costs nothing that matters. It is one
	// pod of ours, it is torn down with the namespace, and the promise this
	// check exists to keep is that no SERVICE image runs on a cluster that
	// does not contain it.
	if !r.skipContainmentCheck {
		if err := r.verifyContainment(ctx, spec, namespace, resolver, progress); err != nil {
			return env, err
		}
	}

	for _, s := range order {
		running, err := r.startService(ctx, spec, s, namespace, proxyIP, journal, progress)
		env.Services = append(env.Services, running)
		if err != nil {
			// Returned with what came up so far, not with nothing. What
			// started is the evidence, and teardown finds it by namespace
			// whether or not this function reports it.
			return env, err
		}
	}
	return env, nil
}

// ensureNamespace creates the environment's namespace, or leaves an existing
// one alone so that a second af up is not a second environment.
func (r *Runtime) ensureNamespace(ctx context.Context, envID string) error {
	ns := r.namespaceObject(envID)
	_, err := r.cli.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := r.cli.CoreV1().Namespaces().Get(ctx, ns.Name, metav1.GetOptions{})
		// A namespace that is on its way out cannot be reused: every create
		// inside it is refused, and the refusal names a terminating namespace
		// rather than the environment somebody is trying to start.
		if getErr == nil && existing.Status.Phase == corev1.NamespaceTerminating {
			return aferrors.Coded(aferrors.AFRUN002, "endpoint", r.rest.Host,
				"detail", fmt.Sprintf("namespace %s is still terminating from a previous "+
					"teardown. Wait for it to finish, or run af down again", ns.Name))
		}
		// The other half of the ownership question Down asks, and the worse
		// half. Reusing somebody else's namespace does not merely add objects
		// to it: applyPolicies runs next and its first policy denies all
		// traffic in both directions, so whatever was already running in there
		// stops talking to anything. Refusing to start is the only outcome
		// that leaves their cluster as it was.
		if getErr == nil && existing.Labels[LabelManaged] != "true" {
			return aferrors.Coded(aferrors.AFRUN045, "kind", "namespace", "name", ns.Name)
		}
	}
	return nil
}

// applyPolicies writes every NetworkPolicy the environment needs.
func (r *Runtime) applyPolicies(ctx context.Context, envID, namespace string) error {
	for _, policy := range networkPolicies(envID, namespace, r.domain != "") {
		_, err := r.cli.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			continue
		}
		if err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
	}
	return nil
}

// ensureCASecret places the environment certificate where services mount it.
func (r *Runtime) ensureCASecret(ctx context.Context, spec provider.EnvSpec, namespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: caSecretName, Namespace: namespace,
			Labels: labelsFor(spec.EnvID, ComponentService),
		},
		Data: map[string][]byte{"ca.crt": []byte(spec.CACertPEM)},
	}
	_, err := r.cli.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	return nil
}

// startProxy places the sidecar and returns the address services resolve
// through.
func (r *Runtime) startProxy(
	ctx context.Context,
	spec provider.EnvSpec,
	namespace, podCIDR, resolver string,
	journal func(string, string) error,
	progress func(string),
) (string, error) {
	secret, deployment, service, err := r.proxyObjects(ctx, spec.EnvID, namespace, podCIDR, resolver, spec)
	if err != nil {
		return "", err
	}
	// Only the Deployment. The Secret and the Service live in the namespace
	// and go when it goes, and a journal entry naming something the inventory
	// does not report is a record nothing can act on.
	if err := journal(kindDeployment, namespace+"/"+deployment.Name); err != nil {
		return "", err
	}
	if err := r.createOrReplaceSecret(ctx, namespace, secret); err != nil {
		return "", err
	}
	if _, err := r.cli.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return "", aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	created, err := r.cli.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = r.cli.CoreV1().Services(namespace).Get(ctx, service.Name, metav1.GetOptions{})
	}
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	progress("egress sidecar placed")

	// The sidecar starts before any service, for two reasons. A service that
	// makes an outbound call the instant it starts would otherwise get a
	// connection refused that looks exactly like a blocked host and is not
	// one. And every service resolves through the sidecar, so its address has
	// to exist before any of them are created.
	if err := r.waitForPods(ctx, namespace, map[string]string{
		LabelComponent: ComponentProxy,
	}, r.readyWait, "the egress sidecar"); err != nil {
		return "", err
	}
	if created.Spec.ClusterIP == "" || created.Spec.ClusterIP == corev1.ClusterIPNone {
		return "", aferrors.Coded(aferrors.AFRUN002, "endpoint", r.rest.Host,
			"detail", "the sidecar's service has no cluster address, so no service could "+
				"be told where to resolve names")
	}
	return created.Spec.ClusterIP, nil
}

// createOrReplaceSecret writes a secret, replacing one left by an earlier run.
//
// Replaced rather than left alone, because the policy lives in it: an af up
// with a changed egress section would otherwise reuse the old rules and
// enforce a policy nobody had written any more.
func (r *Runtime) createOrReplaceSecret(ctx context.Context, namespace string, secret *corev1.Secret) error {
	_, err := r.cli.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = r.cli.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	return nil
}

// startService runs a service's migration, then the service.
func (r *Runtime) startService(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	namespace, resolverIP string,
	journal func(string, string) error,
	progress func(string),
) (provider.RunningService, error) {
	running := provider.RunningService{Name: s.Name, Kind: s.Kind}

	if s.Migrate != "" {
		if err := r.runMigration(ctx, spec, s, namespace, resolverIP, progress); err != nil {
			return running, err
		}
	}

	if err := journal(kindDeployment, namespace+"/"+s.Name); err != nil {
		return running, err
	}
	deployment := r.deploymentFor(spec, s, namespace, resolverIP)
	if _, err := r.cli.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return running, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("creating %s: %v", s.Name, err))
	}

	if _, err := r.cli.CoreV1().Services(namespace).Create(ctx,
		serviceObject(spec.EnvID, namespace, s), metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return running, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}

	if s.Kind == "web" && s.Port > 0 && r.domain != "" {
		if _, err := r.cli.NetworkingV1().Ingresses(namespace).Create(ctx,
			r.ingressFor(spec.EnvID, namespace, s), metav1.CreateOptions{}); err != nil &&
			!apierrors.IsAlreadyExists(err) {
			return running, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
		running.URL = "http://" + r.hostFor(spec.EnvID, s.Name)
	}

	timeout := s.HealthTimeout
	if timeout <= 0 {
		timeout = r.readyWait
	}
	if s.Port > 0 {
		if err := r.waitForPods(ctx, namespace, map[string]string{
			LabelService: s.Name,
		}, timeout, s.Name); err != nil {
			return running, err
		}
		running.Ready = true
		running.State = "running"
		return running, nil
	}
	// A service with no port is ready when it is running, and asking for more
	// would mean inventing a protocol the application does not speak. What is
	// still checked is that it has not already exited, because a worker that
	// died on startup must be reported rather than counted as up.
	running.State = "running"
	if err := r.confirmStarted(ctx, namespace, s); err != nil {
		return running, err
	}
	running.Ready = true
	return running, nil
}

// runMigration runs the migration Job to completion and fails if it did not
// succeed.
func (r *Runtime) runMigration(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	namespace, resolverIP string,
	progress func(string),
) error {
	job := r.migrationJob(spec, s, namespace, resolverIP)
	_, err := r.cli.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	progress(fmt.Sprintf("%s: running migration", s.Name))

	deadline := time.Now().Add(r.readyWait)
	for {
		got, err := r.cli.BatchV1().Jobs(namespace).Get(ctx, job.Name, metav1.GetOptions{})
		if err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
		if got.Status.Succeeded > 0 {
			return nil
		}
		if got.Status.Failed > 0 {
			code := r.migrationExitCode(ctx, namespace, job.Name)
			return aferrors.Coded(aferrors.AFRUN005,
				"service", s.Name, "code", strconv.Itoa(code))
		}
		if time.Now().After(deadline) {
			return aferrors.Coded(aferrors.AFRUN004,
				"service", s.Name, "timeout", r.readyWait.Round(time.Second).String(),
				"health", "the migration never finished")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// migrationExitCode reads what the migration container exited with.
func (r *Runtime) migrationExitCode(ctx context.Context, namespace, job string) int {
	pods, err := r.cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + job,
	})
	if err != nil {
		return 1
	}
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil {
				return int(cs.State.Terminated.ExitCode)
			}
		}
	}
	return 1
}

// confirmStarted reports a service that has already exited rather than
// counting it as running.
func (r *Runtime) confirmStarted(ctx context.Context, namespace string, s provider.ServiceSpec) error {
	// One look after a moment, not a wait. A worker that is going to fail
	// immediately has done so by now, and a worker that is going to run for a
	// week looks the same either way.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	pods, err := r.cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelService + "=" + s.Name,
	})
	if err != nil {
		return nil
	}
	for _, pod := range pods.Items {
		if code, ok := exitCodeOf(pod); ok && code != 0 {
			return aferrors.Coded(aferrors.AFRUN005,
				"service", s.Name, "code", strconv.Itoa(code))
		}
	}
	return nil
}

// waitForPods blocks until every pod matching the selector is ready.
func (r *Runtime) waitForPods(
	ctx context.Context, namespace string, selector map[string]string,
	timeout time.Duration, what string,
) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		pods, err := r.cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: selectorString(selector),
		})
		if err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
		ready, total := 0, len(pods.Items)
		for _, pod := range pods.Items {
			if code, ok := exitCodeOf(pod); ok && code != 0 {
				return aferrors.Coded(aferrors.AFRUN005, "service", what, "code", strconv.Itoa(code))
			}
			if podReady(pod) {
				ready++
			} else {
				last = podTrouble(pod)
			}
		}
		if total > 0 && ready == total {
			return nil
		}
		if time.Now().After(deadline) {
			detail := last
			if detail == "" {
				detail = "no pod was scheduled"
			}
			return aferrors.Coded(aferrors.AFRUN004,
				"service", what, "timeout", timeout.Round(time.Second).String(),
				"health", detail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func selectorString(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for k := range selector {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+selector[k])
	}
	return strings.Join(parts, ",")
}

func podReady(pod corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return len(pod.Status.ContainerStatuses) > 0
}

// podTrouble says, in one line, why a pod is not ready.
//
// The reason matters more here than anywhere else in this package: the two
// most common failures on a cluster are an image that cannot be pulled and a
// pod that cannot be scheduled, and both look identical from outside as a wait
// that never finishes.
func podTrouble(pod corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			return fmt.Sprintf("not scheduled: %s", cond.Message)
		}
	}
	return string(pod.Status.Phase)
}

// exitCodeOf reports the code a pod's application container exited with.
//
// Terminated first, then the last termination, because a Deployment's pods
// must declare restartPolicy Always (Kubernetes rejects anything else), so a
// service that exits is restarted and its exit code moves from the current
// state to the previous one within a second or two. Reading only the current
// state loses the code for exactly the services whose code somebody wanted.
func exitCodeOf(pod corev1.Pod) (int, bool) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != "app" && cs.Name != "migrate" {
			continue
		}
		if cs.State.Terminated != nil {
			return int(cs.State.Terminated.ExitCode), true
		}
		if cs.LastTerminationState.Terminated != nil {
			return int(cs.LastTerminationState.Terminated.ExitCode), true
		}
	}
	return 0, false
}

// startOrder sorts services so a dependency starts before what depends on it,
// and reports a cycle rather than deadlocking on one.
//
// The same algorithm the local runtime uses, deliberately duplicated rather
// than shared: it is twenty lines, and the alternative is a package that
// exists so that two runtimes can agree about something they are each free to
// decide. If a third runtime appears, that is the moment to lift it.
func startOrder(services []provider.ServiceSpec) ([]provider.ServiceSpec, error) {
	byName := make(map[string]provider.ServiceSpec, len(services))
	for _, s := range services {
		byName[s.Name] = s
	}
	var out []provider.ServiceSpec
	state := make(map[string]int, len(services))

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case 2:
			return nil
		case 1:
			return aferrors.Coded(aferrors.AFRUN041,
				"cycle", strings.Join(append(path, name), " -> "))
		}
		s, ok := byName[name]
		if !ok {
			return aferrors.Coded(aferrors.AFRUN042,
				"service", strings.Join(path, " -> "), "missing", name)
		}
		state[name] = 1
		deps := append([]string(nil), s.DependsOn...)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		out = append(out, s)
		return nil
	}
	for _, s := range services {
		if err := visit(s.Name, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Down removes everything belonging to an environment.
//
// Deleting the namespace is the whole teardown, which is the one genuine
// advantage this runtime has over the local one: there is no list of resource
// kinds to keep in step with what Up creates, so a resource added later cannot
// be forgotten here. What it costs is the wait: a namespace is not gone when
// the API returns, and reporting success while it is still terminating would
// make the next af up fail with a message about a terminating namespace.
func (r *Runtime) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	var td provider.Teardown
	if envID == "" {
		return td, aferrors.Coded(aferrors.AFRUN040, "detail", "the environment has no id")
	}
	namespace := r.namespace(envID)

	existing, err := r.cli.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// af down runs on a schedule, after a failed af up, and from a pull
		// request that closed before the environment finished starting. All
		// three can reach an environment that is not there.
		return td, nil
	}
	if err != nil {
		return td, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}

	// The name is not the proof of ownership, the label is.
	//
	// Everything up to here derived the namespace from an environment id, and
	// deleting a namespace takes everything inside it with no undo. A
	// namespace this runtime created carries the managed label because Create
	// sets it in the same call that makes the object, so there is no window in
	// which one of ours exists without it. A namespace with the right name and
	// no label is therefore definitively somebody else's, and the only correct
	// thing to do with it is nothing.
	//
	// The local runtime has never had this hole: it removes by label filter,
	// so it can only touch what it labelled. This runtime deleted by computed
	// name, and the conformance suite cannot see the difference, because its
	// leak check is scoped to what the runtime's own Inventory reports and
	// Inventory reports only what is managed. An assertion scoped to exclude
	// the casualty cannot fail on it.
	if existing.Labels[LabelManaged] != "true" {
		return td, aferrors.Coded(aferrors.AFRUN045, "kind", "namespace", "name", namespace)
	}

	// Counted before the delete, because afterwards there is nothing to count
	// and a teardown that always reported zero would be indistinguishable
	// from one that removed nothing.
	td.Removed = r.countResources(ctx, namespace) + 1

	if existing.Status.Phase != corev1.NamespaceTerminating {
		if err := r.cli.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil &&
			!apierrors.IsNotFound(err) {
			return td, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
	}

	deadline := time.Now().Add(r.readyWait)
	for {
		got, err := r.cli.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return td, nil
		}
		if err != nil {
			return td, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
		if time.Now().After(deadline) {
			// A namespace that will not finish terminating is almost always a
			// finalizer waiting on something, so the finalizers are the
			// reason and belong in the message rather than in a support
			// conversation.
			reason := "still terminating"
			if len(got.Spec.Finalizers) > 0 {
				reason = fmt.Sprintf("still terminating, waiting on finalizers %v", got.Spec.Finalizers)
			}
			td.Removed = 0
			td.Pending = append(td.Pending, provider.PendingResource{
				Kind: "namespace", ID: namespace, Reason: reason,
			})
			return td, nil
		}
		select {
		case <-ctx.Done():
			return td, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// countResources counts what an environment holds, for the teardown report.
func (r *Runtime) countResources(ctx context.Context, namespace string) int {
	n := 0
	if list, err := r.cli.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		n += len(list.Items)
	}
	if list, err := r.cli.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		n += len(list.Items)
	}
	if list, err := r.cli.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		n += len(list.Items)
	}
	if list, err := r.cli.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		n += len(list.Items)
	}
	// Migration Jobs count too. A service that declared migrate leaves one
	// behind, and a teardown that did not count it would report fewer
	// resources removed than the environment held, which is the number
	// somebody compares against the leak detector.
	if list, err := r.cli.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		n += len(list.Items)
	}
	return n
}

// Status reports what is running for an environment.
func (r *Runtime) Status(ctx context.Context, envID string) (provider.Env, error) {
	namespace := r.namespace(envID)
	env := provider.Env{EnvID: envID, NetworkID: namespace}

	pods, err := r.cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelManaged + "=true," + LabelComponent + "=" + ComponentService,
	})
	if apierrors.IsNotFound(err) {
		// An environment that was never created is empty, not an error. af
		// status runs against whatever the branch is, including branches that
		// never had one.
		return env, nil
	}
	if err != nil {
		return env, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}

	proxy, err := r.cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelComponent + "=" + ComponentProxy,
	})
	if err == nil {
		for _, pod := range proxy.Items {
			if podReady(pod) {
				env.ProxyReady = true
			}
		}
	}

	// One entry per service rather than per pod. A Deployment with two
	// replicas is one service in the manifest and reporting it twice would
	// make af status disagree with the file somebody wrote.
	byService := map[string]provider.RunningService{}
	for _, pod := range pods.Items {
		name := pod.Labels[LabelService]
		if name == "" || strings.HasSuffix(name, "-migrate") {
			continue
		}
		rs, seen := byService[name]
		if !seen {
			rs = provider.RunningService{
				Name: name, Kind: pod.Labels[LabelServiceKind], ContainerID: string(pod.UID),
			}
		}
		rs.State = string(pod.Status.Phase)
		rs.Detail = podTrouble(pod)
		if podReady(pod) {
			rs.Ready = true
			rs.State = "running"
		}
		if code, ok := exitCodeOf(pod); ok {
			c := code
			rs.ExitCode = &c
		}
		if rs.Kind == "web" && r.domain != "" {
			rs.URL = "http://" + r.hostFor(envID, name)
		}
		byService[name] = rs
	}
	for _, rs := range byService {
		env.Services = append(env.Services, rs)
	}
	sort.Slice(env.Services, func(i, j int) bool { return env.Services[i].Name < env.Services[j].Name })
	return env, nil
}

// Inventory lists everything this runtime holds.
func (r *Runtime) Inventory(ctx context.Context) ([]provider.Resource, error) {
	namespaces, err := r.cli.CoreV1().Namespaces().List(ctx, managedSelector())
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}
	var out []provider.Resource
	for _, ns := range namespaces.Items {
		envID := ns.Labels[LabelEnv]
		out = append(out, provider.Resource{
			Kind: "namespace", ID: ns.Name, EnvID: envID,
			CreatedAt: ns.CreationTimestamp.UTC(),
			Labels: map[string]string{
				"name": ns.Name, "phase": string(ns.Status.Phase),
				// Carried through so the reaper reads one shape of resource
				// rather than talking to each runtime's own client.
				"expires": ns.Labels[dockerutil.LabelExpires],
			},
		})
		deployments, err := r.cli.AppsV1().Deployments(ns.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, d := range deployments.Items {
			out = append(out, provider.Resource{
				Kind: "deployment/" + componentOf(d), ID: ns.Name + "/" + d.Name, EnvID: envID,
				CreatedAt: d.CreationTimestamp.UTC(),
				Labels: map[string]string{
					"name": d.Name, "service": d.Labels[LabelService],
					"ready": strconv.Itoa(int(d.Status.ReadyReplicas)),
				},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func componentOf(d appsv1.Deployment) string {
	if c := d.Labels[LabelComponent]; c != "" {
		return c
	}
	return "unknown"
}

// Logs returns recent output from an environment's services.
//
// Redacted on the way out rather than at the call site, the same as the local
// runtime: a service's own log is the second likeliest place for a secret to
// surface after a build log, and redacting at the writer means a call site
// somebody forgot cannot leak.
func (r *Runtime) Logs(ctx context.Context, envID, service string, tail int) ([]provider.LogLine, error) {
	if tail <= 0 {
		tail = 200
	}
	namespace := r.namespace(envID)
	selector := LabelManaged + "=true," + LabelComponent + "=" + ComponentService
	if service != "" {
		selector += "," + LabelService + "=" + service
	}
	pods, err := r.cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}

	lines := int64(tail)
	var out []provider.LogLine
	for _, pod := range pods.Items {
		name := pod.Labels[LabelService]
		body, err := r.podLogs(ctx, namespace, pod.Name, lines)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			out = append(out, provider.LogLine{
				Service: name, Stream: "stdout", Text: r.redactor.String(line),
			})
		}
	}
	return out, nil
}

func (r *Runtime) podLogs(ctx context.Context, namespace, pod string, tail int64) (string, error) {
	req := r.cli.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		TailLines: &tail,
	})
	rc, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, 8<<20))
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(body), nil
}

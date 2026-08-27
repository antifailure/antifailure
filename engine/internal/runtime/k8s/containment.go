package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// probeName is the pod that tries to escape.
const probeName = "af-containment-probe"

// containmentMarker is what the probe prints, so that a pod which failed for
// some other reason is never read as a pass.
//
// Absence of the marker is not silence, it is an unanswered question, and this
// is the one check in the product where an unanswered question has to stop the
// environment. A probe that could not run tells you nothing about whether the
// cluster contains anything.
const containmentMarker = "AF-CONTAINMENT"

// escapeScript tries every way out of the environment that does not go
// through the sidecar, and reports whether any of them still works.
//
// It LOOPS until nothing gets out, rather than asking once, and that is not
// patience for its own sake. A NetworkPolicy is an object the API server
// accepts immediately and the CNI programs some time afterwards, so there is a
// window between creating the policy and the packet filter existing in which a
// pod really is uncontained. Asking once inside that window reports an escape
// on a cluster that contains everything a second later, which is a false
// refusal; worse, it is the same window a SERVICE would start in. Waiting here
// until an entire pass gets nowhere is what closes it for the services too,
// because nothing else is created until this returns.
//
// What it does not do is soften: a pass that gets out is an escape, and the
// deadline expiring with anything still reachable fails the environment.
//
// The four attempts are the ones the specification names, and each is a real
// route somebody has used. A direct connection to a public address skips DNS
// entirely, which is the obvious answer to interception by DNS. A UDP query
// straight to a public resolver turns a name lookup into an unlogged channel
// out, because the payload of a DNS query is whatever the client puts in it.
// The link local metadata address is not on the internet at all and hands out
// the node's own cloud credentials to anything on the node that asks for them.
// And the cluster's API server is the one address where reaching it does not
// leak data but rewrites the policy that was containing you.
//
// Every attempt is given a short timeout, because the expected outcome is that
// each hangs until it is cut off, and four hangs at the default timeout would
// add minutes to every af up.
const escapeScript = `
deadline=$(( $(date +%s) + 90 ))
while :; do
  escaped=""
  wget -T 3 -q -O /dev/null http://1.1.1.1/ >/dev/null 2>&1 && escaped="$escaped tcp-to-a-public-address"
  nslookup example.com 1.1.1.1 >/dev/null 2>&1 && escaped="$escaped udp-to-a-public-resolver"
  wget -T 3 -q -O /dev/null http://169.254.169.254/ >/dev/null 2>&1 && escaped="$escaped the-instance-metadata-endpoint"
  wget -T 3 -q -O /dev/null --no-check-certificate https://kubernetes.default.svc/version >/dev/null 2>&1 && escaped="$escaped the-cluster-api-server"
  if [ -z "$escaped" ]; then
    echo "` + containmentMarker + ` contained"
    exit 0
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "` + containmentMarker + ` escaped:$escaped"
    exit 1
  fi
  sleep 2
done
`

// verifyContainment refuses to bring an environment up on a cluster that does
// not enforce the policy it was just given.
//
// This is the check that makes the difference between a product and a
// suggestion. A NetworkPolicy is a request to whatever CNI the cluster runs,
// and several accept the object and enforce nothing: kind's default CNI is one,
// and it is the CNI most people's first cluster has. On such a cluster every
// object this package creates is applied successfully, every status reads
// green, and every environment can reach the internet, the metadata endpoint
// and each other. Nothing downstream would ever discover it, because there is
// no error anywhere: the policy exists, it is just decorative.
//
// So the environment does not start until a pod under the real rules has tried
// to get out and failed.
func (r *Runtime) verifyContainment(
	ctx context.Context,
	spec provider.EnvSpec,
	namespace, resolver string,
	journal func(string, string) error,
	progress func(string),
) error {
	image, err := r.probeImage(ctx, spec)
	if err != nil {
		return err
	}
	if image == "" {
		return aferrors.Coded(aferrors.AFRUN043, "detail",
			"there is no image to run the containment check with, so whether this "+
				"cluster enforces network policy is unknown")
	}
	if err := journal("pod", namespace+"/"+probeName); err != nil {
		return err
	}
	progress("checking that this cluster enforces network policy")

	labels := probeLabels(spec.EnvID)
	dnsPolicy, dnsConfig := podDNS(namespace, resolver)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: probeName, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: falseRef(),
			RestartPolicy:                corev1.RestartPolicyNever,
			DNSPolicy:                    dnsPolicy,
			DNSConfig:                    dnsConfig,
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   image,
				Command: []string{"/bin/sh", "-c", escapeScript},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: falseRef(),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
	// Removed whatever happens, including when this function fails: a probe
	// left behind would be reported by the leak detector as a resource nobody
	// owns, and would be counted by the next teardown.
	defer func() {
		remove, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		_ = r.cli.CoreV1().Pods(namespace).Delete(remove, probeName, metav1.DeleteOptions{})
	}()

	_, err = r.cli.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_ = r.cli.CoreV1().Pods(namespace).Delete(ctx, probeName, metav1.DeleteOptions{})
		_, err = r.cli.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	}
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
	}

	verdict, err := r.awaitProbe(ctx, namespace)
	if err != nil {
		return err
	}
	if routes := strings.TrimSpace(strings.TrimPrefix(verdict, "escaped:")); strings.HasPrefix(verdict, "escaped:") {
		return aferrors.Coded(aferrors.AFRUN043,
			"detail", fmt.Sprintf(
				"a pod in this environment reached %s. The environment's NetworkPolicy "+
					"objects were created and this cluster is not enforcing them, which "+
					"almost always means its CNI does not implement NetworkPolicy at all "+
					"(kind's default kindnet is the usual one). Nothing placed here would "+
					"be contained, so no environment is started",
				strings.ReplaceAll(routes, " ", ", ")))
	}
	progress("network policy is enforced: the probe could not reach anything")
	return nil
}

// probeImage is the image the escape probe runs in.
//
// One of the environment's own images by default, and that is deliberate
// rather than lazy. It needs no second image to be distributed, it works in an
// air gapped install where nothing can be pulled, and it asks the question in
// the image that is actually going to run here rather than in a convenient
// one.
func (r *Runtime) probeImage(ctx context.Context, spec provider.EnvSpec) (string, error) {
	for _, s := range spec.Services {
		if s.Image != "" {
			return s.Image, nil
		}
	}
	return r.proxyImage(ctx)
}

// awaitProbe waits for the probe to finish and returns what it said.
func (r *Runtime) awaitProbe(ctx context.Context, namespace string) (string, error) {
	deadline := time.Now().Add(r.readyWait)
	for {
		pod, err := r.cli.CoreV1().Pods(namespace).Get(ctx, probeName, metav1.GetOptions{})
		if err != nil {
			return "", aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", r.rest.Host)
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			body, logErr := r.podLogs(ctx, namespace, probeName, 50)
			if logErr != nil {
				return "", aferrors.Coded(aferrors.AFRUN043, "detail",
					fmt.Sprintf("the containment check finished and its output could not "+
						"be read (%v), so whether this cluster contains anything is unknown", logErr))
			}
			for _, line := range strings.Split(body, "\n") {
				if verdict, ok := strings.CutPrefix(strings.TrimSpace(line), containmentMarker+" "); ok {
					return verdict, nil
				}
			}
			// The pod ran and never answered. That is not a pass. The usual
			// cause is an image with no shell, and the fix is to say so
			// rather than to assume the best about a security control.
			return "", aferrors.Coded(aferrors.AFRUN043, "detail",
				fmt.Sprintf("the containment check did not report a verdict. Its output was %q. "+
					"The check runs /bin/sh in one of this environment's images, so an image "+
					"with no shell cannot answer it", strings.TrimSpace(body)))
		}
		if trouble := podTrouble(*pod); strings.Contains(trouble, "ImagePull") || strings.Contains(trouble, "ErrImage") {
			return "", aferrors.Coded(aferrors.AFRUN043, "detail",
				fmt.Sprintf("the containment check could not start: %s. %s", trouble, r.images.Describe()))
		}
		if time.Now().After(deadline) {
			return "", aferrors.Coded(aferrors.AFRUN043, "detail",
				fmt.Sprintf("the containment check did not finish within %s (%s), so whether "+
					"this cluster enforces network policy is unknown",
					r.readyWait.Round(time.Second), podTrouble(*pod)))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

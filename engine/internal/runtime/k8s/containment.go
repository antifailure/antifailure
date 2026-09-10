package k8s

import (
	"context"
	"fmt"
	"net"
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
	progress func(string),
) error {
	image, err := r.proxyImage(ctx)
	if err != nil {
		return err
	}
	if image == "" {
		return aferrors.Coded(aferrors.AFRUN043, "detail",
			"there is no image to run the containment check with, so whether this "+
				"cluster enforces network policy is unknown")
	}
	progress("checking that this cluster enforces network policy")

	proxyService, err := r.cli.CoreV1().Services(namespace).Get(ctx, ProxyName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if net.ParseIP(proxyService.Spec.ClusterIP) == nil {
		return aferrors.Coded(aferrors.AFRUN043, "detail", "the sidecar has no network address for the containment control")
	}
	labels := probeLabels(spec.EnvID)
	dnsPolicy, dnsConfig := podDNS(namespace, resolver)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: probeName, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: falseRef(),
			RestartPolicy:                corev1.RestartPolicyNever,
			DNSPolicy:                    dnsPolicy,
			DNSConfig:                    dnsConfig,
			Containers:                   []corev1.Container{networkGateContainer(image, proxyService.Spec.ClusterIP)},
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

	if _, err := r.awaitProbe(ctx, namespace); err != nil {
		return err
	}
	progress("three consecutive direct-egress probe rounds were denied")
	return nil
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
				if strings.TrimSpace(line) == containmentMarker+" contained" && pod.Status.Phase == corev1.PodSucceeded {
					return "contained", nil
				}
			}
			// The pod ran and never answered. That is not a pass. A failed process or
			// an unrecognised message is never a containment verdict.
			return "", aferrors.Coded(aferrors.AFRUN043, "detail",
				fmt.Sprintf("the trusted containment check did not complete successfully. Its output was %q", strings.TrimSpace(body)))
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

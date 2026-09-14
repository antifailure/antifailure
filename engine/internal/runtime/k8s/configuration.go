package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const configurationLabel = "dev.antifailure.configuration"

func configurationFingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part)) + ":"))
		_, _ = hash.Write([]byte(part))
	}
	// Kubernetes label values cannot hold a full 64-character hex digest.
	return hex.EncodeToString(hash.Sum(nil))[:40]
}

func (r *Runtime) serviceFingerprint(spec provider.EnvSpec, service provider.ServiceSpec, resolverIP string) string {
	parts := []string{
		service.Image, service.Command, service.Kind, service.Migrate,
		strconv.Itoa(service.Port), strconv.Itoa(service.Instances()),
		strconv.FormatInt(service.CPUMillis, 10), strconv.FormatInt(service.MemoryBytes, 10),
		resolverIP, r.proxyRef, spec.CACertPEM, spec.DatabaseCACertPEM, spec.MigrationDatabaseURL.Reveal(),
	}
	environment := serviceEnv(spec, service, false)
	parts = append(parts, strconv.Itoa(len(environment)))
	for _, variable := range environment {
		parts = append(parts, variable.Name, variable.Value)
	}
	for _, route := range spec.DatabaseRoutes {
		parts = append(parts, strconv.Itoa(route.Port), route.Upstream)
	}
	return configurationFingerprint(parts...)
}

func ownedConfiguration(labels map[string]string, envID string) bool {
	return labels[LabelManaged] == "true" && labels[LabelEnv] == envID
}

func (r *Runtime) createOrReplaceDeployment(ctx context.Context, namespace string, desired *appsv1.Deployment) (*appsv1.Deployment, error) {
	created, err := r.cli.AppsV1().Deployments(namespace).Create(ctx, desired, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return created, err
	}
	existing, err := r.cli.AppsV1().Deployments(namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !ownedConfiguration(existing.Labels, desired.Labels[LabelEnv]) {
		return nil, fmt.Errorf("the existing deployment does not belong to this environment")
	}
	if existing.Spec.Template.Labels[configurationLabel] == desired.Spec.Template.Labels[configurationLabel] {
		return existing, nil
	}
	desired.ResourceVersion = existing.ResourceVersion
	return r.cli.AppsV1().Deployments(namespace).Update(ctx, desired, metav1.UpdateOptions{})
}

func (r *Runtime) createOrReplaceService(ctx context.Context, namespace string, desired *corev1.Service) (*corev1.Service, error) {
	created, err := r.cli.CoreV1().Services(namespace).Create(ctx, desired, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return created, err
	}
	existing, err := r.cli.CoreV1().Services(namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !ownedConfiguration(existing.Labels, desired.Labels[LabelEnv]) {
		return nil, fmt.Errorf("the existing service does not belong to this environment")
	}
	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
	desired.Spec.IPFamilies = existing.Spec.IPFamilies
	desired.Spec.IPFamilyPolicy = existing.Spec.IPFamilyPolicy
	return r.cli.CoreV1().Services(namespace).Update(ctx, desired, metav1.UpdateOptions{})
}

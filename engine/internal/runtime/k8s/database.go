package k8s

import (
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// databaseNetworkPolicies add only fixed database routes to the existing deny
// rules. Application pods reach listener ports on the sidecar, never database
// addresses directly. Only the sidecar receives exact destination allowances.
func databaseNetworkPolicies(envID, namespace string, routes []provider.DatabaseRoute) ([]*networkingv1.NetworkPolicy, error) {
	if err := provider.ValidateDatabaseRoutes(routes); err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, nil
	}
	proxySelector := metav1.LabelSelector{MatchLabels: map[string]string{
		LabelEnv: envID, LabelComponent: ComponentProxy,
	}}
	containedSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{LabelEnv: envID},
		MatchExpressions: []metav1.LabelSelectorRequirement{{
			Key: LabelComponent, Operator: metav1.LabelSelectorOpIn,
			Values: []string{ComponentService, ComponentProbe},
		}},
	}
	meta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labelsFor(envID, "policy")}
	}
	tcp := corev1.ProtocolTCP
	var listeners []networkingv1.NetworkPolicyPort
	var destinations []networkingv1.NetworkPolicyEgressRule
	for _, route := range routes {
		host, port, _ := net.SplitHostPort(route.Upstream)
		ip := net.ParseIP(host)
		if ip == nil {
			return nil, fmt.Errorf("database network policy requires a pinned literal address")
		}
		bits := 128
		if ip.To4() != nil {
			bits = 32
		}
		n, _ := strconv.Atoi(port)
		upstreamPort := intstr.FromInt(n)
		listenerPort := intstr.FromInt(route.Port)
		listeners = append(listeners, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &listenerPort})
		destinations = append(destinations, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{
				CIDR: ip.String() + "/" + strconv.Itoa(bits),
			}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &upstreamPort}},
		})
	}
	return []*networkingv1.NetworkPolicy{
		{
			ObjectMeta: meta("af-database-via-proxy"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: containedSelector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To:    []networkingv1.NetworkPolicyPeer{{PodSelector: &proxySelector}},
					Ports: listeners,
				}},
			},
		},
		{
			ObjectMeta: meta("af-proxy-database-destinations"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: proxySelector,
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress:      destinations,
			},
		},
	}, nil
}

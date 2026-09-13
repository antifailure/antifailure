// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package policyenforce_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/policyenforce"
)

// The policy engine matches a rule's host with its port taken off, so a rule
// for api.stripe.com:443 reaches api.stripe.com. The deny list compared the host
// as written, so the same rule was not on a list naming api.stripe.com, and a
// denied host became reachable by adding its port. Each subtest names the host
// a different way, so each normalisation is held by a test of its own.
func TestADeniedHostIsNotReachedByNamingItsPort(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"api.stripe.com:443", "API.Stripe.com:8443", "api.stripe.com."} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			req := request()
			req.EgressHosts = []string{host}
			req.EgressModes = map[string]string{host: "sandbox"}
			hook := policyenforce.NewHook(policyenforce.Policy{DeniedHosts: []string{"api.stripe.com"}}, nil)

			err := hook.Check(licensed(t), req)
			var refusal *policyenforce.Refusal
			require.ErrorAs(t, err, &refusal, "%q reaches api.stripe.com, and the deny list names it", host)
			require.Equal(t, "egress deny list", refusal.Policy)
		})
	}
}

func TestAWildcardDenyEntryCoversAHostNamedWithItsPort(t *testing.T) {
	t.Parallel()
	req := request()
	req.EgressHosts = []string{"api.stripe.com:443"}
	req.EgressModes = map[string]string{"api.stripe.com:443": "allow"}
	hook := policyenforce.NewHook(policyenforce.Policy{DeniedHosts: []string{"*.stripe.com"}}, nil)

	err := hook.Check(licensed(t), req)
	var refusal *policyenforce.Refusal
	require.ErrorAs(t, err, &refusal, "api.stripe.com:443 is under *.stripe.com, and the deny list names it")
	require.Equal(t, "egress deny list", refusal.Policy)
}

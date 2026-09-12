// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// The firewall range arithmetic, which is the one piece of computation in this
// package that is both easy to get subtly wrong and cheap to test exhaustively.
//
// Subtly wrong is the operative word. Every case below produces a rule Azure
// accepts without complaint; the wrong ones simply admit a different set of
// addresses than the person writing the CIDR intended. There is no error to
// notice, which is why the arithmetic is tested rather than reviewed.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCIDRRange(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		in         string
		start, end string
	}{
		{"a bare address is itself", "203.0.113.7", "203.0.113.7", "203.0.113.7"},
		{"a /32 is one address", "203.0.113.7/32", "203.0.113.7", "203.0.113.7"},
		{"a /24", "203.0.113.0/24", "203.0.113.0", "203.0.113.255"},
		{"a /16", "198.51.0.0/16", "198.51.0.0", "198.51.255.255"},
		{"a /31 is two addresses", "203.0.113.0/31", "203.0.113.0", "203.0.113.1"},
		{"a /0 is everything", "0.0.0.0/0", "0.0.0.0", "255.255.255.255"},
		// The case that motivates masking. 10.0.0.5/24 NAMES the 10.0.0.0/24
		// network. A rule starting at .5 would silently admit a different
		// range than the one written, and Azure would accept it.
		{"an unmasked host bit is masked", "10.0.0.5/24", "10.0.0.0", "10.0.0.255"},
		{"an unmasked host bit in a /16", "172.16.34.7/16", "172.16.0.0", "172.16.255.255"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			start, end, err := cidrRange(c.in)
			require.NoError(t, err)
			require.Equal(t, c.start, start, "start address")
			require.Equal(t, c.end, end, "end address")
		})
	}
}

// TestCIDRRangeRefusals covers every way of not being able to answer, each of
// which must refuse rather than pass.
//
// An empty value is the one worth reading twice. Defaulting it to 0.0.0.0/0
// would make every branch work immediately and would open a copy of production
// to the whole internet, so the absence is a refusal rather than a default.
func TestCIDRRangeRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, in, contains string }{
		{"empty refuses rather than defaulting to the internet", "", "not defaulted"},
		{"whitespace is empty", "   ", "not defaulted"},
		{"nonsense", "not-an-address", "not an address or a CIDR"},
		{"a bad prefix", "203.0.113.0/33", "not a CIDR"},
		{"IPv6 cannot be expressed as a rule", "2001:db8::/32", "IPv6"},
		{"a bare IPv6 address", "2001:db8::1", "IPv6"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := cidrRange(c.in)
			require.Error(t, err)
			require.Contains(t, err.Error(), c.contains,
				"the refusal does not say what is wrong, so the person who set the "+
					"variable has to guess")
		})
	}
}

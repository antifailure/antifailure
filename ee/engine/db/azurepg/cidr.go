// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// The firewall rule's address range.
//
// Azure firewall rules are expressed as a START and an END address rather than
// as a prefix, so a CIDR a person writes has to be expanded into the pair. This
// is the whole of that conversion and it is separate because it is the one
// piece of arithmetic in this package that is easy to get subtly wrong and easy
// to test exhaustively.

import (
	"fmt"
	"net/netip"
	"strings"
)

// cidrRange converts a CIDR into the first and last address Azure wants.
//
// IPv4 only, and that is a refusal rather than a gap: an Azure PostgreSQL
// firewall rule takes IPv4 start and end addresses, so an IPv6 prefix here
// cannot be expressed and would have to be silently dropped or silently
// truncated. Refusing names the problem at the point somebody sets the
// variable.
//
// A bare address with no prefix is accepted and means exactly that address,
// which is what somebody pasting their own egress IP expects.
func cidrRange(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf(
			"azurepg: %s is empty, so no firewall rule can be created. Azure does not "+
				"copy firewall rules across a restore, so a branch created without one "+
				"is a server that provisioned successfully and that nothing can connect "+
				"to, and the failure arrives as a connection timeout that names no "+
				"firewall. Set it to the address range the engine reaches Azure from. "+
				"It is not defaulted to 0.0.0.0/0 here, because a default that opens a "+
				"copy of production to the whole internet is not a convenience",
			FirewallVariable)
	}

	if !strings.Contains(raw, "/") {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return "", "", fmt.Errorf("azurepg: %s is %q, which is not an address or a CIDR: %w",
				FirewallVariable, raw, err)
		}
		if !addr.Is4() {
			return "", "", errNotIPv4(raw)
		}
		return addr.String(), addr.String(), nil
	}

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return "", "", fmt.Errorf("azurepg: %s is %q, which is not a CIDR: %w",
			FirewallVariable, raw, err)
	}
	if !prefix.Addr().Is4() {
		return "", "", errNotIPv4(raw)
	}
	// Masked first: 10.0.0.5/24 names the 10.0.0.0/24 network, and using the
	// address as written would produce a start that is not the range's first
	// address. Azure accepts the pair without complaint, so the rule would
	// silently admit a different range than the one written.
	prefix = prefix.Masked()
	start := prefix.Addr().As4()
	end := start
	bits := prefix.Bits()
	for i := bits; i < 32; i++ {
		end[i/8] |= 1 << (7 - uint(i)%8)
	}
	return netip.AddrFrom4(start).String(), netip.AddrFrom4(end).String(), nil
}

func errNotIPv4(raw string) error {
	return fmt.Errorf(
		"azurepg: %s is %q, which is IPv6. An Azure Database for PostgreSQL firewall "+
			"rule takes IPv4 start and end addresses, so this range cannot be expressed "+
			"as a rule at all. Refused rather than dropped, because a dropped rule is a "+
			"branch nothing can reach and a truncated one admits addresses nobody chose",
		FirewallVariable, raw)
}

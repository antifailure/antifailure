package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The sidecar is the only thing in an environment with a route out, and that
// makes it a confused deputy. An address a service cannot reach for itself it
// can ask the sidecar to reach on its behalf, and the addresses where that
// matters are not on the internet at all.
//
// 169.254.169.254 is the instance metadata endpoint. It is not routed, it
// needs no credential, and it hands out the node's own cloud credentials to
// anything on the node that asks. The Docker gateway is the host the daemon
// runs on. Loopback is the sidecar itself. None of those is what somebody
// writing default: allow in a manifest meant: that sentence is a statement
// about the internet, not about the machine the environment happens to be
// running on.
//
// So a loopback, link local, private or multicast address is refused unless a
// rule names it literally, which is the only way to ask for one on purpose.
// The check is on the address the name resolved to and not on the name,
// because a name somebody else controls can be pointed at 169.254.169.254 as
// easily as at anything else, and a check on the name would be answered by
// registering metadata.example.com.
//
// The environment's own network is the exception. It is private by
// definition, a service reaching another service is not egress, and the
// packet cannot leave the environment. Refusing it would break a manifest
// that names a peer through the proxy rather than around it.

// destinations decides which addresses the sidecar will open on the
// environment's behalf.
type destinations struct {
	// allowIPv6 is the manifest's egress.allow_ipv6. Off by default, because
	// an address family the policy was never evaluated against is a policy
	// that is advisory.
	allowIPv6 bool
	// named are the addresses some rule spells out, which is consent.
	named map[string]bool
	// local is the environment's own network, which is not egress.
	local *net.IPNet
}

// newDestinations builds the guard from the policy the sidecar was given.
func newDestinations(rules []schema.EgressRule, subnet string, allowIPv6 bool) *destinations {
	d := &destinations{allowIPv6: allowIPv6, named: map[string]bool{}}
	for _, r := range rules {
		host := strings.ToLower(strings.TrimSpace(r.Host))
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
			d.named[ip.String()] = true
		}
	}
	if subnet != "" {
		if _, n, err := net.ParseCIDR(subnet); err == nil {
			d.local = n
		}
	}
	return d
}

// permit reports why an address may not be opened, or nil.
func (d *destinations) permit(ip net.IP) error {
	if d.named[ip.String()] {
		return nil
	}
	if d.local != nil && d.local.Contains(ip) {
		return nil
	}
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("%s is loopback, which is this sidecar rather than a host on the internet", ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf(
			"%s is link local. That range holds the instance metadata endpoint, which hands out "+
				"the node's own cloud credentials, so it is refused unless a rule names it", ip)
	case ip.IsPrivate():
		return fmt.Errorf(
			"%s is a private address outside this environment, which is the machine running the "+
				"environment rather than something the policy was written about", ip)
	case ip.IsUnspecified(), ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("%s is not a host this sidecar can be asked to reach", ip)
	case isSharedAddressSpace(ip):
		return fmt.Errorf(
			"%s is in the carrier grade range, which several clouds put their own services on", ip)
	case ip.To4() == nil && !d.allowIPv6:
		return fmt.Errorf(
			"%s is IPv6 and this environment has IPv6 off. Set egress.allow_ipv6 to turn it on", ip)
	}
	return nil
}

// isSharedAddressSpace reports whether an address is in 100.64.0.0/10.
//
// Named separately because Go has no predicate for it and it is not private
// by RFC 1918. It is where several clouds put internal services, and one of
// them puts a metadata endpoint there, so leaving it out would close the front
// door and leave a window open.
func isSharedAddressSpace(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64
}

// dialGuarded resolves a destination and connects to the first address the
// guard permits.
//
// Resolution happens here rather than inside the dialer so that the decision
// is made about the address that will actually be connected to. Handing the
// name to net.Dial and checking the name would be checking something the
// kernel is about to ignore.
func (p *proxy) dialGuarded(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := p.lookupIP(lookup, host)
	if err != nil {
		return nil, err
	}

	var dialer net.Dialer
	var refusals []string
	var lastDial error
	for _, a := range addrs {
		if reason := p.destinations.permit(a); reason != nil {
			refusals = append(refusals, reason.Error())
			continue
		}
		attempt, attemptCancel := context.WithTimeout(ctx, 30*time.Second)
		conn, dialErr := dialer.DialContext(attempt, network, net.JoinHostPort(a.String(), port))
		attemptCancel()
		if dialErr == nil {
			return conn, nil
		}
		lastDial = dialErr
	}
	if len(refusals) > 0 {
		// The refusal wins over a dial error, because a name that resolves to
		// one address the policy refuses and one that is merely down is still
		// a name the policy refuses.
		return nil, fmt.Errorf("%s: %s", host, strings.Join(refusals, "; "))
	}
	if lastDial != nil {
		return nil, lastDial
	}
	return nil, fmt.Errorf("%s resolved to no address", host)
}

// lookupIP resolves a name to addresses.
//
// A field rather than a direct call to the resolver so that a test can say
// what a name resolves to. The property being tested is that the guard reads
// the address and not the name, and the only way to state that in a test is to
// point a name somewhere it would not go on its own, which is exactly what an
// attacker with a domain does.
func (p *proxy) lookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if p.resolve != nil {
		return p.resolve(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

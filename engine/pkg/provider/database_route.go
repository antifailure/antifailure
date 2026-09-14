package provider

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// DatabaseTrustBundlePath is the public database CA file inside each service
// and migration container. It is never part of HTTP TrustEnv.
const DatabaseTrustBundlePath = "/etc/antifailure/database-ca.crt"

// DatabaseRoute binds one sidecar listener to one trusted database endpoint.
// Upstream contains only host:port, never a connection string or credential.
// Clients cannot supply another destination through the relay protocol.
// This file uses only the standard library so it can also be embedded in the
// sidecar image without the rest of the provider package.
type DatabaseRoute struct {
	Port     int    `json:"port"`
	Upstream string `json:"upstream"`
}

// ValidateDatabaseRoutes checks the trusted relay contract without doing DNS.
// A hostname may be valid here and still be refused when the runtime resolves
// it. Every runtime must resolve before it creates resources.
func ValidateDatabaseRoutes(routes []DatabaseRoute) error {
	seen := make(map[int]bool, len(routes))
	for i, route := range routes {
		if route.Port < 1024 || route.Port > 65535 || route.Port == 3128 {
			return fmt.Errorf("database route %d needs an unreserved listener port from 1024 through 65535", i)
		}
		if seen[route.Port] {
			return fmt.Errorf("database route %d repeats a listener port", i)
		}
		seen[route.Port] = true
		host, _, err := databaseUpstream(route.Upstream)
		if err != nil {
			return fmt.Errorf("database route %d: %w", i, err)
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if err := databaseAddress(ip); err != nil {
				return fmt.Errorf("database route %d: %w", i, err)
			}
		}
	}
	return nil
}

func databaseUpstream(upstream string) (string, string, error) {
	host, port, err := net.SplitHostPort(upstream)
	if err != nil || host == "" || strings.ContainsAny(host, " /\\@%?#\t\r\n") {
		// Do not echo malformed input: a caller might accidentally pass a URL
		// containing a password instead of the required host and port.
		return "", "", fmt.Errorf("the database upstream must contain only a hostname or IP address and a numeric port")
	}
	if _, err := netip.ParseAddr(host); err != nil {
		for _, c := range host {
			if !hostnameRune(c) {
				return "", "", fmt.Errorf("the database upstream contains an invalid hostname")
			}
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("the database upstream needs a numeric port from 1 through 65535")
	}
	return host, strconv.Itoa(n), nil
}

// hostnameRune admits the characters a DNS name may carry, and an underscore,
// which some cloud endpoints use. Anything else could smuggle a path, a user or
// a second address into what the relay dials.
func hostnameRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '_' || c == '.'
}

var databaseServiceAddresses = map[netip.Addr]bool{
	netip.MustParseAddr("168.63.129.16"):   true, // Azure platform services.
	netip.MustParseAddr("100.100.100.200"): true, // Alibaba instance metadata.
	netip.MustParseAddr("fd20:ce::254"):    true, // Google instance metadata.
}

var databaseAWSServiceRange = netip.MustParsePrefix("fd00:ec2::/32")
var databaseNAT64Range = netip.MustParsePrefix("64:ff9b::/96")

// databaseAddress admits private databases but never host or cloud services.
// This differs intentionally from arbitrary HTTP egress: the orchestrator
// selected this exact database, and a private endpoint is a supported target.
func databaseAddress(ip netip.Addr) error {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() ||
		ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("the database upstream resolves to a loopback, link-local, multicast or unspecified address")
	}
	if databaseServiceAddresses[ip] || databaseAWSServiceRange.Contains(ip) {
		return fmt.Errorf("the database upstream resolves to a cloud metadata or platform service address")
	}
	if ip.Is4() {
		v4 := ip.As4()
		if v4[0] == 0 || v4[0] >= 240 {
			return fmt.Errorf("the database upstream resolves to a reserved host address")
		}
	}
	if databaseNAT64Range.Contains(ip) {
		v6 := ip.As16()
		return databaseAddress(netip.AddrFrom4([4]byte{v6[12], v6[13], v6[14], v6[15]}))
	}
	return nil
}

// ResolveDatabaseRoutes returns a copy pinned to validated literal addresses.
// The URL given to the application keeps its original TLS hostname. Pinning
// the relay target makes the network policy and the actual dial agree even if
// DNS changes after startup. Recreate the environment to adopt a new address.
func ResolveDatabaseRoutes(ctx context.Context, routes []DatabaseRoute) ([]DatabaseRoute, error) {
	return resolveDatabaseRoutes(ctx, routes, net.DefaultResolver.LookupIPAddr)
}

func resolveDatabaseRoutes(
	ctx context.Context,
	routes []DatabaseRoute,
	lookup func(context.Context, string) ([]net.IPAddr, error),
) ([]DatabaseRoute, error) {
	if err := ValidateDatabaseRoutes(routes); err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pinned := make([]DatabaseRoute, 0, len(routes))
	for i, route := range routes {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("resolving database route %d: %w", i, err)
		}
		host, port, _ := databaseUpstream(route.Upstream)
		if ip, err := netip.ParseAddr(host); err == nil {
			route.Upstream = net.JoinHostPort(ip.Unmap().String(), port)
			pinned = append(pinned, route)
			continue
		}
		addresses, err := lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolving database route %d: %w", i, err)
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("database route %d resolved to no address", i)
		}
		var selected netip.Addr
		for _, address := range addresses {
			ip, valid := netip.AddrFromSlice(address.IP)
			if !valid || address.Zone != "" {
				return nil, fmt.Errorf("database route %d resolved to an invalid or scoped address", i)
			}
			ip = ip.Unmap()
			if err := databaseAddress(ip); err != nil {
				return nil, fmt.Errorf("database route %d: %w", i, err)
			}
			// Prefer IPv4 when available because the local runtime's networks
			// are IPv4. Every answer is validated, including unused answers.
			if !selected.IsValid() || (!selected.Is4() && ip.Is4()) {
				selected = ip
			}
		}
		route.Upstream = net.JoinHostPort(selected.String(), port)
		pinned = append(pinned, route)
	}
	return pinned, nil
}

package provider

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDatabaseRoutesRejectUnsafeAddresses(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1", "127.8.2.3", "0.0.0.0", "0.1.2.3", "240.0.0.1",
		"169.254.169.254", "169.254.170.2", "224.0.0.1", "255.255.255.255",
		"168.63.129.16", "100.100.100.200", "::", "::1", "fe80::1", "ff02::1",
		"fd00:ec2::254", "fd00:ec2::23", "fd20:ce::254",
		"::ffff:169.254.169.254", "64:ff9b::a9fe:a9fe", "64:ff9b::7f00:1",
	} {
		t.Run(host, func(t *testing.T) {
			err := ValidateDatabaseRoutes([]DatabaseRoute{{Port: 45000, Upstream: net.JoinHostPort(host, "5432")}})
			require.Error(t, err, "a fixed relay must not become a host or metadata service tunnel")
		})
	}
}

func TestDatabaseRoutesPermitPrivateAndPublicDatabases(t *testing.T) {
	for _, host := range []string{"10.10.0.4", "172.18.0.3", "192.168.1.2", "100.64.1.2", "8.8.8.8", "fd12:3456::10", "2001:db8::4", "fake"} {
		t.Run(host, func(t *testing.T) {
			require.NoError(t, ValidateDatabaseRoutes([]DatabaseRoute{{Port: 45000, Upstream: net.JoinHostPort(host, "5432")}}))
		})
	}
}

func TestDatabaseRoutesRequireUniqueUnreservedListeners(t *testing.T) {
	for _, port := range []int{-1, 0, 53, 80, 443, 1023, 3128, 65536} {
		require.Error(t, ValidateDatabaseRoutes([]DatabaseRoute{{Port: port, Upstream: "db.example.test:5432"}}))
	}
	_, err := ResolveDatabaseRoutes(context.Background(), []DatabaseRoute{
		{Port: 45000, Upstream: "10.0.0.1:5432"},
		{Port: 45000, Upstream: "10.0.0.2:5432"},
	})
	require.ErrorContains(t, err, "repeats")
}

func TestDatabaseRoutesRefuseMalformedUpstreamsWithoutQuotingCredentials(t *testing.T) {
	for _, upstream := range []string{"", "host", ":5432", "host:0", "host:65536", "host:postgres", "host:5432/path", "bad host:5432", "host\x1b:5432", "postgres://name:AF_FAKE_PRIVATE@host:5432/db", "name@host:5432"} {
		err := ValidateDatabaseRoutes([]DatabaseRoute{{Port: 45000, Upstream: upstream}})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "AF_FAKE_PRIVATE")
	}
}

func TestDatabaseRouteResolutionPinsACopyAndBoundsDNS(t *testing.T) {
	routes := []DatabaseRoute{{Port: 45000, Upstream: "db.example.test:5432"}}
	called := 0
	pinned, err := resolveDatabaseRoutes(context.Background(), routes, func(ctx context.Context, name string) ([]net.IPAddr, error) {
		called++
		require.Equal(t, "db.example.test", name)
		deadline, bounded := ctx.Deadline()
		require.True(t, bounded, "DNS could hold runtime startup indefinitely")
		require.LessOrEqual(t, time.Until(deadline), 10*time.Second)
		return []net.IPAddr{{IP: net.ParseIP("fd12::10")}, {IP: net.ParseIP("10.10.0.4")}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, called)
	require.Equal(t, []DatabaseRoute{{Port: 45000, Upstream: "10.10.0.4:5432"}}, pinned)
	require.Equal(t, "db.example.test:5432", routes[0].Upstream, "the application's original TLS hostname must not be overwritten")
}

func TestDatabaseRouteResolutionRefusesEveryUnsafeDNSAnswer(t *testing.T) {
	for _, unsafe := range []string{"127.0.0.1", "169.254.169.254", "168.63.129.16", "fd00:ec2::254", "fd20:ce::254"} {
		t.Run(unsafe, func(t *testing.T) {
			pinned, err := resolveDatabaseRoutes(context.Background(), []DatabaseRoute{{Port: 45000, Upstream: "db.example.test:5432"}}, func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("10.0.0.4")}, {IP: net.ParseIP(unsafe)}}, nil
			})
			require.Error(t, err, "a safe first answer must not hide an unsafe later answer")
			require.Nil(t, pinned)
		})
	}
}

func TestDatabaseRouteResolutionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []net.IPAddr
		err  error
	}{
		{"no answers", nil, nil},
		{"resolver failed", []net.IPAddr{{IP: net.ParseIP("10.0.0.4")}}, errors.New("resolver unavailable")},
		{"invalid answer", []net.IPAddr{{IP: nil}}, nil},
		{"scoped answer", []net.IPAddr{{IP: net.ParseIP("fd12::1"), Zone: "lo0"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinned, err := resolveDatabaseRoutes(context.Background(), []DatabaseRoute{{Port: 45000, Upstream: "db.example.test:5432"}}, func(context.Context, string) ([]net.IPAddr, error) {
				return tc.ips, tc.err
			})
			require.Error(t, err)
			require.Nil(t, pinned)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := resolveDatabaseRoutes(ctx, []DatabaseRoute{{Port: 45000, Upstream: "db.example.test:5432"}}, func(context.Context, string) ([]net.IPAddr, error) {
		called = true
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.4")}}, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

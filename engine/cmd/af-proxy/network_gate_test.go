package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func gateTCPServer(t *testing.T) (string, *atomic.Int32, func()) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	var calls atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			calls.Add(1)
			_ = c.Close()
		}
	}()
	closeServer := func() { _ = l.Close() }
	t.Cleanup(closeServer)
	return l.Addr().String(), &calls, closeServer
}

func gateTestConfig(t *testing.T) networkGateConfig {
	t.Helper()
	control, _, _ := gateTCPServer(t)
	closed, _, closeServer := gateTCPServer(t)
	closeServer()
	return networkGateConfig{control: control, probeTimeout: 20 * time.Millisecond,
		interval: 5 * time.Millisecond, consecutive: 3,
		probes: []networkGateProbe{{"direct", "tcp4", closed}},
	}
}

func TestNetworkGateRequiresPositiveControl(t *testing.T) {
	c := gateTestConfig(t)
	c.control = c.probes[0].address
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, runNetworkGate(ctx, c), context.DeadlineExceeded)
}

func TestNetworkGateTCPHandshakeIsAnEscape(t *testing.T) {
	c := gateTestConfig(t)
	address, calls, _ := gateTCPServer(t)
	c.probes = []networkGateProbe{{"metadata", "tcp4", address}}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	err := runNetworkGate(ctx, c)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Greater(t, calls.Load(), int32(0), "a real direct TCP handshake must have reached the receiver")
}

func TestNetworkGateUDPResponseIsAnEscape(t *testing.T) {
	c := gateTestConfig(t)
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	queries := make(chan []byte, 100)
	go func() {
		for {
			var b [512]byte
			n, peer, err := l.ReadFrom(b[:])
			if err != nil {
				return
			}
			queries <- append([]byte(nil), b[:n]...)
			_, _ = l.WriteTo([]byte("response"), peer)
		}
	}()
	c.probes = []networkGateProbe{{"public DNS", "udp4", l.LocalAddr().String()}}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, runNetworkGate(ctx, c), context.DeadlineExceeded)
	select {
	case query := <-queries:
		require.Equal(t, []byte{0x41, 0x46, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}, query)
	default:
		t.Fatal("no real UDP query reached the receiver")
	}
}

func TestNetworkGateWaitsForThreeDeniedRounds(t *testing.T) {
	c := gateTestConfig(t)
	control, calls, _ := gateTCPServer(t)
	c.control = control
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runNetworkGate(ctx, c))
	require.Eventually(t, func() bool { return calls.Load() >= 6 }, time.Second, time.Millisecond)
}

func TestNetworkGateResetsAfterAnEscape(t *testing.T) {
	streak := gateStreak{required: 3}
	for i, denied := range []bool{true, true, false, true, true, true} {
		require.Equal(t, i == 5, streak.observe(denied), "round %d", i+1)
	}
}

func TestNetworkGateUnknownProbeFailureCannotPass(t *testing.T) {
	c := gateTestConfig(t)
	c.probes[0].network = "not-a-network"
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, runNetworkGate(ctx, c), context.DeadlineExceeded)
	_, err := gateDenied("probe", errors.New("no file descriptors"))
	require.Error(t, err)
}

func TestNetworkGateUsesLiteralAPIAndBothIPFamilies(t *testing.T) {
	c, err := defaultNetworkGate("10.0.0.2:3128", "10.43.0.1")
	require.NoError(t, err)
	require.Equal(t, []networkGateProbe{
		{"public TCP IPv4", "tcp4", "1.1.1.1:443"},
		{"public DNS IPv4", "udp4", "1.1.1.1:53"},
		{"metadata IPv4", "tcp4", "169.254.169.254:80"},
		{"public TCP IPv6", "tcp6", "[2606:4700:4700::1111]:443"},
		{"public DNS IPv6", "udp6", "[2606:4700:4700::1111]:53"},
		{"AWS metadata IPv6", "tcp6", "[fd00:ec2::254]:80"},
		{"Google metadata IPv6", "tcp6", "[fd20:ce::254]:80"},
		{"cluster API", "tcp", "10.43.0.1:443"},
	}, c.probes)
	_, err = defaultNetworkGate("10.0.0.2:3128", "kubernetes.default.svc")
	require.Error(t, err)
}

func TestNetworkGateCLIRefusesBeforeLoadingCustomerConfiguration(t *testing.T) {
	if os.Getenv("AF_TEST_NETWORK_GATE_MAIN") == "1" {
		flag.CommandLine = flag.NewFlagSet("af-proxy", flag.ExitOnError)
		os.Args = []string{"af-proxy", "-network-gate", "-gate-control", "127.0.0.1:3128", "-config", "/configuration-must-not-be-read"}
		main()
		return
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(executable, "-test.run=^TestNetworkGateCLIRefusesBeforeLoadingCustomerConfiguration$")
	cmd.Env = append(os.Environ(), "AF_TEST_NETWORK_GATE_MAIN=1", "KUBERNETES_SERVICE_HOST=")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "KUBERNETES_SERVICE_HOST must contain the actual API service IP")
	require.NotContains(t, string(output), "configuration-must-not-be-read")
}

func TestNetworkGateRechecksControlAfterProbing(t *testing.T) {
	c := gateTestConfig(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for n := 1; n <= 5; n++ {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			if n == 5 {
				_ = l.Close()
			}
			_ = conn.Close()
		}
	}()
	c.control = l.Addr().String()
	// A silent UDP peer keeps the direct probe open while the positive
	// control disappears, without depending on the accept loop's scheduling.
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = udp.Close() })
	c.probes = []networkGateProbe{{"silent receiver", "udp4", udp.LocalAddr().String()}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, runNetworkGate(ctx, c), context.DeadlineExceeded)
}

func TestNetworkGateIPv6Sockets(t *testing.T) {
	tcp, err := net.Listen("tcp6", "[::1]:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = tcp.Close() })
	go func() {
		c, err := tcp.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	reached, err := gateReachable(context.Background(), networkGateProbe{"IPv6 TCP", "tcp6", tcp.Addr().String()}, time.Second)
	require.NoError(t, err)
	require.True(t, reached)
	udp, err := net.ListenPacket("udp6", "[::1]:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = udp.Close() })
	go func() {
		var b [512]byte
		_, peer, err := udp.ReadFrom(b[:])
		if err == nil {
			_, _ = udp.WriteTo([]byte("IPv6 DNS answer"), peer)
		}
	}()
	reached, err = gateReachable(context.Background(), networkGateProbe{"IPv6 DNS", "udp6", udp.LocalAddr().String()}, time.Second)
	require.NoError(t, err)
	require.True(t, reached)
}

func TestNetworkGateDisabledAddressFamilyHasNoRoute(t *testing.T) {
	reached, err := gateDenied("IPv6", &net.OpError{Op: "dial", Net: "tcp6", Err: syscall.EAFNOSUPPORT})
	require.NoError(t, err)
	require.False(t, reached)
}

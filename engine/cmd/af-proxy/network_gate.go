package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// The gate runs in a trusted init container sharing the application's network
// namespace. A different preflight pod cannot measure this pod's CNI startup.
// Probes are observations of reachability, not a proof against a later policy
// change or a one-way UDP receiver that deliberately sends nothing back.
type networkGateProbe struct {
	name, network, address string
}

type networkGateConfig struct {
	control                string
	probes                 []networkGateProbe
	probeTimeout, interval time.Duration
	consecutive            int
}

func defaultNetworkGate(control, apiHost string) (networkGateConfig, error) {
	if net.ParseIP(apiHost) == nil {
		return networkGateConfig{}, fmt.Errorf("KUBERNETES_SERVICE_HOST must contain the actual API service IP")
	}
	c := networkGateConfig{
		control: control, probeTimeout: time.Second, interval: 250 * time.Millisecond, consecutive: 3,
		probes: []networkGateProbe{
			{"public TCP IPv4", "tcp4", "1.1.1.1:443"},
			{"public DNS IPv4", "udp4", "1.1.1.1:53"},
			{"metadata IPv4", "tcp4", "169.254.169.254:80"},
			{"public TCP IPv6", "tcp6", "[2606:4700:4700::1111]:443"},
			{"public DNS IPv6", "udp6", "[2606:4700:4700::1111]:53"},
			{"AWS metadata IPv6", "tcp6", "[fd00:ec2::254]:80"},
			{"Google metadata IPv6", "tcp6", "[fd20:ce::254]:80"},
			{"cluster API", "tcp", net.JoinHostPort(apiHost, "443")},
		},
	}
	return c, nil
}

func networkGateMode(control string) error {
	c, err := defaultNetworkGate(control, os.Getenv("KUBERNETES_SERVICE_HOST"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return runNetworkGate(ctx, c)
}

func runNetworkGate(ctx context.Context, c networkGateConfig) error {
	if c.consecutive < 3 || len(c.probes) == 0 || c.probeTimeout <= 0 || c.interval <= 0 {
		return fmt.Errorf("incomplete network gate configuration")
	}
	for _, address := range append([]string{c.control}, probeAddresses(c.probes)...) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(host) == nil || port == "" {
			return fmt.Errorf("network gate requires literal IP endpoints")
		}
	}
	stable := gateStreak{required: c.consecutive}
	last := "network readiness has not been established"
	for {
		// A disconnected pod is not evidence of an enforced policy. Check the
		// real sidecar before AND after the independent direct probes.
		ready, err := gateReachable(ctx, networkGateProbe{"sidecar", "tcp", c.control}, c.probeTimeout)
		if err != nil {
			return err
		}
		denied := ready
		if !ready {
			last = "the sidecar is unreachable"
		}
		if ready {
			type outcome struct {
				probe   networkGateProbe
				reached bool
				err     error
			}
			results := make(chan outcome, len(c.probes))
			for _, p := range c.probes {
				go func() {
					reached, err := gateReachable(ctx, p, c.probeTimeout)
					results <- outcome{p, reached, err}
				}()
			}
			for range c.probes {
				result := <-results
				if result.err != nil {
					denied = false
					last = result.err.Error()
				}
				if result.reached {
					denied = false
					last = result.probe.name + " is reachable"
				}
			}
			ready, err = gateReachable(ctx, networkGateProbe{"sidecar", "tcp", c.control}, c.probeTimeout)
			if err != nil {
				return err
			}
			if !ready {
				denied = false
				last = "the sidecar became unreachable"
			}
		}
		if stable.observe(denied) && ctx.Err() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("network containment was not established: %s: %w", last, ctx.Err())
		case <-time.After(c.interval):
		}
	}
}

type gateStreak struct{ count, required int }

func (s *gateStreak) observe(denied bool) bool {
	if denied {
		s.count++
	} else {
		s.count = 0
	}
	return s.count >= s.required
}

func probeAddresses(probes []networkGateProbe) []string {
	addresses := make([]string, 0, len(probes))
	for _, p := range probes {
		addresses = append(addresses, p.address)
	}
	return addresses
}

func gateReachable(ctx context.Context, p networkGateProbe, timeout time.Duration) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(probeCtx, p.network, p.address)
	if err != nil {
		return gateDenied(p.name, err)
	}
	// The probe only asks whether a packet got through. A failure to close a
	// socket that already answered changes nothing about that answer.
	defer func() { _ = c.Close() }()
	if p.network == "tcp" || p.network == "tcp4" || p.network == "tcp6" {
		// A completed TCP handshake is enough. Never request metadata or a
		// credential, and do not depend on an HTTP status being successful.
		return true, nil
	}
	if p.network != "udp4" && p.network != "udp6" {
		return false, fmt.Errorf("unsupported gate probe network")
	}
	deadline, _ := probeCtx.Deadline()
	if err := c.SetDeadline(deadline); err != nil {
		return false, err
	}
	// A standard recursive A query for example.com, not an application
	// payload. Any returned datagram proves direct reachability.
	query := []byte{0x41, 0x46, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	if _, err = c.Write(query); err != nil {
		return gateDenied(p.name, err)
	}
	var response [512]byte
	_, err = c.Read(response[:])
	if err != nil {
		return gateDenied(p.name, err)
	}
	return true, nil
}

func gateDenied(name string, err error) (bool, error) {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return false, nil
	}
	// A kernel without this address family cannot send an IPv6 packet.
	// That is a missing route, not an incomplete IPv6 probe.
	for _, denied := range []error{syscall.ECONNREFUSED, syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.EAFNOSUPPORT, syscall.EACCES, syscall.EPERM} {
		if errors.Is(err, denied) {
			return false, nil
		}
	}
	return false, fmt.Errorf("%s probe could not complete: %w", name, err)
}

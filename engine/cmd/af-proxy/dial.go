package main

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// Dial mode answers one question: is this address, on the environment's own
// network, accepting connections right now.
//
// It exists because the engine cannot ask. An emulator joins the inner network
// and nothing else, deliberately, so it publishes no port and the host has no
// route to it. The sidecar is the only container attached to that network that
// the engine can reach, and it holds the emulator's address already, because
// that is where emulate mode forwards to.
//
// The failure this closes: the engine treated a STARTED emulator container as a
// ready one. A container is started the moment the daemon says so, and the
// server inside it binds its port some time after that: LocalStack, Azurite and
// the gcloud emulators all spend seconds to tens of seconds getting there. The
// application started in that window, its first call reached the sidecar, the
// sidecar dialled an emulator that was not listening yet, and the application
// was handed 502 Bad Gateway. Nothing was broken, the ordering was wrong, and
// the 502 is indistinguishable from the emulator being absent.
//
// A separate mode rather than a flag on the proxy, for the same reason the
// forwarder is one: it reads no configuration, applies no policy, and must not
// be able to fail for want of either.

// dialDefaultTimeout is how long one attempt waits for a connection.
//
// Short on purpose. The caller retries until its own deadline, and a long
// per-attempt timeout would make a refused connection, which answers
// immediately, indistinguishable from a host that is not resolving yet.
const dialDefaultTimeout = 2 * time.Second

// dialMode connects to target once and reports what happened.
//
// Success closes the connection immediately. The question is whether anything
// is listening, and a probe that held the connection open or wrote to it would
// be a request the emulator has to understand, which turns "is it up" into "is
// it up and does it speak the protocol I guessed".
func dialMode(target string, timeout time.Duration) error {
	if target == "" {
		return errors.New("-dial needs an address to connect to, as host:port")
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || port == "" {
		// Named rather than passed to the dialer. net.Dial on an address with
		// no port reports "missing port in address", which reads like the
		// remote end said something, and this one is our own caller's mistake.
		return fmt.Errorf("%q is not a host:port address", target)
	}
	conn, err := net.DialTimeout("tcp", target, effectiveDialTimeout(timeout))
	if err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}
	fmt.Printf("af-proxy dial: %s is accepting connections\n", target)
	return nil
}

// effectiveDialTimeout is the bound one attempt actually runs under.
//
// Its own function because zero is the dangerous value and zero is what a flag
// nobody set carries. net.DialTimeout reads a zero timeout as NO timeout, so an
// address that neither accepts nor refuses, which is what a host still coming up
// behind a firewall looks like, would leave the attempt waiting for the
// operating system rather than for this bound. The caller loops until its own
// deadline, and an attempt that never returns means that deadline is never
// reached: a bounded wait becomes a hang, in the one code path that exists to
// stop a hang from looking like a failure.
func effectiveDialTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return dialDefaultTimeout
	}
	return d
}

package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

// Dial mode, against real sockets.
//
// A fake dialer would prove that the function calls the thing it calls. What
// this mode is FOR is answering one question about a real TCP port, and the two
// answers it has to keep apart, a port that accepts and a port that refuses,
// are both properties of an operating system rather than of this code.

func TestDialMode_ReportsAListenerThatIsAccepting(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no loopback listener: %v", err)
	}
	defer func() { _ = ln.Close() }()
	// Accepting, because a listener with a full backlog and nobody calling
	// Accept still completes a handshake on Linux and this test would then be
	// measuring the backlog rather than the dial.
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = c.Close()
		}
	}()

	if err := dialMode(ln.Addr().String(), time.Second); err != nil {
		t.Fatalf("dialMode refused an address that is accepting connections: %v", err)
	}
}

func TestDialMode_ReportsAPortWithNothingBehindIt(t *testing.T) {
	t.Parallel()
	// A port that was open and is now closed, which is exactly the emulator's
	// state in the window this mode exists to detect: the name resolves, the
	// container is up, nothing is listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no loopback listener: %v", err)
	}
	address := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}

	err = dialMode(address, time.Second)
	if err == nil {
		t.Fatal("dialMode reported a closed port as accepting connections, which is the " +
			"whole failure it exists to catch")
	}
	// The reason travels, because the engine puts it in AF-RUN-049 and it is
	// the difference between an emulator that is slow and a name that does not
	// resolve.
	if !strings.Contains(err.Error(), address) {
		t.Fatalf("the refusal does not name the address it could not reach: %v", err)
	}
}

func TestDialMode_RefusesAnEmptyTarget(t *testing.T) {
	t.Parallel()
	err := dialMode("", time.Second)
	if err == nil {
		t.Fatal("dialMode accepted an empty address")
	}
	if !strings.Contains(err.Error(), "-dial") {
		t.Fatalf("the refusal does not name the flag that is missing a value: %v", err)
	}
}

func TestDialMode_RefusesAnAddressWithNoPort(t *testing.T) {
	t.Parallel()
	// The emulator's address is built by joining an alias and a port, and a
	// zero port would produce something like this. Refused by name rather than
	// handed to the dialer, whose own message reads as though the remote end
	// said something.
	err := dialMode("af-emu-probe", time.Second)
	if err == nil {
		t.Fatal("dialMode accepted an address with no port")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("the refusal does not say what shape the address should have: %v", err)
	}
}

func TestDialMode_RefusesAnAddressWithNoHost(t *testing.T) {
	t.Parallel()
	// ":8080" parses as a valid listen address and is not a valid dial target:
	// it is the shape a missing alias produces, and dialling it would reach
	// whatever is on the sidecar's own loopback and report the emulator ready.
	err := dialMode(":8080", time.Second)
	if err == nil {
		t.Fatal("dialMode accepted an address with no host, which resolves to the probe's " +
			"own container and would report any emulator ready")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("the refusal does not say what shape the address should have: %v", err)
	}
}

func TestEffectiveDialTimeout_AZeroOrNegativeBoundBecomesTheDefault(t *testing.T) {
	t.Parallel()
	// Asserted on the resolved bound rather than on the outcome of a dial,
	// because a dial to a listener that accepts SUCCEEDS under either bound and
	// a test written that way passes whether the fallback exists or not. The
	// difference only shows against an address that neither accepts nor refuses,
	// where one bound returns and the other waits forever, and reproducing that
	// needs a network nobody has in a test.
	if got := effectiveDialTimeout(0); got != dialDefaultTimeout {
		t.Fatalf("a zero bound resolved to %s, and net.DialTimeout reads zero as no timeout "+
			"at all, so the readiness loop would wait on one attempt forever", got)
	}
	if got := effectiveDialTimeout(-5 * time.Second); got != dialDefaultTimeout {
		t.Fatalf("a negative bound resolved to %s, which net.DialTimeout also reads as no "+
			"timeout", got)
	}
	if got := effectiveDialTimeout(7 * time.Second); got != 7*time.Second {
		t.Fatalf("a bound the caller set was replaced with %s, so the engine cannot shorten "+
			"or lengthen an attempt", got)
	}
	if dialDefaultTimeout <= 0 {
		t.Fatalf("the fallback is %s, so falling back to it changes nothing",
			dialDefaultTimeout)
	}
}

func TestDialMode_TakesAZeroTimeoutAndStillAnswers(t *testing.T) {
	t.Parallel()
	// The wiring half: the flag's zero value reaches dialMode, and dialMode has
	// to resolve it rather than hand it to the dialer.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no loopback listener: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if err := dialMode(ln.Addr().String(), 0); err != nil {
		t.Fatalf("dialMode with no timeout refused a listener that is accepting: %v", err)
	}
}

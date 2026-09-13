package main

// Forward mode: the ingress forwarder, which publishes a web service on the
// host's loopback.
//
// THE FAILURE THIS REPLACES. The local runtime used to build a second image for
// this, FROM alpine with `RUN apk add --no-cache socat`, the first time any
// environment published a port. That build had the same three defects the
// sidecar's did: the inspect in front of it read every daemon error as
// absence, nothing bounded it, and its stream was drained in silence while it
// pulled a base image from Docker Hub and then a package from an Alpine
// mirror. And unlike the sidecar, nothing could ever publish it away, because
// the package fetch is inside the build.
//
// socat was doing four things here: accept on a port, dial a name, copy both
// ways, and pass a half close through. The standard library does all four, and
// this binary is already on the machine, fetched or compiled, before any
// service starts. So the forwarder is this binary with two flags, and the
// first af up reaches no package mirror at all.
//
// WHAT IT DOES NOT DO, on purpose. It applies no policy and reads no
// configuration: it sits on the edge network publishing a service the host
// already asked to reach, which is inbound traffic, and the egress policy is
// about the other direction. It dials exactly one address, the one it was
// started with, and it dials it per connection, so a name that resolves to
// several instances spreads connections across them the way socat's fork did,
// and a service that restarted on a new address is found again.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// forwardDialTimeout bounds one dial to the service. A service that is not
// listening refuses at once; this is for one whose packets go nowhere, which
// would otherwise hold the accepted connection open with nothing behind it.
// A variable only so a test can measure the bound in milliseconds.
var forwardDialTimeout = 10 * time.Second

// forwarder accepts connections and relays each one to target.
type forwarder struct {
	target string
	// dial is how the target is reached. A field so a test can observe that
	// every connection dials afresh rather than reusing one resolution.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	logf func(format string, args ...any)
}

// newForwarder is the forwarder forwardMode runs, with the bounded dialer.
func newForwarder(target string, logf func(format string, args ...any)) *forwarder {
	d := &net.Dialer{Timeout: forwardDialTimeout}
	return &forwarder{target: target, dial: d.DialContext, logf: logf}
}

// forwardMode runs the forwarder until its listener fails.
func forwardMode(listen, target string) error {
	if listen == "" || target == "" {
		return errors.New("-forward-listen and -forward-to are both required, and neither may be empty")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return fmt.Errorf("-forward-to %q is not a host and port: %w", target, err)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	f := newForwarder(target, log.Printf)
	log.Printf("af-proxy forward: listening on %s, forwarding to %s", ln.Addr(), target)
	return f.serve(ln)
}

// serve accepts until the listener is closed.
//
// An accept error other than a closed listener is retried with a growing pause
// rather than returned, because the usual one is a process out of file
// descriptors under a burst of connections, and exiting would take down a
// service's only address over a condition that clears when those connections
// end.
func (f *forwarder) serve(ln net.Listener) error {
	pause := 5 * time.Millisecond
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			f.logf("af-proxy forward: accepting on %s: %v", ln.Addr(), err)
			time.Sleep(pause)
			if pause < time.Second {
				pause *= 2
			}
			continue
		}
		pause = 5 * time.Millisecond
		go f.handle(c)
	}
}

// handle relays one accepted connection to the target.
func (f *forwarder) handle(client net.Conn) {
	defer func() { _ = client.Close() }()
	upstream, err := f.dial(context.Background(), "tcp", f.target)
	if err != nil {
		// Logged, and the client's connection closed with nothing sent. That
		// is what socat did, and it is what a client reads as the service not
		// being up yet, which is exactly what it is.
		f.logf("af-proxy forward: %s to %s: %v", client.RemoteAddr(), f.target, err)
		return
	}
	defer func() { _ = upstream.Close() }()
	relay(client, upstream)
}

// relay copies in both directions until both have ended.
func relay(client, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		halfCopy(upstream, client)
	}()
	go func() {
		defer wg.Done()
		halfCopy(client, upstream)
	}()
	wg.Wait()
}

// halfCopy copies src into dst until src ends.
//
// A clean end of src is passed on as a half close of dst, so the other
// direction keeps flowing: a client that sends a request and closes its
// writing side still receives the whole answer. Anything else, a reset or a
// write to a peer that has gone, closes both connections, because a relay that
// failed in one direction has nothing left to wait for in the other, and
// leaving it open would hold a connection to the service for as long as the
// service is willing to wait.
func halfCopy(dst, src net.Conn) {
	_, err := io.Copy(dst, src)
	if err == nil {
		if cw, ok := dst.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
			return
		}
	}
	_ = dst.Close()
	_ = src.Close()
}

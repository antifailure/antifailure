package main

// The ingress forwarder against real sockets.
//
// Every cell here is a real TCP connection on the loopback, because the thing
// being replaced was socat, and what socat was trusted to do is only visible
// on a real socket: a half close that has to travel through, a reset that has
// to end both sides, and a dial that has to happen per connection.

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// logLines collects the forwarder's log, safely, because it writes from the
// goroutine handling each connection.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// upstream runs a TCP server on the loopback that hands every connection to
// serve, and waits for every handler when the test ends so goleak sees none.
func upstream(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	return ln.Addr().String()
}

// echo copies what it reads back, and passes the end of the request on as the
// end of the answer.
func echo(c net.Conn) {
	_, _ = io.Copy(c, c)
	_ = c.(*net.TCPConn).CloseWrite()
}

// startForwarder runs a forwarder to target on the loopback and returns its
// address. The test fails by name if serve has not returned shortly after its
// listener closes.
func startForwarder(t *testing.T, target string,
	dial func(context.Context, string, string) (net.Conn, error),
) (string, *logLines) {
	t.Helper()
	if dial == nil {
		dial = (&net.Dialer{Timeout: forwardDialTimeout}).DialContext
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	logs := &logLines{}
	f := &forwarder{target: target, dial: dial, logf: logs.logf}
	done := make(chan error, 1)
	go func() { done <- f.serve(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("the forwarder was still accepting five seconds after its listener closed")
		}
	})
	return ln.Addr().String(), logs
}

func dialForwarder(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.SetDeadline(time.Now().Add(20*time.Second)))
	return c.(*net.TCPConn)
}

func TestForward_BytesArriveIntactInBothDirections(t *testing.T) {
	addr, _ := startForwarder(t, upstream(t, echo), nil)
	payload := make([]byte, 4<<20)
	_, err := rand.Read(payload)
	require.NoError(t, err)

	c := dialForwarder(t, addr)
	sent := make(chan error, 1)
	go func() {
		_, werr := c.Write(payload)
		if werr == nil {
			werr = c.CloseWrite()
		}
		sent <- werr
	}()
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	require.NoError(t, <-sent)
	require.Equal(t, len(payload), len(got), "the relay lost or invented bytes")
	require.True(t, bytes.Equal(payload, got), "the relay changed bytes on the way through")
}

func TestForward_AClientThatClosesFirstStillReceivesTheWholeAnswer(t *testing.T) {
	// The service answers only once it has read the end of the request, which
	// it can only see if the client's half close travelled through the relay.
	// Then it answers, into a connection whose client side is already closed
	// for writing, and the answer has to arrive in full.
	answer := bytes.Repeat([]byte("answer "), 64<<10)
	addr, _ := startForwarder(t, upstream(t, func(c net.Conn) {
		request, err := io.ReadAll(c)
		if err != nil || string(request) != "the whole request" {
			return
		}
		_, _ = c.Write(answer)
	}), nil)

	c := dialForwarder(t, addr)
	_, err := c.Write([]byte("the whole request"))
	require.NoError(t, err)
	require.NoError(t, c.CloseWrite())
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	require.Equal(t, len(answer), len(got),
		"the answer to a client that had closed its writing side did not arrive in full")
}

func TestForward_AnUpstreamThatRefusesClosesThatClientAndTheNextConnectionIsDialedAfresh(t *testing.T) {
	// A port nothing listens on, taken and released, so the dial is refused at
	// once rather than timing out.
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	refused := gone.Addr().String()
	require.NoError(t, gone.Close())
	serving := upstream(t, echo)

	var dials atomic.Int32
	d := &net.Dialer{Timeout: forwardDialTimeout}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		// The first connection finds the service down and the second finds it
		// up, which is a service restarting between two requests.
		if dials.Add(1) == 1 {
			return d.DialContext(ctx, network, refused)
		}
		return d.DialContext(ctx, network, serving)
	}
	addr, logs := startForwarder(t, "web:8000", dial)

	first := dialForwarder(t, addr)
	started := time.Now()
	got, _ := io.ReadAll(first)
	require.Empty(t, got, "a client whose service refused was sent something")
	require.Less(t, time.Since(started), 5*time.Second, "a refused dial held the client open")
	require.Contains(t, logs.text(), "to web:8000", "a refused dial was not logged naming its target")

	second := dialForwarder(t, addr)
	_, err = second.Write([]byte("after the restart"))
	require.NoError(t, err)
	require.NoError(t, second.CloseWrite())
	got, err = io.ReadAll(second)
	require.NoError(t, err)
	require.Equal(t, "after the restart", string(got),
		"one refused connection stopped the forwarder serving the next")
	require.EqualValues(t, 2, dials.Load(), "the target was not dialed once per connection")
}

func TestForward_ManyConnectionsAtOnceEachGetTheirOwnBytes(t *testing.T) {
	addr, _ := startForwarder(t, upstream(t, echo), nil)
	const connections = 64
	var wg sync.WaitGroup
	failures := make(chan error, connections)
	for i := range connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				failures <- err
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(20 * time.Second))
			payload := bytes.Repeat([]byte(fmt.Sprintf("connection %03d ", i)), 4096)
			go func() {
				_, _ = c.Write(payload)
				_ = c.(*net.TCPConn).CloseWrite()
			}()
			got, err := io.ReadAll(c)
			if err != nil {
				failures <- fmt.Errorf("connection %d: %w", i, err)
				return
			}
			if !bytes.Equal(payload, got) {
				failures <- fmt.Errorf("connection %d received %d bytes that were not its own", i, len(got))
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestForward_ASecondConnectionIsServedWhileTheFirstIsStillOpen(t *testing.T) {
	// One connection held open by a client that has not finished must not
	// stand in front of the next one. A forwarder that served connections one
	// at a time would pass every other cell here and hang a browser's second
	// request behind its first.
	addr, _ := startForwarder(t, upstream(t, echo), nil)

	first := dialForwarder(t, addr)
	_, err := first.Write([]byte("still talking"))
	require.NoError(t, err)
	buf := make([]byte, len("still talking"))
	_, err = io.ReadFull(first, buf)
	require.NoError(t, err, "the first connection was not served")

	second := dialForwarder(t, addr)
	require.NoError(t, second.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = second.Write([]byte("meanwhile"))
	require.NoError(t, err)
	require.NoError(t, second.CloseWrite())
	got, err := io.ReadAll(second)
	require.NoError(t, err, "a second connection waited behind a first that was still open")
	require.Equal(t, "meanwhile", string(got))
}

func TestForward_ADialThatGoesNowhereIsGivenUpOnWithinItsBound(t *testing.T) {
	// 192.0.2.1 is TEST-NET-1: routable in form, answered by nobody, so a SYN
	// sent to it is dropped rather than refused. Without a bound the dial
	// waits for the operating system's own timeout, over a minute, holding the
	// client's connection open with nothing behind it.
	previous := forwardDialTimeout
	forwardDialTimeout = 300 * time.Millisecond
	t.Cleanup(func() { forwardDialTimeout = previous })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	logs := &logLines{}
	f := newForwarder("192.0.2.1:9", logs.logf)
	done := make(chan error, 1)
	go func() { done <- f.serve(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})

	c := dialForwarder(t, ln.Addr().String())
	started := time.Now()
	got, _ := io.ReadAll(c)
	require.Empty(t, got)
	require.Less(t, time.Since(started), 5*time.Second,
		"a dial to an address that never answers held the client open past its bound")
	require.Contains(t, logs.text(), "to 192.0.2.1:9")
}

func TestForward_AClientThatClosesReleasesItsConnectionToTheService(t *testing.T) {
	// A service that says nothing and waits for the client. When the client
	// goes, the service has to see its connection end, or every browser tab
	// that was ever closed holds a connection to the application open.
	ended := make(chan struct{})
	addr, _ := startForwarder(t, upstream(t, func(c net.Conn) {
		_, _ = io.Copy(io.Discard, c)
		close(ended)
	}), nil)

	c := dialForwarder(t, addr)
	_, err := c.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, c.Close())
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the service's connection was still open five seconds after the client closed")
	}
}

func TestForward_AClientThatResetsReleasesItsConnectionToTheService(t *testing.T) {
	// A reset rather than a close: the relay's read fails instead of ending,
	// and a failure in one direction has to end the other. The service here
	// never closes on its own, so only the relay can end its connection.
	ended := make(chan struct{})
	accepted := make(chan struct{})
	addr, _ := startForwarder(t, upstream(t, func(c net.Conn) {
		close(accepted)
		buf := make([]byte, 1)
		for {
			if _, err := c.Read(buf); err != nil {
				close(ended)
				return
			}
		}
	}), nil)

	c := dialForwarder(t, addr)
	_, err := c.Write([]byte("x"))
	require.NoError(t, err)
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the service never received the connection")
	}
	require.NoError(t, c.SetLinger(0))
	require.NoError(t, c.Close())
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the service's connection was still open five seconds after the client reset it")
	}
}

func TestForwardMode_RefusesAnIncompleteCommandLine(t *testing.T) {
	err := forwardMode("", "web:8000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "-forward-listen and -forward-to are both required")

	err = forwardMode("127.0.0.1:0", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "-forward-listen and -forward-to are both required")

	err = forwardMode("127.0.0.1:0", "web")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a host and port")
}

// flakyListener fails its first accept with an error that is not a closed
// listener, then reports itself closed.
type flakyListener struct {
	calls atomic.Int32
}

func (l *flakyListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8000} }

func (l *flakyListener) Close() error { return nil }

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.calls.Add(1) == 1 {
		return nil, errors.New("accept tcp: too many open files")
	}
	return nil, net.ErrClosed
}

func TestForward_AnAcceptErrorIsRetriedRatherThanEndingTheForwarder(t *testing.T) {
	logs := &logLines{}
	f := &forwarder{target: "web:8000", logf: logs.logf}
	ln := &flakyListener{}
	require.NoError(t, f.serve(ln), "an accept error that clears on its own ended the forwarder")
	require.EqualValues(t, 2, ln.calls.Load(), "the forwarder did not accept again after the error")
	require.Contains(t, logs.text(), "too many open files")
}

func TestForward_TheCommandLineRunsTheForwarderWithoutReadingConfiguration(t *testing.T) {
	if os.Getenv("AF_TEST_FORWARD_MAIN") == "1" {
		flag.CommandLine = flag.NewFlagSet("af-proxy", flag.ExitOnError)
		os.Args = []string{"af-proxy", "-config", "/configuration-must-not-be-read",
			"-forward-listen", os.Getenv("AF_TEST_FORWARD_LISTEN"),
			"-forward-to", os.Getenv("AF_TEST_FORWARD_TO")}
		main()
		return
	}
	target := upstream(t, echo)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listen := free.Addr().String()
	require.NoError(t, free.Close())

	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(executable, "-test.run=^TestForward_TheCommandLineRunsTheForwarderWithoutReadingConfiguration$")
	cmd.Env = append(os.Environ(), "AF_TEST_FORWARD_MAIN=1",
		"AF_TEST_FORWARD_LISTEN="+listen, "AF_TEST_FORWARD_TO="+target)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	require.NoError(t, cmd.Start())
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})

	var c net.Conn
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err = net.DialTimeout("tcp", listen, time.Second)
		if err == nil {
			break
		}
		select {
		case waitErr := <-exited:
			exited <- waitErr
			t.Fatalf("the command line exited instead of forwarding: %v\n%s", waitErr, output.String())
		default:
		}
		require.True(t, time.Now().Before(deadline), "nothing listened on %s: %s", listen, output.String())
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = c.Close() }()
	require.NoError(t, c.SetDeadline(time.Now().Add(10*time.Second)))
	_, err = c.Write([]byte("through the real command line"))
	require.NoError(t, err)
	require.NoError(t, c.(*net.TCPConn).CloseWrite())
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	require.Equal(t, "through the real command line", string(got))
	require.NotContains(t, output.String(), "configuration-must-not-be-read",
		"forward mode read the proxy's configuration, so a forwarder with no policy file would not start")
}

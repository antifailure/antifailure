package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const databaseTestUpstream = "10.20.30.40:5432"

func databaseTestProxy(records *bytes.Buffer, target string) *proxy {
	return &proxy{
		out: json.NewEncoder(records),
		databaseDial: func(ctx context.Context, address string) (net.Conn, error) {
			if address != databaseTestUpstream {
				return nil, fmt.Errorf("the relay selected an endpoint other than its fixed target")
			}
			// Translation belongs only to this protocol fixture. Production
			// uses the actual validated address, exercised by the Docker test.
			return (&net.Dialer{}).DialContext(ctx, "tcp", target)
		},
	}
}

func databaseTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func TestDatabaseRelayPreservesSSLRequestAndTreatsClientTargetsAsBytes(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer upstream.Close()
	sslRequest := []byte{0, 0, 0, 8, 4, 210, 22, 47}
	clientTarget := []byte("CONNECT a-different-database.example:6432\r\n")
	seen := make(chan []byte, 1)
	serverDone := make(chan error, 1)
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		request := make([]byte, len(sslRequest))
		if _, err := io.ReadFull(conn, request); err != nil {
			serverDone <- err
			return
		}
		seen <- request
		_, _ = conn.Write([]byte("S"))
		payload := make([]byte, len(clientTarget))
		_, err = io.ReadFull(conn, payload)
		if err == nil {
			_, err = conn.Write(append([]byte("fixed upstream: "), payload...))
		}
		serverDone <- err
	}()
	var records bytes.Buffer
	p := databaseTestProxy(&records, upstream.Addr().String())
	port := databaseTestPort(t)
	relays, err := p.startDatabaseRelays(context.Background(), "127.0.0.1", []provider.DatabaseRoute{{Port: port, Upstream: databaseTestUpstream}}, nil)
	require.NoError(t, err)
	defer relays.Close()
	client, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = client.Write(sslRequest)
	require.NoError(t, err)
	response := make([]byte, 1)
	_, err = io.ReadFull(client, response)
	require.NoError(t, err, "PostgreSQL's SSLRequest did not reach its server")
	require.Equal(t, []byte("S"), response)
	require.Equal(t, sslRequest, <-seen)
	_, err = client.Write(clientTarget)
	require.NoError(t, err)
	got := make([]byte, len("fixed upstream: ")+len(clientTarget))
	_, err = io.ReadFull(client, got)
	require.NoError(t, err)
	require.Equal(t, "fixed upstream: "+string(clientTarget), string(got), "client bytes selected another target")
	require.NoError(t, <-serverDone)
}

func TestDatabaseRelayClosesPartialStartup(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	first := databaseTestPort(t)
	p := &proxy{}
	relays, err := p.startDatabaseRelays(context.Background(), "127.0.0.1", []provider.DatabaseRoute{
		{Port: first, Upstream: "10.10.0.1:5432"},
		{Port: occupied.Addr().(*net.TCPAddr).Port, Upstream: "10.10.0.2:5432"},
	}, nil)
	if relays != nil {
		defer relays.Close()
	}
	require.Error(t, err)
	require.Nil(t, relays)
	reopened, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(first)))
	require.NoError(t, err, "a failed startup left an earlier database listener open")
	require.NoError(t, reopened.Close())
}

func TestDatabaseRelayCloseEndsActiveConnections(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer upstream.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := upstream.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	var records bytes.Buffer
	p := databaseTestProxy(&records, upstream.Addr().String())
	port := databaseTestPort(t)
	relays, err := p.startDatabaseRelays(context.Background(), "127.0.0.1", []provider.DatabaseRoute{{Port: port, Upstream: databaseTestUpstream}}, nil)
	require.NoError(t, err)
	defer relays.Close()
	client, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	require.NoError(t, err)
	defer client.Close()
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never opened its fixed upstream")
	}
	defer server.Close()
	closed := make(chan struct{})
	go func() { relays.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		// Release both peers even in the mutation so a failed assertion does
		// not leave the test process hanging in cleanup.
		_ = client.Close()
		_ = server.Close()
		<-closed
		t.Fatal("closing the relay left active database connections alive")
	}
}

func TestDatabaseRelayRefusesMetadataBeforeListening(t *testing.T) {
	p := &proxy{}
	port := databaseTestPort(t)
	relays, err := p.startDatabaseRelays(context.Background(), "127.0.0.1", []provider.DatabaseRoute{{Port: port, Upstream: "169.254.169.254:80"}}, nil)
	if relays != nil {
		defer relays.Close()
	}
	require.Error(t, err)
	require.Nil(t, relays)
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	require.NoError(t, err, "a refused metadata route nevertheless opened its listener")
	require.NoError(t, listener.Close())
}

package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// databaseRelays are fixed byte relays, not a protocol that accepts a target.
// PostgreSQL begins TLS with an SSLRequest before its ClientHello. Copying
// bytes to the orchestrator's destination preserves that negotiation without
// inspecting credentials, changing certificates or depending on TLS SNI.
type databaseRelays struct {
	listeners []net.Listener
	cancel    context.CancelFunc
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func (r *databaseRelays) closeListeners() {
	r.closeOnce.Do(func() {
		for _, listener := range r.listeners {
			_ = listener.Close()
		}
	})
}

func (r *databaseRelays) Close() {
	r.cancel()
	r.closeListeners()
	r.wg.Wait()
}

func (p *proxy) startDatabaseRelays(
	parent context.Context,
	listenHost string,
	routes []provider.DatabaseRoute,
	report func(error),
) (*databaseRelays, error) {
	pinned, err := provider.ResolveDatabaseRoutes(parent, routes)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	relays := &databaseRelays{cancel: cancel}
	// Bind every port before accepting any connection or announcing readiness.
	// A partial bind failure closes the listeners already created.
	for _, route := range pinned {
		listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, strconv.Itoa(route.Port)))
		if err != nil {
			relays.Close()
			return nil, fmt.Errorf("binding database relay port %d: %w", route.Port, err)
		}
		relays.listeners = append(relays.listeners, listener)
	}
	context.AfterFunc(ctx, relays.closeListeners)
	for i, listener := range relays.listeners {
		route := pinned[i]
		relays.wg.Add(1)
		go func() {
			defer relays.wg.Done()
			for {
				client, err := listener.Accept()
				if err != nil {
					if ctx.Err() == nil && report != nil {
						report(fmt.Errorf("database relay port %d stopped: %w", route.Port, err))
					}
					return
				}
				relays.wg.Add(1)
				go func() {
					defer relays.wg.Done()
					p.relayDatabase(ctx, client, route)
				}()
			}
		}()
	}
	return relays, nil
}

func (p *proxy) relayDatabase(ctx context.Context, client net.Conn, route provider.DatabaseRoute) {
	defer func() { _ = client.Close() }()
	started := time.Now()
	host, port, _ := net.SplitHostPort(route.Upstream)
	upstreamPort, _ := strconv.Atoi(port)
	rec := record{
		Event: "decision", Method: "CONNECT", Host: host, Port: upstreamPort,
		Mode: "allow", Allowed: true, Via: "transparent",
		HostOnly: true,
		Reason:   "the orchestrator selected this fixed database endpoint",
	}
	defer func() {
		rec.Duration = time.Since(started).String()
		p.emit(rec)
	}()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	dial := p.databaseDial
	if dial == nil {
		dial = func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}
	}
	upstream, err := dial(dialCtx, route.Upstream)
	cancel()
	if err != nil {
		rec.Error = err.Error()
		return
	}
	defer func() { _ = upstream.Close() }()
	stop := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stop()
	rec.Bytes = pipe(client, upstream)
}

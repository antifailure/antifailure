package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// gRPC through the sidecar.
//
// The sidecar terminated TLS with no ALPN and read HTTP/1.1 out of whatever it
// found, so a gRPC client could not talk to it at all: grpc-go requires h2 to
// be selected during the handshake and closes the connection when nothing is,
// with "missing selected ALPN property". Every one of these tests fails on the
// tree before this change, and the first of them fails in the handshake rather
// than anywhere a policy could be blamed.
//
// The pairs matter as much as the tests. A gRPC call that fails looks the same
// whether the policy refused it, the fixture never started, or the protocol
// was never negotiated, so every refusal here is paired with a control that
// must succeed through the same sidecar against the same fixture, differing
// only in the thing the policy is supposed to notice.

// grpcName is the host the environment's rules are written about.
//
// A name rather than an address, because a rule naming an address takes a
// different branch in the policy and the address branch is not what an
// application resolving a vendor endpoint exercises.
const grpcName = "grpc.example.test"

// echoOrigin is a real gRPC server standing in for the origin.
//
// It counts what reached it, which is the assertion in the refusal tests: a
// refusal that still forwarded the call is the failure that matters and it is
// invisible from the client's side.
type echoOrigin struct {
	grpc_testing.UnimplementedTestServiceServer
	mu       sync.Mutex
	calls    int
	authSeen []string
}

func (o *echoOrigin) UnaryCall(
	ctx context.Context, req *grpc_testing.SimpleRequest,
) (*grpc_testing.SimpleResponse, error) {
	o.mu.Lock()
	o.calls++
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		o.authSeen = append(o.authSeen, strings.Join(md.Get("authorization"), ","))
	}
	o.mu.Unlock()
	return &grpc_testing.SimpleResponse{Payload: req.GetPayload()}, nil
}

// FullDuplexCall echoes each message as it arrives.
//
// It answers per message rather than after the client half closes, which is
// what makes the bidirectional test able to fail: a proxy that read the whole
// request body before forwarding it would wait for a half close the client is
// not going to send until it has an answer, and the call would deadlock rather
// than return the wrong bytes.
func (o *echoOrigin) FullDuplexCall(
	stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest,
		grpc_testing.StreamingOutputCallResponse],
) error {
	for {
		in, err := stream.Recv()
		if err != nil {
			// io.EOF is the client half closing, which is the end of a
			// healthy call.
			return nil
		}
		o.mu.Lock()
		o.calls++
		o.mu.Unlock()
		if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{
			Payload: in.GetPayload(),
		}); err != nil {
			return err
		}
	}
}

func (o *echoOrigin) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func (o *echoOrigin) lastAuth() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.authSeen) == 0 {
		return ""
	}
	return o.authSeen[len(o.authSeen)-1]
}

// startGRPCOrigin runs a TLS gRPC server for a name and returns its address
// and the roots that verify it.
func startGRPCOrigin(t *testing.T, name string) (*echoOrigin, string, *x509.CertPool) {
	t.Helper()
	certPEM, keyPEM, err := GenerateAuthority("grpc-origin-fixture", time.Now())
	require.NoError(t, err)
	ca, err := newCertAuthority(certPEM, keyPEM)
	require.NoError(t, err)
	leaf, err := ca.leaf(name)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(certPEM)))

	o := &echoOrigin{}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{*leaf},
		MinVersion:   tls.VersionTLS12,
	})))
	grpc_testing.RegisterTestServiceServer(srv, o)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return o, ln.Addr().String(), roots
}

// startPlainGRPCOrigin runs a cleartext gRPC server, which is what every
// emulator's documented setup speaks.
func startPlainGRPCOrigin(t *testing.T) (*echoOrigin, string) {
	t.Helper()
	o := &echoOrigin{}
	srv := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(srv, o)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return o, ln.Addr().String()
}

// pointSidecarAt wires a sidecar so that the name a rule is written about
// reaches a loopback fixture.
//
// The port rewrite in the dialer is the one piece of scaffolding here and it
// is deliberately after the guard rather than around it: dialGuarded resolves
// the name and refuses the address, and the port it is handed changes nothing
// about that decision. The fixture cannot bind 443, so something has to move
// the port, and moving it inside the guard would have removed the guard from
// the test.
func pointSidecarAt(t *testing.T, s *sidecar, upstream string, roots *x509.CertPool) {
	t.Helper()
	_, fixturePort, err := net.SplitHostPort(upstream)
	require.NoError(t, err)

	// Loopback is where the fixture is, and the guard refuses loopback unless
	// it is the environment's own network or a rule names it.
	s.destinations = newDestinations(nil, "127.0.0.0/8", false)
	s.resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.IPv4(127, 0, 0, 1)}, nil
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, splitErr
		}
		if port == "443" || port == "80" {
			port = fixturePort
		}
		return s.dialGuarded(ctx, network, net.JoinHostPort(host, port))
	}
	for _, tr := range []*http.Transport{s.transport, s.transportH2, s.transportH2C} {
		tr.DialContext = dial
		if roots != nil {
			tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		}
	}
	t.Cleanup(func() {
		s.transport.CloseIdleConnections()
		s.transportH2.CloseIdleConnections()
		s.transportH2C.CloseIdleConnections()
	})
}

// environmentCA gives the sidecar an authority and returns the roots a service
// inside the environment would trust.
func environmentCA(t *testing.T, s *sidecar) *x509.CertPool {
	t.Helper()
	certPEM, keyPEM, err := GenerateAuthority("grpc-lane", time.Now())
	require.NoError(t, err)
	ca, err := newCertAuthority(certPEM, keyPEM)
	require.NoError(t, err)
	s.ca = ca
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(certPEM)))
	return roots
}

// frontDoor runs one of the sidecar's transparent listeners on a real socket
// and returns its address.
func frontDoor(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go handle(conn)
		}
	}()
	return ln.Addr().String()
}

// grpcClient dials the sidecar the way an application's client does: at the
// vendor's own name, over TLS, with no endpoint override and no knowledge that
// anything is in the way.
func grpcClient(t *testing.T, front string, roots *x509.CertPool) grpc_testing.TestServiceClient {
	t.Helper()
	cc, err := grpc.NewClient(
		// passthrough so the name is handed to the dialer rather than looked
		// up. The name resolving to the sidecar is what the environment's own
		// resolver does, and a test resolver is not what is under test here.
		"passthrough:///"+grpcName+":443",
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			ServerName: grpcName, RootCAs: roots, MinVersion: tls.VersionTLS12,
		})),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", front)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	return grpc_testing.NewTestServiceClient(cc)
}

// inspectedEgress is a policy that puts this host on the inspected path.
//
// Paths are what make the sidecar terminate the connection rather than tunnel
// it, and a rule with no path is decided on the host alone and never reads
// inside, so a policy without one would test the tunnel and not the protocol.
func inspectedEgress(mode schema.Mode) *schema.Egress {
	return &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: grpcName, Mode: mode, Paths: []string{"/"}}},
	}
}

func payload(text string) *grpc_testing.Payload {
	return &grpc_testing.Payload{Body: []byte(text)}
}

// The reproduction, and the smallest statement of the finding.
//
// An unmodified application calling a gRPC service through the sidecar. Before
// the ALPN change this failed in the TLS handshake with "missing selected ALPN
// property", which is grpc-go refusing to speak to a server that selected no
// protocol, so nothing the policy did or did not do was ever reached.
func TestGRPC_AUnaryCallReachesTheOriginThroughTheSidecar(t *testing.T) {
	o, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: payload("hello")})
	require.NoError(t, err, "a gRPC client cannot reach anything through this sidecar")
	require.Equal(t, "hello", string(resp.GetPayload().GetBody()))
	require.Equal(t, 1, o.count(), "the call never reached the origin")

	// The policy read inside the connection, which is the other half of the
	// claim: an HTTP/2 request that reached the network without a decision
	// would be a hole rather than a feature.
	rec := s.waitFor(t, func(r record) bool { return r.Host == grpcName && r.Path != "" })
	require.True(t, rec.Allowed)
	require.Equal(t, "inspect", rec.Via)
	require.Equal(t, "POST", rec.Method,
		"gRPC is POST, and a decision log that cannot say so cannot enforce a method rule")
	require.Equal(t, "/grpc.testing.TestService/UnaryCall", rec.Path,
		"the method name is the path, so a path rule is how a gRPC method is named")
	require.False(t, rec.HostOnly, "an inspected HTTP/2 request is not a host only decision")
}

// The half that a buffer cannot fake.
//
// gRPC streams are long lived and bidirectional. A proxy that read the request
// body to its end before forwarding it would hang here rather than return the
// wrong answer, because the server does not reply until it has a message and
// the client does not half close until it has a reply.
func TestGRPC_ABidirectionalStreamCarriesMessagesInBothDirections(t *testing.T) {
	o, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := client.FullDuplexCall(ctx)
	require.NoError(t, err)

	for i := range 3 {
		sent := fmt.Sprintf("message %d", i)
		require.NoError(t, stream.Send(&grpc_testing.StreamingOutputCallRequest{
			Payload: payload(sent),
		}))
		// Received before the next is sent. That ordering is the assertion: a
		// proxy that waits for the whole request before forwarding any of it
		// never gets past this line.
		got, recvErr := stream.Recv()
		require.NoError(t, recvErr, "the stream stalled after %d messages", i)
		require.Equal(t, sent, string(got.GetPayload().GetBody()))
	}
	require.NoError(t, stream.CloseSend())
	require.Equal(t, 3, o.count())
}

// A gRPC error is carried in trailers, and a trailers only response is carried
// in headers.
//
// An unimplemented method answers with a single headers frame holding
// grpc-status and closing the stream. Relayed as headers it would leave the
// stream with no trailers at all, and grpc-go reports that as an internal
// protocol failure rather than as the Unimplemented the server sent, so a
// developer would be sent to debug the network for an ordinary application
// error.
func TestGRPC_AStatusFromTheOriginArrivesAsAStatusAndNotAProtocolError(t *testing.T) {
	_, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// EmptyCall is on the service and the fixture does not implement it, so
	// the server answers with a status and nothing else.
	_, err := client.EmptyCall(ctx, &grpc_testing.Empty{})
	require.Error(t, err)
	require.Equal(t, codes.Unimplemented, status.Code(err),
		"the origin's own status did not survive the sidecar")
}

// The containment claim, over the protocol that could not reach the policy.
func TestGRPC_ABlockedHostIsRefusedOverGRPC(t *testing.T) {
	o, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			// Named with a path so the connection is inspected, and blocked so
			// that what is being tested is the decision rather than the route.
			{Host: grpcName, Mode: schema.ModeBlock, Paths: []string{"/"}},
		},
	})
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: payload("hello")})
	require.Error(t, err, "a blocked host answered a gRPC call")
	require.Zero(t, o.count(), "the call reached the origin through a blocked rule")

	rec := s.waitFor(t, func(r record) bool { return r.Host == grpcName && r.Path != "" })
	require.False(t, rec.Allowed)
	require.Equal(t, "inspect", rec.Via)
	require.Equal(t, string(schema.ModeBlock), rec.Mode)
}

// The control for the test above.
//
// Without it, a sidecar that refused every gRPC call for any reason at all
// would pass, which is exactly the shape of the defect this lane is closing.
func TestGRPC_AnAllowedHostAnswersTheSameCall(t *testing.T) {
	o, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: payload("hello")})
	require.NoError(t, err)
	require.Equal(t, 1, o.count())
}

// The live credential tripwire, over gRPC.
//
// A credential travels in gRPC metadata, which is an HTTP/2 header, so the
// same scan applies. It has to, because an environment holds a copy of
// production data and a key that can act on production must not leave it
// whichever protocol the application chose.
func TestGRPC_ALiveCredentialIsRefusedOverGRPC(t *testing.T) {
	o, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	live := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+liveKey())
	_, err := client.UnaryCall(live, &grpc_testing.SimpleRequest{Payload: payload("charge")})
	require.Error(t, err, "a live credential reached the origin over gRPC")
	require.Zero(t, o.count(), "the call carrying a live credential reached the origin")

	rec := s.waitFor(t, func(r record) bool { return strings.Contains(r.Reason, "live credential") })
	require.False(t, rec.Allowed)
	require.Equal(t, "inspect", rec.Via)
	require.NotContains(t, rec.Reason, "A1b2C3d4", "the refusal never echoes the key")

	// The control. Same client, same rule, same host: only the metadata
	// differs, so the refusal above is the tripwire and not a broken fixture.
	ok := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer whatever-the-app-had")
	_, err = client.UnaryCall(ok, &grpc_testing.SimpleRequest{Payload: payload("charge")})
	require.NoError(t, err)
	require.Equal(t, 1, o.count(), "the control call did not reach the origin")
}

// Sandbox mode over gRPC.
//
// The substitution is the whole difference between sandbox mode and asking
// somebody to configure a sandbox key correctly, and it has to happen on every
// protocol or it happens on whichever one the application did not use.
func TestGRPC_ASandboxCredentialIsSubstitutedOverGRPC(t *testing.T) {
	o, upstream, originRoots := startGRPCOrigin(t, grpcName)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{{
			Host: grpcName, Mode: schema.ModeSandbox,
			Credential: "TEST_KEY", Paths: []string{"/"},
		}},
	})
	const substituted = "sk" + "_" + "test" + "_" + "substituted00000000"
	s.credentials["TEST_KEY"] = substituted
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	client := grpcClient(t, front, envRoots)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sent := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer the-applications-own-key")
	_, err := client.UnaryCall(sent, &grpc_testing.SimpleRequest{Payload: payload("charge")})
	require.NoError(t, err)
	require.Equal(t, 1, o.count())
	require.Equal(t, "Bearer "+substituted, o.lastAuth(),
		"the application's own credential was forwarded over gRPC rather than replaced")
}

// Cleartext HTTP/2, which is what an insecure gRPC client speaks.
//
// Every emulator's documented setup uses insecure credentials, so this is the
// shape a local Pub/Sub or Spanner client arrives in. It reached the plain
// transparent listener, whose HTTP/1.1 reader turned the connection preface
// into a request with the method PRI and no Host header and refused it for
// naming no host.
func TestGRPC_AnInsecureClientOverCleartextHTTP2IsCarried(t *testing.T) {
	o, upstream := startPlainGRPCOrigin(t)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: grpcName, Mode: schema.ModeAllow, Paths: []string{"/"}},
		},
	})
	pointSidecarAt(t, s, upstream, nil)
	front := frontDoor(t, s.serveTransparentHTTP)

	cc, err := grpc.NewClient("passthrough:///"+grpcName+":80",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", front)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := grpc_testing.NewTestServiceClient(cc).UnaryCall(ctx,
		&grpc_testing.SimpleRequest{Payload: payload("cleartext")})
	require.NoError(t, err, "an insecure gRPC client cannot reach anything through this sidecar")
	require.Equal(t, "cleartext", string(resp.GetPayload().GetBody()))
	require.Equal(t, 1, o.count())

	rec := s.waitFor(t, func(r record) bool { return r.Host == grpcName && r.Path != "" })
	require.True(t, rec.Allowed)
	require.Equal(t, "transparent", rec.Via)
	require.Equal(t, "/grpc.testing.TestService/UnaryCall", rec.Path)
}

// The cleartext control.
//
// A blocked rule on the same listener must still refuse, or the h2c reader is
// a way around the policy rather than a protocol the policy can see.
func TestGRPC_ACleartextCallToABlockedHostIsRefused(t *testing.T) {
	o, upstream := startPlainGRPCOrigin(t)
	s := newSidecar(t, &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: grpcName, Mode: schema.ModeBlock, Paths: []string{"/"}},
		},
	})
	pointSidecarAt(t, s, upstream, nil)
	front := frontDoor(t, s.serveTransparentHTTP)

	cc, err := grpc.NewClient("passthrough:///"+grpcName+":80",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", front)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = grpc_testing.NewTestServiceClient(cc).UnaryCall(ctx,
		&grpc_testing.SimpleRequest{Payload: payload("cleartext")})
	require.Error(t, err, "a blocked host answered a cleartext gRPC call")
	require.Zero(t, o.count())

	rec := s.waitFor(t, func(r record) bool { return r.Host == grpcName && r.Path != "" })
	require.False(t, rec.Allowed)
	require.Equal(t, "transparent", rec.Via)
}

// The handshake itself, stated without gRPC in the way.
//
// This is the one line of the finding: a client that offers h2 must be told h2
// was selected. It is kept separate from the calls above because when they all
// fail together this says which half broke.
func TestGRPC_TheTerminatorSelectsH2WhenTheClientOffersIt(t *testing.T) {
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	front := frontDoor(t, s.serveTransparentTLS)

	conn, err := net.Dial("tcp", front)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := tls.Client(conn, &tls.Config{
		ServerName: grpcName, RootCAs: envRoots, MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2"},
	})
	require.NoError(t, client.Handshake())
	require.Equal(t, "h2", client.ConnectionState().NegotiatedProtocol,
		"the terminator selected no protocol, which every gRPC client refuses")
}

// An HTTP/1.1 client offered the same connection must still get HTTP/1.1.
//
// Offering h2 first would be a downgrade in the other direction if it were
// ever selected for a client that did not ask, and the whole HTTP/1.1 suite
// runs through this path.
func TestGRPC_AnHTTP11ClientStillGetsHTTP11(t *testing.T) {
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	front := frontDoor(t, s.serveTransparentTLS)

	conn, err := net.Dial("tcp", front)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := tls.Client(conn, &tls.Config{
		ServerName: grpcName, RootCAs: envRoots, MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	})
	require.NoError(t, client.Handshake())
	require.Equal(t, "http/1.1", client.ConnectionState().NegotiatedProtocol)
}

// startH2Origin runs an ordinary HTTPS server that offers h2, and reports what
// it saw, so the forwarding half can be read from the answer.
func startH2Origin(t *testing.T, name string) (string, *x509.CertPool) {
	t.Helper()
	certPEM, keyPEM, err := GenerateAuthority("h2-origin-fixture", time.Now())
	require.NoError(t, err)
	ca, err := newCertAuthority(certPEM, keyPEM)
	require.NoError(t, err)
	leaf, err := ca.leaf(name)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(certPEM)))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "the origin was asked HTTP/%d %s %s", r.ProtoMajor, r.Method, r.URL.Path)
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{*leaf},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
		},
		ReadHeaderTimeout: 20 * time.Second,
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), roots
}

// An ordinary HTTPS client that speaks HTTP/2, which is most of them.
//
// gRPC is not the only thing that negotiates h2 once the terminator offers it.
// Go's own client does, and so does every runtime with a modern HTTP stack, so
// offering h2 moved the product's ordinary inspected traffic onto this reader
// rather than only its gRPC traffic. That is worth its own test because the
// whole HTTP/1.1 suite in this package kept passing while this path was
// broken: those tests write a request onto a socket by hand and negotiate
// nothing, so not one of them ever reached the code that was wrong.
//
// Three claims, and they fail separately. The answer came back at all and it
// came back framed as HTTP/2, which is the reading half. The origin was asked
// in HTTP/2, which is the forwarding half, and an HTTP/1.1 forward here is
// what drops the trailers a gRPC status travels in. And the request was
// decided by name and path, which is the policy still being applied to a
// protocol it did not previously see.
func TestGRPC_AnOrdinaryHTTP2ClientIsCarriedAndPoliced(t *testing.T) {
	upstream, originRoots := startH2Origin(t, grpcName)
	s := newSidecar(t, inspectedEgress(schema.ModeAllow))
	envRoots := environmentCA(t, s)
	pointSidecarAt(t, s, upstream, originRoots)
	front := frontDoor(t, s.serveTransparentTLS)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: envRoots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", front)
		},
		// A transport carrying its own dialer has HTTP/2 off unless it is
		// asked for, and a client that never offers h2 would take the
		// HTTP/1.1 path and prove nothing about this one.
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr, Timeout: 20 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+grpcName+"/things", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err, "an HTTP/2 client cannot reach anything through this sidecar")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, resp.ProtoMajor,
		"the answer was not framed as HTTP/2, so the connection was read as HTTP/1.1")
	require.Equal(t, "the origin was asked HTTP/2 GET /things", string(body),
		"the sidecar did not forward this request over HTTP/2")

	rec := s.waitFor(t, func(r record) bool { return r.Host == grpcName && r.Path != "" })
	require.True(t, rec.Allowed)
	require.Equal(t, "inspect", rec.Via)
	require.Equal(t, http.MethodGet, rec.Method,
		"the sidecar decided about the connection preface rather than about the request")
	require.Equal(t, "/things", rec.Path)
}
